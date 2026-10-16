import { test, expect } from '@playwright/test';
import { newAdminContext, assertOk, asList, type ApiResponse, type PageData } from '../helpers/api';
import { autoName, uniqueSuffix } from '../helpers/data';

/**
 * 模块 48：C 端商品筛选/排序 + 多级分类
 * 覆盖：
 *   - category_id 筛选：命中子分类商品；父分类（无直挂商品）返回空
 *   - 排序（docs/需求文档/06-接口设计/02-C端接口.md sort 参数）：
 *     sales=销量降序（真实+虚拟）、price_asc/price_desc=价格升/降序、缺省按后台 sort 值降序
 *   - 多级分类：parent_id 创建子分类、C 端分类列表返回层级字段、
 *     有子分类的父分类删除被拒
 * 说明：自建父/子分类与 3 个价格/销量梯度商品隔离数据，afterAll 逆序清理
 */

interface ProductItem {
  id: number;
  name: string;
  min_price?: number;
  sales_count?: number;
  virtual_sales?: number;
  [k: string]: unknown;
}

interface CategoryItem {
  id: number;
  parent_id: number;
  name: string;
  children?: unknown[];
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let parentCategoryId = 0;
let childCategoryId = 0;
/** 三个梯度商品：价 2/5/8，虚拟销量 1/50/5，后台 sort 1/5/10 */
const productIds: number[] = [];

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;

  // 父分类
  const parent = (await (
    await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('父分类') } })
  ).json()) as ApiResponse<{ id: number; parent_id?: number }>;
  expect(parent.code, `创建父分类失败: ${parent.message}`).toBe(0);
  parentCategoryId = parent.data!.id;

  // 子分类（parent_id 层级）
  const child = (await (
    await adminApi.post('/api/v1/admin/categories', {
      data: { name: autoName('子分类'), parent_id: parentCategoryId },
    })
  ).json()) as ApiResponse<{ id: number; parent_id: number }>;
  expect(child.code, `创建子分类失败: ${child.message}`).toBe(0);
  childCategoryId = child.data!.id;
  expect(child.data!.parent_id, '子分类应落 parent_id').toBe(parentCategoryId);

  // 三个梯度商品挂子分类：价 2/5/8，虚拟销量 1/50/5，后台 sort 1/5/10
  const prices = [2, 5, 8];
  const virtualSales = [1, 50, 5];
  const sorts = [1, 5, 10];
  for (let i = 0; i < 3; i++) {
    const prod = (await (
      await adminApi.post('/api/v1/admin/products', {
        data: {
          category_id: childCategoryId,
          name: autoName(`梯度商品${i}`),
          unit: '份',
          is_multi_spec: false,
          sort: sorts[i],
          virtual_sales: virtualSales[i],
          show_sales: true,
          sku_items: [{ price: prices[i], stock: 50, safety_stock: 5 }],
        },
      })
    ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>;
    expect(prod.code, `创建商品${i}失败: ${prod.message}`).toBe(0);
    const pid = prod.data?.product?.id ?? prod.data?.id ?? 0;
    expect(pid).toBeGreaterThan(0);
    productIds.push(pid);
    const on = (await (
      await adminApi.post(`/api/v1/admin/products/${pid}/status`, { data: { status: 1 } })
    ).json()) as ApiResponse;
    expect(on.code).toBe(0);
  }
});

