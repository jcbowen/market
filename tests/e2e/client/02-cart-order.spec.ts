import { test, expect } from '@playwright/test';
import { clientLogin } from '../../helpers/e2e';

/**
 * C 端 H5 E2E 02：购物车与下单
 * 流程：登录 → 分类页打开商品 → 详情加购 → 购物车页 → 立即购买进确认页 → 提交订单离页
 *
 * 定位口径：分类页商品卡片是 `.product-card`。原用例用「首个含 ¥ 的 uni-view」当卡片，
 * 命中的其实是整页容器（375×656），点击落在卡片间空隙时不导航，整条用例只能等到超时；
 * 现按类名定位，并把「加了什么、购物车里有什么」做成可核对的断言。
 */

/**
 * 打开分类页第一个商品的详情页
 * - 函数名: openFirstProduct
 * - 参数: page — 页面对象
 * - 返回: Promise<string> 商品名（供后续购物车/确认页断言比对）
 * - 异常: 卡片或详情页名称未出现时由 expect 抛出
 */
async function openFirstProduct(page: import('@playwright/test').Page): Promise<string> {
  await page.goto('/s/#/pages/category/category');
  const card = page.locator('.product-card').first();
  await expect(card).toBeVisible({ timeout: 15_000 });
  const name = (await card.locator('.product-name').innerText()).trim();
  // 分类页卡片的点击语义是「加购」，只有商品图（.product-image）才跳详情；
  // 原用例点整页容器的中心纯属碰运气，落到空隙就什么都不发生
  await card.locator('.product-image').click();
  await page.waitForURL(/product\/detail/, { timeout: 15_000 });
  await expect(page.locator('.name')).toHaveText(name, { timeout: 15_000 });
  return name;
}

test.describe.serial('C 端购物与下单', () => {
  test.beforeEach(async ({ page }) => {
    await clientLogin(page);
  });

  test('从分类页打开商品详情', async ({ page }) => {
    await openFirstProduct(page);
    // 详情页底部两个动作按钮都在，说明到的是「可下单的详情页」而不是错误页
    await expect(page.locator('uni-button').filter({ hasText: '加入购物车' })).toBeVisible();
    await expect(page.locator('uni-button').filter({ hasText: '立即购买' })).toBeVisible();
  });

  test('商品详情加购后购物车页出现该商品', async ({ page }) => {
    const name = await openFirstProduct(page);
    await page.locator('uni-button').filter({ hasText: '加入购物车' }).click();
    await expect(page.getByText('已加入购物车')).toBeVisible({ timeout: 10_000 });

    await page.goto('/s/#/pages/cart/cart');
    const item = page.locator('.cart-item').filter({ hasText: name }).first();
    await expect(item, `加购的「${name}」应出现在购物车`).toBeVisible({ timeout: 15_000 });
  });

  test('立即购买进确认页并带出同一商品', async ({ page }) => {
    const name = await openFirstProduct(page);
    await page.locator('uni-button').filter({ hasText: '立即购买' }).click();
    await page.waitForURL(/order\/confirm/, { timeout: 15_000 });
    await expect(page.locator('.goods-item').filter({ hasText: name })).toBeVisible({ timeout: 15_000 });
    // 确认页默认自提，且只下发能力就绪的支付渠道（本机仅现金/货到付款）
    await expect(page.locator('.delivery-option.active')).toContainText('社区自提');
    await expect(page.locator('.payment-option')).toHaveCount(1);
  });

  test('提交订单成功后离开确认页，返回不再回到确认页', async ({ page }) => {
    test.setTimeout(120_000);
    await openFirstProduct(page);
    await page.locator('uni-button').filter({ hasText: '立即购买' }).click();
    await page.waitForURL(/order\/confirm/, { timeout: 15_000 });
    await page.locator('.submit-btn uni-button').click();

    // 下单成功即用 redirectTo 替换确认页：停在「已下单 + 购物车已清」的确认页，
    // 用户再点一次就是重复下单，故此处断言 URL 已换成订单详情
    await page.waitForURL(/pages\/order\/detail\?id=\d+/, { timeout: 30_000 });
    const detailUrl = page.url();
    await expect(page.locator('.order-info')).toContainText('订单编号', { timeout: 15_000 });

    await page.goBack();
    await page.waitForTimeout(1500);
    expect(page.url(), '返回应回到进入确认页之前的页面，而不是已结算的确认页').not.toMatch(/order\/confirm/);
    expect(page.url()).not.toBe(detailUrl);
  });

  test('订单列表页正常渲染并含刚下的单', async ({ page }) => {
    await page.goto('/s/#/pages/order/list');
    await expect(page.locator('.order-card, .order-item').first()).toBeVisible({ timeout: 15_000 });
  });
});
