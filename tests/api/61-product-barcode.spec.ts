import { test, expect } from '@playwright/test';
import { newAdminContext, type ApiResponse, type PageData } from '../helpers/api';
import type { APIRequestContext, Playwright } from '@playwright/test';

/**
 * 模块 61：商品条形码（2026-10-17）
 * 覆盖：SKU 级条码录入回读、admin/C 端条码精确查询（多规格定位到具体 SKU）、
 *       C 端关键词=条码搜索命中、跨商品/商品内重复条码拒绝、非数字格式拒绝、
 *       未命中与跨社区隔离、软删商品后条码回收复用
 *
 * 业务口径（与后端实现一致）：
 * - 条码为纯数字 6-20 位（宽松校验，不做 EAN 校验位强校验）
 * - 唯一性=同社区内在售（status=1）SKU 间唯一，软删商品（status=2）条码自动回收
 * - 条码查询仅命中上架商品（status=1）；未命中返回业务码 10003
 */

interface SkuBrief {
  id: number;
  product_id: number;
  barcode: string;
  stock: number;
  status: number;
}

interface ProductBrief {
  id: number;
  name: string;
}

const json = async <T>(resp: { json: () => Promise<unknown> }) => (await resp.json()) as ApiResponse<T>;

/** 条码 A：单规格商品使用 */
const BARCODE_A = `61${Date.now()}`.slice(-13).padStart(13, '6');
/** 条码 B/C：多规格商品两个 SKU 与回收复用用例使用 */
const BARCODE_B1 = `61${Date.now()}1`.slice(-13).padStart(13, '6');
const BARCODE_B2 = `61${Date.now()}2`.slice(-13).padStart(13, '6');

/** 用例专用分类 ID：beforeAll 在当前社区语境下建，商品必须挂同社区分类（跨社区引用被服务端拒绝） */
let categoryId = 0;

/** 单规格商品保存载荷（sku_items 全量提交） */
function singleSkuPayload(name: string, barcode: string) {
  return {
    name,
    category_id: categoryId,
    description: 'AUTO-TEST 条码用例',
    cover_image: '',
    images: [] as string[],
    unit: '件',
    is_multi_spec: false,
    min_buy: 1,
    sort: 0,
    show_sales: true,
    virtual_sales: 0,
    specs: [] as unknown[],
    sku_items: [
      { id: 0, spec_value_ids: [] as string[], sku_code: '', price: 9.9, original_price: 0, cost_price: 0, stock: 10, safety_stock: 0, image: '', barcode },
    ],
  };
}

