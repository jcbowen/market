import { test, expect } from '@playwright/test';
import { asList, assertOk, newAdminContext, type ApiResponse, type PageData } from '../helpers/api';
import { autoName } from '../helpers/data';

/**
 * 模块 84：公开 SKU 出口按商品上架态隐藏价目
 * 背景：GET /api/v1/skus?product_id= 与 GET /api/v1/skus/:id 挂在公开路由组、不经 auth，
 * 任何访问者都能按 ID 遍历。这两个出口此前只抹成本价与安全库存，价格与库存一律放行，
 * 等于绕过商品列表（status=1 过滤）与详情出口（下架即抹价目）读走下架/定时上架商品的价目与库存。
 * 现与详情出口同口径：商品非上架态时 price/original_price/stock 归零，名称等展示字段保留；
 * 管理端出口（PublicView=false）不受影响，仍拿到真实价目。
 */

interface SkuItem {
  id: number;
  product_id: number;
  name?: string;
  price: number;
  original_price?: number;
  stock: number;
  cost_price?: number;
  safety_stock?: number;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let categoryId = 0;
let productId = 0;
let skuId = 0;

/**
 * 查询公开 SKU 列表
 * - 函数名: publicSkus
 * - 返回: Promise<SkuItem[]>
 * - 异常: 接口非 0 码时断言失败
 */
async function publicSkus(): Promise<SkuItem[]> {
  const body = (await (
    await adminApi.get('/api/v1/skus', { params: { product_id: productId } })
  ).json()) as ApiResponse<PageData<SkuItem>>;
  expect(body.code, `公开 SKU 列表失败: ${body.message}`).toBe(0);
  return asList<SkuItem>(body.data);
}

/**
 * 查询公开 SKU 详情
 * - 函数名: publicSku
 * - 返回: Promise<SkuItem>
 */
async function publicSku(): Promise<SkuItem> {
  const body = (await (
    await adminApi.get(`/api/v1/skus/${skuId}`)
  ).json()) as ApiResponse<SkuItem>;
  expect(body.code, `公开 SKU 详情失败: ${body.message}`).toBe(0);
  return body.data as SkuItem;
}

/**
 * 切换商品上下架
 * - 函数名: setStatus
 * - 参数: status — 0 下架 / 1 上架
 */
async function setStatus(status: number): Promise<void> {
  const body = (await (
    await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status } })
  ).json()) as ApiResponse;
  expect(body.code, `切换商品状态为 ${status} 失败: ${body.message}`).toBe(0);
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;

  const cat = assertOk(
    (await (
      await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('公开SKU脱敏分类') } })
    ).json()) as ApiResponse<{ id: number }>,
    '创建分类',
  );
  categoryId = cat.id;

  const created = assertOk(
    (await (
      await adminApi.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name: autoName('公开SKU脱敏商品'),
          unit: '份',
          is_multi_spec: false,
          sku_items: [{ price: 8.8, stock: 40, safety_stock: 3 }],
        },
      })
    ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
    '创建商品',
  );
  productId = created.product?.id ?? created.id ?? 0;
  expect(productId).toBeGreaterThan(0);
  await setStatus(1);

  // 管理端详情取 SKU（公开出口按上架态脱敏，造数阶段用管理端拿真实 ID）
  const detail = assertOk(
    (await (
      await adminApi.get(`/api/v1/admin/products/${productId}`)
    ).json()) as ApiResponse<{ skus: SkuItem[] }>,
    '商品详情',
  );
  skuId = detail.skus?.[0]?.id ?? 0;
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

test.describe.serial('公开 SKU 出口脱敏', () => {
  test('1. 上架态：价格与库存照常下发（C 端选规格要用）', async () => {
    const list = await publicSkus();
    expect(list.length).toBeGreaterThan(0);
    expect(list[0].price, '在售商品价格应可见').toBeGreaterThan(0);
    expect(list[0].stock, '在售商品库存应可见').toBeGreaterThan(0);
    expect(list[0].cost_price ?? 0, '成本价属经营数据，任何出口都不下发').toBe(0);
    expect(list[0].safety_stock ?? 0, '安全库存属经营数据，任何出口都不下发').toBe(0);

    const one = await publicSku();
    expect(one.price).toBeGreaterThan(0);
  });

  test('2. 下架后：公开列表抹掉价目与库存', async () => {
    await setStatus(0);
    const list = await publicSkus();
    expect(list.length, 'SKU 行仍应返回（页面据此提示已下架）').toBeGreaterThan(0);
    expect(list[0].price, '下架商品价格不得经公开列表读出').toBe(0);
    expect(list[0].stock, '下架商品库存不得经公开列表读出').toBe(0);
    expect(list[0].id, 'ID 等标识字段保留').toBe(skuId);
  });

  test('3. 下架后：公开详情同样抹掉价目', async () => {
    const one = await publicSku();
    expect(one.price, '下架商品价格不得经公开详情读出').toBe(0);
    expect(one.stock, '下架商品库存不得经公开详情读出').toBe(0);
    expect(one.product_id, '归属商品仍回显').toBe(productId);
  });

  test('4. 管理端出口不受影响：仍拿到真实价目', async () => {
    const detail = assertOk(
      (await (
        await adminApi.get(`/api/v1/admin/products/${productId}`)
      ).json()) as ApiResponse<{ skus: SkuItem[] }>,
      '管理端商品详情',
    );
    const sku = detail.skus.find((s) => s.id === skuId);
    expect(sku?.price, '后台编辑页必须拿得到真实价格').toBe(8.8);
    expect(sku?.stock, '后台编辑页必须拿得到真实库存').toBe(40);
  });

  test('5. 重新上架后价目恢复可见', async () => {
    await setStatus(1);
    const list = await publicSkus();
    expect(list[0].price, '上架后价格恢复').toBe(8.8);
    expect(list[0].stock, '上架后库存恢复').toBe(40);
  });
});
