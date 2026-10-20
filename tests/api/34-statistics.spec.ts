import { test, expect, type Playwright } from '@playwright/test';
import { newAdminContext, type ApiResponse } from '../helpers/api';

/**
 * 模块 34：数据统计五接口
 * 覆盖：dashboard 仪表盘聚合（字段完备 + 数值非负）；orders 订单统计（各状态之和 = 总数）；
 *       sales 销售统计（客单价 ≈ 支付总额/单数且按分取整，实付 ≤ 原价总额）；
 *       rankings 商品销量排行（结构合法 + 降序）；revenue 营收报表（日期升序 + 数值非负）
 * 口径：统计按今日/区间计算，仅验证结构与数值自洽，不锁定具体数值（环境数据持续增长）
 * 附：统计口径与越权防护（2026-10-17 全平台统计页配套，见文末 describe）
 */

interface Dashboard {
  order_count: number;
  sale_amount: number;
  user_count: number;
  product_count: number;
  pending_ship: number;
  refunding: number;
}

interface OrderStats {
  total_orders: number;
  pending_pay: number;
  pending_ship: number;
  pending_receive: number;
  completed: number;
  refunding: number;
  cancelled: number;
}

interface SalesStats {
  total_orders: number;
  total_amount: number;
  pay_amount: number;
  avg_order_amount: number;
}

interface RankingItem {
  product_id: number;
  product_name: string;
  sales_count: number;
  sales_amount: number;
}

interface RevenueItem {
  date: string;
  order_count: number;
  revenue: number;
}

