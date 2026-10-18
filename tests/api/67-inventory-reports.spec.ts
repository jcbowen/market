import { test, expect } from '@playwright/test';
import type { APIRequestContext } from '@playwright/test';
import {
  newAdminContext,
  assertOk,
  asList,
  adminLogin,
  newAuthedRequest,
  type ApiResponse,
  type PageData,
} from '../helpers/api';
import { autoName } from '../helpers/data';

/**
 * 模块 67：进销存报表（进销存汇总 + 毛利分析）
 * 覆盖：
 *   - GET /api/v1/admin/inventory/reports/summary（期初 + 入 − 出 = 期末）
 *   - GET /api/v1/admin/inventory/reports/profit（近似口径：期间销售数量 × 当前售价/当前成本）
 *   - 造一条完整流水链（入库 20 → 报损出库 3 → POS 到店销售 2），逐列锁定入出口径与守恒
 *   - 口径差异：出库单流水 ref_no 非空不计入毛利，订单扣减流水 ref_no 为空才计入毛利
 *   - 时间窗只约束聚合值、不筛行：窗口内无流水时期初=期末=当前库存（SQL 的 COALESCE 回退 s.stock）
 *   - 缺省区间等于「今天往前 30 天」，与显式传该区间结果一致
 *   - keyword 同时命中商品名与 SKU 编码；分页回显、翻页不重叠、按 SKU ID 升序；社区隔离
 *   - 现状锁定（非期望行为，缺陷已写入待办清单，本轮不改生产代码）：
 *     ① page=0 时 offset=(page-1)*pageSize 为负，MySQL 语法报错 → 对外 10002 服务器内部错误，
 *        本应是 10001 参数错误（两报表接口共用该分页构造，行为一致）；
 *     ② start_date/end_date 无格式校验，非法日期与起止倒挂都静默按「窗口内无流水」处理（code=0）
 * 数据惯例：两个商品在 afterAll 尽力删除（被订单引用时删除失败仅告警）；本轮自行开工的班次在
 * afterAll 交班关闭，避免给后续套件留下进行中班次；POS 到店单一旦生成不可撤销，
 * 故销售数量固定为 2，所有断言只依赖本轮自身增量
 */

/** 服务器内部错误码（网关对未映射的底层错误统一返回） */
const SERVER_ERROR = 10002;

/** 主商品流水数量：入库 20、报损出库 3、POS 销售 2 */
const IN_QTY = 20;
const OUT_QTY = 3;
const SALE_QTY = 2;
/** 无销售商品的入库数量 */
const IDLE_IN_QTY = 7;

interface SummaryRow {
  sku_id: number;
  sku_code?: string;
  spec_name?: string;
  product_name?: string;
  opening_stock?: number;
  in_quantity?: number;
  out_quantity?: number;
  closing_stock?: number;
  cost_price?: number;
  price?: number;
}

