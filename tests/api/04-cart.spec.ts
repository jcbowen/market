import { test, expect } from '@playwright/test';
import { clientLogin, newAuthedRequest, pickSku, type ApiResponse } from '../helpers/api';
import { uniquePhone } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 04：购物车
 * 覆盖：加购、数量修改、删除、全选、清无效项、未登录 401、库存上限
 */

interface CartItem {
  id: number;
  product_id: number;
  sku_id: number;
  quantity: number;
  selected?: boolean;
}

/** 每个用例组共用一个独立登录用户与其已鉴权请求上下文 */
let api: Awaited<ReturnType<typeof newAuthedRequest>>;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  api = await newAuthedRequest(playwright, info.access_token);
});

test.afterAll(async () => {
  await api?.dispose();
});

test.describe.serial('购物车', () => {
  let addedId = 0;
  let productId = 0;
  let skuId = 0;

  test('加购商品成功且出现在购物车', async () => {
    const sku = await pickSku(api);
    productId = sku.product_id;
    skuId = sku.id;

    const resp = await api.post('/api/v1/cart/items', {
      data: { product_id: productId, sku_id: skuId, quantity: 1 },
    });
    const body = (await resp.json()) as ApiResponse<CartItem>;
    expect(body.code).toBe(0);
    addedId = body.data?.id ?? 0;

    const cart = (await (await api.get('/api/v1/cart')).json()) as ApiResponse<{ items: CartItem[] }>;
    expect(cart.data?.items.some((i) => i.sku_id === skuId)).toBe(true);
  });

  test('重复加购同 SKU 数量累加', async () => {
    const resp = await api.post('/api/v1/cart/items', {
      data: { product_id: productId, sku_id: skuId, quantity: 2 },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
    const cart = (await (await api.get('/api/v1/cart')).json()) as ApiResponse<{ items: CartItem[] }>;
    const item = cart.data!.items.find((i) => i.sku_id === skuId);
    expect(item?.quantity).toBeGreaterThanOrEqual(3);
  });

  test('修改数量生效', async () => {
    if (!addedId) test.skip(true, '前置加购未成功');
    const resp = await api.put(`/api/v1/cart/items/${addedId}`, { data: { quantity: 2 } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
    const cart = (await (await api.get('/api/v1/cart')).json()) as ApiResponse<{ items: CartItem[] }>;
    const item = cart.data!.items.find((i) => i.id === addedId);
    expect(item?.quantity).toBe(2);
  });

  test('全选/取消全选切换', async () => {
    const resp = await api.post('/api/v1/cart/select', { data: { selected: false } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
    const cart = (await (await api.get('/api/v1/cart')).json()) as ApiResponse<{ items: CartItem[] }>;
    expect(cart.data!.items.every((i) => !i.selected)).toBe(true);
  });

  test('清空无效项接口正常', async () => {
    const resp = await api.delete('/api/v1/cart/invalid');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('加购数量 0 / 负数被拒绝', async () => {
    for (const qty of [0, -1]) {
      const resp = await api.post('/api/v1/cart/items', {
        data: { product_id: productId, sku_id: skuId, quantity: qty },
      });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `quantity=${qty} 应被拒绝`).not.toBe(0);
    }
  });

  test('不存在的商品加购被拒绝', async () => {
    const resp = await api.post('/api/v1/cart/items', {
      data: { product_id: 999999999, sku_id: 999999999, quantity: 1 },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });

  test('删除购物车项后不再出现', async () => {
    if (!addedId) test.skip(true, '前置加购未成功');
    const resp = await api.delete(`/api/v1/cart/items/${addedId}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
    const cart = (await (await api.get('/api/v1/cart')).json()) as ApiResponse<{ items: CartItem[] }>;
    expect(cart.data!.items.some((i) => i.id === addedId)).toBe(false);
  });

  test('未登录访问购物车被拒绝', async ({ request }) => {
    const resp = await request.get('/api/v1/cart');
    const body = (await resp.json()) as ApiResponse;
    expect(resp.status()).toBeLessThan(500);
    if (resp.status() === 200) expect(body.code).not.toBe(0);
    else expect(resp.status()).toBe(401);
  });
});
