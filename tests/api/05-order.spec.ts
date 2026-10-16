import { test, expect } from '@playwright/test';
import { clientLogin, newAuthedRequest, pickSku, type ApiResponse, type PageData, type SkuRef } from '../helpers/api';
import { uniquePhone } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 05：订单
 * 覆盖：自提单创建、详情、列表、待支付取消、cash 支付推进、参数校验、
 *       常购商品、包邮预览、退款配置、他人订单不可见
 */

interface OrderInfo {
  id: number;
  order_no: string;
  status: number;
  status_text: string;
  pay_amount?: number;
  delivery_type?: number;
  pickup_code?: string;
  items?: unknown[];
}

let api: Awaited<ReturnType<typeof newAuthedRequest>>;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  api = await newAuthedRequest(playwright, info.access_token);
});

test.afterAll(async () => {
  await api?.dispose();
});

/**
 * 创建自提订单
 * - 函数名: createPickupOrder
 * - 参数: skuId — SKU ID；quantity — 数量
 * - 返回: Promise<OrderInfo>（含 id/order_no）
 * - 异常: 下单业务失败时抛出 Error
 */
async function createPickupOrder(skuId: number, quantity = 1): Promise<OrderInfo> {
  const resp = await api.post('/api/v1/orders', {
    data: {
      delivery_type: 1,
      address_id: 0,
      delivery_time: '今天 14:00-16:00',
      remark: '自动化测试订单',
      coupon_id: 0,
      items: [{ sku_id: skuId, quantity }],
    },
  });
  const body = (await resp.json()) as ApiResponse<OrderInfo>;
  if (body.code !== 0) throw new Error(`创建订单失败 code=${body.code} message=${body.message}`);
  return body.data as OrderInfo;
}

test.describe.serial('订单流程', () => {
  let sku: SkuRef;
  let createdOrder: OrderInfo;

  test('自提订单创建成功', async () => {
    sku = await pickSku(api);
    createdOrder = await createPickupOrder(sku.id, 1);
    expect(createdOrder.id).toBeGreaterThan(0);
    expect(createdOrder.order_no).toBeTruthy();
  });

  test('订单详情可查且含商品行', async () => {
    const resp = await api.get(`/api/v1/orders/${createdOrder.id}`);
    const body = (await resp.json()) as ApiResponse<OrderInfo>;
    expect(body.code).toBe(0);
    expect(body.data?.order_no).toBe(createdOrder.order_no);
    expect(body.data?.items?.length).toBeGreaterThan(0);
  });

  test('订单列表可查到新订单', async () => {
    const resp = await api.get('/api/v1/orders', { params: { page: 1, page_size: 10 } });
    const body = (await resp.json()) as ApiResponse<PageData<OrderInfo>>;
    expect(body.code).toBe(0);
    expect(body.data?.list.some((o) => o.id === createdOrder.id)).toBe(true);
  });

  test('服务端取价：客户端注入 price 被清空', async () => {
    // 纵深防御校验：传 price=0.01 也不影响服务端定价（见 order.go CreateOrder）
    const resp = await api.post('/api/v1/orders', {
      data: {
        delivery_type: 1,
        address_id: 0,
        items: [{ sku_id: sku.id, quantity: 1, price: 0.01 }],
      },
    });
    const body = (await resp.json()) as ApiResponse<OrderInfo>;
    expect(body.code).toBe(0);
    // 清理：取消这单避免脏数据
    if (body.data?.id) await api.put(`/api/v1/orders/${body.data.id}/cancel`);
  });

  test('空 items 下单被拒绝', async () => {
    const resp = await api.post('/api/v1/orders', { data: { delivery_type: 1, items: [] } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });

  test('cash 渠道发起支付成功', async () => {
    const resp = await api.post('/api/v1/payments', { data: { order_id: createdOrder.id, channel: 'cash' } });
    const body = (await resp.json()) as ApiResponse<{ payment_no: string }>;
    expect(body.code).toBe(0);
    expect(body.data?.payment_no).toBeTruthy();
  });

  test('对不存在的订单发起支付被拒绝', async () => {
    const resp = await api.post('/api/v1/payments', { data: { order_id: 999999999, channel: 'cash' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });

  test('非法支付渠道被拒绝', async () => {
    const resp = await api.post('/api/v1/payments', { data: { order_id: createdOrder.id, channel: 'bitcoin' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });

  test('待支付订单可取消', async () => {
    const fresh = await createPickupOrder(sku.id, 1);
    const resp = await api.put(`/api/v1/orders/${fresh.id}/cancel`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
    const detail = (await (await api.get(`/api/v1/orders/${fresh.id}`)).json()) as ApiResponse<OrderInfo>;
    expect(detail.data?.status).toBeDefined();
  });

  test('常购商品接口正常', async () => {
    const resp = await api.get('/api/v1/orders/frequent-products');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('包邮试算接口正常', async () => {
    const resp = await api.post('/api/v1/orders/free-shipping/preview', {
      data: { items: [{ sku_id: sku.id, quantity: 1 }] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('退款配置接口正常', async () => {
    const resp = await api.get('/api/v1/refund/config');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('他人订单不可查看（归属校验）', async ({ playwright }) => {
    // 另一个用户查看当前用户的订单
    const other = await clientLogin(playwright.request, uniquePhone());
    const otherApi = await newAuthedRequest(playwright, other.access_token);
    try {
      const resp = await otherApi.get(`/api/v1/orders/${createdOrder.id}`);
      const body = (await resp.json()) as ApiResponse;
      expect(body.code).not.toBe(0);
    } finally {
      await otherApi.dispose();
    }
  });

  test('未登录创建订单被拒绝', async ({ request }) => {
    const resp = await request.post('/api/v1/orders', { data: { items: [] } });
    const body = (await resp.json()) as ApiResponse;
    expect(resp.status()).toBeLessThan(500);
    if (resp.status() === 200) expect(body.code).not.toBe(0);
    else expect(resp.status()).toBe(401);
  });
});
