import { test, expect } from '@playwright/test';
import { newAdminContext, assertOk, type ApiResponse, type PageData } from '../helpers/api';
import { autoName } from '../helpers/data';

/**
 * 模块 88：出入库单幂等重放（biz_no）与采购单乐观锁（updated_at）
 * 背景（2026-10-30 审查修复）：
 *   - 出入库单创建即生效且无幂等键，网络重试/双击会重复加/减库存（P2-1）：
 *     请求新增可选 biz_no（客户端 UUID），服务端以 stock_op_logs 唯一索引互斥，
 *     同 biz_no 重放返回原单、库存只变动一次
 *   - 商品/采购单编辑为整份覆盖式保存，无乐观锁时并发编辑静默互相覆盖（P2-7）：
 *     请求新增 updated_at 版本号，不一致返回 10007
 * 覆盖：
 *   - 入库/出库单同 biz_no 重放返回原单，库存不重复变动
 *   - 采购单携带过期 updated_at 更新被拒（10007）且内容未被改写；携带最新版本更新成功
 *   - 库存变动守恒断言（汇总列口径）
 */

/** 乐观并发冲突（errcode.DataConflict） */
const CODE_DATA_CONFLICT = 10007;
/** 明显早于库中任何版本的时间串：构造「另一个管理员先改过」的过期版本 */
const STALE_VERSION_TIME = '2000-01-01 00:00:00';

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let categoryId = 0;
let productId = 0;
let skuId = 0;
let supplierId = 0;

/** 商品详情中的 SKU 行（只声明用到的字段） */
interface SkuDetailRow {
  id: number;
  stock: number;
}

/** 商品详情响应（skus 在顶层或 product 内） */
interface ProductDetail {
  skus?: SkuDetailRow[];
  product?: { skus?: SkuDetailRow[] };
}

/** 采购单详情视图（info + items，与网关返回一致） */
interface PoDetail {
  info: {
    id: number;
    remark: string;
    updated_at: string;
  };
  items: Array<{ sku_id: number; quantity: number }>;
}

/** 查询指定 SKU 的当前汇总库存 */
async function readSkuStock(): Promise<number> {
  const body = assertOk<ProductDetail>(
    (await (await adminApi.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<ProductDetail>,
    '商品详情库存查询',
  );
  const row = (body.skus ?? body.product?.skus ?? []).find((s) => s.id === skuId);
  expect(row, '详情应包含测试 SKU').toBeTruthy();
  return row!.stock;
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;

  // 造数：分类 → 商品（单规格 SKU）→ 供应商
  categoryId = assertOk<{ id: number }>(
    (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('幂等分类') } })).json()) as ApiResponse<{
      id: number;
    }>,
    '创建分类',
  ).id;

  const created = assertOk<{ product?: { id: number }; id?: number }>(
    (await (
      await adminApi.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name: autoName('幂等商品'),
          unit: '件',
          is_multi_spec: false,
          sku_items: [{ price: 9.9, stock: 0, cost_price: 2 }],
        },
      })
    ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
    '创建商品',
  );
  productId = created.product?.id ?? created.id ?? 0;

  // 商品详情取默认 SKU id（单规格商品首个即默认 SKU），详情自带实时库存供守恒断言
  const detail = assertOk<ProductDetail>(
    (await (await adminApi.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<ProductDetail>,
    '读取商品详情',
  );
  skuId = (detail.skus ?? detail.product?.skus ?? [])[0]?.id ?? 0;

  supplierId = assertOk<{ id: number }>(
    (await (
      await adminApi.post('/api/v1/admin/inventory/suppliers', {
        data: {
          name: autoName('幂等供应商'),
          contact_name: '联系人88',
          phone: '13900000088',
          address: 'AUTO-TEST 地址 88',
          status: 1,
        },
      })
    ).json()) as ApiResponse<{ id: number }>,
    '创建供应商',
  ).id;
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

test.describe.serial('出入库单 biz_no 幂等重放', () => {
  const inBizNo = `test-in-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;

  test('1. 入库单首提交生效，重放同 biz_no 返回原单且不重复加库存', async () => {
    test.skip(!skuId, '造数未完成');

    const first = assertOk<{ info: { id: number; in_no: string } }>(
      (await (
        await adminApi.post('/api/v1/admin/inventory/in-orders', {
          data: {
            biz_no: inBizNo,
            items: [{ sku_id: skuId, quantity: 5 }],
          },
        })
      ).json()) as ApiResponse<{ info: { id: number; in_no: string } }>,
      '首次入库',
    );
    expect(await readSkuStock(), '首入库后库存').toBe(5);

    // 同 biz_no 重放：返回原单，库存不变
    const replay = assertOk<{ info: { id: number; in_no: string } }>(
      (await (
        await adminApi.post('/api/v1/admin/inventory/in-orders', {
          data: {
            biz_no: inBizNo,
            items: [{ sku_id: skuId, quantity: 5 }],
          },
        })
      ).json()) as ApiResponse<{ info: { id: number; in_no: string } }>,
      '重放入库',
    );
    expect(replay.info.id, '重放应返回原单 ID').toBe(first.info.id);
    expect(replay.info.in_no, '重放应返回原单号').toBe(first.info.in_no);
    expect(await readSkuStock(), '重放后库存不应重复增加').toBe(5);
  });

  test('2. 出库单首提交生效，重放同 biz_no 返回原单且不重复减库存', async () => {
    test.skip(!skuId, '造数未完成');

    const outBizNo = `test-out-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    const first = assertOk<{ info: { id: number; out_no: string } }>(
      (await (
        await adminApi.post('/api/v1/admin/inventory/out-orders', {
          data: {
            type: 3,
            biz_no: outBizNo,
            items: [{ sku_id: skuId, quantity: 2 }],
          },
        })
      ).json()) as ApiResponse<{ info: { id: number; out_no: string } }>,
      '首次出库',
    );
    expect(await readSkuStock(), '首出库后库存').toBe(3);

    const replay = assertOk<{ info: { id: number; out_no: string } }>(
      (await (
        await adminApi.post('/api/v1/admin/inventory/out-orders', {
          data: {
            type: 3,
            biz_no: outBizNo,
            items: [{ sku_id: skuId, quantity: 2 }],
          },
        })
      ).json()) as ApiResponse<{ info: { id: number; out_no: string } }>,
      '重放出库',
    );
    expect(replay.info.id, '重放应返回原单 ID').toBe(first.info.id);
    expect(replay.info.out_no, '重放应返回原单号').toBe(first.info.out_no);
    expect(await readSkuStock(), '重放后库存不应重复减少').toBe(3);
  });
});

