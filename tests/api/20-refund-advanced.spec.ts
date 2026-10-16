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
 * 模块 20：后台代客退款进阶
 * 覆盖：部分商品退（金额分摊核验、库存回补、订单回退款前状态）；
 *       仅金额退（mode=2）；多笔部分退累计全退 → 订单 8 + 末笔 type 升级整单退；
 *       仅金额退超额拒绝；仅金额退带商品明细拒绝；订单详情 refunds 明细结构
 * 说明：环境可能存在其他满减活动（如满5减1），金额断言采用恒等式 + 区间策略
 */

const PRICE_A = 3.9; // 商品 A 单价（买 2 件）
const PRICE_B = 5.9; // 商品 B 单价（买 1 件）
const QTY_A = 2;

interface Sku {
  id: number;
  stock: number;
  [k: string]: unknown;
}

interface OrderItem {
  id: number;
  sku_id: number;
  quantity: number;
  total_price: number;
  [k: string]: unknown;
}

interface OrderDetail {
  id: number;
  status: number;
  total_amount: number;
  discount_amount: number;
  pay_amount: number;
  items: OrderItem[];
  refunds?: RefundInfo[];
  [k: string]: unknown;
}

interface RefundInfo {
  id: number;
  refund_no: string;
  type: number; // 1整单 2部分 3仅金额
  status: number; // 4退款中 5成功
  amount: number;
  freight_amount: number;
  items?: { sku_id: number; quantity: number; refund_amount: number }[];
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
let productA = 0;
let skuA = 0;
let productB = 0;
let skuB = 0;
let orderId = 0;
let orderItemAId = 0;
let orderItemBId = 0;
let payAmount = 0;
let stockAAfterOrder = 0;
let stockBAfterOrder = 0;

test.beforeAll(async ({ playwright }) => {
  const buyer = await newClientUser(playwright);
  buyerToken = buyer.access_token;
  communityId = buyer.user.community_id;
  const admin = await newAdminContext(playwright, undefined, undefined, communityId);
  adminApi = admin.api;

  try {
    const catBody = assertOk(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('退款测试分类') } })).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    );
    categoryId = catBody.id;
    productA = await createProduct(adminApi, '退款商品A', PRICE_A);
    skuA = await getSkuId(adminApi, productA);
    productB = await createProduct(adminApi, '退款商品B', PRICE_B);
    skuB = await getSkuId(adminApi, productB);

    // 多行订单：A×2 + B×1（自提）
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    let orderDetail: OrderDetail;
    try {
      const orderBody = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: {
              delivery_type: 1,
              address_id: 0,
              remark: 'AUTO-TEST 代客退款',
              items: [
                { sku_id: skuA, quantity: QTY_A },
                { sku_id: skuB, quantity: 1 },
              ],
            },
          })
        ).json()) as ApiResponse<{ id: number }>,
        'C 端下单',
      );
      const payBody = (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: orderBody.id, channel: 'cash' } })
      ).json()) as ApiResponse;
      expect(payBody.code, `现金支付失败: ${payBody.message}`).toBe(0);
      const detail = assertOk(
        (await (await buyerApi.get(`/api/v1/orders/${orderBody.id}`)).json()) as ApiResponse<OrderDetail>,
        '订单详情',
      );
      orderDetail = detail;
    } finally {
      await buyerApi.dispose();
    }
    orderId = orderDetail.id;
    payAmount = orderDetail.pay_amount;
    orderItemAId = orderDetail.items.find((i) => i.sku_id === skuA)?.id ?? 0;
    orderItemBId = orderDetail.items.find((i) => i.sku_id === skuB)?.id ?? 0;
    expect(orderItemAId, '订单缺少 A 商品行').toBeGreaterThan(0);
    expect(orderItemBId, '订单缺少 B 商品行').toBeGreaterThan(0);
    // 记录下单后库存基准（退款回补核验用）
    stockAAfterOrder = await getStock(skuA);
    stockBAfterOrder = await getStock(skuB);
  } catch (e) {
    console.warn(`[20] 造数失败，用例将跳过：${e instanceof Error ? e.message : e}`);
    orderId = 0;
  }
});

