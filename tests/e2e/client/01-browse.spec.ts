import { test, expect } from '@playwright/test';
import { clientLogin } from '../../helpers/e2e';

/**
 * C 端 H5 E2E 01：登录与浏览
 * 入口：http://localhost:18080/s/（hash 路由，重定向到 /s/app.html#/...）
 * 选择器依据探针：手机号输入为主文档唯一 input，登录按钮为文本「登录」最后一个；
 * uni-input placeholder 为遮罩 div 非原生属性，不能 getByPlaceholder
 *
 * 各用例独立上下文（无 storageState），需要登录态的用例自行调用 clientLogin
 */

test.describe('C 端登录与浏览', () => {
  test('首页（分类）正常渲染商品与价格', async ({ page }) => {
    await page.goto('/s/#/pages/category/category');
    await expect(page.locator('uni-view').filter({ hasText: /¥|￥/ }).first()).toBeVisible({
      timeout: 15_000,
    });
  });

  test('手机号直登成功', async ({ page }) => {
    await clientLogin(page, '13800138000');
    // 登录成功后离开登录页（跳回首页或上一页）
    await expect(page).not.toHaveURL(/pages\/login/);
  });

  test('秒杀页按实付价渲染活动卡', async ({ page, request }) => {
    // 取当前社区的秒杀数据作为期望值：环境无进行中活动时跳过，避免用例依赖种子数据
    const resp = await request.get('/api/v1/flash-sales?community_id=1');
    const body = (await resp.json()) as { data?: { items?: { product_name: string; flash_price: number; original_price: number; sold: number; stock: number }[] }[] };
    const items = (body.data || []).flatMap((a) => a.items || []);
    test.skip(items.length === 0, '当前社区无秒杀商品项');
    const first = items[0];

    // 秒杀页需登录态（路由守卫会跳登录页），且登录后应用初始化才解析出当前社区
    await clientLogin(page, '13800138000');
    await page.goto('/s/#/pages/flash/flash');
    const card = page.locator('.flash-card').first();
    await expect(card, '列表不应为空（onShow 复位分页状态后才有卡片）').toBeVisible({ timeout: 15_000 });

    const price = (await card.locator('.flash-price .current').innerText()).trim();
    // 展示价必须等于下单实付口径的 SKU 价，而不是未生效的活动价
    expect(price).toContain(first.original_price.toFixed(2));
    if (first.flash_price !== first.original_price) {
      await expect(card.locator('.flash-price'), '活动价不得作为可售价格展示').not.toContainText(first.flash_price.toFixed(2));
    }
    await expect(card.locator('.flash-tip')).toContainText('以下单实付为准');
    // 已售/剩余取后端真实字段，前端不再编造
    await expect(card.locator('.flash-stock')).toContainText(`已售 ${first.sold} · 剩余 ${first.stock}`);
  });

  test('满减页可达', async ({ page }) => {
    await page.goto('/s/#/pages/full-reduction/full-reduction');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });

  test('领券中心页可达', async ({ page }) => {
    await page.goto('/s/#/pages/coupon/coupon');
    await expect(page.locator('uni-view').first()).toBeVisible({ timeout: 15_000 });
  });
});
