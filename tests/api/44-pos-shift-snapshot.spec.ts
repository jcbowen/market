import { test, expect } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  pickSku,
  ensureStock,
  assertOk,
  type ApiResponse,
  type SkuRef,
} from '../helpers/api';

/**
 * 模块 44：POS 交班统计快照 + 重新开工
 * 覆盖：
 *   - 班次实时统计：班内现金单按 pay_type 分渠道汇总（income/refund/order_count/cash_total）
 *   - 会员单退款闭环计入班次退款统计（现金渠道），cash_total = 现金收款 - 现金退款
 *   - 交班冻结快照：statistics 与实时统计一致、end_time 落库
 *   - 快照冻结不可变：shifts 列表回读已结束班次统计与交班时一致
 *   - 交班后重新开工新班次（id 递增），收银恢复正常
 * 说明：POS 代选券/散客清零已在 28 号覆盖；本模块聚焦班次统计口径与生命周期
 */

interface StatItem {
  pay_type: number;
  amount: number;
  order_count: number;
}

interface ShiftStatistics {
  income: StatItem[];
  refund: StatItem[];
  total_income: number;
  total_refund: number;
  order_count: number;
  cash_total: number;
}

interface ShiftInfo {
  id: number;
  status: number;
  start_time?: string;
  end_time?: string;
  statistics?: ShiftStatistics;
  [k: string]: unknown;
}

interface PosOrderResp {
  id: number;
  order_no: string;
  pay_amount: number;
  paid: boolean;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let sku: SkuRef | null = null;

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  try {
    sku = await pickSku(adminApi);
    await ensureStock(adminApi, sku.id, 100);
  } catch {
    sku = null;
  }
  const member = await clientLogin(playwright.request, `139${String(Date.now()).slice(-8)}`);
  globalThis.__pos44MemberId = member.user.id;

  // 处理遗留班次：已有进行中班次时先交班，保证本模块从干净班次开始
  const cur = (await (await adminApi.get('/api/v1/admin/pos/shifts/current')).json()) as ApiResponse<ShiftInfo>;
  if ((cur.data?.id ?? 0) > 0) {
    const end = (await (
      await adminApi.post('/api/v1/admin/pos/shifts/end', { data: { note: 'AUTO-TEST 44 清理遗留班次' } })
    ).json()) as ApiResponse;
    console.log(`[44] 清理遗留班次 code=${end.code}`);
  }
  const start = (await (
    await adminApi.post('/api/v1/admin/pos/shifts/start', { data: { note: 'AUTO-TEST 44 开工' } })
  ).json()) as ApiResponse<ShiftInfo>;
  expect(start.code, `开工失败: ${start.message}`).toBe(0);
});

