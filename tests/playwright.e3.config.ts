import { defineConfig } from '@playwright/test';
import base from './playwright.config';

/**
 * E3 本地验证专用配置：仅调整产物目录与 reporter，
 * 避免复用 tests/test-results/artifacts 时启动期清理大量旧产物触发
 * 沙箱批量删除保护导致整个测试运行中止。其余口径与主配置一致。
 */
export default defineConfig({
  ...base,
  // 每次运行独立产物目录（沙箱批量删除保护下，启动期清理 >50 个旧产物会中止运行）
  outputDir: process.env.E3_OUTPUT_DIR ?? `test-results/artifacts-e3-${Date.now()}`,
  reporter: [
    ['list'],
    ['json', { outputFile: 'test-results/results-e3.json' }],
  ],
});
