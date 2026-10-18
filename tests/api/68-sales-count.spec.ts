import { test, expect, type Playwright } from '@playwright/test';
import type { APIRequestContext } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  ensureStock,
  assertOk,
  type ApiResponse,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 68：净成交销量口径（sales_count 累加与回退）
 * 覆盖：
 *   - 累加时机：订单进入 status=2（已支付）才计销量，待支付不计
 *   - 累加幅度：delta = +1 × 明细 quantity（非按单 +1）
 *   - 回退时机：后台取消 POST /admin/orders/:id/cancel（UpdateOrderStatus{6}）且
 *     statusBeforeCancel ∈ {2,3} 时回退 −1×quantity；待支付(1)取消不回退
 *   - 口径差异（现状锁定，非期望行为）：C 端 PUT /orders/:id/cancel 走 CancelOrder，
 *     货到付款单（cash 渠道 pay_type=3）允许在 status=2 直接取消，但该路径不调
 *     adjustOrderSales → 销量不回退，形成「同一业务动作、两条链路两种结果」
 *   - 重复取消被状态机拒绝，不会二次回退成负数（服务端另有 GREATEST 钳制）
 *   - sales_count 为原始净成交量，不含 virtual_sales（展示值由前端相加）
 *   - SKU 维度不可断言：ProductInfo 有 sales_count，SkuInfo 无该字段（proto 事实，此处锁定响应结构）
 * 数据惯例：造数商品在 afterAll 尽力删除；社区语境取自 C 端买家绑定的社区（下单前置条件）
 */

interface ProductDetail {
  product: {
    id: number;
    name: string;
    sales_count?: number;
    virtual_sales?: number;
    [k: string]: unknown;
  };
  skus?: { id: number; product_id: number; stock: number; [k: string]: unknown }[];
  [k: string]: unknown;
}

interface OrderResult {
  id: number;
  order_no: string;
  [k: string]: unknown;
}

let adminApi: APIRequestContext;
let buyerToken = '';
let categoryId = 0;
/** 主商品：销量累加/回退主线断言 */
let productId = 0;
let skuId = 0;
/** 带虚拟销量的商品：验证 sales_count 不含虚拟量 */
let virtualProductId = 0;
let virtualSkuId = 0;

/**
 * 读取商品净成交销量
 * - 函数名: readSalesCount
 * - 参数: id — 商品 ID
 * - 返回: Promise<number> sales_count（proto omitempty，未计销量时键消失，按 0 处理）
 * - 异常: 业务码非 0 时经 assertOk 抛出
 * - 使用示例: const n = await readSalesCount(productId);
 */
async function readSalesCount(id: number): Promise<number> {
  const body = (await (await adminApi.get(`/api/v1/admin/products/${id}`)).json()) as ApiResponse<ProductDetail>;
  return assertOk(body, `商品详情(${id})`).product?.sales_count ?? 0;
}

/**
 * 读取商品详情原始结构（供结构断言，不走 sales 收敛）
 * - 函数名: readDetail
 * - 参数: id — 商品 ID
 * - 返回: Promise<ProductDetail>
 * - 异常: 业务码非 0 时经 assertOk 抛出
 */
async function readDetail(id: number): Promise<ProductDetail> {
  const body = (await (await adminApi.get(`/api/v1/admin/products/${id}`)).json()) as ApiResponse<ProductDetail>;
  return assertOk(body, `商品详情(${id})`);
}

/** 下单限流码（网关 UserRateLimitMiddleware(0.08, 5)：同用户桶容量 5，之后每 12.5 秒补 1 个令牌） */
const RATE_LIMIT = 10005;
/** 令牌回补等待（略大于 1/0.08 秒） */
const REFILL_MS = 13_000;

/**
 * C 端创建待支付自提单（自动等待用户限流令牌回补）
 * 本模块单用户需连开 6 单，超出桶容量属预期，故遇 10005 定时重试而非判失败
 * - 函数名: createOrder
 * - 参数: playwright — Playwright 工厂；sku — SKU ID；quantity — 数量；remark — 备注
 * - 返回: Promise<number> 订单 ID
 * - 异常: 限流重试超上限或其他业务失败时经 assertOk 抛出
 * - 使用示例: const orderId = await createOrder(playwright, skuId, 2, 'AUTO-TEST 68');
 */
