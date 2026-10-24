import { test, expect } from '@playwright/test';
import { uniquePhone } from '../helpers/data';
import { newAdminContext, adminLogin, clientLogin, pickSku, ensureStock, asList, type ApiResponse } from '../helpers/api';

/**
 * 模块 76：后台 WS 新订单语音通知
 * 覆盖：未登录握手被拒、C 端 token 被拒、授权连接成功并收到 hello、
 *       POS 现金收款（支付成功收口）后同社区连接收到 order.paid 事件、
 *       POS 现金单代客退款后收到 order.refund（completed）事件、
 *       异社区连接不接收该社区事件
 * 链路：POS 现金到店单（pay_type=4, delivery_type=3）→ payment 创建即成功 →
 *       order UpdateOrderStatus 置 2（同事务 2→5 终态）→ Redis 发布 → gateway WS 推送；
 *       代客退款（现金）→ completeRefundSuccess 统一收尾 → market:order:refund 发布 → WS 推送
 */

/** 服务端 WS 消息结构（gateway/ws Message） */
interface WsMessage {
  type?: string;
  data?: Record<string, unknown>;
}

/** WS 握手 URL：token 与社区声明经查询参数传递（浏览器 WS 无法自定义请求头） */
function buildWsUrl(token: string, communityId: number | string): string {
  const base = process.env.TEST_BASE_URL ?? 'http://localhost:18080';
  const url = new URL('/api/v1/admin/ws', base);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.searchParams.set('access_token', token);
  url.searchParams.set('community_id', String(communityId));
  return url.toString();
}

/** 环境 WS 客户端可用性（Node 20 需实验开关，Node 22+ 内置） */
const wsSupported = typeof WebSocket !== 'undefined';
const skipNoWs = !wsSupported ? '运行环境无内置 WebSocket 客户端（需 Node 22+）' : '';

/**
 * 轻量 WS 会话：连接、收集消息、按 type 等待
 */
class WsSession {
  readonly messages: WsMessage[] = [];
  /** 握手完成的 Promise（失败时 reject，供负向用例断言） */
  readonly opened: Promise<void>;
  private readonly sock: WebSocket;

  constructor(url: string) {
    this.sock = new WebSocket(url);
    this.opened = new Promise<void>((resolve, reject) => {
      this.sock.onopen = () => resolve();
      this.sock.onerror = () => reject(new Error('WS 握手失败（连接未建立）'));
    });
    this.sock.onmessage = (ev: MessageEvent) => {
      try {
        this.messages.push(JSON.parse(String(ev.data)) as WsMessage);
      } catch {
        // 非 JSON 消息忽略（协议外流量）
      }
    };
  }

  /**
   * 等待指定 type 的消息出现
   * @param type 消息类型
   * @param timeoutMs 最长等待毫秒数
   * @returns 命中的消息，超时返回 undefined
   */
  async waitFor(type: string, timeoutMs: number): Promise<WsMessage | undefined> {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const found = this.messages.find((m) => m.type === type);
      if (found) return found;
      await new Promise((r) => setTimeout(r, 100));
    }
    return undefined;
  }

  /** 关闭连接（幂等） */
  close(): void {
    try {
      this.sock.close();
    } catch {
      // 已关闭时忽略
    }
  }
}

/**
 * 断言握手被拒绝：连接未建立即关闭
 * @param url 握手 URL
 * @returns 是否被拒（true = 未成功建立连接）
 */
async function expectHandshakeRejected(url: string): Promise<boolean> {
  const session = new WsSession(url);
  try {
    await session.opened;
    return false;
  } catch {
    return true;
  } finally {
    session.close();
  }
}

/** POS 现金到店单响应结构（建单仅回 id/order_no/pay_amount 等） */
interface PosOrderResult {
  order?: { id: number; order_no: string; pay_amount?: number };
  id?: number;
  order_no?: string;
  pay_amount?: number;
}

let api: Awaited<ReturnType<typeof newAdminContext>>['api'];
let adminToken = '';
let communityId = 0;
let skuId = 0;

test.beforeAll(async ({ playwright }) => {
  test.skip(skipNoWs, skipNoWs);
  const ctx = await newAdminContext(playwright);
  api = ctx.api;
  communityId = ctx.communityId;
  adminToken = (await adminLogin(playwright.request)).access_token;
  try {
    const sku = await pickSku(api, undefined, communityId);
    skuId = sku.id;
    await ensureStock(api, skuId, 100);
  } catch {
    skuId = 0;
  }
  // 开工兜底：POS 下单要求进行中班次；已有班次时失败可接受
  await api.post('/api/v1/admin/pos/shifts/start', { data: { note: 'AUTO-TEST WS 通知开工' } });
});

test.afterAll(async () => {
  // 交班收尾：不遗留进行中班次，避免与 14/44/69 等班次类用例互踩（失败可接受）
  if (api) {
    await api.post('/api/v1/admin/pos/shifts/end', { data: { note: 'AUTO-TEST WS 通知交班' } }).catch(() => {});
    await api.dispose();
  }
});

