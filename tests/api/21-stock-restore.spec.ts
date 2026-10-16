import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  assertOk,
  asList,
  type ApiResponse,
  type ClientLoginInfo,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 21：库存回补链路
 * 覆盖：C 端取消待支付订单（库存回补 + 占用券复位）；
 *       后台取消已支付现金订单（cash=pay_type 3 货到付款口径，2/3 状态可取消）；
 *       取消后重复取消拒绝（状态机防重，间接验证回补幂等槽位不重复释放）
 * 说明：自建商品精确控制库存基准；每步前后核对 admin 商品详情 stock
 */

const PRICE = 4.5;
const QTY = 2;

interface Sku {
  id: number;
  stock: number;
  [k: string]: unknown;
}

interface OrderDetail {
  id: number;
  status: number;
  items: { sku_id: number; quantity: number }[];
  [k: string]: unknown;
}

interface UserCoupon {
  id: number;
  coupon_id: number;
  status: number; // 1未使用 2已使用 3已过期
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
let buyerToken = '';
let categoryId = 0;
let productId = 0;
let skuId = 0;
let couponId = 0;

test.beforeAll(async ({ playwright }) => {
  const buyer = await newClientUser(playwright);
  buyerToken = buyer.access_token;
  communityId = buyer.user.community_id;
  const admin = await newAdminContext(playwright, undefined, undefined, communityId);
  adminApi = admin.api;

  try {
    const catBody = assertOk(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('库存回补分类') } })).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    );
    categoryId = catBody.id;
    const prodBody = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/products', {
          data: {
            category_id: categoryId,
            name: autoName('库存回补商品'),
            unit: '份',
            is_multi_spec: false,
            sku_items: [{ price: PRICE, stock: 20, safety_stock: 2 }],
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

    // 建无门槛券供取消复位验证
    const coupon = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/coupons', {
          data: {
            name: autoName('回补验证券'),
            type: 1,
            threshold: 0,
            discount_value: 0.5,
            total_count: 10,
            per_user_limit: 1,
            expire_type: 1,
            valid_days: 7,
            publish_status: 1,
          },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建券',
    );
    couponId = coupon.id;
  } catch (e) {
    console.warn(`[21] 造数失败，用例将跳过：${e instanceof Error ? e.message : e}`);
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
 * C 端下单（共用步骤，不支付）
 * - 函数名: createOrder
 * - 参数: buyerApi — 买家已认证上下文；couponId — 券模板 ID（0 不带券）
 * - 返回: Promise<number> 订单 ID
 * - 异常: 下单业务失败时抛出 Error
 */
async function createOrder(buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>, withCoupon: number): Promise<number> {
  const orderBody = assertOk(
    (await (
      await buyerApi.post('/api/v1/orders', {
        data: {
          delivery_type: 1,
          address_id: 0,
          remark: 'AUTO-TEST 库存回补',
          coupon_id: withCoupon,
          items: [{ sku_id: skuId, quantity: QTY }],
        },
      })
    ).json()) as ApiResponse<{ id: number }>,
    'C 端下单',
  );
  return orderBody.id;
}

test.describe.serial('C 端取消待支付订单', () => {
  let orderId = 0;
  let stockAfterOrder = 0;

  test('1. 领券并带券下单（库存扣减、券转已使用）', async ({ playwright }) => {
    test.skip(!productId, '环境造数失败');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const recv = (await (
        await buyerApi.post('/api/v1/coupons/receive', { data: { coupon_id: couponId } })
      ).json()) as ApiResponse;
      expect(recv.code, `领券失败: ${recv.message}`).toBe(0);
      orderId = await createOrder(buyerApi, couponId);
      stockAfterOrder = await getStock();
      expect(stockAfterOrder).toBe(20 - QTY);
      const ucBody = assertOk(
        (await (await buyerApi.get('/api/v1/user/coupons', { params: { status: 2 } })).json()) as ApiResponse<UserCoupon[]>,
        '已使用券列表',
      );
      expect(asList<UserCoupon>(ucBody).some((c) => c.coupon_id === couponId), '券应已核销').toBe(true);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('2. C 端取消待支付订单 → 订单 6', async ({ playwright }) => {
    test.skip(orderId <= 0, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = (await (
        await buyerApi.put(`/api/v1/orders/${orderId}/cancel`, { data: { reason: 'AUTO-TEST 用户取消' } })
      ).json()) as ApiResponse;
      expect(body.code, `取消失败: ${body.message}`).toBe(0);
      const detail = assertOk(
        (await (await buyerApi.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<OrderDetail>,
        '订单详情',
      );
      expect(detail.status).toBe(6);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('3. 取消后库存回补（+QTY）', async () => {
    test.skip(orderId <= 0, '订单未创建');
    expect(await getStock()).toBe(stockAfterOrder + QTY);
  });

  test('4. 取消后占用券复位为未使用', async ({ playwright }) => {
    test.skip(orderId <= 0, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const ucBody = assertOk(
        (await (await buyerApi.get('/api/v1/user/coupons', { params: { status: 1 } })).json()) as ApiResponse<UserCoupon[]>,
        '未使用券列表',
      );
      expect(asList<UserCoupon>(ucBody).some((c) => c.coupon_id === couponId), '券应复位为未使用').toBe(true);
    } finally {
      await buyerApi.dispose();
    }
  });
});

test.describe.serial('后台取消已支付现金订单', () => {
  let orderId2 = 0;
  let stockAfterOrder2 = 0;

  test('5. C 端下单并现金支付（pay_type=3 货到付款口径）', async ({ playwright }) => {
    test.skip(!productId, '环境造数失败');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      orderId2 = await createOrder(buyerApi, 0);
      const payBody = (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: orderId2, channel: 'cash' } })
      ).json()) as ApiResponse;
      expect(payBody.code, `现金支付失败: ${payBody.message}`).toBe(0);
      stockAfterOrder2 = await getStock();
      expect(stockAfterOrder2).toBe(20 - QTY);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('6. 后台取消已支付订单 → 订单 6', async () => {
    test.skip(orderId2 <= 0, '订单未创建');
    const body = (await (
      await adminApi.post(`/api/v1/admin/orders/${orderId2}/cancel`, { data: { reason: 'AUTO-TEST 后台取消' } })
    ).json()) as ApiResponse;
    expect(body.code, `后台取消失败: ${body.message}`).toBe(0);
    const detail = assertOk(
      (await (await adminApi.get(`/api/v1/admin/orders/${orderId2}`)).json()) as ApiResponse<OrderDetail>,
      '后台订单详情',
    );
    expect(detail.status).toBe(6);
    expect(detail.cancel_reason).toBe('AUTO-TEST 后台取消');
  });

  test('7. 后台取消后库存回补（+QTY）', async () => {
    test.skip(orderId2 <= 0, '订单未创建');
    expect(await getStock()).toBe(stockAfterOrder2 + QTY);
  });

  test('8. 重复取消被拒绝（状态机防重）', async () => {
    test.skip(orderId2 <= 0, '订单未创建');
    const body = (await (
      await adminApi.post(`/api/v1/admin/orders/${orderId2}/cancel`, { data: { reason: 'AUTO-TEST 重复取消' } })
    ).json()) as ApiResponse;
    expect(body.code, '已取消订单应不可再取消').not.toBe(0);
    // 幂等槽位防重：库存不重复回补
    expect(await getStock()).toBe(stockAfterOrder2 + QTY);
  });
});