test.describe.serial('采购单乐观锁（updated_at）', () => {
  let purchaseId = 0;
  let currentVersion = '';

  test('3. 创建草稿采购单并记录版本号', async () => {
    test.skip(!skuId || !supplierId, '造数未完成');

    const created = (await (
      await adminApi.post('/api/v1/admin/inventory/purchase-orders', {
        data: {
          supplier_id: supplierId,
          remark: 'AUTO-TEST 88 乐观锁',
          items: [{ sku_id: skuId, quantity: 10, purchase_price: 2 }],
        },
      })
    ).json()) as ApiResponse<{ info: { id: number } }>;
    expect(created.code, `创建采购单失败: ${created.message}`).toBe(0);
    purchaseId = created.data?.info?.id ?? 0;

    const detail = assertOk<PoDetail>(
      (await (await adminApi.get(`/api/v1/admin/inventory/purchase-orders/${purchaseId}`)).json()) as ApiResponse<PoDetail>,
      '采购单详情',
    );
    currentVersion = detail.info.updated_at;
    expect(currentVersion, '详情应返回乐观锁版本号 updated_at').not.toBe('');
  });

  test('4. 携带过期版本更新被拒 10007，且内容未被改写', async () => {
    test.skip(!purchaseId, '采购单未创建');

    const rejected = (await (
      await adminApi.put(`/api/v1/admin/inventory/purchase-orders/${purchaseId}`, {
        data: {
          supplier_id: supplierId,
          remark: 'AUTO-TEST 88 过期版本提交',
          updated_at: STALE_VERSION_TIME,
          items: [{ sku_id: skuId, quantity: 99, purchase_price: 2 }],
        },
      })
    ).json()) as ApiResponse;
    expect(rejected.code, `过期版本应返回 10007: ${rejected.message}`).toBe(CODE_DATA_CONFLICT);

    const detail = assertOk<PoDetail>(
      (await (await adminApi.get(`/api/v1/admin/inventory/purchase-orders/${purchaseId}`)).json()) as ApiResponse<PoDetail>,
      '被拒后回查详情',
    );
    expect(detail.info.remark, '被拒后备注未被改写').toBe('AUTO-TEST 88 乐观锁');
    expect(detail.items[0]?.quantity, '被拒后明细未被改写').toBe(10);
  });

  test('5. 携带最新版本更新成功（乐观锁正路径）', async () => {
    test.skip(!purchaseId, '采购单未创建');

    const ok = assertOk<PoDetail>(
      (await (
        await adminApi.put(`/api/v1/admin/inventory/purchase-orders/${purchaseId}`, {
          data: {
            supplier_id: supplierId,
            remark: 'AUTO-TEST 88 最新版本提交',
            updated_at: currentVersion,
            items: [{ sku_id: skuId, quantity: 10, purchase_price: 2 }],
          },
        })
      ).json()) as ApiResponse<PoDetail>,
      '最新版本更新',
    );
    expect(ok.info.remark).toBe('AUTO-TEST 88 最新版本提交');
  });
});
