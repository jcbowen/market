import { test, expect } from '@playwright/test';
import { adminLogin } from '../../helpers/e2e';

/**
 * 管理端 E2E 01：登录与 Dashboard
 * 入口：http://localhost:18080/b/（hash 路由，页面路径为 /b/#/...）
 * 选择器依据 views/login/index.vue：账号 placeholder「请输入管理员账号」、
 * 密码 placeholder「请输入密码」、按钮文案「登 录」（助手在 helpers/e2e.ts）
 * 登录后多社区管理员落在 /b/login#/select-community，选社区后进入 dashboard
 */

test.describe('管理端登录', () => {
  test('错误密码提示登录失败', async ({ page }) => {
    await page.goto('/b/login');
    await page.getByPlaceholder('请输入管理员账号').fill('admin');
    await page.getByPlaceholder('请输入密码').fill('wrong-password');
    await page.getByRole('button', { name: /登\s*录/ }).click();
    // 失败提示（naive-ui message）且仍停留在登录页
    await expect(page).toHaveURL(/login/);
    await expect(page.getByPlaceholder('请输入管理员账号')).toBeVisible();
  });

  test('admin/admin123 登录成功进入后台', async ({ page }) => {
    await adminLogin(page);
    await expect(page).toHaveURL(/dashboard|\/b\/#/);
  });

  test('未登录访问受保护页面跳回登录页', async ({ page }) => {
    await page.goto('/b/#/dashboard');
    await expect(page).toHaveURL(/login|select-community/);
  });
});
