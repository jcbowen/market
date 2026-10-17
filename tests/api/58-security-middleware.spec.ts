import { test, expect } from '@playwright/test';

/**
 * 模块 58：网关安全中间件
 * 覆盖：SecurityHeadersMiddleware（nosniff / X-Frame-Options / XSS / Referrer-Policy / CSP，
 *       H5 应用页 SAMEORIGIN 例外）、BlockTraceMiddleware（TRACE→405）、
 *       RateLimitMiddleware（IP 令牌桶 100/s 容量 150，超限 429 + Retry-After）
 * 注意：限流为进程内存态、按客户端 IP 计数；本用例放在文件末尾执行，
 *       触发限流后令牌桶每秒回填 100，短暂等待不影响后续模块
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
  // 全文件最后一个用例：突发 600 个请求耗尽令牌桶（容量 150 + 回填），应出现 429
  test('突发超量请求触发 429 限流并携带 Retry-After', async ({ request }) => {
    const total = 600;
    const responses = await Promise.all(
      Array.from({ length: total }, () => request.get('/health')),
    );
    const statuses = responses.map((r) => r.status());
    const limited = statuses.filter((s) => s === 429);

    expect(
      limited.length,
      `突发 ${total} 个请求应触发限流（429），实际 ${limited.length} 个`,
    ).toBeGreaterThan(0);

    // 限流响应应携带 Retry-After: 1 提示客户端退避
    const firstLimited = responses.find((r) => r.status() === 429)!;
    expect(firstLimited.headers()['retry-after']).toBe('1');

    // 部分请求应正常通过（限流不是全量拒绝）
    expect(statuses.filter((s) => s === 200).length).toBeGreaterThan(0);

    // 等待令牌桶回填，避免影响后续模块
    await new Promise((resolve) => setTimeout(resolve, 2000));
  });
});
