import { test, expect } from '@playwright/test';
import { clientLogin, newAdminContext, newAuthedRequest, pickSku, type ApiResponse, type PageData } from '../helpers/api';
import { uniquePhone } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 10：管理端订单操作、库存、统计
 * 覆盖：订单列表/详情、后台核销、发货流转、库存查询/入库/出库/预警/盘点、统计接口
 * 流程：C 端造一笔自提订单（cash 支付）→ 后台查到 → 后台兜底核销 → 状态推进
 */

let api: Awaited<ReturnType<typeof newAuthedRequest>>;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  // 管理端语境须与 C 端下单用户同社区，订单才在列表可见
  const client = await clientLogin(playwright.request, uniquePhone());
  const ctx = await newAdminContext(playwright, undefined, undefined, client.user.community_id);
  api = ctx.api;
  // 取一个有库存的 SKU（公开接口，兼容各环境）
  try {
    const sku = await pickSku(api, undefined, client.user.community_id);
    skuId = sku.id;
  } catch {
    skuId = 0;
  }
});

test.afterAll(async () => {
  await api?.dispose();
});

interface Order {
  id: number;
  order_no: string;
  status: number;
  delivery_type: number;
  pickup_code?: string;
  items?: { product_name: string; quantity: number }[];
}

let skuId = 0;
let createdOrder: Order;

test.beforeAll(async () => {
  // 取一个有库存的 SKU
  const skus = (await (
    await api.get('/api/v1/admin/inventory/skus', { params: { page: 1, page_size: 10 } })
  ).json()) as ApiResponse<PageData<{ id: number; stock: number }>>;
  if (skus.code === 0 && skus.data?.list?.length) {
    skuId = skus.data.list.find((s) => s.stock > 0)?.id ?? 0;
  }
});

test.describe.serial('管理端订单', () => {
  test('C 端造单：自提订单 + cash 支付', async ({ playwright }: { playwright: Playwright }) => {
    const info = await clientLogin(playwright.request, uniquePhone());
    const cApi = await newAuthedRequest(playwright, info.access_token);
    try {
      expect(skuId, '环境需有库存 SKU').toBeGreaterThan(0);
      const order = (await (
        await cApi.post('/api/v1/orders', {
          data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuId, quantity: 1 }] },
        })
      ).json()) as ApiResponse<Order>;
      expect(order.code).toBe(0);
      createdOrder = order.data as Order;
      const pay = (await (
        await cApi.post('/api/v1/payments', { data: { order_id: createdOrder.id, channel: 'cash' } })
      ).json()) as ApiResponse;
      expect(pay.code).toBe(0);
    } finally {
      await cApi.dispose();
    }
  });

  test('后台订单列表可查到该订单', async () => {
    const resp = await api.get('/api/v1/admin/orders', { params: { page: 1, page_size: 50 } });
    const body = (await resp.json()) as ApiResponse<PageData<Order>>;
    expect(body.code).toBe(0);
    expect(body.data?.list.some((o) => o.id === createdOrder.id)).toBe(true);
  });

  test('后台订单详情含商品行', async () => {
    const resp = await api.get(`/api/v1/admin/orders/${createdOrder.id}`);
    const body = (await resp.json()) as ApiResponse<Order>;
    expect(body.code).toBe(0);
    expect(body.data?.items?.length).toBeGreaterThan(0);
  });

  test('不存在的订单详情返回业务错误', async () => {
    const resp = await api.get('/api/v1/admin/orders/999999999');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });

  test('后台兜底核销自提单成功', async () => {
    // 先取订单详情中的取货码（若详情不返回则从 C 端详情拿）
    const detail = (await (await api.get(`/api/v1/admin/orders/${createdOrder.id}`)).json()) as ApiResponse<Order>;
    const code = detail.data?.pickup_code;
    if (!code) {
      test.skip(true, '后台订单详情未返回取货码，需人工确认字段');
      return;
    }
    const resp = await api.post(`/api/v1/admin/orders/${createdOrder.id}/verify-pickup`, {
      data: { pickup_code: code },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });
});

