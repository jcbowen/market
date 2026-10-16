import { defineConfig } from '@playwright/test';

/**
 * Playwright 全局配置
 * - baseURL 指向本地 gateway（18080），可通过 TEST_BASE_URL 环境变量覆盖
 * - globalSetup 启动时探测 /health，服务未启动则直接终止并给出提示
 * - workers=1 顺序执行，规避测试间数据竞争；数据同时做了动态化兜底
 */
export default defineConfig({
  testDir: '.',
  testMatch: '**/*.spec.ts',
  globalSetup: './helpers/global-setup.ts',
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  retries: 0,
  reporter: [
    ['list'],
    ['html', { outputFolder: 'test-results/html', open: 'never' }],
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
