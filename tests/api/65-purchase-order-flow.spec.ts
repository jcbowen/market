import { test, expect } from '@playwright/test';
import { newAdminContext, assertOk, asList, type ApiResponse, type PageData } from '../helpers/api';
import { autoName } from '../helpers/data';

/**
 * 模块 65：采购单状态机与入库回写
 * 覆盖：
 *   - POST/PUT/GET /api/v1/admin/inventory/purchase-orders（+ /:id/submit、/:id/cancel）
 *   - 状态机：1 草稿 →（submit 或 submit_now）→ 2 已下单 →（入库部分）→ 3 部分入库 →（收满）→ 4 已完结；
 *     1/2 可取消为 5，3/4/5 视为已产生入库不允许取消
 *   - 明细全量替换语义（PUT 以提交的 items 覆盖原明细，非增量）与提交前校验（空明细、缺 SKU、
 *     数量 ≤0、单价为负均拒绝，且校验先于状态判定，失败不改坏原单）
 *   - 入库单驱动的采购单回写：received_quantity 累加、超量拒绝、非明细 SKU 拒绝且整单回滚
 *   - 单据不存在的错误由服务层以 InvalidArgument 返回（映射 10001），非 10003
 * 数据惯例：进销存单据无删除接口且供应商受引用保护，本轮按「只增不删」留档，
 * 单号/名称带 AUTO-TEST 前缀与本轮 tag 便于人工识别；商品与 SKU 在 afterAll 清理
 * 说明：单据详情的跨社区拦截（ensureBillInCommunity）对超管直接放行，
 * 本环境管理员为 is_super=1，无法构造越权语境，该分支留作上线前人工验证
 */

/** 业务参数错误码（服务层 InvalidArgument 与网关 binding 同口径） */
const PARAM_ERROR = 10001;

/** 采购单状态常量，与 model/purchase.go 对齐 */
const PO_STATUS = { draft: 1, ordered: 2, partial: 3, completed: 4, cancelled: 5 } as const;

interface PoItem {
  sku_id?: number;
  quantity?: number;
  received_quantity?: number;
  purchase_price?: number;
}

interface PoDetail {
  info?: { id?: number; purchase_no?: string; status?: number; total_amount?: number; supplier_name?: string };
  items?: PoItem[];
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
/** 本轮造数标记，供应商名与单号识别用 */
const tag = Date.now();
let categoryId = 0;
let supplierId = 0;
/** 三张商品各自的单规格 SKU：A/B 进采购单明细，C 专用于「非明细 SKU」拒绝用例 */
let skuA = 0;
let skuB = 0;
let skuC = 0;
let productA = 0;
let productB = 0;
let productC = 0;
/** 用例间共享：草稿单与其单号 */
let purchaseOrderId = 0;
let purchaseNo = '';

/**
 * 创建单规格商品并取其唯一 SKU
 * - 函数名: createSku
 * - 参数: label — 名称标识；stock — 初始库存
 * - 返回: Promise<{ productId: number; skuId: number }>
 * - 异常: 建品失败或 SKU 列表为空时抛出 Error（造数前置）
 */
async function createSku(label: string, stock: number): Promise<{ productId: number; skuId: number }> {
  const created = assertOk<{ product?: { id: number }; id?: number }>(
    (await (
      await adminApi.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name: autoName(`采购商品${label}`),
          unit: '件',
          is_multi_spec: false,
          virtual_sales: 0,
          sku_items: [{ price: 9.9, stock, safety_stock: 0 }],
        },
      })
    ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
    '创建商品',
  );
  const productId = created.product?.id ?? created.id ?? 0;
  if (!productId) throw new Error('创建商品未返回 ID');
  const skus = assertOk<{ id: number }[]>(
    (await (await adminApi.get('/api/v1/skus', { params: { product_id: productId } })).json()) as ApiResponse<{ id: number }[]>,
    'SKU 列表',
  );
  const skuId = (skus ?? [])[0]?.id ?? 0;
  if (!skuId) throw new Error(`商品 ${productId} 未生成 SKU`);
  return { productId, skuId };
}

