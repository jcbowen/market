import { test, expect, type Playwright, type APIRequestContext } from '@playwright/test';
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
import { uniquePhone, autoName } from '../helpers/data';

/**
 * 模块 62：商城流程审查修复回归验证
 * 覆盖本轮审查修复中可通过对外 API 稳定触发验证的 6 项：
 *   P1#4  下单成功后服务端异步清理购物车
 *   P1#5  重复下单防护（5s 指纹窗口内二次提交被拒）
 *   P1#6  单品数量上限校验（>999 / <=0 拒绝）
 *   P0#2  整单退款回补优惠券（券状态由已使用复位为未使用）
 *   P3#15 取货码防暴力破解（连续输错 5 次后锁定 30 分钟）
 *   P3#16 下单接口 per-user 限流（桶容量 5，超出返回 429）
 * 说明：P0#1（定时任务取消回补券）、P3#17（自提单超时自动完成）、P3#18（退款超时自动审批）
 *       为定时器内部逻辑，无稳定对外触发点，不适合黑盒 API 测试，留待 Go 单元测试覆盖；
 *       P0#3（秒杀价止血）、P1#7（支付回调两阶段）、P2#9/#11（批量查询优化）为内部实现细节，
 *       已由构建期检查与既有链路测试间接覆盖，此处不重复。
 */

/** 创建新 C 端用户并返回已绑定社区语境的有效 token（同 15-refund.spec.ts 口径） */
async function newClientUser(playwright: Playwright): Promise<ClientLoginInfo> {
  const phone = uniquePhone();
  await clientLogin(playwright.request, phone);
  return clientLogin(playwright.request, phone);
}

interface OrderResult {
  id: number;
  order_no: string;
  status?: number;
  pickup_code?: string;
  [k: string]: unknown;
}

interface CartItem {
  id: number;
  sku_id: number;
  quantity: number;
}

interface UserCoupon {
  id: number;
  coupon_id: number;
  status: number;
  [k: string]: unknown;
}

/** 每个 describe 独立造数、独立用户，避免相互干扰（尤其是 P3#16 的 per-user 限流桶） */

