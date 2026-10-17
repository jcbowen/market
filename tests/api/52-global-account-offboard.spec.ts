import { test, expect, type Playwright } from '@playwright/test';
import { newAdminContext, asList, assertOk, type ApiResponse, type PageData } from '../helpers/api';

/**
 * 模块 52：全局账号 + 社区管理员卸任
 * 覆盖（超管专用接口）：
 *   - POST /admin/accounts：新建不任职账号（AccountOnly，不挂任何社区角色）
 *   - 用户名重复创建被拒（「用户名已存在」）
 *   - 不任职账号可登录管理端
 *   - PUT /accounts/:id/password：免旧密码重置（Force），旧密码失效、新密码可登录
 *   - POST /communities/:id/admins：已存在用户名复用账号任职（不改密码）
 *   - DELETE /communities/:id/admins/:adminId 卸任：仅摘社区管理员角色、账号保留（accounts 列表仍在）
 *   - 再次指定可复职：同 username 重新任职成功，且密码未被复职操作重置
 * 说明：PUT /admins/:id 资料编辑与停用/启用已在 39 号覆盖，此处不重复
 */

interface AdminVO {
  id: number;
  username: string;
  real_name?: string;
  status?: number;
  community_ids?: number[];
  [k: string]: unknown;
}

let superApi: Awaited<ReturnType<typeof newAdminContext>>['api'];

/** 账号隔离标识 */
const ACCOUNT_USER = `acct${Date.now().toString().slice(-8)}`;
const FIRST_PASS = 'Acct123456';
const RESET_PASS = 'Reset654321';
const ACCOUNT_NAME = 'AUTO-TEST 52 全局账号';

/**
 * 按关键词查询全局账号列表
 * - 函数名: listAccounts
 * - 参数: keyword — 用户名/姓名关键词
 * - 返回: Promise<AdminVO[]> 列表
 * - 异常: 业务码非 0 时经 assertOk 抛出
 * - 使用示例: const rows = await listAccounts(ACCOUNT_USER);
 */
async function listAccounts(keyword: string): Promise<AdminVO[]> {
  const body = (await (
    await superApi.get('/api/v1/admin/accounts', { params: { page: 1, page_size: 50, keyword } })
  ).json()) as ApiResponse<PageData<AdminVO>>;
  return asList<AdminVO>(assertOk(body, '全局账号列表'));
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  superApi = admin.api;
});

test.afterAll(async () => {
  await superApi?.dispose();
});

