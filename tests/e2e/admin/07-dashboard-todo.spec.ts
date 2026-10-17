import { test, expect } from '@playwright/test';
import { adminLogin } from '../../helpers/e2e';

/**
 * 管理端 E2E 07：工作台待办直达与订单列表 query 预筛选
 * 覆盖（2026-10-17 工作台重构 3d2ad0d 配套）：
 *  - dashboard 待办中心渲染（6 个待办入口）
 *  - 点击待办项 → /orders?status=N 直达，列表页状态筛选同步选中
 *  - 直接带 query 进入列表页：合法值生效、非法值回落「全部订单」
 *  - 窄屏（默认 390 视口）下经 n-select 呈现同一筛选值
 * 背景：订单列表桌面端为 n-radio-group，窄屏为 n-select（isNarrow 分支），
 *       两形态共用同一 statusFilter，故同时覆盖两种断言
 */

test.describe('工作台待办中心渲染', () => {
  test.beforeEach(async ({ page }) => {
    await adminLogin(page);
  });

  test('dashboard 展示待办中心与 6 个待办入口', async ({ page }) => {
    await page.waitForURL(/#\/dashboard/, { timeout: 15_000 });
    await expect(page.getByText('待办中心')).toBeVisible();
    for (const label of ['待支付订单', '待发货订单', '待收货订单', '售后待审核', '退款中', '库存预警']) {
      await expect(page.locator('.todo-item').filter({ hasText: label }), `待办入口「${label}」应可见`).toBeVisible();
    }
  });
});

test.describe('订单列表 query 预筛选（桌面视口 radio 组）', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test.beforeEach(async ({ page }) => {
    await adminLogin(page);
  });

  test('点击待办直达订单列表并携带 status 筛选', async ({ page }) => {
    await page.waitForURL(/#\/dashboard/, { timeout: 15_000 });
    await page.locator('.todo-item').filter({ hasText: '待支付订单' }).click();
    await page.waitForURL(/#\/orders\?status=1/, { timeout: 15_000 });
    // 筛选同步选中：单选按钮 + 概览卡片高亮（两处均由 statusFilter 驱动）
    await expect(page.getByRole('radio', { name: '待支付' })).toBeChecked();
    await expect(page.locator('.stat-card.active').filter({ hasText: '待支付' })).toBeVisible();
  });

  test('合法 query 值直达生效（待收货→配送中）', async ({ page }) => {
    await page.goto('/b/#/orders?status=4');
    await expect(page.getByRole('radio', { name: '配送中' })).toBeChecked();
    await expect(page).toHaveURL(/status=4/);
  });

  test('非法 query 值回落全部订单', async ({ page }) => {
    await page.goto('/b/#/orders?status=999');
    await expect(page.getByRole('radio', { name: '全部订单' })).toBeChecked();
  });
});

test.describe('订单列表 query 预筛选（窄屏 select）', () => {
  // 默认 viewport 390×844 即窄屏分支：n-select 呈现当前筛选值
  test.beforeEach(async ({ page }) => {
    await adminLogin(page);
  });

  test('窄屏下 query 预筛选经下拉呈现', async ({ page }) => {
    await page.goto('/b/#/orders?status=1');
    // n-select 选中项文本展示在 .n-base-selection-label
    await expect(page.locator('.status-select .n-base-selection-label')).toHaveText('待支付');
  });
});
