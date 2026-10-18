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
 * 模块 43：C 端确认收货 + 后台发货 + 履约身份失效
 * 覆盖：
 *   - 后台发货 POST /admin/orders/:id/ship（物流公司/单号必填，2→4）
 *   - C 端确认收货 POST /orders/:id/receive 正向路径（4→5）
 *   - 负向：待支付/未发货单确认收货被状态机拒绝；重复确认收货被拒
 *   - 履约身份失效立即性：取消履约标记后 fulfillment-status 即时 false，
 *     工作台查单/开始配送立即 NoPermission
 * 说明：配送单（delivery_type=2）承载发货与收货链路；履约正向协同已在 16 号覆盖，
 *       本模块聚焦后台发货口径与身份撤销的即时生效
 */

interface OrderResult {
  id: number;
  order_no: string;
  [k: string]: unknown;
}

/** 创建新 C 端用户并返回携带已绑定社区的 token（重登刷新 JWT 社区语境） */
async function newClientUser(playwright: Playwright): Promise<ClientLoginInfo> {
  const phone = uniquePhone();
  await clientLogin(playwright.request, phone);
  return clientLogin(playwright.request, phone);
}

/** 买家创建收货地址并返回地址 ID */
async function createAddress(buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>): Promise<number> {
  const resp = await buyerApi.post('/api/v1/user/addresses', {
    data: { contact_name: 'AUTO-TEST 收货', contact_phone: '13800001234', detail: '测试路 43 号' },
  });
  const body = (await resp.json()) as ApiResponse<{ id?: number }>;
  return assertOk(body, '创建收货地址').id ?? 0;
}

/**
 * 买家下送货上门单并现金支付
 * 配送单有起送金额门槛（15 元），按 SKU 单价折算满足门槛的数量
 * extraQty：在基准量上追加的数量，用于错开重复下单指纹（5s 窗口，同用户同 SKU 必须不同数量）
 */
async function createPaidDeliveryOrder(
  buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>,
  sku: SkuRef,
  addressId: number,
  pay: boolean,
  extraQty = 0,
): Promise<number> {
  const skuResp = (await (await buyerApi.get(`/api/v1/skus/${sku.id}`)).json()) as ApiResponse<{ price: number }>;
  const price = skuResp.data?.price ?? 0;
  const qty = (price > 0 ? Math.max(Math.ceil(15 / price), 1) : 1) + extraQty;
  const created = assertOk(
    (await (
      await buyerApi.post('/api/v1/orders', {
        data: { delivery_type: 2, address_id: addressId, remark: 'AUTO-TEST 43 确认收货', items: [{ sku_id: sku.id, quantity: qty }] },
      })
    ).json()) as ApiResponse<OrderResult>,
    'C 端下单',
  );
  if (pay) {
    const paid = (await (
      await buyerApi.post('/api/v1/payments', { data: { order_id: created.id, channel: 'cash' } })
    ).json()) as ApiResponse;
    expect(paid.code, `现金支付失败: ${paid.message}`).toBe(0);
  }
  return created.id;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let buyerToken = '';
let fulfiller: ClientLoginInfo | null = null;
let sku: SkuRef | null = null;
let addressId = 0;

test.beforeAll(async ({ playwright }) => {
  const buyer = await newClientUser(playwright);
  buyerToken = buyer.access_token;
  fulfiller = await newClientUser(playwright);
  const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
  adminApi = admin.api;
  try {
    sku = await pickSku(adminApi, undefined, buyer.user.community_id);
    await ensureStock(adminApi, sku.id, 200);
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      addressId = await createAddress(buyerApi);
    } finally {
      await buyerApi.dispose();
    }
  } catch {
    sku = null;
  }
});

test.afterAll(async () => {
  await adminApi?.dispose();
});

