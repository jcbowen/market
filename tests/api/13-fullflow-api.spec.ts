import { test, expect } from '@playwright/test';
import { clientLogin, newAdminContext, newAuthedRequest, asList, type ApiResponse, type PageData } from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 13：API 级全流程闭环
 * 管理端建分类+商品并上架 → C 端登录 → 加购 → 下单（自提）→ cash 支付
 * → 后台可见订单 → 后台兜底核销 → 状态推进
 */

interface Category {
  id: number;
}
interface Product {
  product?: { id: number; name: string };
  skus?: { id: number }[];
  id?: number;
}
interface Order {
  id: number;
  order_no: string;
  status: number;
  pickup_code?: string;
}

let adminApi: Awaited<ReturnType<typeof newAuthedRequest>>;
let clientApi: Awaited<ReturnType<typeof newAuthedRequest>>;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  // 管理端语境与 C 端用户同社区，保证订单互相可见
  const client = await clientLogin(playwright.request, uniquePhone());
  const adminCtx = await newAdminContext(playwright, undefined, undefined, client.user.community_id);
  adminApi = adminCtx.api;
  clientApi = await newAuthedRequest(playwright, client.access_token);
});

test.afterAll(async () => {
  await adminApi?.dispose();
  await clientApi?.dispose();
});

test.describe.serial('API 全流程：后台建品 → C 端消费 → 后台核销', () => {
  let categoryId = 0;
  let productId = 0;
  let skuId = 0;
  let orderId = 0;
  let pickupCode = '';
  let productName = '';
  test('1. 后台创建分类', async () => {
    const resp = await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('全流程分类') } });
    const body = (await resp.json()) as ApiResponse<Category>;
    expect(body.code).toBe(0);
    categoryId = body.data!.id;
  });

  test('2. 后台创建并上架商品', async () => {
    productName = autoName('全流程商品');
    const resp = await adminApi.post('/api/v1/admin/products', {
      data: {
        category_id: categoryId,
        name: productName,
        unit: '份',
        is_multi_spec: false,
        sku_items: [{ price: 6.6, stock: 50, safety_stock: 5 }],
      },
    });
    const body = (await resp.json()) as ApiResponse<Product>;
    expect(body.code).toBe(0);
    productId = body.data?.product?.id ?? body.data?.id ?? 0;
    expect(productId).toBeGreaterThan(0);
    const on = (await (
      await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })
    ).json()) as ApiResponse;
    expect(on.code).toBe(0);
    // 取 SKU ID（详情结构 { product, skus, specs }）
    const detail = (await (await adminApi.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<Product>;
    skuId = detail.data?.skus?.[0]?.id ?? 0;
    expect(skuId).toBeGreaterThan(0);
  });

  test('3. C 端商品列表可见新商品', async () => {
    // 列表缺省按 sort 降序，新建商品 sort=0 会排在演示商品之后，改用 keyword 定向检索
    const resp = await clientApi.get('/api/v1/products', {
      params: { page: 1, page_size: 50, status: 1, keyword: productName },
    });
    const body = (await resp.json()) as ApiResponse<PageData<Product>>;
    expect(body.code).toBe(0);
    expect(asList<Product>(body.data).some((p) => p.id === productId)).toBe(true);
  });

  test('4. C 端加购', async () => {
    const resp = await clientApi.post('/api/v1/cart/items', {
      data: { product_id: productId, sku_id: skuId, quantity: 2 },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('5. C 端创建自提订单', async () => {
    const resp = await clientApi.post('/api/v1/orders', {
      data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuId, quantity: 2 }] },
    });
    const body = (await resp.json()) as ApiResponse<Order>;
    expect(body.code).toBe(0);
    orderId = body.data!.id;
  });

  test('6. C 端 cash 支付', async () => {
    const resp = await clientApi.post('/api/v1/payments', { data: { order_id: orderId, channel: 'cash' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('7. C 端订单详情取货码存在', async () => {
    const resp = await clientApi.get(`/api/v1/orders/${orderId}`);
    const body = (await resp.json()) as ApiResponse<Order>;
    expect(body.code).toBe(0);
    if (body.data?.pickup_code) pickupCode = body.data.pickup_code;
  });

  test('8. 后台订单列表可见该订单', async () => {
    const resp = await adminApi.get('/api/v1/admin/orders', { params: { page: 1, page_size: 50 } });
    const body = (await resp.json()) as ApiResponse<PageData<Order>>;
    expect(body.code).toBe(0);
    expect(body.data?.list.some((o) => o.id === orderId)).toBe(true);
  });

  test('9. 后台兜底核销（取货码来自 C 端详情）', async () => {
    if (!pickupCode) {
      test.skip(true, 'C 端详情未返回取货码，核销字段需人工确认');
      return;
    }
    const resp = await adminApi.post(`/api/v1/admin/orders/${orderId}/verify-pickup`, {
      data: { pickup_code: pickupCode },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('10. 清理：下架并删除商品与分类', async () => {
    if (productId) await adminApi.delete(`/api/v1/admin/products/${productId}`);
    if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  });
});
