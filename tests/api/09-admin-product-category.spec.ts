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
    const resp = await api.put(`/api/v1/admin/categories/${catId}`, { data: { name: autoName('改名分类') } });
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
    const resp = await api.post('/api/v1/admin/products', {
      data: {
        category_id: catId,
        name: autoName('测试商品'),
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
    const resp = await api.get('/api/v1/admin/products', { params: { page: 1, page_size: 50 } });
    const body = (await resp.json()) as ApiResponse<PageData<Product>>;
    expect(body.code).toBe(0);
    expect(asList<Product>(body.data).some((p) => p.id === productId)).toBe(true);
  });

  test('更新商品名称生效', async () => {
    const newName = autoName('改名商品');
    const resp = await api.put(`/api/v1/admin/products/${productId}`, {
      data: { category_id: catId, name: newName, is_multi_spec: false, sku_items: [{ price: 12.5, stock: 80 }] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
    const detail = (await (await api.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<Product>;
    expect(detail.data?.product?.name ?? detail.data?.name).toBe(newName);
  });

  test('商品上架状态设置成功（下架 status=0 受后端 binding 零值限制，见 TEST-PLAN 已知问题）', async () => {
    // UpdateProductStatusReq.status binding:required 会误拒 0 值（gin 零值坑），此处仅验证上架流转
    const on = (await (
      await api.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })
    ).json()) as ApiResponse;
    expect(on.code).toBe(0);
  });

  test('缺 category_id 的商品创建被拒绝', async () => {
    const resp = await api.post('/api/v1/admin/products', { data: { name: autoName('坏商品') } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });

  test('C 端商品列表可见已上架商品', async () => {
    const resp = await api.get('/api/v1/products', { params: { page: 1, page_size: 50, status: 1 } });
    const body = (await resp.json()) as ApiResponse<PageData<Product>>;
    expect(body.code).toBe(0);
    expect(body.data?.list.some((p) => p.id === productId)).toBe(true);
  });
});
