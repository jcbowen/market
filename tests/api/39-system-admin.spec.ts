import { test, expect } from '@playwright/test';
import { newAdminContext, asList, assertOk, type ApiResponse } from '../helpers/api';
import { uniquePhone, uniqueSuffix } from '../helpers/data';

/**
 * 模块 39：系统管理补充——员工账号生命周期
 * 覆盖：创建员工、列表 keyword 搜索、修改资料生效、停用后登录被拒（11006「账号已被禁用」）、
 *       重新启用后登录恢复、移除任职（删除）
 * 说明：
 * - 社区 PUT 修改生效（含整表覆盖语义与 status 回填保护）已由 33 号覆盖，此处不重复
 * - PUT /admins/:id 仅改资料与任职（RealName/Phone 为 string 无 presence，回传原值防清空）；
 *   停用/启用走专用接口 PUT /accounts/:id/status（超管专用，指针 presence）
 * - 角色须归属员工任职社区（community_id=0 的遗留角色不可用于社区任职，同 24 号前置）
 */

/** 管理端员工 VO（toAdminVO） */
interface AdminVO {
  id: number;
  username: string;
  real_name?: string;
  phone?: string;
  status?: number;
  role_code?: string;
  community_ids?: number[];
  [k: string]: unknown;
}

/** 角色 VO（ListRoles） */
interface Role {
  id: number;
  name: string;
  code?: string;
  community_id?: number;
  [k: string]: unknown;
}

const STAFF_PASS = 'AutoTest123';
const STAFF_USER = `auto_test_staff_${uniqueSuffix()}`;

let api: Awaited<ReturnType<typeof newAdminContext>>['api'];
let communityId = 0;
let roleId = 0;
let staffId = 0;

test.beforeAll(async ({ playwright }) => {
  const ctx = await newAdminContext(playwright);
  api = ctx.api;
  communityId = ctx.communityId;

  // 前置：创建归属当前社区的角色（员工任职必需）
  const role = assertOk(
    (await (
      await api.post('/api/v1/admin/roles', {
        data: {
          name: `AUTO-TEST 角色 ${uniqueSuffix()}`,
          code: `t_${uniqueSuffix()}`,
          permissions: ['product:view'],
          description: 'AUTO-TEST',
          community_id: communityId,
        },
      })
    ).json()) as ApiResponse<Role>,
    '创建前置角色',
  );
  roleId = role.id;
});