/** 发起一笔 POS 现金到店单（支付成功/退款事件触发源），返回订单 id 与单号 */
async function createPosCashOrder(quantity = 1): Promise<{ id: number; orderNo: string }> {
  const resp = await api.post('/api/v1/admin/pos/orders', {
    data: {
      user_id: 0,
      pay_type: 4,
      delivery_type: 3,
      items: [{ sku_id: skuId, quantity }],
      remark: 'AUTO-TEST WS 通知订单',
    },
  });
  const body = (await resp.json()) as ApiResponse<PosOrderResult>;
  expect(body.code, `POS 下单失败: ${body.message}`).toBe(0);
  const order = body.data?.order ?? body.data;
  return { id: order?.id ?? 0, orderNo: order?.order_no ?? '' };
}

test.describe.serial('后台 WS 新订单语音通知', () => {
  test('未登录握手被拒', async () => {
    test.skip(skipNoWs, skipNoWs);
    const rejected = await expectHandshakeRejected(buildWsUrl('', communityId));
    expect(rejected, '无 token 的握手不得建立连接').toBe(true);
  });

  test('C 端 token 握手被拒（token 类型不符）', async ({ playwright }) => {
    test.skip(skipNoWs, skipNoWs);
    const client = await clientLogin(playwright.request, uniquePhone());
    const rejected = await expectHandshakeRejected(buildWsUrl(client.access_token, communityId));
    expect(rejected, 'C 端会员 token 不得建立后台 WS 连接').toBe(true);
  });

  test('授权管理员连接成功并收到 hello', async () => {
    test.skip(skipNoWs, skipNoWs);
    const session = new WsSession(buildWsUrl(adminToken, communityId));
    try {
      await session.opened;
      const hello = await session.waitFor('hello', 5000);
      expect(hello, '连接建立后应收到 hello 欢迎消息').toBeDefined();
    } finally {
      session.close();
    }
  });

  test('POS 现金收款后同社区连接收到 order.paid 事件', async () => {
    test.skip(skipNoWs || !skuId, skipNoWs || !skuId ? '环境不支持 WS 或无库存 SKU' : '');
    const session = new WsSession(buildWsUrl(adminToken, communityId));
    try {
      await session.opened;
      const { orderNo } = await createPosCashOrder(1);
      expect(orderNo).toBeTruthy();
      // 支付成功事件：order 服务 UpdateOrderStatus(2) 收口发布，网关转发
      const evt = await session.waitFor('order.paid', 10000);
      expect(evt, '10 秒内应收到 order.paid 推送').toBeDefined();
      expect(evt?.data?.order_no).toBe(orderNo);
      expect(evt?.data?.community_id).toBe(communityId);
      expect(Number(evt?.data?.pay_amount)).toBeGreaterThan(0);
      expect(evt?.data?.delivery_type).toBe(3);
      expect(evt?.data?.status, '到店单收款即完结，事件状态应为 5').toBe(5);
    } finally {
      session.close();
    }
  });

  test('POS 现金单代客退款后同社区连接收到 order.refund 事件（completed）', async () => {
    test.skip(skipNoWs || !skuId, skipNoWs || !skuId ? '环境不支持 WS 或无库存 SKU' : '');
    const session = new WsSession(buildWsUrl(adminToken, communityId));
    try {
      await session.opened;
      const { id, orderNo } = await createPosCashOrder(1);
      expect(orderNo).toBeTruthy();
      // 先等支付事件到达，再发起代客退款（现金退款同步完成，走统一收尾发布 completed 事件）
      await session.waitFor('order.paid', 10000);
      const refundResp = await api.post(`/api/v1/admin/orders/${id}/refund`, {
        data: { reason: 'AUTO-TEST WS 退款通知' },
      });
      const refundBody = (await refundResp.json()) as ApiResponse<{ refund_no?: string; amount?: number }>;
      expect(refundBody.code, `代客退款失败: ${refundBody.message}`).toBe(0);
      // 退款事件：RefundService.completeRefundSuccess 统一收尾发布，网关转发
      const evt = await session.waitFor('order.refund', 10000);
      expect(evt, '10 秒内应收到 order.refund 推送').toBeDefined();
      expect(evt?.data?.order_no).toBe(orderNo);
      expect(evt?.data?.community_id).toBe(communityId);
      expect(evt?.data?.action, '代客退款发起即完成，事件动作应为 completed').toBe('completed');
      expect(String(evt?.data?.refund_no ?? ''), '应携带退款单号').toBeTruthy();
      expect(Number(evt?.data?.amount), '退款金额应大于 0').toBeGreaterThan(0);
      expect(evt?.data?.operator_type).toBe('admin');
    } finally {
      session.close();
    }
  });

  test('异社区连接不接收该社区订单事件', async () => {
    test.skip(skipNoWs || !skuId, skipNoWs || !skuId ? '环境不支持 WS 或无库存 SKU' : '');
    // 取一个非订单社区：超管可在任意社区语境连接，事件按握手声明社区过滤
    const commsResp = await api.get('/api/v1/admin/my/communities');
    const commsBody = (await commsResp.json()) as ApiResponse<{ id: number; name?: string }[]>;
    const other = asList<{ id: number }>(commsBody.data).find((c) => c.id !== communityId);
    test.skip(!other, '环境无第二个社区，无法验证社区过滤');
    if (!other) return;

    const session = new WsSession(buildWsUrl(adminToken, other.id));
    try {
      await session.opened;
      const orderNo = (await createPosCashOrder(2)).orderNo;
      expect(orderNo).toBeTruthy();
      const evt = await session.waitFor('order.paid', 4000);
      expect(evt, '异社区连接不得收到该社区订单事件').toBeUndefined();
    } finally {
      session.close();
    }
  });
});