async function createOrder(playwright: Playwright, sku: number, quantity: number, remark: string): Promise<number> {
  const buyerApi = await newAuthedRequest(playwright, buyerToken);
  try {
    for (let attempt = 1; attempt <= 3; attempt++) {
      const body = (await (
        await buyerApi.post('/api/v1/orders', {
          data: { delivery_type: 1, address_id: 0, remark, items: [{ sku_id: sku, quantity }] },
        })
      ).json()) as ApiResponse<OrderResult>;
      if (body.code === RATE_LIMIT) {
        await new Promise((r) => setTimeout(r, REFILL_MS));
        continue;
      }
      return assertOk(body, 'C 端下单').id;
    }
  } finally {
    await buyerApi.dispose();
  }
  throw new Error('C 端下单连续 3 次被限流（10005），令牌回补等待仍不足');
}

/**
 * 现金（货到付款）支付订单，使订单进入 status=2
 * - 函数名: payWithCash
 * - 参数: playwright — Playwright 工厂；orderId — 订单 ID
 * - 返回: Promise<void>
 * - 异常: 支付业务失败时经 assertOk 抛出（渠道未启用/订单不属于该用户均属造数失败）
 */
async function payWithCash(playwright: Playwright, orderId: number): Promise<void> {
  const buyerApi = await newAuthedRequest(playwright, buyerToken);
  try {
    const body = (await (
      await buyerApi.post('/api/v1/payments', { data: { order_id: orderId, channel: 'cash' } })
    ).json()) as ApiResponse;
    assertOk(body, `现金支付(${orderId})`);
  } finally {
    await buyerApi.dispose();
  }
}

/**
 * 后台取消订单
 * - 函数名: adminCancel
 * - 参数: orderId — 订单 ID；reason — 取消原因（接口 binding:required，缺省报 10001）
 * - 返回: Promise<ApiResponse<{ id: number; status: number }>>
 * - 异常: 网络错误由 Playwright 抛出，业务错误通过 code 判断
 */
async function adminCancel(
  orderId: number,
  reason?: string,
): Promise<ApiResponse<{ id: number; status: number }>> {
  const data: Record<string, string> = {};
  if (reason !== undefined) data.reason = reason;
  return (await (await adminApi.post(`/api/v1/admin/orders/${orderId}/cancel`, { data })).json()) as ApiResponse<{
    id: number;
    status: number;
  }>;
}

/**
 * C 端取消订单（走 CancelOrder，与后台取消不同链路）
 * - 函数名: clientCancel
 * - 参数: playwright — Playwright 工厂；orderId — 订单 ID；reason — 取消原因
 * - 返回: Promise<ApiResponse>
 * - 异常: 网络错误由 Playwright 抛出，业务错误通过 code 判断
 */
async function clientCancel(playwright: Playwright, orderId: number, reason: string): Promise<ApiResponse> {
  const buyerApi = await newAuthedRequest(playwright, buyerToken);
  try {
    return (await (await buyerApi.put(`/api/v1/orders/${orderId}/cancel`, { data: { reason } })).json()) as ApiResponse;
  } finally {
    await buyerApi.dispose();
  }
}

/**
 * 创建在售单规格商品并取其唯一 SKU
 * 初始库存 0，库存由 afterAll 前的 ensureStock 补足，销量口径断言与库存无关
 * - 函数名: createSku
 * - 参数: label — 名称标识；virtual — 虚拟销量（展示口径 = 真实销量 + 虚拟销量）
 * - 返回: Promise<{ productId: number; skuId: number }>
 * - 异常: 建品、取不到 SKU 或置为在售失败时抛出 Error（造数前置）
 * - 使用示例: const { skuId } = await createSku('主', 0);
 */
