import { test, expect } from '@playwright/test';
import { clientLogin, newAuthedRequest, pickSku, type ApiResponse, type PageData } from '../helpers/api';
import { uniquePhone } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 06：支付单查询 + 履约工作台
 * 覆盖：支付单详情与归属校验、支付渠道列表、微信回调签名拒绝、
 *       履约状态、自提核销、开始/完成配送（依赖履约人员账号，非履约则跳过）
 */

interface OrderInfo {
  id: number;
  order_no: string;
  status: number;
  delivery_type: number;
  pickup_code?: string;
}

let api: Awaited<ReturnType<typeof newAuthedRequest>>;
let userId: number;
let communityId = 0;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  userId = info.user.id;
  communityId = info.user.community_id;
  api = await newAuthedRequest(playwright, info.access_token);
});

test.afterAll(async () => {
  await api?.dispose();
});

/**
 * 创建自提订单并 cash 支付（进入待提货状态）
 * - 函数名: createPaidPickupOrder
 * - 参数: skuId — SKU ID；quantity — 数量（同用户连续下单时错开重复指纹，默认 1）
 * - 返回: Promise<OrderInfo>
 * - 异常: 下单/支付失败时抛出 Error
 */
async function createPaidPickupOrder(skuId: number, quantity = 1): Promise<OrderInfo> {
  const resp = await api.post('/api/v1/orders', {
    data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuId, quantity }] },
  });
  const body = (await resp.json()) as ApiResponse<OrderInfo>;
  if (body.code !== 0) throw new Error(`下单失败 ${body.message}`);
  const order = body.data as OrderInfo;
  const pay = (await (
    await api.post('/api/v1/payments', { data: { order_id: order.id, channel: 'cash' } })
  ).json()) as ApiResponse;
  if (pay.code !== 0) throw new Error(`支付失败 ${pay.message}`);
  return order;
}

/**
 * 查询订单详情
 * - 函数名: getOrder
 * - 参数: id — 订单 ID
 * - 返回: Promise<OrderInfo>
 * - 异常: 业务失败时抛出 Error
 */
async function getOrder(id: number): Promise<OrderInfo> {
  const body = (await (await api.get(`/api/v1/orders/${id}`)).json()) as ApiResponse<OrderInfo>;
  if (body.code !== 0) throw new Error(`查订单失败 ${body.message}`);
  return body.data as OrderInfo;
}

test.describe('支付单查询与回调安全', () => {
  test('支付渠道列表可获取（仅下发能力就绪的渠道）', async ({ request }) => {
    const resp = await request.get('/api/v1/payments/channels');
    const body = (await resp.json()) as ApiResponse<{ channels?: Record<string, unknown>[] }>;
    expect(body.code).toBe(0);
    // 原标题声称「alipay 渠道应不可用」但无对应断言，属标题与实现不符；
    // 就绪口径与渠道下发的联动断言在 47-settings-write 模块，此处只校验响应结构
    const list = body.data?.channels ?? [];
    expect(Array.isArray(list), 'channels 应为数组').toBe(true);
    for (const ch of list) {
      expect(typeof ch.channel, '渠道项应含 channel 标识').toBe('string');
    }
  });

  test('支付单详情仅本人订单可查（归属校验）', async () => {
    let sku;
    try {
      sku = await pickSku(api, undefined, communityId);
    } catch {
      test.skip(true, '环境无可用 SKU');
      return;
    }
    const order = await createPaidPickupOrder(sku.id);
    const payResp = await api.post('/api/v1/payments', { data: { order_id: order.id, channel: 'cash' } });
    const payBody = (await payResp.json()) as ApiResponse<{ id?: number; payment_no?: string }>;
    if (payBody.code !== 0 || !payBody.data?.id) {
      test.skip(true, '重复支付被拒或无支付单 ID，跳过详情断言');
      return;
    }
    const detail = (await (await api.get(`/api/v1/payments/${payBody.data.id}`)).json()) as ApiResponse;
    expect(detail.code).toBe(0);

    // 他人不可查
    const other = await clientLogin(playwright.request, uniquePhone());
    const otherApi = await newAuthedRequest(playwright, other.accessToken);
    try {
      const stranger = (await (await otherApi.get(`/api/v1/payments/${payBody.data.id}`)).json()) as ApiResponse;
      expect(stranger.code).not.toBe(0);
    } finally {
      await otherApi.dispose();
    }
  });

  test('支付状态免登录查询（凭不可枚举支付单号）', async ({ request }) => {
    // 场景：支付宝回跳结果页在系统浏览器打开（无登录态），凭支付单号轮询公开接口
    let sku;
    try {
      sku = await pickSku(api, undefined, communityId);
    } catch {
      test.skip(true, '环境无可用 SKU');
      return;
    }
    // quantity=2 错开同用户重复下单指纹（上一用例已用 quantity=1 下过单）
    const orderResp = await api.post('/api/v1/orders', {
      data: { delivery_type: 1, address_id: 0, items: [{ sku_id: sku.id, quantity: 2 }] },
    });
    const orderBody = (await orderResp.json()) as ApiResponse<OrderInfo>;
    expect(orderBody.code, '下单应成功').toBe(0);
    const orderId = orderBody.data!.id;

    const payResp = await api.post('/api/v1/payments', { data: { order_id: orderId, channel: 'cash' } });
    const payBody = (await payResp.json()) as ApiResponse<{ payment_no?: string }>;
    expect(payBody.code, '创建支付单应成功').toBe(0);
    const payNo = payBody.data?.payment_no ?? '';
    expect(payNo, '应返回支付单号').not.toBe('');

    // 免登录可查：现金支付创建即成功（status=2），只回最小字段集
    const pub = await request.get(`/api/v1/payments/status?no=${encodeURIComponent(payNo)}`);
    const pubBody = (await pub.json()) as ApiResponse<{
      payment_no: string;
      status: number;
      amount: number;
      order_id: number;
    }>;
    expect(pubBody.code).toBe(0);
    expect(pubBody.data?.status).toBe(2);
    expect(pubBody.data?.payment_no).toBe(payNo);
    expect(pubBody.data?.order_id).toBe(orderId);

    // 伪造单号被拒
    const fake = await request.get('/api/v1/payments/status?no=ZFnot-exist');
    const fakeBody = (await fake.json()) as ApiResponse;
    expect(fakeBody.code, '不存在的支付单号应被拒绝').not.toBe(0);

    // 缺参被拒
    const missing = await request.get('/api/v1/payments/status');
    const missingBody = (await missing.json()) as ApiResponse;
    expect(missingBody.code, '缺少支付单号应被拒绝').not.toBe(0);
  });

  test('微信回调：伪造报文被拒绝（签名校验）', async ({ request }) => {
    const resp = await request.post('/api/v1/payments/callback/wechat', {
      data: { nonce: 'fake', ciphertext: 'fake-cipher' },
      headers: { 'Content-Type': 'application/json' },
    });
    // 微信回调按协议返回 XML 应答（FAIL），不返回 JSON 业务码
    const text = await resp.text();
    expect(resp.status()).toBeLessThan(500);
    expect(text).toContain('<xml>');
    expect(text).not.toContain('SUCCESS');
  });
});

