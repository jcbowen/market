import { test, expect, type Playwright } from '@playwright/test';
import { execSync } from 'node:child_process';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  pickSku,
  ensureStock,
  assertOk,
  asList,
  type ApiResponse,
  type SkuRef,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 79：已完成单售后窗口（2026-10-25）
 * 覆盖：
 *  1. 自提单核销完成后（status=5），窗口内 C 端可申请部分退款 → 后台审批同意 → 订单回到已完成(5)；
 *  2. complete_time 被拨到 8 天前（超窗）→ C 端申请被拒并提示联系门店 → 后台代客退款不受窗口限制仍可退；
 *  3. 配置 completed_refund_hours=0（关闭入口）→ C 端申请被拒；
 *  4. 售后配置接口读写 completed_refund_hours。
 * 超窗造数依赖本地 docker 容器 market-mysql（tests 工程本地运行的既定环境），
 * 容器不可用时相关用例自动 skip，不视为失败。
 */

interface OrderResult {
  id: number;
  order_no: string;
  [k: string]: unknown;
}

interface OrderDetailResult {
  id: number;
  status: number;
  pickup_code?: string;
  items?: Array<{ id: number; quantity: number; [k: string]: unknown }>;
  [k: string]: unknown;
}

interface RefundRecord {
  id: number;
  order_id?: number;
  status?: number;
  [k: string]: unknown;
}

/** 直连本地 MySQL 容器执行 SQL（仅造数/拨时间用），返回是否执行成功 */
function execMysql(sql: string): boolean {
  try {
    execSync(`docker exec market-mysql mysql -uroot -pmarket123 market -e "${sql.replace(/"/g, '\\"')}"`, {
      stdio: 'pipe',
    });
    return true;
  } catch {
    return false;
  }
}

/** 探测本地 MySQL 容器是否可用（决定超窗造数用例是否 skip） */
function mysqlAvailable(): boolean {
  return execMysql('SELECT 1');
}

/** 创建现金自提单并支付（status=2 已支付） */
async function createPaidPickupOrder(
  buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>,
  sku: SkuRef,
  quantity: number,
): Promise<OrderResult> {
  const created = assertOk(
    (await (
      await buyerApi.post('/api/v1/orders', {
        data: {
          delivery_type: 1,
          address_id: 0,
          remark: 'AUTO-TEST 售后窗口',
          items: [{ sku_id: sku.id, quantity }],
        },
      })
    ).json()) as ApiResponse<OrderResult>,
    'C 端下单',
  );
  const paid = (await (
    await buyerApi.post('/api/v1/payments', { data: { order_id: created.id, channel: 'cash' } })
  ).json()) as ApiResponse;
  expect(paid.code, `现金支付失败: ${paid.message}`).toBe(0);
  return created;
}

/** 创建新 C 端用户（首登绑社区后重登刷新 token，使 token 携带 community_id） */
async function newClientUser(playwright: Playwright) {
  const phone = uniquePhone();
  await clientLogin(playwright.request, phone);
  return clientLogin(playwright.request, phone);
}

