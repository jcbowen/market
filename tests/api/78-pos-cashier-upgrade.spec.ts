import { test, expect, type Playwright, type APIRequestContext } from '@playwright/test';
import {
  newAdminContext,
  clientLogin,
  newAuthedRequest,
  pickSku,
  ensureStock,
  assertOk,
  type ApiResponse,
  type SkuRef,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 78：收银台交互升级（直接收款 + 待支付单继续收款）
 * 口径（2026-10-25）：
 *   - 直接收款：manual_amount>0 建无商品到店单（source=2），金额即成交金额，
 *     不参与券/满减/库存；现金收款创建即完结（到店单自动 2→5）；
 *   - manual_amount<=0、非到店 delivery_type、无 items 且无 manual_amount 均拒绝；
 *   - 待支付收银台单（付款码两步化第一步）可继续收款：
 *     pay-cash 现金收款（后端关遗留待支付支付单后建现金单）；
 *     pay-code-session 重建付款码会话（同渠道同金额复用，out_trade_no 不变）；
 *   - C 端单（source=1）与非待支付单不允许收银台代收。
 * 渠道缺省口径：环境未配置第三方付款码凭据时，用例 3/4 不 skip，
 *   改测同段代码的降级契约（只下发现金 + 建单前精确拒绝 + 完结单继续收款被拒）。
 * 数据惯例：直接收款单留档不删除；班次 beforeAll/afterAll 兜底关闭，避免与 14/44/69/77 互踩。
 * 运行前置：本 spec 会开启/交回全局唯一班次，必须串行运行（playwright.config 已全局 workers=1，
 *   本 spec 再叠加 describe.serial 保证用例内班次语境不被自身并发打断）。
 *   用例 5 会留下一张 C 端待支付单（不取消，按 AUTO-TEST 备注识别）。
 */

/** 订单状态：1待支付 5已完成 */
const ORDER_STATUS = { pending: 1, completed: 5 } as const;

/** 业务错误码：参数错误 / 操作不允许 */
const PARAM_ERROR = 10001;
const OPERATION_NOT_ALLOWED = 10004;

interface PosOrderResult {
  id: number;
  order_no: string;
  pay_amount: number;
  pickup_code?: string;
  paid: boolean;
  payment_no?: string;
}

interface PayMethod {
  pay_type: number;
  label: string;
}

interface OrderDetailInfo {
  id: number;
  order_no: string;
  status: number;
  source?: number;
  pay_amount: number;
  pay_type?: number;
  pay_type_text?: string;
  items?: Array<{ id: number; sku_id: number; quantity: number }>;
}

let adminApi: APIRequestContext;
let communityId = 0;
let sku: SkuRef | null = null;

/**
 * 创建一笔直接收款单（无商品，manual_amount 模式）
 * - 函数名: createDirectPayOrder
 * - 参数: payType — 支付方式（4=现金 / 5|6=付款码占位）；amount — 收款金额
 * - 返回: Promise<ApiResponse<PosOrderResult>>（业务码交调用方断言）
 * - 异常: 无
 * - 使用示例: const body = await createDirectPayOrder(4, 8.8);
 */
async function createDirectPayOrder(payType: number, amount: number): Promise<ApiResponse<PosOrderResult>> {
  return (await (
    await adminApi.post('/api/v1/admin/pos/orders', {
      data: { user_id: 0, pay_type: payType, delivery_type: 3, manual_amount: amount, remark: autoName('78 直接收款') },
    })
  ).json()) as ApiResponse<PosOrderResult>;
}

/**
 * 读取收银台可用支付方式（付款码占位值的数据源）
 * - 函数名: listPosPayMethods
 * - 参数: 无
 * - 返回: Promise<PayMethod[]>，现金恒在，付款码选项按渠道配置动态下发
 * - 异常: 查询业务失败时经 assertOk 抛出
 * - 使用示例: const methods = await listPosPayMethods();
 */
async function listPosPayMethods(): Promise<PayMethod[]> {
  return assertOk<{ list: PayMethod[] }>(
    (await (await adminApi.get('/api/v1/admin/pos/pay-methods')).json()) as ApiResponse<{ list: PayMethod[] }>,
    '支付方式查询',
  ).list;
}

/**
 * 从支付方式列表取付款码占位 pay_type（5=微信/双渠道，6=仅支付宝）
 * - 函数名: codePayTypeOf
 * - 参数: methods — listPosPayMethods 结果
 * - 返回: number，环境未配置任何付款码渠道时返回 0
 * - 异常: 无
 * - 使用示例: const payType = codePayTypeOf(await listPosPayMethods());
 */
function codePayTypeOf(methods: PayMethod[]): number {
  return methods.find((m) => m.pay_type === 5 || m.pay_type === 6)?.pay_type ?? 0;
}

/**
 * 读取订单详情（管理端）
 * - 函数名: getOrderDetailInfo
 * - 参数: id — 订单 ID
 * - 返回: Promise<OrderDetailInfo>
 * - 异常: 查询业务失败时经 assertOk 抛出
 * - 使用示例: const d = await getOrderDetailInfo(orderId);
 */
async function getOrderDetailInfo(id: number): Promise<OrderDetailInfo> {
  return assertOk<OrderDetailInfo>(
    (await (await adminApi.get(`/api/v1/admin/orders/${id}`)).json()) as ApiResponse<OrderDetailInfo>,
    '订单详情查询',
  );
}

/**
 * 取一个绑定到管理端语境社区的 C 端买家（新建会员，必要时改绑社区后重登刷新 JWT 社区语境）
 * - 函数名: memberIn
 * - 参数: playwright — Playwright 工厂；target — 目标社区 ID
 * - 返回: Promise<{ token: string; userId: number }> 令牌与会员 ID
 * - 异常: 登录或改绑失败时抛出 Error
 * - 使用示例: const buyer = await memberIn(playwright, communityId);
 */
async function memberIn(playwright: Playwright, target: number): Promise<{ token: string; userId: number }> {
  const phone = uniquePhone();
  const first = await clientLogin(playwright.request, phone);
  if (first.user.community_id === target) return { token: first.access_token, userId: first.user.id };

  const ctx = await newAuthedRequest(playwright, first.access_token);
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
  return { token: relogin.access_token, userId: relogin.user.id };
}

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  communityId = admin.communityId;
  // 用例 5 需真实 C 端待支付单：预取在售 SKU 并备足库存，失败则相关断言按前置缺失跳过
  try {
    sku = await pickSku(adminApi, undefined, communityId);
    await ensureStock(adminApi, sku.id, 200);
  } catch {
    sku = null;
  }
  // 班次兜底：关闭遗留班次后重新开工（收银台单必须归属进行中班次）
  const cur = (await (await adminApi.get('/api/v1/admin/pos/shifts/current')).json()) as ApiResponse<{ id?: number }>;
  if ((cur.data?.id ?? 0) > 0) {
    await adminApi.post('/api/v1/admin/pos/shifts/end', { data: { note: autoName('78 清理遗留班次') } });
  }
  const start = (await (
    await adminApi.post('/api/v1/admin/pos/shifts/start', { data: { note: autoName('78 开工') } })
  ).json()) as ApiResponse<{ id?: number }>;
  expect(start.code, `开工失败: ${start.message}`).toBe(0);
});

