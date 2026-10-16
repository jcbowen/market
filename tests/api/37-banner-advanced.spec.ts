import { test, expect, type Playwright } from '@playwright/test';
import { clientLogin, newAdminContext, newAuthedRequest, asList, type ApiResponse, type PageData } from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 37：Banner 进阶
 * 覆盖：有效期窗口外 C 端不可见、管理端仍可见（banner.go 管理态不过滤有效期）；
 *       position=2 分类页 banner 按位展示（position 过滤）；
 *       超管全局 banner（community_id=0）任意社区 C 端兜底可见、系统管理语境可见
 * 造数：超管创建，用后即删（banner 建立默认 status=1）
 */

interface Banner {
  id: number;
  title: string;
  position: number;
  community_id: number;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let adminCommunityId = 0;
let api: Awaited<ReturnType<typeof newAuthedRequest>>;

/** 快速建 banner，返回 ID */
async function createBanner(data: Record<string, unknown>): Promise<number> {
  const resp = await adminApi.post('/api/v1/admin/banners', {
    data: { image_url: 'https://cdn.example.com/banner/auto-37.png', link_url: '/pages/index/index', sort: 1, ...data },
  });
  const body = (await resp.json()) as ApiResponse<{ id: number }>;
  expect(body.code, `创建 banner 失败: ${body.message}`).toBe(0);
  return body.data!.id;
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  adminCommunityId = admin.communityId;
  const info = await clientLogin(playwright.request, uniquePhone());
  api = await newAuthedRequest(playwright, info.access_token);
});

test.afterAll(async () => {
  await api?.dispose();
  await adminApi?.dispose();
});

test.describe.serial('Banner 进阶', () => {
  const expiredTitle = `AUTO-TEST 37 过期横幅 ${Date.now()}`;
  // adminApi 默认语境社区（可能是任意 id），创建的 banner 落在该社区；C 端 banners 按 query community_id 过滤，需显式传
  const clientQ = () => ({ community_id: adminCommunityId });

  test('有效期窗口外：C 端不可见（end_time 已过）', async () => {
    const id = await createBanner({
      title: expiredTitle,
      position: 1,
      start_time: '2020-01-01 00:00:00',
      end_time: '2020-01-02 00:00:00',
    });
    try {
      const resp = await api.get('/api/v1/banners', { params: { ...clientQ(), position: 1 } });
      const body = (await resp.json()) as ApiResponse<Banner[] | { list: Banner[] }>;
      expect(body.code).toBe(0);
      expect(asList<Banner>(body.data).some((b) => b.id === id), '过期 banner 对 C 端不可见').toBe(false);
    } finally {
      await adminApi.delete(`/api/v1/admin/banners/${id}`);
    }
  });

  test('管理端不过滤有效期：过期 banner 仍可查（用于下架管理）', async () => {
    const id = await createBanner({
      title: expiredTitle,
      position: 1,
      start_time: '2020-01-01 00:00:00',
      end_time: '2020-01-02 00:00:00',
    });
    try {
      const resp = await adminApi.get('/api/v1/admin/banners', { params: { page: 1, page_size: 50 } });
      const body = (await resp.json()) as ApiResponse<PageData<Banner>>;
      expect(body.code).toBe(0);
      expect(asList<Banner>(body.data).some((b) => b.id === id), '管理端应能查到过期 banner').toBe(true);
    } finally {
      await adminApi.delete(`/api/v1/admin/banners/${id}`);
    }
  });

  test('position=2 分类页：按位过滤展示', async () => {
    const title = `AUTO-TEST 37 分类页横幅 ${Date.now()}`;
    const id = await createBanner({ title, position: 2 });
    try {
      const p2 = (await (await api.get('/api/v1/banners', { params: { ...clientQ(), position: 2 } })).json()) as ApiResponse<Banner[] | { list: Banner[] }>;
      expect(p2.code).toBe(0);
      expect(asList<Banner>(p2.data).some((b) => b.id === id), 'position=2 列表应包含分类页 banner').toBe(true);
      expect(asList<Banner>(p2.data).every((b) => b.position === 2), 'position=2 列表应只含分类页 banner').toBe(true);

      const p1 = (await (await api.get('/api/v1/banners', { params: { ...clientQ(), position: 1 } })).json()) as ApiResponse<Banner[] | { list: Banner[] }>;
      expect(asList<Banner>(p1.data).some((b) => b.id === id), 'position=1 列表不应包含分类页 banner').toBe(false);
    } finally {
      await adminApi.delete(`/api/v1/admin/banners/${id}`);
    }
  });

  test('全局兜底：community_id=0 的 banner 任意社区 C 端可见', async () => {
    const title = `AUTO-TEST 37 全局横幅 ${Date.now()}`;
    const id = await createBanner({ title, position: 1, community_id: 0 });
    try {
      const resp = await api.get('/api/v1/banners', { params: { position: 1 } });
      const body = (await resp.json()) as ApiResponse<Banner[] | { list: Banner[] }>;
      expect(body.code).toBe(0);
      const found = asList<Banner>(body.data).find((b) => b.id === id);
      expect(found, '全局 banner 应对任意社区 C 端兜底可见').toBeTruthy();
      expect(found!.community_id).toBe(0);
    } finally {
      await adminApi.delete(`/api/v1/admin/banners/${id}`);
    }
  });

  test('系统管理语境可见全局 banner（管理态 community_id=0 兜底）', async () => {
    const title = `AUTO-TEST 37 系统全局横幅 ${Date.now()}`;
    const id = await createBanner({ title, position: 1, community_id: 0 });
    try {
      const resp = await adminApi.get('/api/v1/admin/banners', { params: { page: 1, page_size: 50, position: 1 } });
      const body = (await resp.json()) as ApiResponse<PageData<Banner>>;
      expect(body.code).toBe(0);
      const found = asList<Banner>(body.data).find((b) => b.id === id);
      expect(found, '系统管理语境应可见全局 banner').toBeTruthy();
      expect(found!.community_id).toBe(0);
    } finally {
      await adminApi.delete(`/api/v1/admin/banners/${id}`);
    }
  });
});
