import { test, expect } from '@playwright/test';
import { clientLogin, newAuthedRequest, asList, type ApiResponse } from '../helpers/api';
import { uniquePhone } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 03：用户资料与收货地址
 * 覆盖：资料获取/更新、社区切换、地址 CRUD、默认地址、越权访问
 */

interface Address {
  id: number;
  contact_name: string;
  contact_phone: string;
  detail: string;
  is_default?: boolean;
}

/** 每个用例组共用一个独立登录用户与其已鉴权请求上下文 */
let api: Awaited<ReturnType<typeof newAuthedRequest>>;
let userId: number;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  userId = info.user.id;
  api = await newAuthedRequest(playwright, info.access_token);
});

test.afterAll(async () => {
  await api?.dispose();
});

test.describe.serial('用户资料与地址', () => {
  test('获取用户资料', async () => {
    const resp = await api.get('/api/v1/user/profile');
    const body = (await resp.json()) as ApiResponse<{ id: number; phone: string }>;
    expect(body.code).toBe(0);
    expect(body.data?.id).toBe(userId);
    expect(body.data?.phone).toBeTruthy();
  });

  test('更新昵称后可查回', async () => {
    const nickname = `测试用户${Date.now()}`;
    const resp = await api.put('/api/v1/user/profile', { data: { nickname } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
    const detail = (await (await api.get('/api/v1/user/profile')).json()) as ApiResponse<{ nickname: string }>;
    expect(detail.data?.nickname).toBe(nickname);
  });

  test('新增地址并出现在列表', async () => {
    const detail = `测试路 ${Date.now()} 号`;
    const resp = await api.post('/api/v1/user/addresses', {
      data: {
        contact_name: '自动测试',
        contact_phone: '13900000000',
        province: '湖北省',
        city: '武汉市',
        district: '洪山区',
        detail,
        tag: '家',
      },
    });
    const body = (await resp.json()) as ApiResponse<Address>;
    expect(body.code).toBe(0);
    expect(body.data?.id).toBeGreaterThan(0);

    const list = (await (await api.get('/api/v1/user/addresses')).json()) as ApiResponse<Address[] | { list: Address[] }>;
    expect(asList<Address>(list.data).some((a) => a.id === body.data!.id)).toBe(true);
  });

  test('更新地址内容生效', async () => {
    const created = (await (
      await api.post('/api/v1/user/addresses', {
        data: { contact_name: '更新前', contact_phone: '13900000001', detail: '原地址' },
      })
    ).json()) as ApiResponse<Address>;
    const id = created.data!.id;

    const upd = (await (
      await api.put(`/api/v1/user/addresses/${id}`, {
        data: { contact_name: '更新后', detail: `新地址${Date.now()}` },
      })
    ).json()) as ApiResponse;
    expect(upd.code).toBe(0);

    const list = (await (await api.get('/api/v1/user/addresses')).json()) as ApiResponse<Address[] | { list: Address[] }>;
    const target = asList<Address>(list.data).find((a) => a.id === id);
    expect(target?.contact_name).toBe('更新后');
  });

  test('设置默认地址唯一生效', async () => {
    const a = (await (
      await api.post('/api/v1/user/addresses', {
        data: { contact_name: '默认A', contact_phone: '13900000002', detail: 'A' },
      })
    ).json()) as ApiResponse<Address>;
    const b = (await (
      await api.post('/api/v1/user/addresses', {
        data: { contact_name: '默认B', contact_phone: '13900000003', detail: 'B' },
      })
    ).json()) as ApiResponse<Address>;

    expect((await (await api.put(`/api/v1/user/addresses/${a.data!.id}/default`)).json()).code).toBe(0);
    expect((await (await api.put(`/api/v1/user/addresses/${b.data!.id}/default`)).json()).code).toBe(0);

    const list = (await (await api.get('/api/v1/user/addresses')).json()) as ApiResponse<Address[] | { list: Address[] }>;
    const defaults = asList<Address>(list.data).filter((x) => x.is_default);
    expect(defaults.length).toBe(1);
    expect(defaults[0].id).toBe(b.data!.id);
  });

  test('删除地址后列表不再包含', async () => {
    const created = (await (
      await api.post('/api/v1/user/addresses', {
        data: { contact_name: '待删除', contact_phone: '13900000004', detail: '删' },
      })
    ).json()) as ApiResponse<Address>;
    const id = created.data!.id;

    const del = (await (await api.delete(`/api/v1/user/addresses/${id}`)).json()) as ApiResponse;
    expect(del.code).toBe(0);
    const list = (await (await api.get('/api/v1/user/addresses')).json()) as ApiResponse<Address[] | { list: Address[] }>;
    expect(asList<Address>(list.data).some((a) => a.id === id)).toBe(false);
  });

  // 缺陷①已修复（2026-10-16）：DeleteAddress 删除 0 行时返回 NotFound 业务错误，
  // 越权删除不再静默返回 code=0；SQL 带 user_id 条件保证越权行不会被真删
  test('删除他人地址被拒绝且原地址不受影响（越权防护）', async ({ playwright }) => {
    // 前置用例可能已清空地址，先确保有一条
    let mine = (await (await api.get('/api/v1/user/addresses')).json()) as ApiResponse<Address[] | { list: Address[] }>;
    let addresses = asList<Address>(mine.data);
    if (addresses.length === 0) {
      await api.post('/api/v1/user/addresses', {
        data: { contact_name: '越权测试', contact_phone: '13900000005', detail: '越权' },
      });
      mine = (await (await api.get('/api/v1/user/addresses')).json()) as ApiResponse<Address[] | { list: Address[] }>;
      addresses = asList<Address>(mine.data);
    }
    // 用另一个用户登录，尝试删除第一个用户的地址
    const other = await clientLogin(playwright.request, uniquePhone());
    const otherApi = await newAuthedRequest(playwright, other.access_token);
    try {
      const anyId = addresses[0].id;
      const resp = await otherApi.delete(`/api/v1/user/addresses/${anyId}`);
      const body = (await resp.json()) as ApiResponse;
      expect(body.code).not.toBe(0);

      // 越权删除不得影响原地址
      const after = (await (await api.get('/api/v1/user/addresses')).json()) as ApiResponse<Address[] | { list: Address[] }>;
      expect(asList<Address>(after.data).some((a) => a.id === anyId)).toBe(true);
    } finally {
      await otherApi.dispose();
    }
  });

  test('切换用户社区生效', async () => {
    // 从公开社区列表取一个不同于当前社区的社区
    const comms = (await (await requestCommunities()).json()) as ApiResponse<{ list: { id: number }[] }>;
    const profile = (await (await api.get('/api/v1/user/profile')).json()) as ApiResponse<{ community_id: number }>;
    const target = comms.data!.list.find((c) => c.id !== profile.data?.community_id);
    if (!target) {
      test.skip(true, '环境只有一个社区，跳过切换用例');
      return;
    }
    const resp = await api.put('/api/v1/user/community', { data: { community_id: target.id } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
    const after = (await (await api.get('/api/v1/user/profile')).json()) as ApiResponse<{ community_id: number }>;
    expect(after.data?.community_id).toBe(target.id);
  });

  /** 公开社区列表请求（复用已鉴权上下文，公开接口不受影响） */
  async function requestCommunities(): Promise<Response> {
    return api.get('/api/v1/communities') as unknown as Promise<Response>;
  }
});
