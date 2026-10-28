import { test, expect } from '@playwright/test';
import {
  clientLogin,
  createFulfillerStaff,
  ensureStock,
  newAdminContext,
  newAuthedRequest,
  pickSku,
  removeFulfillerStaff,
  type ApiResponse,
  type ClientLoginInfo,
  type SkuRef,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 06：支付单查询 + 履约工作台
 * 覆盖：支付单详情与归属校验、支付渠道列表、微信回调签名拒绝、
 *       履约身份（是/否两向）、自提核销（错误码拒绝、正确码核销、重复核销拒绝）
 * 说明：履约人员 = 本社区任职且绑定会员的员工（判定见 services/user/service/user.go CheckFulfiller），
 *       身份一律本轮造数（createFulfillerStaff），不再探测环境预置账号后按条件 skip——
 *       旧口径下探测读的是「data 是否非空」而非 data.is_fulfiller，恒判为真，
 *       正向核销必撞 11005「非本社区履约人员」，两条用例双双跳过属假绿。
 */

interface OrderInfo {
  id: number;
  order_no: string;
  status: number;
  delivery_type: number;
  pickup_code?: string;
}

/** 买家（本轮不绑履约身份，用作「非履约人员」反向口径） */
let api: Awaited<ReturnType<typeof newAuthedRequest>>;
/** 履约人员（C 端会员 + 绑定它的员工账号） */
let fulfillerApi: Awaited<ReturnType<typeof newAuthedRequest>>;
let adminApi: Awaited<ReturnType<typeof newAuthedRequest>>;
let communityId = 0;
let adminCommunityId = 0;
/** 绑定履约会员的员工账号 ID（afterAll 解绑，不给库里留履约身份） */
let staffId = 0;
let sku: SkuRef;

/**
 * 新建 C 端会员并返回社区语境已绑定的登录信息
 * - 函数名: newClientUser
 * - 参数: request — Playwright 请求上下文
 * - 返回: Promise<ClientLoginInfo>（user.community_id > 0，access_token 的 JWT 已含该社区）
 * - 异常: 登录失败或环境无可用社区时由 clientLogin 抛 Error
 * 说明: clientLogin 首登才自动绑社区，登录时签发的 JWT 不含 community_id，
 * 依赖社区语境的接口（下单）会报参数错误，故同号登两次。
 */
async function newClientUser(request: Playwright['request']): Promise<ClientLoginInfo> {
  const phone = uniquePhone();
  await clientLogin(request, phone);
  return clientLogin(request, phone);
}

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const buyer = await newClientUser(playwright.request);
  communityId = buyer.user.community_id;
  api = await newAuthedRequest(playwright, buyer.access_token);

  const fulfiller = await newClientUser(playwright.request);
  // 买家与履约人员必须同社区：核销在归属层按「订单社区 == 操作者社区」二次收口
  expect(fulfiller.user.community_id, '本轮两个会员应落在同一社区').toBe(communityId);
  fulfillerApi = await newAuthedRequest(playwright, fulfiller.access_token);

  // 造数失败不 try/catch 置空后 skip：那会让链路坏了也报绿
  const admin = await newAdminContext(playwright, undefined, undefined, communityId);
  adminApi = admin.api;
  adminCommunityId = admin.communityId;
  sku = await pickSku(adminApi, undefined, communityId);
  await ensureStock(adminApi, sku.id, 100);
  staffId = await createFulfillerStaff(adminApi, adminCommunityId, fulfiller.user.id);
});

test.afterAll(async () => {
  if (adminApi && staffId) {
    const warns = await removeFulfillerStaff(adminApi, staffId);
    for (const w of warns) console.log(`[履约] ${w}`);
  }
  await api?.dispose();
  await fulfillerApi?.dispose();
  await adminApi?.dispose();
});

/**
 * 创建自提订单（不支付）
 * - 函数名: createPickupOrder
 * - 参数: skuId — SKU ID；quantity — 数量（同用户连续下单时错开 5s 防重复指纹）
 * - 返回: Promise<OrderInfo>
 * - 异常: 下单失败时抛出 Error
 */
