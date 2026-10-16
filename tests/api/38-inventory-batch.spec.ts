import { test, expect, type Playwright } from '@playwright/test';
import { newAdminContext, pickSku, ensureStock, asList, type ApiResponse, type PageData, type SkuRef } from '../helpers/api';

/**
 * 模块 38：库存批次进阶
 * 覆盖：入库带批次号/生产日期/过期日期 → SKU 库存增加；
 *       库存流水可追溯（batch_no/production_date/expire_date、before/after 库存快照一致）；
 *       出库扣减同样携带批次信息（type=2）；
 *       库存不足出库被拒（stock.go 191-193）
 * 说明：proto StockEntryItem 直传 expire_date（无保质天数字段），过期日由调用方计算
 */

interface StockRecord {
  id: number;
  sku_id: number;
  type: number;
  quantity: number;
  before_stock: number;
  after_stock: number;
  batch_no?: string;
  production_date?: string;
  expire_date?: string;
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
    sku = await pickSku(adminApi);
    await ensureStock(adminApi, sku.id, 200);
  });

  test('带批次入库 +30：库存增加且流水含批次信息', async () => {
    const before = await skuStock(sku.id, sku.product_id);
    const resp = await adminApi.post('/api/v1/admin/inventory/entries', {
      data: {
        type: 1,
        remark: 'AUTO-TEST 38 批次入库',
        items: [{
          sku_id: sku.id,
          quantity: 30,
          batch_no: batchNo,
          production_date: '2026-10-01 00:00:00',
          expire_date: '2027-10-01 00:00:00',
        }],
      },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `批次入库失败: ${body.message}`).toBe(0);
    expect(await skuStock(sku.id, sku.product_id)).toBe(before + 30);
  });

  test('流水可追溯：最新入库记录批次/日期/快照一致', async () => {
    const resp = await adminApi.get('/api/v1/admin/inventory/entries', { params: { page: 1, page_size: 20, sku_id: sku.id } });
    const body = (await resp.json()) as ApiResponse<PageData<StockRecord>>;
    expect(body.code).toBe(0);
    const record = asList<StockRecord>(body.data).find((r) => r.batch_no === batchNo);
    expect(record, '流水应包含本次批次记录').toBeTruthy();
    expect(record!.type).toBe(1);
    expect(record!.quantity).toBe(30);
    expect(record!.production_date, '生产日期应落库').toBeTruthy();
    expect(record!.expire_date, '过期日期应落库').toBeTruthy();
    // 快照自洽：after - before = quantity
    expect(record!.after_stock - record!.before_stock).toBe(30);
  });

  test('带批次出库 -10：库存扣减且流水可追溯', async () => {
    const before = await skuStock(sku.id, sku.product_id);
    const resp = await adminApi.post('/api/v1/admin/inventory/out', {
      data: {
        type: 2,
        remark: 'AUTO-TEST 38 批次出库',
        items: [{ sku_id: sku.id, quantity: 10, batch_no: batchNo }],
      },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `批次出库失败: ${body.message}`).toBe(0);
    expect(await skuStock(sku.id, sku.product_id)).toBe(before - 10);

    // StockOutItem 设计上不含 batch_no（proto 字段），出库流水按 remark + type=2 定位
    const records = (await (await adminApi.get('/api/v1/admin/inventory/entries', { params: { page: 1, page_size: 20, sku_id: sku.id } })).json()) as ApiResponse<PageData<StockRecord>>;
    const record = asList<StockRecord>(records.data).find((r) => r.type === 2 && r.remark?.includes('AUTO-TEST 38 批次出库'));
    expect(record, '出库流水应包含本次记录').toBeTruthy();
    expect(record!.quantity).toBe(-10);
    expect(record!.after_stock - record!.before_stock).toBe(-10);
  });

  test('出库超库存被拒', async () => {
    const before = await skuStock(sku.id, sku.product_id);
    const resp = await adminApi.post('/api/v1/admin/inventory/out', {
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
