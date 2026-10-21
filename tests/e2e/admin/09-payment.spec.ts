import { test, expect } from '@playwright/test';
import { adminLogin } from '../../helpers/e2e';

/**
 * 管理端 E2E 09：支付异常队列页（第三方支付回调可靠性 2026-10-21 配套）
 * 覆盖：
 *  - 订单管理组菜单入口可达，点「支付异常」进入 /payment-anomalies
 *  - 页面结构：标题、说明、处理状态筛选（默认「待处理」）、「显示正常轨迹」开关
 *  - 三种筛选口径与「显示正常轨迹」的行数包含关系（全部 ⊇ 待处理，勾选正常轨迹只会更多）
 *  - 行内展开报文原文有响应（超管带 payment:raw:view）
 *  - 销账弹窗「核实依据」必填，空提交不改动队列数据
 *  - 订单详情「支付记录」区块恒在（无支付单给空态而不是整块消失）
 *
 * 不覆盖（本机不可验证，见 10-部署方案/02 第 9.2.7 节）：
 *  验签失败/挂账等异常流水的**产生**、销账后条目从待处理消失、「去退款」按钮实际退款——
 *  流水只能由渠道报文或对账调度器写入，Playwright 无 HTTP 通路可构造，且销账不可逆（无取消销账接口），
 *  产生侧改由 services/payment/service/scheduler_test.go 假渠道用例与 NeedsHandling 判定矩阵单测覆盖。
 */

test.describe('支付异常队列页', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test.beforeEach(async ({ page }) => {
    await adminLogin(page);
  });

  test('菜单入口可达并渲染队列结构', async ({ page }) => {
    await page.waitForURL(/#\/dashboard/, { timeout: 15_000 });
    await page.locator('.n-menu').getByText('订单管理', { exact: true }).click();
    await page.locator('.n-menu').getByText('支付异常', { exact: true }).click();
    await page.waitForURL(/#\/payment-anomalies/, { timeout: 15_000 });

    await expect(page.locator('.n-card-header__main')).toHaveText('支付异常');
    // 说明文案点明本页是「渠道为准」的人工核实出口
    await expect(page.getByText('支付回调、渠道对账与人工同步产生的异常流水')).toBeVisible();
    // 默认队列只看待处理，避免正常轨迹淹没待办
    await expect(page.locator('.n-select .n-base-selection-label')).toHaveText('待处理');
    await expect(page.getByText('显示正常轨迹')).toBeVisible();
  });

  test('筛选口径与「显示正常轨迹」互斥关系生效', async ({ page }) => {
    await page.goto('/b/#/payment-anomalies');
    await expect(page.locator('.n-card-header__main')).toHaveText('支付异常');

    const failed: string[] = [];
    page.on('requestfailed', (req) => failed.push(req.url()));
    const rows = () => page.locator('.n-data-table tbody tr').filter({ has: page.locator('td') });
    const pick = async (label: string) => {
      await page.locator('.n-select').first().click();
      // naive-ui 的下拉项是普通 div（无 role=option），且收起后节点仍留在 body 里，
      // 必须限定「可见的菜单 + 文本精确匹配」，否则会命中上一轮残留项
      await page.locator('.n-select-menu:visible .n-base-select-option', { hasText: new RegExp(`^${label}$`) }).click();
      await expect(page.locator('.n-select .n-base-selection-label')).toHaveText(label);
    };

    // 三种口径都要能出结果（本机异常队列已有真实流水）
    await pick('待处理');
    const pendingRows = await rows().count();
    expect(pendingRows, '待处理队列应有流水（回调失败/渠道未配置都会留痕）').toBeGreaterThan(0);
    await pick('全部');
    const allRows = await rows().count();
    expect(allRows, '「全部」行数不得少于「待处理」').toBeGreaterThanOrEqual(pendingRows);
    await pick('已处理');
    expect(await rows().count(), '已处理队列只应出现已销账的行').toBeLessThanOrEqual(allRows);

    // 勾选「显示正常轨迹」放行的只会是同一批或更多行，不会变少
    await pick('待处理');
    await page.getByText('显示正常轨迹').click();
    await page.locator('button:has-text("查询")').click();
    expect(await rows().count(), '勾选正常轨迹后行数不应减少').toBeGreaterThanOrEqual(pendingRows);

    expect(failed, `页面请求失败：${failed.join(', ')}`).toHaveLength(0);
  });

  test('行内展开报文原文与销账必填留痕', async ({ page }) => {
    await page.goto('/b/#/payment-anomalies');
    await expect(page.locator('.n-card-header__main')).toHaveText('支付异常');
    const row = page.locator('.n-data-table tbody tr').filter({ has: page.locator('td') }).first();
    await expect(row, '本机异常队列应有流水').toBeVisible();

    // 展开原文：超管带 payment:raw:view，按该行是否真存过原文呈现「报文原文」或提示文案，
    // 两种都算通过——但绝不能点了没反应（that is the bug this guards）
    await row.locator('.n-data-table-expand-trigger').click();
    await expect(page.getByText('报文原文').first()).toBeVisible();

    // 销账必须填核实依据：空提交只弹提示且不改动数据（取消后条目仍留在待处理队列）
    await row.getByRole('button', { name: '销账' }).click();
    const dialog = page.locator('.n-modal').filter({ hasText: '支付异常销账' });
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: '确认销账' }).click();
    await expect(page.getByText('请填写核实依据')).toBeVisible();
    await page.getByRole('button', { name: '知道了' }).click();
    await dialog.getByRole('button', { name: '取消' }).click();
    await expect(row.getByRole('button', { name: '销账' })).toBeVisible();
  });

  test('订单详情支付区块可达（现金单必有支付记录）', async ({ page }) => {
    await page.goto('/b/#/orders');
    await expect(page.locator('.n-data-table')).toBeVisible();
    // 首行按创建时间倒序，本轮跑测必然刚落过收银台现金单，详情里「支付记录」有真实行
    const detailBtn = page.getByRole('button', { name: '详情' }).first();
    await expect(detailBtn, '环境应有订单可供查看支付区块').toBeVisible();

    await detailBtn.click();
    // 「支付记录」区块恒在：无支付单时给「本单暂无支付记录」空态而不是整块消失，
    // 否则运营会误判成「这单没走过支付」。「支付通知轨迹」相反——无流水时按设计整块隐藏。
    await expect(page.getByText('支付记录', { exact: true })).toBeVisible();
    await expect(page.getByText(/本单暂无支付记录|支付单号/).first()).toBeVisible();
  });
});
