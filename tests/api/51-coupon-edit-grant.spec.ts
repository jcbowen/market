import { test, expect, type Playwright } from '@playwright/test';
import { clientLogin, newAdminContext, asList, type ApiResponse, type PageData } from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 51：优惠券已发布态编辑边界 + 批量发放混合场景
 * 覆盖（PUT /admin/coupons/:id 已发布态，gateway 按状态裁剪字段）：
 *   - total_count 仅允许增大：增大生效；减小不报错但被忽略（回读不变）
 *   - grant_status 可改（停止发放/恢复发放）
 *   - rule_text 可改
 *   - 名称/门槛/面值/限领/有效期等传了被忽略（回读不变）
 * 覆盖（POST /admin/coupons/:id/grant 批量发放）：
 *   - 混合场景：部分合格部分不合格 → granted_count 与 failures[]（含 reason）结构正确
 *   - 发放成功者 source=2（后台发放）+ operator 快照（coupon-records 回查）
 *   - 重复发放触发限领 → 全部进 failures
 *   - 草稿券发放被拦（10004 未发布或已下架）
 * 说明：状态机（停发拦截/下架拦截）35 号已覆盖；领取抵扣 07/18 号覆盖，此处不重复
 */

interface CouponTemplate {
  id: number;
  name: string;
  total_count: number;
  received_count?: number;
  per_user_limit?: number;
  threshold?: number;
  discount_value?: number;
  grant_status?: number;
  publish_status?: number;
  rule_text?: string;
  valid_days?: number;
  [k: string]: unknown;
}

interface CouponRecord {
  id: number;
  coupon_id: number;
  user_id: number;
  source: number;
  operator_id: number;
  operator_name?: string;
  [k: string]: unknown;
}

