import { test, expect, type Playwright } from '@playwright/test';
import { clientLogin, newAdminContext, newAuthedRequest, asList, type ApiResponse, type PageData } from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 35：优惠券状态机进阶
 * 覆盖：停止发放（grant_status=0）→ 券模板仍发布、领券中心可见，但 C 端领取与后台发放均被拦（marketing.go 326/362）；
 *       is_center_visible=false → 不进领券中心（07 号可见性用例的镜像面）；
 *       expire_type=2 → 领取后 user_coupon.expire_at = 券模板 use_end_time（marketing.go 426-427）
 */

interface CouponTemplate {
  id: number;
  name: string;
  grant_status?: number;
  is_center_visible?: boolean;
  [k: string]: unknown;
}

interface CenterCoupon {
  id: number;
  [k: string]: unknown;
}

interface UserCoupon {
  id: number;
  coupon_id: number;
  status: number;
  expire_at?: string;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let api: Awaited<ReturnType<typeof newAuthedRequest>>;
let userCommunityId = 0;
let couponB = 0; // 停发券
let couponC = 0; // 固定期券（expire_type=2）
let couponD = 0; // 中心隐藏券
let couponCUseEnd = '';

test.beforeAll(async ({ playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  api = await newAuthedRequest(playwright, info.access_token);
  userCommunityId = info.user.community_id;
  // 券与用户需同社区语境，否则 C 端领券按社区可见性过滤会报「资源不存在」
  const admin = await newAdminContext(playwright, undefined, undefined, info.user.community_id);
  adminApi = admin.api;

  const fmt = (d: Date) => d.toISOString().slice(0, 19).replace('T', ' ');
  const base = () => ({
    type: 1,
    threshold: 0.01,
    discount_value: 0.5,
    total_count: 20,
    per_user_limit: 1,
    // 领取窗口需已开始（领券中心与领取校验均过滤 start_time <= now）
    start_time: fmt(new Date(Date.now() - 60 * 1000)),
    end_time: fmt(new Date(Date.now() + 7 * 24 * 60 * 60 * 1000)),
    publish_status: 1,
  });

  // 券 B：发布后立即停止发放
  const b = (await (await adminApi.post('/api/v1/admin/coupons', {
    data: { ...base(), name: `AUTO-TEST 35 停发券 ${Date.now()}`, valid_days: 7 },
  })).json()) as ApiResponse<{ id: number }>;
  expect(b.code).toBe(0);
  couponB = b.data!.id;
  const stop = (await (await adminApi.put(`/api/v1/admin/coupons/${couponB}`, {
    data: { grant_status: 0 },
  })).json()) as ApiResponse;
  expect(stop.code, `停止发放失败: ${stop.message}`).toBe(0);

  // 券 C：固定可用期（expire_type=2）
  const useStart = fmt(new Date(Date.now() - 60 * 1000));
  const useEnd = fmt(new Date(Date.now() + 7 * 24 * 60 * 60 * 1000));
  couponCUseEnd = useEnd;
  const c = (await (await adminApi.post('/api/v1/admin/coupons', {
    data: { ...base(), name: `AUTO-TEST 35 固定期券 ${Date.now()}`, expire_type: 2, use_start_time: useStart, use_end_time: useEnd },
  })).json()) as ApiResponse<{ id: number }>;
  expect(c.code).toBe(0);
  couponC = c.data!.id;

  // 券 D：中心隐藏券
  const d = (await (await adminApi.post('/api/v1/admin/coupons', {
    data: { ...base(), name: `AUTO-TEST 35 隐藏券 ${Date.now()}`, valid_days: 7, is_center_visible: false },
  })).json()) as ApiResponse<{ id: number }>;
  expect(d.code).toBe(0);
  couponD = d.data!.id;
});

test.afterAll(async () => {
  await api?.dispose();
  await adminApi?.dispose();
});

test.describe.serial('优惠券状态机进阶', () => {
  test('停发券：模板仍发布，但 C 端领取与后台发放均被拦', async () => {
    // 模板层：仍为发布态、发放已停
    const list = (await (await adminApi.get('/api/v1/admin/coupons', { params: { page: 1, page_size: 50, keyword: 'AUTO-TEST 35 停发券' } })).json()) as ApiResponse<PageData<CouponTemplate>>;
    expect(list.code).toBe(0);
    const tpl = asList<CouponTemplate>(list.data).find((c) => c.id === couponB);
    expect(tpl, '停发券模板应存在').toBeTruthy();
    expect(tpl!.publish_status).toBe(1);

    // C 端领取被拦
    const receive = (await (await api.post('/api/v1/coupons/receive', { data: { coupon_id: couponB } })).json()) as ApiResponse;
    expect(receive.code, `停发券领取应被拦: ${receive.message}`).not.toBe(0);
    expect(receive.message).toContain('停止发放');

    // 后台手动发放同样被拦：券级校验整体拒绝（10004），早于逐人循环
    const grant = (await (await adminApi.post(`/api/v1/admin/coupons/${couponB}/grant`, {
      data: { user_ids: [99999901] },
    })).json()) as ApiResponse<{ failures?: { user_id: number; reason: string }[] }>;
    expect(grant.code, `后台发放停发券应被拦: ${grant.message}`).toBe(10004);
    expect(grant.message).toContain('停止发放');
  });

  test('is_center_visible=false 的券不进领券中心', async () => {
    const resp = await api.get('/api/v1/coupons', { params: { community_id: userCommunityId } });
    const body = (await resp.json()) as ApiResponse<CenterCoupon[] | { list: CenterCoupon[] }>;
    expect(body.code).toBe(0);
    const ids = asList<CenterCoupon>(body.data).map((c) => c.id);
    expect(ids).not.toContain(couponD);
    expect(ids).toContain(couponC);
  });

  test('expire_type=2：领取后到期时间为 use_end_time', async () => {
    const receive = (await (await api.post('/api/v1/coupons/receive', { data: { coupon_id: couponC } })).json()) as ApiResponse;
    expect(receive.code, `领取固定期券失败: ${receive.message}`).toBe(0);

    const list = (await (await api.get('/api/v1/user/coupons', { params: { status: 1 } })).json()) as ApiResponse<UserCoupon[] | { list: UserCoupon[] }>;
    expect(list.code).toBe(0);
    const mine = asList<UserCoupon>(list.data).find((uc) => uc.coupon_id === couponC);
    expect(mine, '用户券列表应含刚领取的固定期券').toBeTruthy();
    expect(mine!.expire_at, '固定期券应有到期时间').toBeTruthy();
    // 快照到期时间 = 模板 use_end_time（用户券快照为 RFC3339 格式，模板为空格分隔）
    expect(mine!.expire_at!.slice(0, 19).replace('T', ' ')).toBe(couponCUseEnd.slice(0, 19));
  });
});
