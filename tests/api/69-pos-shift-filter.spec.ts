import { test, expect } from '@playwright/test';
import type { APIRequestContext, Playwright } from '@playwright/test';
import {
  newAdminContext,
  newAuthedRequest,
  clientLogin,
  pickSku,
  ensureStock,
  assertOk,
  asList,
  type ApiResponse,
  type PageData,
  type SkuRef,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 69：收银班次过滤 GET /api/v1/admin/orders?shift_id=
 * 覆盖（对应交班记录「查看班内订单」入口）：
 *   - 开工 A → 班内下单 → 交班 A → 开工 B → 班内下单 → 交班 B，两个班次结果集互斥
 *   - 列表行 shift_id 回显与订单归属一致；C 端/非收银单不落入任一班次
 *   - 班次过滤与状态过滤可叠加（shift_id + status=5 只留已完结到店单）
 *   - shift_id=0 与不传等价：不过滤
 *   - 不存在的班次 ID 返回空集（不报错、不回落全量）
 *   - 交班后班次仍可查（历史班次不因交班而失效）
 * 数据惯例：POS 到店单不可撤销，本轮固定 2 笔（A 班 1 件、B 班 2 件，叠加过滤复用 B 班单）；
 * beforeAll 先关闭遗留班次保证从干净班次开始，afterAll 兜底交班避免给后续套件留下进行中班次；
 * 用例 9 的 C 端待支付单当场取消，不留下悬挂订单
 */

interface ShiftInfo {
  id: number;
  status: number;
  start_time?: string;
  end_time?: string;
  [k: string]: unknown;
}

interface PosOrderResult {
  id: number;
  order_no: string;
  pay_amount: number;
  [k: string]: unknown;
}

interface AdminOrderRow {
  id: number;
  order_no: string;
  status: number;
  source: number;
  shift_id?: number;
  [k: string]: unknown;
}

let adminApi: APIRequestContext;
let communityId = 0;
let sku: SkuRef | null = null;

/**
 * 查询当前进行中班次
 * - 函数名: currentShift
 * - 参数: 无（依赖管理端上下文社区语境）
 * - 返回: Promise<ShiftInfo | null> 无进行中班次（业务码非 0 或 data 缺失）时返回 null
 * - 异常: 无（"无班次"是本模块的正常状态，不作失败抛出）
 * - 使用示例: const s = await currentShift();
 */
async function currentShift(): Promise<ShiftInfo | null> {
  const body = (await (await adminApi.get('/api/v1/admin/pos/shifts/current')).json()) as ApiResponse<ShiftInfo>;
  return body.code === 0 ? body.data ?? null : null;
}

/**
 * 开工新班次并回读其 ID
 * - 函数名: startShift
 * - 参数: note — 开工备注
 * - 返回: Promise<number> 班次 ID（开工接口未直接回显 id 时以 current 为准）
 * - 异常: 已有进行中班次或开工失败时抛出 Error
 */
async function startShift(note: string): Promise<number> {
  const resp = (await (await adminApi.post('/api/v1/admin/pos/shifts/start', { data: { note } })).json()) as ApiResponse<ShiftInfo>;
  assertOk(resp, `开工(${note})`);
  const id = (await currentShift())?.id ?? 0;
  if (!id) throw new Error(`开工后 current 未返回班次 ID：${JSON.stringify(resp.data)}`);
  return id;
}

/**
 * 交班（尽力而为，用于前置清理）
 * - 函数名: endShift
 * - 参数: note — 交班备注
 * - 返回: Promise<number> 业务码（0=成功，非 0=无进行中班次等，交由用例断言处理）
 * - 异常: 无
 */
async function endShift(note: string): Promise<number> {
  const resp = (await (await adminApi.post('/api/v1/admin/pos/shifts/end', { data: { note } })).json()) as ApiResponse<ShiftInfo>;
  return resp.code;
}

/**
 * 收银台散客到店单
 * - 函数名: posSale
 * - 参数: quantity — 件数（同社区连续多单错开数量便于区分）
 * - 返回: Promise<PosOrderResult>
 * - 异常: 下单业务失败时经 assertOk 抛出
 * - 使用示例: const order = await posSale(2);
 */
async function posSale(quantity: number): Promise<PosOrderResult> {
  const body = (await (
    await adminApi.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: 0,
        pay_type: 4,
        delivery_type: 3,
        items: [{ sku_id: sku!.id, quantity }],
        remark: autoName('69 班次单'),
      },
    })
  ).json()) as ApiResponse<PosOrderResult>;
  const data = assertOk(body, 'POS 到店单');
  return data.order ?? data;
}

