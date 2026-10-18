import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  ensureStock,
  assertOk,
  type ApiResponse,
  type SkuRef,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 56：订单状态角标统计（GET /api/v1/orders/status-counts）
 * 覆盖：
 *   - 新用户各状态计数全 0
 *   - 待支付订单计入 pending_pay（其余为 0）
 *   - 支付后状态迁移：pending_pay 归零、pending_ship +1（自提单支付即 status=2）
 *   - 口径对照：pending_ship 与订单列表 status=90（待发货聚合）total 一致
 *   - 退款中订单计入 after_sale（status=7），不影响 pending_ship
 * 说明：独立新买家（uniquePhone）保证计数确定性，自建商品承载下单
 */

interface StatusCounts {
  pending_pay: number;
  pending_ship: number;
  pending_receive: number;
  after_sale: number;
  [k: string]: unknown;
}

interface OrderResult {
  id: number;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let buyerToken = '';
let skuA: SkuRef | null = null;
let productIdA = 0;
let categoryId = 0;

/** 创建单个自建商品（单规格 SKU，挂在自建分类下）并返回 SKU 引用 */
async function createProduct(name: string, price: number): Promise<{ sku: SkuRef; productId: number }> {
  const prod = (await (
    await adminApi.post('/api/v1/admin/products', {
      data: {
        category_id: categoryId,
        name: autoName(name),
        unit: '份',
        is_multi_spec: false,
        sku_items: [{ price, stock: 100, safety_stock: 5 }],
      },
    })
  ).json()) as ApiResponse<{ product?: { id: number }; id?: number; skus?: { id: number }[] }>;
  expect(prod.code, `创建商品失败: ${prod.message}`).toBe(0);
  const productId = prod.data?.product?.id ?? prod.data?.id ?? 0;
  const on = (await (
    await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })
  ).json()) as ApiResponse;
  expect(on.code).toBe(0);
  const detail = (await (
    await adminApi.get(`/api/v1/admin/products/${productId}`)
  ).json()) as ApiResponse<{ skus: { id: number; stock: number }[] }>;
  const sku = detail.data?.skus?.[0];
  expect(sku?.id, 'SKU 未就绪').toBeGreaterThan(0);
  return { sku: { id: sku.id, product_id: productId, stock: sku.stock }, productId };
}

/** C 端查询订单状态角标统计 */
async function fetchCounts(buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>): Promise<StatusCounts> {
  const resp = await buyerApi.get('/api/v1/orders/status-counts');
  const body = (await resp.json()) as ApiResponse<StatusCounts>;
  return assertOk(body, '订单状态角标统计');
}

/** C 端下单（自提单，无地址）并返回订单 ID */
async function createSelfPickupOrder(buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>, quantity = 1): Promise<number> {
  const order = assertOk(
    (await (
      await buyerApi.post('/api/v1/orders', {
        data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuA!.id, quantity }] },
      })
    ).json()) as ApiResponse<OrderResult>,
    'C 端下单',
  );
  return order.id;
}

/** C 端现金支付（自提单支付成功即 status=2 已支付） */
async function payByCash(buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>, orderId: number): Promise<void> {
  const paid = (await (
    await buyerApi.post('/api/v1/payments', { data: { order_id: orderId, channel: 'cash' } })
  ).json()) as ApiResponse;
  expect(paid.code, `现金支付失败: ${paid.message}`).toBe(0);
}

test.beforeAll(async ({ playwright }) => {
  const buyer = await clientLogin(playwright.request, uniquePhone());
  buyerToken = buyer.access_token;
  const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
  adminApi = admin.api;
  const cat = (await (
    await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('角标统计分类') } })
  ).json()) as ApiResponse<{ id: number }>;
  expect(cat.code, `创建分类失败: ${cat.message}`).toBe(0);
  categoryId = cat.data!.id;
  const a = await createProduct(`角标统计A${Date.now()}`, 3.5);
  skuA = a.sku;
  productIdA = a.productId;
});

