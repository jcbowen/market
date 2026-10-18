import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  pickSku,
  ensureStock,
  assertOk,
  type ApiResponse,
  type SkuRef,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 46：后台取消订单
 * 覆盖：
 *   - 取消原因必填（缺省被参数校验拒绝）
 *   - 待支付单后台取消成功：状态落 6、cancel_reason/cancel_time 落库、库存回补
 *   - 已取消单重复取消被状态机拒绝
 *   - 已支付现金单（pay_type=3 COD 口径）后台取消成功并回补库存
 *   - 已发货(4)单后台取消被状态机拒绝
 * 说明：取消口径 cancelableStatuses——普通单仅待支付(1)；COD（含现金支付）2/3 亦可；
 *       C 端取消与 COD 退款链路已在 30 号覆盖，本模块聚焦后台入口
 */

interface OrderResult {
  id: number;
  order_no: string;
  [k: string]: unknown;
}

interface AdminOrderDetail {
  id: number;
  status: number;
  cancel_reason?: string;
  cancel_time?: string;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let buyerToken = '';
let sku: SkuRef | null = null;

/** 读取 SKU 当前库存（管理端商品详情） */
async function currentStock(): Promise<number> {
  const detail = (await (
    await adminApi.get(`/api/v1/admin/products/${sku!.product_id}`)
  ).json()) as ApiResponse<{ skus: { id: number; stock: number }[] }>;
  const target = (assertOk(detail, '商品详情').skus ?? []).find((s) => s.id === sku!.id);
  expect(target, 'SKU 不存在').toBeTruthy();
  return target!.stock;
}

/** 创建待支付自提单（不支付，订单状态 1） */
async function createPendingOrder(buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>, quantity: number): Promise<number> {
  const created = assertOk(
    (await (
      await buyerApi.post('/api/v1/orders', {
        data: {
          delivery_type: 1,
          address_id: 0,
          remark: 'AUTO-TEST 46 后台取消',
          items: [{ sku_id: sku!.id, quantity }],
        },
      })
    ).json()) as ApiResponse<OrderResult>,
    'C 端下单',
  );
  return created.id;
}

/** 读取后台订单详情 */
async function getAdminOrder(orderId: number): Promise<AdminOrderDetail> {
  const body = (await (
    await adminApi.get(`/api/v1/admin/orders/${orderId}`)
  ).json()) as ApiResponse<AdminOrderDetail>;
  return assertOk(body, `后台订单详情(${orderId})`);
}

test.beforeAll(async ({ playwright }) => {
  const buyer = await clientLogin(playwright.request, uniquePhone());
  buyerToken = buyer.access_token;
  const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
  adminApi = admin.api;
  try {
    sku = await pickSku(adminApi, undefined, buyer.user.community_id);
    await ensureStock(adminApi, sku.id, 100);
  } catch {
    sku = null;
  }
});

test.afterAll(async () => {
  await adminApi?.dispose();
});

test.describe.serial('后台取消待支付订单', () => {
  test('1. 缺取消原因被参数校验拒绝', async ({ playwright }) => {
    test.skip(!sku, '环境无库存 SKU');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    let orderId = 0;
    try {
      orderId = await createPendingOrder(buyerApi, 1);
    } finally {
      await buyerApi.dispose();
    }
    expect(orderId).toBeGreaterThan(0);
    const resp = await adminApi.post(`/api/v1/admin/orders/${orderId}/cancel`, { data: {} });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '缺取消原因应被拒绝').not.toBe(0);
    // 校验失败不落库：订单仍为待支付，可继续后续用例
    const detail = await getAdminOrder(orderId);
    expect(detail.status).toBe(1);
    globalThis.__o46KeepOrder = orderId;
  });

  test('2. 待支付单后台取消成功（状态落 6，原因/时间落库）', async () => {
    const orderId = globalThis.__o46KeepOrder ?? 0;
    if (!orderId) test.skip(true, '订单未创建');
    const stockBeforeCancel = await currentStock();
    // 下单占用了 1 件库存，取消后应回补：先记录"已占用"状态下的库存快照
    const resp = await adminApi.post(`/api/v1/admin/orders/${orderId}/cancel`, {
      data: { reason: 'AUTO-TEST 46 后台取消' },
    });
    const body = (await resp.json()) as ApiResponse<{ id: number; status: number }>;
    expect(body.code, `后台取消失败: ${body.message}`).toBe(0);
    expect(body.data?.status).toBe(6);

    const detail = await getAdminOrder(orderId);
    expect(detail.status).toBe(6);
    expect(detail.cancel_reason).toBe('AUTO-TEST 46 后台取消');
    expect(detail.cancel_time, '取消时间应落库').toBeTruthy();
    globalThis.__o46StockOccupied = stockBeforeCancel;
  });

  test('3. 取消后库存回补', async () => {
    const orderId = globalThis.__o46KeepOrder ?? 0;
    if (!orderId) test.skip(true, '订单未创建');
    // 占用态库存 + 1 件（本单购买量）= 回补后库存
    const stockAfter = await currentStock();
    expect(stockAfter, '取消应回补 1 件库存').toBe((globalThis.__o46StockOccupied ?? 0) + 1);
  });

  test('4. 已取消单重复取消被状态机拒绝', async () => {
    const orderId = globalThis.__o46KeepOrder ?? 0;
    if (!orderId) test.skip(true, '订单未创建');
    const resp = await adminApi.post(`/api/v1/admin/orders/${orderId}/cancel`, {
      data: { reason: 'AUTO-TEST 46 重复取消' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '已取消订单不应可再取消').not.toBe(0);
  });

  test('5. 已支付现金单后台取消走 COD 口径（2→6）', async ({ playwright }) => {
    // C 端 cash → pay_type=3，取消语义按货到付款处理：未实际收款，已支付(2)亦允许后台直接取消
    test.skip(!sku, '环境无库存 SKU');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    let cashOrderId = 0;
    try {
      const created = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: {
              delivery_type: 1,
              address_id: 0,
              remark: 'AUTO-TEST 46 现金单取消',
              // quantity=2：与测试 1 的待支付自提单（quantity=1）错开，避免重复下单指纹碰撞
              items: [{ sku_id: sku!.id, quantity: 2 }],
            },
          })
        ).json()) as ApiResponse<OrderResult>,
        'C 端下单',
      );
      cashOrderId = created.id;
      const paid = (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: cashOrderId, channel: 'cash' } })
      ).json()) as ApiResponse;
      expect(paid.code, `现金支付失败: ${paid.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
    const stockBefore = await currentStock();
    const resp = await adminApi.post(`/api/v1/admin/orders/${cashOrderId}/cancel`, {
      data: { reason: 'AUTO-TEST 46 现金单后台取消' },
    });
    const body = (await resp.json()) as ApiResponse<{ id: number; status: number }>;
    expect(body.code, `现金单后台取消失败: ${body.message}`).toBe(0);
    expect(body.data?.status).toBe(6);
    const detail = await getAdminOrder(cashOrderId);
    expect(detail.status).toBe(6);
    expect(detail.cancel_reason).toBe('AUTO-TEST 46 现金单后台取消');
    expect(await currentStock(), '取消后库存回补 2 件').toBe(stockBefore + 2);
  });

  test('6. 已发货(4)单后台取消被状态机拒绝', async ({ playwright }) => {
    test.skip(!sku, '环境无库存 SKU');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    let deliveryOrderId = 0;
    try {
      // 配送单（delivery_type=2）需地址，下单支付后后台发货至状态 4
      const addr = assertOk(
        (await (
          await buyerApi.post('/api/v1/user/addresses', {
            data: { contact_name: 'AUTO-TEST 46', contact_phone: '13800004646', detail: '测试路 46 号' },
          })
        ).json()) as ApiResponse<{ id?: number }>,
        '创建收货地址',
      );
      // 配送单有 15 元起送门槛，按 SKU 单价折算数量
      const skuResp = (await (await buyerApi.get(`/api/v1/skus/${sku!.id}`)).json()) as ApiResponse<{ price: number }>;
      const price = skuResp.data?.price ?? 0;
      const qty = price > 0 ? Math.max(Math.ceil(15 / price), 1) : 1;
      const created = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: {
              delivery_type: 2,
              address_id: addr.id ?? 0,
              remark: 'AUTO-TEST 46 发货后取消',
              items: [{ sku_id: sku!.id, quantity: qty }],
            },
          })
        ).json()) as ApiResponse<OrderResult>,
        'C 端下单',
      );
      deliveryOrderId = created.id;
      const paid = (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: deliveryOrderId, channel: 'cash' } })
      ).json()) as ApiResponse;
      expect(paid.code).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
    const ship = (await (
      await adminApi.post(`/api/v1/admin/orders/${deliveryOrderId}/ship`, {
        data: { express_company: '顺丰速运', express_no: 'SF46AUTO0001' },
      })
    ).json()) as ApiResponse<{ status: number }>;
    expect(ship.code, `发货失败: ${ship.message}`).toBe(0);
    expect(ship.data?.status).toBe(4);

    const resp = await adminApi.post(`/api/v1/admin/orders/${deliveryOrderId}/cancel`, {
      data: { reason: 'AUTO-TEST 46 发货后取消' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '已发货(4)订单不应允许后台取消').not.toBe(0);
    const detail = await getAdminOrder(deliveryOrderId);
    expect(detail.status, '取消被拒后订单保持已发货').toBe(4);
  });
});

declare global {
  // eslint-disable-next-line no-var
  var __o46KeepOrder: number;
  // eslint-disable-next-line no-var
  var __o46StockOccupied: number;
}
