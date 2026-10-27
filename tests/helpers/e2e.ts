import { expect } from '@playwright/test';
import type { Page } from '@playwright/test';

/** 管理端默认账号（演示种子包预置） */
const ADMIN_USER = process.env.TEST_ADMIN_USER ?? 'admin';
const ADMIN_PASS = process.env.TEST_ADMIN_PASS ?? 'admin123';

/** C 端测试手机号（直登，自动建号/复用） */
export const CLIENT_PHONE = '13800138000';

/**
 * 管理端登录助手（含社区选择）
 * 流程：/b/login 填表登录 → 落在 select-community 页点第一个社区卡片 → 等待进入后台
 * 已知行为：选社区后 URL 形如 /b/login#/dashboard
 * - 函数名: adminLogin
 * - 参数: page — Playwright 页面对象
 * - 返回: Promise<void>，成功后已登录且已选定社区
 * - 异常: 登录超时或表单元素未找到时抛出
 * - 使用示例: await adminLogin(page);
 */
export async function adminLogin(page: Page): Promise<void> {
  await page.goto('/b/login');
  await page.getByPlaceholder('请输入管理员账号').fill(ADMIN_USER);
  await page.getByPlaceholder('请输入密码').fill(ADMIN_PASS);
  await page.getByRole('button', { name: /登\s*录/ }).click();
  // 注意：登录页初始 URL 为 #/login?redirect=/dashboard，若正则不锚定 hash 开头，
  // redirect 参数中的 dashboard 会误匹配导致登录未完成就返回
  await page.waitForURL(/#\/(select-community|dashboard)/, { timeout: 15_000 });

  if (page.url().includes('select-community')) {
    // 社区卡片真实 class 为 community-card（见 views/select-community/index.vue），
    // 不可用 [class*=card] 模糊匹配——会命中外层 .n-card.select-card 容器导致点击无效
    const card = page.locator('.community-card').first();
    await card.waitFor({ state: 'visible', timeout: 10_000 });
    await card.click();
    // 等待离开选择页（跳到 #/dashboard 或其他目标页）
    await page.waitForURL((url) => !url.href.includes('select-community'), { timeout: 15_000 });
    await page.waitForTimeout(1000); // 等待后台布局渲染
  }
}

/**
 * 收银台登录助手（POS 独立应用，/pos 前缀 hash 路由）
 * 流程：/b/#/pos/login 填表登录 → 多社区账号先落 select-community 点第一个社区卡片
 *       （选后整页 reload 回 redirect 目标）→ 进入收银工作台
 * 已知行为：
 * - 不可用 URL 正则判定登录后终态：select-community 的 redirect 查询串形如
 *   /b/#/select-community?redirect=/pos/cashier，含 /pos/cashier 会误匹配，
 *   必须按页面元素（.community-card / .start-shift-guide / .cashier-tabs）判定
 * - 选社区后 onPick 执行 router.push(redirect) + window.location.reload()，
 *   reload 后凭 cookie + localStorage 社区记录恢复登录态直达 redirect 目标
 * - 函数名: posLogin
 * - 参数: page — Playwright 页面对象
 * - 返回: Promise<void>，成功后已登录、已选定社区并位于收银台
 * - 异常: 登录超时或页面元素未找到时抛出
 * - 使用示例: await posLogin(page);
 */
export async function posLogin(page: Page): Promise<void> {
  await page.goto('/b/#/pos/login');
  await page.getByPlaceholder('员工账号').fill(ADMIN_USER);
  await page.getByPlaceholder('密码').fill(ADMIN_PASS);
  await page.getByRole('button', { name: /进入收银台/ }).click();

  // 登录后终态二选一：社区选择页（多社区账号未选定）或直接进收银台
  const landing = page.locator('.community-card, .start-shift-guide, .cashier-tabs').first();
  await landing.waitFor({ state: 'visible', timeout: 20_000 });

  if (await page.locator('.community-card').first().isVisible()) {
    await page.locator('.community-card').first().click();
  }
  // 无论是否经过社区选择，最终都应到达收银台（班次状态异步加载中，tabs 可能闪现）
  await expect(page.locator('.start-shift-guide, .cashier-tabs').first()).toBeVisible({ timeout: 20_000 });
}

/**
 * C 端登录助手（H5 手机号直登）
 * 已知行为：/s/#/... 会重定向到 /s/app.html#/...；uni-input 的 placeholder 是遮罩
 * div 而非原生属性，输入框须按 input 元素定位；登录按钮按可见文本「登录」取最后一个
 * - 函数名: clientLogin
 * - 参数: page — 页面对象；phone — 手机号（默认 13800138000）
 * - 返回: Promise<void>，成功后已登录
 * - 异常: 元素未找到/登录超时抛出
 * - 使用示例: await clientLogin(page);
 */
export async function clientLogin(page: Page, phone: string = CLIENT_PHONE): Promise<void> {
  await page.goto('/s/#/pages/login/login');
  const input = page.locator('input').first();
  await input.waitFor({ state: 'visible', timeout: 15_000 });
  await input.fill(phone);
  // 注意：按钮文本在 uni-button 内部的 span 上，直接 getByText 点 span 会被 uni-button
  // 拦截 pointer events 导致点击超时；必须点 uni-button 本身
  await page.locator('uni-button').filter({ hasText: '登录' }).last().click();
  // 登录成功后跳首页（#/），等待离开登录页即代表 token 已写入 storage
  await page.waitForURL((url) => !url.href.includes('pages/login/login'), { timeout: 15_000 });
}
