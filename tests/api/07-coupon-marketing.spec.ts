import { test, expect } from '@playwright/test';
import { clientLogin, newAdminContext, newAuthedRequest, asList, type ApiResponse } from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 07：优惠券与营销活动
 * 覆盖：券列表、领券、我的券、重复领券、秒杀/满减/包邮公开接口
 * 说明：券由本模块自建（per_user_limit=1），不依赖环境存量活动；
 *       两个 C 端券列表出口均为裸数组（SuccessWithList），且公开路由只认 community_id query
 */

interface Coupon {
  id: number;
  name?: string;
  [k: string]: unknown;
}

interface UserCoupon {
  id: number;
  coupon_id: number;
  status: number;
  [k: string]: unknown;
}

let api: Awaited<ReturnType<typeof newAuthedRequest>>;
let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
/** 本模块自建券（限量 1 张/人），保证领取类用例不依赖环境存量数据 */
let couponId = 0;
/** 会员与券的同社区语境（公开领券中心按 community_id 过滤，不传即查不到） */
let communityId = 0;

// 本地墙钟格式化：全栈（Go time.Local / MySQL NOW()）按服务器本地时间比对，
// 不可用 toISOString()（UTC 墙钟，慢 8h 会把券窗整体推后，导致「不在领取时间范围内」）
const fmt = (d: Date) => {
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
};

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  api = await newAuthedRequest(playwright, info.access_token);
  communityId = info.user.community_id;
  // 券与会员需同社区语境：管理端建券落在语境社区，C 端领券中心再按该社区过滤
  const admin = await newAdminContext(playwright, undefined, undefined, communityId);
  adminApi = admin.api;

  const created = (await (await adminApi.post('/api/v1/admin/coupons', {
    data: {
      name: autoName('07 限领券'),
      type: 1,
      threshold: 0.01,
      discount_value: 0.5,
      total_count: 20,
      per_user_limit: 1,
      start_time: fmt(new Date(Date.now() - 60 * 1000)),
      end_time: fmt(new Date(Date.now() + 7 * 24 * 60 * 60 * 1000)),
      publish_status: 1,
      expire_type: 2,
      use_start_time: fmt(new Date(Date.now() - 60 * 1000)),
      use_end_time: fmt(new Date(Date.now() + 7 * 24 * 60 * 60 * 1000)),
    },
  })).json()) as ApiResponse<{ id: number }>;
  expect(created.code, `创建测试券失败: ${created.message}`).toBe(0);
  couponId = created.data!.id;
});

test.afterAll(async () => {
  await api?.dispose();
  await adminApi?.dispose();
});

test.describe.serial('优惠券', () => {
  test('可领券列表包含自建券', async () => {
    const resp = await api.get('/api/v1/coupons', { params: { community_id: communityId } });
    const body = (await resp.json()) as ApiResponse<Coupon[]>;
    expect(body.code).toBe(0);
    // 已发布 + 进领券中心（is_center_visible 缺省即 1）的券必须出现在列表里
    expect(asList<Coupon>(body.data).some((c) => c.id === couponId), `自建券 ${couponId} 未出现在领券中心`).toBe(true);
  });

  test('领取优惠券成功', async () => {
    const resp = await api.post('/api/v1/coupons/receive', { data: { coupon_id: couponId } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `领取券 ${couponId} 失败: ${body.message}`).toBe(0);
  });

  test('我的券列表包含刚领取的券', async () => {
    const resp = await api.get('/api/v1/user/coupons', { params: { status: 1 } });
    const body = (await resp.json()) as ApiResponse<UserCoupon[]>;
    expect(body.code).toBe(0);
    const mine = asList<UserCoupon>(body.data).find((c) => c.coupon_id === couponId);
    expect(mine, `未使用券列表中缺少券 ${couponId}`).toBeDefined();
    expect(mine!.status).toBe(1);
  });

  test('超出每人限领数量后再次领取被拒绝', async () => {
    const resp = await api.post('/api/v1/coupons/receive', { data: { coupon_id: couponId } });
    const body = (await resp.json()) as ApiResponse;
    // 自建券 per_user_limit=1，第二次领取必然命中限领规则（不再依赖环境活动的配置）
    expect(body.code, `券 ${couponId} 限领 1 张，重复领取竟被放行`).not.toBe(0);
  });
});

test.describe('营销活动公开接口', () => {
  test('秒杀活动列表结构', async ({ request }) => {
    const resp = await request.get('/api/v1/flash-sales');
    const body = (await resp.json()) as ApiResponse<{ list?: unknown[] } | unknown[]>;
    expect(body.code).toBe(0);
    expect(body.data).toBeDefined();
  });

  test('满减活动列表结构', async ({ request }) => {
    const resp = await request.get('/api/v1/full-reductions');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('包邮活动：无活动时 data 为 null 但业务码 0', async ({ request }) => {
    const resp = await request.get('/api/v1/free-shippings/active');
    const body = (await resp.json()) as ApiResponse<unknown | null>;
    expect(body.code).toBe(0);
  });
});
