import { test, expect } from '@playwright/test';
import {
  assertOk,
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  type ApiResponse,
  type ClientLoginInfo,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 79：商品起订份数（min_buy）服务端校验
 * 覆盖：C 端低于起订量下单被拒、等于起订量放行、购物车不受起订量约束
 * 未覆盖：收银台代客单（source=2）豁免分支——需要真实班次上下文，本轮按代码约定不校验
 */

/** 起订份数：本模块造数统一用 5 份 */
const MIN_BUY = 5;

interface ProductCreated {
  product?: { id: number };
  id?: number;
  skus?: { id: number }[];
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let communityId = 0;
let buyerToken = '';
let categoryId = 0;
let productId = 0;
let skuId = 0;

/**
 * 创建新 C 端用户并完成社区绑定（首登绑社区、重登刷新 JWT 社区语境）
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

test.beforeAll(async ({ playwright }) => {
  const buyer = await newClientUser(playwright);
  buyerToken = buyer.access_token;
  communityId = buyer.user.community_id;
  const admin = await newAdminContext(playwright, undefined, undefined, communityId);
  adminApi = admin.api;

  const cat = assertOk(
    (await (
      await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('起订分类') } })
    ).json()) as ApiResponse<{ id: number }>,
    '创建分类',
  );
  categoryId = cat.id;

  const created = assertOk(
    (await (
      await adminApi.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name: autoName('起订整箱菜'),
          unit: '份',
          min_buy: MIN_BUY,
          is_multi_spec: false,
          sku_items: [{ price: 9.9, stock: 20, safety_stock: 1 }],
        },
      })
    ).json()) as ApiResponse<ProductCreated>,
    '创建起订商品',
  );
  productId = created.product?.id ?? created.id ?? 0;
  expect(productId).toBeGreaterThan(0);

  const on = (await (
    await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })
  ).json()) as ApiResponse;
  expect(on.code, `上架失败: ${on.message}`).toBe(0);

  const detail = assertOk(
    (await (await adminApi.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<ProductCreated>,
    '商品详情',
  );
  skuId = detail.skus?.[0]?.id ?? 0;
  expect(skuId).toBeGreaterThan(0);
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

test.describe.serial('起订份数校验', () => {
  test('1. 低于起订量下单被拒并给出起订份数提示', async ({ playwright }) => {
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = (await (
        await buyerApi.post('/api/v1/orders', {
          data: { delivery_type: 1, address_id: 0, community_id: communityId, items: [{ sku_id: skuId, quantity: MIN_BUY - 1 }] },
        })
      ).json()) as ApiResponse;
      expect(body.code, '低于起订量应下单失败').not.toBe(0);
      expect(body.message, '提示需带上起订份数').toContain(`最低购买 ${MIN_BUY} 份`);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('2. 等于起订量下单放行，取消后库存归还', async ({ playwright }) => {
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = (await (
        await buyerApi.post('/api/v1/orders', {
          data: { delivery_type: 1, address_id: 0, community_id: communityId, items: [{ sku_id: skuId, quantity: MIN_BUY }] },
        })
      ).json()) as ApiResponse<{ id: number }>;
      expect(body.code, `等于起订量应可下单: ${body.message}`).toBe(0);
      const orderId = body.data?.id ?? 0;
      expect(orderId).toBeGreaterThan(0);

      const cancel = (await (
        await buyerApi.put(`/api/v1/orders/${orderId}/cancel`)
      ).json()) as ApiResponse;
      expect(cancel.code, `取消订单失败: ${cancel.message}`).toBe(0);

      const skus = (await (await adminApi.get('/api/v1/skus', { params: { product_id: productId } })).json()) as
        ApiResponse<{ id: number; stock: number }[]>;
      expect(skus.data?.find((s) => s.id === skuId)?.stock, '取消后库存应回到造数时的 20').toBe(20);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('3. 购物车不受起订量约束（可先放 1 件凑单）', async ({ playwright }) => {
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const add = (await (
        await buyerApi.post('/api/v1/cart/items', {
          data: { product_id: productId, sku_id: skuId, quantity: 1, community_id: communityId },
        })
      ).json()) as ApiResponse<{ items: { sku_id: number; quantity: number }[] }>;
      expect(add.code, `低于起订量加购应放行: ${add.message}`).toBe(0);
      expect(add.data?.items.some((i) => i.sku_id === skuId && i.quantity === 1)).toBe(true);

      const remove = (await (
        await buyerApi.delete(`/api/v1/cart/items/${skuId}`)
      ).json()) as ApiResponse;
      expect(remove.code).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });
});
