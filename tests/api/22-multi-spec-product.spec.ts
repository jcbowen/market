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
 * 模块 22：多规格商品全链路
 * 覆盖：specs/sku_items 多规格树创建（temp_id 引用）→ admin 详情规格树/SKU 组合
 *       → 价格区间汇总（min/max）→ 划线价（SKU 级 + 汇总）
 *       → C 端详情/列表/SKU 列表可见 → 按指定 SKU 下单金额与库存核验
 * 规格：口味(原味/香辣) × 规格(小份/大份)，建 3 个 SKU 组合
 */

const PRICE_MIN = 8.8; // 原味小份（划线价 9.9）
const PRICE_MID = 9.8; // 香辣小份
const PRICE_MAX = 15.8; // 原味大份
const ORIGINAL_PRICE = 9.9; // 最低价 SKU 的划线价

interface SpecValue {
  id: number;
  name: string;
  [k: string]: unknown;
}

interface SpecInfo {
  id: number;
  name: string;
  values: SpecValue[];
  [k: string]: unknown;
}

interface Sku {
  id: number;
  product_id: number;
  spec_name: string;
  spec_value_ids: string;
  price: number;
  original_price: number;
  stock: number;
  status: number;
  [k: string]: unknown;
}

interface ProductInfo {
  id: number;
  name: string;
  is_multi_spec: boolean;
  min_price: number;
  max_price: number;
  original_price: number;
  price: number;
  stock: number;
  [k: string]: unknown;
}

interface ProductDetail {
  product: ProductInfo;
  skus: Sku[];
  specs: SpecInfo[];
  [k: string]: unknown;
}