test.describe.serial('履约工作台', () => {
  let isFulfiller = false;

  test('履约身份探测', async () => {
    const resp = await api.get('/api/v1/user/fulfillment-status');
    const body = (await resp.json()) as ApiResponse<{ is_fulfiller?: boolean } | null>;
    isFulfiller = body.code === 0 && Boolean(body.data);
    if (!isFulfiller) {
      console.log('[履约] 当前测试账号不是履约人员，核销/配送用例将跳过');
    }
  });

  test('非履约人员访问工作台被拒绝', async () => {
    if (isFulfiller) test.skip(true, '当前账号是履约人员，反向用例不适用');
    const resp = await api.get('/api/v1/fulfillment/orders');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });

  test('自提单核销：取货码错误被拒绝', async () => {
    test.setTimeout(60_000);
    if (!isFulfiller) test.skip(true, '需要履约人员身份');
    let sku;
    try {
      sku = await pickSku(api, undefined, communityId);
    } catch {
      test.skip(true, '环境无有库存 SKU');
      return;
    }
    const order = await createPaidPickupOrder(sku.id, 2);
    const detail = await getOrder(order.id);
    const wrongCode = '00000000';
    if (detail.pickup_code === wrongCode) test.skip(true, '取货码恰好为 00000000，跳过');

    const resp = await api.post(`/api/v1/fulfillment/orders/${order.id}/verify-pickup`, {
      data: { pickup_code: wrongCode },
    });
    const body = (await resp.json()) as ApiResponse;
    // 11005=非本社区履约人员（预置履约身份与测试用户社区不一致时无法核销）
    if (body.code === 11005) {
      console.log('[履约] 测试用户履约身份与订单社区不一致，核销用例跳过');
      test.skip(true, '履约身份社区不匹配');
      return;
    }
    expect(body.code, '错误取货码必须被拒绝').not.toBe(0);
  });

  test('自提单核销：正确取货码核销成功且不可重复核销', async () => {
    if (!isFulfiller) test.skip(true, '需要履约人员身份');
    let sku;
    try {
      sku = await pickSku(api, undefined, communityId);
    } catch {
      test.skip(true, '环境无有库存 SKU');
      return;
    }
    const order = await createPaidPickupOrder(sku.id, 3);
    const detail = await getOrder(order.id);
    const code = detail.pickup_code;
    if (!code) {
      test.skip(true, '订单详情未返回取货码，需人工确认字段');
      return;
    }
    const ok = (await (
      await api.post(`/api/v1/fulfillment/orders/${order.id}/verify-pickup`, { data: { pickup_code: code } })
    ).json()) as ApiResponse;
    if (ok.code === 11005) {
      console.log('[履约] 测试用户履约身份与订单社区不一致，核销用例跳过');
      test.skip(true, '履约身份社区不匹配');
      return;
    }
    expect(ok.code, `核销失败 ${ok.message}`).toBe(0);

    const again = (await (
      await api.post(`/api/v1/fulfillment/orders/${order.id}/verify-pickup`, { data: { pickup_code: code } })
    ).json()) as ApiResponse;
    expect(again.code, '重复核销必须被拒绝').not.toBe(0);
  });
});

test.describe('履约身份标识', () => {
  test('fulfillment-status 需登录', async ({ request }) => {
    const resp = await request.get('/api/v1/user/fulfillment-status');
    const body = (await resp.json()) as ApiResponse;
    expect(resp.status()).toBeLessThan(500);
    if (resp.status() === 200) expect(body.code).not.toBe(0);
    else expect(resp.status()).toBe(401);
  });

  test('上传图片接口未登录被拒绝', async ({ request }) => {
    const resp = await request.post('/api/v1/upload/image');
    const body = (await resp.json()) as ApiResponse;
    expect(resp.status()).toBeLessThan(500);
    if (resp.status() === 200) expect(body.code).not.toBe(0);
    else expect(resp.status()).toBe(401);
  });
});
