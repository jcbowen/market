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
 * 模块 42：常买商品统计链路（GET /api/v1/orders/frequent-products）
 * 覆盖：
 *   - 新用户无常买记录（不含未购买商品）
 *   - 购买后统计 purchase_count（去重订单数）/ total_quantity（累计件数）/ sku_id（最常买 SKU）
 *   - 多订单聚合、按购买次数降序
 *   - 排除口径：待支付(1)/退款中(7) 订单不计入统计
 *   - limit 参数生效
 * 说明：自建两个商品承载统计断言（唯一后缀隔离），新买家无历史订单避免环境污染
 */

interface FrequentItem {
  product_id: number;
  product_name: string;
  sku_id: number;
  purchase_count: number;
  total_quantity: number;
  [k: string]: unknown;
}

interface OrderResult {
  id: number;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let buyerToken = '';
/** 自建商品 A/B 的 SKU（A 为高频验证主体，B 为降序对照） */
let skuA: SkuRef | null = null;
let skuB: SkuRef | null = null;
let productIdA = 0;
let productIdB = 0;
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

/** C 端查询常买商品列表 */
async function fetchFrequent(buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>, limit?: number): Promise<FrequentItem[]> {
  const params: Record<string, number> = {};
  if (limit) params.limit = limit;
  const resp = await buyerApi.get('/api/v1/orders/frequent-products', { params });
  const body = (await resp.json()) as ApiResponse<{ list: FrequentItem[] }>;
  return assertOk(body, '常买商品查询').list ?? [];
}

test.beforeAll(async ({ playwright }) => {
  const buyer = await clientLogin(playwright.request, uniquePhone());
  buyerToken = buyer.access_token;
  const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
  adminApi = admin.api;
  // 自建分类承载两个测试商品
  const cat = (await (
    await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('常买统计分类') } })
  ).json()) as ApiResponse<{ id: number }>;
  expect(cat.code, `创建分类失败: ${cat.message}`).toBe(0);
  categoryId = cat.data!.id;
  const a = await createProduct(`常买A${Date.now()}`, 4.5);
  skuA = a.sku;
  productIdA = a.productId;
  const b = await createProduct(`常买B${Date.now()}`, 6.0);
  skuB = b.sku;
  productIdB = b.productId;
});

test.afterAll(async () => {
  try {
    if (productIdA) await adminApi?.delete(`/api/v1/admin/products/${productIdA}`);
    if (productIdB) await adminApi?.delete(`/api/v1/admin/products/${productIdB}`);
    if (categoryId) await adminApi?.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

test.describe.serial('常买商品统计', () => {
  test('1. 新用户无常买记录', async ({ playwright }) => {
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const list = await fetchFrequent(buyerApi);
      expect(list.some((p) => p.product_id === productIdA || p.product_id === productIdB), '未购买前不应出现自建商品').toBe(false);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('2. 首单购买 A×2，统计单量与件数', async ({ playwright }) => {
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const order = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuA!.id, quantity: 2 }] },
          })
        ).json()) as ApiResponse<OrderResult>,
        'C 端下单',
      );
      const paid = (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: order.id, channel: 'cash' } })
      ).json()) as ApiResponse;
      expect(paid.code, `现金支付失败: ${paid.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
    const list = await fetchFrequent(await newAuthedRequest(playwright, buyerToken));
    const itemA = list.find((p) => p.product_id === productIdA);
    expect(itemA, '购买后应统计到商品 A').toBeTruthy();
    expect(itemA!.purchase_count, '购买次数应为去重订单数 1').toBe(1);
    expect(itemA!.total_quantity, '累计件数应为 2').toBe(2);
    expect(itemA!.sku_id, '最常买 SKU 应为 A 的 SKU').toBe(skuA!.id);
  });

  test('3. 第二单 A×1 + B×3，聚合与降序', async ({ playwright }) => {
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const order = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: {
              delivery_type: 1,
              address_id: 0,
              items: [
                { sku_id: skuA!.id, quantity: 1 },
                { sku_id: skuB!.id, quantity: 3 },
              ],
            },
          })
        ).json()) as ApiResponse<OrderResult>,
        'C 端下单',
      );
      const paid = (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: order.id, channel: 'cash' } })
      ).json()) as ApiResponse;
      expect(paid.code, `现金支付失败: ${paid.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
    const list = await fetchFrequent(await newAuthedRequest(playwright, buyerToken));
    const itemA = list.find((p) => p.product_id === productIdA);
    const itemB = list.find((p) => p.product_id === productIdB);
    expect(itemA!.purchase_count, 'A 应聚合两单').toBe(2);
    expect(itemA!.total_quantity, 'A 累计件数 2+1=3').toBe(3);
    expect(itemB!.purchase_count, 'B 应为 1 单').toBe(1);
    expect(itemB!.total_quantity, 'B 累计件数 3').toBe(3);
    const idxA = list.findIndex((p) => p.product_id === productIdA);
    const idxB = list.findIndex((p) => p.product_id === productIdB);
    expect(idxA, 'A（2 单）应排在 B（1 单）之前').toBeLessThan(idxB);
  });

  test('4. 待支付订单不计入统计', async ({ playwright }) => {
    await ensureStock(adminApi, skuA!.id, 300);
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    let pendingOrderId = 0;
    try {
      const order = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuA!.id, quantity: 99 }] },
          })
        ).json()) as ApiResponse<OrderResult>,
        'C 端下单',
      );
      pendingOrderId = order.id;
    } finally {
      await buyerApi.dispose();
    }
    expect(pendingOrderId).toBeGreaterThan(0);
    const list = await fetchFrequent(await newAuthedRequest(playwright, buyerToken));
    const itemA = list.find((p) => p.product_id === productIdA);
    expect(itemA!.total_quantity, '待支付单 99 件不应计入').toBe(3);
  });

  test('5. 退款中订单不计入统计', async ({ playwright }) => {
    await ensureStock(adminApi, skuA!.id, 300);
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    let refundOrderId = 0;
    try {
      // 下单支付后立即申请退款（订单 → 7 退款中）
      const order = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuA!.id, quantity: 99 }] },
          })
        ).json()) as ApiResponse<OrderResult>,
        'C 端下单',
      );
      refundOrderId = order.id;
      const paid = (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: refundOrderId, channel: 'cash' } })
      ).json()) as ApiResponse;
      expect(paid.code).toBe(0);
      const refund = (await (
        await buyerApi.post(`/api/v1/orders/${refundOrderId}/refund`, { data: { reason: 'AUTO-TEST 常买排除' } })
      ).json()) as ApiResponse;
      expect(refund.code, `申请退款失败: ${refund.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
    const list = await fetchFrequent(await newAuthedRequest(playwright, buyerToken));
    const itemA = list.find((p) => p.product_id === productIdA);
    expect(itemA!.purchase_count, '退款中订单不应计入购买次数').toBe(2);
    expect(itemA!.total_quantity, '退款中订单不应计入累计件数').toBe(3);
  });

  test('6. limit 参数生效', async ({ playwright }) => {
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const list = await fetchFrequent(buyerApi, 1);
      expect(list.length, 'limit=1 应最多返回 1 条').toBeLessThanOrEqual(1);
    } finally {
      await buyerApi.dispose();
    }
  });
});