interface ProfitRow {
  sku_id: number;
  sku_code?: string;
  product_name?: string;
  quantity?: number;
  sales_amount?: number;
  cost_amount?: number;
  profit?: number;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let communityId = 0;
let categoryId = 0;
/** 主商品：有入库、报损出库、销售三类流水 */
let mainProductId = 0;
let mainSkuId = 0;
let mainName = '';
/** 无销售商品：只有入库流水，用于验证毛利口径筛掉非销售流水 */
let idleProductId = 0;
let idleSkuId = 0;
let idleName = '';
/** 本轮是否自行开工（是则 afterAll 交班关闭） */
let shiftStartedByMe = false;

/**
 * 金额保留两位小数（与服务端 round2 同口径）
 * - 函数名: round2
 * - 参数: n — 原始数值
 * - 返回: number 四舍五入到分
 * - 异常: 无（非数值原样返回，由断言暴露）
 * - 使用示例: round2(25.799) === 25.8
 */
function round2(n: number): number {
  return Math.round(n * 100) / 100;
}

/**
 * 取 YYYY-MM-DD 日期串（本地时区，与服务端 time.Now().Format 同口径）
 * - 函数名: dayString
 * - 参数: offset — 相对今天的偏移天数（负数为过去）
 * - 返回: string，如 '2026-10-18'
 * - 异常: 无
 * - 使用示例: dayString(-30) 取 30 天前
 */
function dayString(offset: number): string {
  const d = new Date();
  d.setDate(d.getDate() + offset);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/**
 * 创建单规格商品并置为在售
 * - 函数名: createSku
 * - 参数: label — 名称标识；price — 售价；cost — 成本价；stock — 初始库存
 * - 返回: Promise<{ productId, skuId, name }>
 * - 异常: 建品、取不到 SKU 或置在售失败时抛出 Error（仅造数前置调用）
 * - 使用示例: const { skuId } = await createSku('主', 12.9, 6.5, 0);
 */
async function createSku(label: string, price: number, cost: number, stock: number): Promise<{ productId: number; skuId: number; name: string }> {
  const name = autoName(`报表商品${label}`);
  const created = assertOk<{ product?: { id: number }; id?: number }>(
    (await (
      await adminApi.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name,
          unit: '份',
          is_multi_spec: false,
          sku_items: [{ price, cost_price: cost, stock, safety_stock: 0 }],
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
  const skuId = asList<{ id: number }>(skus)[0]?.id ?? 0;
  if (!skuId) throw new Error(`商品 ${productId} 未生成 SKU`);

  const on = (await (await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })).json()) as ApiResponse;
  if (on.code !== 0) throw new Error(`商品 ${productId} 置为在售失败 code=${on.code} message=${on.message}`);
  return { productId, skuId, name };
}

/**
 * 读取进销存汇总（管理端默认社区语境）
 * - 函数名: readSummary
 * - 参数: params — query 参数；ctx — 请求上下文（缺省用 beforeAll 的管理端上下文）
 * - 返回: Promise<PageData<SummaryRow>>
 * - 异常: code !== 0 时由 assertOk 抛出含 message 的 Error
 * - 使用示例: const rows = asList<SummaryRow>(await readSummary({ keyword: '张三' }));
 */
async function readSummary(params: Record<string, string | number> = {}, ctx: APIRequestContext = adminApi): Promise<PageData<SummaryRow>> {
  const body = (await (await ctx.get('/api/v1/admin/inventory/reports/summary', { params })).json()) as ApiResponse<PageData<SummaryRow>>;
  return assertOk(body, '进销存汇总');
}

/**
 * 读取毛利分析
 * - 函数名: readProfit
 * - 参数: params — query 参数；ctx — 请求上下文（缺省用管理端上下文）
 * - 返回: Promise<PageData<ProfitRow>>
 * - 异常: code !== 0 时由 assertOk 抛出含 message 的 Error
 */
async function readProfit(params: Record<string, string | number> = {}, ctx: APIRequestContext = adminApi): Promise<PageData<ProfitRow>> {
  const body = (await (await ctx.get('/api/v1/admin/inventory/reports/profit', { params })).json()) as ApiResponse<PageData<ProfitRow>>;
  return assertOk(body, '毛利分析');
}

/**
 * 取报表中指定 SKU 的行
 * - 函数名: rowOfSku
 * - 参数: list — 报表行；skuId — SKU ID
 * - 返回: T | undefined（未命中返回 undefined，由调用方断言）
 * - 异常: 无
 */
function rowOfSku<T extends { sku_id: number }>(list: T[], skuId: number): T | undefined {
  return list.find((r) => r.sku_id === skuId);
}

/**
 * 原始响应读取（不走 assertOk，供负向断言使用）
 * - 函数名: rawGet
 * - 参数: path — 接口路径；params — query 参数
 * - 返回: Promise<ApiResponse>
 * - 异常: 无
 */
async function rawGet(path: string, params: Record<string, string | number>): Promise<ApiResponse> {
  return (await (await adminApi.get(path, { params })).json()) as ApiResponse;
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  communityId = admin.communityId;

  try {
    categoryId = assertOk<{ id: number }>(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('报表分类') } })).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    ).id;

    // 初始库存 0：全部流水都由本用例显式制造，期初/入/出/期末可逐列核对
    const main = await createSku('主', 12.9, 6.5, 0);
    mainProductId = main.productId;
    mainSkuId = main.skuId;
    mainName = main.name;