/**
 * 按班次查订单列表
 * - 函数名: listByQuery
 * - 参数: params — query 参数（shift_id / source / status / page_size 等）
 * - 返回: Promise<PageData<AdminOrderRow>>
 * - 异常: 业务码非 0 时经 assertOk 抛出
 * - 使用示例: const rows = asList(await listByQuery({ shift_id: shiftA }));
 */
async function listByQuery(params: Record<string, string | number>): Promise<PageData<AdminOrderRow>> {
  const body = (await (await adminApi.get('/api/v1/admin/orders', { params })).json()) as ApiResponse<PageData<AdminOrderRow>>;
  return assertOk(body, `订单列表(${JSON.stringify(params)})`);
}

/**
 * 取一个绑定到指定社区 C 端买家令牌
 * 新注册号默认落在社区列表首个社区，与本轮管理端语境可能不同，故显式改绑后重登（JWT 需刷新社区）
 * - 函数名: buyerTokenIn
 * - 参数: playwright — Playwright 工厂；target — 目标社区 ID
 * - 返回: Promise<string> 访问令牌
 * - 异常: 登录失败或改绑失败时经 assertOk 抛出
 * - 使用示例: const token = await buyerTokenIn(playwright, communityId);
 */
async function buyerTokenIn(playwright: Playwright, target: number): Promise<string> {
  const phone = uniquePhone();
  const buyer = await clientLogin(playwright.request, phone);
  if (buyer.user.community_id === target) return buyer.access_token;

  const ctx = await newAuthedRequest(playwright, buyer.access_token);
  try {
    assertOk(
      (await (await ctx.put('/api/v1/user/community', { data: { community_id: target } })).json()) as ApiResponse,
      `改绑社区(${target})`,
    );
  } finally {
    await ctx.dispose();
  }

  // 改绑发生在登录之后，JWT 内社区仍是旧值，需重登刷新
  const relogin = await clientLogin(playwright.request, phone);
  if (relogin.user.community_id !== target) {
    throw new Error(`买家社区改绑失败：期望 ${target}，实际 ${relogin.user.community_id}`);
  }
  return relogin.access_token;
}

/**
 * C 端创建待支付自提单（非收银台单，shift_id 应为 0）
 * - 函数名: createClientOrder
 * - 参数: playwright — Playwright 工厂；token — 买家令牌；quantity — 件数
 * - 返回: Promise<{ id: number; order_no: string }>
 * - 异常: 下单业务失败时经 assertOk 抛出
 */
async function createClientOrder(playwright: Playwright, token: string, quantity: number): Promise<{ id: number; order_no: string }> {
  const buyerApi = await newAuthedRequest(playwright, token);
  try {
    return assertOk<{ id: number; order_no: string }>(
      (await (
        await buyerApi.post('/api/v1/orders', {
          data: { delivery_type: 1, address_id: 0, remark: autoName('69 C端单'), items: [{ sku_id: sku!.id, quantity }] },
        })
      ).json()) as ApiResponse<{ id: number; order_no: string }>,
      'C 端下单',
    );
  } finally {
    await buyerApi.dispose();
  }
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  communityId = admin.communityId;
  try {
    sku = await pickSku(adminApi, undefined, admin.communityId);
    await ensureStock(adminApi, sku.id, 100);
  } catch {
    sku = null;
  }
  // 关闭遗留班次，保证本模块从「无进行中班次」开始
  const cur = await currentShift().catch(() => null);
  if ((cur?.id ?? 0) > 0) console.log(`[API69] 清理遗留班次 code=${await endShift(autoName('69 清理遗留'))}`);
});