test.describe.serial('商品条形码录入与使用', () => {
  let admin: Awaited<ReturnType<typeof newAdminContext>>;
  let publicApi: APIRequestContext;
  let productAId = 0;
  let skuAId = 0;
  const createdProductIds: number[] = [];
  let otherCommunityId = 0;

  test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
    admin = await newAdminContext(playwright);
    publicApi = await playwright.request.newContext();

    // 找一个非当前语境的营业中社区（跨社区隔离用例用）
    const comms = await json<PageData<{ id: number; status: number }>>(
      await publicApi.get('/api/v1/communities', { params: { page: 1, page_size: 50 } }),
    );
    otherCommunityId = (comms.data?.list ?? []).find((c) => c.id !== admin.communityId && c.status === 1)?.id ?? 0;

    // 商品挂分类必须是同社区引用，故先在当前语境建一个用例专用分类，结束后删除
    categoryId = (await json<{ id: number }>(
      await admin.api.post('/api/v1/admin/categories', { data: { name: `AUTO-条码分类 ${Date.now()}` } }),
    )).data!.id;
  });

  test.afterAll(async () => {
    // 清理：软删本用例创建的全部商品
    for (const id of createdProductIds) {
      await admin?.api.delete(`/api/v1/admin/products/${id}`);
    }
    if (categoryId) await admin?.api.delete(`/api/v1/admin/categories/${categoryId}`);
    await admin?.api.dispose();
    await publicApi?.dispose();
  });

  test('创建带条码的单规格商品并回读', async () => {
    const resp = await json<{ product: ProductBrief; skus: SkuBrief[] }>(
      await admin.api.post('/api/v1/admin/products', { data: singleSkuPayload('AUTO-条码商品A', BARCODE_A) }),
    );
    expect(resp.code).toBe(0);
    productAId = resp.data!.product.id;
    createdProductIds.push(productAId);
    const sku = resp.data!.skus.find((s) => s.barcode === BARCODE_A);
    expect(sku).toBeTruthy();
    skuAId = sku!.id;

    // 详情回读（编辑页回填口径）
    const detail = await json<{ skus: SkuBrief[] }>(
      await admin.api.get(`/api/v1/admin/products/${productAId}`),
    );
    expect(detail.code).toBe(0);
    expect(detail.data!.skus.some((s) => s.barcode === BARCODE_A)).toBe(true);
  });

  test('管理端条码精确查询命中商品与 SKU', async () => {
    const resp = await json<{ product: ProductBrief; sku: SkuBrief }>(
      await admin.api.get(`/api/v1/admin/products/barcode/${BARCODE_A}`),
    );
    expect(resp.code).toBe(0);
    expect(resp.data!.product.id).toBe(productAId);
    expect(resp.data!.sku.id).toBe(skuAId);
    expect(resp.data!.sku.barcode).toBe(BARCODE_A);
  });

  test('C 端公开条码查询命中（community_id 参数）', async () => {
    const resp = await json<{ product: ProductBrief; sku: SkuBrief }>(
      await publicApi.get('/api/v1/products/barcode/' + BARCODE_A, {
        params: { community_id: admin.communityId },
      }),
    );
    expect(resp.code).toBe(0);
    expect(resp.data!.product.id).toBe(productAId);
    expect(resp.data!.sku.id).toBe(skuAId);
  });

  test('C 端关键词=条码搜索命中（name OR barcode 双通道）', async () => {
    const resp = await json<PageData<ProductBrief>>(
      await publicApi.get('/api/v1/products', {
        params: { keyword: BARCODE_A, community_id: admin.communityId, status: 1 },
      }),
    );
    expect(resp.code).toBe(0);
    expect((resp.data?.list ?? []).some((p) => p.id === productAId)).toBe(true);
  });

  test('跨商品重复条码被拒绝并提示占用商品', async () => {
    const resp = await json(
      await admin.api.post('/api/v1/admin/products', {
        data: singleSkuPayload('AUTO-条码商品-重复', BARCODE_A),
      }),
    );
    expect(resp.code).toBe(10001);
    expect(resp.message).toContain('已被商品');
  });

  test('商品内条码重复被拒绝（多规格两 SKU 同码）', async () => {
    const resp = await json(
      await admin.api.post('/api/v1/admin/products', {
        data: {
          name: 'AUTO-条码商品-内重复',
          category_id: categoryId,
          description: '',
          cover_image: '',
          images: [],
          unit: '件',
          is_multi_spec: true,
          min_buy: 1,
          sort: 0,
          show_sales: true,
          virtual_sales: 0,
          specs: [
            {
              id: 0,
              temp_id: 's1',
              name: '规格',
              sort: 0,
              values: [
                { id: 0, temp_id: 'v1', name: '大', sort: 0 },
                { id: 0, temp_id: 'v2', name: '小', sort: 1 },
              ],
            },
          ],
          sku_items: [
            { id: 0, spec_value_ids: ['v1'], sku_code: '', price: 10, original_price: 0, cost_price: 0, stock: 5, safety_stock: 0, image: '', barcode: BARCODE_B1 },
            { id: 0, spec_value_ids: ['v2'], sku_code: '', price: 8, original_price: 0, cost_price: 0, stock: 5, safety_stock: 0, image: '', barcode: BARCODE_B1 },
          ],
        },
      }),
    );
    expect(resp.code).toBe(10001);
    expect(resp.message).toContain('重复');
  });

  test('非数字条码被拒绝（宽松格式校验）', async () => {
    const resp = await json(
      await admin.api.post('/api/v1/admin/products', {
        data: singleSkuPayload('AUTO-条码商品-格式错误', 'ABC123'),
      }),
    );
    expect(resp.code).toBe(10001);
    expect(resp.message).toContain('数字');
  });

  test('创建多规格商品：两 SKU 各自条码，查询精确定位', async () => {
    const resp = await json<{ product: ProductBrief; skus: SkuBrief[] }>(
      await admin.api.post('/api/v1/admin/products', {
        data: {
          name: 'AUTO-条码商品B（多规格）',
          category_id: categoryId,
          description: '',
          cover_image: '',
          images: [],
          unit: '件',
          is_multi_spec: true,
          min_buy: 1,
          sort: 0,
          show_sales: true,
          virtual_sales: 0,
          specs: [
            {
              id: 0,
              temp_id: 's1',
              name: '规格',
              sort: 0,
              values: [
                { id: 0, temp_id: 'v1', name: '大', sort: 0 },
                { id: 0, temp_id: 'v2', name: '小', sort: 1 },
              ],
            },
          ],
          sku_items: [
            { id: 0, spec_value_ids: ['v1'], sku_code: '', price: 10, original_price: 0, cost_price: 0, stock: 5, safety_stock: 0, image: '', barcode: BARCODE_B1 },
            { id: 0, spec_value_ids: ['v2'], sku_code: '', price: 8, original_price: 0, cost_price: 0, stock: 5, safety_stock: 0, image: '', barcode: BARCODE_B2 },
          ],
        },
      }),
    );
    expect(resp.code).toBe(0);
    const productBId = resp.data!.product.id;
    createdProductIds.push(productBId);

    // 条码 B2 应精确定位到「小」规格 SKU（而非仅返回商品）
    const lookup = await json<{ product: ProductBrief; sku: SkuBrief }>(
      await publicApi.get('/api/v1/products/barcode/' + BARCODE_B2, {
        params: { community_id: admin.communityId },
      }),
    );
    expect(lookup.code).toBe(0);
    expect(lookup.data!.product.id).toBe(productBId);
    const small = resp.data!.skus.find((s) => s.barcode === BARCODE_B2);
    expect(lookup.data!.sku.id).toBe(small!.id);
  });

  test('未命中条码返回 10003 业务码', async () => {
    const resp = await json(
      await publicApi.get('/api/v1/products/barcode/1111111111111', {
        params: { community_id: admin.communityId },
      }),
    );
    expect(resp.code).toBe(10003);
    expect(resp.message).toContain('未找到');
  });

  test('跨社区隔离：其他社区查不到本社区条码', async () => {
    test.skip(!otherCommunityId, '环境不满足：无第二个营业社区');
    const resp = await json(
      await publicApi.get('/api/v1/products/barcode/' + BARCODE_A, {
        params: { community_id: otherCommunityId },
      }),
    );
    expect(resp.code).toBe(10003);
  });

  test('软删商品后条码回收可复用', async () => {
    const del = await json(await admin.api.delete(`/api/v1/admin/products/${productAId}`));
    expect(del.code).toBe(0);

    // 同社区新商品复用同一条码应成功
    const resp = await json<{ product: ProductBrief }>(
      await admin.api.post('/api/v1/admin/products', {
        data: singleSkuPayload('AUTO-条码商品C（复用回收条码）', BARCODE_A),
      }),
    );
    expect(resp.code).toBe(0);
    createdProductIds.push(resp.data!.product.id);
  });
});
