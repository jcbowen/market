import { test, expect } from '@playwright/test';

/**
 * 管理端 E2E 05：POS 收银台独立应用（/pos 前缀，独立登录页与布局壳）
 * 覆盖：未登录访问收银台跳回登录页、错误密码拒绝、
 *       登录成功进入收银台（无班次显示开工引导 / 有班次显示收银工作台）、
 *       开工引导可用（开始营业 → 进入收银工作台）、交班记录页渲染
 * 凭据与主系统一致（员工账号复用管理员账号体系）
 */

const POS_USER = process.env.TEST_ADMIN_USER ?? 'admin';
const POS_PASS = process.env.TEST_ADMIN_PASS ?? 'admin123';

test.describe.serial('POS 收银台', () => {
  test('未登录访问收银台跳回登录页', async ({ page }) => {
    await page.goto('/pos/cashier');
    await expect(page).toHaveURL(/\/pos\/login/, { timeout: 15_000 });
    await expect(page.getByPlaceholder('员工账号')).toBeVisible();
  });

  test('错误密码登录被拒绝', async ({ page }) => {
    await page.goto('/pos/login');
    await page.getByPlaceholder('员工账号').fill(POS_USER);
    await page.getByPlaceholder('密码').fill('wrong-password');
    await page.getByRole('button', { name: /进入收银台/ }).click();
    // 登录失败应弹出错误提示且停留在登录页
    await expect(page.locator('.n-message, .n-message-error').first()).toBeVisible({ timeout: 10_000 });
    await expect(page).toHaveURL(/\/pos\/login/);
  });

  test('登录成功进入收银台并显示开工引导或工作台', async ({ page }) => {
    await page.goto('/pos/login');
    await page.getByPlaceholder('员工账号').fill(POS_USER);
    await page.getByPlaceholder('密码').fill(POS_PASS);
    await page.getByRole('button', { name: /进入收银台/ }).click();
    await page.waitForURL(/\/pos\/cashier/, { timeout: 15_000 });

    // 无进行中班次 → 开工引导；有班次 → 收银工作台 tabs
    await expect(page.locator('.start-shift-guide, .cashier-tabs').first()).toBeVisible({
      timeout: 15_000,
    });
  });

  test('开工引导可用：开始营业进入收银工作台', async ({ page }) => {
    await page.goto('/pos/cashier');
    const guide = page.locator('.start-shift-guide');
    // 已有进行中班次时无引导，直接通过
    if (!(await guide.isVisible())) {
      await expect(page.locator('.cashier-tabs')).toBeVisible();
      return;
    }
    await guide.getByRole('button', { name: /开始营业/ }).click();
    // 开工成功后进入商品下单工作台
    await expect(page.locator('.cashier-tabs')).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('.start-shift-guide')).toHaveCount(0);
  });

  test('交班记录页正常渲染', async ({ page }) => {
    await page.goto('/pos/shifts');
    await expect(page.locator('.n-data-table, .n-card, .n-empty, .n-tabs').first()).toBeVisible({
      timeout: 15_000,
    });
  });
});
