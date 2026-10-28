import { test, expect, type Page } from '@playwright/test';
import { newAdminContext, assertOk, type ApiResponse } from '../../helpers/api';
import { adminLogin } from '../../helpers/e2e';

/**
 * 管理端 E2E 08：商品列表批量上下架与行内改名
 * 覆盖（对应 54bd2c5 行内改名 + ffe625f 批量上下架两次前端改造的行为断言）：
 *  - 批量按钮按选中项状态动态显隐（全在售只出「批量下架」，混合时两个都出）
 *  - 确认弹窗文案与「取消不生效 / 确定生效」两条分支
 *  - 批量成功后 message 提示、行状态标签刷新，且 loadData 回调清空多选（按钮随之消失）
 *  - 状态 Tab 与列表联动：下架后商品从「出售中」Tab 消失、出现在「仓库中」Tab
 *    （当前因前端 :tabs 误用未渲染，用例 5 标 fixme 保留断言，待修复后自动生效）
 *  - 行内改名：空值与未变更静默退出且不发请求；成功改名回显新名称；
 *    接口失败（超 100 字）保留编辑态与草稿供修正
 * 行定位统一按商品 ID 单元格（rowOf），原因见该函数注释：编辑态下名称不再是文本节点，
 * 连续改名也会让名称失效
 * 社区语境：helpers/e2e 的 adminLogin 点选社区列表首卡，与 API 造数语境（newAdminContext
 * 取首个非 AUTO-TEST 社区）可能不同，故登录后把 localStorage.current_community_id 固定为
 * 造数社区并整页重载，保证「造的数据一定能在这台浏览器里看到」
 */

/** 造数社区 ID 与列表浏览器语境对齐用的 localStorage 键（见 stores/auth.ts CURRENT_COMMUNITY_KEY） */
const COMMUNITY_STORAGE_KEY = 'current_community_id';

/** 造数商品名公共前缀，兼作列表搜索关键词（所有改名结果同前缀，保证可搜回） */
const SEED_PREFIX = 'AUTO-TEST 批量E2E';

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let communityId = 0;
let categoryId = 0;
/** 本轮造数唯一标记，两个商品名共享，隔离历史轮次 */
let tag = '';
/** 造数商品 ID：甲（在售）、乙（仓库中），用例统一按 ID 定位表格行 */
let idA = 0;
let idB = 0;

/**
 * 构造本轮造数商品名（保留 AUTO-TEST 前缀便于识别与清理）
 * - 函数名: seedName
 * - 参数: label — 名称后缀标识，如 '甲' / '甲改名'
 * - 返回: string，如 'AUTO-TEST 批量E2E甲 1718000000000'
 * - 异常: 无
 */
function seedName(label: string): string {
  return `${SEED_PREFIX}${label} ${tag}`;
}

/**
 * 创建一条商品并设定上下架状态
 * - 函数名: createProduct
 * - 参数: name — 商品名；status — 目标状态（1 在售 / 0 仓库中）
 * - 返回: Promise<number> 新商品 ID
 * - 异常: 建品、设状态或建分类失败时抛出 Error（beforeAll 造数前置，非用例断言）
 */
async function createProduct(name: string, status: number): Promise<number> {
  const created = assertOk<{ product?: { id: number }; id?: number }>(
    (await (
      await adminApi.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name,
          unit: '份',
          is_multi_spec: false,
          sku_items: [{ price: 3.5, stock: 5, safety_stock: 0 }],
        },
      })
    ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
    '创建商品',
  );
  const id = created.product?.id ?? created.id ?? 0;
  if (!id) throw new Error('创建商品未返回 ID');
  const set = (await (
    await adminApi.post(`/api/v1/admin/products/${id}/status`, { data: { status } })
  ).json()) as ApiResponse;
  if (set.code !== 0) throw new Error(`设置状态失败 code=${set.code} message=${set.message}`);
  return id;
}

