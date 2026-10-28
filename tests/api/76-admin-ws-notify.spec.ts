import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { uniquePhone } from '../helpers/data';
import { newAdminContext, adminLogin, clientLogin, pickSku, ensureStock, asList, type ApiResponse } from '../helpers/api';

/**
 * 模块 76：后台 WS 新订单语音通知
 * 覆盖：未登录握手被拒、C 端 token 被拒、授权连接成功并收到 hello、
 *       POS 现金收款（支付成功收口）后同社区连接收到 order.paid 事件、
 *       POS 现金单代客退款后收到 order.refund（completed）事件、
 *       异社区连接不接收该社区事件
 * 环境要求：支付异常事件那条用例走 redis-cli 直发事件总线，需进程能拿到 REDIS_PASSWORD
 *       （环境变量优先，回退读项目根 .env）；拿不到时按「凭据不可得」诚实 skip，
 *       不记成产品链路缺陷
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

/** 环境是否有可用的 docker+redis-cli（支付异常事件直发用；探测失败自动降级 skip） */
let redisAvailable = false;
/** redis-cli 探测不可用的原因（写进 skip 文案，别让人去猜是不是产品链路坏了） */
let redisProbeError = '';
/**
 * Redis 认证口令：优先取进程环境变量（compose/CI 已注入 REDIS_PASSWORD 时无需读文件），
 * 回退读项目根 .env——沙箱或受限终端里读不到 .env 属凭据不可得，不是产品链路坏了
 */
let redisPassword = process.env.REDIS_PASSWORD?.trim() ?? '';
if (!redisPassword) {
  try {
    redisPassword =
      readFileSync(resolve(process.cwd(), '../.env'), 'utf-8')
        .split('\n')
        .find((line) => line.startsWith('REDIS_PASSWORD='))
        ?.slice('REDIS_PASSWORD='.length)
        .trim() ?? '';
  } catch {
    redisPassword = '';
  }
}
try {
  // 必须看回复内容而不是退出码：redis-cli 拿到 NOAUTH 这类错误回复时退出码仍是 0，
  // 只看退出码会把「口令没注入」误判成「环境可用」，随后的 PUBLISH 石沉大海，
  // 用例就红成「网关转发链路坏了」（2026-10-29 实测踩中）
  const pong = execFileSync('docker', ['exec', 'market-redis', 'redis-cli', '-a', redisPassword, 'PING'], {
    timeout: 5000,
    stdio: 'pipe',
  })
    .toString()
    .trim();
  redisAvailable = pong === 'PONG';
  if (!redisAvailable) redisProbeError = `PING 回复「${pong}」非 PONG（REDIS_PASSWORD 未注入）`;
} catch (e) {
  redisAvailable = false;
  redisProbeError = `PING 执行失败：${(e as Error).message}`;
}

/** 向事件总线发布一条支付异常事件（docker exec redis-cli 直发，验证 gateway 转发链路） */
function publishPaymentAnomaly(payload: string): void {
  const out = execFileSync('docker', ['exec', 'market-redis', 'redis-cli', '-a', redisPassword, 'PUBLISH', 'market:payment:anomaly', payload], {
    timeout: 10000,
    stdio: 'pipe',
  })
    .toString()
    .trim();
  // 正常回复是订阅者数量（整数）；不是整数说明这条事件根本没进总线，
  // 直接抛错定位到环境，而不是让下面的断言等 10 秒超时、误报成转发链路缺陷
  if (!/^\d+$/.test(out)) throw new Error(`PUBLISH 未进入事件总线，回复「${out}」`);
}

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

  test('创建导出任务后创建人连接收到 export.task_changed 事件（done）', async () => {
    test.skip(skipNoWs, skipNoWs);
    const session = new WsSession(buildWsUrl(adminToken, communityId));
    try {
      await session.opened;
      // 不存在的支付单号：导出 0 行、任务快速置 done，不产生大数据量
      const resp = await api.post('/api/v1/admin/orders/export-tasks', {
        params: { pay_no: 'AUTO-TEST-WS-NONE', query_desc: '76 号用例-导出事件验证' },
      });
      const body = (await resp.json()) as ApiResponse<{ id: number }>;
      expect(body.code, `创建导出任务应成功: ${body.message}`).toBe(0);
      expect(body.data?.id, '应返回任务 ID').toBeGreaterThan(0);

      // 终态事件：order 服务置 done 后发布（频道 market:export:task），
      // 网关按创建人管理员定向推送；轮询兜底同口径，此处断言事件先于/等价于落库
      const evt = await session.waitFor('export.task_changed', 15000);
      expect(evt, '15 秒内应收到 export.task_changed 推送').toBeDefined();
      expect(evt?.data?.task_id).toBe(body.data?.id);
      expect(evt?.data?.state).toBe('done');
      expect(evt?.data?.admin_id, '应携带创建人 admin_id（定向下发依据）').toBeGreaterThan(0);
      expect(evt?.data?.export_type).toBe('orders');
      // 契约红线：载荷不得出现文件路径（下载走带归属校验的接口）
      expect(evt?.data, '载荷不得携带文件路径').not.toHaveProperty('file_path');
    } finally {
      session.close();
    }
  });

  test('支付异常事件同社区连接可达、异社区连接不可达（Redis 直发验证转发链路）', async () => {
    test.skip(skipNoWs, skipNoWs);
    // 渠道侧事实离线无法构造（挂账检出需真实渠道回查），此处直发事件总线验证
    // gateway 的订阅 → 社区过滤 → 权限门槛 → WS 转发链路；
    // 发布点/白名单口径由 payment 服务单测与 eventbus 契约用例覆盖
    const publishPayload = JSON.stringify({
      kind: 3,
      order_no: 'O-AUTO-TEST-WS',
      payment_no: 'PAY-AUTO-TEST-WS',
      community_id: communityId,
      amount: 66.6,
      event_time: new Date().toISOString().slice(0, 19).replace('T', ' '),
    });
    test.skip(!redisAvailable, `环境无可用 redis-cli 直发通道（${redisProbeError || 'docker 不可用'}），跳过事件总线直发验证`);

    const session = new WsSession(buildWsUrl(adminToken, communityId));
    try {
      await session.opened;
      publishPaymentAnomaly(publishPayload);
      const evt = await session.waitFor('payment.anomaly_detected', 10000);
      expect(evt, '10 秒内应收到 payment.anomaly_detected 推送').toBeDefined();
      expect(evt?.data?.payment_no).toBe('PAY-AUTO-TEST-WS');
      expect(evt?.data?.community_id).toBe(communityId);
      expect(evt?.data?.kind).toBe(3);

      // 异社区连接不得收到（community_id 过滤）；无第二社区时退化为只验证可达性
      const commsResp = await api.get('/api/v1/admin/my/communities');
      const commsBody = (await commsResp.json()) as ApiResponse<{ id: number }[]>;
      const other = asList<{ id: number }>(commsBody.data).find((c) => c.id !== communityId);
      if (!other) return;
      const elsewhere = new WsSession(buildWsUrl(adminToken, other.id));
      try {
        await elsewhere.opened;
        publishPaymentAnomaly(publishPayload);
        const missed = await elsewhere.waitFor('payment.anomaly_detected', 4000);
        expect(missed, '异社区连接不得收到该社区支付异常告警').toBeUndefined();
      } finally {
        elsewhere.close();
      }
    } finally {
      session.close();
    }
  });
});
