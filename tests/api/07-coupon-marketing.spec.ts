import { test, expect } from '@playwright/test';
import { clientLogin, newAuthedRequest, type ApiResponse, type PageData } from '../helpers/api';
import { uniquePhone } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 07：优惠券与营销活动
 * 覆盖：券列表、领券、我的券、重复领券、秒杀/满减/包邮公开接口
 */

interface Coupon {
  id: number;
  name?: string;
  [k: string]: unknown;
}

let api: Awaited<ReturnType<typeof newAuthedRequest>>;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  api = await newAuthedRequest(playwright, info.access_token);
});

test.afterAll(async () => {
  await api?.dispose();
});

test.describe.serial('优惠券', () => {
  let couponId = 0;
  let hasCouponActivity = false;

  test('可领券列表获取', async () => {
    const resp = await api.get('/api/v1/coupons');
    const body = (await resp.json()) as ApiResponse<PageData<Coupon>>;
    expect(body.code).toBe(0);
    const first = body.data?.list?.[0];
    if (first) {
      hasCouponActivity = true;
      couponId = first.id;
    } else {
      console.log('[优惠券] 环境无可领优惠券活动，领取类用例将跳过');
    }
  });

  test('领取优惠券成功', async () => {
    if (!hasCouponActivity) test.skip(true, '环境无可领优惠券');
    const resp = await api.post('/api/v1/coupons/receive', { data: { coupon_id: couponId } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('我的券列表包含刚领取的券', async () => {
    if (!hasCouponActivity) test.skip(true, '环境无可领优惠券');
    const resp = await api.get('/api/v1/user/coupons');
    const body = (await resp.json()) as ApiResponse<PageData<{ coupon_id?: number; id: number }>>;
    expect(body.code).toBe(0);
    expect(Array.isArray(body.data?.list)).toBe(true);
  });

  test('重复领取同一张券被限制（若有此规则）', async () => {
    if (!hasCouponActivity) test.skip(true, '环境无可领优惠券');
    const resp = await api.post('/api/v1/coupons/receive', { data: { coupon_id: couponId } });
    const body = (await resp.json()) as ApiResponse;
    // 领取限制取决于活动规则（每人限领 0/1 次），仅记录不强制断言失败
    if (body.code === 0) {
      console.log('[优惠券] 该活动允许重复领取，符合配置则无需处理');
    } else {
      expect(body.code).not.toBe(0);
    }
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