/**
 * 登录并进入商品列表页，浏览器社区语境与造数社区对齐
 * - 函数名: openProductList
 * - 参数: page — Playwright 页面对象
 * - 返回: Promise<void>
 * - 异常: 登录失败或列表页未渲染时由断言/超时抛出
 */
async function openProductList(page: Page): Promise<void> {
  await adminLogin(page);
  await page.evaluate(
    ([key, value]) => localStorage.setItem(key, value),
    [COMMUNITY_STORAGE_KEY, String(communityId)] as [string, string],
  );
  // hash 路由换页不触发文档重载，须显式 reload 让 pinia store 重新读取持久化社区
  await page.goto('/b/#/products');
  await page.reload();
  await expect(page.locator('.n-data-table')).toBeVisible({ timeout: 15_000 });
}

/**
 * 按造数前缀搜索，把列表收敛到本轮商品
 * - 函数名: searchSeed
 * - 参数: page — 页面对象；keyword — 搜索词（默认取本轮造数公共前缀）
 * - 返回: Promise<void>
 * - 异常: 查询后表格未渲染时由超时抛出
 */
async function searchSeed(page: Page, keyword = SEED_PREFIX): Promise<void> {
  await page.getByPlaceholder('商品名称').fill(keyword);
  await page.getByRole('button', { name: '查询' }).click();
  await expect(page.locator('.n-data-table')).toBeVisible();
}

/**
 * 断言出现指定内容的 message 提示
 * - 函数名: expectMessage
 * - 参数: page — 页面对象；text — 提示文本片段
 * - 返回: Promise<void>
 * - 异常: 超时未出现时抛出；用 filter 收敛避免多条提示触发 strict mode 冲突
 */
async function expectMessage(page: Page, text: string): Promise<void> {
  await expect(page.locator('.n-message').filter({ hasText: text })).toBeVisible();
}

/**
 * 取某个商品所在表格行
 * - 函数名: rowOf
 * - 参数: page — 页面对象；id — 商品 ID（第 2 列，第 1 列是多选框）
 * - 返回: Locator 行元素
 * - 异常: 无（断言交由调用方）
 * 说明: 不能按商品名 hasText 定位——行内改名进入编辑态后名称变成输入框的 value，
 * 不再是可以匹配的文本节点；且连续改名用例中名称会变，按名称定位需不断换关键词。
 * 也不能只用 td 文本等于 ID：库存列同样可能是小数字，故锁定第 2 列再比精确文本。
 */
function rowOf(page: Page, id: number): ReturnType<Page['locator']> {
  // 第 1 列是多选框，第 2 列才是 ID；限定列位避免库存/销量数字撞车
  const idCell = page.locator('.n-data-table-td:nth-child(2)').filter({ hasText: new RegExp(`^${id}$`) });
  return page.locator('.n-data-table-tr').filter({ has: idCell });
}

/**
 * 监听行内改名请求，返回被捕获的 PUT URL 列表
 * - 函数名: watchRenameRequests
 * - 参数: page — 页面对象
 * - 返回: string[] 实时追加的数组，用于断言「是否发出请求」
 * - 异常: 无
 */
function watchRenameRequests(page: Page): string[] {
  const hits: string[] = [];
  page.on('request', (req) => {
    if (req.method() === 'PUT' && /\/api\/v1\/admin\/products\/\d+\/name$/.test(req.url())) hits.push(req.url());
  });
  return hits;
}

/**
 * 勾选/取消勾选某行的多选框
 * - 函数名: toggleRowCheckbox
 * - 参数: page — 页面对象；id — 行内商品 ID
 * - 返回: Promise<void>
 * - 异常: 行或复选框不可见时由超时抛出
 */
async function toggleRowCheckbox(page: Page, id: number): Promise<void> {
  await rowOf(page, id).locator('.n-checkbox').first().click();
}

/**
 * 发起批量操作并处理确认弹窗
 * - 函数名: runBatch
 * - 参数: page — 页面对象；action — '批量上架' | '批量下架'；accept — 点确定还是取消
 * - 返回: Promise<void>
 * - 异常: 按钮或弹窗未出现时由超时抛出
 */
