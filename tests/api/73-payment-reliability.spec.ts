import { test, expect, type Playwright } from '@playwright/test';
import {
  adminLogin,
  asList,
  clientLogin,
  ensureStock,
  newAdminContext,
  newAuthedRequest,
  pickSku,
  type ApiResponse,
  type PageData,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 73：支付回调可靠性后台链路（solution-20261021-支付回调可靠性-v2.md 第十节 T13–T18）
 * 覆盖：待支付单无渠道支付单时「同步支付状态」回传 NO_PAYMENT 且零状态改动（可重复点击）
 *       → 改单护栏（无成功支付单不得手工置已支付）→ 人工「标记已支付」入参与存在性校验
 *       → 订单详情支付明细/汇总/通知时间线口径（含 cash 创建即成功）
 *       → 已支付订单再次同步被状态守卫拦截 → 异常队列接口契约与销账校验
 *       → 社区隔离（跨社区订单不可同步/标记）与报文原文权限边界（payment:raw:view 只给超管）
 *       → 渠道链路与环境配置同口径（未配置渠道：拒绝发起支付且后台无可核实对象；
 *         已配置渠道：真实核实零改动 + 人工标记后订单一并流转）
 * 说明：本文件只用 HTTP 语义（tests 无数据库通路），渠道应答类异常（验签失败/金额不符/挂账/放弃对账）
 *       由 services/payment/service/scheduler_test.go 的 T1–T12 单测覆盖；
 *       渠道相关分支按环境实际配置走对应断言，不做 test.skip，避免"链路坏了也显示绿"
 */

interface Order {
  id: number;
  order_no: string;
  status: number;
  pay_amount: number;
}

/** 订单详情 data 中本轮关注的字段（ payments / 汇总 / 时间线 ） */
interface OrderDetail {
  id: number;
  status: number;
  pay_amount: number;
  pay_type: number;
  pay_type_text: string;
  payments?: PaymentRow[];
  payment_summary?: { attempts: number; succeeded: number; succeeded_amount: number };
  payments_error?: string;
  payment_logs?: AnomalyRow[];
  payment_logs_error?: string;
  can_view_payment_raw?: boolean;
}

interface PaymentRow {
  id: number;
  payment_no: string;
  pay_type: number;
  amount: number;
  status: number;
  transaction_id: string;
  confirm_source: number;
  confirm_ref: string;
  reconcile_count: number;
}

interface AnomalyRow {
  id: number;
  kind: number;
  result: number;
  order_id: number;
  community_id: number;
  handled: number;
  can_view_raw: boolean;
  has_raw: boolean;
  raw_body?: string;
  headers?: string;
}

interface AnomalyList {
  list: AnomalyRow[];
  total: number;
  pending: number;
  can_view_raw: boolean;
  community_scope: number;
}

interface SyncResult {
  paid: boolean;
  changed: boolean;
  channel_state: string;
  transaction_id: string;
  amount: number;
  payment_no: string;
  message: string;
}

let buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>;
let adminApi: Awaited<ReturnType<typeof newAuthedRequest>>;
let communityId = 0;
let skuId = 0;
/** 环境是否配置了微信/支付宝渠道（决定渠道相关用例是否实跑） */
let channelAvailable = false;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  buyerApi = await newAuthedRequest(playwright, info.access_token);
  communityId = info.user.community_id;
  const admin = await newAdminContext(playwright, undefined, undefined, communityId);
  adminApi = admin.api;
});

test.afterAll(async () => {
  await buyerApi?.dispose();
  await adminApi?.dispose();
});

/**
 * 创建自提待支付订单（quantity 错开同用户连续下单的重复指纹）
 * - 函数名: createPickupOrder
 * - 参数: quantity — 购买数量
 * - 返回: Promise<Order> 订单摘要（id/order_no/status/pay_amount）
 * - 异常: 业务码非 0 时抛出 Error
 */
async function createPickupOrder(quantity: number): Promise<Order> {
  const resp = await buyerApi.post('/api/v1/orders', {
    data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuId, quantity }] },
  });
  const body = (await resp.json()) as ApiResponse<Order>;
  if (body.code !== 0) throw new Error(`创建订单失败 code=${body.code} message=${body.message}`);
  return body.data as Order;
}

