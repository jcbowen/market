import { test, expect, type Playwright } from '@playwright/test';
import { clientLogin, newAuthedRequest, type ApiResponse } from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 29：支付单幂等
 * 覆盖：支付渠道列表与配置过滤（未配置渠道不下发）→ 未配置渠道发起支付被拒
 *       → cash（货到付款口径）创建即成功并同步订单已支付 → 已支付订单再次发起支付被拦截
 *       →（条件）待支付单重复发起同渠道支付复用同一支付单号（需环境配置微信渠道）
 * 说明：复用逻辑仅适用于微信/支付宝等第三方渠道（payment.go findReusablePendingPayment），
 *       现金/货到付款创建即成功无待支付态；环境无微信配置时复用用例自动 skip
 */

interface Channel {
  channel: string;
  name: string;
}

interface Order {
  id: number;
  order_no: string;
  status: number;
}

interface PaymentResp {
  id?: number;
  payment_no: string;
  [k: string]: unknown;
}

let api: Awaited<ReturnType<typeof newAuthedRequest>>;
let buyerCommunityId = 0;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  api = await newAuthedRequest(playwright, info.access_token);
  buyerCommunityId = info.user.community_id;
});

test.afterAll(async () => {
  await api?.dispose();
});

/** 创建自提待支付订单（quantity 错开同用户连续下单的重复指纹） */
async function createPickupOrder(skuId: number, quantity = 1): Promise<Order> {
  const resp = await api.post('/api/v1/orders', {
    data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuId, quantity }] },
  });
  const body = (await resp.json()) as ApiResponse<Order>;
  if (body.code !== 0) throw new Error(`创建订单失败 code=${body.code} message=${body.message}`);
  return body.data as Order;
}

test.describe.serial('支付单幂等', () => {
  let skuId = 0;
  let wechatAvailable = false;

  test('支付渠道列表：未配置渠道不下发（收银台展示依据）', async () => {
    const resp = await api.get('/api/v1/payments/channels');
    const body = (await resp.json()) as ApiResponse<{ channels: Channel[] }>;
    expect(body.code).toBe(0);
    const channels = body.data?.channels ?? [];
    expect(Array.isArray(channels)).toBe(true);
    expect(channels.some((c) => c.channel === 'cash')).toBe(true);
    wechatAvailable = channels.some((c) => c.channel === 'wechat');
  });

  test('准备：就绪可下单 SKU（优先环境商品，缺失时自建兜底）', async ({ playwright }) => {
    const { newAdminContext, pickSku, ensureStock } = await import('../helpers/api');
    const { autoName } = await import('../helpers/data');
    const admin = await newAdminContext(playwright, undefined, undefined, buyerCommunityId);
    try {
      try {
        const sku = await pickSku(admin.api, undefined, buyerCommunityId);
        skuId = sku.id;
        await ensureStock(admin.api, skuId, 50);
      } catch {
        const cat = (await (await admin.api.post('/api/v1/admin/categories', { data: { name: autoName('支付幂等分类') } })).json()) as ApiResponse<{ id: number }>;
        const prod = (await (await admin.api.post('/api/v1/admin/products', {
          data: {
            category_id: cat.data!.id,
            name: autoName('支付幂等商品'),
            unit: '份',
            is_multi_spec: false,
            sku_items: [{ price: 3.3, stock: 50 }],
          },
        })).json()) as ApiResponse<{ product?: { id: number }; id?: number }>;
        const productId = prod.data?.product?.id ?? prod.data?.id ?? 0;
        await admin.api.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } });
        const detail = (await (await admin.api.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<{ skus: { id: number }[] }>;
        skuId = detail.data?.skus?.[0]?.id ?? 0;
      }
    } finally {
      await admin.api.dispose();
    }
    expect(skuId, 'SKU 未就绪').toBeGreaterThan(0);
  });

  test('未配置渠道（wechat）发起支付被拒', async () => {
    test.skip(wechatAvailable, '环境已配置微信渠道，跳过未配置渠道负向用例');
    const fresh = await createPickupOrder(skuId, 1);
    try {
      const resp = await api.post('/api/v1/payments', { data: { order_id: fresh.id, channel: 'wechat' } });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `未配置渠道应被拒绝: ${body.message}`).not.toBe(0);
    } finally {
      await api.put(`/api/v1/orders/${fresh.id}/cancel`);
    }
  });

  test('cash 支付创建即成功（货到付款口径，无待支付态）', async () => {
    const fresh = await createPickupOrder(skuId, 2);
    const resp = await api.post('/api/v1/payments', { data: { order_id: fresh.id, channel: 'cash' } });
    const body = (await resp.json()) as ApiResponse<PaymentResp>;
    expect(body.code, `cash 支付失败: ${body.message}`).toBe(0);
    expect(body.data?.payment_no).toBeTruthy();
    const detail = (await (await api.get(`/api/v1/orders/${fresh.id}`)).json()) as ApiResponse<Order>;
    expect(detail.data?.status, '货到付款创建支付单即同步订单已支付').toBe(2);
  });

  test('已支付订单再次发起支付被状态机拦截', async () => {
    const fresh = await createPickupOrder(skuId, 3);
    const payResp = await api.post('/api/v1/payments', { data: { order_id: fresh.id, channel: 'cash' } });
    const payBody = (await payResp.json()) as ApiResponse<PaymentResp>;
    expect(payBody.code, `首次支付失败: ${payBody.message}`).toBe(0);

    const resp = await api.post('/api/v1/payments', { data: { order_id: fresh.id, channel: 'cash' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `已支付订单再次支付应被拦截: ${body.message}`).not.toBe(0);
    // 网关订单状态校验优先命中「订单当前状态不可支付」；穿透到支付服务则为幂等「请勿重复支付」
    expect(body.message).toMatch(/不可支付|重复支付/);
  });

  test('（条件）待支付单重复发起同渠道支付复用同一支付单号', async () => {
    test.skip(!wechatAvailable, '环境未配置微信渠道，复用逻辑无法实测');
    const fresh = await createPickupOrder(skuId, 4);
    const first = (await (await api.post('/api/v1/payments', { data: { order_id: fresh.id, channel: 'wechat' } })).json()) as ApiResponse<PaymentResp>;
    expect(first.code, `首次微信支付失败: ${first.message}`).toBe(0);
    const second = (await (await api.post('/api/v1/payments', { data: { order_id: fresh.id, channel: 'wechat' } })).json()) as ApiResponse<PaymentResp>;
    expect(second.code, `重复微信支付失败: ${second.message}`).toBe(0);
    expect(second.data?.payment_no, '复用应返回同一支付单号').toBe(first.data?.payment_no);
    await api.put(`/api/v1/orders/${fresh.id}/cancel`);
  });
});
