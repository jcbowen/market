import { test, expect } from '@playwright/test';
import { newAdminContext, asList, type ApiResponse, type PageData } from '../helpers/api';
import { autoName, uniqueSuffix } from '../helpers/data';

/**
 * 模块 40：C 端搜索与 SKU 详情
 * 覆盖：GET /products?keyword= 搜索命中/不命中、下架商品不进搜索、
 *       GET /skus?product_id= 列表、GET /skus/:id 详情、
 *       GET /products/:id 商品详情结构、不存在资源的业务码
 * 说明：自建分类+商品承载搜索断言（唯一后缀保证与其他数据隔离），afterAll 清理
 */

interface ProductItem {
  id: number;
  name: string;
  status?: number;
  [k: string]: unknown;
}

interface SkuItem {
  id: number;
  product_id: number;
  price?: number;
  stock?: number;
  status?: number;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
/** 管理端测试语境社区（C 端详情按社区归属校验，community_id 缺失即参数错误） */
let communityId = 0;
let productId = 0;
let skuId = 0;
let categoryId = 0;
/** 唯一搜索关键词（商品名尾缀，保证搜索结果只命中自建商品） */
const SEARCH_KEY = `sk${uniqueSuffix()}`;

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  communityId = admin.communityId;

  // 自建分类与商品（名称含唯一关键词），上架后取 SKU
  const cat = (await (
    await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('搜索测试分类') } })
  ).json()) as ApiResponse<{ id: number }>;
  expect(cat.code, `创建分类失败: ${cat.message}`).toBe(0);
  categoryId = cat.data!.id;

  const prod = (await (
    await adminApi.post('/api/v1/admin/products', {
      data: {
        category_id: categoryId,
        name: autoName(SEARCH_KEY),
        unit: '份',
        is_multi_spec: false,
        sku_items: [{ price: 3.9, stock: 100, safety_stock: 5 }],
      },
    })
  ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>;
  expect(prod.code, `创建商品失败: ${prod.message}`).toBe(0);
  productId = prod.data?.product?.id ?? prod.data?.id ?? 0;

  const on = (await (
    await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })
  ).json()) as ApiResponse;
  expect(on.code).toBe(0);

  const detail = (await (
    await adminApi.get(`/api/v1/admin/products/${productId}`)
  ).json()) as ApiResponse<{ skus: { id: number }[] }>;
  skuId = detail.data?.skus?.[0]?.id ?? 0;
  expect(skuId, 'SKU 未就绪').toBeGreaterThan(0);
});

test.afterAll(async () => {
  try {
    if (productId) await adminApi?.delete(`/api/v1/admin/products/${productId}`);
    if (categoryId) await adminApi?.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

test.describe.serial('C 端搜索', () => {
  test('1. keyword 搜索命中自建商品', async () => {
    const resp = await adminApi.get('/api/v1/products', { params: { keyword: SEARCH_KEY, status: 1, page: 1, page_size: 20 } });
    const body = (await resp.json()) as ApiResponse<PageData<ProductItem>>;
    expect(body.code).toBe(0);
    const list = asList<ProductItem>(body.data);
    expect(list.some((p) => p.id === productId), `搜索「${SEARCH_KEY}」应命中自建商品`).toBe(true);
  });

  test('2. 无关关键词返回空结果', async () => {
    const resp = await adminApi.get('/api/v1/products', {
      params: { keyword: `nonexist_${uniqueSuffix()}`, status: 1, page: 1, page_size: 20 },
    });
    const body = (await resp.json()) as ApiResponse<PageData<ProductItem>>;
    expect(body.code).toBe(0);
    expect(asList<ProductItem>(body.data)).toHaveLength(0);
  });

  test('3. 下架商品不进在售搜索结果', async () => {
    // 下架 → 搜索不可见 → 恢复上架（后续详情用例不受影响）
    const off = (await (
      await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 0 } })
    ).json()) as ApiResponse;
    expect(off.code, `下架失败: ${off.message}`).toBe(0);

    const resp = await adminApi.get('/api/v1/products', { params: { keyword: SEARCH_KEY, status: 1, page: 1, page_size: 20 } });
    const body = (await resp.json()) as ApiResponse<PageData<ProductItem>>;
    expect(body.code).toBe(0);
    expect(asList<ProductItem>(body.data).some((p) => p.id === productId)).toBe(false);

    const on = (await (
      await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })
    ).json()) as ApiResponse;
    expect(on.code).toBe(0);
  });
});

test.describe.serial('SKU 与商品详情', () => {
  test('4. product_id 查 SKU 列表', async () => {
    const resp = await adminApi.get('/api/v1/skus', { params: { product_id: productId } });
    const body = (await resp.json()) as ApiResponse<SkuItem[]>;
    expect(body.code).toBe(0);
    const list = asList<SkuItem>(body.data);
    expect(list.length).toBeGreaterThan(0);
    expect(list.every((s) => s.product_id === productId)).toBe(true);
    expect(list.some((s) => s.id === skuId)).toBe(true);
  });

  test('5. SKU 详情字段一致', async () => {
    const resp = await adminApi.get(`/api/v1/skus/${skuId}`);
    const body = (await resp.json()) as ApiResponse<SkuItem>;
    expect(body.code, `SKU 详情失败: ${body.message}`).toBe(0);
    const sku = body.data as SkuItem;
    expect(sku.id).toBe(skuId);
    expect(sku.product_id).toBe(productId);
    expect(sku.price).toBeGreaterThan(0);
  });

  test('6. 不存在的 SKU 返回业务错误', async () => {
    const resp = await adminApi.get('/api/v1/skus/999999999');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '不存在的 SKU 应返回非 0 业务码').not.toBe(0);
  });

  test('7. 商品详情包含 SKU 信息', async () => {
    // ProductDetail 结构：{ product: {...}, skus: [...], specs: [...] }
    // C 端详情按社区归属校验，必须带 community_id（该路由在公开组，令牌不产生语境）
    const resp = await adminApi.get(`/api/v1/products/${productId}`, { params: { community_id: communityId } });
    const body = (await resp.json()) as ApiResponse<{ product?: { id: number }; skus?: SkuItem[] }>;
    expect(body.code, `商品详情失败: ${body.message}`).toBe(0);
    const detail = body.data as { product?: { id: number }; skus?: SkuItem[] };
    expect(detail.product?.id).toBe(productId);
    const skus = detail.skus ?? [];
    expect(skus.some((s) => s.id === skuId), '商品详情应内嵌 SKU 列表').toBe(true);
  });

  test('8. 不存在的商品返回业务错误', async () => {
    const resp = await adminApi.get('/api/v1/products/999999999');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '不存在的商品应返回非 0 业务码').not.toBe(0);
  });
});