test.afterAll(async () => {
  try {
    // 收尾交班，恢复环境默认（无进行中班次）
    await adminApi?.post('/api/v1/admin/pos/shifts/end', { data: { note: 'AUTO-TEST 44 收尾交班' } });
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

declare global {
  // eslint-disable-next-line no-var
  var __pos44MemberId: number;
}

/** 查询当前进行中班次（实时统计） */
async function getCurrentShift(): Promise<ShiftInfo> {
  const body = (await (await adminApi.get('/api/v1/admin/pos/shifts/current')).json()) as ApiResponse<ShiftInfo>;
  return assertOk(body, '当前班次查询');
}

/** 从班次统计中取指定渠道条目 */
function statOf(items: StatItem[] | undefined, payType: number): StatItem | undefined {
  return items?.find((i) => i.pay_type === payType);
}

test.describe.serial('POS 交班统计快照', () => {
  let shiftId = 0;
  let memberOrderAmount = 0;
  let guestOrderAmount = 0;
  let refundOrderId = 0;

  test('1. 班内两笔现金单（会员单 + 散客单）', async () => {
    test.skip(!sku, '环境无库存 SKU');
    const cur = await getCurrentShift();
    shiftId = cur.id;
    expect(shiftId).toBeGreaterThan(0);

    const memberResp = await adminApi.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: globalThis.__pos44MemberId,
        pay_type: 4,
        delivery_type: 3,
        items: [{ sku_id: sku!.id, quantity: 1 }],
        remark: 'AUTO-TEST 44 会员单',
      },
    });
    const memberBody = (await memberResp.json()) as ApiResponse<PosOrderResp>;
    expect(memberBody.code, `会员单失败: ${memberBody.message}`).toBe(0);
    memberOrderAmount = memberBody.data!.pay_amount;
    refundOrderId = memberBody.data!.id;

    const guestResp = await adminApi.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: 0,
        pay_type: 4,
        delivery_type: 3,
        items: [{ sku_id: sku!.id, quantity: 2 }],
        remark: 'AUTO-TEST 44 散客单',
      },
    });
    const guestBody = (await guestResp.json()) as ApiResponse<PosOrderResp>;
    expect(guestBody.code, `散客单失败: ${guestBody.message}`).toBe(0);
    guestOrderAmount = guestBody.data!.pay_amount;
  });

  test('2. 会员单退款闭环（后台代客退款，退款计入班次）', async () => {
    if (!refundOrderId) test.skip(true, '会员单未创建');
    // POS 门店单（source=2）C 端只读，退款走后台代客退款（发起即执行）
    const refund = (await (
      await adminApi.post(`/api/v1/admin/orders/${refundOrderId}/refund`, { data: { reason: 'AUTO-TEST 44 班次退款' } })
    ).json()) as ApiResponse;
    expect(refund.code, `后台代客退款失败: ${refund.message}`).toBe(0);
  });

  test('3. 实时统计：收款/退款分渠道汇总，现金合计正确', async () => {
    if (!shiftId) test.skip(true, '班次未就绪');
    const stat = (await getCurrentShift()).statistics;
    expect(stat, '进行中班次应含实时统计').toBeTruthy();
    const income = statOf(stat!.income, 4);
    expect(income, '现金收款条目应存在').toBeTruthy();
    expect(income!.order_count, '班内现金收款应为 2 单').toBe(2);
    expect(income!.amount, '现金收款金额应为两单实付之和').toBeCloseTo(memberOrderAmount + guestOrderAmount, 2);

    const refund = statOf(stat!.refund, 4);
    expect(refund, '现金退款条目应存在').toBeTruthy();
    expect(refund!.amount, '退款金额应为会员单实付').toBeCloseTo(memberOrderAmount, 2);

    expect(stat!.total_income).toBeCloseTo(memberOrderAmount + guestOrderAmount, 2);
    expect(stat!.total_refund).toBeCloseTo(memberOrderAmount, 2);
    expect(stat!.cash_total, '现金合计 = 收款 - 退款（钱箱应交）').toBeCloseTo(guestOrderAmount, 2);
  });

  test('4. 交班成功：快照与实时统计一致', async () => {
    if (!shiftId) test.skip(true, '班次未就绪');
    const resp = await adminApi.post('/api/v1/admin/pos/shifts/end', { data: { note: 'AUTO-TEST 44 交班' } });
    const body = (await resp.json()) as ApiResponse<ShiftInfo>;
    expect(body.code, `交班失败: ${body.message}`).toBe(0);
    const info = body.data as ShiftInfo;
    expect(info.status, '交班后班次应为已结束(2)').toBe(2);
    expect(info.end_time, '交班时间应落库').toBeTruthy();
    expect(info.statistics?.total_income, '快照收款与实时统计一致').toBeCloseTo(memberOrderAmount + guestOrderAmount, 2);
    expect(info.statistics?.total_refund, '快照退款与实时统计一致').toBeCloseTo(memberOrderAmount, 2);
    expect(info.statistics?.cash_total, '快照现金合计一致').toBeCloseTo(guestOrderAmount, 2);
  });

  test('5. 快照冻结：列表回读已结束班次统计一致', async () => {
    if (!shiftId) test.skip(true, '班次未就绪');
    const body = (await (
      await adminApi.get('/api/v1/admin/pos/shifts', { params: { page: 1, page_size: 20 } })
    ).json()) as ApiResponse<{ list?: ShiftInfo[] }>;
    const list = body.data?.list ?? [];
    const ended = list.find((s) => s.id === shiftId);
    expect(ended, '班次列表应含本班次').toBeTruthy();
    expect(ended!.status).toBe(2);
    expect(ended!.statistics?.total_income, '回读快照收款一致').toBeCloseTo(memberOrderAmount + guestOrderAmount, 2);
    expect(ended!.statistics?.total_refund, '回读快照退款一致').toBeCloseTo(memberOrderAmount, 2);
    expect(ended!.statistics?.cash_total).toBeCloseTo(guestOrderAmount, 2);
  });

  test('6. 交班后重新开工新班次', async () => {
    const start = (await (
      await adminApi.post('/api/v1/admin/pos/shifts/start', { data: { note: 'AUTO-TEST 44 新班次' } })
    ).json()) as ApiResponse<ShiftInfo>;
    expect(start.code, `重新开工失败: ${start.message}`).toBe(0);
    const cur = await getCurrentShift();
    expect(cur.id, '当前班次应切换为新班次').not.toBe(shiftId);
    expect(cur.status).toBe(1);
  });

  test('7. 新班次收银恢复正常', async () => {
    test.skip(!sku, '环境无库存 SKU');
    const resp = await adminApi.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: 0,
        pay_type: 4,
        delivery_type: 3,
        items: [{ sku_id: sku!.id, quantity: 1 }],
        remark: 'AUTO-TEST 44 新班次收银',
      },
    });
    const body = (await resp.json()) as ApiResponse<PosOrderResp>;
    expect(body.code, `新班次收银失败: ${body.message}`).toBe(0);
    // 新班次实时统计应只含本单
    const stat = (await getCurrentShift()).statistics;
    expect(statOf(stat?.income, 4)?.order_count, '新班次统计独立计算').toBe(1);
  });
});
