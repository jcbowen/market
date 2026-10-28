import { test, expect } from '@playwright/test';
import { assertOk, newAdminContext, type ApiResponse } from '../helpers/api';
import { autoName } from '../helpers/data';

/**
 * 模块 82：商品编辑接口三态字段（sort / on_sale_at）
 * 背景：两者原为 proto3 值类型，「不传」与「传零值」无法区分——
 *   - sort：`if req.Sort > 0` 才赋值，排序永远回不到 0；
 *   - on_sale_at：`else` 分支恒清除，任何一次不含该字段的编辑都会顺手取消定时上架预约。
 * 现改为 proto3 optional（网关与服务侧均为指针），三态口径：
 *   - 不传 = 维持原值；
 *   - 传 0 / 传空串 = 显式写入（sort 置 0、取消预约）。
 * 后台编辑表单始终全量提交这两个字段，故后台行为不变；本模块用 HTTP 显式省略字段来验证接口契约。
 * 注：编辑接口按全量口径要求携带 sku_items（缺失即「请填写商品价格与库存」），
 *     故每次 PUT 都回传当前 SKU 的原值，只让被测字段变化。
 */

/** 未来时间（定时上架用），格式与后端校验口径一致 */
function futureTime(minutes = 30): string {
  const d = new Date(Date.now() + minutes * 60 * 1000);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

interface ProductSku {
  id: number;
  price: number;
  stock: number;
  safety_stock?: number;
  status?: number;
}

interface ProductDetail {
  product: { name: string; sort: number; status: number; on_sale_at?: string; [k: string]: unknown };
  skus: ProductSku[];
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let categoryId = 0;
let productId = 0;

/**
 * 读取商品详情（管理端出口，字段口径与编辑入参一致）
 * - 函数名: getDetail
 * - 返回: Promise<ProductDetail>
 * - 异常: 接口非 0 码时由 assertOk 抛出断言失败
 */
async function getDetail(): Promise<ProductDetail> {
  return assertOk(
    (await (await adminApi.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<ProductDetail>,
    '商品详情',
  );
}

/**
 * 更新商品并回读详情：自动附带当前 SKU 原值（编辑接口要求全量提交规格）
 * - 函数名: putProduct
 * - 参数: data — 被测编辑入参（刻意省略的字段即验证「不传=不改动」）
 * - 返回: Promise<ProductDetail> — 更新后的详情
 * - 异常: 更新接口非 0 码时断言失败
 */
async function putProduct(data: Record<string, unknown>): Promise<ProductDetail> {
  const before = await getDetail();
  const payload = {
    is_multi_spec: false,
    sku_items: before.skus.map((s) => ({
      id: s.id,
      price: s.price,
      stock: s.stock,
      safety_stock: s.safety_stock ?? 0,
    })),
    ...data,
  };
  const resp = (await (
    await adminApi.put(`/api/v1/admin/products/${productId}`, { data: payload })
  ).json()) as ApiResponse;
  expect(resp.code, `更新商品失败(${JSON.stringify(data)}): ${resp.message}`).toBe(0);
  return getDetail();
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;

  const cat = assertOk(
    (await (
      await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('三态分类') } })
    ).json()) as ApiResponse<{ id: number }>,
    '创建分类',
  );
  categoryId = cat.id;

  const created = assertOk(
    (await (
      await adminApi.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name: autoName('三态商品'),
          unit: '份',
          sort: 5,
          is_multi_spec: false,
          sku_items: [{ price: 9.9, stock: 20, safety_stock: 1 }],
        },
      })
    ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
    '创建商品',
  );
  productId = created.product?.id ?? created.id ?? 0;
  expect(productId).toBeGreaterThan(0);
});

test.afterAll(async () => {
  try {
    if (productId) await adminApi.delete(`/api/v1/admin/products/${productId}`);
    if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

test.describe.serial('编辑接口三态字段', () => {
  test('1. 基线：新建商品按提交值带上排序 5', async () => {
    const detail = await getDetail();
    expect(detail.product.sort, '创建时应写入 sort=5').toBe(5);
  });

  test('2. 传 0 能把排序置回 0（旧实现回不到 0）', async () => {
    const detail = await putProduct({ sort: 0 });
    expect(detail.product.sort, '显式传 0 应写入 0').toBe(0);
  });

  test('3. 不传 sort 时维持原值', async () => {
    await putProduct({ sort: 7 });
    const detail = await putProduct({ name: autoName('三态商品改名') });
    expect(detail.product.sort, '未提交 sort 不应改动排序').toBe(7);
  });

  test('4. 设置定时上架后，普通编辑不清除预约', async () => {
    const detail = await putProduct({ on_sale_at: futureTime() });
    expect(detail.product.on_sale_at, '预约时间应写入').toBeTruthy();
    expect(detail.product.status, '定时上架期间应保持下架').toBe(0);

    const afterEdit = await putProduct({ name: autoName('三态商品改名2') });
    expect(
      afterEdit.product.on_sale_at,
      '未提交 on_sale_at 应维持原预约（旧实现会被恒清除）',
    ).toBe(detail.product.on_sale_at);
  });

  test('5. 显式传空串取消预约并配合 status 立即上架', async () => {
    const detail = await putProduct({ on_sale_at: '', status: 1 });
    expect(detail.product.on_sale_at || '', '显式空串应清除预约').toBe('');
    expect(detail.product.status, '配合 status=1 提交应立即上架').toBe(1);
  });
});
