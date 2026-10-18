import { test, expect } from '@playwright/test';

/**
 * 进销存 SKU 选择器 UI 回归（保留）：采购单表单交互
 * 覆盖：单框远程搜索出 SKU 级选项（含库存）、选中回填、批量选货弹窗
 */

test.use({ viewport: { width: 1440, height: 900 } });

test('采购表单 SKU 选择器交互', async ({ page }) => {
  await page.goto('/b/#/login');
  await page.getByPlaceholder(/用户名|账号/).fill('admin');
  await page.getByPlaceholder(/密码/).fill('admin123');
  await page.getByRole('button', { name: /登\s*录|登录/ }).click();
  await expect(page).not.toHaveURL(/login/, { timeout: 15000 });

  await page.goto('/b/#/stock/purchase/create');
  const communityCard = page.getByText('盘龙金茂悦', { exact: true });
  if (await communityCard.isVisible({ timeout: 3000 }).catch(() => false)) {
    await communityCard.click();
  }
  await expect(page.getByText('采购明细')).toBeVisible({ timeout: 15000 });

  // 单框远程搜索（SKU 选择器在明细表格内，第一个 .n-base-selection 是供应商下拉）
  const picker = page.locator('tbody .n-base-selection').first();
  await picker.click();
  await page.keyboard.type('苹果');
  await expect(page.locator('.n-base-select-option').first()).toBeVisible({ timeout: 10000 });
  const optionText = await page.locator('.n-base-select-option').first().textContent();
  expect(optionText ?? '').toContain('库存');

  await page.locator('.n-base-select-option').first().click();
  await expect(page.getByText(/当前库存 \d+/).first()).toBeVisible();

  // 批量选货弹窗
  await page.getByRole('button', { name: '批量选货' }).click();
  await expect(page.getByText('批量选货').first()).toBeVisible();
  await expect(page.locator('.n-data-table').first()).toBeVisible({ timeout: 10000 });
});
