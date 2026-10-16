import { test, expect } from '@playwright/test';
import { adminLogin } from '../../helpers/e2e';

/**
 * 管理端 E2E 02：商品与库存页面
 * hash 路由：/b/#/products/categories 等；登录+选社区后可达
 */

test.describe.serial('管理端商品管理', () => {
  test.beforeEach(async ({ page }) => {
    await adminLogin(page);
  });

  test('商品分类页正常渲染', async ({ page }) => {
    await page.goto('/b/#/products/categories');
    await expect(page.locator('.n-data-table, .n-card, table').first()).toBeVisible({ timeout: 15_000 });
  });

  test('商品列表页正常渲染', async ({ page }) => {
    await page.goto('/b/#/products');
    await expect(page.locator('.n-data-table, .n-card, table').first()).toBeVisible({ timeout: 15_000 });
  });

  test('新建商品页可达', async ({ page }) => {
    await page.goto('/b/#/products/create');
    await expect(page.locator('form, .n-form, .n-card').first()).toBeVisible({ timeout: 15_000 });
  });

  test('库存页正常渲染', async ({ page }) => {
    await page.goto('/b/#/stock');
    await expect(page.locator('.n-data-table, .n-card, table').first()).toBeVisible({ timeout: 15_000 });
  });
});
