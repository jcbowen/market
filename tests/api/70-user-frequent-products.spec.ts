import { test, expect, type Playwright } from '@playwright/test';
import type { APIRequestContext } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  assertOk,
  asList,
  type ApiResponse,
  type PageData,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 70：会员常买商品排行分页模式（GET /api/v1/admin/users/:id/frequent-products）
 * 背景：`c4f4b4e` 给常买统计加了 page/page_size/total 分页模式，42 号只覆盖 C 端 limit 模式
 * （C 端 /orders/frequent-products 仍走 limit，两者共用同一 RPC 但分支不同），本模块补齐管理端分支。
 * 覆盖：
 *   - 分页三件套回显：total = 该会员有效订单去重商品数，page/page_size 原样回显
 *   - 排序口径：purchase_count DESC, total_quantity DESC；翻页结果不重叠且顺序连续
 *   - 排除口径：待支付(1)/已取消(6)/退款中(7)/已退款(8) 不计入，有效状态 2/3/4/5
 *   - 越界页返回空列表但 total 正常
 *   - 每商品附带该用户最常买 SKU（sku_id 回填为累计件数 Top1）
 *   - 现状锁定（非期望行为，缺陷已记入待办清单，本轮不改生产代码）：
 *     ① page=0 时服务端 `paged = req.Page > 0` 判为 false，整条请求退回 limit 模式：
 *        返回最多 10 条且 total=0（前端若把 0 当总数会算出 0 页）；
 *     ② page_size=0 实际按 5 执行（服务端 pageSize<=0 兜底），但响应 page_size 回显 0，
 *        回显值与真实分页尺寸不一致
 *   - 路径参数非法（非数字/≤0）→ 10001；未知会员 → 空列表 total 0（用户维度不校验存在性）
 * 数据惯例：4 个自建商品（A 两次成交、B/C/D 各一次且件数递减以打破并列）；
 * 排除口径用第二个买家单独验证，避免主买家耗尽下单限流令牌（UserRateLimit 0.08/s，桶容量 5）
 */

/** 下单限流码与令牌回补等待（同 68 号，防御性兜底） */
const RATE_LIMIT = 10005;
const REFILL_MS = 13_000;

interface FrequentItem {
  product_id: number;
  product_name: string;
  product_image?: string;
  sku_id: number;
  purchase_count: number;
  total_quantity: number;
  [k: string]: unknown;
}

let adminApi: APIRequestContext;
let communityId = 0;
let categoryId = 0;
/** 主买家令牌与会员 ID（排行统计对象） */
let buyerToken = '';
let buyerId = 0;
/** 商品 A/B/C/D：A 成交 2 单，B/C/D 各 1 单且件数 4/3/2 */
const products: { productId: number; skuId: number; name: string }[] = [];
/** 第二买家（验证待支付不计入，独立限流桶） */
let secondBuyerToken = '';
let secondBuyerId = 0;

/**
 * 创建在售单规格商品
 * - 函数名: createProduct
 * - 参数: label — 名称标识
 * - 返回: Promise<{ productId: number; skuId: number; name: string }>
 * - 异常: 建品、取不到 SKU 或置为在售失败时抛出 Error（造数前置）
 * - 使用示例: const p = await createProduct('A');
 */
async function createProduct(label: string): Promise<{ productId: number; skuId: number; name: string }> {
  const name = autoName(`常买排行${label}`);
  const created = assertOk<{ product?: { id: number }; id?: number }>(
    (await (
      await adminApi.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name,
          unit: '份',
          is_multi_spec: false,
          sku_items: [{ price: 3.5, cost_price: 1.5, stock: 50, safety_stock: 0 }],
        },
      })
    ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
    '创建商品',
  );
  const productId = created.product?.id ?? created.id ?? 0;
  if (!productId) throw new Error('创建商品未返回 ID');

  const on = (await (await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })).json()) as ApiResponse;
  if (on.code !== 0) throw new Error(`商品 ${productId} 置为在售失败: ${on.message}`);

  const skus = assertOk<{ id: number }[]>(
    (await (await adminApi.get('/api/v1/skus', { params: { product_id: productId } })).json()) as ApiResponse<{ id: number }[]>,
    'SKU 列表',
  );
  const skuId = skus?.[0]?.id ?? 0;
  if (!skuId) throw new Error(`商品 ${productId} 未生成 SKU`);
  return { productId, skuId, name };
}

