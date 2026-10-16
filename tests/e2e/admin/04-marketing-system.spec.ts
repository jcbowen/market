import { test, expect } from '@playwright/test';
import { adminLogin } from '../../helpers/e2e';

/**
 * 管理端 E2E 04：营销与系统管理页面（hash 路由）
 */

test.describe.serial('管理端营销', () => {
  test.beforeEach(async ({ page }) => {
    await adminLogin(page);
  });

  const pages: [string, string][] = [
    ['/b/#/marketing/coupons', '优惠券'],
    ['/b/#/marketing/flash-sales', '秒杀'],
    ['/b/#/marketing/full-reduction', '满减'],
    ['/b/#/marketing/free-shipping', '包邮'],
    ['/b/#/marketing/banners', '轮播'],
  ];

  for (const [path, label] of pages) {
    test(`${label}页面正常渲染`, async ({ page }) => {
      await page.goto(path);
      await expect(page.locator('.n-data-table, .n-card, table, .n-tabs').first()).toBeVisible({
        timeout: 15_000,
      });
    });
  }
});

test.describe.serial('管理端系统管理', () => {
  test.beforeEach(async ({ page }) => {
    await adminLogin(page);
  });

  const pages: [string][] = [
    ['/b/#/system/communities'],
    ['/b/#/system/roles'],
    ['/b/#/system/admins'],
    ['/b/#/system/payment'],
    ['/b/#/system/refund'],
  ];

  for (const [path] of pages) {
    test(`${path} 正常渲染`, async ({ page }) => {
      await page.goto(path);
      await expect(page.locator('.n-data-table, .n-card, table, form, .n-form').first()).toBeVisible({
        timeout: 15_000,
      });
    });
  }
});