async function createPickupOrder(skuId: number, quantity = 1): Promise<OrderInfo> {
  const body = (await (
    await api.post('/api/v1/orders', {
      data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuId, quantity }] },
    })
  ).json()) as ApiResponse<OrderInfo>;
  if (body.code !== 0) throw new Error(`下单失败 ${body.message}`);
  return body.data as OrderInfo;
}

/**
 * 创建自提订单并 cash 支付（进入待提货状态）
 * - 函数名: createPaidPickupOrder
 * - 参数: skuId — SKU ID；quantity — 数量（同用户连续下单时错开重复指纹，默认 1）
 * - 返回: Promise<OrderInfo>
 * - 异常: 下单/支付失败时抛出 Error
 * 说明: 网关有 5s 防重复下单窗口（同用户+社区+配送方式+商品列表）。Playwright 重试会
 * 在窗口内重放同一请求体，被 11005 之外的 ParamError「请勿重复提交订单」挡下——这不是
 * 产品缺陷，故此处对去重报错做一次「等窗口过去再提交」的兼容，而非改指纹或弱化断言。
 */
async function createPaidPickupOrder(skuId: number, quantity = 1): Promise<OrderInfo> {
  const submit = () => api.post('/api/v1/orders', {
    data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuId, quantity }] },
  });
  let resp = await submit();
  let body = (await resp.json()) as ApiResponse<OrderInfo>;
  if (body.code !== 0 && body.message?.includes('请勿重复提交订单')) {
    await new Promise((r) => setTimeout(r, 5500)); // 等过 5s 防重窗口再重放
    resp = await submit();
    body = (await resp.json()) as ApiResponse<OrderInfo>;
  }
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

  test('支付单详情仅本人订单可查（归属校验）', async ({ playwright }) => {
    // 待支付单建一次支付即可读详情；旧写法先 cash 支付再重复建单，第二次必被
    // 「订单当前状态不可支付」拒绝，用例落进 skip 分支——那不是环境限制，是用例造数错了。
    // 路径参数是订单 ID（handler 按 GetOrderReq{Id,UserId} 校验归属，见 C 端 getPayment(orderId)）
    const order = await createPickupOrder(sku.id);
    const payBody = (await (
      await api.post('/api/v1/payments', { data: { order_id: order.id, channel: 'cash' } })
    ).json()) as ApiResponse<{ payment_no?: string; amount?: number }>;
    expect(payBody.code, `创建支付单应成功: ${payBody.message}`).toBe(0);
    expect(payBody.data?.payment_no ?? '', '应返回支付单号').not.toBe('');

    const detail = (await (await api.get(`/api/v1/payments/${order.id}`)).json()) as ApiResponse<{
      payment_no?: string;
    }>;
    expect(detail.code, '本人可查自己订单的支付单').toBe(0);
    expect(detail.data?.payment_no, '详情应回显同一支付单号').toBe(payBody.data?.payment_no);

    // 他人不可查
    const other = await newClientUser(playwright.request);
    const otherApi = await newAuthedRequest(playwright, other.access_token);
    try {
      const stranger = (await (await otherApi.get(`/api/v1/payments/${order.id}`)).json()) as ApiResponse;
      expect(stranger.code, '他人订单的支付单不得可读').not.toBe(0);
    } finally {
      await otherApi.dispose();
    }
  });

  test('支付状态免登录查询（凭不可枚举支付单号）', async ({ request }) => {
    // 场景：支付宝回跳结果页在系统浏览器打开（无登录态），凭支付单号轮询公开接口
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
    const fake = await request.get(`/api/v1/payments/status?no=ZFnot-exist`);
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
    // V3 规范失败应答（wego v0.2.0 NotificationFail）：5xx + {"code":"FAIL"}，
    // 微信按间隔重试；绝不能回 2xx——那会被渠道视为受理成功而停止重试，通知即丢失
    expect(resp.status()).toBe(500);
    const body = (await resp.json()) as { code: string; message?: string };
    expect(body.code).toBe('FAIL');
  });
});