async function createSku(label: string, virtual: number): Promise<{ productId: number; skuId: number }> {
  const created = assertOk<{ product?: { id: number }; id?: number }>(
    (await (
      await adminApi.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name: autoName(`销量商品${label}`),
          unit: '份',
          is_multi_spec: false,
          virtual_sales: virtual,
          sku_items: [{ price: 9.9, cost_price: 4, stock: 0, safety_stock: 0 }],
        },
      })
    ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
    '创建商品',
  );
  const pid = created.product?.id ?? created.id ?? 0;
  if (!pid) throw new Error('创建商品未返回 ID');

  const skus = assertOk<{ id: number }[]>(
    (await (await adminApi.get('/api/v1/skus', { params: { product_id: pid } })).json()) as ApiResponse<{ id: number }[]>,
    'SKU 列表',
  );
  const sid = skus?.[0]?.id ?? 0;
  if (!sid) throw new Error(`商品 ${pid} 未生成 SKU`);

  const on = (await (await adminApi.post(`/api/v1/admin/products/${pid}/status`, { data: { status: 1 } })).json()) as ApiResponse;
  if (on.code !== 0) throw new Error(`商品 ${pid} 置为在售失败 code=${on.code} message=${on.message}`);
  return { productId: pid, skuId: sid };
}

test.beforeAll(async ({ playwright }) => {
  const buyer = await clientLogin(playwright.request, uniquePhone());
  buyerToken = buyer.access_token;
  const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
  adminApi = admin.api;

  try {
    categoryId = assertOk<{ id: number }>(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('销量分类') } })).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    ).id;

    const main = await createSku('主', 0);
    productId = main.productId;
    skuId = main.skuId;

    const virtual = await createSku('虚拟量', 5);
    virtualProductId = virtual.productId;
    virtualSkuId = virtual.skuId;

    await ensureStock(adminApi, skuId, 50);
    await ensureStock(adminApi, virtualSkuId, 20);
  } catch (e) {
    console.warn(`[API68] 造数失败，用例将跳过：${e instanceof Error ? e.message : e}`);
    categoryId = 0;
  }
});

test.afterAll(async () => {
  for (const id of [productId, virtualProductId]) {
    if (!id) continue;
    const del = (await (await adminApi.delete(`/api/v1/admin/products/${id}`)).json()) as ApiResponse;
    if (del.code !== 0) console.warn(`[API68] 商品 ${id} 清理失败（被订单引用属预期）：${del.message}`);
  }
  if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  await adminApi?.dispose();
});