/** 今日与区间起止（跨月安全）；本地墙钟日期（不可用 toISOString——UTC 日期在 0:00-8:00 会落后一天，口径同 59-coupon） */
function dateRange(): { start: string; end: string } {
  const now = new Date();
  const p = (n: number) => String(n).padStart(2, '0');
  const fmt = (d: Date) => `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
  const start = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000);
  return { start: fmt(start), end: fmt(now) };
}

let api: Awaited<ReturnType<typeof newAdminContext>>['api'];
/** 超管当前语境社区 ID（与 api 上下文携带的 X-Community-Id 同源，勿用 my/communities 列表首项推断） */
let adminCommunityId = 0;

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  api = admin.api;
  adminCommunityId = admin.communityId;
});

test.afterAll(async () => {
  await api?.dispose();
});

test.describe.serial('数据统计五接口', () => {
  const range = dateRange();
  const qs = `start_date=${range.start}&end_date=${range.end}`;

  test('dashboard：字段完备且数值非负', async () => {
    const resp = await api.get('/api/v1/admin/dashboard');
    const body = (await resp.json()) as ApiResponse<Dashboard>;
    expect(body.code).toBe(0);
    const d = body.data!;
    for (const key of ['order_count', 'sale_amount', 'user_count', 'product_count', 'pending_ship', 'refunding'] as const) {
      expect(d[key], `${key} 应存在`).toBeDefined();
      expect(d[key], `${key} 应非负`).toBeGreaterThanOrEqual(0);
    }
    // 今日订单数与待发货数逻辑自洽：今日单不可能超过待发货+今日完成等，仅验证非负与类型
    expect(typeof d.order_count).toBe('number');
    expect(typeof d.sale_amount).toBe('number');
  });

  test('orders：各状态订单数之和等于总单数', async () => {
    const resp = await api.get('/api/v1/admin/statistics/orders', { params: { start_date: range.start, end_date: range.end } });
    const body = (await resp.json()) as ApiResponse<OrderStats>;
    expect(body.code).toBe(0);
    const s = body.data!;
    const sum = s.pending_pay + s.pending_ship + s.pending_receive + s.completed + s.refunding + s.cancelled;
    expect(sum, `各状态之和 ${sum} 应等于总单数 ${s.total_orders}`).toBe(s.total_orders);
    expect(s.total_orders).toBeGreaterThanOrEqual(0);
  });

  test('sales：客单价与总额自洽', async () => {
    const resp = await api.get('/api/v1/admin/statistics/sales', {
      params: { start_date: range.start, end_date: range.end, granularity: 'day' },
    });
    const body = (await resp.json()) as ApiResponse<SalesStats>;
    expect(body.code).toBe(0);
    const s = body.data!;
    if (s.total_orders > 0) {
      expect(s.pay_amount, '有订单时支付总额应大于 0').toBeGreaterThan(0);
      const expectedAvg = s.pay_amount / s.total_orders;
      expect(Math.abs(s.avg_order_amount - expectedAvg), `客单价应≈支付总额/单数`).toBeLessThanOrEqual(0.01);
      // 客单价按"分"取整（2026-10-17 起）：最多两位小数，避免 5.0649999999999995 之类的浮点尾差
      expect(
        Math.abs(s.avg_order_amount * 100 - Math.round(s.avg_order_amount * 100)),
        '客单价应按分取整（最多两位小数）',
      ).toBeLessThan(1e-6);
      // 优惠/取消口径下实付不应超过原价总额
      expect(s.pay_amount).toBeLessThanOrEqual(s.total_amount + 0.001);
    } else {
      expect(s.pay_amount).toBe(0);
    }
  });

  test('rankings：结构合法且按销量降序', async () => {
    const resp = await api.get('/api/v1/admin/statistics/rankings', { params: { start_date: range.start, end_date: range.end } });
    const body = (await resp.json()) as ApiResponse<RankingItem[]>;
    expect(body.code).toBe(0);
    const list = body.data ?? [];
    for (const r of list) {
      expect(r.product_id, '排行项应有商品 ID').toBeGreaterThan(0);
      expect(r.product_name, '排行项应有商品名').toBeTruthy();
      expect(r.sales_count, '销量应非负').toBeGreaterThanOrEqual(0);
      expect(r.sales_amount, '销售额应非负').toBeGreaterThanOrEqual(0);
    }
    for (let i = 1; i < list.length; i++) {
      expect(
        list[i - 1].sales_count >= list[i].sales_count || list[i - 1].sales_amount >= list[i].sales_amount,
        `第 ${i - 1}→${i} 项应保持降序`,
      ).toBe(true);
    }
  });

  test('revenue：日期升序且数值非负', async () => {
    const resp = await api.get('/api/v1/admin/statistics/revenue', {
      params: { start_date: range.start, end_date: range.end, granularity: 'day' },
    });
    const body = (await resp.json()) as ApiResponse<RevenueItem[]>;
    expect(body.code).toBe(0);
    const list = body.data ?? [];
    for (const r of list) {
      expect(/^\d{4}-\d{2}-\d{2}$/.test(r.date), `日期格式应为 YYYY-MM-DD: ${r.date}`).toBe(true);
      expect(r.order_count, '订单数应非负').toBeGreaterThanOrEqual(0);
      expect(r.revenue, '营收应非负').toBeGreaterThanOrEqual(0);
    }
    for (let i = 1; i < list.length; i++) {
      expect(list[i].date > list[i - 1].date, `日期应升序：${list[i - 1].date} → ${list[i].date}`).toBe(true);
    }
  });
});

/**
 * 统计口径与越权防护（2026-10-17 全平台统计页配套）
 * 覆盖：超管 scope=all 全平台聚合口径 ≥ 本社区口径；超管 community_id 指定口径等于该社区默认口径；
 *       普通管理员携带 scope/community_id 传参一律被忽略（回落本社区），防止越权查看其他社区数据
 * 数据形态说明：环境可能只有一个社区有订单（此时全平台口径恰等于本社区口径），
 *               越权用例退化为"传参前后一致"断言；存在多社区订单时才追加全平台差异断言
 */
test.describe.serial('统计口径与越权防护', () => {
  const range = dateRange();
  const qs = `start_date=${range.start}&end_date=${range.end}`;
  /** 目标社区：超管当前语境社区，用于 community_id 指定口径对照 */
  let superCommunityId = 0;
  let staffCtx: Awaited<ReturnType<typeof newAdminContext>> | undefined;
  let staffUserId = 0;
  let staffRoleId = 0;

  test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
    // 超管当前社区：建员工走 api 上下文的 X-Community-Id 语境，角色须归属同一社区。
    // 不能用 my/communities 首项推断——该列表按创建倒序，往轮遗留的 AUTO-TEST 空壳社区会排在前面
    superCommunityId = adminCommunityId;
    expect(superCommunityId, '应能取到超管当前社区 ID').toBeGreaterThan(0);

    // 超管建最小权限测试角色（统计查看 + 订单查看）与测试员工，用完即删
    // 注意：角色一律社区私有，超管建角色必须显式传当前社区的 community_id
    const stamp = Date.now();
    const roleResp = await api.post('/api/v1/admin/roles', {
      data: {
        name: `AUTO-TEST 统计口径角色 ${stamp}`,
        code: `stats_scope_${stamp}`,
        permissions: ['statistics:view', 'order:view'],
        community_id: superCommunityId,
      },
    });
    const roleBody = (await roleResp.json()) as ApiResponse<{ id?: number; community_id?: number }>;
    expect(roleBody.code, `建角色失败：${roleBody.message}`).toBe(0);
    staffRoleId = roleBody.data?.id ?? 0;
    expect(staffRoleId, '建角色应返回角色 ID').toBeGreaterThan(0);
    expect(roleBody.data?.community_id, '测试角色应归属当前社区').toBe(superCommunityId);

    const username = `stats_scope_${stamp}`;
    const staffResp = await api.post('/api/v1/admin/admins', {
      data: {
        username,
        password: 'Stats@2026',
        real_name: 'AUTO-TEST 统计口径员工',
        phone: `139${String(stamp).slice(-8)}`,
        role_ids: [staffRoleId],
      },
    });
    const staffBody = (await staffResp.json()) as ApiResponse<{ id?: number }>;
    expect(staffBody.code, `建员工失败：${staffBody.message}`).toBe(0);
    staffUserId = staffBody.data?.id ?? 0;
    expect(staffUserId, '建员工应返回员工 ID').toBeGreaterThan(0);

    // 员工语境：与超管同一社区（建员工时取超管当前 X-Community-Id）
    staffCtx = await newAdminContext(playwright, username, 'Stats@2026');
  });

  test.afterAll(async () => {
    await staffCtx?.api.dispose();
    if (staffUserId > 0) await api.delete(`/api/v1/admin/admins/${staffUserId}`);
    if (staffRoleId > 0) await api.delete(`/api/v1/admin/roles/${staffRoleId}`);
  });

  test('超管 scope=all：全平台口径覆盖本社区数据', async () => {
    const own = (await (await api.get(`/api/v1/admin/statistics/orders?${qs}`)).json()) as ApiResponse<OrderStats>;
    const all = (await (
      await api.get(`/api/v1/admin/statistics/orders?${qs}&scope=all`)
    ).json()) as ApiResponse<OrderStats>;
    expect(own.code).toBe(0);
    expect(all.code).toBe(0);
    // 全平台聚合包含本社区，总量与各状态分项都不应小于本社区口径
    expect(all.data!.total_orders, '全平台总单数应 ≥ 本社区').toBeGreaterThanOrEqual(own.data!.total_orders);
    for (const key of ['pending_pay', 'pending_ship', 'pending_receive', 'completed', 'refunding', 'cancelled'] as const) {
      expect(all.data![key], `全平台 ${key} 应 ≥ 本社区`).toBeGreaterThanOrEqual(own.data![key]);
    }
  });

  test('超管 community_id：指定社区口径等于该社区默认口径', async () => {
    expect(superCommunityId, '应能取到超管当前社区 ID').toBeGreaterThan(0);
    const base = (await (await api.get(`/api/v1/admin/statistics/orders?${qs}`)).json()) as ApiResponse<OrderStats>;
    const byParam = (await (
      await api.get(`/api/v1/admin/statistics/orders?${qs}&community_id=${superCommunityId}`)
    ).json()) as ApiResponse<OrderStats>;
    expect(base.code).toBe(0);
    expect(byParam.code).toBe(0);
    expect(byParam.data!.total_orders, '指定本社区 ID 应与默认口径一致').toBe(base.data!.total_orders);
  });

  test('普通管理员：scope/community_id 传参被忽略，不越权', async () => {
    const staffApi = staffCtx!.api;
    const own = (await (await staffApi.get(`/api/v1/admin/statistics/orders?${qs}`)).json()) as ApiResponse<OrderStats>;
    expect(own.code, `员工统计请求失败：${own.message}`).toBe(0);
    // 同时携带 scope=all 与一个不存在的 community_id，二者都应被忽略
    const spoof = (await (
      await staffApi.get(`/api/v1/admin/statistics/orders?${qs}&scope=all&community_id=999999`)
    ).json()) as ApiResponse<OrderStats>;
    expect(spoof.code).toBe(0);
    expect(spoof.data!.total_orders, '普通管理员传参应被忽略，回落本社区口径').toBe(own.data!.total_orders);

    // 环境存在其他社区订单时（全平台 ≠ 本社区），进一步确认员工看到的不是全平台数据
    const superAll = (await (
      await api.get(`/api/v1/admin/statistics/orders?${qs}&scope=all`)
    ).json()) as ApiResponse<OrderStats>;
    const superOwn = (await (await api.get(`/api/v1/admin/statistics/orders?${qs}`)).json()) as ApiResponse<OrderStats>;
    if (superAll.data!.total_orders !== superOwn.data!.total_orders) {
      expect(spoof.data!.total_orders, '普通管理员不得通过传参看到全平台数据').not.toBe(superAll.data!.total_orders);
    }
  });
});
