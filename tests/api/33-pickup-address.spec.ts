import { test, expect, type Playwright } from '@playwright/test';
import { clientLogin, newAdminContext, newAuthedRequest, type ApiResponse } from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 33：自提单自提点地址展示
 * 覆盖：自建测试社区（隔离环境配置）→
 *       1) 未配置 pickup_address 时，自提单详情地址卡回退社区地址（order.go 313-316）
 *       2) 配置 pickup_address 后，新自提单详情地址卡展示自提点地址
 * 地址卡口径：contact_name=社区名称、detail=pickup_address（未配置回退 community.address）
 */

interface Community {
  id: number;
  name: string;
  address: string;
  pickup_address: string;
  [k: string]: unknown;
}

interface OrderDetail {
  id: number;
  address: {
    contact_name: string;
    detail: string;
    [k: string]: unknown;
  };
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let clientApi: Awaited<ReturnType<typeof newAuthedRequest>>;
let communityId = 0;
let skuId = 0;

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;

  // 自建测试社区（pickup_address 留空，验证回退路径）
  const created = (await (await adminApi.post('/api/v1/admin/communities', {
    data: {
      name: autoName('自提点测试社区'),
      address: `AUTO-TEST 回退地址-${Date.now() % 100000}`,
      longitude: 114.3,
      latitude: 30.5,
      contact_phone: '13900001111',
      pickup_address: '',
      delivery_fee: 3,
      min_order_amount: 0,
      status: 1,
    },
  })).json()) as ApiResponse<{ id: number }>;
  expect(created.code, `创建测试社区失败: ${created.message}`).toBe(0);
  communityId = created.data!.id;

  // C 端用户绑定测试社区（重登刷新社区语境）
  const info = await clientLogin(playwright.request, uniquePhone());
  const sel = (await (await adminApi.put('/api/v1/user/community', {
    data: { community_id: communityId },
    headers: { Authorization: `Bearer ${info.access_token}` },
  })).json()) as ApiResponse;
  expect(sel.code).toBe(0);
  const relogin = await clientLogin(playwright.request, info.user.phone);
  clientApi = await newAuthedRequest(playwright, relogin.access_token);

  // 测试社区语境（X-Community-Id）下自建商品，社区隔离默认无商品
  const ctxHeaders = { 'X-Community-Id': String(communityId) };
  const cat = (await (await adminApi.post('/api/v1/admin/categories', {
    data: { name: autoName('自提点分类') },
    headers: ctxHeaders,
  })).json()) as ApiResponse<{ id: number }>;
  expect(cat.code, `创建分类失败: ${cat.message}`).toBe(0);
  const prod = (await (await adminApi.post('/api/v1/admin/products', {
    data: {
      category_id: cat.data!.id,
      name: autoName('自提点商品'),
      unit: '份',
      is_multi_spec: false,
      sku_items: [{ price: 2.5, stock: 100, safety_stock: 5 }],
    },
    headers: ctxHeaders,
  })).json()) as ApiResponse<{ product?: { id: number }; id?: number }>;
  expect(prod.code, `创建商品失败: ${prod.message}`).toBe(0);
  const productId = prod.data?.product?.id ?? prod.data?.id ?? 0;
  const on = (await (await adminApi.post(`/api/v1/admin/products/${productId}/status`, {
    data: { status: 1 },
    headers: ctxHeaders,
  })).json()) as ApiResponse;
  expect(on.code).toBe(0);
  const detail = (await (await adminApi.get(`/api/v1/admin/products/${productId}`, { headers: ctxHeaders })).json()) as ApiResponse<{ skus: { id: number }[] }>;
  skuId = detail.data?.skus?.[0]?.id ?? 0;
  expect(skuId, 'SKU 未就绪').toBeGreaterThan(0);
});

test.afterAll(async () => {
  await clientApi?.dispose();
  // 清理自建测试社区，避免残留社区占据 my/communities 首位影响其他用例的默认语境
  if (communityId) await adminApi.delete(`/api/v1/admin/communities/${communityId}`);
  await adminApi?.dispose();
});

/** C 端下自提单（默认待支付，地址卡展示不依赖支付） */
async function createPickupOrder(): Promise<OrderDetail['id']> {
  const resp = await clientApi.post('/api/v1/orders', {
    data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuId, quantity: 1 }] },
  });
  const body = (await resp.json()) as ApiResponse<{ id: number }>;
  expect(body.code, `下单失败: ${body.message}`).toBe(0);
  return body.data!.id;
}

test.describe.serial('自提单自提点地址展示', () => {
  test('未配置自提点：地址卡回退社区地址，联系人为社区名称', async () => {
    test.skip(!skuId, '测试社区商品未就绪');
    const orderId = await createPickupOrder();
    const detail = (await (await clientApi.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<OrderDetail>;
    expect(detail.code).toBe(0);
    expect(detail.data!.address.detail, '未配置 pickup_address 应回退社区地址').toContain('AUTO-TEST 回退地址-');
    expect(detail.data!.address.contact_name, '地址卡联系人为社区名称').toContain('AUTO-TEST');
  });

  test('配置自提点后：新自提单详情展示自提点地址', async () => {
    test.skip(!skuId, '测试社区商品未就绪');
    const pickup = `AUTO-TEST 自提点-${Date.now() % 100000}`;
    // name 必填；更新为整表覆盖，需回填建社区时的全量字段（status 缺失会被清成 0 停业）
    const upd = (await (await adminApi.put(`/api/v1/admin/communities/${communityId}`, {
      data: {
        name: `AUTO-TEST 自提点测试社区`,
        address: `AUTO-TEST 回退地址-33`,
        longitude: 114.3,
        latitude: 30.5,
        contact_phone: '13900001111',
        pickup_address: pickup,
        delivery_fee: 3,
        min_order_amount: 0,
        status: 1,
      },
    })).json()) as ApiResponse;
    expect(upd.code, `更新社区自提点失败: ${upd.message}`).toBe(0);

    const orderId = await createPickupOrder();
    const detail = (await (await clientApi.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<OrderDetail>;
    expect(detail.code).toBe(0);
    expect(detail.data!.address.detail, '配置后应展示自提点地址').toBe(pickup);
  });
});
