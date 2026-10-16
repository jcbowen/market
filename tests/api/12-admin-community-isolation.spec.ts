import { test, expect } from '@playwright/test';
import { newAdminContext, type ApiResponse, type PageData } from '../helpers/api';
import { autoName } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 12：社区管理与数据隔离
 * 覆盖：社区 CRUD、社区权限读取、跨社区隔离（A 社区管理员的商品/订单不越界）
 * 隔离策略：超管 admin 建测试社区 → 在新社区语境（X-Community-Id）下列商品/订单
 *          → 断言与源社区数据无交集 → 删除测试社区
 */

interface Community {
  id: number;
  name: string;
  [k: string]: unknown;
}

interface Ref {
  id: number;
  [k: string]: unknown;
}

let api: Awaited<ReturnType<typeof newAdminContext>['api']>;
let defaultCommunityId = 0;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const ctx = await newAdminContext(playwright);
  api = ctx.api;
  defaultCommunityId = ctx.communityId;
});

test.afterAll(async () => {
  await api?.dispose();
});

test.describe.serial('社区管理与隔离', () => {
  let testCommunityId = 0;
  let sourceCommunityId = 0;

  test('准备：确认默认社区可用', async () => {
    const comms = (await (await api.get('/api/v1/admin/communities')).json()) as ApiResponse<PageData<Community>>;
    expect(comms.code).toBe(0);
    expect(defaultCommunityId).toBeGreaterThan(0);
    sourceCommunityId = defaultCommunityId;
  });

  test('超管创建测试社区成功', async () => {
    const resp = await api.post('/api/v1/admin/communities', {
      data: {
        name: autoName('测试社区'),
        address: '自动化测试地址',
        longitude: 114.3,
        latitude: 30.5,
        contact_phone: '13900001111',
        pickup_address: '测试自提点',
        delivery_fee: 3,
        min_order_amount: 20,
        status: 1,
      },
    });
    const body = (await resp.json()) as ApiResponse<{ id?: number }>;
    expect(body.code).toBe(0);
    testCommunityId = body.data?.id ?? 0;
    expect(testCommunityId).toBeGreaterThan(0);
  });

  test('测试社区列表与详情可查', async () => {
    const detail = (await (await api.get(`/api/v1/admin/communities/${testCommunityId}`)).json()) as ApiResponse<Community>;
    expect(detail.code).toBe(0);
    expect(detail.data?.name).toContain('AUTO-TEST');
  });

  test('隔离：新社区语境下商品列表与源社区无交集', async () => {
    const src = (await (
      await api.get('/api/v1/admin/products', { params: { page: 1, page_size: 50 } })
    ).json()) as ApiResponse<PageData<Ref>>;
    const srcIds = new Set((src.data?.list ?? []).map((p) => p.id));

    const isolated = (await (
      await api.get('/api/v1/admin/products', {
        params: { page: 1, page_size: 50 },
        headers: { 'X-Community-Id': String(testCommunityId) },
      })
    ).json()) as ApiResponse<PageData<Ref>>;
    expect(isolated.code).toBe(0);
    const isoIds = (isolated.data?.list ?? []).map((p) => p.id);
    expect(isoIds.every((id) => !srcIds.has(id) || id === 0)).toBe(true);
  });

  test('隔离：新社区语境下订单列表与源社区无交集', async () => {
    const src = (await (
      await api.get('/api/v1/admin/orders', { params: { page: 1, page_size: 50 } })
    ).json()) as ApiResponse<PageData<Ref>>;
    const srcIds = new Set((src.data?.list ?? []).map((o) => o.id));

    const isolated = (await (
      await api.get('/api/v1/admin/orders', {
        params: { page: 1, page_size: 50 },
        headers: { 'X-Community-Id': String(testCommunityId) },
      })
    ).json()) as ApiResponse<PageData<Ref>>;
    expect(isolated.code).toBe(0);
    const isoIds = (isolated.data?.list ?? []).map((o) => o.id);
    expect(isoIds.every((id) => !srcIds.has(id) || id === 0)).toBe(true);
  });

  test('社区权限集可读取', async () => {
    const resp = await api.get(`/api/v1/admin/communities/${testCommunityId}/permissions`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('更新社区资料生效', async () => {
    // UpdateAdminCommunityReq 要求 name 必填，整表覆盖式更新
    const resp = await api.put(`/api/v1/admin/communities/${testCommunityId}`, {
      data: { name: autoName('改名社区'), address: '自动化测试地址', delivery_fee: 5, status: 1 },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('清理：删除测试社区', async () => {
    const resp = await api.delete(`/api/v1/admin/communities/${testCommunityId}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('空名称社区被拒绝', async () => {
    const resp = await api.post('/api/v1/admin/communities', { data: { name: '' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });
});
