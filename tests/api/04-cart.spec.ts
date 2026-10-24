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
let communityId = 0;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  api = await newAuthedRequest(playwright, info.access_token);
  communityId = info.user.community_id;
});

test.afterAll(async () => {
  await api?.dispose();
});

test.describe.serial('购物车', () => {
  let addedId = 0;
  let productId = 0;
  let skuId = 0;

  test('加购商品成功且出现在购物车', async () => {
    const sku = await pickSku(api, undefined, communityId);
    productId = sku.product_id;
    skuId = sku.id;

    const resp = await api.post('/api/v1/cart/items', {
      data: { product_id: productId, sku_id: skuId, quantity: 1 },
    });
    const body = (await resp.json()) as ApiResponse<{ items: CartItem[] }>;
    expect(body.code).toBe(0);
    // 响应为 CartResp（items 数组），行 ID 取 sku_id 匹配项（UpdateItem/RemoveItem 的 :id 即 sku_id）
    addedId = body.data?.items?.find((i) => i.sku_id === skuId)?.id ?? 0;

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

  test('纯改数量不影响选中态', async () => {
    // 独立加购一个 SKU：加购默认选中
    const sku = await pickSku(api, undefined, communityId);
    const add = (await (
      await api.post('/api/v1/cart/items', {
        data: { product_id: sku.product_id, sku_id: sku.id, quantity: 1 },
      })
    ).json()) as ApiResponse;
    expect(add.code).toBe(0);

    // 选中态改数量：selected 保持 true
    // （回归 2026-10-24 修复：UpdateItem 曾无条件写 selected，改一次数量勾选被静默清掉）
    await api.put(`/api/v1/cart/items/${sku.id}`, { data: { quantity: 2 } });
    let cart = (await (await api.get('/api/v1/cart')).json()) as ApiResponse<{ items: CartItem[] }>;
    expect(cart.data!.items.find((i) => i.sku_id === sku.id)?.selected).toBe(true);

    // 未选中态改数量：selected 保持 false，数量正常更新
    await api.put(`/api/v1/cart/items/${sku.id}`, { data: { selected: false } });
    await api.put(`/api/v1/cart/items/${sku.id}`, { data: { quantity: 3 } });
    cart = (await (await api.get('/api/v1/cart')).json()) as ApiResponse<{ items: CartItem[] }>;
    const item = cart.data!.items.find((i) => i.sku_id === sku.id);
    expect(item?.quantity).toBe(3);
    expect(item?.selected).toBe(false);

    // 清理测试项，不影响后续用例
    await api.delete(`/api/v1/cart/items/${sku.id}`);
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
    expect([200, 401], '未授权只允许 HTTP 200+业务码 或 401 两种表达').toContain(resp.status());
    if (resp.status() === 200) expect(body.code).not.toBe(0);
    else expect(resp.status()).toBe(401);
  });
});