test.describe.serial('P1#6 单品数量上限校验', () => {
  let api: APIRequestContext;
  let sku: SkuRef;

  test.beforeAll(async ({ playwright }) => {
    const buyer = await newClientUser(playwright);
    api = await newAuthedRequest(playwright, buyer.access_token);
    const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
    try {
      sku = await pickSku(admin.api);
      await ensureStock(admin.api, sku.id, 2000);
    } finally {
      await admin.api.dispose();
    }
  });

  test.afterAll(async () => {
    await api?.dispose();
  });

  test('数量超过 999 被拒绝', async () => {
    const resp = await api.post('/api/v1/orders', {
      data: { delivery_type: 1, items: [{ sku_id: sku.id, quantity: 1000 }] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `超上限下单应被拒绝: ${body.message}`).not.toBe(0);
    expect(body.message).toContain('1~999');
  });

  test('数量为 0 被拒绝', async () => {
    const resp = await api.post('/api/v1/orders', {
      data: { delivery_type: 1, items: [{ sku_id: sku.id, quantity: 0 }] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `数量为 0 应被拒绝: ${body.message}`).not.toBe(0);
    expect(body.message).toContain('1~999');
  });

  test('数量为负数被拒绝', async () => {
    const resp = await api.post('/api/v1/orders', {
      data: { delivery_type: 1, items: [{ sku_id: sku.id, quantity: -1 }] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `负数数量应被拒绝: ${body.message}`).not.toBe(0);
    expect(body.message).toContain('1~999');
  });
});

test.describe.serial('P1#5 重复下单防护（5s 指纹窗口）', () => {
  let api: APIRequestContext;
  let sku: SkuRef;
  const createdOrderIds: number[] = [];

  test.beforeAll(async ({ playwright }) => {
    const buyer = await newClientUser(playwright);
    api = await newAuthedRequest(playwright, buyer.access_token);
    const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
    try {
      sku = await pickSku(admin.api);
      await ensureStock(admin.api, sku.id, 50);
    } finally {
      await admin.api.dispose();
    }
  });

  test.afterAll(async () => {
    for (const id of createdOrderIds) {
      try {
        await api.put(`/api/v1/orders/${id}/cancel`);
      } catch {
        /* 清理失败不影响测试结论 */
      }
    }
    await api?.dispose();
  });

  test('首次下单成功', async () => {
    const body = assertOk(
      (await (
        await api.post('/api/v1/orders', {
          data: { delivery_type: 1, items: [{ sku_id: sku.id, quantity: 1 }] },
        })
      ).json()) as ApiResponse<OrderResult>,
      '首次下单',
    );
    createdOrderIds.push(body.id);
    expect(body.id).toBeGreaterThan(0);
  });

  test('5s 内相同指纹重复下单被拒绝', async () => {
    const resp = await api.post('/api/v1/orders', {
      data: { delivery_type: 1, items: [{ sku_id: sku.id, quantity: 1 }] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `重复下单应被拒绝: ${body.message}`).not.toBe(0);
    expect(body.message).toContain('重复提交');
  });

  test('修改数量后指纹变化，不被误拦截', async () => {
    const body = assertOk(
      (await (
        await api.post('/api/v1/orders', {
          data: { delivery_type: 1, items: [{ sku_id: sku.id, quantity: 2 }] },
        })
      ).json()) as ApiResponse<OrderResult>,
      '不同数量下单',
    );
    createdOrderIds.push(body.id);
    expect(body.id).toBeGreaterThan(0);
  });
});

test.describe.serial('P1#4 下单成功后服务端清理购物车', () => {
  let api: APIRequestContext;
  let sku: SkuRef;
  let createdOrderId = 0;

  test.beforeAll(async ({ playwright }) => {
    const buyer = await newClientUser(playwright);
    api = await newAuthedRequest(playwright, buyer.access_token);
    const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
    try {
      sku = await pickSku(admin.api);
      await ensureStock(admin.api, sku.id, 20);
    } finally {
      await admin.api.dispose();
    }
  });

  test.afterAll(async () => {
    if (createdOrderId) {
      try {
        await api.put(`/api/v1/orders/${createdOrderId}/cancel`);
      } catch {
        /* 清理失败不影响测试结论 */
      }
    }
    await api?.dispose();
  });

  test('加购后下单，购物车中该 SKU 被异步清除', async () => {
    const addBody = (await (
      await api.post('/api/v1/cart/items', {
        data: { product_id: sku.product_id, sku_id: sku.id, quantity: 1 },
      })
    ).json()) as ApiResponse;
    expect(addBody.code, `加购失败: ${addBody.message}`).toBe(0);

    const cartBefore = (await (await api.get('/api/v1/cart')).json()) as ApiResponse<{ items: CartItem[] }>;
    expect(cartBefore.data?.items.some((i) => i.sku_id === sku.id), '加购后购物车应含该 SKU').toBe(true);

    const orderBody = assertOk(
      (await (
        await api.post('/api/v1/orders', {
          data: { delivery_type: 1, items: [{ sku_id: sku.id, quantity: 1 }] },
        })
      ).json()) as ApiResponse<OrderResult>,
      '下单',
    );
    createdOrderId = orderBody.id;

    // 购物车清理为下单成功后的异步兜底 goroutine，轮询最多 3s 等待其生效
    let cleared = false;
    for (let i = 0; i < 15; i++) {
      await new Promise((r) => setTimeout(r, 200));
      const cart = (await (await api.get('/api/v1/cart')).json()) as ApiResponse<{ items: CartItem[] }>;
      if (!cart.data?.items.some((it) => it.sku_id === sku.id)) {
        cleared = true;
        break;
      }
    }
    expect(cleared, '下单后购物车未在预期时间内清除该 SKU').toBe(true);
  });
});

test.describe.serial('P0#2 整单退款回补优惠券', () => {
  let adminApi: APIRequestContext;
  let buyerApi: APIRequestContext;
  let sku: SkuRef;
  let couponId = 0;
  let orderId = 0;

  test.beforeAll(async ({ playwright }) => {
    const buyer = await newClientUser(playwright);
    const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
    adminApi = admin.api;
    buyerApi = await newAuthedRequest(playwright, buyer.access_token);

    sku = await pickSku(adminApi);
    await ensureStock(adminApi, sku.id, 20);

    const coupon = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/coupons', {
          data: {
            name: autoName('退款回补验证券'),
            type: 1,
            threshold: 0,
            discount_value: 0.5,
            total_count: 5,
            per_user_limit: 1,
            expire_type: 1,
            valid_days: 7,
            publish_status: 1,
          },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建无门槛券',
    );
    couponId = coupon.id;
  });

  test.afterAll(async () => {
    await buyerApi?.dispose();
    await adminApi?.dispose();
  });

  test('领券成功', async () => {
    const body = (await (
      await buyerApi.post('/api/v1/coupons/receive', { data: { coupon_id: couponId } })
    ).json()) as ApiResponse;
    expect(body.code, `领券失败: ${body.message}`).toBe(0);
  });

  test('带券下单并现金支付', async () => {
    const created = assertOk(
      (await (
        await buyerApi.post('/api/v1/orders', {
          data: {
            delivery_type: 1,
            coupon_id: couponId,
            items: [{ sku_id: sku.id, quantity: 2 }],
          },
        })
      ).json()) as ApiResponse<OrderResult>,
      '带券下单',
    );
    orderId = created.id;
    const paid = (await (
      await buyerApi.post('/api/v1/payments', { data: { order_id: orderId, channel: 'cash' } })
    ).json()) as ApiResponse;
    expect(paid.code, `现金支付失败: ${paid.message}`).toBe(0);
  });

  test('下单后券流转为已使用', async () => {
    const body = assertOk(
      (await (await buyerApi.get('/api/v1/user/coupons', { params: { status: 2 } })).json()) as ApiResponse<UserCoupon[]>,
      '已使用券列表',
    );
    expect(asList<UserCoupon>(body).some((c) => c.coupon_id === couponId), '券未流转为已使用').toBe(true);
  });

  test('买家申请整单退款', async () => {
    const resp = await buyerApi.post(`/api/v1/orders/${orderId}/refund`, { data: { reason: 'AUTO-TEST 退款回补验证' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `申请退款失败: ${body.message}`).toBe(0);
  });

  test('后台定位退款单并审批同意', async () => {
    const listBody = (await (
      await adminApi.get('/api/v1/admin/refunds', { params: { page: 1, page_size: 50 } })
    ).json()) as ApiResponse<{ id: number; order_id?: number }[]>;
    expect(listBody.code, `退款列表失败: ${listBody.message}`).toBe(0);
    const record = asList<{ id: number; order_id?: number }>(listBody.data).find((r) => r.order_id === orderId);
    expect(record, '退款列表中未找到本单退款记录').toBeTruthy();

    const approveBody = (await (
      await adminApi.post(`/api/v1/admin/refunds/${record!.id}/approve`, { data: { note: 'AUTO-TEST 同意' } })
    ).json()) as ApiResponse;
    expect(approveBody.code, `审批失败: ${approveBody.message}`).toBe(0);
  });

  test('整单退款完成后券回补为未使用', async () => {
    const body = assertOk(
      (await (await buyerApi.get('/api/v1/user/coupons', { params: { status: 1 } })).json()) as ApiResponse<UserCoupon[]>,
      '未使用券列表',
    );
    expect(asList<UserCoupon>(body).some((c) => c.coupon_id === couponId), '整单退款后券未回补为未使用状态').toBe(true);
  });
});

test.describe.serial('P3#15 取货码防暴力破解', () => {
  let adminApi: APIRequestContext;
  let buyerApi: APIRequestContext;
  let orderId = 0;

  test.beforeAll(async ({ playwright }) => {
    const buyer = await newClientUser(playwright);
    const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
    adminApi = admin.api;
    buyerApi = await newAuthedRequest(playwright, buyer.access_token);

    const sku = await pickSku(adminApi);
    await ensureStock(adminApi, sku.id, 10);

    const created = assertOk(
      (await (
        await buyerApi.post('/api/v1/orders', {
          data: { delivery_type: 1, items: [{ sku_id: sku.id, quantity: 1 }] },
        })
      ).json()) as ApiResponse<OrderResult>,
      '创建自提单',
    );
    orderId = created.id;
    const paid = (await (
      await buyerApi.post('/api/v1/payments', { data: { order_id: orderId, channel: 'cash' } })
    ).json()) as ApiResponse;
    expect(paid.code, `现金支付失败: ${paid.message}`).toBe(0);
  });

  test.afterAll(async () => {
    await buyerApi?.dispose();
    await adminApi?.dispose();
  });

  test('连续 5 次输错取货码均返回"取货码不正确"（尚未触发锁定提示）', async () => {
    for (let i = 0; i < 5; i++) {
      const resp = await adminApi.post(`/api/v1/admin/orders/${orderId}/verify-pickup`, {
        data: { pickup_code: '000000' },
      });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `第 ${i + 1} 次错误取货码应被拒绝`).not.toBe(0);
      expect(body.message, `第 ${i + 1} 次错误提示文案异常`).toContain('取货码不正确');
    }
  });

  test('第 6 次触发锁定，提示需等待', async () => {
    const resp = await adminApi.post(`/api/v1/admin/orders/${orderId}/verify-pickup`, {
      data: { pickup_code: '000000' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '锁定后应继续拒绝').not.toBe(0);
    expect(body.message, '应提示错误次数过多需等待').toContain('取货码错误次数过多');
  });
});

test.describe.serial('P3#16 下单接口 per-user 限流', () => {
  let api: APIRequestContext;

  test.beforeAll(async ({ playwright }) => {
    const buyer = await newClientUser(playwright);
    api = await newAuthedRequest(playwright, buyer.access_token);
  });

  test.afterAll(async () => {
    await api?.dispose();
  });

  test('桶容量内请求正常处理，超出后返回 429', async () => {
    // 网关配置：UserRateLimitMiddleware(0.08, 5)，桶容量 5、补充速率 0.08/s（约 12.5s 补 1 个令牌）
    // 使用空 items 触发参数校验失败即可（不产生真实订单、不依赖库存），
    // 限流中间件在参数绑定之前已消耗令牌，故不影响本用例的限流触发
    const statuses: number[] = [];
    for (let i = 0; i < 6; i++) {
      const resp = await api.post('/api/v1/orders', { data: { delivery_type: 1, items: [] } });
      statuses.push(resp.status());
    }
    expect(statuses.slice(0, 5).every((s) => s === 200), `前 5 次应正常返回 200，实际：${statuses.slice(0, 5)}`).toBe(true);
    expect(statuses[5], `第 6 次应触发限流返回 429，实际：${statuses[5]}`).toBe(429);
  });
});
