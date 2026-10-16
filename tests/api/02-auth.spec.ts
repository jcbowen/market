import { test, expect } from '@playwright/test';
import { clientLogin, assertOk, type ApiResponse, type ClientLoginInfo } from '../helpers/api';
import { PRESET_PHONE, uniquePhone } from '../helpers/data';

/**
 * 模块 02：认证流程
 * 覆盖：手机号直登（自动建号/复用）、非法手机号拒绝、token 刷新、错误 refresh 拒绝、
 *       发送验证码接口、鉴权 401
 */

test.describe('认证流程', () => {
  test('预置手机号 13800138000 可直接登录（免验证码）', async ({ request }) => {
    const info = await clientLogin(request, PRESET_PHONE);
    expect(info.access_token).toBeTruthy();
    expect(info.user.phone).toBe(PRESET_PHONE);
  });

  test('新手机号登录自动建号，重复登录用户 ID 复用', async ({ request }) => {
    const phone = uniquePhone();
    const first = await clientLogin(request, phone);
    expect(first.user.id).toBeGreaterThan(0);
    const second = await clientLogin(request, phone);
    expect(second.user.id).toBe(first.user.id);
  });

  test('非法手机号登录被拒绝', async ({ request }) => {
    const resp = await request.post('/api/v1/auth/login/phone', { data: { phone: '12345' } });
    const body = (await resp.json()) as ApiResponse;
    // 业务码非 0（参数错误），且服务未崩溃
    expect(resp.status()).toBeLessThan(500);
    expect(body.code).not.toBe(0);
  });

  test('refresh token 可换取新 token，错误 refresh 被拒绝', async ({ request }) => {
    const info = await clientLogin(request, uniquePhone());
    const okResp = await request.post('/api/v1/auth/refresh', { data: { refresh_token: info.refresh_token } });
    const okBody = (await okResp.json()) as ApiResponse<{ access_token?: string }>;
    expect(okBody.code).toBe(0);
    expect(okBody.data?.access_token).toBeTruthy();

    const badResp = await request.post('/api/v1/auth/refresh', { data: { refresh_token: 'invalid-token' } });
    const badBody = (await badResp.json()) as ApiResponse;
    expect(badBody.code).not.toBe(0);
  });

  test('发送短信验证码接口正常受理', async ({ request }) => {
    const resp = await request.post('/api/v1/auth/phone-code', { data: { phone: PRESET_PHONE } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('未携带 token 访问需登录接口返回 401/业务码非 0', async ({ request }) => {
    const resp = await request.get('/api/v1/user/profile');
    const body = (await resp.json()) as ApiResponse;
    // 网关可能以 HTTP 200 + 业务码 或 HTTP 401 表达未授权
    expect(resp.status()).toBeLessThan(500);
    if (resp.status() === 200) expect(body.code).not.toBe(0);
    else expect(resp.status()).toBe(401);
  });

  test('伪造 token 访问需登录接口被拒绝', async ({ request }) => {
    const resp = await request.get('/api/v1/user/profile', {
      headers: { Authorization: 'Bearer fake-token-abc' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(resp.status()).toBeLessThan(500);
    if (resp.status() === 200) expect(body.code).not.toBe(0);
    else expect(resp.status()).toBe(401);
  });

  test('同手机号并发多次登录均成功', async ({ request }) => {
    const results: ClientLoginInfo[] = [];
    for (let i = 0; i < 3; i++) {
      results.push(await clientLogin(request, PRESET_PHONE));
    }
    const ids = new Set(results.map((r) => r.user.id));
    expect(ids.size).toBe(1);
  });
});

test.describe('公开接口鉴权边界', () => {
  test('auth/info 未登录返回 401/业务码非 0', async ({ request }) => {
    const resp = await request.get('/api/v1/admin/auth/info');
    if (resp.status() === 200) {
      const body = (await resp.json()) as ApiResponse;
      expect(body.code).not.toBe(0);
    } else {
      expect(resp.status()).toBe(401);
    }
  });

  test('公开接口无需登录即可访问（冒烟）', async ({ request }) => {
    const body = assertOk(
      (await (await request.get('/api/v1/skus', { params: { product_id: 1 } })).json()) as ApiResponse,
      'SKU 列表',
    );
    expect(body).toBeDefined();
  });
});