/**
 * C 端下现金支付自提单（下单即支付，使订单进入有效状态 2）
 * - 函数名: createPaidOrder
 * - 参数: playwright — Playwright 工厂；token — 买家令牌；sku — SKU ID；quantity — 件数
 * - 返回: Promise<number> 订单 ID
 * - 异常: 限流重试超上限或业务失败时抛出 Error
 * - 使用示例: const id = await createPaidOrder(playwright, buyerToken, skuId, 2);
 */
async function createPaidOrder(playwright: Playwright, token: string, sku: number, quantity: number): Promise<number> {
  return createOrder(playwright, token, sku, quantity, true);
}

/**
 * C 端下单（可选立即现金支付）
 * - 函数名: createOrder
 * - 参数: playwright — Playwright 工厂；token — 买家令牌；sku — SKU ID；quantity — 件数；pay — 是否立即现金支付
 * - 返回: Promise<number> 订单 ID
 * - 异常: 下单/支付业务失败时经 assertOk 抛出；限流重试超上限抛 Error
 * - 使用示例: await createOrder(playwright, token, skuId, 1, false);
 */
async function createOrder(playwright: Playwright, token: string, sku: number, quantity: number, pay: boolean): Promise<number> {
  const api = await newAuthedRequest(playwright, token);
  try {
    for (let attempt = 1; attempt <= 3; attempt++) {
      const body = (await (
        await api.post('/api/v1/orders', {
          data: { delivery_type: 1, address_id: 0, remark: autoName('70 排行单'), items: [{ sku_id: sku, quantity }] },
        })
      ).json()) as ApiResponse<{ id: number }>;
      if (body.code === RATE_LIMIT) {
        await new Promise((r) => setTimeout(r, REFILL_MS));
        continue;
      }
      const orderId = assertOk(body, 'C 端下单').id;
      if (pay) {
        const paid = (await (await api.post('/api/v1/payments', { data: { order_id: orderId, channel: 'cash' } })).json()) as ApiResponse;
        assertOk(paid, '现金支付');
      }
      return orderId;
    }
  } finally {
    await api.dispose();
  }
  throw new Error('C 端下单连续 3 次被限流（10005）');
}

/**
 * 读取会员常买商品排行（管理端分页模式）
 * - 函数名: fetchFrequent
 * - 参数: userId — 会员 ID；params — query 参数（page/page_size）
 * - 返回: Promise<PageData<FrequentItem>>
 * - 异常: 业务码非 0 时经 assertOk 抛出
 * - 使用示例: const page = await fetchFrequent(buyerId, { page: 1, page_size: 2 });
 */
async function fetchFrequent(userId: number, params: Record<string, string | number> = {}): Promise<PageData<FrequentItem>> {
  const body = (await (
    await adminApi.get(`/api/v1/admin/users/${userId}/frequent-products`, { params })
  ).json()) as ApiResponse<PageData<FrequentItem>>;
  return assertOk(body, `会员常买排行(${userId})`);
}

/**
 * 原始响应读取（供负向断言，不抛业务异常）
 * - 函数名: rawFetch
 * - 参数: userId — 路径会员 ID；params — query 参数
 * - 返回: Promise<ApiResponse>
 * - 异常: 无
 */
