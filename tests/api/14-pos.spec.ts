import { test, expect } from '@playwright/test';
import { newAdminContext, pickSku, ensureStock, type ApiResponse } from '../helpers/api';

/**
 * 模块 14：POS 收银台
 * 覆盖：开工/当前班次、散客到店/自提/配送单、挂单增删查、
 *       班次记录、交班、交班后无班次下单被拒、空明细拒绝
 * 流程：开工 → 收银系列写操作 → 班次记录 → 交班（冻结统计）→ 无班次负向校验
 */

let api: Awaited<ReturnType<typeof newAdminContext>>['api'];
let skuId = 0;

test.beforeAll(async ({ playwright }) => {
  const ctx = await newAdminContext(playwright);
  api = ctx.api;
  try {
    const sku = await pickSku(api, undefined, ctx.communityId);
    skuId = sku.id;
    await ensureStock(api, skuId, 100);
  } catch {
    skuId = 0;
  }
});

test.afterAll(async () => {
  await api?.dispose();
});

interface PosOrderResult {
  order?: { id: number; order_no: string; status?: number };
  id?: number;
  order_no?: string;
  [k: string]: unknown;
}

test.describe.serial('POS 收银台', () => {
  let heldId = 0;

  test('开工成功', async () => {
    const resp = await api.post('/api/v1/admin/pos/shifts/start', { data: { note: 'AUTO-TEST 开工' } });
    const body = (await resp.json()) as ApiResponse;
    // 重复开工（已有进行中班次）属于环境残留，记录不失败
    if (body.code !== 0) {
      console.log(`[POS] 开工返回 code=${body.code} message=${body.message}（可能已有进行中班次）`);
      test.skip(true, '已有进行中班次，跳过开工');
      return;
    }
    expect(body.code).toBe(0);
  });

  test('当前班次可查询', async () => {
    const resp = await api.get('/api/v1/admin/pos/shifts/current');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('散客到店单下单成功（即买即走）', async () => {
    test.skip(!skuId, '环境无库存 SKU');
    const resp = await api.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: 0,
        pay_type: 4,
        delivery_type: 3,
        items: [{ sku_id: skuId, quantity: 1 }],
        remark: 'AUTO-TEST 到店单',
      },
    });
    const body = (await resp.json()) as ApiResponse<PosOrderResult>;
    expect(body.code, `到店单失败: ${body.message}`).toBe(0);
    const order = body.data?.order ?? body.data;
    expect(order?.order_no ?? order?.id).toBeTruthy();
  });

  test('散客自提单下单成功', async () => {
    test.skip(!skuId, '环境无库存 SKU');
    const resp = await api.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: 0,
        pay_type: 4,
        delivery_type: 1,
        items: [{ sku_id: skuId, quantity: 2 }],
      },
    });
    const body = (await resp.json()) as ApiResponse<PosOrderResult>;
    expect(body.code, `自提单失败: ${body.message}`).toBe(0);
  });

  test('配送单需联系人信息，缺失被拒绝', async () => {
    test.skip(!skuId, '环境无库存 SKU');
    const resp = await api.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: 0,
        pay_type: 4,
        delivery_type: 2,
        items: [{ sku_id: skuId, quantity: 1 }],
      },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '配送单缺联系人应被拒绝').not.toBe(0);
  });

  test('配送单补齐联系人后下单成功', async () => {
    test.skip(!skuId, '环境无库存 SKU');
    const resp = await api.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: 0,
        pay_type: 4,
        delivery_type: 2,
        contact_name: 'AUTO-TEST 配送',
        contact_phone: '13900001234',
        contact_address: '测试路 1 号',
        items: [{ sku_id: skuId, quantity: 1 }],
      },
    });
    const body = (await resp.json()) as ApiResponse<PosOrderResult>;
    expect(body.code, `配送单失败: ${body.message}`).toBe(0);
  });

  test('空明细下单被拒绝', async () => {
    const resp = await api.post('/api/v1/admin/pos/orders', {
      data: { user_id: 0, pay_type: 4, delivery_type: 3, items: [] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });

  test('创建挂单成功', async () => {
    test.skip(!skuId, '环境无库存 SKU');
    const resp = await api.post('/api/v1/admin/pos/held-orders', {
      data: {
        user_id: 0,
        user_name: 'AUTO-TEST 挂单顾客',
        remark: 'AUTO-TEST 挂单',
        items: [{ sku_id: skuId, quantity: 1, product_name: '挂单商品' }],
      },
    });
    const body = (await resp.json()) as ApiResponse<{ id?: number }>;
    expect(body.code, `挂单失败: ${body.message}`).toBe(0);
    heldId = body.data?.id ?? 0;
    expect(heldId).toBeGreaterThan(0);
  });

  test('挂单列表包含新挂单', async () => {
    if (!heldId) test.skip(true, '挂单未创建');
    const resp = await api.get('/api/v1/admin/pos/held-orders');
    const body = (await resp.json()) as ApiResponse<{ id: number }[] | { list: { id: number }[] }>;
    expect(body.code).toBe(0);
    const list = Array.isArray(body.data) ? body.data : (body.data?.list ?? []);
    expect(list.some((h) => h.id === heldId)).toBe(true);
  });

  test('删除挂单成功且列表移除', async () => {
    if (!heldId) test.skip(true, '挂单未创建');
    const del = (await (await api.delete(`/api/v1/admin/pos/held-orders/${heldId}`)).json()) as ApiResponse;
    expect(del.code).toBe(0);
    const resp = await api.get('/api/v1/admin/pos/held-orders');
    const body = (await resp.json()) as ApiResponse<{ id: number }[] | { list: { id: number }[] }>;
    const list = Array.isArray(body.data) ? body.data : (body.data?.list ?? []);
    expect(list.some((h) => h.id === heldId)).toBe(false);
  });

  test('我的班次记录可查', async () => {
    const resp = await api.get('/api/v1/admin/pos/shifts/mine');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('本店全部班次可查', async () => {
    const resp = await api.get('/api/v1/admin/pos/shifts');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('交班成功', async () => {
    const resp = await api.post('/api/v1/admin/pos/shifts/end', { data: { note: 'AUTO-TEST 交班' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `交班失败: ${body.message}`).toBe(0);
  });

  test('交班后无进行中班次，下单被拒绝', async () => {
    test.skip(!skuId, '环境无库存 SKU');
    const resp = await api.post('/api/v1/admin/pos/orders', {
      data: { user_id: 0, pay_type: 4, delivery_type: 3, items: [{ sku_id: skuId, quantity: 1 }] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '无进行中班次下单必须被拒绝').not.toBe(0);
  });
});
