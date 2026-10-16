import { test, expect } from '@playwright/test';
import { clientLogin, newAdminContext, newAuthedRequest, pickSku, ensureStock, asList, type ApiResponse, type PageData } from '../helpers/api';
import { uniquePhone } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 27：门店订单只读（收银台代客单 source=2）
 * 覆盖：POS 为会员下自提单 → C 端列表/详情可见（source=2 门店标识、pay_type=4、取货码）
 *       → C 端取消/申请退款被拒 11005、确认收货被拒 10001（统一文案「门店订单请联系门店处理」）
 *       → C 端再次发起支付被幂等拦截 → 后台取消该门店单成功（售后走后台口径）
 * 流程：开工班次 → POS 会员单 → C 端可见性 → C 端只读拦截四连 → 后台兜底取消
 */

const STORE_ORDER_MSG = '门店订单请联系门店处理';

interface PosOrderResp {
  id: number;
  order_no: string;
  pay_amount: number;
  delivery_fee: number;
  pickup_code: string;
  paid: boolean;
}

interface ClientOrder {
  id: number;
  order_no: string;
  status: number;
  source: number;
  pay_type: number;
  pay_amount: number;
  pickup_code?: string;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let clientApi: Awaited<ReturnType<typeof newAuthedRequest>>;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;

  // 开工班次（容错：已有进行中班次时 code!=0，继续用当前班次）
  const startResp = await adminApi.post('/api/v1/admin/pos/shifts/start', { data: { note: 'AUTO-TEST 门店只读开工' } });
  const startBody = (await startResp.json()) as ApiResponse;
  if (startBody.code !== 0) {
    console.log(`[27] 开工返回 code=${startBody.code} message=${startBody.message}（可能已有进行中班次）`);
  }
  const curResp = await adminApi.get('/api/v1/admin/pos/shifts/current');
  const curBody = (await curResp.json()) as ApiResponse<{ id?: number }>;
  if (!curBody.data?.id) throw new Error('POS 无进行中班次且开工失败，门店单用例无法执行');

  const sku = await pickSku(adminApi);
  await ensureStock(adminApi, sku.id, 100);
  globalThis.__sku = sku.id;

  const member = await clientLogin(playwright.request, uniquePhone());
  globalThis.__memberId = member.user.id;
  clientApi = await newAuthedRequest(playwright, member.access_token);
});

test.afterAll(async () => {
  await clientApi?.dispose();
  await adminApi?.dispose();
});

declare global {
  // eslint-disable-next-line no-var
  var __sku: number;
  // eslint-disable-next-line no-var
  var __memberId: number;
}

test.describe.serial('门店订单只读', () => {
  let orderId = 0;
  let orderNo = '';

  test('POS 为会员下自提单成功（source=2 已收款）', async () => {
    test.skip(!globalThis.__sku, '环境无库存 SKU');
    const resp = await adminApi.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: globalThis.__memberId,
        pay_type: 4,
        delivery_type: 1,
        remark: 'AUTO-TEST 门店会员自提单',
        items: [{ sku_id: globalThis.__sku, quantity: 1 }],
      },
    });
    const body = (await resp.json()) as ApiResponse<PosOrderResp>;
    expect(body.code, `POS 会员单失败: ${body.message}`).toBe(0);
    orderId = body.data!.id;
    orderNo = body.data!.order_no;
    expect(orderId).toBeGreaterThan(0);
    expect(body.data!.paid).toBe(true);
    expect(body.data!.pay_amount).toBeGreaterThan(0);
  });

  test('C 端订单列表可见门店单（source=2 标识）', async () => {
    test.skip(!orderId, '订单未创建');
    const resp = await clientApi.get('/api/v1/orders', { params: { page: 1, page_size: 20 } });
    const body = (await resp.json()) as ApiResponse<PageData<ClientOrder>>;
    expect(body.code).toBe(0);
    const found = asList<ClientOrder>(body.data).find((o) => o.id === orderId);
    expect(found, 'C 端订单列表应包含收银台代客单').toBeTruthy();
    expect(found!.source).toBe(2);
  });

  test('C 端订单详情可查：source=2、pay_type=4、含取货码', async () => {
    test.skip(!orderId, '订单未创建');
    const resp = await clientApi.get(`/api/v1/orders/${orderId}`);
    const body = (await resp.json()) as ApiResponse<ClientOrder>;
    expect(body.code).toBe(0);
    expect(body.data!.order_no).toBe(orderNo);
    expect(body.data!.source).toBe(2);
    expect(body.data!.pay_type).toBe(4);
    expect(body.data!.status).toBe(2);
    expect(body.data!.pickup_code, '自提单详情应含取货码').toBeTruthy();
  });

  test('C 端取消门店单被拒（11005 门店订单请联系门店处理）', async () => {
    test.skip(!orderId, '订单未创建');
    const resp = await clientApi.put(`/api/v1/orders/${orderId}/cancel`, { data: { reason: 'AUTO-TEST 尝试取消' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(11005);
    expect(body.message).toBe(STORE_ORDER_MSG);
  });

  test('C 端申请退款被拒（11005 门店订单请联系门店处理）', async () => {
    test.skip(!orderId, '订单未创建');
    const resp = await clientApi.post(`/api/v1/orders/${orderId}/refund`, { data: { reason: 'AUTO-TEST 尝试退款' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(11005);
    expect(body.message).toBe(STORE_ORDER_MSG);
  });

  test('C 端确认收货被拒（10001 门店订单请联系门店处理）', async () => {
    test.skip(!orderId, '订单未创建');
    const resp = await clientApi.post(`/api/v1/orders/${orderId}/receive`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(10001);
    expect(body.message).toBe(STORE_ORDER_MSG);
  });

  test('C 端再次发起支付被拦截（状态机/幂等双重防护）', async () => {
    test.skip(!orderId, '订单未创建');
    const resp = await clientApi.post('/api/v1/payments', { data: { order_id: orderId, channel: 'cash' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `已支付订单再次发起支付应被拦截: ${body.message}`).not.toBe(0);
    // 网关订单状态校验「订单当前状态不可支付」；若穿透到支付服务则命中幂等「请勿重复支付」
    expect(body.message).toMatch(/不可支付|重复支付/);
  });

  test('后台对已收款门店单直接取消被拒（应走代客退款）', async () => {
    test.skip(!orderId, '订单未创建');
    const resp = await adminApi.post(`/api/v1/admin/orders/${orderId}/cancel`, {
      data: { reason: 'AUTO-TEST 后台取消门店单' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `已收款门店单取消应被拒绝: ${body.message}`).not.toBe(0);
    expect(body.message).toContain('不允许取消');
  });

  test('后台代客退款门店单成功 → 订单 8 已退款', async () => {
    test.skip(!orderId, '订单未创建');
    const resp = await adminApi.post(`/api/v1/admin/orders/${orderId}/refund`, {
      data: { reason: 'AUTO-TEST 门店单后台代退' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `后台代客退款失败: ${body.message}`).toBe(0);
    const detail = (await (await adminApi.get(`/api/v1/admin/orders/${orderId}`)).json()) as ApiResponse<{
      status: number;
      refunds?: { id: number; status: number }[];
    }>;
    expect(detail.data?.status).toBe(8);
    expect(detail.data?.refunds?.length ?? 0).toBeGreaterThan(0);
  });
});