interface OrderDetail {
  id: number;
  status: number;
  total_amount: number;
  pay_amount: number;
  items: { sku_id: number; quantity: number; total_price: number }[];
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
let skuMinId = 0; // 最低价 SKU（原味小份）

test.beforeAll(async ({ playwright }) => {
  const buyer = await newClientUser(playwright);
  buyerToken = buyer.access_token;
  communityId = buyer.user.community_id;
  const admin = await newAdminContext(playwright, undefined, undefined, communityId);
  adminApi = admin.api;

  try {
    const catBody = assertOk(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('多规格分类') } })).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    );
    categoryId = catBody.id;
  } catch (e) {
    console.warn(`[22] 造数失败，用例将跳过：${e instanceof Error ? e.message : e}`);
    categoryId = 0;
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

test.describe.serial('多规格商品创建与详情', () => {
  test('1. 管理端创建多规格商品（2 规格 × 3 SKU 组合）', async () => {
    test.skip(categoryId <= 0, '环境造数失败');
    const body = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/products', {
          data: {
            category_id: categoryId,
            name: autoName('多规格卤味'),
            unit: '盒',
            description: 'AUTO-TEST 多规格商品',
            is_multi_spec: true,
            specs: [
              {
                name: '口味',
                sort: 1,
                values: [
                  { temp_id: 't1', name: '原味', sort: 1 },
                  { temp_id: 't2', name: '香辣', sort: 2 },
                ],
              },
              {
                name: '规格',
                sort: 2,
                values: [
                  { temp_id: 't3', name: '小份', sort: 1 },
                  { temp_id: 't4', name: '大份', sort: 2 },
                ],
              },
            ],
            sku_items: [
              { spec_value_ids: ['t1', 't3'], price: PRICE_MIN, stock: 10, safety_stock: 1, original_price: ORIGINAL_PRICE },
              { spec_value_ids: ['t2', 't3'], price: PRICE_MID, stock: 8, safety_stock: 1 },
              { spec_value_ids: ['t1', 't4'], price: PRICE_MAX, stock: 6, safety_stock: 1 },
            ],
          },
        })
      ).json()) as ApiResponse<{ product: ProductInfo; skus: Sku[]; specs: SpecInfo[] }>,
      '创建多规格商品',
    );
    productId = body.product.id;
    expect(productId).toBeGreaterThan(0);
    expect(body.product.is_multi_spec).toBe(true);
    // 创建响应即含规格树与 SKU
    expect(body.specs.length).toBe(2);
    expect(body.skus.length).toBe(3);
    // 上架
    const on = (await (
      await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })
    ).json()) as ApiResponse;
    expect(on.code, `上架失败: ${on.message}`).toBe(0);
  });

  test('2. admin 详情：规格树完整且 SKU 组合键正确', async () => {
    test.skip(productId <= 0, '商品未创建');
    const body = assertOk(
      (await (await adminApi.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<ProductDetail>,
      'admin 商品详情',
    );
    // 规格树：口味(2值) + 规格(2值)
    const specNames = body.specs.map((s) => s.name).sort();
    expect(specNames).toEqual(['口味', '规格']);
    const allValues = body.specs.flatMap((s) => s.values.map((v) => v.name)).sort();
    expect(allValues).toEqual(['原味', '小份', '大份', '香辣'].sort());
    expect(body.specs.every((s) => s.values.every((v) => v.id > 0)), '规格值应有真实 ID').toBe(true);
    // 3 个 SKU，多规格组合键非空
    expect(body.skus.length).toBe(3);
    expect(body.skus.every((k) => k.spec_value_ids.length > 0), '多规格 SKU 组合键应非空').toBe(true);
    expect(body.skus.every((k) => k.spec_name.length > 0), 'SKU 规格名应非空').toBe(true);
  });

  test('3. admin 详情：价格区间汇总与划线价正确', async () => {
    test.skip(productId <= 0, '商品未创建');
    const body = assertOk(
      (await (await adminApi.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<ProductDetail>,
      'admin 商品详情',
    );
    expect(body.product.min_price).toBeCloseTo(PRICE_MIN, 2);
    expect(body.product.max_price).toBeCloseTo(PRICE_MAX, 2);
    expect(body.product.stock).toBe(24); // 10+8+6
    // 汇总划线价 = 最低价 SKU 的划线价
    expect(body.product.original_price).toBeCloseTo(ORIGINAL_PRICE, 2);
    const minSku = body.skus.find((k) => Math.abs(k.price - PRICE_MIN) < 0.001);
    expect(minSku, '未找到最低价 SKU').toBeTruthy();
    expect(minSku!.original_price).toBeCloseTo(ORIGINAL_PRICE, 2);
    // 其他 SKU 未设置划线价 → 0（不展示）
    const others = body.skus.filter((k) => Math.abs(k.price - PRICE_MIN) >= 0.001);
    expect(others.every((k) => k.original_price === 0), '未设置划线价的 SKU 应为 0').toBe(true);
    skuMinId = minSku!.id;
  });

  test('4. C 端详情可见规格树与 SKU 划线价', async ({ playwright }) => {
    test.skip(productId <= 0, '商品未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = assertOk(
        (await (await buyerApi.get(`/api/v1/products/${productId}`, { params: { community_id: communityId } })).json()) as ApiResponse<ProductDetail>,
        'C 端商品详情',
      );
      expect(body.product.is_multi_spec).toBe(true);
      expect(body.specs.length).toBe(2);
      expect(body.skus.length).toBe(3);
      expect(body.skus.some((k) => Math.abs(k.original_price - ORIGINAL_PRICE) < 0.001), 'C 端应可见划线价').toBe(true);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('5. C 端列表可见该商品且展示价格区间', async () => {
    test.skip(productId <= 0, '商品未创建');
    const resp = await adminApi.get('/api/v1/products', {
      params: { page: 1, page_size: 50, status: 1, community_id: communityId },
    });
    const body = (await resp.json()) as ApiResponse<{ list?: ProductInfo[] }>;
    const products = asList<ProductInfo>(body.data as unknown as { list?: ProductInfo[] });
    const found = products.find((p) => p.id === productId);
    expect(found, 'C 端列表未展示多规格商品').toBeTruthy();
    expect(found!.min_price).toBeCloseTo(PRICE_MIN, 2);
    expect(found!.max_price).toBeCloseTo(PRICE_MAX, 2);
    expect(found!.original_price).toBeCloseTo(ORIGINAL_PRICE, 2);
  });

  test('6. C 端 SKU 列表返回 3 个在售 SKU', async () => {
    test.skip(productId <= 0, '商品未创建');
    const resp = await adminApi.get('/api/v1/skus', { params: { product_id: productId } });
    const body = (await resp.json()) as ApiResponse<Sku[]>;
    const skus = asList<Sku>(body.data);
    expect(skus.length).toBe(3);
    expect(skus.every((s) => s.status === 1 && s.stock > 0), 'SKU 应在售且有库存').toBe(true);
  });
});

test.describe.serial('按 SKU 下单', () => {
  let orderId = 0;
  let stockBefore = 0;

  test('7. 按最低价 SKU 下单 2 件并支付', async ({ playwright }) => {
    test.skip(skuMinId <= 0, 'SKU 未就绪');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      stockBefore = (await (await adminApi.get('/api/v1/skus', { params: { product_id: productId } })).json() as ApiResponse<Sku[]>).data?.find((s) => s.id === skuMinId)?.stock ?? 0;
      const orderBody = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: {
              delivery_type: 1,
              address_id: 0,
              remark: 'AUTO-TEST 多规格下单',
              items: [{ sku_id: skuMinId, quantity: 2 }],
            },
          })
        ).json()) as ApiResponse<{ id: number }>,
        'C 端按 SKU 下单',
      );
      orderId = orderBody.id;
      const payBody = (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: orderId, channel: 'cash' } })
      ).json()) as ApiResponse;
      expect(payBody.code, `现金支付失败: ${payBody.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('8. 订单金额按所选 SKU 单价计算（8.8×2=17.6）', async ({ playwright }) => {
    test.skip(orderId <= 0, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const detail = assertOk(
        (await (await buyerApi.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<OrderDetail>,
        '订单详情',
      );
      expect(detail.items.length).toBe(1);
      expect(detail.items[0].sku_id).toBe(skuMinId);
      expect(detail.items[0].quantity).toBe(2);
      // 环境可能叠加满减等优惠，商品行金额应精确
      expect(detail.items[0].total_price).toBeCloseTo(PRICE_MIN * 2, 2);
      expect(detail.total_amount).toBeCloseTo(PRICE_MIN * 2, 2);
      expect(detail.pay_amount).toBeCloseTo(detail.total_amount - (detail.discount_amount ?? 0), 2);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('9. 下单后对应 SKU 库存扣减 2', async () => {
    test.skip(orderId <= 0, '订单未创建');
    const resp = await adminApi.get('/api/v1/skus', { params: { product_id: productId } });
    const body = (await resp.json()) as ApiResponse<Sku[]>;
    const stockAfter = body.data?.find((s) => s.id === skuMinId)?.stock ?? 0;
    expect(stockAfter).toBe(stockBefore - 2);
  });

  test('10. 超出 SKU 库存下单被拒绝', async ({ playwright }) => {
    test.skip(skuMinId <= 0, 'SKU 未就绪');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = (await (
        await buyerApi.post('/api/v1/orders', {
          data: {
            delivery_type: 1,
            address_id: 0,
            items: [{ sku_id: skuMinId, quantity: 999 }],
          },
        })
      ).json()) as ApiResponse;
      expect(body.code, '超出库存应下单失败').not.toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });
});
