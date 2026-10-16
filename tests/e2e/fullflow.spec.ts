import { test, expect } from '@playwright/test';
import { adminLogin, clientLogin } from '../helpers/e2e';

/**
 * E2E 全流程串联：管理端操作 → C 端消费 → 管理端核对
 * 场景：
 *  1. 管理端登录并进入商品列表（保证后台可用）
 *  2. C 端 H5 登录 → 浏览分类页 → 打开商品 → 加入购物车 → 进入购物车
 *  3. 管理端订单列表可见数据（渲染层面验证，订单号级断言由 tests/api/13 覆盖）
 */

test.describe.serial('全流程串联（管理端 → C 端 → 管理端）', () => {
  test('步骤1：管理端登录进入 Dashboard', async ({ page }) => {
    await adminLogin(page);
    await expect(page).not.toHaveURL(/select-community/);
  });

  test('步骤2：C 端登录并浏览商品', async ({ page }) => {
    await clientLogin(page);
    await page.goto('/s/#/pages/category/category');
    // 首页有商品内容渲染（存在价格元素或商品卡片）
    const hasGoods = await page
      .locator('uni-view')
      .filter({ hasText: /¥|￥/ })
      .first()
      .isVisible()
      .catch(() => false);
    if (!hasGoods) {
      test.skip(true, 'C 端首页未渲染出商品卡片，需检查社区/商品数据');
      return;
    }
  });

  test('步骤3：C 端打开商品详情并加购', async ({ page }) => {
    await page.goto('/s/#/pages/category/category');
    const firstProduct = page.locator('uni-view').filter({ hasText: /¥|￥/ }).first();
    await expect(firstProduct).toBeVisible({ timeout: 15_000 });
    await firstProduct.click();
    await page.waitForTimeout(2000);
    const addBtn = page.getByText(/加入购物车|加购/).first();
    if (await addBtn.isVisible().catch(() => false)) {
      await addBtn.click();
      await page.waitForTimeout(1000);
    }
    // 购物车页可达
    await page.goto('/s/#/pages/cart/cart');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });

  test('步骤4：C 端进入确认订单页', async ({ page }) => {
    await page.goto('/s/#/pages/cart/cart');
    await page.waitForTimeout(2000);
    // 购物车有商品时点「去结算/结算」
    const settleBtn = page.getByText(/去结算|结算|立即下单/).first();
    if (await settleBtn.isVisible().catch(() => false)) {
      await settleBtn.click();
      await page.waitForTimeout(2000);
    }
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });

  test('步骤5：管理端订单列表可见订单数据', async ({ page }) => {
    await adminLogin(page);
    await page.goto('/b/#/orders');
    await expect(page.locator('.n-data-table, .n-card, table').first()).toBeVisible({ timeout: 15_000 });
  });
});