/**
 * 读取后台订单详情（支付明细口径来源）
 * - 函数名: adminOrderDetail
 * - 参数: orderId — 订单 ID
 * - 返回: Promise<OrderDetail>
 * - 异常: 业务码非 0 时抛出 Error
 */
async function adminOrderDetail(orderId: number): Promise<OrderDetail> {
  const body = (await (await adminApi.get(`/api/v1/admin/orders/${orderId}`)).json()) as ApiResponse<OrderDetail>;
  if (body.code !== 0) throw new Error(`订单详情读取失败 code=${body.code} message=${body.message}`);
  return body.data as OrderDetail;
}

/**
 * 取消订单（用例收尾，避免遗留待支付单被调度器反复扫）
 * - 函数名: cancelOrder
 * - 参数: orderId — 订单 ID
 * - 返回: Promise<void>
 * - 异常: 不抛错（清理动作失败只留痕，不掩盖用例本身结果）
 */
async function cancelOrder(orderId: number): Promise<void> {
  await buyerApi.put(`/api/v1/orders/${orderId}/cancel`);
}

test.describe.serial('支付回调可靠性后台链路', () => {
  test('准备：就绪可下单 SKU（优先环境商品，缺失时自建兜底）', async () => {
    const channels = (await (await buyerApi.get('/api/v1/payments/channels')).json()) as ApiResponse<{
      channels: { channel: string }[];
    }>;
    expect(channels.code).toBe(0);
    const names = channels.data?.channels ?? [];
    channelAvailable = names.some((c) => c.channel === 'wechat' || c.channel === 'alipay');

    try {
      const sku = await pickSku(adminApi, undefined, communityId);
      skuId = sku.id;
      await ensureStock(adminApi, skuId, 50);
    } catch {
      const cat = (await (
        await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('支付可靠性分类') } })
      ).json()) as ApiResponse<{ id: number }>;
      const prod = (await (
        await adminApi.post('/api/v1/admin/products', {
          data: {
            category_id: cat.data!.id,
            name: autoName('支付可靠性商品'),
            unit: '份',
            is_multi_spec: false,
            sku_items: [{ price: 6.6, stock: 50 }],
          },
        })
      ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>;
      const productId = prod.data?.product?.id ?? prod.data?.id ?? 0;
      await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } });
      const detail = (await (await adminApi.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<{
        skus: { id: number }[];
      }>;
      skuId = detail.data?.skus?.[0]?.id ?? 0;
    }
    expect(skuId, 'SKU 未就绪').toBeGreaterThan(0);
  });

  test('T13/T15/T14 待支付单三道护栏：同步零改动、手工置已支付被拒、非法标记被拒', async () => {
    // 三条负向链路共用一张「待支付且无渠道支付单」的订单：下单接口按用户维度令牌桶限流，
    // 每个用例各造一单会撞「操作过于频繁」，而这三条断言本来也指向同一前置事实
    const order = await createPickupOrder(1);
    try {
      // ① 同步支付状态：没有可向渠道核实的单 → 回传 NO_PAYMENT，零状态改动，且可反复点
      const first = (await (
        await adminApi.post(`/api/v1/admin/orders/${order.id}/payment-sync`, { data: {} })
      ).json()) as ApiResponse<SyncResult>;
      expect(first.code, `同步支付状态失败: ${first.message}`).toBe(0);
      expect(first.data?.paid, '渠道未确认收款不得回传已付').toBe(false);
      expect(first.data?.changed, '未收款必须零状态改动').toBe(false);
      expect(first.data?.channel_state, '从未发起支付应为 NO_PAYMENT').toBe('NO_PAYMENT');
      expect(first.data?.payment_no ?? '', 'NO_PAYMENT 无支付单号').toBe('');

      const second = (await (
        await adminApi.post(`/api/v1/admin/orders/${order.id}/payment-sync`, { data: {} })
      ).json()) as ApiResponse<SyncResult>;
      expect(second.code).toBe(0);
      expect(second.data?.channel_state).toBe('NO_PAYMENT');
      expect(second.data?.changed).toBe(false);

      const afterSync = await adminOrderDetail(order.id);
      expect(afterSync.status, '同步动作不得改动订单状态').toBe(1);
      expect(afterSync.payments?.length ?? 0, '未发起支付不应凭空产生支付记录').toBe(0);
      expect(afterSync.payments_error ?? '', '支付记录读取失败会误导运营').toBe('');

      // ② 改单护栏：置「已支付」必须以渠道收款事实为前提
      const manual = (await (
        await adminApi.put(`/api/v1/admin/orders/${order.id}/status`, { data: { status: 2, remark: 'AUTO-TEST 越权改单' } })
      ).json()) as ApiResponse;
      expect(manual.code, '无收款记录不应允许手工置已支付').not.toBe(0);
      expect(manual.message, '拒绝理由需指明以渠道收款为前提并引导正规入口').toMatch(/收款记录|同步支付状态/);

      // ③ 人工标记：四字段必填 + 支付单必须存在
      const missing = (await (
        await adminApi.post(`/api/v1/admin/orders/${order.id}/payment-mark-paid`, { data: { payment_no: 'X' } })
      ).json()) as ApiResponse;
      expect(missing.code, '缺渠道交易号/金额/核实依据必须被拒').not.toBe(0);
      expect(missing.message).toMatch(/必填/);

      const ghost = (await (
        await adminApi.post(`/api/v1/admin/orders/${order.id}/payment-mark-paid`, {
          data: { payment_no: 'AUTO-TEST-不存在', transaction_id: 'T-1', amount: 6.6, remark: 'AUTO-TEST 探针' },
        })
      ).json()) as ApiResponse;
      expect(ghost.code, '支付单不存在必须被拒').not.toBe(0);
      expect(ghost.message, '服务层按 bizerr 分类返回，不能笼统报内部错误').toMatch(/不存在/);

      const detail = await adminOrderDetail(order.id);
      expect(detail.status, '三次非法处置都不得改动订单').toBe(1);
    } finally {
      await cancelOrder(order.id);
    }
  });

  test('T17 订单详情支付明细：cash 创建即成功的汇总与来源口径', async () => {
    const order = await createPickupOrder(2);
    const pay = (await (
      await buyerApi.post('/api/v1/payments', { data: { order_id: order.id, channel: 'cash' } })
    ).json()) as ApiResponse<{ payment_no: string }>;
    expect(pay.code, `cash 支付失败: ${pay.message}`).toBe(0);

    const detail = await adminOrderDetail(order.id);
    expect(detail.status, '货到付款创建支付单即已支付').toBe(2);
    const rows = detail.payments ?? [];
    expect(rows.length, '支付尝试数应与实际发起次数一致').toBe(1);
    const summary = detail.payment_summary;
    expect(summary?.attempts, '汇总次数须与明细行数一致').toBe(rows.length);
    expect(summary?.succeeded, '一笔成功').toBe(1);
    expect(summary?.succeeded_amount ?? 0, '成功金额合计=订单应付').toBeCloseTo(detail.pay_amount, 2);
    expect(rows[0].payment_no, '明细需含支付单号').toBeTruthy();
    expect(rows[0].status).toBe(2);
    expect(rows[0].confirm_source, '线下渠道确认来源应为 4（非回调，统计回调丢失率需排除）').toBe(4);
    expect(typeof rows[0].reconcile_count, '对账次数应随明细下发').toBe('number');
    expect(detail.pay_type_text, '支付方式文案供详情页渲染').toBeTruthy();
    expect(Array.isArray(detail.payment_logs), '通知时间线字段必须存在（可为空数组）').toBe(true);
    expect(detail.payment_logs_error ?? '', '时间线读取失败会误导运营').toBe('');
    expect(detail.can_view_payment_raw, '超管默认具备报文原文查看权限').toBe(true);

    // 已支付订单再点「同步支付状态」：状态守卫先拦截，不打扰渠道也不改状态
    const again = (await (
      await adminApi.post(`/api/v1/admin/orders/${order.id}/payment-sync`, { data: {} })
    ).json()) as ApiResponse;
    expect(again.code, '已支付订单无需再同步').not.toBe(0);
    expect(again.message).toMatch(/无需同步/);
  });

  test('T18 支付异常队列：列表契约 + 销账入参与存在性校验', async () => {
    const pending = (await (
      await adminApi.get('/api/v1/admin/payments/anomalies', { params: { page: 1, page_size: 10, handled: 0 } })
    ).json()) as ApiResponse<AnomalyList>;
    expect(pending.code, `异常队列读取失败: ${pending.message}`).toBe(0);
    expect(Array.isArray(pending.data?.list)).toBe(true);
    expect(typeof pending.data?.total).toBe('number');
    expect(typeof pending.data?.pending).toBe('number');
    expect(pending.data?.can_view_raw, '超管可见报文原文').toBe(true);
    expect(pending.data?.community_scope, '超管社区范围为 0（全量）').toBe(0);
    // 列表默认不回原文：只有显式带权限且服务端判定可见时才下发 raw_body 字段
    for (const row of pending.data?.list ?? []) {
      expect(typeof row.has_raw, '前端据此决定是否渲染展开按钮').toBe('boolean');
      expect(row.can_view_raw).toBe(true);
    }

    // 入参 handled 是筛选选择器（1 待处理 / 2 已处理 / 0 全部），行的 handled 是落库标记（0 待处理 / 1 已处理），
    // 两套取值容易看错，这里把两侧口径同时钉住
    const handled = (await (
      await adminApi.get('/api/v1/admin/payments/anomalies', { params: { handled: 2, page: 1, page_size: 10 } })
    ).json()) as ApiResponse<AnomalyList>;
    expect(handled.code, '已处理筛选应可查询').toBe(0);
    for (const row of handled.data?.list ?? []) {
      expect(row.handled, '筛选「已处理」结果必须全部已销账').toBe(1);
    }

    const pendingOnly = (await (
      await adminApi.get('/api/v1/admin/payments/anomalies', { params: { handled: 1, page: 1, page_size: 10 } })
    ).json()) as ApiResponse<AnomalyList>;
    expect(pendingOnly.code, '待处理筛选应可查询（异常页默认队列）').toBe(0);
    for (const row of pendingOnly.data?.list ?? []) {
      expect(row.handled, '筛选「待处理」结果必须仍未销账').toBe(0);
    }
    expect(pendingOnly.data?.pending, '待办红标数是社区口径，不随 handled 筛选变化').toBe(pending.data?.pending);

    const noRemark = (await (
      await adminApi.post('/api/v1/admin/payments/anomalies/1/handle', { data: {} })
    ).json()) as ApiResponse;
    expect(noRemark.code, '销账必须填写核实依据').not.toBe(0);

    const ghost = (await (
      await adminApi.post('/api/v1/admin/payments/anomalies/99999999/handle', { data: { remark: 'AUTO-TEST 探针' } })
    ).json()) as ApiResponse;
    expect(ghost.code, '不存在的流水不得销账成功').not.toBe(0);
  });

  test('T16 社区隔离与原文权限边界：他社区管理员不可操作、不可看报文', async ({ playwright }) => {
    const comm = (await (
      await adminApi.post('/api/v1/admin/communities', {
        data: { name: autoName('支付隔离社区'), address: 'AUTO-TEST 地址', status: 1, delivery_fee: 0, min_order_amount: 0 },
      })
    ).json()) as ApiResponse<{ id: number }>;
    expect(comm.code, `建隔离社区失败: ${comm.message}`).toBe(0);
    const otherId = comm.data!.id;

    // 建角色/建员工都必须在目标社区语境下做（角色属于社区，跨社区 role_ids 会被拒），
    // 这里用同一个超管 token 另开一个 X-Community-Id 为隔离社区的上下文
    const superInfo = await adminLogin(playwright.request);
    const isoCtx = await playwright.request.newContext({
      extraHTTPHeaders: { Authorization: `Bearer ${superInfo.access_token}`, 'X-Community-Id': String(otherId) },
    });
    const username = `auto_iso_${uniquePhone()}`;
    let staffId = 0;
    let orderId = 0;
    let otherApi: Awaited<ReturnType<typeof playwright.request.newContext>> | undefined;
    try {
      const roles = (await (
        await isoCtx.get('/api/v1/admin/roles', { params: { community_id: otherId } })
      ).json()) as ApiResponse<{ id: number; code?: string }[] | PageData<{ id: number; code?: string }>>;
      const builtin = asList<{ id: number; code?: string }>(roles.data).find((r) => r.code === 'community_admin');
      expect(builtin, '隔离社区缺内置社区管理员角色').toBeTruthy();

      const created = (await (
        await isoCtx.post('/api/v1/admin/admins', {
          data: { username, password: 'Ff123456', real_name: 'AUTO-TEST 隔离管理员', role_ids: [builtin!.id] },
        })
      ).json()) as ApiResponse<{ id: number }>;
      expect(created.code, `建隔离社区管理员失败: ${created.message}`).toBe(0);
      staffId = created.data!.id;

      const order = await createPickupOrder(3);
      const info = await adminLogin(playwright.request, username, 'Ff123456');
      otherApi = await playwright.request.newContext({
        extraHTTPHeaders: { Authorization: `Bearer ${info.access_token}`, 'X-Community-Id': String(otherId) },
      });

      const sync = (await (
        await otherApi.post(`/api/v1/admin/orders/${order.id}/payment-sync`, { data: {} })
      ).json()) as ApiResponse;
      expect(sync.code, '跨社区订单不得同步支付状态').not.toBe(0);
      expect(sync.message).toMatch(/无权|不存在/);

      const mark = (await (
        await otherApi.post(`/api/v1/admin/orders/${order.id}/payment-mark-paid`, {
          data: { payment_no: 'AUTO-TEST-不存在', transaction_id: 'T-1', amount: 1, remark: 'AUTO-TEST 探针' },
        })
      ).json()) as ApiResponse;
      expect(mark.code, '跨社区订单不得人工标记').not.toBe(0);

      const anomalies = (await (
        await otherApi.get('/api/v1/admin/payments/anomalies', { params: { page: 1, page_size: 10 } })
      ).json()) as ApiResponse<AnomalyList>;
      expect(anomalies.code, '本社区异常队列应可访问（order:view）').toBe(0);
      expect(anomalies.data?.community_scope, '社区管理员队列应收敛到本社区').toBe(otherId);
      expect(anomalies.data?.can_view_raw, 'payment:raw:view 不在社区管理员默认上限内').toBe(false);
      for (const row of anomalies.data?.list ?? []) {
        expect(row.raw_body === undefined, '无原文权限时不得下发 raw_body').toBe(true);
        expect(row.headers === undefined, '无原文权限时不得下发 headers').toBe(true);
      }

      const detail = (await (await otherApi.get(`/api/v1/admin/orders/${order.id}`)).json()) as ApiResponse;
      expect(detail.code, '跨社区订单详情不可读').not.toBe(0);
      orderId = order.id;
    } finally {
      await otherApi?.dispose();
      if (orderId) await cancelOrder(orderId);
      // 清理尽力而为：账号被历史数据引用时删除会被外键拒绝，只留痕不影响断言
      await isoCtx.delete(`/api/v1/admin/admins/${staffId}`);
      await isoCtx.delete(`/api/v1/admin/communities/${otherId}`);
      await isoCtx.dispose();
    }
  });

  test('渠道链路与环境配置同口径：未配置则无可核实对象，已配置则核实/标记全链路可用', async () => {
    const order = await createPickupOrder(4);
    try {
      // 渠道按环境实际可用性选择：channelAvailable 只保证「wechat 或 alipay 任一就绪」，
      // 硬编码 wechat 会在「仅配置了 alipay」的环境里误走创建被拒分支
      const channels = (await (await buyerApi.get('/api/v1/payments/channels')).json()) as ApiResponse<{
        channels: { channel: string }[];
      }>;
      const useChannel = (channels.data?.channels ?? []).some((c) => c.channel === 'wechat') ? 'wechat' : 'alipay';
      const created = (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: order.id, channel: useChannel } })
      ).json()) as ApiResponse<{ payment_no: string }>;

      if (!channelAvailable) {
        // 与收银台展示口径一致：渠道未配置时既不下发选项也不允许发起支付（10004/参数类拒绝均可），
        // 于是后台「同步支付状态」没有可向渠道核实的对象——回传 NO_PAYMENT 且零状态改动，
        // 这条负向断言保证「配置未就绪却已产生支付单」这类半截数据不会出现
        expect(created.code, `未配置渠道应拒绝发起支付: ${created.message}`).not.toBe(0);
        const sync = (await (
          await adminApi.post(`/api/v1/admin/orders/${order.id}/payment-sync`, { data: {} })
        ).json()) as ApiResponse<SyncResult>;
        expect(sync.code).toBe(0);
        expect(sync.data?.channel_state, '无渠道支付单时不得凭空判定收款').toBe('NO_PAYMENT');
        expect(sync.data?.changed).toBe(false);
        const detail = await adminOrderDetail(order.id);
        expect(detail.status, '订单仍待支付').toBe(1);
        expect(detail.payments?.length ?? 0, '被拒的发起不应留下支付单').toBe(0);
        return;
      }

      expect(created.code, `渠道下单失败: ${created.message}`).toBe(0);
      const paymentNo = created.data!.payment_no;

      const sync = (await (
        await adminApi.post(`/api/v1/admin/orders/${order.id}/payment-sync`, { data: { payment_no: paymentNo } })
      ).json()) as ApiResponse<SyncResult>;
      expect(sync.code).toBe(0);
      expect(sync.data?.payment_no, '应回传被核实的支付单号').toBe(paymentNo);
      if (!sync.data?.paid) {
        expect(sync.data?.changed, '渠道未收款不得改动本地状态').toBe(false);
        const stillPending = await adminOrderDetail(order.id);
        expect(stillPending.status, '未收款订单仍待支付').toBe(1);
      }

      // 人工标记：内测环境模拟「线下已核实到账」，金额必须与应付分位一致
      const detail = await adminOrderDetail(order.id);
      if (detail.status === 1) {
        const mark = (await (
          await adminApi.post(`/api/v1/admin/orders/${order.id}/payment-mark-paid`, {
            data: {
              payment_no: paymentNo,
              transaction_id: `AUTO-TEST-${Date.now()}`,
              amount: detail.pay_amount,
              remark: 'AUTO-TEST 商户平台核对',
            },
          })
        ).json()) as ApiResponse;
        expect(mark.code, `人工标记失败: ${mark.message}`).toBe(0);
        const after = await adminOrderDetail(order.id);
        expect(after.status, '标记成功后订单必须同步流转为已支付').toBe(2);
        const row = (after.payments ?? []).find((p) => p.payment_no === paymentNo);
        expect(row?.status).toBe(2);
        expect(row?.confirm_source, '人工标记来源为 3').toBe(3);
        expect(row?.confirm_ref, '来源凭据区分具体入口').toBe('manual-mark');

        const repeated = (await (
          await adminApi.post(`/api/v1/admin/orders/${order.id}/payment-mark-paid`, {
            data: {
              payment_no: paymentNo,
              transaction_id: 'AUTO-TEST-again',
              amount: detail.pay_amount,
              remark: 'AUTO-TEST 重复标记',
            },
          })
        ).json()) as ApiResponse;
        expect(repeated.code, '已支付订单不得重复标记').not.toBe(0);
      }
    } finally {
      const after = await adminOrderDetail(order.id);
      if (after.status === 1) await cancelOrder(order.id);
    }
  });

  test('T19 已取消订单的渠道核实挂账：只产待办不改资金状态', async () => {
    // 背景：挂账扫描只覆盖关闭后 120 分钟内的单，超窗后靠订单详情页「渠道核实挂账」人工核实；
    // 已取消订单走 status=5 支付单的渠道核实语义——渠道已收款 → 异常页待办，未收款 → 消除疑虑，
    // 无论哪种都不允许改动资金状态（订单状态机不允许 6→2）
    const order = await createPickupOrder(5);
    try {
      let paymentCreated = false;
      if (channelAvailable) {
        const created = (await (
          await buyerApi.post('/api/v1/payments', { data: { order_id: order.id, channel: 'alipay' } })
        ).json()) as ApiResponse<{ payment_no: string }>;
        paymentCreated = created.code === 0;
      }
      await cancelOrder(order.id);
      const detail = await adminOrderDetail(order.id);
      expect(detail.status, '订单应已取消').toBe(6);

      const sync = (await (
        await adminApi.post(`/api/v1/admin/orders/${order.id}/payment-sync`, { data: {} })
      ).json()) as ApiResponse<SyncResult>;
      expect(sync.code, '已取消订单应允许挂账核实').toBe(0);
      const r = sync.data!;
      expect(r.changed, '挂账核实不得改动资金状态').toBe(false);
      expect(r.paid, '未支付交易的挂账核实不得判已收款').toBe(false);
      if (paymentCreated) {
        expect(r.payment_no.length, '应回退定位到已关闭的渠道支付单').toBeGreaterThan(0);
        // 真实渠道下未支付交易的查询结果是无此交易/已关闭；渠道配置异常时为 ERROR（不得误判已收款）
        expect(['NOT_EXIST', 'CLOSED', 'ERROR'], `渠道状态异常: ${r.channel_state}`).toContain(r.channel_state);
      } else {
        expect(r.channel_state, '无渠道支付单时无可核实对象').toBe('NO_PAYMENT');
      }
    } finally {
      // 订单已取消，无遗留待支付单需清理
    }
  });
});
