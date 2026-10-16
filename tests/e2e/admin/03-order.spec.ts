import { test, expect } from '@playwright/test';
import { adminLogin } from '../../helpers/e2e';

/**
 * 管理端 E2E 03：订单、用户、统计页面（hash 路由）
 */

test.describe.serial('管理端订单与统计', () => {
  test.beforeEach(async ({ page }) => {
    await adminLogin(page);
  });

  test('订单列表页正常渲染', async ({ page }) => {
    await page.goto('/b/#/orders');
    await expect(page.locator('.n-data-table, .n-card, table').first()).toBeVisible({ timeout: 15_000 });
  });

  test('退款列表页正常渲染', async ({ page }) => {
    await page.goto('/b/#/refunds');
    await expect(page.locator('.n-data-table, .n-card, table').first()).toBeVisible({ timeout: 15_000 });
  });

  test('用户列表页正常渲染', async ({ page }) => {
    await page.goto('/b/#/users');
    await expect(page.locator('.n-data-table, .n-card, table').first()).toBeVisible({ timeout: 15_000 });
  });

  test('统计页正常渲染', async ({ page }) => {
    await page.goto('/b/#/statistics');
    await expect(page.locator('.n-card, canvas, .n-grid').first()).toBeVisible({ timeout: 15_000 });
  });

  test('Dashboard 正常渲染', async ({ page }) => {
    await page.goto('/b/#/dashboard');
    await expect(page.locator('.n-card, .n-grid, canvas').first()).toBeVisible({ timeout: 15_000 });
  });
});
