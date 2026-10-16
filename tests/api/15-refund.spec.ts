import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  pickSku,
  ensureStock,
  assertOk,
  asList,
  type ApiResponse,
  type ClientLoginInfo,
  type SkuRef,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 15：退款流程
 * 覆盖：C 端申请退款 → 后台退款列表可见 → 审批同意；
 *       后台代客退款（即时执行）；空原因拒绝
 * 说明：退款列表以 order_id 关联定位记录；现金支付单审批即为完成态，不依赖外部支付渠道
 */

interface OrderResult {
  id: number;
  order_no: string;
  amount?: number;
  [k: string]: unknown;
}

interface RefundRecord {
  id: number;
  order_id?: number;
  status?: number;
  [k: string]: unknown;
}

/** 创建现金自提单（共用前置）：items 数组结构 + payments 接口发起 cash 支付 */
async function createPaidOrder(buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>, sku: SkuRef): Promise<OrderResult> {
  const created = assertOk(
    (await (
      await buyerApi.post('/api/v1/orders', {
        data: {
          delivery_type: 1,
          address_id: 0,
          remark: 'AUTO-TEST 退款',
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

/**
 * 创建新 C 端用户并返回携带已绑定社区的 token
 * clientLogin 首登会自动绑社区，但登录时签发的 JWT 不含 community_id，
 * 下单等依赖社区语境的接口会报参数错误，故绑社区后用同号重登刷新 token
 */
async function newClientUser(playwright: Playwright): Promise<ClientLoginInfo> {
  const phone = uniquePhone();
  await clientLogin(playwright.request, phone);
  return clientLogin(playwright.request, phone);
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let buyerToken = '';
let sku: SkuRef | null = null;

test.beforeAll(async ({ playwright }) => {
  const buyer = await newClientUser(playwright);
  buyerToken = buyer.access_token;
  // 管理端语境与 C 端买家同社区，避免后台退款按"无权操作其他社区的订单"拒绝
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

test.describe.serial('C 端申请退款 + 后台审批', () => {
  let orderId = 0;
  let refundId = 0;

  test('买家下单并现金支付', async ({ playwright }) => {
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

  test('买家申请退款成功', async ({ playwright }) => {
    if (!orderId) test.skip(true, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const resp = await buyerApi.post(`/api/v1/orders/${orderId}/refund`, { data: { reason: 'AUTO-TEST 申请退款' } });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `申请退款失败: ${body.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('后台退款列表可见该退款记录', async () => {
    if (!orderId) test.skip(true, '订单未创建');
    const resp = await adminApi.get('/api/v1/admin/refunds', { params: { page: 1, page_size: 50 } });
    const body = (await resp.json()) as ApiResponse<RefundRecord[] | { list: RefundRecord[] }>;
    expect(body.code, `退款列表失败: ${body.message}`).toBe(0);
    const records = asList<RefundRecord>(body.data).filter((r) => r.order_id === orderId);
    expect(records.length, '退款列表中未找到本单退款记录').toBeGreaterThan(0);
    refundId = records[0].id;
  });

  test('后台审批同意退款', async () => {
    if (!refundId) test.skip(true, '退款记录未定位');
    const resp = await adminApi.post(`/api/v1/admin/refunds/${refundId}/approve`, { data: { note: 'AUTO-TEST 同意' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `审批失败: ${body.message}`).toBe(0);
  });

  test('重复审批被拒绝（状态机防重）', async () => {
    if (!refundId) test.skip(true, '退款记录未定位');
    const resp = await adminApi.post(`/api/v1/admin/refunds/${refundId}/approve`, { data: {} });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });
});

test.describe.serial('后台代客退款', () => {
  let orderId2 = 0;

  test('买家再下一单并现金支付', async ({ playwright }) => {
    test.skip(!sku, '环境无库存 SKU');
    const buyer = await newClientUser(playwright);
    const buyerApi = await newAuthedRequest(playwright, buyer.access_token);
    try {
      const order = await createPaidOrder(buyerApi, sku!);
      orderId2 = order.id;
      expect(orderId2).toBeGreaterThan(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('后台直接退款成功', async () => {
    if (!orderId2) test.skip(true, '订单未创建');
    const resp = await adminApi.post(`/api/v1/admin/orders/${orderId2}/refund`, {
      data: { reason: 'AUTO-TEST 后台代退' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `后台退款失败: ${body.message}`).toBe(0);
  });

  test('退款原因为空被拒绝', async () => {
    if (!orderId2) test.skip(true, '订单未创建');
    const resp = await adminApi.post(`/api/v1/admin/orders/${orderId2}/refund`, { data: {} });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });
});
