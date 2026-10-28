import { test, expect } from '@playwright/test';
import { asList, assertOk, newAdminContext, type ApiResponse, type PageData } from '../helpers/api';
import { uniqueSuffix } from '../helpers/data';

/**
 * 模块 83：搜索关键词的 LIKE 通配符按字面量匹配
 * 背景：商品搜索把 keyword 直接拼进 `%keyword%`，MySQL LIKE 的默认通配符因此对用户输入生效——
 *   - 输入一个 `%` 让条件退化成「匹配全部」，精确搜索失效且索引失效（全表扫描）；
 *   - 输入一个 `_` 会匹配任意单字符，搜「A_B」把「AXB」一起捞出来。
 * 现在 keyword 统一走 likeutil 转义（`\`、`%`、`_`），列表 / 状态统计 / C 端搜索 / SKU 搜索四处共用同一口径。
 * 用例做法：同一唯一后缀下造 4 个商品名，让「通配符语义」与「字面量语义」的结果集可区分——
 * 修复前两者相同（多命中），修复后只有字面量那一个命中。
 */

interface ProductItem {
  id: number;
  name: string;
  [k: string]: unknown;
}

interface SkuItem {
  id: number;
  product_id: number;
  [k: string]: unknown;
}

interface StatusStats {
  total: number;
  on_sale: number;
  sold_out: number;
  warehouse: number;
  [k: string]: unknown;
}

/** 本轮唯一后缀（纯 ts+数字+x+数字，不含通配符），保证与库内既有数据隔离 */
const BASE = `w${uniqueSuffix()}`;

/** 名称含字面 `%` 的商品—— `%` 被误当通配符时，它与「无 % 的近邻名」结果相同 */
const NAME_PCT = `AUTO-TEST ${BASE}A%B`;
/** 名称无 `%`，仅在 `%` 被当通配符时会被 NAME_PCT 的关键词误命中 */
const NAME_PLAIN = `AUTO-TEST ${BASE}AB`;
/** 名称含字面 `_` */
const NAME_UND = `AUTO-TEST ${BASE}A_B`;
/** 名称无 `_`，仅在 `_` 被当通配符时会被 NAME_UND 的关键词误命中 */
const NAME_XB = `AUTO-TEST ${BASE}AXB`;

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let categoryId = 0;
const productIds: number[] = [];

/**
 * 创建上架商品（单规格，价格/库存固定）
 * - 函数名: createProduct
 * - 参数: name — 商品名（须含 BASE，保证关键词只命中本轮数据）
 * - 返回: Promise<number> — 商品 ID
 * - 异常: 创建/上架接口非 0 码时由 assertOk 抛出断言失败
 */
async function createProduct(name: string): Promise<number> {
  const created = assertOk(
    (await (
      await adminApi.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name,
          unit: '份',
          is_multi_spec: false,
          sku_items: [{ price: 6.6, stock: 30, safety_stock: 2 }],
        },
      })
    ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
    `创建商品「${name}」`,
  );
  const id = created.product?.id ?? created.id ?? 0;
  expect(id, `创建商品「${name}」未返回 ID`).toBeGreaterThan(0);
  productIds.push(id);

  const on = await adminApi.post(`/api/v1/admin/products/${id}/status`, { data: { status: 1 } });
  assertOk((await on.json()) as ApiResponse, `上架商品 ${id}`);
  return id;
}

/**
 * 管理端商品列表按关键词搜索
 * - 函数名: searchAdmin
 * - 参数: keyword — 搜索词
 * - 返回: Promise<ProductItem[]>
 */
async function searchAdmin(keyword: string): Promise<ProductItem[]> {
  const resp = await adminApi.get('/api/v1/admin/products', {
    params: { keyword, page: 1, page_size: 50 },
  });
  const body = (await resp.json()) as ApiResponse<PageData<ProductItem>>;
  expect(body.code, `管理端搜索「${keyword}」失败: ${body.message}`).toBe(0);
  return asList<ProductItem>(body.data);
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;

  const cat = assertOk(
    (await (
      await adminApi.post('/api/v1/admin/categories', { data: { name: `AUTO-TEST 转义分类 ${BASE}` } })
    ).json()) as ApiResponse<{ id: number }>,
    '创建分类',
  );
  categoryId = cat.id;

  await createProduct(NAME_PCT);
  await createProduct(NAME_PLAIN);
  await createProduct(NAME_UND);
  await createProduct(NAME_XB);
});

test.afterAll(async () => {
  try {
    for (const id of productIds) await adminApi?.delete(`/api/v1/admin/products/${id}`);
    if (categoryId) await adminApi?.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

test.describe.serial('LIKE 通配符转义', () => {
  test('1. 普通关键词不受转义影响（正向对照）', async () => {
    const list = await searchAdmin(BASE);
    expect(list.map((p) => p.name).sort(), '无通配符的关键词应命中全部 4 个自建商品').toEqual(
      [NAME_PCT, NAME_PLAIN, NAME_UND, NAME_XB].sort(),
    );
  });

  test('2. 关键词含 % 时按字面量匹配', async () => {
    const list = await searchAdmin(`${BASE}A%B`);
    expect(list.map((p) => p.name), '「A%B」只应命中名称真含 % 的商品（未转义时 AB 会一起命中）').toEqual([NAME_PCT]);
  });

  test('3. 关键词含 _ 时按字面量匹配', async () => {
    const list = await searchAdmin(`${BASE}A_B`);
    expect(list.map((p) => p.name), '「A_B」只应命中名称真含 _ 的商品（未转义时 AXB 会一起命中）').toEqual([NAME_UND]);
  });

  test('4. C 端搜索与后台同口径', async () => {
    const resp = await adminApi.get('/api/v1/products', {
      params: { keyword: `${BASE}A%B`, status: 1, page: 1, page_size: 50 },
    });
    const body = (await resp.json()) as ApiResponse<PageData<ProductItem>>;
    expect(body.code, `C 端搜索失败: ${body.message}`).toBe(0);
    expect(asList<ProductItem>(body.data).map((p) => p.name)).toEqual([NAME_PCT]);
  });

  test('5. 状态统计与列表同口径（Tab 计数不虚高）', async () => {
    const resp = await adminApi.get('/api/v1/admin/products/status-stats', {
      params: { keyword: `${BASE}A%B` },
    });
    const stats = assertOk(
      (await resp.json()) as ApiResponse<StatusStats>,
      '商品状态统计',
    );
    expect(stats.total, '统计的 total 应与列表命中数一致').toBe(1);
    expect(stats.on_sale, '命中的商品处于出售中').toBe(1);
  });

  test('6. SKU 搜索同口径', async () => {
    const resp = await adminApi.get('/api/v1/admin/inventory/skus', {
      params: { keyword: `${BASE}A%B`, page: 1, page_size: 50 },
    });
    const body = (await resp.json()) as ApiResponse<PageData<SkuItem>>;
    expect(body.code, `SKU 搜索失败: ${body.message}`).toBe(0);
    const list = asList<SkuItem>(body.data);
    expect(list.length, '应恰好命中 1 条 SKU').toBe(1);
    expect(list[0].product_id, '命中的 SKU 属于含 % 的商品').toBe(productIds[0]);
  });
});