interface GrantResp {
  granted_count: number;
  failures?: { user_id: number; reason: string }[];
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let buyerAId = 0;
let buyerBId = 0;
let publishedId = 0;
let draftId = 0;

/** 券名隔离标记 */
const couponName = `AUTO-TEST 51 发布态编辑券 ${Date.now()}`;
const draftName = `AUTO-TEST 51 草稿券 ${Date.now()}`;

/**
 * 按 ID 回读券模板（管理端列表接口无 keyword 过滤，取全量后按 ID 定位）
 * - 函数名: getCouponById
 * - 参数: id — 券 ID
 * - 返回: Promise<CouponTemplate>（找不到时断言失败）
 * - 异常: 业务码非 0 或券不存在时抛出
 * - 使用示例: const tpl = await getCouponById(publishedId);
 */
async function getCouponById(id: number): Promise<CouponTemplate> {
  const body = (await (
    await adminApi.get('/api/v1/admin/coupons', { params: { page: 1, page_size: 100 } })
  ).json()) as ApiResponse<PageData<CouponTemplate>>;
  expect(body.code, `券列表查询失败: ${body.message}`).toBe(0);
  const tpl = asList<CouponTemplate>(body.data).find((c) => c.id === id);
  expect(tpl, `券 ${id} 应存在于列表`).toBeTruthy();
  return tpl!;
}

/**
 * 已发布态编辑请求
 * - 函数名: patchCoupon
 * - 参数: data — 部分字段
 * - 返回: Promise<ApiResponse<unknown>>
 * - 异常: 网络错误由 Playwright 抛出
 * - 使用示例: await patchCoupon({ total_count: 15 });
 */
async function patchCoupon(data: Record<string, unknown>): Promise<ApiResponse<unknown>> {
  return (await (
    await adminApi.put(`/api/v1/admin/coupons/${publishedId}`, { data })
  ).json()) as ApiResponse<unknown>;
}

test.beforeAll(async ({ playwright }) => {
  const buyerA = await clientLogin(playwright.request, uniquePhone());
  buyerAId = buyerA.user.id;
  const buyerB = await clientLogin(playwright.request, uniquePhone());
  buyerBId = buyerB.user.id;
  // 券与会员同社区语境，避免社区隔离拦截发放
  const admin = await newAdminContext(playwright, undefined, undefined, buyerA.user.community_id);
  adminApi = admin.api;

  const fmt = (d: Date) => d.toISOString().slice(0, 19).replace('T', ' ');
  const base = {
    type: 1,
    threshold: 10,
    discount_value: 2,
    total_count: 10,
    per_user_limit: 1,
    start_time: fmt(new Date(Date.now() - 60 * 1000)),
    end_time: fmt(new Date(Date.now() + 7 * 24 * 60 * 60 * 1000)),
    valid_days: 7,
  };

  // 已发布券：发布态编辑边界与批量发放的载体
  const pub = (await (
    await adminApi.post('/api/v1/admin/coupons', {
      data: { ...base, name: couponName, publish_status: 1, rule_text: 'AUTO51 初始规则' },
    })
  ).json()) as ApiResponse<{ id: number }>;
  expect(pub.code, `创建已发布券失败: ${pub.message}`).toBe(0);
  publishedId = pub.data!.id;

  // 草稿券：不传 publish_status 默认 0
  const draft = (await (
    await adminApi.post('/api/v1/admin/coupons', { data: { ...base, name: draftName } })
  ).json()) as ApiResponse<{ id: number }>;
  expect(draft.code, `创建草稿券失败: ${draft.message}`).toBe(0);
  draftId = draft.data!.id;
});

test.afterAll(async () => {
  await adminApi?.dispose();
});

test.describe.serial('已发布态编辑边界', () => {
  test('1. total_count 增大生效（10→15）', async () => {
    test.skip(!publishedId, '券未创建');
    const resp = await patchCoupon({ total_count: 15 });
    expect(resp.code, `增大总量失败: ${resp.message}`).toBe(0);
    const tpl = await getCouponById(publishedId);
    expect(tpl?.total_count, '总量应更新为 15').toBe(15);
  });

  test('2. total_count 减小被忽略（回读不变）', async () => {
    test.skip(!publishedId, '券未创建');
    const resp = await patchCoupon({ total_count: 5 });
    expect(resp.code, '减小总量不应报错（服务端静默忽略）').toBe(0);
    const tpl = await getCouponById(publishedId);
    expect(tpl?.total_count, '已领不可截断，总量应保持 15').toBe(15);
  });

  test('3. grant_status 停止/恢复发放生效', async () => {
    test.skip(!publishedId, '券未创建');
    const stop = await patchCoupon({ grant_status: 0 });
    expect(stop.code, `停止发放失败: ${stop.message}`).toBe(0);
    const stopped = await getCouponById(publishedId);
    expect(stopped?.grant_status).toBe(0);

    const resume = await patchCoupon({ grant_status: 1 });
    expect(resume.code, `恢复发放失败: ${resume.message}`).toBe(0);
    const resumed = await getCouponById(publishedId);
    expect(resumed?.grant_status).toBe(1);
  });

  test('4. rule_text 可改', async () => {
    test.skip(!publishedId, '券未创建');
    const resp = await patchCoupon({ rule_text: 'AUTO51 更新后的使用规则' });
    expect(resp.code, `更新规则文案失败: ${resp.message}`).toBe(0);
    const tpl = await getCouponById(publishedId);
    expect(tpl?.rule_text).toBe('AUTO51 更新后的使用规则');
  });

  test('5. 名称/门槛/面值/限领/有效期传了被忽略（回读不变）', async () => {
    test.skip(!publishedId, '券未创建');
    const before = await getCouponById(publishedId);
    const resp = await patchCoupon({
      name: 'AUTO51 不应生效的新名',
      threshold: 999,
      discount_value: 999,
      per_user_limit: 99,
      valid_days: 99,
      start_time: '2020-01-01 00:00:00',
      end_time: '2030-01-01 00:00:00',
    });
    expect(resp.code, '编辑请求本身应成功（字段被静默忽略）').toBe(0);
    const after = await getCouponById(publishedId);
    expect(after?.name, '名称不可改').toBe(couponName);
    expect(after?.threshold, '门槛不可改').toBe(before.threshold);
    expect(after?.discount_value, '面值不可改').toBe(before.discount_value);
    expect(after?.per_user_limit, '限领不可改').toBe(before.per_user_limit);
    expect(after?.valid_days, '有效期天数不可改').toBe(before.valid_days);
    expect(after?.end_time, '领取截止不可改').toBe(before.end_time);
  });
});

test.describe.serial('批量发放混合场景', () => {
  test('6. 混合发放：2 成功 1 失败（granted_count + failures 结构）', async () => {
    test.skip(!publishedId, '券未创建');
    // 同一 uid 出现两次：第一次成功，第二次触发限领进 failures
    // （服务端逐人独立校验，不存在的用户 ID 也会发放成功，故用重复领取构造失败面）
    const body = (await (
      await adminApi.post(`/api/v1/admin/coupons/${publishedId}/grant`, {
        data: { user_ids: [buyerAId, buyerAId, buyerBId] },
      })
    ).json()) as ApiResponse<GrantResp>;
    expect(body.code, `发放请求失败: ${body.message}`).toBe(0);
    expect(body.data?.granted_count, '甲首次+乙应发放成功').toBe(2);
    const failures = body.data?.failures ?? [];
    expect(failures, '仅重复领取的甲进 failures').toHaveLength(1);
    expect(failures[0].user_id).toBe(buyerAId);
    expect(failures[0].reason, '失败原因应提示限领').toContain('限领');

    // 发放记录回查：成功者 source=2 + operator 快照
    const records = (await (
      await adminApi.get('/api/v1/admin/coupon-records', { params: { coupon_id: publishedId, page: 1, page_size: 50 } })
    ).json()) as ApiResponse<PageData<CouponRecord>>;
    expect(records.code).toBe(0);
    const rows = asList<CouponRecord>(records.data).filter(
      (r) => r.user_id === buyerAId || r.user_id === buyerBId,
    );
    expect(rows, '甲乙各应有一条发放记录').toHaveLength(2);
    for (const row of rows) {
      expect(row.source, '后台发放来源应为 2').toBe(2);
      expect(row.operator_id, '经手人 ID 应快照').toBeGreaterThan(0);
      expect(row.operator_name, '经手人姓名应快照').toBeTruthy();
    }
  });

  test('7. 重复发放触发限领：全部进 failures', async () => {
    test.skip(!publishedId, '券未创建');
    const body = (await (
      await adminApi.post(`/api/v1/admin/coupons/${publishedId}/grant`, {
        data: { user_ids: [buyerAId, buyerBId] },
      })
    ).json()) as ApiResponse<GrantResp>;
    expect(body.code).toBe(0);
    expect(body.data?.granted_count, '限领 1 张不应再发放').toBe(0);
    const failures = body.data?.failures ?? [];
    expect(failures).toHaveLength(2);
    for (const f of failures) expect(f.reason, '失败原因应提示限领').toContain('限领');
  });

  test('8. 草稿券发放被拦（10004）', async () => {
    test.skip(!draftId, '草稿券未创建');
    const body = (await (
      await adminApi.post(`/api/v1/admin/coupons/${draftId}/grant`, {
        data: { user_ids: [buyerAId] },
      })
    ).json()) as ApiResponse<GrantResp>;
    expect(body.code, '草稿券发放应被拦').toBe(10004);
    expect(body.message).toContain('未发布');
  });
});
