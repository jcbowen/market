import { test, expect, type Playwright } from '@playwright/test';
import { newAdminContext, pickSku, ensureStock, asList, type ApiResponse, type PageData, type SkuRef } from '../helpers/api';

/**
 * 模块 38：库存批次进阶（进销存重构版）
 * 覆盖：入库单带批次号/生产日期/过期日期 → SKU 库存增加、生成批次；
 *       库存流水可追溯（batch_no/ref_no、before/after 库存快照一致）；
 *       出库单按 FEFO 从批次扣减（type 由单据承载，不再逐行传）；
 *       出库超库存被拒（批次库存不足）
 * 说明：入库/出库已单据化，日期格式为 YYYY-MM-DD（后端原样落库 date 列）
 */

interface StockRecord {
  id: number;
  sku_id: number;
  type: number;
  quantity: number;
  before_stock: number;
  after_stock: number;
  batch_id?: number;
  batch_no?: string;
  ref_no?: string;
  remark?: string;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let adminCommunityId = 0;

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  adminCommunityId = admin.communityId;
});

test.afterAll(async () => {
  await adminApi?.dispose();
});

/** 查询 SKU 当前库存 */
async function skuStock(skuId: number, productId: number): Promise<number> {
  const resp = await adminApi.get('/api/v1/skus', { params: { product_id: productId } });
  const body = (await resp.json()) as ApiResponse<{ id: number; stock: number }[]>;
  return (body.data ?? []).find((s) => s.id === skuId)?.stock ?? -1;
}

test.describe.serial('库存批次进阶', () => {
  let sku: SkuRef;
  const batchNo = `AUTO-BATCH-${Date.now()}`;

  test('准备：就绪 SKU', async () => {
    sku = await pickSku(adminApi, undefined, adminCommunityId);
    await ensureStock(adminApi, sku.id, 200);
  });

  test('带批次入库 +30：库存增加且流水含批次信息', async () => {
    const before = await skuStock(sku.id, sku.product_id);
    const resp = await adminApi.post('/api/v1/admin/inventory/in-orders', {
      data: {
        remark: 'AUTO-TEST 38 批次入库',
        items: [{
          sku_id: sku.id,
          quantity: 30,
          batch_no: batchNo,
          production_date: '2026-10-01',
          expire_date: '2027-10-01',
        }],
      },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `批次入库失败: ${body.message}`).toBe(0);
    expect(await skuStock(sku.id, sku.product_id)).toBe(before + 30);
  });

  test('流水可追溯：最新入库记录批次号/快照一致', async () => {
    const resp = await adminApi.get('/api/v1/admin/inventory/entries', { params: { page: 1, page_size: 20, sku_id: sku.id } });
    const body = (await resp.json()) as ApiResponse<PageData<StockRecord>>;
    expect(body.code).toBe(0);
    const record = asList<StockRecord>(body.data).find((r) => r.batch_no === batchNo);
    expect(record, '流水应包含本次批次记录').toBeTruthy();
    expect(record!.type).toBe(1);
    expect(record!.quantity).toBe(30);
    expect(record!.ref_no, '入库流水应带入库单号').toBeTruthy();
    // 快照自洽：after - before = quantity
    expect(record!.after_stock - record!.before_stock).toBe(30);
  });

  test('批次可查：入库生成的批次在批次列表可见', async () => {
    const resp = await adminApi.get('/api/v1/admin/inventory/batches', { params: { page: 1, page_size: 50, keyword: batchNo } });
    const body = (await resp.json()) as ApiResponse<PageData<{ batch_no: string; quantity: number; production_date: string; expire_date: string }>>;
    expect(body.code).toBe(0);
    const batch = asList(body.data).find((b) => b.batch_no === batchNo);
    expect(batch, '批次列表应包含本次入库批次').toBeTruthy();
    expect(batch!.quantity).toBe(30);
    expect(batch!.production_date).toContain('2026-10-01');
    expect(batch!.expire_date).toContain('2027-10-01');
  });

  test('出库单扣减 -10：库存减少且流水可追溯（FEFO）', async () => {
    const before = await skuStock(sku.id, sku.product_id);
    const resp = await adminApi.post('/api/v1/admin/inventory/out-orders', {
      data: {
        type: 2,
        remark: 'AUTO-TEST 38 批次出库',
        items: [{ sku_id: sku.id, quantity: 10 }],
      },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `批次出库失败: ${body.message}`).toBe(0);
    expect(await skuStock(sku.id, sku.product_id)).toBe(before - 10);

    const records = (await (await adminApi.get('/api/v1/admin/inventory/entries', { params: { page: 1, page_size: 20, sku_id: sku.id } })).json()) as ApiResponse<PageData<StockRecord>>;
    // FEFO 逐批次落流水：一次出库可能拆成多条记录（每批次一条），不能假设单条 -10。
    // 取最新一条同备注记录拿到本次出库单号，再按 ref_no 聚合本轮全部流水断言。
    const newest = asList<StockRecord>(records.data).find((r) => r.type === 2 && r.remark?.includes('AUTO-TEST 38 批次出库'));
    expect(newest, '出库流水应包含本次记录').toBeTruthy();
    const sameOrder = asList<StockRecord>(records.data).filter((r) => r.type === 2 && r.ref_no === newest!.ref_no);
    expect(sameOrder.length, '同一出库单至少落一条流水').toBeGreaterThan(0);
    const totalOut = sameOrder.reduce((sum, r) => sum + r.quantity, 0);
    expect(totalOut).toBe(-10);
    // 快照自洽：聚合后 after-before 等于总扣减，且链上首条 before 与末条 after 连续
    expect(sameOrder[0]!.before_stock - sameOrder[sameOrder.length - 1]!.after_stock).toBe(10);
    for (let i = 1; i < sameOrder.length; i++) {
      expect(sameOrder[i]!.before_stock).toBe(sameOrder[i - 1]!.after_stock);
    }
  });

  test('出库超库存被拒', async () => {
    const before = await skuStock(sku.id, sku.product_id);
    const resp = await adminApi.post('/api/v1/admin/inventory/out-orders', {
      data: {
        type: 2,
        remark: 'AUTO-TEST 38 超库存出库',
        items: [{ sku_id: sku.id, quantity: before + 1000 }],
      },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `超库存出库应被拒绝: ${body.message}`).not.toBe(0);
    expect(await skuStock(sku.id, sku.product_id), '被拒后库存不变').toBe(before);
  });
});
