import { test, expect } from '@playwright/test';
import { adminLogin } from '../../helpers/e2e';

/**
 * 管理端 E2E 06：库存 / 门店设置 / 附件配置 / 行政区划页面
 * 覆盖：库存六页（列表/流水/盘点/预警/出库/采购建议）与
 *       门店设置、附件配置、行政区划渲染冒烟；
 *       行政区划关键词搜索做一次真实交互（填入关键词 → 表格过滤）
 */

test.describe.serial('库存管理页面', () => {
  test.beforeEach(async ({ page }) => {
    await adminLogin(page);
  });

  const pages: [string, string][] = [
    ['/b/#/stock', '库存列表'],
    ['/b/#/stock/records', '库存流水'],
    ['/b/#/stock/check', '库存盘点'],
    ['/b/#/stock/warnings', '库存预警'],
    ['/b/#/stock/out', '库存出库'],
    ['/b/#/stock/purchase', '采购建议'],
  ];

  for (const [path, label] of pages) {
    test(`${label}正常渲染`, async ({ page }) => {
      await page.goto(path);
      await expect(page.locator('.n-data-table, .n-card, table, .n-tabs').first()).toBeVisible({
        timeout: 15_000,
      });
    });
  }
});

test.describe.serial('门店与系统配置页面', () => {
  test.beforeEach(async ({ page }) => {
    await adminLogin(page);
  });

  test('门店设置页渲染表单', async ({ page }) => {
    await page.goto('/b/#/store/settings');
    await expect(page.locator('.n-card, form, .n-form, .n-form-item').first()).toBeVisible({
      timeout: 15_000,
    });
  });

  test('附件配置页渲染表单', async ({ page }) => {
    await page.goto('/b/#/system/attachment');
    await expect(page.locator('.n-card, form, .n-form, .n-form-item, .n-radio-group').first()).toBeVisible({
      timeout: 15_000,
    });
  });

  test('行政区划页渲染并支持关键词搜索', async ({ page }) => {
    await page.goto('/b/#/system/regions');
    await expect(page.locator('.n-data-table, .n-card, table').first()).toBeVisible({
      timeout: 15_000,
    });

    // 真实交互：按区划名称搜索（接口为 GET /admin/regions?keyword=）
    const input = page.getByPlaceholder('搜索区划代码或名称');
    await input.fill('北京');
    // 树形表格折叠模式下搜索命中应展示北京相关行
    await expect(page.locator('text=北京').first()).toBeVisible({ timeout: 10_000 });
  });
});
