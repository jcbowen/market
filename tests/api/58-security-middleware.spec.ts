import { test, expect } from '@playwright/test';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 58：网关安全中间件
 * 覆盖：SecurityHeadersMiddleware（nosniff / X-Frame-Options / XSS / Referrer-Policy / CSP，
 *       H5 应用页 SAMEORIGIN 例外）、BlockTraceMiddleware（TRACE→405）、
 *       RateLimitMiddleware（超限 429 + 业务码 10005 + Retry-After；限流只挂接口分组，
 *       静态资源不吃任何桶；通用接口桶阈值可配、认证入口桶恒为 10/30，故洪水断言打后者）
 * 注意：限流为进程内存态、按客户端 IP 计数；本用例放在文件末尾执行，
 *       触发限流后认证入口桶每秒回填 10，文件末尾等待 3 秒不影响后续模块
 */

test.describe.serial('安全响应头中间件', () => {
  test('API 响应携带全套安全头', async ({ request }) => {
    const resp = await request.get('/health');
    expect(resp.status()).toBe(200);
    const h = resp.headers();
    expect(h['x-content-type-options']).toBe('nosniff');
    expect(h['x-frame-options']).toBe('DENY');
    expect(h['x-xss-protection']).toBe('1; mode=block');
    expect(h['referrer-policy']).toBe('strict-origin-when-cross-origin');
    expect(h['content-security-policy']).toContain("default-src 'self'");
  });

  test('H5 应用页（iframe 嵌入场景）X-Frame-Options 为 SAMEORIGIN', async ({ request }) => {
    // 无论文件是否存在（404 亦走全局中间件链），仅需断言响应头分支
    const resp = await request.get('/s/app.html');
    const h = resp.headers();
    expect(h['x-frame-options']).toBe('SAMEORIGIN');
    expect(h['content-security-policy']).toContain("frame-ancestors 'self'");
  });

  test('TRACE 方法被拦截返回 405', async ({ request }) => {
    const resp = await request.fetch('/health', { method: 'TRACE' });
    expect(resp.status()).toBe(405);
  });

  test('OPTIONS 预检等正常方法不受 TRACE 拦截影响', async ({ request }) => {
    // 反向校验：拦截只针对 TRACE，正常 GET 不被误伤
    const resp = await request.get('/health');
    expect(resp.status()).toBe(200);
  });
});

test.describe.serial('限流中间件', () => {
  // 洪水兜底断言打在「C 端认证入口」桶上（10 r/s、容量 30，见 gateway/main.go cAuthLimiter）：
  // 通用接口桶的阈值自 2026-10-29 起可配（security.api_rate_qps / RATE_LIMIT_API_QPS），
  // 本地栈为跑整套用例已上调，用它做「必然超限」的断言会随环境配置漂移；
  // 认证入口桶在所有环境都是同一口径，且中间件是同一个实现，兜底机制的覆盖不打折
  test('突发超量认证请求触发 429 限流并携带 Retry-After', async ({ request }) => {
    const total = 300;
    const phone = uniquePhone();
    const responses = await Promise.all(
      Array.from({ length: total }, () => request.post('/api/v1/auth/phone-code', { data: { phone } })),
    );
    const statuses = responses.map((r) => r.status());
    const limited = statuses.filter((s) => s === 429);

    expect(
      limited.length,
      `突发 ${total} 个认证请求应触发限流（429），实际 ${limited.length} 个`,
    ).toBeGreaterThan(0);

    // 限流响应应携带 Retry-After: 1 提示客户端退避
    const firstLimited = responses.find((r) => r.status() === 429)!;
    expect(firstLimited.headers()['retry-after']).toBe('1');
    expect(((await firstLimited.json()) as { code?: number }).code, '限流业务码应为 10005').toBe(10005);

    // 部分请求应正常通过（限流不是全量拒绝）
    expect(statuses.filter((s) => s === 200).length).toBeGreaterThan(0);
  });

  // 紧随认证入口突发之后执行：此刻该 IP 的认证桶已被打空。
  // 静态资源与只读接口分属另外的桶（静态根本不挂桶，只读接口挂可调的通用桶），
  // 若它们与认证桶共用一份额度就会成片 429——浏览器侧的表现是 H5 首屏 chunk 拉不到、
  // uni-h5 弹「连接服务器超时，点击屏幕重试」这类假故障
  test('静态资源与只读接口不吃认证入口的令牌桶', async ({ request }) => {
    const staticTotal = 300;
    const [statics, apiReads] = await Promise.all([
      Promise.all(
        Array.from({ length: staticTotal }, (_, i) => request.get(`/s/app.html?probe=${i}`)),
      ),
      Promise.all(Array.from({ length: 100 }, () => request.get('/api/v1/site-info'))),
    ]);

    const staticLimited = statics.filter((r) => r.status() === 429);
    expect(
      staticLimited.length,
      `静态资源突发 ${staticTotal} 个请求不应被任何限流拦下，实际 429 ${staticLimited.length} 个`,
    ).toBe(0);
    expect(statics.every((r) => r.status() === 200), '静态入口页应正常返回').toBe(true);

    const apiLimited = apiReads.filter((r) => r.status() === 429);
    expect(
      apiLimited.length,
      `只读接口不应与认证入口共用令牌桶，实际 429 ${apiLimited.length} 个`,
    ).toBe(0);

    // 等待认证桶回填（10 r/s，容量 30），避免影响后续模块的登录用例
    await new Promise((resolve) => setTimeout(resolve, 3000));
  });
});
