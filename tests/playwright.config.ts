import { defineConfig } from '@playwright/test';

/**
 * Playwright 全局配置
 * - baseURL 指向本地 gateway（18080），可通过 TEST_BASE_URL 环境变量覆盖
 * - globalSetup 启动时探测 /health，服务未启动则直接终止并给出提示
 * - workers=1 顺序执行，规避测试间数据竞争；数据同时做了动态化兜底
 * - retries 默认 1（原为 2）：本套件大量用例带造数与状态断言，多次重试会把「偶发但真实」
 *   的问题洗成绿色，且重试会重复下单/入库放大脏数据。仅本地网络抖动需要放宽时用
 *   TEST_RETRIES 覆盖（如 TEST_RETRIES=2），CI/验收一律用默认值
 * - json reporter 输出 test-results/results.json，供 scripts/check-test-integrity.mjs
 *   统计 skip 明细（造数类 skip 属于假绿，必须可见）
 */
export default defineConfig({
  testDir: '.',
  testMatch: '**/*.spec.ts',
  globalSetup: './helpers/global-setup.ts',
  globalTeardown: './helpers/global-teardown.ts',
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  retries: Number(process.env.TEST_RETRIES ?? 1),
  reporter: [
    ['list'],
    ['html', { outputFolder: 'test-results/html', open: 'never' }],
    ['json', { outputFile: 'test-results/results.json' }],
  ],
  use: {
    baseURL: process.env.TEST_BASE_URL ?? 'http://localhost:18080',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    locale: 'zh-CN',
    viewport: { width: 390, height: 844 }, // C 端 H5 为移动端布局，默认按手机视口
  },
  outputDir: 'test-results/artifacts',
});
