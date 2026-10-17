import { test, expect } from '@playwright/test';
import { clientLogin, newAuthedRequest, type ApiResponse, type PageData } from '../helpers/api';
import { uniquePhone } from '../helpers/data';
import type { APIRequestContext, Playwright } from '@playwright/test';

/**
 * 模块 57：跨社区购物车与订单（2026-10-17）
 * 覆盖：购物车不传社区返回全部社区商品、显式传社区过滤、
 *       下单显式指定 community_id（订单归属/履约社区）、
 *       订单列表返回 community_id/community_name、免配送费试算指定社区
 *
 * 环境要求：至少两个营业中社区，且第二社区有在售商品（不满足时用例跳过）
 */

interface CommunityBrief {
  id: number;
  name: string;
  status: number;
  enable_self_pickup?: boolean;
}

interface CartItemBrief {
  id: number;
  sku_id: number;
  quantity: number;
  community_id: number;
  selected: boolean;
}

/** 购物车响应（gateway GetCart/AddItem：items + 汇总字段） */
interface CartResp {
  items: CartItemBrief[];
  total_amount: number;
  total_count: number;
  selected_count: number;
}

interface OrderBrief {
  id: number;
  community_id: number;
  community_name?: string;
  order_no: string;
  status: number;
}

const json = async <T>(resp: { json: () => Promise<unknown> }) => (await resp.json()) as ApiResponse<T>;

/** 取目标社区（非用户归属社区）的在售 SKU；环境不满足时返回 null */
async function pickOtherCommunitySku(
  api: APIRequestContext,
  homeCommunityId: number,
): Promise<{ community: CommunityBrief; skuId: number; productId: number } | null> {
  const commsBody = await json<PageData<CommunityBrief>>(await api.get('/api/v1/communities', { params: { page: 1, page_size: 50 } }));
  const communities = commsBody.data?.list ?? [];
  const candidates = communities.filter(
    (c) => c.id !== homeCommunityId && c.status === 1 && c.enable_self_pickup !== false,
  );
  for (const community of candidates) {
    const productsBody = await json<PageData<{ id: number }>>(
      await api.get('/api/v1/products', { params: { community_id: community.id, page: 1, page_size: 10, status: 1 } }),
    );
    for (const p of productsBody.data?.list ?? []) {
      const skusBody = await json<{ id: number; product_id: number; stock: number; status: number }[]>(
        await api.get('/api/v1/skus', { params: { product_id: p.id } }),
      );
      const sku = (skusBody.data ?? []).find((s) => s.status === 1 && s.stock > 0);
      if (sku) return { community, skuId: sku.id, productId: sku.product_id };
    }
  }
  return null;
}

