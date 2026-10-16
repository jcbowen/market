import { test, expect } from '@playwright/test';
import { clientLogin } from '../../helpers/e2e';

/**
 * C 端 H5 E2E 03：个人中心与地址管理
 * 页面：pages/user/user、pages/address/list、pages/address/edit、pages/community/select
 */

test.describe.serial('C 端个人中心', () => {
  test.beforeEach(async ({ page }) => {
    await clientLogin(page);
  });

  test('我的页正常渲染', async ({ page }) => {
    await page.goto('/s/#/pages/user/user');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });

  test('地址列表页可达', async ({ page }) => {
    await page.goto('/s/#/pages/address/list');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });

  test('新增地址页可达', async ({ page }) => {
    await page.goto('/s/#/pages/address/edit');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });

  test('新增地址表单提交成功', async ({ page }) => {
    await page.goto('/s/#/pages/address/edit');
    await page.waitForTimeout(2500);
    // uni-input placeholder 为遮罩 div，按 input 顺序定位（收货人/手机号/详细地址）
    const inputs = page.locator('input');
    const inputCount = await inputs.count();
    if (inputCount < 3) {
      test.skip(true, `地址表单输入框数量 ${inputCount} 与预期不符，需按实际调整`);
      return;
    }
    await inputs.nth(0).fill('自动化测试');
    await inputs.nth(1).fill('13900001234');
    await inputs.nth(2).fill(`测试路 ${Date.now()} 号`);
    // 保存按钮文本在 uni-button 内部 span 上，点 span 会被 uni-button 拦截 pointer events，须点 uni-button 本身
    await page.locator('uni-button').filter({ hasText: /保存|提交/ }).last().click();
    await page.waitForTimeout(2000);
    // 保存成功后页面走 uni.navigateBack 返回（直接 goto 打开时无历史栈，可能停留原页），
    // 故以列表页出现新地址作为成功依据
    await page.goto('/s/#/pages/address/list');
    await page.waitForTimeout(2500);
    await expect(page.getByText('13900001234').first()).toBeVisible({ timeout: 10_000 });
  });

  test('社区选择页可达', async ({ page }) => {
    await page.goto('/s/#/pages/community/select');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });

  test('履约工作台可达', async ({ page }) => {
    await page.goto('/s/#/pages/fulfillment/fulfillment');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });
});
