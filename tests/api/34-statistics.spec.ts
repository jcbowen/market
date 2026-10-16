import { test, expect, type Playwright } from '@playwright/test';
import { newAdminContext, type ApiResponse } from '../helpers/api';

/**
 * 模块 34：数据统计五接口
 * 覆盖：dashboard 仪表盘聚合（字段完备 + 数值非负）；orders 订单统计（各状态之和 = 总数）；
 *       sales 销售统计（客单价 ≈ 支付总额/单数，实付 ≤ 原价总额）；
 *       rankings 商品销量排行（结构合法 + 降序）；revenue 营收报表（日期升序 + 数值非负）
 * 口径：统计按今日/区间计算，仅验证结构与数值自洽，不锁定具体数值（环境数据持续增长）
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

/** 今日与区间起止（跨月安全） */
function dateRange(): { start: string; end: string } {
  const now = new Date();
  const fmt = (d: Date) => d.toISOString().slice(0, 10);
  const start = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000);
  return { start: fmt(start), end: fmt(now) };
}

let api: Awaited<ReturnType<typeof newAdminContext>>['api'];

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  api = admin.api;
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