test.describe.serial('跨社区购物车与订单', () => {
  let api: Awaited<ReturnType<typeof newAuthedRequest>>;
  let homeCommunityId: number;
  let target: { community: CommunityBrief; skuId: number; productId: number } | null;
  let createdOrderId = 0;
  let cartItemIds: number[] = [];

  test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
    const info = await clientLogin(playwright.request, uniquePhone());
    api = await newAuthedRequest(playwright, info.access_token);
    homeCommunityId = info.user.community_id;
    target = await pickOtherCommunitySku(api, homeCommunityId);
  });

  test.afterAll(async () => {
    // 清理：取消测试订单、移除测试购物车项
    if (createdOrderId > 0) {
      await api.put(`/api/v1/orders/${createdOrderId}/cancel`, { data: { reason: 'AUTO-TEST 清理' } });
    }
    for (const id of cartItemIds) {
      await api.delete(`/api/v1/cart/items/${id}`);
    }
    await api?.dispose();
  });

  test('环境具备两个营业中社区且目标社区有在售商品', async () => {
    expect(homeCommunityId).toBeGreaterThan(0);
    expect(target).not.toBeNull();
  });

  test('购物车不传 community_id 返回全部社区商品', async () => {
    test.skip(!target, '环境不满足：无第二营业社区或目标社区无在售商品');
    const addResp = await json<CartResp>(
      await api.post('/api/v1/cart/items', {
        data: { product_id: target!.productId, sku_id: target!.skuId, quantity: 2, community_id: target!.community.id },
      }),
    );
    expect(addResp.code).toBe(0);
    cartItemIds = (addResp.data?.items ?? []).filter((i) => i.community_id === target!.community.id).map((i) => i.id);
    expect(cartItemIds.length).toBeGreaterThan(0);

    // 不传 community_id：跨社区商品可见（旧逻辑回退 JWT 社区会查不到）
    const all = await json<CartResp>(await api.get('/api/v1/cart'));
    expect(all.code).toBe(0);
    const crossItem = (all.data?.items ?? []).find(
      (i) => i.community_id === target!.community.id && i.sku_id === target!.skuId,
    );
    expect(crossItem).toBeTruthy();
    expect(crossItem!.quantity).toBe(2);
    // 金额口径：total_amount 为选中商品合计
    expect(all.data?.total_amount).toBeGreaterThan(0);

    // 显式传用户归属社区：目标社区商品被过滤
    const scoped = await json<CartResp>(
      await api.get('/api/v1/cart', { params: { community_id: homeCommunityId } }),
    );
    expect(scoped.code).toBe(0);
    const leaked = (scoped.data?.items ?? []).some((i) => i.community_id === target!.community.id);
    expect(leaked).toBe(false);
  });

  test('下单显式指定 community_id：订单归属结算社区', async () => {
    test.skip(!target, '环境不满足：无第二营业社区或目标社区无在售商品');
    const createResp = await json<OrderBrief>(
      await api.post('/api/v1/orders', {
        data: {
          delivery_type: 1, // 自提（目标社区已过滤 enable_self_pickup）
          address_id: 0,
          community_id: target!.community.id,
          remark: 'AUTO-TEST 跨社区下单',
          items: [{ sku_id: target!.skuId, quantity: 2 }],
        },
      }),
    );
    expect(createResp.code).toBe(0);
    createdOrderId = createResp.data!.id;
    expect(createResp.data!.community_id).toBe(target!.community.id);
  });

  test('订单列表返回所属社区 community_id/community_name', async () => {
    test.skip(!target || createdOrderId <= 0, '前置下单未执行');
    const listResp = await json<PageData<OrderBrief>>(
      await api.get('/api/v1/orders', { params: { page: 1, page_size: 10 } }),
    );
    expect(listResp.code).toBe(0);
    const order = (listResp.data?.list ?? []).find((o) => o.id === createdOrderId);
    expect(order).toBeTruthy();
    expect(order!.community_id).toBe(target!.community.id);
    expect(order!.community_name).toBe(target!.community.name);
  });

  test('免配送费试算支持指定 community_id', async () => {
    test.skip(!target, '环境不满足：无第二营业社区或目标社区无在售商品');
    const previewResp = await json<{ free_shipping: boolean; threshold: number; delivery_fee: number }>(
      await api.post('/api/v1/orders/free-shipping/preview', {
        data: {
          community_id: target!.community.id,
          items: [{ sku_id: target!.skuId, quantity: 2 }],
        },
      }),
    );
    expect(previewResp.code).toBe(0);
    expect(previewResp.data).toBeTruthy();
  });

  test('跨社区下单不传 community_id 时按用户归属社区（回归）', async () => {
    test.skip(!target, '环境不满足：无第二营业社区或目标社区无在售商品');
    const createResp = await json<OrderBrief>(
      await api.post('/api/v1/orders', {
        data: {
          delivery_type: 1,
          address_id: 0,
          remark: 'AUTO-TEST 默认社区下单',
          items: [{ sku_id: target!.skuId, quantity: 1 }],
        },
      }),
    );
    expect(createResp.code).toBe(0);
    expect(createResp.data!.community_id).toBe(homeCommunityId);
    // 清理该回归验证订单
    const cancelResp = await json(await api.put(`/api/v1/orders/${createResp.data!.id}/cancel`, { data: { reason: 'AUTO-TEST 清理' } }));
    expect(cancelResp.code).toBe(0);
  });
});
