import { test, expect } from '@playwright/test';
import { adminLogin } from '../../helpers/e2e';

/**
 * 管理端 E2E 06：库存 / 门店设置 / 附件配置 / 行政区划页面
 * 覆盖：进销存重构后的库存线页面（工作台含 列表/预警/流水/批次 四个 tab 深链、
 *       采购/入库/出库/盘点/报表五个独立页）与
 *       门店设置、附件配置、行政区划渲染冒烟；
 *       行政区划关键词搜索做一次真实交互（填入关键词 → 表格过滤）
 * 说明：旧路径 stock/records、stock/warnings 已随重构并入工作台 tab（?tab= 深链），
 *       采购建议不再是独立页（采购管理页内的 tab）。
 */

test.describe.serial('库存管理页面', () => {
  test.beforeEach(async ({ page }) => {
    await adminLogin(page);
  });

  const pages: [string, string][] = [
    ['/b/#/stock', '库存工作台'],
    ['/b/#/stock?tab=records', '库存流水'],
    ['/b/#/stock?tab=warnings', '库存预警'],
    ['/b/#/stock?tab=batches', '批次库存'],
    ['/b/#/stock/purchase', '采购管理'],
    ['/b/#/stock/in', '入库管理'],
    ['/b/#/stock/out', '出库管理'],
    ['/b/#/stock/check', '盘点管理'],
    ['/b/#/stock/reports', '报表中心'],
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
    // 关键词固定用「重庆」：它是内置演示数据包自带的区划（重庆市 + 37 区县），
    // 新库首启即可命中；曾用「北京」依赖后台「行政区划 → 检查更新」同步过全国数据，
    // 未同步的库会让本用例假红，属环境前置而非页面缺陷
    const input = page.getByPlaceholder('搜索区划代码或名称');
    await input.fill('重庆');
    // 树形表格折叠模式下搜索命中应展示重庆相关行
    await expect(page.locator('text=重庆').first()).toBeVisible({ timeout: 10_000 });
  });
});