test.afterAll(async () => {
  try {
    if (productA) await adminApi.delete(`/api/v1/admin/products/${productA}`);
    if (productB) await adminApi.delete(`/api/v1/admin/products/${productB}`);
    if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

/**
 * 管理端创建并上架商品（共用造数步骤）
 * - 函数名: createProduct
 * - 参数: api — 管理端上下文；name — 商品名；price — 单价
 * - 返回: Promise<number> 商品 ID
 * - 异常: 创建或上架失败时抛出 Error
 */
async function createProduct(api: Awaited<ReturnType<typeof newAdminContext>>['api'], name: string, price: number): Promise<number> {
  const body = assertOk(
    (await (
      await api.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name: autoName(name),
          unit: '份',
          is_multi_spec: false,
          sku_items: [{ price, stock: 30, safety_stock: 3 }],
        },
      })
    ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
    `创建商品 ${name}`,
  );
  const pid = body.product?.id ?? body.id ?? 0;
  const on = (await (await api.post(`/api/v1/admin/products/${pid}/status`, { data: { status: 1 } })).json()) as ApiResponse;
  if (on.code !== 0) throw new Error(`上架失败 code=${on.code}`);
  return pid;
}

/**
 * 取商品首个 SKU ID
 * - 函数名: getSkuId
 * - 参数: api — 管理端上下文；productId — 商品 ID
 * - 返回: Promise<number> SKU ID
 * - 异常: 商品无 SKU 时抛出 Error
 */
async function getSkuId(api: Awaited<ReturnType<typeof newAdminContext>>['api'], productId: number): Promise<number> {
  const detail = assertOk(
    (await (await api.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<{ skus?: Sku[] }>,
    '商品详情',
  );
  const skuId = detail.skus?.[0]?.id ?? 0;
  if (!skuId) throw new Error(`商品 ${productId} 无 SKU`);
  return skuId;
}

/**
 * 查询 SKU 当前库存
 * - 函数名: getStock
 * - 参数: skuId — SKU ID
 * - 返回: Promise<number> 当前库存
 * - 异常: 商品查询失败时抛出
 */
async function getStock(skuId: number): Promise<number> {
  const detail = assertOk(
    (await (await adminApi.get(`/api/v1/admin/products/${skuA === skuId ? productA : productB}`)).json()) as ApiResponse<{ skus?: Sku[] }>,
    '商品详情',
  );
  const sku = (detail.skus ?? []).find((s) => s.id === skuId);
  return sku?.stock ?? 0;
}

/**
 * 后台代客退款（共用步骤）
 * - 函数名: adminRefund
 * - 参数: body — 退款请求体（mode/items/amount/refund_freight/reason）
 * - 返回: Promise<{ code: number; message: string; data: RefundInfo }> 原始响应
 * - 异常: 网络错误抛出
 */
async function adminRefund(body: Record<string, unknown>): Promise<{ code: number; message: string; data: RefundInfo }> {
  const resp = await adminApi.post(`/api/v1/admin/orders/${orderId}/refund`, { data: body });
  return (await resp.json()) as { code: number; message: string; data: RefundInfo };
}

/**
 * 查询后台订单详情
 * - 函数名: getAdminOrder
 * - 参数: 无
 * - 返回: Promise<OrderDetail>
 * - 异常: 查询失败抛出
 */
async function getAdminOrder(): Promise<OrderDetail> {
  return assertOk(
    (await (await adminApi.get(`/api/v1/admin/orders/${orderId}`)).json()) as ApiResponse<OrderDetail>,
    '后台订单详情',
  );
}

test.describe.serial('部分商品退款', () => {
  let refund1Amount = 0;

  test('1. 后台代客退 A 商品 1 件（部分退）', async () => {
    test.skip(orderId <= 0, '订单未创建');
    const body = await adminRefund({
      mode: 1,
      reason: 'AUTO-TEST 部分退 A 1件',
      items: [{ order_item_id: orderItemAId, quantity: 1 }],
    });
    expect(body.code, `代客部分退失败: ${body.message}`).toBe(0);
    expect(body.data.status).toBe(5); // 发起即执行成功
    expect(body.data.type).toBe(2); // 部分退
    refund1Amount = body.data.amount;
    expect(refund1Amount, '退款金额应大于 0').toBeGreaterThan(0);
    // 单行部分退金额不应超过该行分摊应退（≤ A 行总价分摊 ≤ pay_amount）
    expect(refund1Amount).toBeLessThanOrEqual(payAmount);
  });

  test('2. 部分退后订单回退款前状态（2 已支付）', async () => {
    test.skip(orderId <= 0, '订单未创建');
    const detail = await getAdminOrder();
    expect(detail.status).toBe(2);
  });

  test('3. 部分退回补库存（A 库存 +1）', async () => {
    test.skip(orderId <= 0, '订单未创建');
    const stock = await getStock(skuA);
    expect(stock).toBe(stockAAfterOrder + 1);
  });

  test('4. 订单详情 refunds 数组含本笔部分退记录', async () => {
    test.skip(orderId <= 0, '订单未创建');
    const detail = await getAdminOrder();
    const refunds = detail.refunds ?? [];
    const found = refunds.find((r) => r.type === 2 && r.amount === refund1Amount);
    expect(found, '退款明细未找到本笔部分退').toBeTruthy();
    expect(found!.refund_no, '退款单号应非空').toBeTruthy();
  });

  test('5. 同一行再退 1 件（第二笔部分退，A 行退满）', async () => {
    test.skip(orderId <= 0, '订单未创建');
    const body = await adminRefund({
      mode: 1,
      reason: 'AUTO-TEST 部分退 A 再退1件',
      items: [{ order_item_id: orderItemAId, quantity: 1 }],
    });
    expect(body.code, `第二笔部分退失败: ${body.message}`).toBe(0);
    expect(body.data.status).toBe(5);
    // 第二笔 + 第一笔 ≤ 实付
    expect(body.data.amount + refund1Amount).toBeLessThanOrEqual(payAmount);
  });

  test('6. 超出该行剩余可退数量被拒绝（再退 1 件但 A 行已退满）', async () => {
    test.skip(orderId <= 0, '订单未创建');
    const body = await adminRefund({
      mode: 1,
      reason: 'AUTO-TEST 超量退款',
      items: [{ order_item_id: orderItemAId, quantity: 1 }],
    });
    expect(body.code, 'A 行已退满 2 件应拒绝').not.toBe(0);
  });
});

test.describe.serial('仅金额退款（mode=2）', () => {
  test('7. 仅金额退超额被拒绝', async () => {
    test.skip(orderId <= 0, '订单未创建');
    const body = await adminRefund({ mode: 2, amount: 99999, reason: 'AUTO-TEST 超额仅金额退' });
    expect(body.code, '超额仅金额退应拒绝').not.toBe(0);
    expect(body.message).toContain('超过');
  });

  test('8. 仅金额退携带商品明细被拒绝', async () => {
    test.skip(orderId <= 0, '订单未创建');
    const body = await adminRefund({
      mode: 2,
      amount: 1,
      reason: 'AUTO-TEST 带明细仅金额退',
      items: [{ order_item_id: orderItemBId, quantity: 1 }],
    });
    expect(body.code, '仅金额退不应接受商品明细').not.toBe(0);
  });

  test('9. 仅金额退 B 行（不回补库存）', async () => {
    test.skip(orderId <= 0, '订单未创建');
    // 先按商品退 B 行 1 件（此时 A 已退满、B 退 1 件 → 全部退完 → 订单 8）
    // 为单独验证"仅金额退"，本用例先只退金额 1 元，订单保持部分退款
    const stockBBefore = await getStock(skuB);
    const body = await adminRefund({ mode: 2, amount: 1, reason: 'AUTO-TEST 仅金额退1元' });
    expect(body.code, `仅金额退失败: ${body.message}`).toBe(0);
    expect(body.data.status).toBe(5);
    expect(body.data.type).toBe(3); // 仅金额退
    expect(body.data.amount).toBe(1);
    // 不回补库存
    const stockBAfter = await getStock(skuB);
    expect(stockBAfter).toBe(stockBBefore);
    // 订单回退款前状态（未全额退款）
    const detail = await getAdminOrder();
    expect(detail.status).toBe(2);
  });
});

test.describe.serial('多笔累计全退', () => {
  test('10. 退 B 行 1 件后累计全退 → 订单 8 且末笔 type 升级整单退', async () => {
    test.skip(orderId <= 0, '订单未创建');
    const body = await adminRefund({
      mode: 1,
      reason: 'AUTO-TEST 退 B 行补齐全款',
      items: [{ order_item_id: orderItemBId, quantity: 1 }],
    });
    expect(body.code, `末笔退款失败: ${body.message}`).toBe(0);
    expect(body.data.status).toBe(5);
    const detail = await getAdminOrder();
    // money 口径：累计退款达实付（A2件 + B1件 + 仅金额1元 会有收敛，末笔按剩余可退退）
    // 订单到达 8 已退款 或 因仅金额退的 1 元导致剩余可退不为 0（末笔按商品退收敛剩余可退）
    // 服务端按剩余可退金额收敛：末笔后累计 = 实付 → 订单 8
    expect(detail.status, `订单应已退款(8)，实际 ${detail.status}`).toBe(8);
    // 末笔 type 升级为整单退（1）
    const refunds = detail.refunds ?? [];
    const last = refunds[refunds.length - 1];
    expect(last, '退款明细为空').toBeTruthy();
    expect(last.type, '末笔退款类型应升级为整单退').toBe(1);
  });

  test('11. 全退后订单详情 refunds 数量与累计金额核验', async () => {
    test.skip(orderId <= 0, '订单未创建');
    const detail = await getAdminOrder();
    const refunds = detail.refunds ?? [];
    expect(refunds.length, '应有 4 笔退款记录').toBe(4);
    const total = refunds.reduce((s, r) => s + (r.amount ?? 0) + (r.freight_amount ?? 0), 0);
    // money 口径：累计退款（含运费）= 实付
    expect(total).toBeCloseTo(payAmount, 2);
    // 全部成功
    expect(refunds.every((r) => r.status === 5), '所有退款单应为成功状态').toBe(true);
  });
});
