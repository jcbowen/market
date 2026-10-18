import { test, expect } from '@playwright/test';
import { newAdminContext, pickSku, ensureStock, assertOk, type ApiResponse } from '../helpers/api';

/**
 * 模块 14：POS 收银台
 * 覆盖：开工/当前班次、散客到店/自提/配送单、挂单增删查、
 *       班次记录、交班、交班后无班次下单被拒、空明细拒绝、
 *       到店单终态（现金收款即自动完结，无「已支付」停留态）
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

/** 管理端订单详情（gateway/admin.go GetAdminOrder：订单字段与 items/status_logs 平铺返回） */
interface AdminOrderDetail {
  id: number;
  order_no?: string;
  status?: number;
  status_text?: string;
  delivery_type?: number;
  pay_time?: string;
  complete_time?: string;
  status_logs?: { from_status: number; to_status: number; operator_type?: string; remark?: string; created_at?: string }[];
  items?: unknown[];
}

test.describe.serial('POS 收银台', () => {
  let heldId = 0;
  /** 本轮到店单 ID，供终态用例二次读详情用（建单响应不含状态） */
  let inStoreOrderId = 0;

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
    // 建单响应只回 id/order_no/pay_amount 等，不含状态，终态需二次读详情（见下一用例）
    inStoreOrderId = order?.id ?? 0;
  });

  test('到店单收款即自动完结：终态 5、时间与状态日志逐项匹配', async () => {
    test.skip(!inStoreOrderId, '到店单未创建');
    const body = (await (await api.get(`/api/v1/admin/orders/${inStoreOrderId}`)).json()) as ApiResponse<AdminOrderDetail>;
    const detail = assertOk(body, '到店单详情');

    // 现金收款成功后同事务终态化：不存在「已支付」停留态，后台无需核销/完成
    expect(detail.status, '到店单应直接落到已完成(5)而非已支付(2)').toBe(5);
    expect(detail.status_text).toBe('已完成');
    expect(detail.pay_time, 'pay_time 应非空').toBeTruthy();
    expect(detail.complete_time, 'complete_time 应非空').toBeTruthy();
    expect(detail.delivery_type).toBe(3);

    const logs = detail.status_logs ?? [];
    expect(logs.length, '应记录创建→收款→自动完结三段流转').toBeGreaterThanOrEqual(3);
    const last = logs[logs.length - 1];
    const prev = logs[logs.length - 2];
    expect([prev.from_status, prev.to_status], '倒数第二段应为 1→2').toEqual([1, 2]);
    expect(prev.remark, '收款日志文案取自 payment.go').toBe('门店现金收款');
    expect([last.from_status, last.to_status], '末段应为 2→5').toEqual([2, 5]);
    expect(last.remark, '自动完结日志文案取自 order.go').toBe('到店订单收款完成，自动完结');
    // 完结时间与收款时间同源（同一 now 变量），无二次写入
    expect(last.created_at, '末段流转时间与 pay_time 同源').toBe(detail.pay_time);
    expect(detail.complete_time, '完结时间应等于收款时间').toBe(detail.pay_time);
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