    const idle = await createSku('无销售', 5.5, 2.2, 0);
    idleProductId = idle.productId;
    idleSkuId = idle.skuId;
    idleName = idle.name;

    const must = (resp: ApiResponse, label: string) => {
      assertOk(resp, label);
    };
    // 入库：生成 type=1 流水（ref_no=入库单号）
    must(
      (await (await adminApi.post('/api/v1/admin/inventory/in-orders', {
        data: { items: [{ sku_id: mainSkuId, quantity: IN_QTY, unit_cost: 6.5 }], remark: autoName('报表入库') },
      })).json()) as ApiResponse,
      '主商品入库',
    );
    must(
      (await (await adminApi.post('/api/v1/admin/inventory/in-orders', {
        data: { items: [{ sku_id: idleSkuId, quantity: IDLE_IN_QTY, unit_cost: 2.2 }], remark: autoName('报表入库') },
      })).json()) as ApiResponse,
      '无销售商品入库',
    );
    // 报损出库：生成 type=2 流水但 ref_no=出库单号，按口径不计入毛利
    must(
      (await (await adminApi.post('/api/v1/admin/inventory/out-orders', {
        data: { type: 1, items: [{ sku_id: mainSkuId, quantity: OUT_QTY }], remark: autoName('报表报损') },
      })).json()) as ApiResponse,
      '主商品报损出库',
    );

    // POS 到店销售：订单扣减流水 type=2 且 ref_no 为空，计入毛利
    const start = (await (await adminApi.post('/api/v1/admin/pos/shifts/start', { data: { note: autoName('报表开工') } })).json()) as ApiResponse;
    shiftStartedByMe = start.code === 0;
    if (!shiftStartedByMe) console.log(`[API67] 开工 code=${start.code} message=${start.message}（复用已有班次）`);

    const sale = (await (await adminApi.post('/api/v1/admin/pos/orders', {
      data: { user_id: 0, pay_type: 4, delivery_type: 3, items: [{ sku_id: mainSkuId, quantity: SALE_QTY }], remark: autoName('报表到店单') },
    })).json()) as ApiResponse;
    assertOk(sale, 'POS 到店单');
  } catch (e) {
    console.warn(`[API67] 造数失败，用例将跳过：${e instanceof Error ? e.message : e}`);
    categoryId = 0;
  }
});

test.afterAll(async () => {
  if (shiftStartedByMe) {
    const end = (await (await adminApi.post('/api/v1/admin/pos/shifts/end', { data: { note: autoName('报表交班') } })).json()) as ApiResponse;
    if (end.code !== 0) console.warn(`[API67] 交班失败 code=${end.code} message=${end.message}`);
  }
  for (const id of [mainProductId, idleProductId]) {
    if (!id) continue;
    const del = (await (await adminApi.delete(`/api/v1/admin/products/${id}`)).json()) as ApiResponse;
    if (del.code !== 0) console.warn(`[API67] 商品 ${id} 清理失败（被订单引用属预期）：${del.message}`);
  }
  if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  await adminApi?.dispose();
});