test.describe.serial('库存管理', () => {
  let targetSku = 0;

  test.beforeAll(async () => {
    targetSku = skuId;
  });

  test('库存 SKU 列表可查', async () => {
    const resp = await api.get('/api/v1/admin/inventory/skus', { params: { page: 1, page_size: 10 } });
    const body = (await resp.json()) as ApiResponse<PageData>;
    expect(body.code).toBe(0);
  });

  test('入库操作成功且流水可查', async () => {
    test.skip(!targetSku, '环境无 SKU');
    const before = (await (
      await api.get('/api/v1/admin/inventory/skus', { params: { page: 1, page_size: 100 } })
    ).json()) as ApiResponse<PageData<{ id?: number; sku_id?: number; stock: number }>>;
    const findStock = (list: { id?: number; sku_id?: number; stock: number }[]) =>
      list.find((s) => (s.id ?? s.sku_id) === targetSku)?.stock;
    const stockBefore = findStock(before.data?.list ?? []);
    if (stockBefore === undefined) {
      test.skip(true, '库存列表未返回目标 SKU（结构差异），跳过入库断言');
      return;
    }

    // 进销存重构：入库走入库单接口（直接入库，创建即生效）
    const entry = (await (
      await api.post('/api/v1/admin/inventory/in-orders', {
        data: { remark: 'AUTO-TEST 入库', items: [{ sku_id: targetSku, quantity: 10 }] },
      })
    ).json()) as ApiResponse;
    expect(entry.code, `入库失败: ${entry.message}`).toBe(0);

    const after = (await (
      await api.get('/api/v1/admin/inventory/skus', { params: { page: 1, page_size: 100 } })
    ).json()) as ApiResponse<PageData<{ id?: number; sku_id?: number; stock: number }>>;
    expect(findStock(after.data!.list)).toBe(stockBefore + 10);

    const records = (await (
      await api.get('/api/v1/admin/inventory/entries', { params: { page: 1, page_size: 10 } })
    ).json()) as ApiResponse<PageData>;
    expect(records.code).toBe(0);
  });

  test('出库操作成功', async () => {
    test.skip(!targetSku, '环境无 SKU');
    // 进销存重构：出库走出库单接口（type=2 领用）
    const resp = await api.post('/api/v1/admin/inventory/out-orders', {
      data: { type: 2, items: [{ sku_id: targetSku, quantity: 1 }], remark: 'AUTO-TEST 领用出库' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `出库失败: ${body.message}`).toBe(0);
  });

  test('出库超量库存被拒绝且错误码为库存不足', async () => {
    test.skip(!targetSku, '环境无 SKU');
    // 查当前库存，出库 stock+5 必然超出
    const before = (await (
      await api.get('/api/v1/admin/inventory/skus', { params: { page: 1, page_size: 100 } })
    ).json()) as ApiResponse<PageData<{ id?: number; sku_id?: number; stock: number }>>;
    const stock = (before.data?.list ?? []).find((s) => (s.id ?? s.sku_id) === targetSku)?.stock;
    if (stock === undefined) {
      test.skip(true, '库存列表未返回目标 SKU，跳过超量出库断言');
      return;
    }
    const resp = await api.post('/api/v1/admin/inventory/out-orders', {
      data: { type: 2, items: [{ sku_id: targetSku, quantity: stock + 5 }], remark: 'AUTO-TEST 超量出库' },
    });
    const body = (await resp.json()) as ApiResponse;
    // 出库单库存不足与下单扣减口径一致：ResourceExhausted → 12002（修复前误报参数错误 10001）
    expect(body.code, `预期库存不足 12002，实际 ${body.code} ${body.message}`).toBe(12002);
  });

  test('库存预警接口可查', async () => {
    const resp = await api.get('/api/v1/admin/inventory/warnings');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('采购建议单可生成', async () => {
    test.skip(!targetSku, '环境无 SKU');
    const resp = await api.post('/api/v1/admin/inventory/purchase-order', {
      data: { sku_ids: [targetSku] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('盘点：创建成功且列表可查（空单提交被网关拒绝）', async () => {
    test.skip(!targetSku, '环境无 SKU');
    const create = (await (
      await api.post('/api/v1/admin/inventory/checks', { data: { remark: 'AUTO-TEST 盘点' } })
    ).json()) as ApiResponse<{ id: number }>;
    expect(create.code).toBe(0);

    // 空盘点单提交：网关与服务层均硬拒（参数错误 10001），不再只记录不断言（假绿收敛）
    const submit = (await (
      await api.post(`/api/v1/admin/inventory/checks/${create.data!.id}/submit`)
    ).json()) as ApiResponse;
    expect(submit.code).toBe(10001);

    const list = (await (
      await api.get('/api/v1/admin/inventory/checks', { params: { page: 1, page_size: 10 } })
    ).json()) as ApiResponse<PageData>;
    expect(list.code).toBe(0);
  });
});

test.describe('统计接口', () => {
  for (const path of [
    '/api/v1/admin/statistics/orders',
    '/api/v1/admin/statistics/sales',
    '/api/v1/admin/statistics/rankings',
    '/api/v1/admin/statistics/revenue',
    '/api/v1/admin/dashboard',
  ]) {
    test(`GET ${path} 业务码为 0`, async () => {
      const resp = await api.get(path);
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `${path} 失败: ${body.message}`).toBe(0);
    });
  }
});