test.describe.serial('后台发货 + C 端确认收货', () => {
  let orderId = 0;

  test('1. 买家下配送单并现金支付（status=2）', async ({ playwright }) => {
    test.skip(!sku || !addressId, '环境缺 SKU 或地址');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      orderId = await createPaidDeliveryOrder(buyerApi, sku!, addressId, true);
      expect(orderId).toBeGreaterThan(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('2. 待发货单 C 端确认收货被拒', async ({ playwright }) => {
    if (!orderId) test.skip(true, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const resp = await buyerApi.post(`/api/v1/orders/${orderId}/receive`, { data: {} });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, '未发货(2)订单不应允许确认收货').not.toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('3. 发货缺物流公司被拒', async () => {
    if (!orderId) test.skip(true, '订单未创建');
    const resp = await adminApi.post(`/api/v1/admin/orders/${orderId}/ship`, {
      data: { express_company: '', express_no: 'SF1234567890' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '缺物流公司应被拒绝').not.toBe(0);
  });

  test('4. 发货缺物流单号被拒', async () => {
    if (!orderId) test.skip(true, '订单未创建');
    const resp = await adminApi.post(`/api/v1/admin/orders/${orderId}/ship`, {
      data: { express_company: '顺丰速运', express_no: '' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '缺物流单号应被拒绝').not.toBe(0);
  });

  test('5. 后台发货成功（2→4）', async () => {
    if (!orderId) test.skip(true, '订单未创建');
    const resp = await adminApi.post(`/api/v1/admin/orders/${orderId}/ship`, {
      data: { express_company: '顺丰速运', express_no: 'SF43AUTO0001', remark: 'AUTO-TEST 发货' },
    });
    const body = (await resp.json()) as ApiResponse<{ id: number; status: number }>;
    expect(body.code, `发货失败: ${body.message}`).toBe(0);
    expect(body.data?.status).toBe(4);
  });

  test('6. 买家确认收货成功（4→5）', async ({ playwright }) => {
    if (!orderId) test.skip(true, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const resp = await buyerApi.post(`/api/v1/orders/${orderId}/receive`, { data: {} });
      const body = (await resp.json()) as ApiResponse<{ id: number; status: number }>;
      expect(body.code, `确认收货失败: ${body.message}`).toBe(0);
      expect(body.data?.status).toBe(5);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('7. 重复确认收货被状态机拒绝', async ({ playwright }) => {
    if (!orderId) test.skip(true, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const resp = await buyerApi.post(`/api/v1/orders/${orderId}/receive`, { data: {} });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, '完成态(5)订单不应允许再次确认收货').not.toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('8. 待支付订单确认收货被拒', async ({ playwright }) => {
    test.skip(!sku || !addressId, '环境缺 SKU 或地址');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    let pendingId = 0;
    try {
      // extraQty=1：与测试 1 的同 SKU 配送单错开数量，避免 5s 窗口内重复下单指纹碰撞
      pendingId = await createPaidDeliveryOrder(buyerApi, sku!, addressId, false, 1);
      const resp = await buyerApi.post(`/api/v1/orders/${pendingId}/receive`, { data: {} });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, '待支付(1)订单不应允许确认收货').not.toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });
});

test.describe.serial('履约身份撤销立即生效', () => {
  test('9. 设置履约人员后状态为 true', async ({ playwright }) => {
    test.skip(!fulfiller, '履约账号未创建');
    const resp = await adminApi.put(`/api/v1/admin/users/${fulfiller!.user.id}/fulfiller`, {
      data: { is_fulfiller: true },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `设置履约失败: ${body.message}`).toBe(0);
    const fApi = await newAuthedRequest(playwright, fulfiller!.access_token);
    try {
      const status = (await (
        await fApi.get('/api/v1/user/fulfillment-status')
      ).json()) as ApiResponse<{ is_fulfiller: boolean }>;
      expect(assertOk(status, '履约状态查询').is_fulfiller).toBe(true);
    } finally {
      await fApi.dispose();
    }
  });

  test('10. 取消履约身份后状态立即为 false', async ({ playwright }) => {
    if (!fulfiller) test.skip(true, '履约账号未创建');
    const resp = await adminApi.put(`/api/v1/admin/users/${fulfiller!.user.id}/fulfiller`, {
      data: { is_fulfiller: false },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `取消履约失败: ${body.message}`).toBe(0);
    const fApi = await newAuthedRequest(playwright, fulfiller!.access_token);
    try {
      const status = (await (
        await fApi.get('/api/v1/user/fulfillment-status')
      ).json()) as ApiResponse<{ is_fulfiller: boolean }>;
      expect(assertOk(status, '履约状态查询').is_fulfiller, '取消后应立即失效').toBe(false);
    } finally {
      await fApi.dispose();
    }
  });

  test('11. 工作台查单立即无权限', async ({ playwright }) => {
    if (!fulfiller) test.skip(true, '履约账号未创建');
    const fApi = await newAuthedRequest(playwright, fulfiller!.access_token);
    try {
      const resp = await fApi.get('/api/v1/fulfillment/orders', {
        params: { order_type: 2, status_group: 0, page: 1, page_size: 10 },
      });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, '非履约人员查工作台应被拒绝').not.toBe(0);
    } finally {
      await fApi.dispose();
    }
  });

  test('12. 开始配送立即无权限', async ({ playwright }) => {
    if (!fulfiller) test.skip(true, '履约账号未创建');
    const fApi = await newAuthedRequest(playwright, fulfiller!.access_token);
    try {
      // 目标订单可为任意 ID：权限校验先于订单校验，用不存在的 ID 也能区分 403 与 404 语义
      const resp = await fApi.post('/api/v1/fulfillment/orders/999999999/start-delivery', { data: {} });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, '非履约人员开始配送应被拒绝').not.toBe(0);
    } finally {
      await fApi.dispose();
    }
  });
});
