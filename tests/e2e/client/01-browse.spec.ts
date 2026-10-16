import { test, expect } from '@playwright/test';
import { clientLogin } from '../../helpers/e2e';

/**
 * C 端 H5 E2E 01：登录与浏览
 * 入口：http://localhost:18080/s/（hash 路由，重定向到 /s/app.html#/...）
 * 选择器依据探针：手机号输入为主文档唯一 input，登录按钮为文本「登录」最后一个；
 * uni-input placeholder 为遮罩 div 非原生属性，不能 getByPlaceholder
 */

test.describe('C 端登录与浏览', () => {
  test('首页（分类）正常渲染商品与价格', async ({ page }) => {
    await page.goto('/s/#/pages/category/category');
    await expect(page.locator('uni-view').filter({ hasText: /¥|￥/ }).first()).toBeVisible({
      timeout: 15_000,
    });
  });

  test('手机号直登成功', async ({ page }) => {
    await clientLogin(page, '13800138000');
    // 登录成功后离开登录页（跳回首页或上一页）
    await expect(page).not.toHaveURL(/pages\/login/);
  });

  test('秒杀页可达', async ({ page }) => {
    await page.goto('/s/#/pages/flash/flash');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });

  test('满减页可达', async ({ page }) => {
    await page.goto('/s/#/pages/full-reduction/full-reduction');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });

  test('领券中心页可达', async ({ page }) => {
    await page.goto('/s/#/pages/coupon/coupon');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });
});