async function runBatch(page: Page, action: '批量上架' | '批量下架', accept: boolean): Promise<void> {
  await page.getByRole('button', { name: action }).click();
  const dialog = page.locator('.n-dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText('批量操作确认');
  await dialog.getByRole('button', { name: accept ? '确定' : '取消' }).click();
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  communityId = admin.communityId;
  tag = String(Date.now());

  try {
    categoryId = assertOk<{ id: number }>(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: `AUTO-TEST 批量E2E分类 ${tag}` } })).json()) as ApiResponse<{
        id: number;
      }>,
      '创建分类',
    ).id;
    idA = await createProduct(seedName('甲'), 1);
    idB = await createProduct(seedName('乙'), 0);
  } catch (e) {
    console.warn(`[E2E08] 造数失败，用例将跳过：${e instanceof Error ? e.message : e}`);
    categoryId = 0;
  }
});

test.afterAll(async () => {
  try {
    const list = assertOk<{ list?: { id: number }[] }>(
      (await (await adminApi.get('/api/v1/admin/products', { params: { keyword: SEED_PREFIX } })).json()) as ApiResponse<{
        list?: { id: number }[];
      }>,
      '查询造数商品',
    );
    for (const row of list.list ?? []) await adminApi.delete(`/api/v1/admin/products/${row.id}`);
    if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

test.describe.serial('商品列表批量上下架与行内改名', () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test.beforeEach(async ({ page }) => {
    test.skip(categoryId === 0, '造数未完成');
    await openProductList(page);
    await searchSeed(page);
  });

  test('1. 选中在售行时只出现「批量下架」，未选中时无批量按钮', async ({ page }) => {
    await expect(page.getByRole('button', { name: '批量下架' })).toHaveCount(0);

    await toggleRowCheckbox(page, idA);
    await expect(page.getByRole('button', { name: '批量下架' })).toBeVisible();
    await expect(page.getByRole('button', { name: '批量上架' })).toHaveCount(0);
  });

  test('2. 混合选中（在售 + 仓库中）时两个批量按钮同时出现', async ({ page }) => {
    await toggleRowCheckbox(page, idA);
    await toggleRowCheckbox(page, idB);
    await expect(page.getByRole('button', { name: '批量上架' })).toBeVisible();
    await expect(page.getByRole('button', { name: '批量下架' })).toBeVisible();
  });

  test('3. 确认弹窗点「取消」：数量文案准确且状态不变', async ({ page }) => {
    await toggleRowCheckbox(page, idA);
    await toggleRowCheckbox(page, idB);
    await page.getByRole('button', { name: '批量下架' }).click();
    const dialog = page.locator('.n-dialog');
    await expect(dialog).toContainText('确定将选中的 2 件商品下架吗？');
    await dialog.getByRole('button', { name: '取消' }).click();

    await expect(dialog).toBeHidden();
    await expect(rowOf(page, idA).getByText('出售中')).toBeVisible();
    await expect(rowOf(page, idB).getByText('仓库中')).toBeVisible();
  });

  test('4. 批量下架：提示件数、行状态转仓库中、成功后多选被清空', async ({ page }) => {
    await toggleRowCheckbox(page, idA);
    await toggleRowCheckbox(page, idB);
    await runBatch(page, '批量下架', true);

    await expectMessage(page, '已下架 2 件商品');
    await expect(rowOf(page, idA).getByText('仓库中')).toBeVisible();
    await expect(rowOf(page, idB).getByText('仓库中')).toBeVisible();
    // loadData 的 finally 清空 checkedRowKeys，批量按钮随选中态消失（不残留失效多选）
    await expect(page.getByRole('button', { name: '批量下架' })).toHaveCount(0);
  });

  test('5. 状态 Tab 联动：下架商品从「出售中」消失、在「仓库中」可见', async ({ page }) => {
    // 本用例是 list.vue 状态 Tab 回归判据：曾因 `:tabs="statusTabs"` 误传 NTabs 不存在的 prop
    // 而整条 Tab 渲染成空壳（看不到任何标签、状态筛选入口消失），2026-10-29 改回 <n-tab-pane> 子节点写法后转绿
    await page.locator('.n-tabs-tab').filter({ hasText: '出售中' }).click();
    await expect(rowOf(page, idA)).toHaveCount(0);
    await expect(rowOf(page, idB)).toHaveCount(0);

    await page.locator('.n-tabs-tab').filter({ hasText: '仓库中' }).click();
    await expect(rowOf(page, idA).getByText('仓库中')).toBeVisible();
    await expect(rowOf(page, idB).getByText('仓库中')).toBeVisible();
  });

  test('6. 批量上架：两商品回到出售中且状态标签同步', async ({ page }) => {
    await toggleRowCheckbox(page, idA);
    await toggleRowCheckbox(page, idB);
    await runBatch(page, '批量上架', true);

    await expectMessage(page, '已上架 2 件商品');
    await expect(rowOf(page, idA).getByText('出售中')).toBeVisible();
    await expect(rowOf(page, idB).getByText('出售中')).toBeVisible();
  });

  test('7. 行内改名：草稿为空或未变更时不发请求并退出编辑态', async ({ page }) => {
    const name = seedName('甲');
    const puts = watchRenameRequests(page);
    const row = rowOf(page, idA);

    await row.getByRole('button', { name: '改名' }).click();
    await row.locator('.n-input input').fill('');
    await row.getByRole('button', { name: '保存' }).click();
    await expect(row.getByRole('button', { name: '改名' })).toBeVisible();
    expect(puts, '空草稿不应发请求').toHaveLength(0);

    await row.getByRole('button', { name: '改名' }).click();
    await row.locator('.n-input input').fill(name);
    await row.getByRole('button', { name: '保存' }).click();
    await expect(row.getByRole('button', { name: '改名' })).toBeVisible();
    expect(puts, '未变更草稿不应发请求').toHaveLength(0);
  });

  test('8. 行内改名：Esc 取消后名称保持原值', async ({ page }) => {
    const name = seedName('甲');
    const puts = watchRenameRequests(page);
    const row = rowOf(page, idA);

    await row.getByRole('button', { name: '改名' }).click();
    await row.locator('.n-input input').fill('按ESC撤销的名字');
    await row.locator('.n-input input').press('Escape');
    await expect(row.getByText(name, { exact: true })).toBeVisible();
    expect(puts, '取消编辑不应发请求').toHaveLength(0);
  });

  test('9. 行内改名：成功提交后列表回显新名称并提示', async ({ page }) => {
    const newName = seedName('甲改名');
    const row = rowOf(page, idA);

    await row.getByRole('button', { name: '改名' }).click();
    await row.locator('.n-input input').fill(newName);
    await row.getByRole('button', { name: '保存' }).click();

    await expectMessage(page, '名称已更新');
    await expect(rowOf(page, idA).getByText(newName, { exact: true })).toBeVisible();
  });

  test('10. 行内改名：接口拒绝超长名时保留编辑态与草稿供修正', async ({ page }) => {
    const overLimit = '超'.repeat(101);
    const row = rowOf(page, idA);

    await row.getByRole('button', { name: '改名' }).click();
    await row.locator('.n-input input').fill(overLimit);
    await row.getByRole('button', { name: '保存' }).click();

    await expectMessage(page, '不能超过 100 字');
    await expect(row.locator('.n-input input')).toHaveValue(overLimit);

    // 修正为合法长度后重试应成功，证明编辑态未丢数据
    const finalName = seedName('甲终态');
    await row.locator('.n-input input').fill(finalName);
    await row.getByRole('button', { name: '保存' }).click();
    await expect(rowOf(page, idA).getByText(finalName, { exact: true })).toBeVisible();
  });
});