test.afterAll(async () => {
  try {
    for (const pid of productIds) await adminApi?.delete(`/api/v1/admin/products/${pid}`);
    // 逆序删除：先子分类再父分类（有子分类的父分类删除会被拒）
    if (childCategoryId) await adminApi?.delete(`/api/v1/admin/categories/${childCategoryId}`);
    if (parentCategoryId) await adminApi?.delete(`/api/v1/admin/categories/${parentCategoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

/** C 端商品列表（带筛选/排序参数），返回自建商品子集（按入参顺序） */
async function listClientProducts(params: Record<string, string | number>): Promise<ProductItem[]> {
  const resp = await adminApi.get('/api/v1/products', { params: { status: 1, page: 1, page_size: 20, ...params } });
  const body = (await resp.json()) as ApiResponse<PageData<ProductItem>>;
  expect(body.code, `商品列表失败: ${body.message}`).toBe(0);
  const all = asList<ProductItem>(body.data);
  return all.filter((p) => productIds.includes(p.id));
}

test.describe.serial('C 端筛选与排序', () => {
  test('1. category_id 筛选命中子分类商品', async () => {
    const list = await listClientProducts({ category_id: childCategoryId });
    expect(list, '子分类应命中全部 3 个自建商品').toHaveLength(3);
  });

  test('2. 父分类（无直挂商品）返回空', async () => {
    const list = await listClientProducts({ category_id: parentCategoryId });
    expect(list, '商品挂在子分类，父分类筛选应为空').toHaveLength(0);
  });

  test('3. 缺省排序按后台 sort 值降序', async () => {
    const list = await listClientProducts({ category_id: childCategoryId });
    expect(list.map((p) => p.id), '缺省应按 sort 10/5/1 降序（创建序号的逆序）').toEqual(
      [...productIds].reverse(),
    );
  });

  test('4. sort=sales 按销量降序（真实+虚拟）', async () => {
    const list = await listClientProducts({ category_id: childCategoryId, sort: 'sales' });
    expect(list.map((p) => p.id), '销量 50/5/1 应对应商品序号 1/2/0').toEqual([
      productIds[1],
      productIds[2],
      productIds[0],
    ]);
  });

  test('5. sort=price_asc 价格升序', async () => {
    const list = await listClientProducts({ category_id: childCategoryId, sort: 'price_asc' });
    expect(list.map((p) => p.id), '价格 2/5/8 升序').toEqual(productIds);
  });

  test('6. sort=price_desc 价格降序', async () => {
    const list = await listClientProducts({ category_id: childCategoryId, sort: 'price_desc' });
    expect(list.map((p) => p.id), '价格 8/5/2 降序').toEqual([...productIds].reverse());
  });

  test('7. 非法 sort 值回退缺省排序不报错', async () => {
    const list = await listClientProducts({ category_id: childCategoryId, sort: 'magic_sort' });
    expect(list.map((p) => p.id), '未知 sort 应回退缺省排序').toEqual([...productIds].reverse());
  });
});

test.describe.serial('多级分类', () => {
  test('8. C 端分类列表树形层级（子分类嵌于父分类 children）', async () => {
    const resp = await adminApi.get('/api/v1/categories');
    const body = (await resp.json()) as ApiResponse<CategoryItem[] | { list: CategoryItem[] }>;
    expect(body.code, `分类列表失败: ${body.message}`).toBe(0);
    const all = asList<CategoryItem>(body.data);
    const parent = all.find((c) => c.id === parentCategoryId);
    expect(parent, '父分类应在顶层').toBeTruthy();
    expect(parent!.parent_id, '父分类 parent_id 应为 0').toBe(0);
    // C 端列表为树形结构：子分类嵌套在父分类 children 中
    const children = (parent!.children ?? []) as CategoryItem[];
    const child = children.find((c) => c.id === childCategoryId);
    expect(child, '子分类应嵌套在父分类 children 中').toBeTruthy();
    expect(child!.parent_id).toBe(parentCategoryId);
  });

  test('9. 有子分类的父分类删除被拒', async () => {
    const resp = await adminApi.delete(`/api/v1/admin/categories/${parentCategoryId}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '存在子分类时父分类不应可删除').not.toBe(0);
    // 被拒后分类仍存在
    const list = (await (
      await adminApi.get('/api/v1/categories')
    ).json()) as ApiResponse<CategoryItem[] | { list: CategoryItem[] }>;
    expect(
      asList<CategoryItem>(list.data).some((c) => c.id === parentCategoryId),
      '删除被拒后父分类应仍存在',
    ).toBe(true);
  });
});
