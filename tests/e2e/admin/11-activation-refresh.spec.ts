import { test, expect, type Page } from '@playwright/test';
import { adminLogin } from '../../helpers/e2e';

/**
 * 管理端 E2E 11：keep-alive 页「切回即刷新」与统计页并发上限（2026-10-28 全栈审查 P1-12 配套）
 * 覆盖：
 *   - 全平台统计页首次进入按社区分片拉取排行，浏览器侧同一时刻在途请求不超过并发上限 8
 *   - 统计页切走再切回（标签页缓存路径，不重新挂载）必须重新发请求——旧口径只在 onMounted 拉数，
 *     切回时展示的是离开前的旧数据
 *   - 支付配置页同样在激活时重拉（该页是整份覆盖保存的代表页，拿旧值保存会覆盖他人改动）
 * 口径说明：
 *   - 「真刷新」的判据是**切回后发出了新的请求**，而不是页面 URL 变了；
 *     同页 hash 直达（`#/system/statistics` → `#/dashboard` → 点标签回来）不触发文档重载，
 *     组件实例由 keep-alive 复用，正是审查关心的那条路径
 *   - 在途请求数由 Playwright 侧的 request/requestfinished 事件配对统计，上限取页面代码里的
 *     分片常量 8 并容 1 个完成事件滞后（判据拦的是「不分片一次并发 200」，不是这 1 个误差）
 */

/** 统计页逐社区拉数用的接口（平台聚合那次带 scope=all，不带 community_id） */
const SALES_URL = '/api/v1/admin/statistics/sales';
/** 与 statistics.vue 的 COMMUNITY_FETCH_CONCURRENCY 保持一致 */
const COMMUNITY_FETCH_CONCURRENCY = 8;

interface RequestTrack {
  /** 匹配到的请求 URL（按发出顺序） */
  urls: string[];
  /** 当前在途数 */
  inFlight(): number;
  /** 观测到的在途峰值 */
  peak(): number;
  /** 自此刻起新发出的匹配请求 URL */
  since(mark: number): string[];
}

/**
 * 挂接请求监听，统计匹配到的接口发出情况
 * - 函数名: trackRequests
 * - 参数: page — 页面对象；match — URL 判定函数
 * - 返回: RequestTrack（含已发出列表、在途数与峰值）
 * - 异常: 无（纯监听；页面关闭后事件自然停）
 * - 使用示例: const t = trackRequests(page, (u) => u.includes(SALES_URL))
 */
function trackRequests(page: Page, match: (url: string) => boolean): RequestTrack {
  const urls: string[] = [];
  let inFlight = 0;
  let peakInFlight = 0;
  page.on('request', (req) => {
    if (!match(req.url())) return;
    urls.push(req.url());
    inFlight += 1;
    peakInFlight = Math.max(peakInFlight, inFlight);
  });
  const release = (req: { url(): string }) => {
    if (match(req.url())) inFlight -= 1;
  };
  page.on('requestfinished', release);
  page.on('requestfailed', release);
  return {
    urls,
    inFlight: () => inFlight,
    peak: () => peakInFlight,
    since: (mark: number) => urls.slice(mark),
  };
}

/**
 * 等待匹配请求排空（分片拉数完成的确定性判据，避免用固定 sleep 猜时间）
 * - 函数名: waitForIdle
 * - 参数: page — 页面对象；track — trackRequests 返回值；timeoutMs — 上限
 * - 返回: Promise<void>（超时仍未排空由 expect 抛出）
 * - 异常: 30 秒内未排空即断言失败
 */
async function waitForIdle(page: Page, track: RequestTrack, timeoutMs = 30_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    await page.waitForTimeout(200);
    if (track.inFlight() === 0 && track.urls.length > 0) return;
  }
  expect(
    track.inFlight(),
    `接口请求未在 ${timeoutMs}ms 内排空（已发出 ${track.urls.length} 个、在途 ${track.inFlight()} 个）`,
  ).toBe(0);
}