async function rawFetch(userId: string | number, params: Record<string, string | number> = {}): Promise<ApiResponse> {
  return (await (await adminApi.get(`/api/v1/admin/users/${userId}/frequent-products`, { params })).json()) as ApiResponse;
}

/**
 * 取一个绑定到指定社区的 C 端买家
 * 新注册号自动落在社区列表首个社区，可能与本轮语境不同；跨社区下单会被拒，故显式改绑后重登（JWT 需刷新社区）
 * - 函数名: newBuyerIn
 * - 参数: playwright — Playwright 工厂；target — 目标社区 ID
 * - 返回: Promise<{ token: string; id: number }> 访问令牌与会员 ID
 * - 异常: 登录/改绑失败或改绑后社区仍不符时抛出 Error
 * - 使用示例: const { token, id } = await newBuyerIn(playwright, communityId);
 */
async function newBuyerIn(playwright: Playwright, target: number): Promise<{ token: string; id: number }> {
  const phone = uniquePhone();
  let info = await clientLogin(playwright.request, phone);
  if (info.user.community_id !== target) {
    const ctx = await newAuthedRequest(playwright, info.access_token);
    try {
      assertOk(
        (await (await ctx.put('/api/v1/user/community', { data: { community_id: target } })).json()) as ApiResponse,
        `改绑社区(${target})`,
      );
    } finally {
      await ctx.dispose();
    }
    // 改绑发生在登录之后，JWT 内社区仍是旧值，需重登刷新
    info = await clientLogin(playwright.request, phone);
    if (info.user.community_id !== target) {
      throw new Error(`买家社区改绑失败：期望 ${target}，实际 ${info.user.community_id}`);
    }
  }
  return { token: info.access_token, id: info.user.id };
}

test.beforeAll(async ({ playwright }) => {
  const buyer = await clientLogin(playwright.request, uniquePhone());
  buyerToken = buyer.access_token;
  buyerId = buyer.user.id;

  const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
  adminApi = admin.api;
  communityId = admin.communityId;

  try {
    categoryId = assertOk<{ id: number }>(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('排行分类') } })).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    ).id;
    for (const label of ['A', 'B', 'C', 'D']) products.push(await createProduct(label));

    const second = await newBuyerIn(playwright, communityId);
    secondBuyerToken = second.token;
    secondBuyerId = second.id;
  } catch (e) {
    console.warn(`[API70] 造数失败，用例将跳过：${e instanceof Error ? e.message : e}`);
    categoryId = 0;
  }
});

test.afterAll(async () => {
  for (const p of products) {
    const del = (await (await adminApi.delete(`/api/v1/admin/products/${p.productId}`)).json()) as ApiResponse;
    if (del.code !== 0) console.warn(`[API70] 商品 ${p.productId} 清理失败（被订单引用属预期）：${del.message}`);
  }
  if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  await adminApi?.dispose();
});

