import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  assertOk,
  asList,
  type ApiResponse,
  type ClientLoginInfo,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 18：优惠券抵扣全链路
 * 覆盖：建券（满减/折扣/草稿）→ 领券中心可见 → C 端领券 → 我的券状态
 *       → 带券下单金额核验 → 券转已使用 → 已用券复用拒绝
 *       → 未达门槛拒绝 → 超限领拒绝 → 草稿券不可领
 * 说明：自建商品保证金额可控（单价 3.9，数量 2 → 商品额 7.8）；
 *       环境可能存在其他营销活动叠加，故金额断言采用恒等式 + 下限策略
 */

const UNIT_PRICE = 3.9; // 自建商品单价
const BUY_QTY = 2; // 每单购买数量 → 商品额 7.8

interface Coupon {
  id: number;
  name: string;
  type: number;
  threshold: number;
  discount_value: number;
  publish_status: number;
  [k: string]: unknown;
}

interface UserCoupon {
  id: number;
  coupon_id: number;
  name: string;
  type: number;
  threshold: number;
  discount_value: number;
  status: number; // 1未使用 2已使用 3已过期
  expire_at?: string;
  [k: string]: unknown;
}

interface OrderDetail {
  id: number;
  status: number;
  total_amount: number;
  discount_amount: number;
  delivery_fee: number;
  pay_amount: number;
  [k: string]: unknown;
}

/**
 * 创建新 C 端用户并返回携带已绑定社区的 token
 * 首登自动绑社区，但登录时签发的 JWT 不含 community_id，
 * 下单等依赖社区语境的接口会报参数错误，故绑社区后用同号重登刷新 token
 * - 函数名: newClientUser
 * - 参数: playwright — Playwright 实例
 * - 返回: Promise<ClientLoginInfo>
 * - 异常: 登录或绑社区失败时抛出
 */
async function newClientUser(playwright: Playwright): Promise<ClientLoginInfo> {
  const phone = uniquePhone();
  await clientLogin(playwright.request, phone);
  return clientLogin(playwright.request, phone);
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let communityId = 0;
let buyerToken = '';
let productId = 0;
let categoryId = 0;
let skuId = 0;

test.beforeAll(async ({ playwright }) => {
  const buyer = await newClientUser(playwright);
  buyerToken = buyer.access_token;
  communityId = buyer.user.community_id;
  const admin = await newAdminContext(playwright, undefined, undefined, communityId);
  adminApi = admin.api;

  // 自建分类 + 商品 + 上架，保证金额可控
  try {
    const catBody = assertOk(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('券测试分类') } })).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    );
    categoryId = catBody.id;
    const prodBody = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/products', {
          data: {
            category_id: categoryId,
            name: autoName('券测试商品'),
            unit: '份',
            is_multi_spec: false,
            sku_items: [{ price: UNIT_PRICE, stock: 50, safety_stock: 5 }],
          },
        })
      ).json()) as ApiResponse<{ product?: { id: number }; id?: number; skus?: { id: number }[] }>,
      '创建商品',
    );
    productId = prodBody.product?.id ?? prodBody.id ?? 0;
    const on = (await (
      await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })
    ).json()) as ApiResponse;
    if (on.code !== 0) throw new Error(`上架失败 code=${on.code}`);
    const detail = assertOk(
      (await (await adminApi.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<{ skus?: { id: number }[] }>,
      '商品详情',
    );
    skuId = detail.skus?.[0]?.id ?? 0;
  } catch (e) {
    console.warn(`[18] 造数失败，用例将跳过：${e instanceof Error ? e.message : e}`);
    productId = 0;
  }
});