test.afterAll(async () => {
  try {
    if (productIdA) await adminApi?.delete(`/api/v1/admin/products/${productIdA}`);
    if (categoryId) await adminApi?.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

test.describe.serial('订单状态角标统计', () => {
  test('1. 新用户各状态计数全 0', async ({ playwright }) => {
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const counts = await fetchCounts(buyerApi);
      expect(counts.pending_pay).toBe(0);
      expect(counts.pending_ship).toBe(0);
      expect(counts.pending_receive).toBe(0);
      expect(counts.after_sale).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('2. 待支付订单计入 pending_pay', async ({ playwright }) => {
    await ensureStock(adminApi, skuA!.id, 300);
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      await createSelfPickupOrder(buyerApi);
    } finally {
      await buyerApi.dispose();
    }
    const buyerApi2 = await newAuthedRequest(playwright, buyerToken);
    try {
      const counts = await fetchCounts(buyerApi2);
      expect(counts.pending_pay, '待支付订单应计入 pending_pay').toBe(1);
      expect(counts.pending_ship, '未支付不应计入待发货').toBe(0);
      expect(counts.after_sale, '无售后应计为 0').toBe(0);
    } finally {
      await buyerApi2.dispose();
    }
  });

  test('3. 支付后迁移：新单转入待发货，与列表 total 一致', async ({ playwright }) => {
    await ensureStock(adminApi, skuA!.id, 300);
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    let orderId = 0;
    try {
      // quantity=2 错开与上一单（qty=1）的重复下单指纹（5s 窗口按 user+社区+配送方式+券+商品列表判重）
      orderId = await createSelfPickupOrder(buyerApi, 2);
      await payByCash(buyerApi, orderId);
    } finally {
      await buyerApi.dispose();
    }
    expect(orderId).toBeGreaterThan(0);
    const buyerApi2 = await newAuthedRequest(playwright, buyerToken);
    try {
      const counts = await fetchCounts(buyerApi2);
      // 注意：测试 2 的未支付单仍在，pending_pay 保持 1（状态计数为全量口径，非增量）
      expect(counts.pending_pay, '遗留未支付单仍计入待支付').toBe(1);
      expect(counts.pending_ship, '自提单支付后 status=2 应计入待发货').toBe(1);
      // 口径对照：待发货聚合（status=90）列表 total 与角标一致
      const list = (await (
        await buyerApi2.get('/api/v1/orders?status=90&page=1&page_size=1')
      ).json()) as ApiResponse<{ total: number }>;
      expect(list.data?.total, '角标 pending_ship 应等于待发货列表 total').toBe(counts.pending_ship);
    } finally {
      await buyerApi2.dispose();
    }
  });

  test('4. 退款中订单计入 after_sale，不影响 pending_ship', async ({ playwright }) => {
    await ensureStock(adminApi, skuA!.id, 300);
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    let refundOrderId = 0;
    try {
      // 第三单：支付后立即申请整单退款（订单 → 7 退款中）
      // quantity=3 错开与前两单（qty=1/2）的重复下单指纹
      refundOrderId = await createSelfPickupOrder(buyerApi, 3);
      await payByCash(buyerApi, refundOrderId);
      const refund = (await (
        await buyerApi.post(`/api/v1/orders/${refundOrderId}/refund`, { data: { reason: 'AUTO-TEST 角标售后' } })
      ).json()) as ApiResponse;
      expect(refund.code, `申请退款失败: ${refund.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
    const buyerApi2 = await newAuthedRequest(playwright, buyerToken);
    try {
      const counts = await fetchCounts(buyerApi2);
      expect(counts.after_sale, '退款中订单应计入 after_sale').toBe(1);
      expect(counts.pending_ship, '退款中的订单不再计入待发货').toBe(1);
      expect(counts.pending_pay, '遗留未支付单仍为 1').toBe(1);
    } finally {
      await buyerApi2.dispose();
    }
  });
});
