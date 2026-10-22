import { test, expect, type APIRequestContext } from '@playwright/test';
import { type ApiResponse } from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 53：手机号验证码登录完整链路
 * 覆盖：
 *   - POST /auth/phone-code 发码成功（expire=300，手机号回显）
 *   - 发码/登录手机号非法格式被参数校验拒绝（10001「手机号格式错误」）
 *   - 登录缺手机号被拒绝（10001）
 *   - POST /auth/login/phone：后端开发态不校验验证码（gateway/handler/auth.go PhoneLogin
 *     只做 isValidPhone 格式校验，code 字段兼容保留），按此口径断言登录成功
 *   - 新手机号自动注册返回 user，并按后台同口径补默认昵称「会员+尾号4位」；重复登录 user.id 一致
 *   - 用户自定义昵称不被后续登录覆盖（登录链路仅在昵称为空时填默认值）
 *   - 登录 token 可访问需认证接口
 * 说明：微信登录（真实 code2Session）在排除项清单，此处不覆盖
 */

interface LoginInfo {
  access_token: string;
  refresh_token: string;
  expires_in: number;
  user: { id: number; phone: string; nickname?: string; community_id?: number };
}

/** 本模块专用手机号（uniquePhone 保证合法 11 位且不与历史数据冲突） */
const PHONE = uniquePhone();

/**
 * 手机号验证码登录
 * - 函数名: phoneLogin
 * - 参数: request — 请求上下文；phone — 手机号；code — 验证码（开发态不校验，可省略）
 * - 返回: Promise<ApiResponse<LoginInfo>>
 * - 异常: 网络错误由 Playwright 抛出
 * - 使用示例: const body = await phoneLogin(request, PHONE);
 */
async function phoneLogin(
  request: APIRequestContext,
  phone: string,
  code?: string,
): Promise<ApiResponse<LoginInfo>> {
  return (await (
    await request.post('/api/v1/auth/login/phone', { data: code ? { phone, code } : { phone } })
  ).json()) as ApiResponse<LoginInfo>;
}

test.describe.serial('手机号验证码登录链路', () => {
  test('1. 发码成功：expire=300 且手机号回显', async ({ request }) => {
    const body = (await (
      await request.post('/api/v1/auth/phone-code', { data: { phone: PHONE } })
    ).json()) as ApiResponse<{ phone: string; expire: number }>;
    expect(body.code, `发码失败: ${body.message}`).toBe(0);
    expect(body.data?.phone).toBe(PHONE);
    expect(body.data?.expire, '验证码有效期 5 分钟').toBe(300);
  });

  test('2. 发码手机号非法格式被拒（10001）', async ({ request }) => {
    const body = (await (
      await request.post('/api/v1/auth/phone-code', { data: { phone: '12345' } })
    ).json()) as ApiResponse;
    expect(body.code, '非法手机号应被拒绝').toBe(10001);
    expect(body.message).toContain('手机号格式错误');
  });

  test('3. 登录缺手机号被拒（10001）', async ({ request }) => {
    const body = (await (
      await request.post('/api/v1/auth/login/phone', { data: {} })
    ).json()) as ApiResponse;
    expect(body.code).toBe(10001);
  });

  test('4. 登录手机号非法格式被拒（10001）', async ({ request }) => {
    const body = (await (
      await request.post('/api/v1/auth/login/phone', { data: { phone: 'abc-def-gh' } })
    ).json()) as ApiResponse;
    expect(body.code, '非法手机号应被拒绝').toBe(10001);
    expect(body.message).toContain('手机号格式错误');
  });

  test('5. 新手机号登录自动注册（开发态不校验验证码）', async ({ request }) => {
    // 开发口径：仅校验手机号格式，code 传任意值不影响登录
    const body = await phoneLogin(request, PHONE, '000000');
    expect(body.code, `登录失败: ${body.message}`).toBe(0);
    expect(body.data?.access_token).toBeTruthy();
    expect(body.data?.refresh_token).toBeTruthy();
    expect(body.data?.expires_in).toBeGreaterThan(0);
    expect(body.data?.user?.id).toBeGreaterThan(0);
    expect(body.data?.user?.phone).toBe(PHONE);
    expect(body.data?.user?.nickname, '自动注册应补默认昵称').toBe(`会员${PHONE.slice(-4)}`);
  });

  test('6. 重复登录 user.id 一致', async ({ request }) => {
    const first = await phoneLogin(request, PHONE);
    const second = await phoneLogin(request, PHONE);
    expect(first.code).toBe(0);
    expect(second.code).toBe(0);
    expect(second.data?.user?.id, '同一手机号应复用同一账号').toBe(first.data?.user?.id);
  });

  test('7. 登录 token 可访问需认证接口', async ({ request }) => {
    const login = await phoneLogin(request, PHONE);
    expect(login.code).toBe(0);
    const resp = await request.get('/api/v1/user/coupons', {
      headers: { Authorization: `Bearer ${login.data!.access_token}` },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `需认证接口应可访问: ${body.message}`).toBe(0);
  });

  test('8. 自定义昵称不被重复登录覆盖', async ({ request }) => {
    const login = await phoneLogin(request, PHONE);
    expect(login.code).toBe(0);
    const authHeaders = { Authorization: `Bearer ${login.data!.access_token}` };

    const updated = (await (
      await request.put('/api/v1/user/profile', { data: { nickname: '张阿姨' }, headers: authHeaders })
    ).json()) as ApiResponse<{ nickname?: string }>;
    expect(updated.code, `修改昵称失败: ${updated.message}`).toBe(0);
    expect(updated.data?.nickname).toBe('张阿姨');

    const again = await phoneLogin(request, PHONE);
    expect(again.code).toBe(0);
    expect(again.data?.user?.nickname, '登录链路不应覆盖用户自定义昵称').toBe('张阿姨');
  });
});
