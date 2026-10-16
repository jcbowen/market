import { test, expect, type Playwright } from '@playwright/test';
import { clientLogin, newAdminContext, newAuthedRequest, pickSku, ensureStock, asList, type ApiResponse, type PageData } from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 31：订单字段进阶
 * 覆盖：待支付单 expire_at ≈ created_at + order_timeout（默认 1800s，±120s 容差）；
 *       已支付单 expire_at 为空（仅待支付返回，order.go orderExpireAt）；
 *       列表查询聚合虚拟状态 90=待发货(2∪3)、9=售后(7∪8)（查询入参，不落库）；
 *       后台列表 source 筛选（1=C 端 / 2=收银台代客）与 pickup_code 精确查单
 */

interface Order {
  id: number;
  order_no: string;
  status: number;
  source?: number;
  pickup_code?: string;
  [k: string]: unknown;
}

interface OrderListItem {
  id: number;
  order_no: string;
  status: number;
  expire_at?: string;
  [k: string]: unknown;
}

interface AdminOrder {
  id: number;
  order_no: string;
  status: number;
  source: number;
  pickup_code?: string;
  [k: string]: unknown;
}

const TIMEOUT_SEC = 1800; // 网关 order.go 默认超时秒数
const TOLERANCE_SEC = 120; // 时钟/时序容差

let api: Awaited<ReturnType<typeof newAuthedRequest>>;
let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  api = await newAuthedRequest(playwright, info.access_token);
  const admin = await newAdminContext(playwright, undefined, undefined, info.user.community_id);
  adminApi = admin.api;

  // 开工班次（容错：已有进行中班次时继续用当前班次），POS 会员单需要
  const startResp = await adminApi.post('/api/v1/admin/pos/shifts/start', { data: { note: 'AUTO-TEST 订单字段开工' } });
  const startBody = (await startResp.json()) as ApiResponse;
  if (startBody.code !== 0) {
    console.log(`[31] 开工返回 code=${startBody.code} message=${startBody.message}（可能已有进行中班次）`);
  }
  const cur = (await (await adminApi.get('/api/v1/admin/pos/shifts/current')).json()) as ApiResponse<{ id?: number }>;
  if (!cur.data?.id) throw new Error('POS 无进行中班次且开工失败，订单字段用例无法执行');
});

test.afterAll(async () => {
  await api?.dispose();
  await adminApi?.dispose();
});

declare global {
  // eslint-disable-next-line no-var
  var __memberId: number;
}

/** 创建自提订单（默认待支付） */
async function createPickupOrder(skuId: number, quantity = 1): Promise<Order> {
  const resp = await api.post('/api/v1/orders', {
    data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuId, quantity }] },
  });
  const body = (await resp.json()) as ApiResponse<Order>;
  if (body.code !== 0) throw new Error(`下单失败 code=${body.code} message=${body.message}`);
  return body.data as Order;
}

/** 解析 datetime 字符串为时间戳（毫秒），解析失败返回 NaN */
function ts(datetime: string | undefined): number {
  if (!datetime) return NaN;
  return new Date(datetime.replace(' ', 'T')).getTime();
}

