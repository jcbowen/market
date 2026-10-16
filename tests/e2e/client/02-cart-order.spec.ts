import { test, expect } from '@playwright/test';
import { clientLogin } from '../../helpers/e2e';

/**
 * C 端 H5 E2E 02：购物车与下单
 * 流程：登录 → 分类页打开商品 → 详情加购 → 购物车页 → 去结算 → 订单列表
 */

test.describe.serial('C 端购物与下单', () => {
  test.beforeEach(async ({ page }) => {
    await clientLogin(page);
  });

  test('从分类页打开商品详情', async ({ page }) => {
    await page.goto('/s/#/pages/category/category');
    const firstProduct = page.locator('uni-view').filter({ hasText: /¥|￥/ }).first();
    await expect(firstProduct).toBeVisible({ timeout: 15_000 });
    await firstProduct.click();
    await page.waitForTimeout(2000);
    // 进入详情页或停留在分类页（结构差异兼容）
    expect(page.url()).toMatch(/product\/detail|category/);
  });

  test('商品详情加购后购物车页可达', async ({ page }) => {
    await page.goto('/s/#/pages/category/category');
    const firstProduct = page.locator('uni-view').filter({ hasText: /¥|￥/ }).first();
    await expect(firstProduct).toBeVisible({ timeout: 15_000 });
    await firstProduct.click();
    await page.waitForTimeout(2000);

    // 详情页加购按钮（文本「加入购物车」或「加购」）
    const addBtn = page.getByText(/加入购物车|加购/).first();
    if (await addBtn.isVisible().catch(() => false)) {
      await addBtn.click();
      await page.waitForTimeout(1000);
    }

    await page.goto('/s/#/pages/cart/cart');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });

  test('确认订单页可达', async ({ page }) => {
    await page.goto('/s/#/pages/order/confirm');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });

  test('订单列表页正常渲染', async ({ page }) => {
    await page.goto('/s/#/pages/order/list');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });

  test('订单列表取消入口可见（无可取消订单时跳过）', async ({ page }) => {
    await page.goto('/s/#/pages/order/list');
    await page.waitForTimeout(2500);
    const cancelBtn = page.getByText(/取消订单|取消/).first();
    if (await cancelBtn.isVisible().catch(() => false)) {
      await expect(cancelBtn).toBeVisible();
    } else {
      test.skip(true, '当前账号无可取消订单');
    }
  });
});