/** 自提单后台核销置为已完成(5)：从 C 端详情读取取货码 */
async function completeOrderByVerify(
  adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'],
  buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>,
  orderId: number,
): Promise<OrderDetailResult> {
  const detail = assertOk(
    (await (await buyerApi.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<OrderDetailResult>,
    'C 端订单详情',
  );
  expect(detail.pickup_code, '订单详情未返回取货码').toBeTruthy();
  const verified = (await (
    await adminApi.post(`/api/v1/admin/orders/${orderId}/verify-pickup`, {
      data: { pickup_code: detail.pickup_code },
    })
  ).json()) as ApiResponse;
  expect(verified.code, `核销失败: ${verified.message}`).toBe(0);
  const after = assertOk(
    (await (await buyerApi.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<OrderDetailResult>,
    '核销后订单详情',
  );
  expect(after.status, '核销后订单应为已完成(5)').toBe(5);
  return after;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let sku: SkuRef | null = null;
/** 进入本模块前的原售后配置（afterAll 恢复，避免污染其他用例） */
let originalHours: number | null = null;
let originalPolicy = 'always';
const hasMysql = mysqlAvailable();

test.beforeAll(async ({ playwright }) => {
  // 先建买家确定社区，管理端语境与买家同社区，避免后台退款按"无权操作其他社区的订单"拒绝（同 15 号用例）
  const firstBuyer = await newClientUser(playwright);
  const admin = await newAdminContext(playwright, undefined, undefined, firstBuyer.user.community_id);
  adminApi = admin.api;
  try {
    const setting = await adminApi.get('/api/v1/admin/settings/refund');
    const body = (await setting.json()) as ApiResponse<{ completed_refund_hours?: number; refund_freight_policy?: string }>;
    if (body.code === 0 && typeof body.data?.completed_refund_hours === 'number') {
      originalHours = body.data.completed_refund_hours;
    }
    if (body.code === 0 && body.data?.refund_freight_policy) {
      originalPolicy = body.data.refund_freight_policy;
    }
    sku = await pickSku(adminApi, undefined, firstBuyer.user.community_id);
    await ensureStock(adminApi, sku.id, 100);
  } catch {
    sku = null;
  }
});

test.afterAll(async () => {
  // 恢复售后配置（含原运费策略）：服务未带新字段重启时 GET 拿不到原窗口值，兜底恢复默认 168
  if (adminApi && (originalHours !== null || originalPolicy)) {
    await adminApi.put('/api/v1/admin/settings/refund', {
      data: {
        refund_freight_policy: originalPolicy,
        completed_refund_hours: originalHours ?? 168,
      },
    });
  }
  await adminApi?.dispose();
});

test.describe.serial('已完成单窗口内 C 端申请售后', () => {
  let orderId = 0;
  let orderItemId = 0;
  let refundId = 0;
  let buyerToken = '';

  test('买家下单现金支付并核销完成', async ({ playwright }) => {
    test.skip(!sku, '环境无库存 SKU');
    const buyer = await newClientUser(playwright);
    buyerToken = buyer.access_token;
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const order = await createPaidPickupOrder(buyerApi, sku!, 2);
      orderId = order.id;
      const detail = await completeOrderByVerify(adminApi, buyerApi, orderId);
      orderItemId = detail.items?.[0]?.id ?? 0;
      expect(orderItemId).toBeGreaterThan(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('窗口内 C 端申请部分退款成功', async ({ playwright }) => {
    if (!orderId) test.skip(true, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const resp = await buyerApi.post(`/api/v1/orders/${orderId}/refund`, {
        data: { reason: 'AUTO-TEST 已完成单部分退', items: [{ order_item_id: orderItemId, quantity: 1 }] },
      });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `已完成单申请退款被拒: ${body.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('退款单可定位并审批同意', async () => {
    if (!orderId) test.skip(true, '订单未创建');
    const resp = await adminApi.get('/api/v1/admin/refunds', { params: { page: 1, page_size: 50 } });
    const body = (await resp.json()) as ApiResponse<RefundRecord[] | { list: RefundRecord[] }>;
    expect(body.code, `退款列表失败: ${body.message}`).toBe(0);
    const records = asList<RefundRecord>(body.data).filter((r) => r.order_id === orderId);
    expect(records.length, '退款列表中未找到本单退款记录').toBeGreaterThan(0);
    refundId = records[0].id;
    const approve = (await (
      await adminApi.post(`/api/v1/admin/refunds/${refundId}/approve`, { data: { note: 'AUTO-TEST 同意' } })
    ).json()) as ApiResponse;
    expect(approve.code, `审批失败: ${approve.message}`).toBe(0);
  });

  test('部分退款收尾后订单回到已完成(5)', async ({ playwright }) => {
    if (!orderId) test.skip(true, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const detail = assertOk(
        (await (await buyerApi.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<OrderDetailResult>,
        '订单详情',
      );
      // from=5 部分退款未退完必须回到 5，否则后台会把它当作待核销/待发货单处理
      expect(detail.status, '部分退款后订单应回到已完成(5)').toBe(5);
    } finally {
      await buyerApi.dispose();
    }
  });
});

test.describe.serial('超窗拒绝与后台兜底', () => {
  test.skip(() => !hasMysql, '本地 MySQL 容器不可用，无法拨 complete_time 造超窗数据');
  let orderId2 = 0;
  let buyerToken2 = '';

  test('买家下单核销完成后拨回 8 天前', async ({ playwright }) => {
    test.skip(!sku, '环境无库存 SKU');
    const buyer = await newClientUser(playwright);
    buyerToken2 = buyer.access_token;
    const buyerApi = await newAuthedRequest(playwright, buyerToken2);
    try {
      const order = await createPaidPickupOrder(buyerApi, sku!, 1);
      orderId2 = order.id;
      await completeOrderByVerify(adminApi, buyerApi, orderId2);
      // 拨 complete_time 到 8 天前，制造超窗（默认窗口 7 天）；
      // 必须用本地时间格式化：toISOString 是 UTC，与 MySQL 会话时区（+8）差 8 小时，
      // 窗口参数调小后会造成用例翻车
      const past = new Date(Date.now() - 8 * 24 * 3600 * 1000);
      const pad = (n: number) => String(n).padStart(2, '0');
      const pastStr = `${past.getFullYear()}-${pad(past.getMonth() + 1)}-${pad(past.getDate())} ${pad(past.getHours())}:${pad(past.getMinutes())}:${pad(past.getSeconds())}`;
      expect(execMysql(`UPDATE orders SET complete_time='${pastStr}' WHERE id=${orderId2}`), '拨库失败（订单应仍为已完成5）').toBe(true);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('超窗后 C 端申请被拒并提示联系门店', async ({ playwright }) => {
    if (!orderId2) test.skip(true, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken2);
    try {
      const resp = await buyerApi.post(`/api/v1/orders/${orderId2}/refund`, {
        data: { reason: 'AUTO-TEST 超窗申请' },
      });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `超窗申请未被拒绝: ${JSON.stringify(body)}`).not.toBe(0);
      expect(body.message, '超窗文案应包含售后窗口提示').toContain('窗口');
    } finally {
      await buyerApi.dispose();
    }
  });

  test('后台代客退款不受窗口限制', async () => {
    if (!orderId2) test.skip(true, '订单未创建');
    const resp = await adminApi.post(`/api/v1/admin/orders/${orderId2}/refund`, {
      data: { reason: 'AUTO-TEST 超窗后台代退' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `后台代退超窗单失败: ${body.message}`).toBe(0);
  });
});

test.describe.serial('售后窗口配置', () => {
  let orderId3 = 0;
  let buyerToken3 = '';

  test('窗口配置读写 completed_refund_hours', async () => {
    const resp = await adminApi.put('/api/v1/admin/settings/refund', {
      data: { refund_freight_policy: 'always', completed_refund_hours: 0 },
    });
    const body = (await resp.json()) as ApiResponse<{ completed_refund_hours?: number }>;
    expect(body.code, `保存售后配置失败: ${body.message}`).toBe(0);
    expect(body.data?.completed_refund_hours).toBe(0);
    const read = (await (await adminApi.get('/api/v1/admin/settings/refund')).json()) as ApiResponse<{
      completed_refund_hours?: number;
    }>;
    expect(read.data?.completed_refund_hours).toBe(0);
  });

  test('买家下单核销完成（窗口已配置为 0）', async ({ playwright }) => {
    test.skip(!sku, '环境无库存 SKU');
    const buyer = await newClientUser(playwright);
    buyerToken3 = buyer.access_token;
    const buyerApi = await newAuthedRequest(playwright, buyerToken3);
    try {
      const order = await createPaidPickupOrder(buyerApi, sku!, 1);
      orderId3 = order.id;
      await completeOrderByVerify(adminApi, buyerApi, orderId3);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('窗口为 0 时 C 端申请被拒（入口关闭）', async ({ playwright }) => {
    if (!orderId3) test.skip(true, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken3);
    try {
      const resp = await buyerApi.post(`/api/v1/orders/${orderId3}/refund`, {
        data: { reason: 'AUTO-TEST 窗口关闭申请' },
      });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `窗口关闭后申请未被拒绝: ${JSON.stringify(body)}`).not.toBe(0);
      expect(body.message, '文案应包含联系门店引导').toContain('门店');
    } finally {
      await buyerApi.dispose();
    }
  });

  test('恢复窗口配置为默认 168', async () => {
    const resp = await adminApi.put('/api/v1/admin/settings/refund', {
      data: { refund_freight_policy: 'always', completed_refund_hours: 168 },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `恢复配置失败: ${body.message}`).toBe(0);
  });
});