test.afterAll(async () => {
  try {
    const cur = await currentShift().catch(() => null);
    if ((cur?.id ?? 0) > 0) await endShift(autoName('69 收尾交班'));
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

test.describe.serial('收银班次过滤', () => {
  let shiftA = 0;
  let shiftB = 0;
  let orderA: PosOrderResult | null = null;
  let orderB: PosOrderResult | null = null;

  test('1. 开工班次 A', async () => {
    test.skip(!sku, '环境无库存 SKU');
    shiftA = await startShift(autoName('69 开工A'));
    expect(shiftA, '开工应返回班次 ID').toBeGreaterThan(0);
    expect((await currentShift())?.status, '进行中班次状态应为 1').toBe(1);
  });

  test('2. A 班内下 1 单：列表 shift_id 回显归属班次', async () => {
    test.skip(!sku || !shiftA, '班次 A 未开工');
    orderA = await posSale(1);
    expect(orderA.id).toBeGreaterThan(0);

    const page = await listByQuery({ shift_id: shiftA, page_size: 50 });
    const rows = asList<AdminOrderRow>(page);
    expect(rows.map((r) => r.order_no), '班次 A 过滤应命中本单').toContain(orderA.order_no);
    expect(rows.every((r) => (r.shift_id ?? 0) === shiftA), '回显行的 shift_id 应全等于查询班次').toBe(true);
    expect(rows.every((r) => r.source === 2), '班次内订单来源应为收银台(2)').toBe(true);
  });

  test('3. 交班 A：班次冻结且历史订单仍可查', async () => {
    if (!shiftA) test.skip(true, '班次 A 未开工');
    const ended = (await (
      await adminApi.post('/api/v1/admin/pos/shifts/end', { data: { note: autoName('69 交班A') } })
    ).json()) as ApiResponse<ShiftInfo>;
    expect(ended.code, `交班 A 失败: ${ended.message}`).toBe(0);
    expect(ended.data?.status, '交班后班次应为已结束(2)').toBe(2);
    expect(ended.data?.end_time, '交班时间应落库').toBeTruthy();

    const page = await listByQuery({ shift_id: shiftA, page_size: 50 });
    expect(asList<AdminOrderRow>(page).map((r) => r.order_no), '交班后历史班次订单仍应可查').toContain(orderA?.order_no);
    expect((await currentShift())?.id ?? 0, '交班后不应存在进行中班次').toBe(0);
  });

  test('4. 开工班次 B 并下单：新班次 ID 与 A 不同', async () => {
    test.skip(!sku || !shiftA, '前置班次 A 未就绪');
    shiftB = await startShift(autoName('69 开工B'));
    expect(shiftB, '重新开工应生成新班次').not.toBe(shiftA);

    orderB = await posSale(2);
    const rows = asList<AdminOrderRow>(await listByQuery({ shift_id: shiftB, page_size: 50 }));
    expect(rows.map((r) => r.order_no), '班次 B 过滤应命中本单').toContain(orderB.order_no);
  });

  test('5. 结果集互斥：A 班不含 B 班单，B 班不含 A 班单', async () => {
    if (!shiftA || !shiftB || !orderA || !orderB) test.skip(true, '两个班次或订单未就绪');
    const a = asList<AdminOrderRow>(await listByQuery({ shift_id: shiftA, page_size: 50 }));
    const b = asList<AdminOrderRow>(await listByQuery({ shift_id: shiftB, page_size: 50 }));

    expect(a.map((r) => r.order_no), 'A 班结果集只含 A 班单').toEqual([orderA.order_no]);
    expect(b.map((r) => r.order_no), 'B 班结果集只含 B 班单').toEqual([orderB.order_no]);
    expect(a.some((r) => r.order_no === orderB.order_no), 'A 班不得混入 B 班单').toBe(false);
    expect(b.some((r) => r.order_no === orderA.order_no), 'B 班不得混入 A 班单').toBe(false);
    expect(a.every((r) => (r.shift_id ?? 0) === shiftA), 'A 班行 shift_id 应全部等于 A').toBe(true);
    expect(b.every((r) => (r.shift_id ?? 0) === shiftB), 'B 班行 shift_id 应全部等于 B').toBe(true);
  });

  test('6. 与状态过滤叠加：shift_id + status=5 只留已完结到店单', async () => {
    if (!shiftB || !orderB) test.skip(true, '班次 B 未就绪');
    const rows = asList<AdminOrderRow>(await listByQuery({ shift_id: shiftB, status: 5, page_size: 50 }));
    expect(rows.map((r) => r.order_no), '到店现金单收款即完结，应命中 status=5 过滤').toEqual([orderB.order_no]);
    expect(rows[0]?.status).toBe(5);

    const none = await listByQuery({ shift_id: shiftB, status: 1, page_size: 50 });
    expect(asList<AdminOrderRow>(none), '班次内无待支付单，叠加过滤应返回空集').toHaveLength(0);
    expect(none.total ?? -1).toBe(0);
  });

  test('7. shift_id=0 与不传等价：不过滤且两班订单同现', async () => {
    if (!shiftA || !shiftB || !orderA || !orderB) test.skip(true, '两个班次或订单未就绪');
    const zeroPage = await listByQuery({ shift_id: 0, page_size: 100 });
    const plainPage = await listByQuery({ page_size: 100 });
    const nos = (rows: AdminOrderRow[]) => rows.map((r) => r.order_no);
    const zero = asList<AdminOrderRow>(zeroPage);
    const plain = asList<AdminOrderRow>(plainPage);

    expect(nos(zero), 'shift_id=0 应不过滤，两班订单同现').toContain(orderA.order_no);
    expect(nos(zero)).toContain(orderB.order_no);
    expect(nos(zero).length, 'shift_id=0 与不传结果集大小一致').toBe(nos(plain).length);
    expect(zeroPage.total ?? -1, 'shift_id=0 的 total 应与不传一致').toBe(plainPage.total ?? -2);
    expect(zeroPage.total ?? 0, '不过滤时 total 至少含本轮两单').toBeGreaterThanOrEqual(2);
  });

  test('8. 未知班次 ID 返回空集，不报错也不回落全量', async () => {
    if (!shiftA) test.skip(true, '班次 A 未开工');
    const body = (await (
      await adminApi.get('/api/v1/admin/orders', { params: { shift_id: 9_999_999, page_size: 20 } })
    ).json()) as ApiResponse<PageData<AdminOrderRow>>;
    expect(body.code, '未知班次 ID 应正常返回').toBe(0);
    expect(asList<AdminOrderRow>(body.data), '未知班次 ID 应为空集').toHaveLength(0);
    expect(body.data?.total ?? -1).toBe(0);
  });

  test('9. 非收银单不落入班次过滤：C 端自提单 shift_id 为 0 且不出现在任一班次', async ({ playwright }) => {
    test.skip(!shiftA || !shiftB || !sku, '两个班次或 SKU 未就绪');
    const token = await buyerTokenIn(playwright, communityId);
    const clientOrder = await createClientOrder(playwright, token, 3);

    const all = asList<AdminOrderRow>(await listByQuery({ page_size: 100 }));
    const row = all.find((r) => r.order_no === clientOrder.order_no);
    expect(row, '不过滤列表应含刚创建的 C 端单').toBeTruthy();
    expect(row!.source, '来源应为 C 端(1)').toBe(1);
    expect(row!.shift_id ?? 0, 'C 端单不归属任何收银班次').toBe(0);

    const inA = asList<AdminOrderRow>(await listByQuery({ shift_id: shiftA, page_size: 100 }));
    const inB = asList<AdminOrderRow>(await listByQuery({ shift_id: shiftB, page_size: 100 }));
    expect(inA.some((r) => r.order_no === clientOrder.order_no), 'A 班不得混入 C 端单').toBe(false);
    expect(inB.some((r) => r.order_no === clientOrder.order_no), 'B 班不得混入 C 端单').toBe(false);

    const sourceRows = asList<AdminOrderRow>(await listByQuery({ source: 1, page_size: 100 }));
    expect(sourceRows.map((r) => r.order_no), '来源过滤应命中该 C 端单').toContain(clientOrder.order_no);
    expect(sourceRows.every((r) => (r.shift_id ?? 0) === 0), '来源过滤结果集内全部为无班次订单').toBe(true);

    // 待支付单当场取消，不留悬挂订单（取消后仍不归属任何班次）
    const buyerApi = await newAuthedRequest(playwright, token);
    try {
      const cancelled = (await (
        await buyerApi.put(`/api/v1/orders/${clientOrder.id}/cancel`, { data: { reason: autoName('69 清理') } })
      ).json()) as ApiResponse;
      expect(cancelled.code, `C 端取消待支付单失败: ${cancelled.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
    const after = asList<AdminOrderRow>(await listByQuery({ shift_id: shiftB, page_size: 100 }));
    expect(after.some((r) => r.order_no === clientOrder.order_no), '已取消的 C 端单仍不应出现在班次结果集').toBe(false);
  });
});
