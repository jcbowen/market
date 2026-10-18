import { test, expect } from '@playwright/test';
import { newAdminContext, assertOk, type ApiResponse, type PageData } from '../helpers/api';
import { autoName, uniqueSuffix } from '../helpers/data';

/**
 * 模块 45：盘点提交闭环 + 临期预警
 * 覆盖：
 *   - 创建盘点单（status=1）→ 提交带差异明细 → 库存按盘点调整 + type=3 流水可查
 *   - 盘点详情含差异快照（system_stock/actual_stock/diff_quantity）
 *   - 重复提交同一盘点单被状态机拒绝
 *   - 入库临期批次（expire_date ≤ 3 天）→ GET /inventory/warnings 产生 warning_type=2 临期记录
 *   - 库存清零后触发 warning_type=1 低库存，且临期记录随库存归零消失
 * 说明：自建商品隔离数据；临期天数为服务端配置（ExpireWarningDays 默认 3 天）
 */

interface SkuInfo {
  id: number;
  stock: number;
  [k: string]: unknown;
}

interface StockCheckInfo {
  id: number;
  check_no: string;
  status: number;
  total_items: number;
  diff_items: number;
  remark?: string;
  [k: string]: unknown;
}

interface CheckDetailItem {
  sku_id: number;
  system_stock: number;
  actual_stock: number;
  diff_quantity: number;
  [k: string]: unknown;
}

interface StockRecord {
  sku_id: number;
  type: number;
  quantity: number;
  before_stock: number;
  after_stock: number;
  remark?: string;
  [k: string]: unknown;
}

