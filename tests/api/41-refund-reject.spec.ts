import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  pickSku,
  ensureStock,
  assertOk,
  type ApiResponse,
  type ClientLoginInfo,
  type SkuRef,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 41：退款拒绝链路
 * 覆盖：后台拒绝退款（POST /admin/refunds/:id/reject）
 *   - reject_reason 必填（缺省被参数校验拒绝）
 *   - 拒绝后退款单状态落 3、reject_reason 落库，订单回退款前状态（2 已支付）
 *   - 拒绝后不可再审批/再拒绝（状态机防重）
 *   - retry（POST /admin/refunds/:id/retry）非「退款中(4)」状态被拒
 *   - 被拒后 C 端可重新申请退款
 * 说明：现金自提单不依赖外部支付渠道；后台订单详情含 refunds 数组（status/reject_reason）承载断言
 */

interface OrderResult {
  id: number;
  order_no: string;
  [k: string]: unknown;
}

/** 后台订单详情（截取断言相关字段） */
interface AdminOrderDetail {
  id: number;
  status: number;
  refunds?: { id: number; status: number; reject_reason?: string; amount?: number }[];
  [k: string]: unknown;
}

/** 创建现金自提单并支付（订单状态 2 已支付） */
async function createPaidOrder(buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>, sku: SkuRef): Promise<OrderResult> {
  const created = assertOk(
    (await (
      await buyerApi.post('/api/v1/orders', {
        data: {
          delivery_type: 1,
          address_id: 0,
          remark: 'AUTO-TEST 退款拒绝',
          items: [{ sku_id: sku.id, quantity: 1 }],
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

/** 读取后台订单详情（含 refunds 数组） */
async function getAdminOrder(orderId: number): Promise<AdminOrderDetail> {
  const body = (await (
    await adminApi.get(`/api/v1/admin/orders/${orderId}`)
  ).json()) as ApiResponse<AdminOrderDetail>;
  return assertOk(body, `后台订单详情(${orderId})`);
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let buyerToken = '';
let sku: SkuRef | null = null;

test.beforeAll(async ({ playwright }) => {
  const buyer = await clientLogin(playwright.request, uniquePhone());
  buyerToken = buyer.access_token;
  // 管理端语境与买家同社区，避免拒绝操作按"跨社区订单"拒绝
  const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
  adminApi = admin.api;
  try {
    sku = await pickSku(adminApi);
    await ensureStock(adminApi, sku.id, 100);
  } catch {
    sku = null;
  }
});

test.afterAll(async () => {
  await adminApi?.dispose();
});

test.describe.serial('退款拒绝链路', () => {
  let orderId = 0;
  let refundId = 0;

  test('1. 买家下单并现金支付', async ({ playwright }) => {
    test.skip(!sku, '环境无库存 SKU');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const order = await createPaidOrder(buyerApi, sku!);
      orderId = order.id;
      expect(orderId).toBeGreaterThan(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('2. 买家申请退款，订单进入退款中', async ({ playwright }) => {
    if (!orderId) test.skip(true, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const resp = await buyerApi.post(`/api/v1/orders/${orderId}/refund`, { data: { reason: 'AUTO-TEST 第一次申请' } });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `申请退款失败: ${body.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
    const detail = await getAdminOrder(orderId);
    expect(detail.status, '申请后订单应为 7 退款中').toBe(7);
    refundId = detail.refunds?.find((r) => r.status === 1)?.id ?? 0;
    expect(refundId, '退款列表中应有待审核退款单').toBeGreaterThan(0);
  });

  test('3. 缺 reject_reason 被参数校验拒绝', async () => {
    if (!refundId) test.skip(true, '退款记录未定位');
    const resp = await adminApi.post(`/api/v1/admin/refunds/${refundId}/reject`, { data: {} });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '缺 reject_reason 应返回非 0 业务码').not.toBe(0);
    // 校验失败不落库：退款单仍为待审核
    const detail = await getAdminOrder(orderId);
    expect(detail.refunds?.find((r) => r.id === refundId)?.status).toBe(1);
  });

  test('4. 后台拒绝退款成功，订单回已支付', async () => {
    if (!refundId) test.skip(true, '退款记录未定位');
    const resp = await adminApi.post(`/api/v1/admin/refunds/${refundId}/reject`, {
      data: { reject_reason: 'AUTO-TEST 凭证不足拒绝' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `拒绝退款失败: ${body.message}`).toBe(0);

    const detail = await getAdminOrder(orderId);
    const refund = detail.refunds?.find((r) => r.id === refundId);
    expect(refund?.status, '退款单应落 3 已拒绝').toBe(3);
    expect(refund?.reject_reason, '拒绝原因应落库').toBe('AUTO-TEST 凭证不足拒绝');
    expect(detail.status, '订单应回退款前状态 2 已支付').toBe(2);
  });

  test('5. 拒绝后再审批被拒（状态机防重）', async () => {
    if (!refundId) test.skip(true, '退款记录未定位');
    const resp = await adminApi.post(`/api/v1/admin/refunds/${refundId}/approve`, { data: {} });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '已拒绝退款单不应可审批').not.toBe(0);
  });

  test('6. 拒绝后再拒绝被拒（状态机防重）', async () => {
    if (!refundId) test.skip(true, '退款记录未定位');
    const resp = await adminApi.post(`/api/v1/admin/refunds/${refundId}/reject`, {
      data: { reject_reason: 'AUTO-TEST 重复拒绝' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '已拒绝退款单不应可再拒绝').not.toBe(0);
  });

  test('7. retry 非「退款中」状态被拒', async () => {
    if (!refundId) test.skip(true, '退款记录未定位');
    const resp = await adminApi.post(`/api/v1/admin/refunds/${refundId}/retry`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, 'status=3 已拒绝的退款单不应可重试').not.toBe(0);
  });

  test('8. 被拒后买家可重新申请退款', async ({ playwright }) => {
    if (!orderId) test.skip(true, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const resp = await buyerApi.post(`/api/v1/orders/${orderId}/refund`, { data: { reason: 'AUTO-TEST 重新申请' } });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `重新申请退款失败: ${body.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
    const detail = await getAdminOrder(orderId);
    expect(detail.status, '重新申请后订单应为 7 退款中').toBe(7);
    const pending = detail.refunds?.find((r) => r.status === 1);
    expect(pending?.id, '应有新的待审核退款单').toBeGreaterThan(0);
    expect(pending?.id, '新退款单应为不同记录').not.toBe(refundId);
  });

  test('9. 收尾拒绝第二次申请，原因落库', async () => {
    const detail = await getAdminOrder(orderId);
    const pending = detail.refunds?.find((r) => r.status === 1);
    if (!pending) test.skip(true, '无待审核退款单');
    const resp = await adminApi.post(`/api/v1/admin/refunds/${pending.id}/reject`, {
      data: { reject_reason: 'AUTO-TEST 收尾拒绝' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `第二次拒绝失败: ${body.message}`).toBe(0);

    const after = await getAdminOrder(orderId);
    const refund = after.refunds?.find((r) => r.id === pending.id);
    expect(refund?.status).toBe(3);
    expect(refund?.reject_reason).toBe('AUTO-TEST 收尾拒绝');
    expect(after.status).toBe(2);
  });
});