test.afterAll(async () => {
  // 清理：下架删除商品与分类（券不删，便于后台留痕核对）
  try {
    if (productId) await adminApi.delete(`/api/v1/admin/products/${productId}`);
    if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

/** 带券下单（共用步骤）：创建自提单 + cash 支付，返回订单 ID
 * - 函数名: createCouponOrder
 * - 参数: buyerApi — 买家已认证上下文；couponId — 券模板 ID（0 表示不带券）
 * - 返回: Promise<number> 订单 ID
 * - 异常: 下单业务失败时抛出 Error（含服务端 message）
 */
async function createCouponOrder(buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>, couponId: number): Promise<number> {
  const orderBody = assertOk(
    (await (
      await buyerApi.post('/api/v1/orders', {
        data: {
          delivery_type: 1,
          address_id: 0,
          remark: 'AUTO-TEST 券抵扣',
          coupon_id: couponId,
          items: [{ sku_id: skuId, quantity: BUY_QTY }],
        },
      })
    ).json()) as ApiResponse<{ id: number }>,
    'C 端带券下单',
  );
  const payBody = (await (
    await buyerApi.post('/api/v1/payments', { data: { order_id: orderBody.id, channel: 'cash' } })
  ).json()) as ApiResponse;
  expect(payBody.code, `现金支付失败: ${payBody.message}`).toBe(0);
  return orderBody.id;
}

test.describe.serial('满减券抵扣主链路', () => {
  let couponId = 0;
  let orderId = 0;

  test('1. 管理端建满减券并直接发布', async () => {
    test.skip(!productId, '环境造数失败');
    const body = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/coupons', {
          data: {
            name: autoName('满减券-立减1元'),
            type: 1,
            threshold: 2,
            discount_value: 1,
            total_count: 50,
            per_user_limit: 2,
            expire_type: 1,
            valid_days: 7,
            publish_status: 1,
            rule_text: 'AUTO-TEST 满2减1',
          },
        })
      ).json()) as ApiResponse<Coupon>,
      '创建满减券',
    );
    couponId = body.id;
    expect(couponId).toBeGreaterThan(0);
    expect(body.publish_status).toBe(1);
    expect(body.threshold).toBe(2);
    expect(body.discount_value).toBe(1);
  });

  test('2. C 端领券中心可见该券', async ({ playwright }) => {
    test.skip(couponId <= 0, '券未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = assertOk(
        (await (await buyerApi.get('/api/v1/coupons', { params: { community_id: communityId } })).json()) as ApiResponse<Coupon[]>,
        '领券中心列表',
      );
      expect(asList<Coupon>(body).some((c) => c.id === couponId), '领券中心未展示新券').toBe(true);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('3. C 端领券成功', async ({ playwright }) => {
    test.skip(couponId <= 0, '券未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = (await (
        await buyerApi.post('/api/v1/coupons/receive', { data: { coupon_id: couponId } })
      ).json()) as ApiResponse;
      expect(body.code, `领券失败: ${body.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('4. 我的券列表可见未使用券且字段正确', async ({ playwright }) => {
    test.skip(couponId <= 0, '券未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = assertOk(
        (await (await buyerApi.get('/api/v1/user/coupons', { params: { status: 1 } })).json()) as ApiResponse<UserCoupon[]>,
        '我的券列表',
      );
      const uc = asList<UserCoupon>(body).find((c) => c.coupon_id === couponId);
      expect(uc, '未找到刚领取的券').toBeTruthy();
      expect(uc!.status).toBe(1);
      expect(uc!.threshold).toBe(2);
      expect(uc!.discount_value).toBe(1);
      expect(uc!.expire_at, '领取后N天类型的券应有过期时间').toBeTruthy();
    } finally {
      await buyerApi.dispose();
    }
  });

  test('5. 带券下单并现金支付', async ({ playwright }) => {
    test.skip(couponId <= 0, '券未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      orderId = await createCouponOrder(buyerApi, couponId);
      expect(orderId).toBeGreaterThan(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('6. 订单详情金额核验：券抵扣生效且恒等式成立', async ({ playwright }) => {
    test.skip(orderId <= 0, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const detail = assertOk(
        (await (await buyerApi.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<OrderDetail>,
        '订单详情',
      );
      const goodsAmount = UNIT_PRICE * BUY_QTY; // 7.8
      expect(detail.total_amount).toBeCloseTo(goodsAmount, 2);
      // 环境可能叠加其他活动，券至少抵 1 元，至多不超过商品总额
      expect(detail.discount_amount).toBeGreaterThanOrEqual(1);
      expect(detail.discount_amount).toBeLessThanOrEqual(detail.total_amount);
      // 自提单无配送费：pay_amount = total_amount - discount_amount
      expect(detail.delivery_fee).toBe(0);
      expect(detail.pay_amount).toBeCloseTo(detail.total_amount - detail.discount_amount, 2);
      expect(detail.status).toBe(2); // 已支付
    } finally {
      await buyerApi.dispose();
    }
  });

  test('7. 券状态流转为已使用且关联订单', async ({ playwright }) => {
    test.skip(orderId <= 0, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = assertOk(
        (await (await buyerApi.get('/api/v1/user/coupons', { params: { status: 2 } })).json()) as ApiResponse<UserCoupon[]>,
        '已使用券列表',
      );
      const uc = asList<UserCoupon>(body).find((c) => c.coupon_id === couponId);
      expect(uc, '券未流转为已使用').toBeTruthy();
      // 注：券与订单的关联（user_coupons.order_id）未在响应中暴露，由 18-6 的金额核验兜底
    } finally {
      await buyerApi.dispose();
    }
  });

  test('8. 已使用的券再次下单被拒绝', async ({ playwright }) => {
    test.skip(orderId <= 0, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = (await (
        await buyerApi.post('/api/v1/orders', {
          data: {
            delivery_type: 1,
            address_id: 0,
            coupon_id: couponId,
            items: [{ sku_id: skuId, quantity: 1 }],
          },
        })
      ).json()) as ApiResponse;
      expect(body.code, '已用券应下单失败').not.toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });
});

test.describe.serial('门槛与限领边界', () => {
  test('9. 高门槛券下单被拒绝（未达使用门槛）', async ({ playwright }) => {
    test.skip(!productId, '环境造数失败');
    // 建门槛 9999 的满减券
    const coupon = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/coupons', {
          data: {
            name: autoName('高门槛券'),
            type: 1,
            threshold: 9999,
            discount_value: 1,
            total_count: 10,
            per_user_limit: 1,
            expire_type: 1,
            valid_days: 7,
            publish_status: 1,
          },
        })
      ).json()) as ApiResponse<Coupon>,
      '创建高门槛券',
    );
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const recv = (await (
        await buyerApi.post('/api/v1/coupons/receive', { data: { coupon_id: coupon.id } })
      ).json()) as ApiResponse;
      expect(recv.code, `领高门槛券失败: ${recv.message}`).toBe(0);
      // 商品额 7.8 < 门槛 9999，下单应被拒
      const orderBody = (await (
        await buyerApi.post('/api/v1/orders', {
          data: {
            delivery_type: 1,
            address_id: 0,
            coupon_id: coupon.id,
            items: [{ sku_id: skuId, quantity: BUY_QTY }],
          },
        })
      ).json()) as ApiResponse;
      expect(orderBody.code, '未达门槛应下单失败').not.toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('10. 超出每人限领次数被拒绝', async ({ playwright }) => {
    test.skip(!productId, '环境造数失败');
    const coupon = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/coupons', {
          data: {
            name: autoName('限领1次券'),
            type: 1,
            threshold: 0,
            discount_value: 0.5,
            total_count: 10,
            per_user_limit: 1,
            expire_type: 1,
            valid_days: 7,
            publish_status: 1,
          },
        })
      ).json()) as ApiResponse<Coupon>,
      '创建限领券',
    );
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const first = (await (
        await buyerApi.post('/api/v1/coupons/receive', { data: { coupon_id: coupon.id } })
      ).json()) as ApiResponse;
      expect(first.code, `首次领券失败: ${first.message}`).toBe(0);
      const second = (await (
        await buyerApi.post('/api/v1/coupons/receive', { data: { coupon_id: coupon.id } })
      ).json()) as ApiResponse;
      expect(second.code, '超过限领次数应失败').not.toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('11. 草稿券不可领取', async ({ playwright }) => {
    test.skip(!productId, '环境造数失败');
    // 不传 publish_status，默认草稿
    const coupon = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/coupons', {
          data: {
            name: autoName('草稿券'),
            type: 1,
            threshold: 0,
            discount_value: 1,
            total_count: 10,
            expire_type: 1,
            valid_days: 7,
          },
        })
      ).json()) as ApiResponse<Coupon>,
      '创建草稿券',
    );
    expect(coupon.publish_status).toBe(0);
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      // 领券中心不可见
      const center = assertOk(
        (await (await buyerApi.get('/api/v1/coupons', { params: { community_id: communityId } })).json()) as ApiResponse<Coupon[]>,
        '领券中心列表',
      );
      expect(asList<Coupon>(center).some((c) => c.id === coupon.id), '草稿券不应出现在领券中心').toBe(false);
      // 直接领取被拒
      const recv = (await (
        await buyerApi.post('/api/v1/coupons/receive', { data: { coupon_id: coupon.id } })
      ).json()) as ApiResponse;
      expect(recv.code, '草稿券应领取失败').not.toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });
});

test.describe.serial('折扣券抵扣', () => {
  test('12. 折扣券下单按比例抵扣', async ({ playwright }) => {
    test.skip(!productId, '环境造数失败');
    const coupon = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/coupons', {
          data: {
            name: autoName('折扣券-85折'),
            type: 2,
            threshold: 0,
            discount_value: 0.85,
            total_count: 10,
            per_user_limit: 1,
            expire_type: 1,
            valid_days: 7,
            publish_status: 1,
          },
        })
      ).json()) as ApiResponse<Coupon>,
      '创建折扣券',
    );
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const recv = (await (
        await buyerApi.post('/api/v1/coupons/receive', { data: { coupon_id: coupon.id } })
      ).json()) as ApiResponse;
      expect(recv.code, `领折扣券失败: ${recv.message}`).toBe(0);

      const orderBody = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: {
              delivery_type: 1,
              address_id: 0,
              remark: 'AUTO-TEST 折扣券',
              coupon_id: coupon.id,
              items: [{ sku_id: skuId, quantity: BUY_QTY }],
            },
          })
        ).json()) as ApiResponse<{ id: number }>,
        'C 端带折扣券下单',
      );
      const payBody = (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: orderBody.id, channel: 'cash' } })
      ).json()) as ApiResponse;
      expect(payBody.code, `现金支付失败: ${payBody.message}`).toBe(0);

      const detail = assertOk(
        (await (await buyerApi.get(`/api/v1/orders/${orderBody.id}`)).json()) as ApiResponse<OrderDetail>,
        '订单详情',
      );
      const goodsAmount = UNIT_PRICE * BUY_QTY; // 7.8
      expect(detail.total_amount).toBeCloseTo(goodsAmount, 2);
      // 85 折至少省 7.8 * 0.15 ≈ 1.17（环境叠加其他活动时折扣更大）
      expect(detail.discount_amount).toBeGreaterThanOrEqual(goodsAmount * 0.15 - 0.01);
      expect(detail.pay_amount).toBeCloseTo(detail.total_amount - detail.discount_amount, 2);
    } finally {
      await buyerApi.dispose();
    }
  });
});