test.afterAll(async () => {
  // 交班恢复环境默认（无进行中班次），避免与 14/44/69/77 互踩
  if (adminApi) {
    await adminApi.post('/api/v1/admin/pos/shifts/end', { data: { note: autoName('78 收尾交班') } });
    await adminApi.dispose();
  }
});

test.describe.serial('收银台直接收款', () => {
  test('1. 现金直接收款：创建即完结，到店单自动已完成且无商品明细', async () => {
    const body = await createDirectPayOrder(4, 8.8);
    expect(body.code, `现金直接收款失败: ${body.message}`).toBe(0);
    expect(body.data?.paid, '现金收款应创建即完成').toBe(true);
    expect(body.data?.pay_amount, '成交金额应等于输入金额').toBe(8.8);

    const d = await getOrderDetailInfo(body.data!.id);
    expect(d.status, '到店单收款后应自动完结为已完成').toBe(ORDER_STATUS.completed);
    expect(d.source, '应记录为收银台来源').toBe(2);
    expect(d.pay_type, '支付方式应为现金(4)').toBe(4);
    expect(d.items ?? [], '直接收款单不应有商品明细').toHaveLength(0);
  });

  test('2. 参数校验：0 元 / 非到店 / 无明细无金额均拒绝', async () => {
    const zero = await createDirectPayOrder(4, 0);
    expect(zero.code, '0 元直接收款应拒绝').toBe(PARAM_ERROR);

    const notStore = (await (
      await adminApi.post('/api/v1/admin/pos/orders', {
        data: { user_id: 0, pay_type: 4, delivery_type: 1, manual_amount: 5 },
      })
    ).json()) as ApiResponse;
    expect(notStore.code, '直接收款仅支持到店').toBe(PARAM_ERROR);

    const empty = (await (
      await adminApi.post('/api/v1/admin/pos/orders', {
        data: { user_id: 0, pay_type: 4, delivery_type: 3, items: [] },
      })
    ).json()) as ApiResponse;
    expect(empty.code, '无明细且无金额应拒绝').toBe(PARAM_ERROR);
  });

  test('3. 付款码待支付直接收款单可改现金：pay-cash 后订单完结且支付方式为现金', async () => {
    // 付款码渠道按环境配置可用性选择占位值：优先 5（微信/双渠道），仅支付宝就绪时用 6
    const methods = await listPosPayMethods();
    const codePayType = codePayTypeOf(methods);
    if (codePayType === 0) {
      // 环境无第三方付款码凭据（本地测试服常态）：不 skip，改测同一段代码的降级契约——
      // 支付方式只下发「现金」，付款码下单在渠道前置校验处被精确拒绝（P1-8：校验先于建单，不留孤儿待支付单）。
      // 若跳过，渠道配置读取写反（例如恒判可用）也会显示绿
      expect(methods.map((m) => m.pay_type), '付款码渠道未就绪时收银台应只保留现金').toEqual([4]);
      const rejected = await createDirectPayOrder(5, 6.6);
      expect(rejected.code, '付款码渠道未就绪应拒绝下单').toBe(OPERATION_NOT_ALLOWED);
      expect(rejected.message).toContain('请使用现金收款');
      return;
    }

    const pending = await createDirectPayOrder(codePayType, 6.6);
    expect(pending.code, `付款码直接收款下单失败: ${pending.message}`).toBe(0);
    expect(pending.data?.paid, '付款码应为两步化第一步（待支付）').toBe(false);
    expect(pending.data?.payment_no, '应返回待支付支付单号').toBeTruthy();
    expect((await getOrderDetailInfo(pending.data!.id)).status).toBe(ORDER_STATUS.pending);

    // 扣款失败换现金：pay-cash 关遗留待支付单后收现金
    const cash = (await (
      await adminApi.post(`/api/v1/admin/pos/orders/${pending.data!.id}/pay-cash`)
    ).json()) as ApiResponse<{ paid: boolean; pay_type: number; order_no: string }>;
    expect(cash.code, `改现金收款失败: ${cash.message}`).toBe(0);
    expect(cash.data?.paid, '现金收款应立即完结').toBe(true);

    const d = await getOrderDetailInfo(pending.data!.id);
    expect(d.status, '换现金后订单应完结').toBe(ORDER_STATUS.completed);
    expect(d.pay_type, '支付方式应落为现金(4)').toBe(4);
  });

  test('4. pay-code-session：待支付单复用同一支付单号，已支付单拒绝', async () => {
    const codePayType = codePayTypeOf(await listPosPayMethods());
    if (codePayType === 0) {
      // 无第三方付款码凭据：待支付单造不出来，改覆盖本用例另一半小时态守卫——
      // 现金单完结后「重建会话」「继续收现金」都必须被收银台待支付口径（source=2 + status=1）拒绝
      const cash = await createDirectPayOrder(4, 3.3);
      expect(cash.code, `现金单创建失败: ${cash.message}`).toBe(0);

      const s = (await (
        await adminApi.post(`/api/v1/admin/pos/orders/${cash.data!.id}/pay-code-session`)
      ).json()) as ApiResponse;
      expect(s.code, '已收款单不得重建付款码会话').toBe(OPERATION_NOT_ALLOWED);

      const cash2 = (await (
        await adminApi.post(`/api/v1/admin/pos/orders/${cash.data!.id}/pay-cash`)
      ).json()) as ApiResponse;
      expect(cash2.code, '已完结单不得再次现金收款').toBe(OPERATION_NOT_ALLOWED);
      return;
    }

    const pending = await createDirectPayOrder(codePayType, 3.3);
    expect(pending.code, `下单失败: ${pending.message}`).toBe(0);

    // 会话重建：同渠道同金额复用遗留待支付单，out_trade_no 不变（扣款失败重扫前提）
    const s1 = (await (
      await adminApi.post(`/api/v1/admin/pos/orders/${pending.data!.id}/pay-code-session`)
    ).json()) as ApiResponse<{ payment_no: string }>;
    expect(s1.code, `重建会话失败: ${s1.message}`).toBe(0);
    expect(s1.data?.payment_no, '应复用下单返回的待支付单号').toBe(pending.data!.payment_no);

    // 收现金完结后，会话重建与继续收款应被拒绝
    await adminApi.post(`/api/v1/admin/pos/orders/${pending.data!.id}/pay-cash`);
    const s2 = (await (
      await adminApi.post(`/api/v1/admin/pos/orders/${pending.data!.id}/pay-code-session`)
    ).json()) as ApiResponse;
    expect(s2.code, '已支付单不得重建付款码会话').toBe(OPERATION_NOT_ALLOWED);

    const cash2 = (await (
      await adminApi.post(`/api/v1/admin/pos/orders/${pending.data!.id}/pay-cash`)
    ).json()) as ApiResponse;
    expect(cash2.code, '已完结单不得再次现金收款').toBe(OPERATION_NOT_ALLOWED);
  });

  test('5. C 端待支付单不允许收银台代收：pay-cash/pay-code-session 均精确拒绝', async ({ playwright }) => {
    test.skip(!sku, '环境无可用库存 SKU，无法构造 C 端待支付单');
    const buyer = await memberIn(playwright, communityId);
    const buyerApi = await newAuthedRequest(playwright, buyer.token);
    let cOrderId = 0;
    try {
      const created = assertOk<{ id: number }>(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: {
              delivery_type: 1,
              address_id: 0,
              remark: autoName('78 C端待支付代收门禁'),
              items: [{ sku_id: sku!.id, quantity: 1 }],
            },
          })
        ).json()) as ApiResponse<{ id: number }>,
        'C 端自提下单',
      );
      cOrderId = created.id;
    } finally {
      await buyerApi.dispose();
    }

    // 前置核实：确为 C 端来源(source=1)且停在待支付(1)，否则后面的拒绝断言失去意义
    const before = await getOrderDetailInfo(cOrderId);
    expect(before.source, '前置：应为 C 端来源单').toBe(1);
    expect(before.status, '前置：应为待支付状态').toBe(ORDER_STATUS.pending);

    const cash = (await (
      await adminApi.post(`/api/v1/admin/pos/orders/${cOrderId}/pay-cash`)
    ).json()) as ApiResponse;
    expect(cash.code, 'C 端待支付单不得被收银台现金代收').toBe(OPERATION_NOT_ALLOWED);

    const session = (await (
      await adminApi.post(`/api/v1/admin/pos/orders/${cOrderId}/pay-code-session`)
    ).json()) as ApiResponse;
    expect(session.code, 'C 端待支付单不得被收银台重建付款码会话').toBe(OPERATION_NOT_ALLOWED);

    // 门禁只拒不改：代收被拒后订单状态与来源均不应被触碰
    const after = await getOrderDetailInfo(cOrderId);
    expect(after.status, '被拒后订单应仍为待支付').toBe(ORDER_STATUS.pending);
    expect(after.source, '被拒后订单来源不应改变').toBe(1);
  });
});