/**
 * 读取 SKU 当前库存
 * - 函数名: readSkuStock
 * - 参数: productId — SKU 所属商品 ID；skuId — SKU ID
 * - 返回: Promise<number> 库存数量，SKU 不存在时返回 -1
 * - 异常: 接口业务失败时由 assertOk 抛出
 */
async function readSkuStock(productId: number, skuId: number): Promise<number> {
  const body = (await (await adminApi.get('/api/v1/skus', { params: { product_id: productId } })).json()) as ApiResponse<
    { id: number; stock: number }[]
  >;
  const row = asList<{ id: number; stock: number }>(body.data).find((s) => s.id === skuId);
  return row?.stock ?? -1;
}

/**
 * 创建采购单
 * - 函数名: createPurchaseOrder
 * - 参数: data — 请求体（items 可传非法值以覆盖校验分支，submit_now 可跳过草稿）
 * - 返回: Promise<ApiResponse<PoDetail>> 原始响应，业务码交由调用方断言
 * - 异常: 无
 */
async function createPurchaseOrder(data: Record<string, unknown>): Promise<ApiResponse<PoDetail>> {
  const resp = await adminApi.post('/api/v1/admin/inventory/purchase-orders', {
    data: { supplier_id: supplierId, ...data },
  });
  return (await resp.json()) as ApiResponse<PoDetail>;
}

/**
 * 读取采购单详情
 * - 函数名: readPurchaseOrder
 * - 参数: id — 采购单 ID
 * - 返回: Promise<PoDetail>，业务失败时由 assertOk 抛出
 * - 异常: code !== 0 时抛出含 message 的 Error
 */
async function readPurchaseOrder(id: number): Promise<PoDetail> {
  const body = (await (await adminApi.get(`/api/v1/admin/inventory/purchase-orders/${id}`)).json()) as ApiResponse<PoDetail>;
  return assertOk(body, '采购单详情');
}

/**
 * 取采购单明细中指定 SKU 的行
 * - 函数名: findItem
 * - 参数: detail — 详情；skuId — SKU ID
 * - 返回: PoItem | undefined
 * - 异常: 无
 */
function findItem(detail: PoDetail, skuId: number): PoItem | undefined {
  return (detail.items ?? []).find((it) => it.sku_id === skuId);
}

/**
 * 以采购单为来源创建入库单（创建即生效并回写采购单）
 * - 函数名: stockIn
 * - 参数: data — 请求体（items 为入库明细，purchase_order_id 缺省即直接入库）
 * - 返回: Promise<ApiResponse> 原始响应
 * - 异常: 无
 */