test.describe.serial('统计与设置页激活刷新', () => {
  test('全平台统计页逐社区拉数按并发上限分片', async ({ page }) => {
    await adminLogin(page);
    // 只统计逐社区那一批：平台聚合请求（scope=all）与它同一段代码发出，会干扰峰值判定
    const track = trackRequests(page, (u) => u.includes(SALES_URL) && u.includes('community_id='));
    await page.goto('/b/#/system/statistics');
    await expect(page.locator('.n-card').first()).toBeVisible({ timeout: 20_000 });
    await waitForIdle(page, track);

    // 测试库社区数远超一批（接口用例每轮都会新建社区），断言分片确实生效而不是恰好只有一批
    expect(
      track.urls.length,
      `逐社区拉数应超过一个并发批次（>${COMMUNITY_FETCH_CONCURRENCY}），实际 ${track.urls.length} 个`,
    ).toBeGreaterThan(COMMUNITY_FETCH_CONCURRENCY);
    // 在途峰值容许 1 个的测量误差：Promise.allSettled 在 JS 侧结算后，上一个请求的
    // requestfinished 事件才投递到 Playwright，此时下一批已发出，瞬时重叠可到 9；
    // 判据要拦的是「不分片、一次并发 200 个」，不是这 1 个事件滞后
    expect(
      track.peak(),
      `浏览器侧在途请求应贴合并发上限 ${COMMUNITY_FETCH_CONCURRENCY}（容 1 个完成事件滞后）`,
    ).toBeLessThanOrEqual(COMMUNITY_FETCH_CONCURRENCY + 1);
  });

  test('统计页切回时重新拉数（keep-alive 真刷新，非 URL 空转）', async ({ page }) => {
    await adminLogin(page);
    const track = trackRequests(page, (u) => u.includes(SALES_URL) && u.includes('community_id='));
    await page.goto('/b/#/system/statistics');
    await expect(page.locator('.n-card').first()).toBeVisible({ timeout: 20_000 });
    await waitForIdle(page, track);
    const firstBatch = track.urls.length;
    expect(firstBatch, '首次进入应已发出逐社区拉数').toBeGreaterThan(0);
    // 在窗口上留标记：切回后若标记还在，说明从未重载文档、组件实例由 keep-alive 复用，
    // 下面的「重新发请求」才只可能来自激活刷新（onActivated），而不是重新挂载的 onMounted
    await page.evaluate(() => {
      (window as unknown as { __k4NoReload: number }).__k4NoReload = 1;
    });

    // 切到工作台：统计页进入 keep-alive 缓存（标签仍开着，组件实例不销毁）
    await page.goto('/b/#/dashboard');
    await expect(page.locator('.n-card').first()).toBeVisible({ timeout: 20_000 });
    const markBeforeBack = track.urls.length;

    // 点标签切回：同文档导航，不重载页面；旧口径只在 onMounted 拉数，这里不会有任何新请求
    const tab = page.locator('.n-tabs-tab').filter({ hasText: '全平台统计' }).first();
    await expect(tab).toBeVisible({ timeout: 10_000 });
    await tab.click();
    await page.waitForTimeout(1_500);

    expect(
      await page.evaluate(() => (window as unknown as { __k4NoReload?: number }).__k4NoReload),
      '切回标签不应重载文档，否则测的是重新挂载而不是激活刷新',
    ).toBe(1);
    const refetched = track.since(markBeforeBack);
    expect(
      refetched.length,
      '切回统计页必须重新发请求（激活刷新未生效时会展示离开前的旧数据）',
    ).toBeGreaterThan(0);
    expect(
      track.urls.length - firstBatch,
      '激活刷新应重跑整轮逐社区拉数，量级与首轮相当',
    ).toBeGreaterThan(0);
  });

  test('支付配置页切回时重新拉取设置', async ({ page }) => {
    await adminLogin(page);
    const track = trackRequests(page, (u) => u.includes('/api/v1/admin/settings/payment'));
    await page.goto('/b/#/system/payment');
    await expect(page.locator('.n-card').first()).toBeVisible({ timeout: 20_000 });
    await waitForIdle(page, track);
    const markBeforeLeave = track.urls.length;
    await page.evaluate(() => {
      (window as unknown as { __k4PaymentNoReload: number }).__k4PaymentNoReload = 1;
    });

    await page.goto('/b/#/dashboard');
    await expect(page.locator('.n-card').first()).toBeVisible({ timeout: 20_000 });

    const tab = page.locator('.n-tabs-tab').filter({ hasText: '支付配置' }).first();
    await expect(tab).toBeVisible({ timeout: 10_000 });
    await tab.click();
    await page.waitForTimeout(1_500);

    expect(
      await page.evaluate(() => (window as unknown as { __k4PaymentNoReload?: number }).__k4PaymentNoReload),
      '切回支付配置标签不应重载文档，否则测的是重新挂载而不是激活刷新',
    ).toBe(1);

    expect(
      track.since(markBeforeLeave).length,
      '支付配置页是整份覆盖保存的代表页：切回不重拉会拿旧值覆盖他人改动，必须在激活时重新拉取',
    ).toBeGreaterThan(0);
  });
});