test.describe.serial('履约工作台', () => {
  test('履约身份两向：绑定会员的员工为真，未绑定的买家为假', async () => {
    const fBody = (await (await fulfillerApi.get('/api/v1/user/fulfillment-status')).json()) as ApiResponse<{
      is_fulfiller: boolean;
      community_id: number;
    }>;
    expect(fBody.code).toBe(0);
    expect(fBody.data?.is_fulfiller, '已绑定会员的本社区员工应是履约人员').toBe(true);

    const bBody = (await (await api.get('/api/v1/user/fulfillment-status')).json()) as ApiResponse<{
      is_fulfiller: boolean;
    }>;
    expect(bBody.code).toBe(0);
    expect(bBody.data?.is_fulfiller, '未绑定会员的普通买家不是履约人员').toBe(false);
  });

  test('非履约人员访问工作台被拒绝', async () => {
    const resp = await api.get('/api/v1/fulfillment/orders');
    const body = (await resp.json()) as ApiResponse;
    // 11005=无权限（网关 checkFulfiller 对本社区无履约身份者直接拒绝）
    expect(body.code, '非履约人员必须被拒').toBe(11005);
  });

  test('自提单核销：取货码错误被拒绝', async () => {
    test.setTimeout(60_000);
    const order = await createPaidPickupOrder(sku.id, 3);
    const detail = await getOrder(order.id);
    // 取货码是 8 位随机串：从候选里取一个「必然不等于真实码」的值，
    // 替掉旧写法里「恰好撞上 00000000 就 skip」的条件分支
    const wrongCode = ['00000000', '11111111', '22222222'].find((c) => c !== detail.pickup_code)!;

    const body = (await (
      await fulfillerApi.post(`/api/v1/fulfillment/orders/${order.id}/verify-pickup`, {
        data: { pickup_code: wrongCode },
      })
    ).json()) as ApiResponse;
    // 10004=操作不被允许（服务层 errPickupCodeMismatch：FailedPrecondition）
    expect(body.code, '错误取货码必须被拒绝').toBe(10004);
    expect((await getOrder(order.id)).status, '核销失败后订单仍停在待提货').not.toBe(5);
  });

  test('自提单核销：正确取货码核销成功且不可重复核销', async () => {
    test.setTimeout(60_000);
    const order = await createPaidPickupOrder(sku.id, 4);
    const detail = await getOrder(order.id);
    const code = detail.pickup_code ?? '';
    // 字段缺失属产品契约破坏，必须红而不是 skip
    expect(code, '自提单详情应下发取货码').not.toBe('');

    const ok = (await (
      await fulfillerApi.post(`/api/v1/fulfillment/orders/${order.id}/verify-pickup`, { data: { pickup_code: code } })
    ).json()) as ApiResponse;
    expect(ok.code, `核销失败 ${ok.message}`).toBe(0);
    expect((await getOrder(order.id)).status, '核销后订单置为已完成').toBe(5);

    const again = (await (
      await fulfillerApi.post(`/api/v1/fulfillment/orders/${order.id}/verify-pickup`, { data: { pickup_code: code } })
    ).json()) as ApiResponse;
    expect(again.code, '重复核销必须被拒绝').toBe(10004);
  });
});

test.describe('履约身份标识', () => {
  test('fulfillment-status 需登录', async ({ request }) => {
    const resp = await request.get('/api/v1/user/fulfillment-status');
    const body = (await resp.json()) as ApiResponse;
    expect([200, 401], '未授权只允许 HTTP 200+业务码 或 401 两种表达').toContain(resp.status());
    if (resp.status() === 200) expect(body.code).not.toBe(0);
    else expect(resp.status()).toBe(401);
  });

  test('上传图片接口未登录被拒绝', async ({ request }) => {
    const resp = await request.post('/api/v1/upload/image');
    const body = (await resp.json()) as ApiResponse;
    expect([200, 401], '未授权只允许 HTTP 200+业务码 或 401 两种表达').toContain(resp.status());
    if (resp.status() === 200) expect(body.code).not.toBe(0);
    else expect(resp.status()).toBe(401);
  });
});
