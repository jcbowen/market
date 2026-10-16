import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  assertOk,
  type ApiResponse,
  type ClientLoginInfo,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 25：后台订单流转 + 用户拉黑管理
 * 覆盖：配送单 ship（2→4 填快递单号）→ complete（4→5）；自提单 complete（2→5）
 *       → 完成后重复流转拒绝；后台取消已支付配送单（2→6 回补库存）
 *       → 会员拉黑 status=0 → C 端请求被拦（30 秒缓存强制回源）→ 解封恢复
 * 说明：配送单需收货地址；自建商品保证库存断言可控
 */

const PRICE = 6.6;

interface Sku {
  id: number;
  stock: number;
  [k: string]: unknown;
}

interface OrderDetail {
  id: number;
  status: number;
  delivery_type: number;
  express_company?: string;
  express_no?: string;
  cancel_reason?: string;
  items: { sku_id: number; quantity: number }[];
  [k: string]: unknown;
}

interface MemberRow {
  id: number;
  phone: string;
  status: number;
  [k: string]: unknown;
}

/**
 * 创建新 C 端用户并返回携带已绑定社区的 token
 * - 函数名: newClientUser
 * - 参数: playwright — Playwright 实例
 * - 返回: Promise<ClientLoginInfo>
 * - 异常: 登录或绑社区失败时抛出
 */
async function newClientUser(playwright: Playwright): Promise<ClientLoginInfo> {
  const phone = uniquePhone();
  await clientLogin(playwright.request, phone);
  return clientLogin(playwright.request, phone);
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let communityId = 0;
let categoryId = 0;
let productId = 0;
let skuId = 0;

test.beforeAll(async ({ playwright }) => {
  const buyer = await newClientUser(playwright);
  communityId = buyer.user.community_id;
  const admin = await newAdminContext(playwright, undefined, undefined, communityId);
  adminApi = admin.api;

  try {
    const catBody = assertOk(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('流转测试分类') } })).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    );
    categoryId = catBody.id;
    const prodBody = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/products', {
          data: {
            category_id: categoryId,
            name: autoName('流转测试商品'),
            unit: '份',
            is_multi_spec: false,
            sku_items: [{ price: PRICE, stock: 30, safety_stock: 3 }],
          },
        })
      ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
      '创建商品',
    );
    productId = prodBody.product?.id ?? prodBody.id ?? 0;
    const on = (await (
      await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })
    ).json()) as ApiResponse;
    if (on.code !== 0) throw new Error(`上架失败 code=${on.code}`);
    const detail = assertOk(
      (await (await adminApi.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<{ skus?: Sku[] }>,
      '商品详情',
    );
    skuId = detail.skus?.[0]?.id ?? 0;
  } catch (e) {
    console.warn(`[25] 造数失败，用例将跳过：${e instanceof Error ? e.message : e}`);
    productId = 0;
  }
});

