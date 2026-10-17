import { test, expect } from '@playwright/test';
import { clientLogin, newAdminContext, newAuthedRequest, asList, type ApiResponse, type PageData } from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 59：优惠券过期流转调度器（product 服务 CouponScheduler）
 * 覆盖：expire_type=2 固定可用期券，use_end_time 过去后由调度器（每分钟一轮）
 *       将 user_coupons.status 1未使用 → 3已过期 落库（marketing 被动校验之外的状态流转）；
 *       同时校验未到期对照券保持未使用
 * 说明：调度周期为 1 分钟，本模块包含轮询等待，整体耗时约 2~3 分钟；
 *       前置数据在 beforeAll 创建（券过期时点 = now + 75s）
 */

interface UserCoupon {
  id: number;
  coupon_id: number;
  status: number;
  expire_at?: string;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let api: Awaited<ReturnType<typeof newAuthedRequest>>;
let expiringCouponId = 0;   // 即将过期的固定期券
let controlCouponId = 0;    // 未到期的对照券

const fmt = (d: Date) => d.toISOString().slice(0, 19).replace('T', ' ');

test.beforeAll(async ({ playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  api = await newAuthedRequest(playwright, info.access_token);
  // 券与用户需同社区语境，否则 C 端领券按社区可见性过滤会报「资源不存在」
  const admin = await newAdminContext(playwright, undefined, undefined, info.user.community_id);
  adminApi = admin.api;

  const base = () => ({
    type: 1,
    threshold: 0.01,
    discount_value: 0.5,
    total_count: 20,
    per_user_limit: 1,
    start_time: fmt(new Date(Date.now() - 60 * 1000)),
    end_time: fmt(new Date(Date.now() + 7 * 24 * 60 * 60 * 1000)),
    publish_status: 1,
  });

  // 即将过期的券：可用期至 now + 75s（留出领取耗时），之后等待调度器流转
  const c1 = (await (await adminApi.post('/api/v1/admin/coupons', {
    data: {
      ...base(),
      name: `AUTO-TEST 59 秒级过期券 ${Date.now()}`,
      expire_type: 2,
      use_start_time: fmt(new Date(Date.now() - 60 * 1000)),
      use_end_time: fmt(new Date(Date.now() + 75 * 1000)),
    },
  })).json()) as ApiResponse<{ id: number }>;
  expect(c1.code, `创建过期券失败: ${c1.message}`).toBe(0);
  expiringCouponId = c1.data!.id;

  // 对照券：可用期 7 天，全程应保持未使用
  const c2 = (await (await adminApi.post('/api/v1/admin/coupons', {
    data: {
      ...base(),
      name: `AUTO-TEST 59 对照券 ${Date.now()}`,
      expire_type: 2,
      use_start_time: fmt(new Date(Date.now() - 60 * 1000)),
      use_end_time: fmt(new Date(Date.now() + 7 * 24 * 60 * 60 * 1000)),
    },
  })).json()) as ApiResponse<{ id: number }>;
  expect(c2.code, `创建对照券失败: ${c2.message}`).toBe(0);
  controlCouponId = c2.data!.id;

  // 领取两张券
  for (const cid of [expiringCouponId, controlCouponId]) {
    const recv = (await (await api.post('/api/v1/coupons/receive', { data: { coupon_id: cid } })).json()) as ApiResponse;
    expect(recv.code, `领取券 ${cid} 失败: ${recv.message}`).toBe(0);
  }
});

test.afterAll(async () => {
  await api?.dispose();
  await adminApi?.dispose();
});

test.describe.serial('优惠券过期流转调度器', () => {
  test('领取后两张券均为未使用状态且过期时间快照正确', async () => {
    const resp = await api.get('/api/v1/user/coupons', { params: { status: 1 } });
    const body = (await resp.json()) as ApiResponse<PageData<UserCoupon>>;
    expect(body.code).toBe(0);
    const mine = asList<UserCoupon>(body.data);
    const expiring = mine.find((uc) => uc.coupon_id === expiringCouponId);
    const control = mine.find((uc) => uc.coupon_id === controlCouponId);
    expect(expiring, '我的券应含即将过期的固定期券').toBeTruthy();
    expect(control, '我的券应含对照券').toBeTruthy();
    expect(expiring!.status).toBe(1);
    expect(control!.status).toBe(1);
    expect(expiring!.expire_at, '固定期券应有 expire_at 快照').toBeTruthy();
    // expire_at 应约等于创建时设定的 use_end_time（now+75s，允许 ±60s 时钟/处理偏差）
    const expireTs = new Date(expiring!.expire_at!.replace(' ', 'T')).getTime();
    const expectedTs = Date.now() + 75 * 1000;
    expect(Math.abs(expireTs - expectedTs)).toBeLessThan(60 * 1000);
  });

  test('调度器将过期未使用券流转为已过期，未到期对照券不受影响', async () => {
    test.setTimeout(300_000); // 轮询等待调度器（周期 1 分钟），上限 5 分钟

    // 轮询：过期时点(创建后约 75s) + 一个调度周期，最多再等 ~4 分钟
    let expiredFound = false;
    const deadline = Date.now() + 240_000;
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 10_000));
      const resp = await api.get('/api/v1/user/coupons', { params: { status: 3 } });
      const body = (await resp.json()) as ApiResponse<PageData<UserCoupon>>;
      if (body.code !== 0) continue;
      const hit = asList<UserCoupon>(body.data).find((uc) => uc.coupon_id === expiringCouponId);
      if (hit) {
        expiredFound = true;
        break;
      }
    }
    expect(expiredFound, '调度器应在过期后把用户券流转为 status=3（已过期）').toBe(true);

    // 对照券：已过期分组不应出现，未使用分组仍在
    const expiredList = (await (await api.get('/api/v1/user/coupons', { params: { status: 3 } })).json()) as ApiResponse<PageData<UserCoupon>>;
    expect(asList<UserCoupon>(expiredList.data).some((uc) => uc.coupon_id === controlCouponId)).toBe(false);

    const activeList = (await (await api.get('/api/v1/user/coupons', { params: { status: 1 } })).json()) as ApiResponse<PageData<UserCoupon>>;
    const control = asList<UserCoupon>(activeList.data).find((uc) => uc.coupon_id === controlCouponId);
    expect(control, '未到期对照券应仍为未使用').toBeTruthy();
    expect(control!.status).toBe(1);
  });
});
