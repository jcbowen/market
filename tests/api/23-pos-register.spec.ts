import { test, expect, type Playwright } from '@playwright/test';
import { clientLogin, newAdminContext, assertOk, asList, type ApiResponse } from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 23：POS 现场注册会员
 * 覆盖：POS 场景注册新会员（缺省昵称）→ 同号重复注册幂等复用（is_new=false、id 不变）
 *       → 跨社区复用（幂等补写社区关联）→ 非法手机号拒绝
 *       → 注册后 C 端可直接登录 → 后台会员列表可见
 * 语义：手机号全局唯一，已注册（含其他社区）复用既有会员并补写本社区关联；
 *       is_new 仅首次新注册为 true（新用户活动只对 is_new=true 生效）
 */

interface RegisterResult {
  id: number;
  phone: string;
  nickname: string;
  gender: number;
  status: number;
  created_at: string;
  is_new: boolean;
}

interface MemberRow {
  id: number;
  phone: string;
  nickname: string;
  status: number;
  [k: string]: unknown;
}

let superAdminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let primaryCommunity = 0;
let secondCommunity = 0;

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  superAdminApi = admin.api;
  primaryCommunity = admin.communityId;
  // 找第二个社区做跨社区复用验证（环境只有单社区时跳过）
  try {
    const resp = await superAdminApi.get('/api/v1/admin/my/communities');
    const body = (await resp.json()) as ApiResponse<{ id: number }[]>;
    const list = asList<{ id: number }>(body.data);
    const other = list.find((c) => c.id !== primaryCommunity);
    secondCommunity = other?.id ?? 0;
  } catch {
    secondCommunity = 0;
  }
});

test.afterAll(async () => {
  await superAdminApi?.dispose();
});

test.describe.serial('POS 注册与幂等复用', () => {
  const phone = uniquePhone();

  test('1. POS 场景注册新会员：is_new=true 且缺省昵称为会员+尾号', async () => {
    const body = assertOk(
      (await (
        await superAdminApi.post('/api/v1/admin/users', {
          data: { phone, scene: 'pos' },
        })
      ).json()) as ApiResponse<RegisterResult>,
      'POS 注册新会员',
    );
    expect(body.id).toBeGreaterThan(0);
    expect(body.is_new).toBe(true);
    expect(body.phone).toBe(phone);
    expect(body.status).toBe(1);
    expect(body.nickname).toBe(`会员${phone.slice(-4)}`);
  });

  test('2. 同手机号再次注册（admin 场景）幂等复用', async () => {
    const body = assertOk(
      (await (
        await superAdminApi.post('/api/v1/admin/users', {
          data: { phone, nickname: '不应覆盖新昵称', gender: 1, scene: 'admin' },
        })
      ).json()) as ApiResponse<RegisterResult>,
      '重复注册',
    );
    expect(body.is_new).toBe(false);
    expect(body.phone).toBe(phone);
    // 复用既有会员：不应重建（id 不变），昵称不被覆盖
    const first = (await (
      await superAdminApi.get('/api/v1/admin/users', { params: { keyword: phone } })
    ).json()) as ApiResponse<{ list?: MemberRow[] }>;
    const rows = asList<MemberRow>(first.data);
    const found = rows.find((r) => r.phone === phone);
    expect(found, '会员列表应可见该会员').toBeTruthy();
    expect(found!.id).toBe(body.id);
    expect(found!.nickname).toBe(`会员${phone.slice(-4)}`);
  });

  test('3. 自定义昵称注册全新手机号生效', async () => {
    const phone2 = uniquePhone();
    const body = assertOk(
      (await (
        await superAdminApi.post('/api/v1/admin/users', {
          data: { phone: phone2, nickname: 'AUTO-TEST 张三', gender: 2, scene: 'pos' },
        })
      ).json()) as ApiResponse<RegisterResult>,
      '自定义昵称注册',
    );
    expect(body.is_new).toBe(true);
    expect(body.nickname).toBe('AUTO-TEST 张三');
    expect(body.gender).toBe(2);
  });

  test('4. 非法手机号被拒绝', async () => {
    const resp = await superAdminApi.post('/api/v1/admin/users', {
      data: { phone: '123', scene: 'pos' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '非法手机号应被拒绝').not.toBe(0);
  });

  test('5. 跨社区注册同手机号复用同一会员', async ({ playwright }) => {
    test.skip(secondCommunity <= 0, '环境仅有单社区，无法验证跨社区');
    const other = await newAdminContext(playwright, undefined, undefined, secondCommunity);
    try {
      const body = assertOk(
        (await (
          await other.api.post('/api/v1/admin/users', { data: { phone, scene: 'pos' } })
        ).json()) as ApiResponse<RegisterResult>,
        '跨社区注册',
      );
      expect(body.is_new).toBe(false);
      // 与主社区注册的会员是同一个（手机号全局唯一）
      const primaryList = (await (
        await superAdminApi.get('/api/v1/admin/users', { params: { keyword: phone } })
      ).json()) as ApiResponse<{ list?: MemberRow[] }>;
      const rows = asList<MemberRow>(primaryList.data);
      const found = rows.find((r) => r.phone === phone);
      expect(found, '主社区会员列表应可见').toBeTruthy();
      expect(body.id).toBe(found!.id);
    } finally {
      await other.api.dispose();
    }
  });

  test('6. POS 注册的手机号可直接 C 端登录', async ({ playwright }) => {
    const info = await clientLogin(playwright.request, phone);
    expect(info.access_token).toBeTruthy();
    expect(info.user.community_id, 'C 端登录应绑定社区').toBeGreaterThan(0);
  });

  test('7. 后台会员列表可见 POS 注册会员', async () => {
    const body = assertOk(
      (await (
        await superAdminApi.get('/api/v1/admin/users', { params: { keyword: phone, page: 1, page_size: 10 } })
      ).json()) as ApiResponse<{ list?: MemberRow[] }>,
      '会员列表',
    );
    const rows = asList<MemberRow>(body);
    expect(rows.some((r) => r.phone === phone), '会员列表应包含 POS 注册的会员').toBe(true);
  });
});