test.afterAll(async () => {
  try {
    if (productId) await adminApi.delete(`/api/v1/admin/products/${productId}`);
    if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

/**
 * 查询 SKU 当前库存
 * - 函数名: getStock
 * - 参数: 无
 * - 返回: Promise<number> 当前库存
 * - 异常: 商品查询失败时抛出
 */
async function getStock(): Promise<number> {
  const detail = assertOk(
    (await (await adminApi.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<{ skus?: Sku[] }>,
    '商品详情',
  );
  return detail.skus?.find((s) => s.id === skuId)?.stock ?? 0;
}

/**
 * C 端下单并现金支付（共用步骤）
 * - 函数名: createPaidOrder
 * - 参数: playwright — Playwright 实例；token — 买家 token；deliveryType — 1 自提 2 配送
 * - 返回: Promise<number> 订单 ID
 * - 异常: 下单或支付失败时抛出 Error
 */
async function createPaidOrder(playwright: Playwright, token: string, deliveryType: number): Promise<number> {
  const buyerApi = await newAuthedRequest(playwright, token);
  try {
    let addressId = 0;
    if (deliveryType === 2) {
      const addr = assertOk(
        (await (
          await buyerApi.post('/api/v1/user/addresses', {
            data: {
              contact_name: 'AUTO-TEST 收货人',
              contact_phone: '13800000002',
              province: '广东省',
              city: '深圳市',
              district: '南山区',
              detail: `流转路 ${Date.now()} 号`,
              is_default: true,
            },
          })
        ).json()) as ApiResponse<{ id: number }>,
        '创建收货地址',
      );
      addressId = addr.id;
    }
    const orderBody = assertOk(
      (await (
        await buyerApi.post('/api/v1/orders', {
          data: {
            delivery_type: deliveryType,
            address_id: addressId,
            remark: 'AUTO-TEST 订单流转',
            items: [{ sku_id: skuId, quantity: 3 }], // 19.8 元，满足社区 15 元起送
          },
        })
      ).json()) as ApiResponse<{ id: number }>,
      'C 端下单',
    );
    const payBody = (await (
      await buyerApi.post('/api/v1/payments', { data: { order_id: orderBody.id, channel: 'cash' } })
    ).json()) as ApiResponse;
    expect(payBody.code, `现金支付失败: ${payBody.message}`).toBe(0);
    return orderBody.id;
  } finally {
    await buyerApi.dispose();
  }
}

test.describe.serial('配送单发货与完成', () => {
  let deliveryOrderId = 0;

  test('1. 配送单支付后后台发货（填快递公司与单号）', async ({ playwright }) => {
    test.skip(!productId, '环境造数失败');
    const buyer = await newClientUser(playwright);
    deliveryOrderId = await createPaidOrder(playwright, buyer.access_token, 2);
    assertOk(
      (await (
        await adminApi.post(`/api/v1/admin/orders/${deliveryOrderId}/ship`, {
          data: { express_company: '顺丰速运', express_no: `SF${Date.now()}`, remark: 'AUTO-TEST 发货' },
        })
      ).json()) as ApiResponse,
      '后台发货',
    );
    const detail = assertOk(
      (await (await adminApi.get(`/api/v1/admin/orders/${deliveryOrderId}`)).json()) as ApiResponse<OrderDetail>,
      '后台订单详情',
    );
    expect(detail.status).toBe(4); // 配送中
    expect(detail.express_company).toBe('顺丰速运');
    expect(detail.express_no).toBeTruthy();
  });

  test('2. 配送单完成（4→5 已完成）', async () => {
    test.skip(deliveryOrderId <= 0, '配送单未创建');
    assertOk(
      (await (await adminApi.put(`/api/v1/admin/orders/${deliveryOrderId}/complete`)).json()) as ApiResponse,
      '后台完成订单',
    );
    const detail = assertOk(
      (await (await adminApi.get(`/api/v1/admin/orders/${deliveryOrderId}`)).json()) as ApiResponse<OrderDetail>,
      '后台订单详情',
    );
    expect(detail.status).toBe(5);
  });

  test('3. 已完成订单重复发货/重复完成被拒绝', async () => {
    test.skip(deliveryOrderId <= 0, '配送单未创建');
    const ship = (await (
      await adminApi.post(`/api/v1/admin/orders/${deliveryOrderId}/ship`, {
        data: { express_company: '顺丰速运', express_no: 'SF2' },
      })
    ).json()) as ApiResponse;
    expect(ship.code, '已完成订单不可再发货').not.toBe(0);
    const complete = (await (
      await adminApi.put(`/api/v1/admin/orders/${deliveryOrderId}/complete`)
    ).json()) as ApiResponse;
    expect(complete.code, '已完成订单不可再完成').not.toBe(0);
  });
});

test.describe.serial('自提单完成与后台取消', () => {
  test('4. 自提单支付后直接完成（2→5）', async ({ playwright }) => {
    test.skip(!productId, '环境造数失败');
    const buyer = await newClientUser(playwright);
    const orderId = await createPaidOrder(playwright, buyer.access_token, 1);
    assertOk(
      (await (await adminApi.put(`/api/v1/admin/orders/${orderId}/complete`)).json()) as ApiResponse,
      '自提单完成',
    );
    const detail = assertOk(
      (await (await adminApi.get(`/api/v1/admin/orders/${orderId}`)).json()) as ApiResponse<OrderDetail>,
      '后台订单详情',
    );
    expect(detail.status).toBe(5);
  });

  test('5. 后台取消已支付配送单（2→6）并回补库存', async ({ playwright }) => {
    test.skip(!productId, '环境造数失败');
    const buyer = await newClientUser(playwright);
    const stockBefore = await getStock();
    const orderId = await createPaidOrder(playwright, buyer.access_token, 2);
    expect(await getStock()).toBe(stockBefore - 3);

    assertOk(
      (await (
        await adminApi.post(`/api/v1/admin/orders/${orderId}/cancel`, { data: { reason: 'AUTO-TEST 配送单取消' } })
      ).json()) as ApiResponse,
      '后台取消配送单',
    );
    const detail = assertOk(
      (await (await adminApi.get(`/api/v1/admin/orders/${orderId}`)).json()) as ApiResponse<OrderDetail>,
      '后台订单详情',
    );
    expect(detail.status).toBe(6);
    expect(detail.cancel_reason).toBe('AUTO-TEST 配送单取消');
    expect(await getStock()).toBe(stockBefore); // 回补 3
  });
});

test.describe.serial('会员拉黑与解封', () => {
  const blackPhone = uniquePhone();
  let blackUserId = 0;
  let blackToken = '';

  test('6. 注册会员并拉黑（status=0）', async ({ playwright }) => {
    const reg = await clientLogin(playwright.request, blackPhone);
    blackToken = reg.access_token;
    blackUserId = reg.user.id;
    expect(blackUserId, '登录响应应含会员 ID').toBeGreaterThan(0);

    assertOk(
      (await (
        await adminApi.put(`/api/v1/admin/users/${blackUserId}/status`, { data: { status: 0 } })
      ).json()) as ApiResponse,
      '拉黑会员',
    );
    const list2 = assertOk(
      (await (
        await adminApi.get('/api/v1/admin/users', { params: { keyword: blackPhone, page: 1, page_size: 5 } })
      ).json()) as ApiResponse<{ list?: MemberRow[] }>,
      '回读会员',
    );
    const row = (list2.list ?? []).find((u) => u.id === blackUserId);
    if (row) {
      expect(row.status).toBe(0);
    }
    // 注：会员列表按社区语境过滤，跨社区时可能查不到，拉黑状态以 C 端拦截结果为准（用例 7）
  });

  test('7. 拉黑后 C 端登录态请求被拦截', async ({ playwright }) => {
    test.skip(blackUserId <= 0, '会员未拉黑');
    const buyerApi = await newAuthedRequest(playwright, blackToken);
    try {
      const resp = await buyerApi.get('/api/v1/user/coupons');
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, '拉黑会员请求应被拦截').not.toBe(0);
      expect(body.message).toContain('系统错误');
    } finally {
      await buyerApi.dispose();
    }
  });

  test('8. 解封后 C 端请求恢复', async ({ playwright }) => {
    test.skip(blackUserId <= 0, '会员未拉黑');
    assertOk(
      (await (
        await adminApi.put(`/api/v1/admin/users/${blackUserId}/status`, { data: { status: 1 } })
      ).json()) as ApiResponse,
      '解封会员',
    );
    const buyerApi = await newAuthedRequest(playwright, blackToken);
    try {
      const resp = await buyerApi.get('/api/v1/user/coupons');
      const r = (await resp.json()) as ApiResponse;
      expect(r.code, '解封后请求应恢复').toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });
});