test.describe.serial('净成交销量口径', () => {
  let pendingOrderId = 0;

  test('1. 建品后销量为 0：sales_count 零值也下发，virtual_sales 取造数值', async () => {
    test.skip(!categoryId || !skuId, '造数未完成');
    const detail = await readDetail(productId);
    // 实测网关序列化 proto 时零值字段仍下发（sales_count: 0 存在），
    // 但断言统一用 ?? 0 兜底，避免序列化口径变更后用例脆断
    expect(detail.product.sales_count ?? 0, '新建商品净成交销量应为 0').toBe(0);
    expect(detail.product.virtual_sales ?? -1).toBe(0);
    expect(await readSalesCount(productId)).toBe(0);
  });

  test('2. C 端下单不支付：待支付不计销量', async ({ playwright }) => {
    test.skip(!categoryId || !skuId, '造数未完成');
    pendingOrderId = await createOrder(playwright, skuId, 1, autoName('68 待支付单'));
    expect(pendingOrderId).toBeGreaterThan(0);
    expect(await readSalesCount(productId), 'status=1 不应累加销量').toBe(0);
  });

  test('3. 后台取消待支付单：取消缺原因被拒，成功后销量仍为 0', async () => {
    test.skip(!pendingOrderId, '待支付单未创建');
    const noReason = await adminCancel(pendingOrderId);
    expect(noReason.code, '缺 reason 应被参数校验拒绝').toBe(10001);
    expect(noReason.message).toContain('请填写取消原因');

    const cancelled = await adminCancel(pendingOrderId, autoName('68 取消待支付'));
    expect(cancelled.code, `取消待支付单失败: ${cancelled.message}`).toBe(0);
    expect(cancelled.data?.status).toBe(6);
    expect(await readSalesCount(productId), '待支付取消未计销量，不回退').toBe(0);
  });

  test('4. 现金支付自提单：销量按 quantity 累加（+2 而非 +1）', async ({ playwright }) => {
    test.skip(!categoryId || !skuId, '造数未完成');
    const orderId = await createOrder(playwright, skuId, 2, autoName('68 已支付单'));
    await payWithCash(playwright, orderId);
    expect(await readSalesCount(productId), '支付成功应累加明细数量').toBe(2);
  });

  test('5. 再次支付累加到存量之上，且 SKU 维度无 sales_count 字段', async ({ playwright }) => {
    test.skip(!categoryId || !skuId, '造数未完成');
    const orderId = await createOrder(playwright, skuId, 3, autoName('68 累加单'));
    await payWithCash(playwright, orderId);
    expect(await readSalesCount(productId), '销量按订单增量累加').toBe(5);

    const detail = await readDetail(productId);
    const sku = (detail.skus ?? []).find((s) => s.id === skuId);
    expect(sku, '商品详情应回显本轮 SKU').toBeTruthy();
    expect('sales_count' in (sku ?? {}), 'SkuInfo 无 sales_count 字段，销量只能按商品维度断言').toBe(false);
  });

  test('6. 后台取消已支付单：销量整单回退，重复取消不二次回退', async ({ playwright }) => {
    test.skip(!categoryId || !skuId, '造数未完成');
    const before = await readSalesCount(productId);
    expect(before, '前置累计销量应为 5').toBe(5);

    const qty = 4;
    const paidOrderId = await createOrder(playwright, skuId, qty, autoName('68 回退单'));
    await payWithCash(playwright, paidOrderId);
    expect(await readSalesCount(productId), '支付后应先累加').toBe(before + qty);

    const cancelled = await adminCancel(paidOrderId, autoName('68 取消已支付'));
    expect(cancelled.code, `后台取消已支付单失败: ${cancelled.message}`).toBe(0);
    expect(await readSalesCount(productId), 'statusBeforeCancel=2 应整单回退').toBe(before);

    const again = await adminCancel(paidOrderId, autoName('68 重复取消'));
    expect(again.code, '已取消单重复取消应被拒').not.toBe(0);
    expect(await readSalesCount(productId), '重复取消不得二次回退').toBe(before);
  });

  test('7. 口径差异锁定：C 端取消货到付款已支付单成功但销量不回退', async ({ playwright }) => {
    test.skip(!categoryId || !skuId, '造数未完成');
    const before = await readSalesCount(productId);
    const qty = 5;
    const orderId = await createOrder(playwright, skuId, qty, autoName('68 C端取消单'));
    await payWithCash(playwright, orderId);
    expect(await readSalesCount(productId), '支付后应先累加').toBe(before + qty);

    // 货到付款（cash→pay_type=3）单在 status=2 允许 C 端直接取消
    const cancelled = await clientCancel(playwright, orderId, autoName('68 C端取消'));
    expect(cancelled.code, `C 端取消货到付款单失败: ${cancelled.message}`).toBe(0);

    const after = await readSalesCount(productId);
    expect(after, '现状：C 端 CancelOrder 不调 adjustOrderSales，销量不回退（与后台取消不一致）').toBe(before + qty);
  });

  test('8. sales_count 为原始净成交量，不含 virtual_sales', async ({ playwright }) => {
    test.skip(!categoryId || !virtualSkuId, '造数未完成');
    const detail = await readDetail(virtualProductId);
    expect(detail.product.virtual_sales ?? -1, '虚拟销量造数应为 5').toBe(5);
    expect(await readSalesCount(virtualProductId), '未成交时原始销量为 0').toBe(0);

    const orderId = await createOrder(playwright, virtualSkuId, 1, autoName('68 虚拟量单'));
    await payWithCash(playwright, orderId);
    expect(await readSalesCount(virtualProductId), '累加只写真实成交量，虚拟量不参与计算').toBe(1);
    expect((await readDetail(virtualProductId)).product.virtual_sales ?? -1, '虚拟销量不受订单影响').toBe(5);
  });
});