async function stockIn(data: Record<string, unknown>): Promise<ApiResponse> {
  const resp = await adminApi.post('/api/v1/admin/inventory/in-orders', {
    data: { remark: `AUTO-TEST 采购入库 ${tag}`, ...data },
  });
  return (await resp.json()) as ApiResponse;
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;

  try {
    categoryId = assertOk<{ id: number }>(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('采购分类') } })).json()) as ApiResponse<{
        id: number;
      }>,
      '创建分类',
    ).id;
    const supplier = assertOk<{ id: number }>(
      (await (
        await adminApi.post('/api/v1/admin/inventory/suppliers', {
          data: { name: autoName('采购供应商'), contact_name: '测试联系人', phone: '13900000000' },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建供应商',
    );
    supplierId = supplier.id;
    const a = await createSku('A', 10);
    const b = await createSku('B', 10);
    const c = await createSku('C', 10);
    productA = a.productId;
    productB = b.productId;
    productC = c.productId;
    skuA = a.skuId;
    skuB = b.skuId;
    skuC = c.skuId;
  } catch (e) {
    // 假绿收敛：栈已起时造数失败即回归，显式失败而非静默 skip
    throw new Error(`[65] 造数失败：${e instanceof Error ? e.message : e}`);
  }
});

test.afterAll(async () => {
  try {
    for (const id of [productA, productB, productC]) {
      if (id) await adminApi.delete(`/api/v1/admin/products/${id}`);
    }
    if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果；采购单/入库单无删除接口，供应商受引用保护 */
  }
  await adminApi?.dispose();
});

test.describe.serial('采购单状态机与入库回写', () => {
  test('1. 创建草稿：状态落 1，金额按数量×单价汇总，明细未收货数量为 0', async () => {
    test.skip(supplierId === 0 || skuA === 0, '造数未完成');
    const body = await createPurchaseOrder({
      remark: `AUTO-TEST 采购草稿 ${tag}`,
      items: [{ sku_id: skuA, quantity: 5, purchase_price: 2.5 }],
    });
    expect(body.code, `创建采购单失败: ${body.message}`).toBe(0);
    purchaseOrderId = body.data?.info?.id ?? 0;
    purchaseNo = body.data?.info?.purchase_no ?? '';
    expect(purchaseOrderId, '应返回采购单 ID').toBeGreaterThan(0);
    expect(purchaseNo, '单号应回填 PO 前缀').toMatch(/^PO\d+/);
    expect(body.data?.info?.status, '默认应为草稿态').toBe(PO_STATUS.draft);
    expect(body.data?.info?.total_amount, '金额应为 5×2.5').toBeCloseTo(12.5, 2);
    expect(body.data?.items?.length, '明细 1 条').toBe(1);
    expect(findItem(body.data ?? {}, skuA)?.received_quantity ?? 0, '新建明细未收货').toBe(0);
  });

  test('2. 明细校验矩阵：空明细/缺 SKU/数量≤0/单价为负均拒绝', async () => {
    test.skip(supplierId === 0 || skuA === 0, '造数未完成');
    const cases: Array<{ items: unknown[]; keyword: string }> = [
      { items: [], keyword: '采购明细不能为空' },
      { items: [{ sku_id: 0, quantity: 1, purchase_price: 1 }], keyword: '采购明细缺少 SKU' },
      { items: [{ sku_id: skuA, quantity: 0, purchase_price: 1 }], keyword: '采购数量必须大于 0' },
      { items: [{ sku_id: skuA, quantity: 1, purchase_price: -1 }], keyword: '采购单价不能为负数' },
    ];
    for (const tc of cases) {
      const body = await createPurchaseOrder({ items: tc.items });
      expect(body.code, `${tc.keyword} 应被拒绝`).toBe(PARAM_ERROR);
      expect(body.message, `应透传「${tc.keyword}」`).toContain(tc.keyword);
    }
  });

  test('3. 列表可见且可按单号/状态过滤，供应商名回填', async () => {
    test.skip(purchaseOrderId === 0, '前置草稿单未建立');
    const byNo = assertOk<PageData<{ id: number; supplier_name?: string; status?: number }>>(
      (await (await adminApi.get('/api/v1/admin/inventory/purchase-orders', { params: { keyword: purchaseNo, page_size: 50 } })).json()) as ApiResponse<
        PageData<{ id: number; supplier_name?: string; status?: number }>
      >,
      '按单号查询采购单',
    );
    const row = asList<{ id: number; supplier_name?: string; status?: number }>(byNo).find((r) => r.id === purchaseOrderId);
    expect(row, '按单号应命中该单').toBeTruthy();
    expect(row?.status, '列表状态应为草稿').toBe(PO_STATUS.draft);
    expect(row?.supplier_name, '列表应回填供应商名').toBeTruthy();

    const draftOnly = assertOk<PageData<{ id: number }>>(
      (await (await adminApi.get('/api/v1/admin/inventory/purchase-orders', { params: { status: 1, page_size: 50 } })).json()) as ApiResponse<
        PageData<{ id: number }>
      >,
      '按草稿状态过滤',
    );
    expect(asList<{ id: number }>(draftOnly).some((r) => r.id === purchaseOrderId), '状态 1 过滤应命中').toBe(true);

    const orderedOnly = assertOk<PageData<{ id: number }>>(
      (await (await adminApi.get('/api/v1/admin/inventory/purchase-orders', { params: { status: 2, page_size: 50 } })).json()) as ApiResponse<
        PageData<{ id: number }>
      >,
      '按已下单状态过滤',
    );
    expect(asList<{ id: number }>(orderedOnly).some((r) => r.id === purchaseOrderId), '草稿单不应出现在已下单过滤结果中').toBe(
      false,
    );
  });

  test('4. submit_now=true 跳过草稿直接落已下单', async () => {
    test.skip(supplierId === 0 || skuA === 0, '造数未完成');
    const body = await createPurchaseOrder({
      submit_now: true,
      remark: `AUTO-TEST 采购直提 ${tag}`,
      items: [{ sku_id: skuA, quantity: 2, purchase_price: 3 }],
    });
    expect(body.code, `直提采购单失败: ${body.message}`).toBe(0);
    expect(body.data?.info?.status, '应为已下单态').toBe(PO_STATUS.ordered);
  });

  test('5. PUT 全量替换明细：以提交集合覆盖，金额重算', async () => {
    test.skip(purchaseOrderId === 0, '前置草稿单未建立');
    const resp = await adminApi.put(`/api/v1/admin/inventory/purchase-orders/${purchaseOrderId}`, {
      data: {
        supplier_id: supplierId,
        remark: `AUTO-TEST 采购改单 ${tag}`,
        items: [
          { sku_id: skuA, quantity: 5, purchase_price: 2.5 },
          { sku_id: skuB, quantity: 4, purchase_price: 3 },
        ],
      },
    });
    const body = (await resp.json()) as ApiResponse<PoDetail>;
    expect(body.code, `修改采购单失败: ${body.message}`).toBe(0);
    expect(body.data?.items?.length, '明细应替换为 2 条').toBe(2);
    expect(body.data?.info?.total_amount, '金额应重算为 5×2.5+4×3').toBeCloseTo(24.5, 2);

    const detail = await readPurchaseOrder(purchaseOrderId);
    expect(detail.items?.length, '详情读取应仍为替换后的 2 条').toBe(2);
    expect(detail.info?.status, '改单不改变草稿状态').toBe(PO_STATUS.draft);
  });

  test('6. 非法明细不改动原单：校验先于替换，空明细提交后明细仍在', async () => {
    test.skip(purchaseOrderId === 0, '前置草稿单未建立');
    const resp = await adminApi.put(`/api/v1/admin/inventory/purchase-orders/${purchaseOrderId}`, {
      data: { supplier_id: supplierId, items: [] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '空明细应被拒绝').toBe(PARAM_ERROR);
    expect(body.message).toContain('采购明细不能为空');

    const detail = await readPurchaseOrder(purchaseOrderId);
    expect(detail.items?.length, '校验失败不应清空原明细').toBe(2);
  });

  test('7. submit：草稿→已下单', async () => {
    test.skip(purchaseOrderId === 0, '前置草稿单未建立');
    const body = (await (await adminApi.post(`/api/v1/admin/inventory/purchase-orders/${purchaseOrderId}/submit`)).json()) as ApiResponse;
    expect(body.code, `提交采购单失败: ${body.message}`).toBe(0);
    expect((await readPurchaseOrder(purchaseOrderId)).info?.status, '状态应为已下单').toBe(PO_STATUS.ordered);
  });

  test('8. 已下单后禁止改单与重复下单', async () => {
    test.skip(purchaseOrderId === 0, '前置草稿单未建立');
    const putResp = await adminApi.put(`/api/v1/admin/inventory/purchase-orders/${purchaseOrderId}`, {
      data: { supplier_id: supplierId, items: [{ sku_id: skuA, quantity: 1, purchase_price: 1 }] },
    });
    const putBody = (await putResp.json()) as ApiResponse;
    expect(putBody.code, '已下单不应允许修改').toBe(PARAM_ERROR);
    expect(putBody.message).toContain('仅草稿态采购单允许修改');

    const submitBody = (await (
      await adminApi.post(`/api/v1/admin/inventory/purchase-orders/${purchaseOrderId}/submit`)
    ).json()) as ApiResponse;
    expect(submitBody.code, '重复提交应被拒绝').toBe(PARAM_ERROR);
    expect(submitBody.message).toContain('仅草稿态采购单允许下单');
  });

  test('9. 部分入库：received_quantity 累加、单据转部分入库、SKU 库存同步增加', async () => {
    test.skip(purchaseOrderId === 0, '前置已下单单未建立');
    const beforeStock = await readSkuStock(productA, skuA);
    const body = await stockIn({
      purchase_order_id: purchaseOrderId,
      items: [{ sku_id: skuA, quantity: 3, unit_cost: 2.5 }],
    });
    expect(body.code, `部分入库失败: ${body.message}`).toBe(0);

    const detail = await readPurchaseOrder(purchaseOrderId);
    expect(detail.info?.status, '应收满一条未完 → 部分入库').toBe(PO_STATUS.partial);
    expect(findItem(detail, skuA)?.received_quantity ?? 0, 'SKU A 已收 3').toBe(3);
    expect(findItem(detail, skuB)?.received_quantity ?? 0, 'SKU B 仍未收').toBe(0);
    expect(await readSkuStock(productA, skuA), '入库应回写 SKU 库存 +3').toBe(beforeStock + 3);
  });

  test('10. 超量入库拒绝：超出采购单剩余未收数量', async () => {
    test.skip(purchaseOrderId === 0, '前置入库中的单未建立');
    const body = await stockIn({
      purchase_order_id: purchaseOrderId,
      items: [{ sku_id: skuA, quantity: 3, unit_cost: 2.5 }],
    });
    expect(body.code, '超量入库应被拒绝').toBe(PARAM_ERROR);
    expect(body.message, '应提示剩余未收').toContain('超出采购单剩余未收数量');
    expect((await readPurchaseOrder(purchaseOrderId)).info?.status, '拒绝后状态保持部分入库').toBe(PO_STATUS.partial);
  });

  test('11. 非明细 SKU 入库拒绝且整单回滚：库存与单据均不变', async () => {
    test.skip(purchaseOrderId === 0 || skuC === 0, '前置数据未就绪');
    const stockBefore = await readSkuStock(productC, skuC);
    const body = await stockIn({
      purchase_order_id: purchaseOrderId,
      items: [{ sku_id: skuC, quantity: 1, unit_cost: 1 }],
    });
    expect(body.code, '非明细 SKU 应被拒绝').toBe(PARAM_ERROR);
    expect(body.message).toContain('不在采购单明细中');
    expect(await readSkuStock(productC, skuC), '同事务回滚，SKU C 库存不应变化').toBe(stockBefore);

    const listBody = assertOk<PageData<{ id: number }>>(
      (await (await adminApi.get('/api/v1/admin/inventory/in-orders', { params: { purchase_order_id: purchaseOrderId, page_size: 50 } })).json()) as ApiResponse<
        PageData<{ id: number }>
      >,
      '入库单列表',
    );
    const bills = asList<{ id: number }>(listBody);
    const detail = await readPurchaseOrder(purchaseOrderId);
    expect(findItem(detail, skuC), '回滚后不应残留 SKU C 收货记录').toBeFalsy();
    expect(bills.length, '回滚后入库单不应新增（仅用例 9 的一张）').toBe(1);
  });

  test('12. 草稿单不允许入库', async () => {
    test.skip(supplierId === 0 || skuA === 0, '造数未完成');
    const draft = await createPurchaseOrder({
      remark: `AUTO-TEST 采购禁止入库 ${tag}`,
      items: [{ sku_id: skuA, quantity: 1, purchase_price: 1 }],
    });
    expect(draft.code, '创建草稿单应成功').toBe(0);
    const draftId = draft.data?.info?.id ?? 0;
    const body = await stockIn({ purchase_order_id: draftId, items: [{ sku_id: skuA, quantity: 1, unit_cost: 1 }] });
    expect(body.code, '草稿态不允许入库').toBe(PARAM_ERROR);
    expect(body.message).toContain('采购单当前状态不允许入库');

    const cancel = (await (await adminApi.post(`/api/v1/admin/inventory/purchase-orders/${draftId}/cancel`)).json()) as ApiResponse;
    expect(cancel.code, '草稿可取消以清理本轮单据').toBe(0);
  });

  test('13. 收满转已完结', async () => {
    test.skip(purchaseOrderId === 0, '前置入库中的单未建立');
    const body = await stockIn({
      purchase_order_id: purchaseOrderId,
      items: [
        { sku_id: skuA, quantity: 2, unit_cost: 2.5 },
        { sku_id: skuB, quantity: 4, unit_cost: 3 },
      ],
    });
    expect(body.code, `收尾入库失败: ${body.message}`).toBe(0);

    const detail = await readPurchaseOrder(purchaseOrderId);
    expect(detail.info?.status, '全部收满应为已完结').toBe(PO_STATUS.completed);
    expect(findItem(detail, skuA)?.received_quantity ?? 0, 'SKU A 收满 5').toBe(5);
    expect(findItem(detail, skuB)?.received_quantity ?? 0, 'SKU B 收满 4').toBe(4);
  });

  test('14. 已完结禁止再入库与取消', async () => {
    test.skip(purchaseOrderId === 0, '前置已完结单未建立');
    const inBody = await stockIn({ purchase_order_id: purchaseOrderId, items: [{ sku_id: skuA, quantity: 1, unit_cost: 1 }] });
    expect(inBody.code, '已完结不应再入库').toBe(PARAM_ERROR);
    expect(inBody.message).toContain('采购单当前状态不允许入库');

    const cancelBody = (await (
      await adminApi.post(`/api/v1/admin/inventory/purchase-orders/${purchaseOrderId}/cancel`)
    ).json()) as ApiResponse;
    expect(cancelBody.code, '已完结不应取消').toBe(PARAM_ERROR);
    expect(cancelBody.message).toContain('采购单已产生入库或已完结，不能取消');
  });

  test('15. 已下单可取消，取消后不可再提交', async () => {
    test.skip(supplierId === 0 || skuA === 0, '造数未完成');
    const created = await createPurchaseOrder({
      submit_now: true,
      remark: `AUTO-TEST 采购取消 ${tag}`,
      items: [{ sku_id: skuA, quantity: 1, purchase_price: 1 }],
    });
    const id = created.data?.info?.id ?? 0;
    expect(created.code, '创建直提单应成功').toBe(0);

    const cancel = (await (await adminApi.post(`/api/v1/admin/inventory/purchase-orders/${id}/cancel`)).json()) as ApiResponse;
    expect(cancel.code, `已下单应可取消: ${cancel.message}`).toBe(0);
    expect((await readPurchaseOrder(id)).info?.status, '状态应为已取消').toBe(PO_STATUS.cancelled);

    const resubmit = (await (await adminApi.post(`/api/v1/admin/inventory/purchase-orders/${id}/submit`)).json()) as ApiResponse;
    expect(resubmit.code, '已取消不应再提交').toBe(PARAM_ERROR);
    expect(resubmit.message).toContain('仅草稿态采购单允许下单');
  });

  test('16. 单据不存在与非法 ID：服务层以参数错误返回', async () => {
    test.skip(supplierId === 0, '造数未完成');
    const miss = (await (await adminApi.get('/api/v1/admin/inventory/purchase-orders/999999999')).json()) as ApiResponse;
    expect(miss.code, '不存在 ID 应报错').toBe(PARAM_ERROR);
    expect(miss.message).toContain('采购单不存在');

    const missSubmit = (await (await adminApi.post('/api/v1/admin/inventory/purchase-orders/999999999/submit')).json()) as ApiResponse;
    expect(missSubmit.code, '不存在 ID 提交应报错').toBe(PARAM_ERROR);

    const badId = (await (await adminApi.get('/api/v1/admin/inventory/purchase-orders/abc')).json()) as ApiResponse;
    expect(badId.code, '非数字 ID 应在网关拦截').toBe(PARAM_ERROR);
  });
});
