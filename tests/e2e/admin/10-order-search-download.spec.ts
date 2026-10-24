import { test, expect } from '@playwright/test';
import { adminLogin } from '../../helpers/e2e';

/**
 * 管理端 E2E 10：订单搜索增强与下载中心（前端交互）
 * 覆盖暂存需求「订单搜索与下载中心」的 UI 行为（接口口径由 api/75 号与服务层集成测试收口）：
 *   - 筛选区改版：账单号输入框（单框双号占位）、时间快捷 4 档互斥、来源下拉；
 *   - 更多筛选折叠/展开 + 条件计数；会员/商品选择器与选中回显；
 *   - 导出改异步：点击导出立即出「已创建」提示（不再阻塞逐页拉取）；
 *   - 下载中心：右上角入口、未下载角标、任务状态轮询至已完成、下载/删除（超管视角）。
 */

test.describe.serial('订单搜索与下载中心', () => {
  test.beforeEach(async ({ page }) => {
    await adminLogin(page);
    await page.goto('/b/#/orders');
    await expect(page.locator('.filter-card')).toBeVisible({ timeout: 15_000 });
  });

  test('筛选区改版渲染：账单号输入与时间快捷档位', async ({ page }) => {
    // 账单号输入框（单框双号：支付单号 / 渠道交易号）
    await expect(page.getByPlaceholder('支付单号 / 渠道交易号')).toBeVisible();
    // 时间快捷 4 档
    for (const label of ['今天', '昨天', '近7天', '近30天']) {
      await expect(page.locator('.quick-chips').getByText(label, { exact: true })).toBeVisible();
    }
    // 更多筛选折叠入口与导出按钮（异步语义提示）
    await expect(page.getByRole('button', { name: /更多筛选/ })).toBeVisible();
    await expect(page.getByRole('button', { name: '导出 CSV' })).toBeVisible();
  });

  test('时间快捷档位互斥：点选/切换/再点取消', async ({ page }) => {
    // n-button 的 class 在 button 元素上，getByText 会命中内层 .n-button__content，故用 role 定位
    const today = page.locator('.quick-chips').getByRole('button', { name: '今天' });
    const last7 = page.locator('.quick-chips').getByRole('button', { name: '近7天' });

    // 点「今天」→ 激活（primary），日期范围联动填充
    await today.click();
    await expect(today).toHaveClass(/n-button--primary-type/, { timeout: 5_000 });
    await expect(page.locator('.date-picker input').first()).not.toHaveValue('');

    // 切换「近7天」→「今天」取消激活
    await last7.click();
    await expect(last7).toHaveClass(/n-button--primary-type/);
    await expect(today).not.toHaveClass(/n-button--primary-type/);

    // 再点「近7天」→ 取消快捷并清空日期范围
    await last7.click();
    await expect(last7).not.toHaveClass(/n-button--primary-type/);
    await expect(page.locator('.date-picker input').first()).toHaveValue('', { timeout: 5_000 });
  });

  test('更多筛选折叠/展开与条件计数', async ({ page }) => {
    // 切换按钮双态文案：收起时「更多筛选（n）」、展开时「收起筛选」
    const moreBtn = page.locator('.filter-card').getByRole('button', { name: /更多筛选|收起筛选/ });

    // 初始收起：折叠区不可见
    await expect(page.locator('.more-area')).toBeHidden();

    // 展开后可见会员/商品选择入口与收货人/电话输入
    await moreBtn.click();
    await expect(page.locator('.more-area')).toBeVisible();
    await expect(page.getByRole('button', { name: '+ 选择会员' })).toBeVisible();
    await expect(page.getByRole('button', { name: '+ 选择商品' })).toBeVisible();

    // 填收货人 + 电话 → 收起 → 按钮出现条件计数（2）
    await page.getByPlaceholder('姓名模糊').fill('张');
    await page.getByPlaceholder('手机号').fill('138');
    await moreBtn.click();
    await expect(moreBtn).toHaveText(/更多筛选\s*（2）/);

    // 重置清空条件后计数消失
    await page.getByRole('button', { name: '重置' }).click();
    await expect(moreBtn).toHaveText(/^更多筛选\s*$/);
  });

  test('会员选择器可打开并取消', async ({ page }) => {
    await page.getByRole('button', { name: /更多筛选/ }).click();
    await page.getByRole('button', { name: '+ 选择会员' }).click();
    // 会员选择弹窗出现（含搜索框），关闭取消不改变筛选
    const modal = page.locator('.n-modal');
    await expect(modal.first()).toBeVisible({ timeout: 10_000 });
    await page.keyboard.press('Escape');
    await expect(page.getByRole('button', { name: '+ 选择会员' })).toBeVisible();
  });

  test('商品选择器选中后回显商品标签', async ({ page }) => {
    await page.getByRole('button', { name: /更多筛选/ }).click();
    await page.getByRole('button', { name: '+ 选择商品' }).click();
    // 商品搜索弹窗：按名称搜索后出现商品行，点第一行完成选择
    await page.getByPlaceholder('按商品名搜索').fill('a');
    const firstRow = page.locator('.product-row').first();
    await expect(firstRow).toBeVisible({ timeout: 10_000 });
    const productName = (await firstRow.locator('.name').innerText()).trim();
    await firstRow.click();
    // 选中回显为可关闭标签（清掉以便后续用例不受商品过滤影响）
    const tag = page.locator('.more-area .n-tag');
    await expect(tag).toHaveText(new RegExp(productName));
    await tag.locator('.n-base-close').click();
    await expect(page.getByRole('button', { name: '+ 选择商品' })).toBeVisible();
  });

  test('账单号搜索交互：输入回车出空态，清空恢复列表', async ({ page }) => {
    const payInput = page.getByPlaceholder('支付单号 / 渠道交易号');
    await payInput.fill('ZF-NOT-EXIST-E2E');
    await payInput.press('Enter');
    // 无命中：表格空态（订单列表空态文案为「暂无订单」）
    await expect(page.locator('.n-data-table').getByText('暂无订单')).toBeVisible({ timeout: 10_000 });
    // 清空触发重查（fill 置空 + Enter 搜索，等价于点清除图标后的 @clear=onSearch）：列表恢复
    await payInput.fill('');
    await payInput.press('Enter');
    await expect(page.locator('.n-data-table tbody tr').first()).toBeVisible({ timeout: 10_000 });
  });

  test('导出改异步：点击立即提示创建成功', async ({ page }) => {
    await page.getByRole('button', { name: '导出 CSV' }).click();
    await expect(
      page.getByText('导出任务已创建，生成完成后可在右上角「下载中心」下载'),
    ).toBeVisible({ timeout: 10_000 });
  });

  test('下载中心：入口/角标/任务完成/下载按钮', async ({ page }) => {
    // 上一用例已创建任务；打开下载中心面板
    await page.locator('.dl-trigger').click();
    await expect(page.locator('.dl-panel')).toBeVisible({ timeout: 10_000 });
    await expect(page.locator('.dl-panel .title')).toHaveText('下载中心');

    // 新任务最终到达已完成（面板打开时 3s 轮询）
    const doneTag = page.locator('.task-row .n-tag', { hasText: '已完成' }).first();
    await expect(doneTag).toBeVisible({ timeout: 20_000 });
    // 完成任务行提供下载按钮；未下载角标出现（badge 包裹触发按钮，≥1 含本例新任务）
    const dlBtn = page.locator('.task-row').first().getByRole('button', { name: '下载' });
    await expect(dlBtn).toBeVisible();
    await expect(page.locator('.n-badge:has(.dl-trigger) .n-badge-sup')).toBeVisible();

    // 回归（2026-10-24 11001 修复）：真实点击下载必须触发浏览器下载事件且文件名正确——
    // 下载按钮不能是 <a href> 原生导航（直链 GET 不带 Authorization 头会被网关拒为未登录）
    const dl = page.waitForEvent('download', { timeout: 10_000 });
    await dlBtn.click();
    const download = await dl;
    expect(download.suggestedFilename()).toMatch(/^orders-.*\.csv$/);
  });

  test('下载中心：超管可删除任务（文件与记录一并删）', async ({ page }) => {
    await page.locator('.dl-trigger').click();
    await expect(page.locator('.dl-panel')).toBeVisible({ timeout: 10_000 });
    const rows = page.locator('.task-row');
    await expect(rows.first()).toBeVisible({ timeout: 10_000 });
    const before = await rows.count();
    if (before === 0) {
      // 无任务时跳过删除（前置用例已保证至少 1 条，这里仅防御空面板）
      return;
    }

    await rows.first().getByRole('button', { name: '删除' }).click();
    // 确认弹窗：删除后行数减少
    await expect(page.getByText('文件将被一并删除，不可恢复')).toBeVisible({ timeout: 10_000 });
    await page.getByRole('button', { name: '删除', exact: true }).last().click();
    await expect(page.getByText('已删除')).toBeVisible({ timeout: 10_000 });
    await expect(rows).toHaveCount(before - 1, { timeout: 10_000 });
  });
});
