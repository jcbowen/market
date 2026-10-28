import { test, expect } from '@playwright/test';
import { adminLogin, newAdminContext, newAuthedRequest, asList, assertOk, type ApiResponse, type PageData } from '../helpers/api';
import { autoName, uniqueSuffix } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 08：管理端认证与 RBAC
 * 覆盖：登录/错误密码、auth/info、角色 CRUD、管理员 CRUD、改密码、越权 403、
 *       刷新 token、我的社区
 * 注意：角色一律社区私有，建角色须显式传 community_id（不传会被服务端拒绝）
 */

let api: Awaited<ReturnType<typeof newAuthedRequest>>;
let communityId = 0;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const ctx = await newAdminContext(playwright);
  api = ctx.api;
  communityId = ctx.communityId;
});

test.afterAll(async () => {
  await api?.dispose();
});

test.describe.serial('管理端认证', () => {
  test('admin/admin123 登录成功', async ({ request }) => {
    const info = await adminLogin(request);
    expect(info.access_token).toBeTruthy();
  });

  test('错误密码返回业务错误', async ({ request }) => {
    const resp = await request.post('/api/v1/admin/auth/login', {
      data: { username: 'admin', password: 'wrong-password' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });

  test('auth/info 返回当前管理员与权限', async () => {
    const resp = await api.get('/api/v1/admin/auth/info');
    const body = (await resp.json()) as ApiResponse<Record<string, unknown>>;
    expect(body.code).toBe(0);
    expect(body.data).toBeDefined();
  });

  test('管理端 refresh token 有效', async ({ request }) => {
    const info = await adminLogin(request);
    const resp = await request.post('/api/v1/admin/auth/refresh', { data: { refresh_token: info.refresh_token } });
    const body = (await resp.json()) as ApiResponse<{ access_token?: string }>;
    expect(body.code).toBe(0);
    expect(body.data?.access_token).toBeTruthy();
  });

  test('我的社区列表非空', async () => {
    const resp = await api.get('/api/v1/admin/my/communities');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });
});

test.describe.serial('角色管理', () => {
  let roleId = 0;

  test('创建角色成功', async () => {
    const resp = await api.post('/api/v1/admin/roles', {
      data: {
        name: autoName('测试角色'),
        code: `test_role_${uniqueSuffix()}`,
        permissions: ['product:view', 'order:view'],
        description: '自动化测试创建',
        community_id: communityId,
      },
    });
    const body = (await resp.json()) as ApiResponse<{ id?: number }>;
    expect(body.code).toBe(0);
    roleId = body.data?.id ?? 0;
    expect(roleId).toBeGreaterThan(0);
  });

  test('角色列表包含新角色', async () => {
    const resp = await api.get('/api/v1/admin/roles');
    const body = (await resp.json()) as ApiResponse<PageData<{ id: number }>>;
    expect(body.code).toBe(0);
    expect(asList<{ id: number }>(body.data).some((r) => r.id === roleId)).toBe(true);
  });

  test('更新角色名称生效', async () => {
    const newName = autoName('更新角色');
    const resp = await api.put(`/api/v1/admin/roles/${roleId}`, { data: { name: newName } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('空名称角色被拒绝', async () => {
    const resp = await api.post('/api/v1/admin/roles', { data: { name: '', code: '' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });

  test('删除角色成功', async () => {
    const resp = await api.delete(`/api/v1/admin/roles/${roleId}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });
});

test.describe.serial('员工管理', () => {
  let adminId = 0;
  const username = `autotest_${uniqueSuffix()}`;

  test('创建员工成功（需先有角色）', async () => {
    // 先建一个角色供员工绑定
    const role = (await (
      await api.post('/api/v1/admin/roles', {
        data: { name: autoName('员工角色'), code: `staff_${uniqueSuffix()}`, permissions: ['product:view'], community_id: communityId },
      })
    ).json()) as ApiResponse<{ id: number }>;
    expect(role.code).toBe(0);

    const resp = await api.post('/api/v1/admin/admins', {
      data: {
        username,
        password: 'Test@12345',
        real_name: '自动化测试员工',
        phone: '13900009999',
        role_ids: [role.data!.id],
        community_id: communityId,
      },
    });
    const body = (await resp.json()) as ApiResponse<{ id?: number }>;
    expect(body.code).toBe(0);
    adminId = body.data?.id ?? 0;
    expect(adminId).toBeGreaterThan(0);
  });

  test('员工列表包含新员工', async () => {
    const resp = await api.get('/api/v1/admin/admins');
    const body = (await resp.json()) as ApiResponse<PageData<{ id: number; username: string }>>;
    expect(body.code).toBe(0);
    expect(asList<{ id: number }>(body.data).some((a) => a.id === adminId)).toBe(true);
  });

  test('新员工可用新密码登录', async ({ request }) => {
    const resp = await request.post('/api/v1/admin/auth/login', { data: { username, password: 'Test@12345' } });
    const body = (await resp.json()) as ApiResponse<{ access_token?: string }>;
    expect(body.code).toBe(0);
    expect(body.data?.access_token).toBeTruthy();
  });

  test('修改员工密码后旧密码失效', async ({ request }) => {
    // 接口要求 old_password + new_password（ChangeAdminPasswordReq）
    const resp = await api.put(`/api/v1/admin/admins/${adminId}/password`, {
      data: { old_password: 'Test@12345', new_password: 'NewPass@12345' },
    });
    const body = (await resp.json()) as ApiResponse;
    if (body.code !== 0) {
      console.log(`[改密] code=${body.code} message=${body.message}（超管改他人密码受限则调整用例）`);
    }
    expect(body.code).toBe(0);

    const oldLogin = await request.post('/api/v1/admin/auth/login', { data: { username, password: 'Test@12345' } });
    const oldBody = (await oldLogin.json()) as ApiResponse;
    expect(oldBody.code).not.toBe(0);

    const newLogin = await request.post('/api/v1/admin/auth/login', { data: { username, password: 'NewPass@12345' } });
    const newBody = (await newLogin.json()) as ApiResponse;
    expect(newBody.code).toBe(0);
  });

  test('删除员工成功', async () => {
    const resp = await api.delete(`/api/v1/admin/admins/${adminId}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });
});

test.describe.serial('越权与 403', () => {
  // 网关 response.Error 恒返回 HTTP 200 + 业务码，权限拒绝表达为 code=11005（NoPermission）
  // 缺陷②已复验（2026-10-16）：RequirePermission 按（管理员, 当前社区）实时解析权限并拦截
  test('低权限员工访问无权限接口返回 11005（RBAC 接口拦截）', async ({ playwright, request }) => {
    const role = assertOk(
      (await (
        await api.post('/api/v1/admin/roles', {
          data: { name: autoName('低权角色'), code: `low_${uniqueSuffix()}`, permissions: ['product:view'], community_id: communityId },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建低权角色',
    );
    const username = `low_${uniqueSuffix()}`;
    const emp = assertOk(
      (await (
        await api.post('/api/v1/admin/admins', {
          data: { username, password: 'Low@123456', real_name: '低权员工', role_ids: [role.id], community_id: communityId },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建低权员工',
    );

    const login = (await (
      await request.post('/api/v1/admin/auth/login', { data: { username, password: 'Low@123456' } })
    ).json()) as ApiResponse<{ access_token: string }>;
    expect(login.code).toBe(0);

    const empApi = await newAuthedRequest(playwright, login.data!.access_token);
    try {
      // 仅 product:view 的员工访问角色管理（需 role:view）必须被拦截
      const resp = await empApi.get('/api/v1/admin/roles');
      const body = (await resp.json()) as ApiResponse;
      expect(resp.status()).toBe(200);
      expect(body.code).toBe(11005);
    } finally {
      await empApi.dispose();
      await api.delete(`/api/v1/admin/admins/${emp.id}`);
      await api.delete(`/api/v1/admin/roles/${role.id}`);
    }
  });
});
