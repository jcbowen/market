import { test, expect } from '@playwright/test';
import {
  newAdminContext,
  clientLogin,
  pickSku,
  asList,
  type ApiResponse,
  type ClientLoginInfo,
  type SkuRef,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 17：营销写操作
 * 覆盖：优惠券创建/列表/更新/手动发放/发放记录/删除；
 *       banner 创建/列表/更新/删除；秒杀活动创建/更新；包邮规则创建/更新
 * 说明：时间窗统一使用相对当前时间的未来区间，避免"开始时间已过"类业务校验
 */

/** 未来时间窗（开始 +5min，结束 +7 天） */
function timeWindow(): { start_time: string; end_time: string } {
  const fmt = (d: Date) => d.toISOString().slice(0, 19).replace('T', ' ');
  const start = new Date(Date.now() + 5 * 60 * 1000);
  const end = new Date(Date.now() + 7 * 24 * 60 * 60 * 1000);
  return { start_time: fmt(start), end_time: fmt(end) };
}

interface Coupon {
  id: number;
  name: string;
  status?: number;
  [k: string]: unknown;
}

interface Banner {
  id: number;
  title: string;
  status?: number;
  [k: string]: unknown;
}

let api: Awaited<ReturnType<typeof newAdminContext>>['api'];
let buyer: ClientLoginInfo | null = null;

test.beforeAll(async ({ playwright }) => {
  const ctx = await newAdminContext(playwright);
  api = ctx.api;
  buyer = (await clientLogin(playwright.request, uniquePhone())) as ClientLoginInfo;
});

test.afterAll(async () => {
  await api?.dispose();
});

test.describe.serial('优惠券写操作', () => {
  let couponId = 0;
  const couponName = `AUTO-TEST 满减券 ${Date.now()}`;

  test('创建满减券成功', async () => {
    const w = timeWindow();
    const resp = await api.post('/api/v1/admin/coupons', {
      data: {
        name: couponName,
        type: 1,
        threshold: 50,
        discount_value: 10,
        total_count: 100,
        per_user_limit: 1,
        start_time: w.start_time,
        end_time: w.end_time,
        valid_days: 7,
        publish_status: 1, // 0=草稿 1=直接发布（草稿券不可发放）
      },
    });
    const body = (await resp.json()) as ApiResponse<{ id: number }>;
    expect(body.code, `创建优惠券失败: ${body.message}`).toBe(0);
    couponId = body.data?.id ?? 0;
    expect(couponId).toBeGreaterThan(0);
  });

  test('优惠券列表包含新券', async () => {
    if (!couponId) test.skip(true, '优惠券未创建');
    const resp = await api.get('/api/v1/admin/coupons', { params: { page: 1, page_size: 50 } });
    const body = (await resp.json()) as ApiResponse<Coupon[] | { list: Coupon[] }>;
    expect(body.code).toBe(0);
    expect(asList<Coupon>(body.data).some((c) => c.id === couponId)).toBe(true);
  });

  test('更新优惠券成功', async () => {
    if (!couponId) test.skip(true, '优惠券未创建');
    const resp = await api.put(`/api/v1/admin/coupons/${couponId}`, {
      data: { name: `${couponName}-改`, total_count: 200 },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `更新失败: ${body.message}`).toBe(0);
  });

  test('手动发放给指定会员成功', async () => {
    if (!couponId || !buyer) test.skip(true, '优惠券或会员未就绪');
    const resp = await api.post(`/api/v1/admin/coupons/${couponId}/grant`, {
      data: { user_ids: [buyer!.user.id] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `发放失败: ${body.message}`).toBe(0);
  });

  test('空发放名单被拒绝', async () => {
    if (!couponId) test.skip(true, '优惠券未创建');
    const resp = await api.post(`/api/v1/admin/coupons/${couponId}/grant`, { data: { user_ids: [] } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });

  test('已领取的券不可删除（业务保护）', async () => {
    if (!couponId) test.skip(true, '优惠券未创建');
    // 前序用例已将该券发放给 buyer，删除应被业务保护拦截（code 10004）
    const resp = await api.delete(`/api/v1/admin/coupons/${couponId}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `已领取的券删除应被拒: ${body.message}`).not.toBe(0);
  });

  test('删除未领取的优惠券成功', async () => {
    // 业务规则：已领取的券不可删除，故创建一张未发放的专用于删除验证
    const w = timeWindow();
    const createResp = await api.post('/api/v1/admin/coupons', {
      data: {
        name: `AUTO-TEST 待删券 ${Date.now()}`,
        type: 1,
        threshold: 50,
        discount_value: 10,
        total_count: 100,
        per_user_limit: 1,
        start_time: w.start_time,
        end_time: w.end_time,
        valid_days: 7,
        publish_status: 1,
      },
    });
    const createBody = (await createResp.json()) as ApiResponse<{ id: number }>;
    expect(createBody.code, `创建待删券失败: ${createBody.message}`).toBe(0);
    const id = createBody.data?.id ?? 0;
    expect(id).toBeGreaterThan(0);

    const resp = await api.delete(`/api/v1/admin/coupons/${id}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `删除失败: ${body.message}`).toBe(0);
  });
});

test.describe.serial('Banner 写操作', () => {
  let bannerId = 0;
  const bannerTitle = `AUTO-TEST 横幅 ${Date.now()}`;

  test('创建 banner 成功', async () => {
    const w = timeWindow();
    const resp = await api.post('/api/v1/admin/banners', {
      data: {
        title: bannerTitle,
        image_url: 'https://cdn.example.com/banner/auto-test.png',
        link_url: '/pages/index/index',
        position: 1,
        sort: 1,
        start_time: w.start_time,
        end_time: w.end_time,
      },
    });
    const body = (await resp.json()) as ApiResponse<{ id: number }>;
    expect(body.code, `创建 banner 失败: ${body.message}`).toBe(0);
    bannerId = body.data?.id ?? 0;
    expect(bannerId).toBeGreaterThan(0);
  });

  test('banner 列表包含新 banner', async () => {
    if (!bannerId) test.skip(true, 'banner 未创建');
    const resp = await api.get('/api/v1/admin/banners', { params: { page: 1, page_size: 50 } });
    const body = (await resp.json()) as ApiResponse<Banner[] | { list: Banner[] }>;
    expect(body.code).toBe(0);
    expect(asList<Banner>(body.data).some((b) => b.id === bannerId)).toBe(true);
  });

  test('更新 banner 成功', async () => {
    if (!bannerId) test.skip(true, 'banner 未创建');
    const resp = await api.put(`/api/v1/admin/banners/${bannerId}`, { data: { sort: 9 } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `更新失败: ${body.message}`).toBe(0);
  });

  test('删除 banner 成功', async () => {
    if (!bannerId) test.skip(true, 'banner 未创建');
    const resp = await api.delete(`/api/v1/admin/banners/${bannerId}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `删除失败: ${body.message}`).toBe(0);
  });
});

test.describe.serial('秒杀与包邮写操作', () => {
  let skuId = 0;

  test.beforeAll(async () => {
    try {
      const sku: SkuRef = await pickSku(api);
      skuId = sku.id;
    } catch {
      skuId = 0;
    }
  });

  test('创建秒杀活动成功', async () => {
    test.skip(!skuId, '环境无库存 SKU');
    const w = timeWindow();
    const resp = await api.post('/api/v1/admin/flash-sales', {
      data: {
        name: `AUTO-TEST 秒杀 ${Date.now()}`,
        start_time: w.start_time,
        end_time: w.end_time,
        items: [{ sku_id: skuId, flash_price: 0.01, stock: 10, limit_per_user: 1 }],
      },
    });
    const body = (await resp.json()) as ApiResponse<{ id: number }>;
    expect(body.code, `创建秒杀失败: ${body.message}`).toBe(0);
    const flashId = body.data?.id ?? 0;
    expect(flashId).toBeGreaterThan(0);

    // 更新秒杀
    const upd = (await (
      await api.put(`/api/v1/admin/flash-sales/${flashId}`, { data: { name: `AUTO-TEST 秒杀改 ${Date.now()}` } })
    ).json()) as ApiResponse;
    expect(upd.code, `更新秒杀失败: ${upd.message}`).toBe(0);
  });

  test('创建包邮规则成功', async () => {
    const w = timeWindow();
    const resp = await api.post('/api/v1/admin/free-shippings', {
      data: {
        name: `AUTO-TEST 包邮 ${Date.now()}`,
        threshold: 99,
        start_time: w.start_time,
        end_time: w.end_time,
        // scopes 为排除范围（4=排除分类 5=排除商品），缺省=全范围包邮
      },
    });
    const body = (await resp.json()) as ApiResponse<{ id: number }>;
    expect(body.code, `创建包邮失败: ${body.message}`).toBe(0);
    const fsId = body.data?.id ?? 0;
    expect(fsId).toBeGreaterThan(0);

    // 更新包邮规则
    const upd = (await (
      await api.put(`/api/v1/admin/free-shippings/${fsId}`, { data: { threshold: 199 } })
    ).json()) as ApiResponse;
    expect(upd.code, `更新包邮失败: ${upd.message}`).toBe(0);
  });
});