test.describe.serial('全局账号与社区管理员卸任', () => {
  let accountId = 0;
  let communityId = 0;
  let resetAt = false;

  test('1. 新建不任职账号（AccountOnly）', async () => {
    const body = (await (
      await superApi.post('/api/v1/admin/accounts', {
        data: { username: ACCOUNT_USER, password: FIRST_PASS, real_name: ACCOUNT_NAME, phone: '13800005252' },
      })
    ).json()) as ApiResponse<AdminVO>;
    expect(body.code, `创建账号失败: ${body.message}`).toBe(0);
    expect(body.data?.username).toBe(ACCOUNT_USER);
    expect(body.data?.status).toBe(1);
    accountId = body.data!.id;
    expect(accountId).toBeGreaterThan(0);
  });

  test('2. 用户名重复创建被拒', async () => {
    if (!accountId) test.skip(true, '账号未创建');
    const body = (await (
      await superApi.post('/api/v1/admin/accounts', {
        data: { username: ACCOUNT_USER, password: 'Another123', real_name: '重复名' },
      })
    ).json()) as ApiResponse<AdminVO>;
    expect(body.code, '重复用户名应被拒绝').not.toBe(0);
    expect(body.message).toContain('已存在');
  });

  test('3. 不任职账号可登录管理端（无社区语境）', async ({ request }) => {
    if (!accountId) test.skip(true, '账号未创建');
    // 不任职账号无可选社区，newAdminContext 会拒绝建语境，直接断言登录成功
    const login = await request.post('/api/v1/admin/auth/login', {
      data: { username: ACCOUNT_USER, password: FIRST_PASS },
    });
    const body = (await login.json()) as ApiResponse<{ access_token?: string }>;
    expect(body.code, `不任职账号登录应成功: ${body.message}`).toBe(0);
    expect(body.data?.access_token, '应颁发 access_token').toBeTruthy();
  });

  test('4. 重置密码免旧密码：旧密码失效、新密码可登录', async ({ request }) => {
    if (!accountId) test.skip(true, '账号未创建');
    const body = (await (
      await superApi.put(`/api/v1/admin/accounts/${accountId}/password`, { data: { new_password: RESET_PASS } })
    ).json()) as ApiResponse;
    expect(body.code, `重置密码失败: ${body.message}`).toBe(0);
    resetAt = true;

    // 旧密码登录失败
    const oldLogin = await request.post('/api/v1/admin/auth/login', {
      data: { username: ACCOUNT_USER, password: FIRST_PASS },
    });
    const oldBody = (await oldLogin.json()) as ApiResponse;
    expect(oldBody.code, '旧密码应已失效').not.toBe(0);

    // 新密码登录成功
    const newLogin = await request.post('/api/v1/admin/auth/login', {
      data: { username: ACCOUNT_USER, password: RESET_PASS },
    });
    const newBody = (await newLogin.json()) as ApiResponse<{ access_token?: string }>;
    expect(newBody.code, '新密码应可登录').toBe(0);
    expect(newBody.data?.access_token).toBeTruthy();
  });

  test('5. 指定为社区管理员：复用已存在账号任职（不改密码）', async () => {
    if (!accountId) test.skip(true, '账号未创建');
    const comm = assertOk(
      (await (
        await superApi.post('/api/v1/admin/communities', {
          data: {
            name: `AUTO-TEST 52 卸任社区 ${Date.now()}`,
            address: 'AUTO-TEST 地址',
            status: 1,
            delivery_fee: 0,
            min_order_amount: 0,
          },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建测试社区',
    );
    communityId = comm.id;

    const assigned = (await (
      await superApi.post(`/api/v1/admin/communities/${communityId}/admins`, {
        data: { username: ACCOUNT_USER, password: 'Ignored123', real_name: ACCOUNT_NAME },
      })
    ).json()) as ApiResponse<AdminVO>;
    expect(assigned.code, `指定社区管理员失败: ${assigned.message}`).toBe(0);
    expect(assigned.data?.id, '应复用已有账号而非新建').toBe(accountId);

    const admins = asList<AdminVO>(
      assertOk(
        (await (
          await superApi.get(`/api/v1/admin/communities/${communityId}/admins`)
        ).json()) as ApiResponse<PageData<AdminVO>>,
        '社区管理员列表',
      ),
    );
    expect(admins.some((a) => a.id === accountId), '现任列表应含该账号').toBe(true);
  });

  test('6. 卸任：仅摘角色，账号保留', async () => {
    if (!accountId || !communityId) test.skip(true, '前置未就绪');
    const body = (await (
      await superApi.delete(`/api/v1/admin/communities/${communityId}/admins/${accountId}`)
    ).json()) as ApiResponse;
    expect(body.code, `卸任失败: ${body.message}`).toBe(0);

    // 现任列表为空
    const admins = asList<AdminVO>(
      assertOk(
        (await (
          await superApi.get(`/api/v1/admin/communities/${communityId}/admins`)
        ).json()) as ApiResponse<PageData<AdminVO>>,
        '卸任后管理员列表',
      ),
    );
    expect(admins.some((a) => a.id === accountId), '卸任后不应再在任').toBe(false);

    // 账号保留：全局账号列表仍可查到
    const rows = await listAccounts(ACCOUNT_USER);
    const kept = rows.find((a) => a.id === accountId);
    expect(kept, '卸任仅摘角色，账号应保留').toBeTruthy();
    expect(kept?.community_ids ?? [], '任职社区应为空').toEqual([]);
  });

  test('7. 再次指定可复职（密码不被复职操作重置）', async ({ request }) => {
    if (!accountId || !communityId) test.skip(true, '前置未就绪');
    const reassign = (await (
      await superApi.post(`/api/v1/admin/communities/${communityId}/admins`, {
        data: { username: ACCOUNT_USER, password: 'Ignored456', real_name: ACCOUNT_NAME },
      })
    ).json()) as ApiResponse<AdminVO>;
    expect(reassign.code, `复职失败: ${reassign.message}`).toBe(0);
    expect(reassign.data?.id).toBe(accountId);

    const admins = asList<AdminVO>(
      assertOk(
        (await (
          await superApi.get(`/api/v1/admin/communities/${communityId}/admins`)
        ).json()) as ApiResponse<PageData<AdminVO>>,
        '复职后管理员列表',
      ),
    );
    expect(admins.some((a) => a.id === accountId), '复职后应在任').toBe(true);

    // 复职不重置密码：卸任前重置的密码仍可登录
    test.skip(!resetAt, '前置密码重置未执行');
    const login = await request.post('/api/v1/admin/auth/login', {
      data: { username: ACCOUNT_USER, password: RESET_PASS },
    });
    const body = (await login.json()) as ApiResponse;
    expect(body.code, '复职不应重置密码，原密码应仍有效').toBe(0);
  });
});
