import { test, expect, type Playwright } from '@playwright/test';
import { clientLogin, newAdminContext, newAuthedRequest, pickSku, ensureStock, type ApiResponse, type SkuRef } from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 30：货到付款取消规则
 * 覆盖：pay_type=3（C 端 cash 口径）已支付未发货 C 端直接取消 → 订单 6、库存回补、无退款流水
 *       → pay_type=3 发货后（status=4）C 端直接取消被拒，售后改走退款申请
 * 依据：需求文档 02-订单与支付「货到付款订单取消规则（2026-10-15）」
 */

interface Order {
  id: number;
  order_no: string;
  status: number;
  pay_type?: number;
  [k: string]: unknown;
}

interface RefundRecord {
  id: number;
  order_id?: number;
  order_no?: string;
  [k: string]: unknown;
}

let api: Awaited<ReturnType<typeof newAuthedRequest>>;
let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let skuRef: SkuRef | null = null;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  api = await newAuthedRequest(playwright, info.access_token);
  const admin = await newAdminContext(playwright, undefined, undefined, info.user.community_id);
  adminApi = admin.api;
});

test.afterAll(async () => {
  await api?.dispose();
  await adminApi?.dispose();
});

declare global {
  // eslint-disable-next-line no-var
  var __addressId: number;
}

/** 查询 SKU 当前库存（管理端 SKU 列表口径） */
async function skuStock(skuId: number, productId: number): Promise<number> {
  const resp = await adminApi.get('/api/v1/skus', { params: { product_id: productId } });
  const body = (await resp.json()) as ApiResponse<{ id: number; stock: number }[]>;
  return (body.data ?? []).find((s) => s.id === skuId)?.stock ?? -1;
}

test.describe.serial('货到付款取消：未发货直接取消', () => {
  let orderId = 0;
  let orderNo = '';
  let stockBefore = 0;

  test('准备：就绪 SKU 并创建收货地址', async () => {
    skuRef = await pickSku(api);
    await ensureStock(adminApi, skuRef.id, 100);
    const addr = (await (await api.post('/api/v1/user/addresses', {
      data: {
        contact_name: 'AUTO-TEST COD',
        contact_phone: '13900000002',
        province: '湖北省',
        city: '武汉市',
        district: '洪山区',
        detail: '测试路 2 号',
        tag: '家',
      },
    })).json()) as ApiResponse<{ id: number }>;
    expect(addr.code).toBe(0);
    globalThis.__addressId = addr.data!.id;
  });

  test('货到付款下单支付：status=2 且 pay_type=3', async () => {
    test.skip(!skuRef, '环境无库存 SKU');
    const resp = await api.post('/api/v1/orders', {
      data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuRef!.id, quantity: 2 }] },
    });
    const body = (await resp.json()) as ApiResponse<Order>;
    expect(body.code, `下单失败: ${body.message}`).toBe(0);
    orderId = body.data!.id;
    orderNo = body.data!.order_no;
    const pay = (await (await api.post('/api/v1/payments', { data: { order_id: orderId, channel: 'cash' } })).json()) as ApiResponse;
    expect(pay.code, `货到付款支付失败: ${pay.message}`).toBe(0);
    const detail = (await (await api.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<Order>;
    expect(detail.data?.status).toBe(2);
    expect(detail.data?.pay_type).toBe(3);
  });

  test('未发货 C 端直接取消成功 → 订单 6', async () => {
    test.skip(!orderId, '订单未创建');
    stockBefore = await skuStock(skuRef!.id, skuRef!.product_id);
    const resp = await api.put(`/api/v1/orders/${orderId}/cancel`, { data: { reason: 'AUTO-TEST 货到付款未发货直取' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `货到付款未发货取消失败: ${body.message}`).toBe(0);
    const detail = (await (await api.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<Order>;
    expect(detail.data?.status).toBe(6);
  });

  test('取消后库存回补 +2', async () => {
    test.skip(!orderId, '订单未创建');
    const stockAfter = await skuStock(skuRef!.id, skuRef!.product_id);
    expect(stockAfter).toBe(stockBefore + 2);
  });

  test('直接取消不产生退款流水', async () => {
    test.skip(!orderId, '订单未创建');
    const resp = await adminApi.get('/api/v1/admin/refunds', { params: { page: 1, page_size: 100 } });
    const body = (await resp.json()) as ApiResponse<RefundRecord[] | { list: RefundRecord[] }>;
    expect(body.code).toBe(0);
    const list = Array.isArray(body.data) ? body.data : (body.data?.list ?? []);
    expect(
      list.some((r) => r.order_no === orderNo || r.order_id === orderId),
      '直接取消不应产生退款记录',
    ).toBe(false);
  });
});

test.describe.serial('货到付款取消：发货后不可直接取消', () => {
  let deliveryOrderId = 0;

  test('配送单下单并货到付款支付', async () => {
    test.skip(!skuRef, '环境无库存 SKU');
    // 配送单受起送金额（15 元）约束，环境 SKU 单价约 1 元，按 20 件保底
    const resp = await api.post('/api/v1/orders', {
      data: { delivery_type: 2, address_id: globalThis.__addressId, items: [{ sku_id: skuRef!.id, quantity: 20 }] },
    });
    const body = (await resp.json()) as ApiResponse<Order>;
    expect(body.code, `配送单下单失败: ${body.message}`).toBe(0);
    deliveryOrderId = body.data!.id;
    const pay = (await (await api.post('/api/v1/payments', { data: { order_id: deliveryOrderId, channel: 'cash' } })).json()) as ApiResponse;
    expect(pay.code).toBe(0);
  });

  test('后台发货 → status=4', async () => {
    test.skip(!deliveryOrderId, '订单未创建');
    const ship = (await (await adminApi.post(`/api/v1/admin/orders/${deliveryOrderId}/ship`, {
      data: { express_company: 'AUTO-TEST 快递', express_no: `SF${Date.now()}` },
    })).json()) as ApiResponse;
    expect(ship.code, `发货失败: ${ship.message}`).toBe(0);
    const detail = (await (await api.get(`/api/v1/orders/${deliveryOrderId}`)).json()) as ApiResponse<Order>;
    expect(detail.data?.status).toBe(4);
  });

  test('发货后 C 端直接取消被拒（应走退款流程）', async () => {
    test.skip(!deliveryOrderId, '订单未创建');
    const resp = await api.put(`/api/v1/orders/${deliveryOrderId}/cancel`, { data: { reason: 'AUTO-TEST 发货后尝试取消' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `发货后直接取消应被拒绝: ${body.message}`).not.toBe(0);
    const detail = (await (await api.get(`/api/v1/orders/${deliveryOrderId}`)).json()) as ApiResponse<Order>;
    expect(detail.data?.status).toBe(4);
  });

  test('发货后售后改走退款申请 → status=7', async () => {
    test.skip(!deliveryOrderId, '订单未创建');
    const resp = await api.post(`/api/v1/orders/${deliveryOrderId}/refund`, { data: { reason: 'AUTO-TEST 货到付款发货后退款' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `退款申请失败: ${body.message}`).toBe(0);
    const detail = (await (await api.get(`/api/v1/orders/${deliveryOrderId}`)).json()) as ApiResponse<Order>;
    expect(detail.data?.status).toBe(7);
  });
});
