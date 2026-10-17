import { test, expect } from '@playwright/test';
import { posLogin } from '../../helpers/e2e';

/**
 * 管理端 E2E 05：POS 收银台独立应用（/pos 前缀，独立登录页与布局壳）
 * 覆盖：未登录访问收银台跳回登录页、错误密码拒绝、
 *       登录成功进入收银台（无班次显示开工引导 / 有班次显示收银工作台）、
 *       开工引导可用（开始营业 → 进入收银工作台）、交班记录页渲染
 * 凭据与主系统一致（员工账号复用管理员账号体系）
 * 入口说明：POS 是 admin SPA（静态挂载 /b/）内的 hash 路由子应用，URL 必须带
 *   /b/#/pos/... 前缀；裸 /pos/... 没有静态挂载与 NoRoute fallback，
 *   会拿到 gateway 的 10003 JSON 而永不加载 SPA（2026-10-17 修复，历史写法错误）
 * 登录说明：测试账号为多社区超管，全新浏览器上下文（无 localStorage 社区记录）下
 *   POS 守卫按设计先强制进入 /select-community，点选社区整页 reload 后才进收银台，
 *   统一走 posLogin 助手（2026-10-17 修复，历史用例漏了社区选择步骤）
 */

test.describe.serial('POS 收银台', () => {
  test('未登录访问收银台跳回登录页', async ({ page }) => {
    await page.goto('/b/#/pos/cashier');
    await expect(page).toHaveURL(/\/pos\/login/, { timeout: 15_000 });
    await expect(page.getByPlaceholder('员工账号')).toBeVisible();
  });

  test('错误密码登录被拒绝', async ({ page }) => {
    await page.goto('/b/#/pos/login');
    await page.getByPlaceholder('员工账号').fill(process.env.TEST_ADMIN_USER ?? 'admin');
    await page.getByPlaceholder('密码').fill('wrong-password');
    await page.getByRole('button', { name: /进入收银台/ }).click();
    // 登录失败应弹出错误提示且停留在登录页
    await expect(page.locator('.n-message, .n-message-error').first()).toBeVisible({ timeout: 10_000 });
    await expect(page).toHaveURL(/\/pos\/login/);
  });

  test('登录成功进入收银台并显示开工引导或工作台', async ({ page }) => {
    await posLogin(page);
    // 无进行中班次 → 开工引导；有班次 → 收银工作台 tabs
    await expect(page.locator('.start-shift-guide, .cashier-tabs').first()).toBeVisible({
      timeout: 15_000,
    });
  });

  test('开工引导可用：开始营业进入收银工作台', async ({ page }) => {
    await posLogin(page);

    const guide = page.locator('.start-shift-guide');
    const tabs = page.locator('.cashier-tabs');
    // 开工引导只在终态出现（班次接口已返回且无进行中班次），班次加载瞬间的
    // tabs 闪现不会误判为「无引导」；已有进行中班次时引导永不出现
    let hasGuide = true;
    try {
      await expect(guide).toBeVisible({ timeout: 8_000 });
    } catch {
      hasGuide = false;
    }
    // 已有进行中班次（如上次运行开工后未交班）：无引导可点，验证工作台即可
    if (!hasGuide) {
      await expect(tabs).toBeVisible();
      return;
    }
    await guide.getByRole('button', { name: /开始营业/ }).click();
    // 开工成功后进入商品下单工作台
    await expect(tabs).toBeVisible({ timeout: 15_000 });
    await expect(guide).toHaveCount(0);
  });

  test('交班记录页正常渲染', async ({ page }) => {
    await posLogin(page);
    await page.goto('/b/#/pos/shifts');
    await expect(page.locator('.shifts-page')).toBeVisible({ timeout: 15_000 });
    // 班次列表接口返回后：无记录出空态，有记录出班次卡片
    await expect(page.locator('.shifts-page .n-empty, .shifts-page .shift-item').first()).toBeVisible({
      timeout: 15_000,
    });
  });
});