test.describe.serial('订单字段进阶', () => {
  let skuId = 0;
  let productId = 0;
  let memberId = 0;

  test('准备：就绪 SKU 与会员', async ({ playwright }) => {
    const sku = await pickSku(api);
    skuId = sku.id;
    productId = sku.product_id;
    await ensureStock(adminApi, skuId, 100);
    const member = await clientLogin(playwright.request, uniquePhone());
    memberId = member.user.id;
    globalThis.__memberId = memberId;
  });

  test('待支付单详情 expire_at ≈ created_at + 1800s', async () => {
    const order = await createPickupOrder(skuId);
    const detail = (await (await api.get(`/api/v1/orders/${order.id}`)).json()) as ApiResponse<Order>;
    expect(detail.code).toBe(0);
    const created = ts(detail.data?.created_at as string);
    const expire = ts(detail.data?.expire_at as string);
    expect(detail.data?.expire_at, '待支付单应返回 expire_at').toBeTruthy();
    const diffSec = (expire - created) / 1000;
    expect(Math.abs(diffSec - TIMEOUT_SEC), `expire_at 与 created_at 差值 ${diffSec}s 应≈${TIMEOUT_SEC}s`).toBeLessThanOrEqual(TOLERANCE_SEC);
    // 保留该单供聚合状态用例使用，最后统一取消
    globalThis.__pendingId = order.id;
  });

  test('列表项同样携带 expire_at（待支付）', async () => {
    test.skip(!globalThis.__pendingId, '待支付单未创建');
    const resp = await api.get('/api/v1/orders', { params: { page: 1, page_size: 20, status: 1 } });
    const body = (await resp.json()) as ApiResponse<PageData<OrderListItem>>;
    expect(body.code).toBe(0);
    const found = asList<OrderListItem>(body.data).find((o) => o.id === globalThis.__pendingId);
    expect(found, '待支付列表应包含该单').toBeTruthy();
    expect(found!.expire_at).toBeTruthy();
  });

  test('已支付单 expire_at 为空（仅待支付返回）', async () => {
    const order = await createPickupOrder(skuId);
    const pay = (await (await api.post('/api/v1/payments', { data: { order_id: order.id, channel: 'cash' } })).json()) as ApiResponse;
    expect(pay.code).toBe(0);
    const detail = (await (await api.get(`/api/v1/orders/${order.id}`)).json()) as ApiResponse<Order>;
    expect(detail.data?.status).toBe(2);
    expect(detail.data?.expire_at ?? '', '已支付单 expire_at 应为空串').toBe('');
  });

  test('聚合状态 90：列表只返回待发货单（2/3）', async () => {
    const resp = await api.get('/api/v1/orders', { params: { page: 1, page_size: 50, status: 90 } });
    const body = (await resp.json()) as ApiResponse<PageData<OrderListItem>>;
    expect(body.code).toBe(0);
    const list = asList<OrderListItem>(body.data);
    // 本会话已支付的货到付款自提单（status=2）应出现在 90 结果中
    expect(list.some((o) => o.id !== globalThis.__pendingId)).toBe(true);
    expect(list.every((o) => [2, 3].includes(o.status)), '聚合 90 应只含状态 2/3').toBe(true);
  });

  test('聚合状态 9：造退款单后列表只返回售后单（7/8）', async () => {
    // 造一笔退款单：自提支付 → 申请退款 → status=7
    const order = await createPickupOrder(skuId);
    await api.post('/api/v1/payments', { data: { order_id: order.id, channel: 'cash' } });
    await api.post(`/api/v1/orders/${order.id}/refund`, { data: { reason: 'AUTO-TEST 聚合状态 9 造数' } });

    const resp = await api.get('/api/v1/orders', { params: { page: 1, page_size: 50, status: 9 } });
    const body = (await resp.json()) as ApiResponse<PageData<OrderListItem>>;
    expect(body.code).toBe(0);
    const list = asList<OrderListItem>(body.data);
    expect(list.some((o) => o.id === order.id), '退款中的单应出现在聚合 9 结果').toBe(true);
    expect(list.every((o) => [7, 8].includes(o.status)), '聚合 9 应只含状态 7/8').toBe(true);
    // 清理待支付单，避免残留
    await api.put(`/api/v1/orders/${globalThis.__pendingId}/cancel`);
  });

  test('POS 会员单 → 后台 source=2 筛选命中且 source=1 不含', async () => {
    test.skip(!memberId, '会员未就绪');
    const pos = (await (await adminApi.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: memberId,
        pay_type: 4,
        delivery_type: 1,
        remark: 'AUTO-TEST source 筛选造数',
        items: [{ sku_id: skuId, quantity: 1 }],
      },
    })).json()) as ApiResponse<{ id: number; pickup_code: string }>;
    expect(pos.code, `POS 会员单失败: ${pos.message}`).toBe(0);
    globalThis.__posOrderId = pos.data!.id;
    globalThis.__posPickupCode = pos.data!.pickup_code;

    const s2 = (await (await adminApi.get('/api/v1/admin/orders', { params: { page: 1, page_size: 50, source: 2 } })).json()) as ApiResponse<PageData<AdminOrder>>;
    expect(s2.code).toBe(0);
    const s2List = asList<AdminOrder>(s2.data);
    expect(s2List.some((o) => o.id === globalThis.__posOrderId), 'source=2 应包含收银台代客单').toBe(true);
    expect(s2List.every((o) => o.source === 2), 'source=2 结果应全部为代客单').toBe(true);

    const s1 = (await (await adminApi.get('/api/v1/admin/orders', { params: { page: 1, page_size: 50, source: 1 } })).json()) as ApiResponse<PageData<AdminOrder>>;
    expect(s1.code).toBe(0);
    const s1List = asList<AdminOrder>(s1.data);
    expect(s1List.some((o) => o.id === globalThis.__posOrderId), 'source=1 不应包含收银台代客单').toBe(false);
  });

  test('后台 pickup_code 精确查单命中唯一订单（详情回读取货码）', async () => {
    test.skip(!globalThis.__posPickupCode, '取货码未就绪');
    // 后台列表项不含 pickup_code 字段（详情口径），此处仅验证筛选命中与唯一性
    const resp = await adminApi.get('/api/v1/admin/orders', { params: { page: 1, page_size: 10, pickup_code: globalThis.__posPickupCode } });
    const body = (await resp.json()) as ApiResponse<PageData<AdminOrder>>;
    expect(body.code).toBe(0);
    const list = asList<AdminOrder>(body.data);
    expect(list.length).toBe(1);
    expect(list[0].id).toBe(globalThis.__posOrderId);
    // 详情回读取货码与查询入参一致
    const detail = (await (await adminApi.get(`/api/v1/admin/orders/${globalThis.__posOrderId}`)).json()) as ApiResponse<AdminOrder>;
    expect(detail.data?.pickup_code).toBe(globalThis.__posPickupCode);
  });
});

declare global {
  // eslint-disable-next-line no-var
  var __pendingId: number;
  // eslint-disable-next-line no-var
  var __posOrderId: number;
  // eslint-disable-next-line no-var
  var __posPickupCode: string;
}