test.afterAll(async () => {
  // 清理：移除员工任职、删除角色（账号本身保留，任职已移除即可复用环境）
  try {
    if (staffId) await api?.delete(`/api/v1/admin/admins/${staffId}`);
    if (roleId) await api?.delete(`/api/v1/admin/roles/${roleId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await api?.dispose();
});

/** 员工资料更新体：回传原值避免 proto 无 presence 字段被覆盖（RealName/Phone 为 string 会清空） */
function staffUpdate(over: Partial<{ real_name: string; phone: string }>) {
  return {
    real_name: 'AUTO-TEST 员工',
    phone: uniquePhone(),
    role_ids: [roleId],
    ...over,
  };
}

test.describe.serial('员工账号生命周期', () => {
  test('1. 创建员工成功（默认启用）', async () => {
    const body = assertOk(
      (await (
        await api.post('/api/v1/admin/admins', {
          data: {
            username: STAFF_USER,
            password: STAFF_PASS,
            real_name: 'AUTO-TEST 员工',
            phone: uniquePhone(),
            role_ids: [roleId],
          },
        })
      ).json()) as ApiResponse<AdminVO>,
      '创建员工',
    );
    staffId = body.id;
    expect(staffId).toBeGreaterThan(0);
    expect(body.status).toBe(1);
  });

  test('2. 列表按用户名 keyword 搜索可定位', async () => {
    if (!staffId) test.skip(true, '员工未创建');
    const resp = await api.get('/api/v1/admin/admins', { params: { keyword: STAFF_USER, page: 1, page_size: 20 } });
    const body = (await resp.json()) as ApiResponse<{ list: AdminVO[] } | AdminVO[]>;
    expect(body.code).toBe(0);
    const list = asList<AdminVO>(body.data);
    const hit = list.find((a) => a.id === staffId);
    expect(hit, 'keyword 搜索应命中新建员工').toBeTruthy();
    expect(hit?.username).toBe(STAFF_USER);
  });

  test('3. 修改员工资料生效（real_name/phone）', async () => {
    if (!staffId) test.skip(true, '员工未创建');
    // 回传原值避免 proto 无 presence 的 string 字段被覆盖为空
    const resp = await api.put(`/api/v1/admin/admins/${staffId}`, {
      data: staffUpdate({ real_name: 'AUTO-TEST 员工改名' }),
    });
    const body = (await resp.json()) as ApiResponse<AdminVO>;
    expect(body.code, `修改资料失败: ${body.message}`).toBe(0);
    const detail = assertOk(
      (await (
        await api.get('/api/v1/admin/admins', { params: { keyword: STAFF_USER, page: 1, page_size: 20 } })
      ).json()) as ApiResponse<{ list: AdminVO[] }>,
      '回读员工列表',
    );
    const hit = asList<AdminVO>(detail).find((a) => a.id === staffId);
    expect(hit?.real_name).toBe('AUTO-TEST 员工改名');
  });

  test('4. 停用员工后登录被拒（11006 账号已被禁用）', async ({ request }) => {
    if (!staffId) test.skip(true, '员工未创建');
    // 停用走专用接口 PUT /accounts/:id/status（超管专用，指针 presence 避免 0 值歧义）；
    // PUT /admins/:id 的普通分支不处理 status
    const resp = await api.put(`/api/v1/admin/accounts/${staffId}/status`, { data: { status: 0 } });
    const body = (await resp.json()) as ApiResponse<AdminVO>;
    expect(body.code, `停用失败: ${body.message}`).toBe(0);
    expect(body.data?.status).toBe(0);

    const login = await request.post('/api/v1/admin/auth/login', {
      data: { username: STAFF_USER, password: STAFF_PASS },
    });
    const loginBody = (await login.json()) as ApiResponse;
    expect(loginBody.code, '停用账号登录应返回 11006').toBe(11006);
    expect(loginBody.message).toContain('禁用');
  });

  test('5. 重新启用后登录恢复', async ({ request }) => {
    if (!staffId) test.skip(true, '员工未创建');
    const resp = await api.put(`/api/v1/admin/accounts/${staffId}/status`, { data: { status: 1 } });
    const body = (await resp.json()) as ApiResponse<AdminVO>;
    expect(body.code, `启用失败: ${body.message}`).toBe(0);
    expect(body.data?.status).toBe(1);

    const login = await request.post('/api/v1/admin/auth/login', {
      data: { username: STAFF_USER, password: STAFF_PASS },
    });
    const loginBody = (await login.json()) as ApiResponse<{ access_token?: string }>;
    expect(loginBody.code, `启用后登录应成功: ${loginBody.message}`).toBe(0);
    expect(loginBody.data?.access_token).toBeTruthy();
  });

  test('6. 移除任职（删除）后列表不再包含', async () => {
    const id = staffId;
    if (!id) test.skip(true, '员工未创建');
    const resp = await api.delete(`/api/v1/admin/admins/${id}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `移除任职失败: ${body.message}`).toBe(0);
    staffId = 0; // 已清理，afterAll 不再重复删除

    const list = (await (
      await api.get('/api/v1/admin/admins', { params: { keyword: STAFF_USER, page: 1, page_size: 20 } })
    ).json()) as ApiResponse<{ list: AdminVO[] }>;
    expect(asList<AdminVO>(list.data).some((a) => a.id === id)).toBe(false);
  });
});
