import { test, expect } from '@playwright/test';
import { adminLogin, newAuthedRequest, asList, type ApiResponse, type PageData } from '../helpers/api';
import { autoName } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 09：管理端分类与商品
 * 覆盖：分类 CRUD、单规格商品创建、列表/详情、更新、上下架、删除、参数校验
 * 注意：创建的数据带 AUTO-TEST 前缀，测试通过接口自清理（删除）
 */

let api: Awaited<ReturnType<typeof newAuthedRequest>>;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await adminLogin(playwright.request);
  api = await newAuthedRequest(playwright, info.access_token);
});

test.afterAll(async () => {
  await api?.dispose();
});

interface Category {
  id: number;
  name: string;
}

interface Product {
  product?: { id: number; name: string; status?: number };
  skus?: { id: number; stock?: number; price?: number }[];
  id?: number;
  name?: string;
  status?: number;
}

test.describe.serial('分类管理', () => {
  let catId = 0;

  test('创建分类成功', async () => {
    const resp = await api.post('/api/v1/admin/categories', { data: { name: autoName('测试分类'), sort: 99 } });
    const body = (await resp.json()) as ApiResponse<Category>;
    expect(body.code).toBe(0);
    catId = body.data?.id ?? 0;
    expect(catId).toBeGreaterThan(0);
  });

  test('分类列表包含新分类', async () => {
    const resp = await api.get('/api/v1/admin/categories');
    const body = (await resp.json()) as ApiResponse<PageData<Category>>;
    expect(body.code).toBe(0);
    expect(asList<Category>(body.data).some((c) => c.id === catId)).toBe(true);
  });

  test('更新分类名称生效', async () => {
    // PUT 为全量覆盖语义：status 必传（指针区分未传与传 0），只发改名会判参数错误
    const resp = await api.put(`/api/v1/admin/categories/${catId}`, {
      data: { name: autoName('改名分类'), sort: 99, status: 1 },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('空名称分类被拒绝', async () => {
    const resp = await api.post('/api/v1/admin/categories', { data: { name: '' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });

  test('删除分类成功', async () => {
    const resp = await api.delete(`/api/v1/admin/categories/${catId}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });
});

test.describe.serial('商品管理', () => {
  let catId = 0;
  let productId = 0;
  let productName = '';

  test.beforeAll(async () => {
    // 商品组自备一个专用分类（用后删除）
    const resp = await api.post('/api/v1/admin/categories', { data: { name: autoName('商品组分类'), sort: 98 } });
    const body = (await resp.json()) as ApiResponse<Category>;
    catId = body.data!.id;
  });

  test.afterAll(async () => {
    // 清理商品与分类
    if (productId) await api.delete(`/api/v1/admin/products/${productId}`);
    if (catId) await api.delete(`/api/v1/admin/categories/${catId}`);
  });

  test('创建单规格商品成功', async () => {
    productName = autoName('测试商品');
    const resp = await api.post('/api/v1/admin/products', {
      data: {
        category_id: catId,
        name: productName,
        description: '自动化测试创建的商品',
        unit: '份',
        is_multi_spec: false,
        sku_items: [{ price: 9.9, cost_price: 5, stock: 100, safety_stock: 10 }],
      },
    });
    const body = (await resp.json()) as ApiResponse<Product>;
    expect(body.code).toBe(0);
    // 响应结构：{ product: {...}, skus: [...] }
    productId = body.data?.product?.id ?? body.data?.id ?? 0;
    expect(productId).toBeGreaterThan(0);
  });

  test('商品详情含 SKU 与库存', async () => {
    const resp = await api.get(`/api/v1/admin/products/${productId}`);
    const body = (await resp.json()) as ApiResponse<Product>;
    expect(body.code).toBe(0);
    expect((body.data?.skus ?? body.data?.product?.skus)?.length).toBeGreaterThan(0);
    expect((body.data?.skus ?? body.data?.product?.skus)?.[0].stock).toBe(100);
  });

  test('管理端商品列表可检索新商品', async () => {
    // 列表缺省按 sort 降序、id 降序，新建商品 sort=0 未必落在首页，改用 keyword 定向检索
    const resp = await api.get('/api/v1/admin/products', { params: { page: 1, page_size: 50, keyword: productName } });
    const body = (await resp.json()) as ApiResponse<PageData<Product>>;
    expect(body.code).toBe(0);
    expect(asList<Product>(body.data).some((p) => p.id === productId)).toBe(true);
  });

  test('更新商品名称生效', async () => {
    productName = autoName('改名商品');
    const resp = await api.put(`/api/v1/admin/products/${productId}`, {
      data: { category_id: catId, name: productName, is_multi_spec: false, sku_items: [{ price: 12.5, stock: 80 }] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
    const detail = (await (await api.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<Product>;
    expect(detail.data?.product?.name ?? detail.data?.name).toBe(productName);
  });

  test('表单调整库存落流水(ref_no=FORM)', async () => {
    // 以详情当前库存为基准调低 20，不依赖前序用例的库存值
    const beforeDetail = (await (await api.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<Product>;
    const skuId = (beforeDetail.data?.skus ?? beforeDetail.data?.product?.skus)?.[0]?.id ?? 0;
    const beforeStock = (beforeDetail.data?.skus ?? beforeDetail.data?.product?.skus)?.[0]?.stock;
    test.skip(!skuId || beforeStock === undefined || beforeStock < 20, '环境无 SKU 或库存不足 20，跳过表单调库存用例');

    const afterStock = beforeStock! - 20;
    productName = autoName('调库存商品');
    const resp = await api.put(`/api/v1/admin/products/${productId}`, {
      data: { category_id: catId, name: productName, is_multi_spec: false, sku_items: [{ price: 12.5, stock: afterStock }] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `表单调库存失败: ${body.message}`).toBe(0);

    // 表单调低库存须同步落流水（ref_no=FORM），保证进销存报表口径完整
    const records = (await (
      await api.get('/api/v1/admin/inventory/entries', { params: { sku_id: skuId, page: 1, page_size: 20 } })
    ).json()) as ApiResponse<PageData<{ ref_no?: string; type?: number; quantity?: number; before_stock?: number; after_stock?: number }>>;
    expect(records.code).toBe(0);
    const form = (records.data?.list ?? []).find((r) => r.ref_no === 'FORM');
    expect(form, '未查到 ref_no=FORM 的表单调整流水').toBeTruthy();
    expect(form!.type, '表单调低库存流水类型应为 2(出库)').toBe(2);
    expect(form!.quantity).toBe(-20);
    expect(form!.before_stock).toBe(beforeStock);
    expect(form!.after_stock).toBe(afterStock);
  });

  test('商品上架状态设置成功', async () => {
    const on = (await (
      await api.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })
    ).json()) as ApiResponse;
    expect(on.code).toBe(0);
  });

  test('缺 status 的状态更新被拒绝', async () => {
    const resp = await api.post(`/api/v1/admin/products/${productId}/status`, { data: {} });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });

  test('C 端商品列表可见已上架商品', async () => {
    // 同列表检索口径：按 keyword 定向命中，避免新建商品 sort=0 排在演示商品之后
    const resp = await api.get('/api/v1/products', { params: { page: 1, page_size: 50, status: 1, keyword: productName } });
    const body = (await resp.json()) as ApiResponse<PageData<Product>>;
    expect(body.code).toBe(0);
    expect(body.data?.list.some((p) => p.id === productId)).toBe(true);
  });

  // 缺陷③已修复（2026-10-16）：UpdateProductStatusReq.Status 改为 *int32 指针校验，
  // 零值 0（下架）不再被 binding:"required" 误拒
  test('商品下架 status=0 设置成功', async () => {
    const off = (await (
      await api.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 0 } })
    ).json()) as ApiResponse;
    expect(off.code, `下架失败: ${off.message}`).toBe(0);

    // 下架后 C 端列表不可见（缺陷④已修复：网关 ListProducts 透传 status 参数）
    const list = (await (
      await api.get('/api/v1/products', { params: { page: 1, page_size: 50, status: 1, keyword: productName } })
    ).json()) as ApiResponse<PageData<Product>>;
    expect(list.code).toBe(0);
    expect(list.data?.list.some((p) => p.id === productId)).toBe(false);
  });

  test('缺 category_id 的商品创建被拒绝', async () => {
    const resp = await api.post('/api/v1/admin/products', { data: { name: autoName('坏商品') } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });
});