test.describe.serial('进销存报表', () => {
  test('1. 汇总逐列准确：期初 0 + 入 20 − 出 5 = 期末 15，期末等于 SKU 实时库存', async () => {
    test.skip(!categoryId || !mainSkuId, '造数未完成');
    const row = rowOfSku(asList<SummaryRow>(await readSummary({ keyword: mainName, page_size: 20 })), mainSkuId);
    expect(row, `汇总未命中主商品 SKU ${mainSkuId}`).toBeTruthy();

    expect(row!.opening_stock ?? -1, '期初应为首笔流水前库存').toBe(0);
    expect(row!.in_quantity ?? -1).toBe(IN_QTY);
    expect(row!.out_quantity ?? -1, '出库应含报损出库与销售扣减').toBe(OUT_QTY + SALE_QTY);
    expect(row!.closing_stock ?? -1).toBe(IN_QTY - OUT_QTY - SALE_QTY);
    expect(row!.product_name).toBe(mainName);
    expect(row!.sku_code, 'SKU 编码应回填').toBeTruthy();

    const skus = (await (await adminApi.get('/api/v1/skus', { params: { product_id: mainProductId } })).json()) as ApiResponse<{ id: number; stock: number }[]>;
    const live = asList<{ id: number; stock: number }>(skus.data).find((s) => s.id === mainSkuId);
    expect(row!.closing_stock ?? -1, '期末应等于 SKU 当前库存').toBe(live?.stock ?? -2);
    expect(row!.price ?? -1, '售价回显建品价格').toBeCloseTo(12.9, 2);
    expect(row!.cost_price ?? -1, '成本回显入库单价（移动加权）').toBeCloseTo(6.5, 2);
  });

  test('2. 汇总守恒：本社区每一行都满足 期初 + 入 − 出 = 期末', async () => {
    test.skip(!categoryId || !mainSkuId, '造数未完成');
    const rows = asList<SummaryRow>(await readSummary({ page_size: 200 }));
    expect(rows.length, '报表应返回本社区 SKU 行').toBeGreaterThan(0);
    for (const r of rows) {
      const o = r.opening_stock ?? 0;
      const i = r.in_quantity ?? 0;
      const x = r.out_quantity ?? 0;
      const c = r.closing_stock ?? 0;
      expect(o + i - x, `SKU ${r.sku_id} 不守恒：${o}+${i}-${x}≠${c}`).toBe(c);
    }
  });

  test('3. 关键字命中商品名与 SKU 编码，无命中时返回空集', async () => {
    test.skip(!categoryId || !mainSkuId, '造数未完成');
    const byName = await readSummary({ keyword: mainName });
    expect(asList<SummaryRow>(byName), '按商品名应唯一命中本轮商品').toHaveLength(1);

    const skuCode = (rowOfSku(asList<SummaryRow>(byName), mainSkuId)?.sku_code ?? '').trim();
    expect(skuCode).toBeTruthy();
    const byCode = rowOfSku(asList<SummaryRow>(await readSummary({ keyword: skuCode })), mainSkuId);
    expect(byCode?.sku_code, '按 SKU 编码应命中同一行').toBe(skuCode);

    const miss = await readSummary({ keyword: autoName('报表不存在的商品') });
    expect(asList<SummaryRow>(miss)).toHaveLength(0);
    expect(miss.total ?? -1).toBe(0);
  });

  test('4. 时间窗只筛流水不筛行：窗口外入出为 0，期初=期末=当前库存', async () => {
    test.skip(!categoryId || !mainSkuId, '造数未完成');
    const row = rowOfSku(asList<SummaryRow>(await readSummary({ keyword: mainName, start_date: '2020-01-01', end_date: '2020-01-02' })), mainSkuId);
    expect(row, '窗口外仍应返回该 SKU 行（报表按 SKU 全量出行）').toBeTruthy();
    expect(row!.in_quantity ?? -1).toBe(0);
    expect(row!.out_quantity ?? -1).toBe(0);
    // 窗口内无流水时 opening/closing 走 COALESCE 回退，取的是「当前」库存而非历史快照
    expect(row!.opening_stock, '无流水时期初应回退当前库存，与期末相等').toBe(row!.closing_stock);
    expect(row!.closing_stock ?? -1).toBe(IN_QTY - OUT_QTY - SALE_QTY);
  });

  test('5. 缺省区间等于近 30 天：不传日期与显式传该区间结果一致', async () => {
    test.skip(!categoryId || !mainSkuId, '造数未完成');
    const dflt = rowOfSku(asList<SummaryRow>(await readSummary({ keyword: mainName })), mainSkuId);
    const explicit = rowOfSku(asList<SummaryRow>(await readSummary({ keyword: mainName, start_date: dayString(-30), end_date: dayString(0) })), mainSkuId);
    expect(explicit?.in_quantity ?? -1).toBe(dflt?.in_quantity ?? -2);
    expect(explicit?.out_quantity ?? -1).toBe(dflt?.out_quantity ?? -2);
    expect(explicit?.closing_stock ?? -1).toBe(dflt?.closing_stock ?? -2);

    // 窗口右端收到昨天：今日的入库与出库都应被排除
    const beforeToday = rowOfSku(asList<SummaryRow>(await readSummary({ keyword: mainName, start_date: dayString(-60), end_date: dayString(-1) })), mainSkuId);
    expect(beforeToday?.in_quantity ?? -1, '昨天以前不应含今日入库').toBe(0);
    expect(beforeToday?.out_quantity ?? -1, '昨天以前不应含今日出库').toBe(0);
  });

  test('6. 分页：page/page_size/total 回显、翻页不重叠且按 SKU ID 升序', async () => {
    test.skip(!categoryId || !mainSkuId, '造数未完成');
    const p1 = await readSummary({ page: 1, page_size: 1 });
    const p2 = await readSummary({ page: 2, page_size: 1 });
    expect(p1.page).toBe(1);
    expect(p1.page_size).toBe(1);
    expect(p2.page).toBe(2);
    expect(asList<SummaryRow>(p1)).toHaveLength(1);
    expect(p1.total ?? -1, 'total 应与页大小无关').toBe(p2.total ?? -2);
    expect(p1.total ?? 0, '本轮至少造了 2 个 SKU').toBeGreaterThan(1);

    const id1 = asList<SummaryRow>(p1)[0].sku_id;
    const id2 = asList<SummaryRow>(p2)[0].sku_id;
    expect(id2, '第二页不应与第一页重复').not.toBe(id1);
    expect(id2, '报表按 SKU ID 升序输出').toBeGreaterThan(id1);
  });

  test('7. 毛利口径：只计销售扣减，报损出库不计入，金额三项自洽', async () => {
    test.skip(!categoryId || !mainSkuId, '造数未完成');
    const row = rowOfSku(asList<ProfitRow>(await readProfit({ keyword: mainName, page_size: 20 })), mainSkuId);
    expect(row, '毛利报表应命中本轮销售 SKU').toBeTruthy();
    expect(row!.quantity ?? -1, '毛利数量应只含 POS 销售件数，不含报损出库').toBe(SALE_QTY);

    const summary = rowOfSku(asList<SummaryRow>(await readSummary({ keyword: mainName })), mainSkuId);
    const price = summary?.price ?? 0;
    const cost = summary?.cost_price ?? 0;
    expect(row!.sales_amount ?? -1, '销售额=销售数量×当前售价').toBe(round2(SALE_QTY * price));
    expect(row!.cost_amount ?? -1, '销售成本=销售数量×当前移动加权成本').toBe(round2(SALE_QTY * cost));
    expect(row!.profit ?? -1, '毛利=销售额−销售成本').toBe(round2((row!.sales_amount ?? 0) - (row!.cost_amount ?? 0)));
  });

  test('8. 毛利筛除无销售 SKU：只有入库的商品不进毛利表，但仍在汇总表', async () => {
    test.skip(!categoryId || !idleSkuId, '造数未完成');
    const byIdle = await readProfit({ keyword: idleName, page_size: 20 });
    expect(rowOfSku(asList<ProfitRow>(byIdle), idleSkuId), '无销售流水的 SKU 不应进毛利表').toBeUndefined();
    expect(byIdle.total ?? -1).toBe(0);

    const sum = rowOfSku(asList<SummaryRow>(await readSummary({ keyword: idleName })), idleSkuId);
    expect(sum?.in_quantity ?? -1, '无销售商品的入库应计入汇总').toBe(IDLE_IN_QTY);
    expect(sum?.out_quantity ?? -1).toBe(0);
    expect(sum?.closing_stock ?? -1).toBe(IDLE_IN_QTY);
  });

  test('9. 毛利全量自洽：每行毛利=销售额−销售成本，数量为正且按数量降序', async () => {
    test.skip(!categoryId || !mainSkuId, '造数未完成');
    const rows = asList<ProfitRow>(await readProfit({ page_size: 200 }));
    expect(rows.length, '本轮销售后毛利报表应至少有一行').toBeGreaterThan(0);
    let prev = Number.POSITIVE_INFINITY;
    for (const r of rows) {
      expect(r.quantity ?? -1, `SKU ${r.sku_id} 销售数量应为正`).toBeGreaterThan(0);
      expect(r.profit ?? 0, `SKU ${r.sku_id} 毛利不平`).toBe(round2((r.sales_amount ?? 0) - (r.cost_amount ?? 0)));
      expect(r.quantity ?? 0, '毛利报表按销售数量降序').toBeLessThanOrEqual(prev);
      prev = r.quantity ?? 0;
    }
  });

  test('10. 社区隔离：切换社区语境后查不到本社区 SKU 的报表行', async ({ playwright }) => {
    test.skip(!categoryId || !mainSkuId, '造数未完成');
    const info = await adminLogin(playwright.request);
    const probe = await newAuthedRequest(playwright, info.access_token);
    const body = (await (await probe.get('/api/v1/admin/my/communities')).json()) as ApiResponse<{ id: number }[]>;
    await probe.dispose();
    const other = asList<{ id: number }>(body.data).find((c) => c.id !== communityId);
    test.skip(!other, '环境缺少第二个社区，无法验证跨社区隔离');

    const ctx = await playwright.request.newContext({
      extraHTTPHeaders: { Authorization: `Bearer ${info.access_token}`, 'X-Community-Id': String(other!.id) },
    });
    try {
      expect(asList<SummaryRow>(await readSummary({ keyword: mainName }, ctx)), '他社区不应看到本社区 SKU 的汇总行').toHaveLength(0);
      expect(asList<ProfitRow>(await readProfit({ keyword: mainName }, ctx)), '他社区不应看到本社区 SKU 的毛利行').toHaveLength(0);
    } finally {
      await ctx.dispose();
    }
  });

  test('11. 现状锁定：日期参数不校验，非法日期与起止倒挂均静默按「窗口内无流水」处理', async () => {
    test.skip(!categoryId || !mainSkuId, '造数未完成');
    const garbage = rowOfSku(asList<SummaryRow>(await readSummary({ keyword: mainName, start_date: 'abc', end_date: 'def' })), mainSkuId);
    expect(garbage, '日期非法仍应出行（报表按 SKU 全量出行，日期只约束流水）').toBeTruthy();
    expect(garbage!.in_quantity ?? -1, '非法日期等价于窗口内无流水').toBe(0);
    expect(garbage!.opening_stock, '非法日期时期初回退当前库存').toBe(garbage!.closing_stock);

    const reversed = rowOfSku(asList<SummaryRow>(await readSummary({ keyword: mainName, start_date: dayString(0), end_date: dayString(-1) })), mainSkuId);
    expect(reversed?.in_quantity ?? -1, '起止倒挂等价于窗口内无流水').toBe(0);

    const profit = await readProfit({ keyword: mainName, start_date: 'abc' });
    expect(asList<ProfitRow>(profit), '非法日期下毛利报表应为空（无销售流水可比对）').toHaveLength(0);
  });

  test('12. 现状锁定：page=0 触发负 OFFSET 报 10002；page_size=0 返回空列表但 total 正常', async () => {
    test.skip(!categoryId || !mainSkuId, '造数未完成');
    // 缺陷：offset=(page-1)*pageSize 未做下限保护，MySQL 收到 OFFSET -10 直接报错，
    // 对外表现为 10002 服务器内部错误（应为 10001 参数错误）。两报表接口共用该分页构造，行为一致。
    // 已写入待办清单缺陷区，待决策后修复；本轮锁定现状以免回归时漏掉。
    const zeroPage = await rawGet('/api/v1/admin/inventory/reports/summary', { page: 0, page_size: 10 });
    expect(zeroPage.code, 'page=0 现状返回内部错误码').toBe(SERVER_ERROR);
    const profitZeroPage = await rawGet('/api/v1/admin/inventory/reports/profit', { page: 0 });
    expect(profitZeroPage.code, '毛利报表 page=0 同样报内部错误').toBe(SERVER_ERROR);

    const zeroSize = (await (await adminApi.get('/api/v1/admin/inventory/reports/summary', { params: { page: 1, page_size: 0 } })).json()) as ApiResponse<PageData<SummaryRow>>;
    expect(zeroSize.code).toBe(0);
    expect(asList<SummaryRow>(zeroSize.data), 'page_size=0 时 LIMIT 0 返回空列表').toHaveLength(0);
    expect(zeroSize.data?.total ?? -1, 'page_size=0 时 total 仍应回显').toBeGreaterThan(0);
  });
});