interface WarningItem {
  sku_id: number;
  warning_type: number;
  expire_date?: string;
  stock: number;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let productId = 0;
let categoryId = 0;
let skuId = 0;

/** 读取自建商品当前 SKU 库存 */
async function currentStock(): Promise<number> {
  const detail = (await (
    await adminApi.get(`/api/v1/admin/products/${productId}`)
  ).json()) as ApiResponse<{ skus: SkuInfo[] }>;
  const sku = (assertOk(detail, '商品详情').skus ?? []).find((s) => s.id === skuId);
  expect(sku, 'SKU 不存在').toBeTruthy();
  return sku!.stock;
}

/** 查询库存预警列表 */
async function fetchWarnings(): Promise<WarningItem[]> {
  const body = (await (
    await adminApi.get('/api/v1/admin/inventory/warnings')
  ).json()) as ApiResponse<{ warnings?: WarningItem[] } | WarningItem[]>;
  const data = assertOk(body, '库存预警查询');
  const list = Array.isArray(data) ? data : (data.warnings ?? data.list ?? []);
  return list as WarningItem[];
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  const cat = (await (
    await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('盘点分类') } })
  ).json()) as ApiResponse<{ id: number }>;
  expect(cat.code, `创建分类失败: ${cat.message}`).toBe(0);
  categoryId = cat.data!.id;
  const prod = (await (
    await adminApi.post('/api/v1/admin/products', {
      data: {
        category_id: categoryId,
        name: autoName('盘点商品'),
        unit: '份',
        is_multi_spec: false,
        sku_items: [{ price: 2.5, stock: 50, safety_stock: 5 }],
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
  ).json()) as ApiResponse<{ skus: SkuInfo[] }>;
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

test.describe.serial('盘点提交闭环', () => {
  let checkId = 0;
  let stockBefore = 0;
  const DIFF = 7; // 盘亏数量（实际比系统少 7）

  test('1. 创建盘点单（status=1）', async () => {
    const body = (await (
      await adminApi.post('/api/v1/admin/inventory/checks', { data: { remark: 'AUTO-TEST 45 盘点' } })
    ).json()) as ApiResponse<StockCheckInfo>;
    const check = assertOk(body, '创建盘点单');
    checkId = check.id;
    expect(checkId).toBeGreaterThan(0);
    expect(check.check_no, '盘点单号应生成').toBeTruthy();
    expect(check.status).toBe(1);
  });

  test('2. 提交带差异明细：库存按盘点调整', async () => {
    if (!checkId) test.skip(true, '盘点单未创建');
    stockBefore = await currentStock();
    const resp = await adminApi.post(`/api/v1/admin/inventory/checks/${checkId}/submit`, {
      data: { items: [{ sku_id: skuId, actual_stock: stockBefore - DIFF }] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `提交盘点失败: ${body.message}`).toBe(0);
    expect(await currentStock(), '库存应调整为盘点实存').toBe(stockBefore - DIFF);
  });

  test('3. 盘点详情含差异快照', async () => {
    if (!checkId) test.skip(true, '盘点单未创建');
    const body = (await (
      await adminApi.get(`/api/v1/admin/inventory/checks/${checkId}`)
    ).json()) as ApiResponse<{ info: StockCheckInfo; items: CheckDetailItem[] }>;
    const detail = assertOk(body, '盘点详情');
    expect(detail.info.status, '提交后单据应为已提交(2)').toBe(2);
    expect(detail.info.total_items).toBe(1);
    expect(detail.info.diff_items, '差异条目数应为 1').toBe(1);
    const item = (detail.items ?? []).find((i) => i.sku_id === skuId);
    expect(item, '详情应含本 SKU 明细').toBeTruthy();
    expect(item!.system_stock, '系统库存快照').toBe(stockBefore);
    expect(item!.actual_stock, '实盘库存快照').toBe(stockBefore - DIFF);
    expect(item!.diff_quantity, '差异数量快照').toBe(-DIFF);
  });

  test('4. 盘点调整流水可查（type=3）', async () => {
    if (!checkId) test.skip(true, '盘点单未创建');
    const body = (await (
      await adminApi.get('/api/v1/admin/inventory/entries', { params: { page: 1, page_size: 20, sku_id: skuId } })
    ).json()) as ApiResponse<PageData<StockRecord>>;
    expect(body.code).toBe(0);
    const record = (body.data?.list ?? []).find((r) => r.type === 3 && r.quantity === -DIFF);
    expect(record, '应有盘点调整流水（type=3）').toBeTruthy();
    expect(record!.after_stock - record!.before_stock, '流水差值应等于盘亏数').toBe(-DIFF);
  });

  test('5. 重复提交同一盘点单被拒', async () => {
    if (!checkId) test.skip(true, '盘点单未创建');
    const resp = await adminApi.post(`/api/v1/admin/inventory/checks/${checkId}/submit`, {
      data: { items: [{ sku_id: skuId, actual_stock: 999 }] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '已提交盘点单不应可重复提交').not.toBe(0);
    expect(await currentStock(), '重复提交不应改动库存').toBe(stockBefore - DIFF);
  });
});

test.describe.serial('临期与低库存预警', () => {
  const batchNo = `PC45-${uniqueSuffix()}`;

  test('6. 入库临期批次产生 warning_type=2 预警', async () => {
    // 过期日期取明天（≤ 3 天临期阈值），批次数量 8 保证 stock>0
    const tomorrow = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
    // 进销存重构：走入库单接口，日期格式 YYYY-MM-DD
    const entry = (await (
      await adminApi.post('/api/v1/admin/inventory/in-orders', {
        data: {
          remark: 'AUTO-TEST 45 临期入库',
          items: [{
            sku_id: skuId,
            quantity: 8,
            batch_no: batchNo,
            expire_date: tomorrow,
          }],
        },
      })
    ).json()) as ApiResponse;
    expect(entry.code, `临期批次入库失败: ${entry.message}`).toBe(0);

    const warnings = await fetchWarnings();
    const near = warnings.find((w) => w.sku_id === skuId && w.warning_type === 2);
    expect(near, '应产生临期预警记录').toBeTruthy();
    expect(near!.expire_date, '临期预警应带过期日期').toBeTruthy();
  });

  test('7. 库存清零触发 warning_type=1 低库存，临期记录消失', async () => {
    const stock = await currentStock();
    // 进销存重构：走出库单接口（type=2 领用，FEFO 扣减）
    const out = (await (
      await adminApi.post('/api/v1/admin/inventory/out-orders', {
        data: { type: 2, items: [{ sku_id: skuId, quantity: stock }], remark: 'AUTO-TEST 45 清零出库' },
      })
    ).json()) as ApiResponse;
    expect(out.code, `清零出库失败: ${out.message}`).toBe(0);
    expect(await currentStock()).toBe(0);

    const warnings = await fetchWarnings();
    const low = warnings.find((w) => w.sku_id === skuId && w.warning_type === 1);
    expect(low, '零库存应触发低库存预警').toBeTruthy();
    expect(
      warnings.some((w) => w.sku_id === skuId && w.warning_type === 2),
      '库存归零后临期预警应消失（stock>0 前提）',
    ).toBe(false);
  });
});