test.describe.serial('会员常买商品排行分页', () => {
  test('1. 成交 4 个商品：A 两单、B/C/D 各一单且件数递减', async ({ playwright }) => {
    test.skip(!categoryId || products.length < 4, '造数未完成');
    const [a, b, c, d] = products;
    await createPaidOrder(playwright, buyerToken, a.skuId, 1);
    await createPaidOrder(playwright, buyerToken, a.skuId, 2);
    await createPaidOrder(playwright, buyerToken, b.skuId, 4);
    await createPaidOrder(playwright, buyerToken, c.skuId, 3);
    await createPaidOrder(playwright, buyerToken, d.skuId, 2);
    expect(buyerId, '主买家会员 ID 应已建立').toBeGreaterThan(0);
  });

  test('2. 分页三件套：total=去重商品数 4，page/page_size 原样回显', async () => {
    test.skip(products.length < 4, '造数未完成');
    const page = await fetchFrequent(buyerId, { page: 1, page_size: 2 });
    expect(page.total ?? -1, 'total 应为有效订单去重商品数').toBe(4);
    expect(page.page).toBe(1);
    expect(page.page_size).toBe(2);
    expect(asList<FrequentItem>(page), 'page_size=2 应返回 2 行').toHaveLength(2);

    const all = await fetchFrequent(buyerId, { page: 1, page_size: 10 });
    expect(asList<FrequentItem>(all), '一页取全应含 4 个商品').toHaveLength(4);
    expect(all.total ?? -1).toBe(4);
  });

  test('3. 排序与翻页：次数降序、件数破并列，翻页结果不重叠且顺序连续', async () => {
    test.skip(products.length < 4, '造数未完成');
    const [a, b, c, d] = products;
    const p1 = asList<FrequentItem>(await fetchFrequent(buyerId, { page: 1, page_size: 2 }));
    const p2 = asList<FrequentItem>(await fetchFrequent(buyerId, { page: 2, page_size: 2 }));
    const full = asList<FrequentItem>(await fetchFrequent(buyerId, { page: 1, page_size: 10 }));

    expect(p1.map((r) => r.product_id), '首页应是 A（2 单）与 B（1 单 4 件）').toEqual([a.productId, b.productId]);
    expect(p2.map((r) => r.product_id), '第二页应是 C（3 件）与 D（2 件）').toEqual([c.productId, d.productId]);
    expect(new Set([...p1, ...p2].map((r) => r.product_id)).size, '两页合计 4 个不重复商品').toBe(4);
    expect(
      full.map((r) => r.product_id),
      '全量顺序与翻页拼接顺序一致',
    ).toEqual([...p1, ...p2].map((r) => r.product_id));

    const itemA = full.find((r) => r.product_id === a.productId);
    expect(itemA?.purchase_count ?? -1, '购买次数 = 去重订单数').toBe(2);
    expect(itemA?.total_quantity ?? -1, '累计件数 = SUM(quantity)').toBe(3);
    expect(itemA?.product_name, '商品名应回填').toBe(a.name);

    const counts = full.map((r) => r.purchase_count ?? -1);
    const quantities = full.map((r) => r.total_quantity ?? -1);
    expect(counts, '排序键：购买次数降序').toEqual([2, 1, 1, 1]);
    expect(quantities, '同次数时按累计件数降序（B 4 件 > C 3 件 > D 2 件）').toEqual([3, 4, 3, 2]);
  });

  test('4. 每商品回显最常买 SKU（sku_id 为该用户累计件数 Top1）', async () => {
    test.skip(products.length < 4, '造数未完成');
    const full = asList<FrequentItem>(await fetchFrequent(buyerId, { page: 1, page_size: 10 }));
    for (const p of products) {
      const row = full.find((r) => r.product_id === p.productId);
      expect(row, `排行应含商品 ${p.name}`).toBeTruthy();
      expect(row!.sku_id ?? 0, 'sku_id 应回填该商品的成交 SKU').toBe(p.skuId);
    }
  });

  test('5. 待支付订单不计入：新会员下单未支付时排行为空', async ({ playwright }) => {
    test.skip(!secondBuyerId || products.length < 4, '第二买家或商品未就绪');
    await createOrder(playwright, secondBuyerToken, products[0].skuId, 1, false);
    const page = await fetchFrequent(secondBuyerId, { page: 1, page_size: 10 });
    expect(asList<FrequentItem>(page), '待支付(1) 订单不应产生排行行').toHaveLength(0);
    expect(page.total ?? -1, '待支付订单不应计入去重商品数').toBe(0);
  });

  test('6. 越界页：返回空列表但 total 仍为真实总数', async () => {
    test.skip(products.length < 4, '造数未完成');
    const page = await fetchFrequent(buyerId, { page: 9, page_size: 2 });
    expect(asList<FrequentItem>(page)).toHaveLength(0);
    expect(page.total ?? -1, '越界页 total 不应归零').toBe(4);
    expect(page.page).toBe(9);
  });

  test('7. 未知会员：空列表且 total 0（用户维度不校验会员存在性）', async () => {
    const page = await fetchFrequent(9_999_999, { page: 1, page_size: 10 });
    expect(asList<FrequentItem>(page)).toHaveLength(0);
    expect(page.total ?? -1).toBe(0);
  });

  test('8. 路径参数非法（非数字与 ≤0）被参数校验拒绝（10001）', async () => {
    for (const bad of ['abc', '0', '-1']) {
      const body = await rawFetch(bad, { page: 1, page_size: 10 });
      expect(body.code, `user_id=${bad} 应被参数校验拒绝`).toBe(10001);
    }
  });

  test('9. 分页入参已收敛：page=0/负数归一为首页，走分页模式且 total 为真实总数', async () => {
    test.skip(products.length < 4, '造数未完成');
    // 原缺陷（服务端 paged := req.Page > 0，page=0 或负数整条请求退回 limit 分支，
    // limit<=0 兜底 10 且 total 未赋值恒为 0，前端据此算页数会得到 0 页）已在 c4f5287 修掉：
    // 网关 parsePageParams 把 page<=0 归一为 1、page_size<=0 归一为默认值并封顶 100，
    // 因此管理端始终走分页模式；C 端 limit 模式（不传 page）仍由 42 号用例覆盖。
    for (const page of [0, -3]) {
      const body = await fetchFrequent(buyerId, { page });
      expect(asList<FrequentItem>(body), `page=${page} 返回首页数据`).toHaveLength(4);
      expect(body.total ?? -1, `page=${page} 的分页模式 total 应为真实总数`).toBe(4);
      expect(body.page, `page=${page} 应回显为 1`).toBe(1);
    }
  });

  test('10. 分页入参已收敛：page_size=0 归一为默认 5，超大值封顶 100', async () => {
    test.skip(products.length < 4, '造数未完成');
    const body = (await (
      await adminApi.get(`/api/v1/admin/users/${buyerId}/frequent-products`, { params: { page: 1, page_size: 0 } })
    ).json()) as ApiResponse<PageData<FrequentItem>>;
    expect(body.code).toBe(0);
    expect(body.data?.page_size, 'page_size=0 应归一并回显为默认 5').toBe(5);
    expect(asList<FrequentItem>(body.data), '本轮 4 个商品全部落在首页内').toHaveLength(4);

    const huge = await fetchFrequent(buyerId, { page: 1, page_size: 1000 });
    expect(asList<FrequentItem>(huge), '超大 page_size 不报错，按实际命中返回').toHaveLength(4);
    expect(huge.total ?? -1).toBe(4);
  });

  test('11. 已支付单取消后从排行消失：有效状态口径 2/3/4/5', async ({ playwright }) => {
    test.skip(!secondBuyerId || products.length < 4, '第二买家或商品未就绪');
    const orderId = await createPaidOrder(playwright, secondBuyerToken, products[1].skuId, 2);
    const paidPage = await fetchFrequent(secondBuyerId, { page: 1, page_size: 10 });
    expect(asList<FrequentItem>(paidPage), '支付后应出现 1 个排行商品').toHaveLength(1);

    const api = await newAuthedRequest(playwright, secondBuyerToken);
    try {
      const cancelled = (await (
        await api.put(`/api/v1/orders/${orderId}/cancel`, { data: { reason: autoName('70 取消验证') } })
      ).json()) as ApiResponse;
      expect(cancelled.code, `C 端取消货到付款单失败: ${cancelled.message}`).toBe(0);
    } finally {
      await api.dispose();
    }
    const after = await fetchFrequent(secondBuyerId, { page: 1, page_size: 10 });
    expect(asList<FrequentItem>(after), '已取消(6) 订单应从排行消失').toHaveLength(0);
    expect(after.total ?? -1, '去重商品数同步归零').toBe(0);
  });
});
