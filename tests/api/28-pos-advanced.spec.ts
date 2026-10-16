import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  type ApiResponse,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 28：POS 收银台进阶
 * 覆盖：单品改价（price 覆盖商品档案价）→ 手动优惠 manual_discount 抹零核验
 *       → 手动优惠超应付被拒 → 0 元全额优惠单（pay_amount=0 创建即成功）
 *       → 会员单代选券（coupon_id 抵扣核验 + 券转已使用）→ 散客单带券被忽略
 * 金额口径：自提单免运费，pay_amount = 商品金额 - 券抵扣 - manual_discount
 */

interface PosOrderResp {
  id: number;
  order_no: string;
  pay_amount: number;
  delivery_fee: number;
  pickup_code: string;
  paid: boolean;
}

interface UserCoupon {
  id: number;
  coupon_id: number;
  status: number;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let memberToken = '';
let skuId = 0;
let productId = 0;
let categoryId = 0;
const SKU_PRICE = 5.8; // 自建商品单价，金额断言以此为准
let couponId = 0;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;

  // 开工班次（容错：已有进行中班次时继续用当前班次）
  const startResp = await adminApi.post('/api/v1/admin/pos/shifts/start', { data: { note: 'AUTO-TEST POS 进阶开工' } });
  const startBody = (await startResp.json()) as ApiResponse;
  if (startBody.code !== 0) {
    console.log(`[28] 开工返回 code=${startBody.code} message=${startBody.message}（可能已有进行中班次）`);
  }
  const cur = (await (await adminApi.get('/api/v1/admin/pos/shifts/current')).json()) as ApiResponse<{ id?: number }>;
  if (!cur.data?.id) throw new Error('POS 无进行中班次且开工失败，POS 进阶用例无法执行');

  // 自建价格可控的单规格商品（环境商品价格不可控，无法承载优惠核验）
  const cat = (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('POS 进阶分类') } })).json()) as ApiResponse<{ id: number }>;
  expect(cat.code, `创建分类失败: ${cat.message}`).toBe(0);
  categoryId = cat.data!.id;
  const prod = (await (await adminApi.post('/api/v1/admin/products', {
    data: {
      category_id: categoryId,
      name: autoName('POS 进阶商品'),
      unit: '份',
      is_multi_spec: false,
      sku_items: [{ price: SKU_PRICE, stock: 200, safety_stock: 5 }],
    },
  })).json()) as ApiResponse<{ product?: { id: number }; id?: number }>;
  expect(prod.code, `创建商品失败: ${prod.message}`).toBe(0);
  productId = prod.data?.product?.id ?? prod.data?.id ?? 0;
  const on = (await (await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })).json()) as ApiResponse;
  expect(on.code).toBe(0);
  const detail = (await (await adminApi.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<{ skus: { id: number }[] }>;
  skuId = detail.data?.skus?.[0]?.id ?? 0;
  expect(skuId, 'SKU 未就绪').toBeGreaterThan(0);

  const member = await clientLogin(playwright.request, uniquePhone());
  memberToken = member.access_token;
  globalThis.__memberId = member.user.id;

  // 建低门槛满减券（threshold=0.01 减 1 元）并直接发放给会员
  const fmt = (d: Date) => d.toISOString().slice(0, 19).replace('T', ' ');
  const createResp = await adminApi.post('/api/v1/admin/coupons', {
    data: {
      name: `AUTO-TEST POS 代选券 ${Date.now()}`,
      type: 1,
      threshold: 0.01,
      discount_value: 1,
      total_count: 10,
      per_user_limit: 1,
      start_time: fmt(new Date(Date.now() + 5 * 60 * 1000)),
      end_time: fmt(new Date(Date.now() + 7 * 24 * 60 * 60 * 1000)),
      valid_days: 7,
      publish_status: 1,
    },
  });
  const createBody = (await createResp.json()) as ApiResponse<{ id: number }>;
  if (createBody.code === 0) {
    couponId = createBody.data?.id ?? 0;
    await adminApi.post(`/api/v1/admin/coupons/${couponId}/grant`, {
      data: { user_ids: [member.user.id] },
    });
  } else {
    console.log(`[28] 代选券创建失败 code=${createBody.code} message=${createBody.message}`);
  }
});

test.afterAll(async () => {
  // 清理自建商品（下架 → 删除商品 → 删除分类），避免污染环境
  if (adminApi && productId) {
    await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 0 } });
    await adminApi.delete(`/api/v1/admin/products/${productId}`);
  }
  if (adminApi && categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  await adminApi?.dispose();
});

declare global {
  // eslint-disable-next-line no-var
  var __memberId: number;
}

test.describe.serial('POS 收银台进阶', () => {
  test('单品改价下单：price 覆盖商品档案价', async () => {
    test.skip(!skuId, '自建商品未就绪');
    const resp = await adminApi.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: 0,
        pay_type: 4,
        delivery_type: 3,
        items: [{ sku_id: skuId, quantity: 1, price: 2 }],
        remark: 'AUTO-TEST 单品改价',
      },
    });
    const body = (await resp.json()) as ApiResponse<PosOrderResp>;
    expect(body.code, `单品改价下单失败: ${body.message}`).toBe(0);
    expect(body.data!.pay_amount).toBeCloseTo(2, 2);
  });

  test('手动优惠抹零：pay_amount = 应付 - manual_discount', async () => {
    test.skip(!skuId, '自建商品未就绪');
    const resp = await adminApi.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: 0,
        pay_type: 4,
        delivery_type: 3,
        manual_discount: 1,
        items: [{ sku_id: skuId, quantity: 1 }],
      },
    });
    const body = (await resp.json()) as ApiResponse<PosOrderResp>;
    expect(body.code, `手动优惠下单失败: ${body.message}`).toBe(0);
    expect(body.data!.pay_amount).toBeCloseTo(SKU_PRICE - 1, 2);
  });

  test('手动优惠超应付被拒绝', async () => {
    test.skip(!skuId, '自建商品未就绪');
    const resp = await adminApi.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: 0,
        pay_type: 4,
        delivery_type: 3,
        manual_discount: SKU_PRICE + 10,
        items: [{ sku_id: skuId, quantity: 1 }],
      },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `手动优惠超应付应被拒绝: ${body.message}`).not.toBe(0);
  });

  test('0 元全额优惠单：pay_amount=0 且创建即成功', async () => {
    test.skip(!skuId, '自建商品未就绪');
    const resp = await adminApi.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: 0,
        pay_type: 4,
        delivery_type: 3,
        manual_discount: SKU_PRICE,
        items: [{ sku_id: skuId, quantity: 1 }],
        remark: 'AUTO-TEST 全额优惠 0 元单',
      },
    });
    const body = (await resp.json()) as ApiResponse<PosOrderResp>;
    expect(body.code, `0 元单失败: ${body.message}`).toBe(0);
    expect(body.data!.pay_amount).toBe(0);
    expect(body.data!.paid).toBe(true);
  });

  test('会员单代选券：券抵扣 1 元生效', async () => {
    test.skip(!skuId || !couponId, '自建商品或代选券未就绪');
    const resp = await adminApi.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: globalThis.__memberId,
        pay_type: 4,
        delivery_type: 3,
        coupon_id: couponId,
        items: [{ sku_id: skuId, quantity: 1 }],
      },
    });
    const body = (await resp.json()) as ApiResponse<PosOrderResp>;
    expect(body.code, `代选券下单失败: ${body.message}`).toBe(0);
    expect(body.data!.pay_amount, `券抵扣后实付应为 ${SKU_PRICE - 1}`).toBeCloseTo(SKU_PRICE - 1, 2);
  });

  test('代选的券状态流转为已使用', async ({ playwright }) => {
    test.skip(!couponId, '代选券未就绪');
    const memberApi = await newAuthedRequest(playwright, memberToken);
    try {
      const resp = await memberApi.get('/api/v1/user/coupons', { params: { status: 2 } });
      const body = (await resp.json()) as ApiResponse<UserCoupon[]>;
      expect(body.code).toBe(0);
      expect(asUsed(body.data ?? []).some((c) => c.coupon_id === couponId), '券未流转为已使用').toBe(true);
    } finally {
      await memberApi.dispose();
    }
  });

  test('散客单带券被服务端忽略（按原价成交）', async () => {
    test.skip(!skuId, '自建商品未就绪');
    const resp = await adminApi.post('/api/v1/admin/pos/orders', {
      data: {
        user_id: 0,
        pay_type: 4,
        delivery_type: 3,
        coupon_id: couponId || 999999,
        items: [{ sku_id: skuId, quantity: 1 }],
      },
    });
    const body = (await resp.json()) as ApiResponse<PosOrderResp>;
    expect(body.code, `散客单带券应正常成交: ${body.message}`).toBe(0);
    expect(body.data!.pay_amount, '散客单券应被忽略，按原价成交').toBeCloseTo(SKU_PRICE, 2);
  });
});

/** 兼容数组/分页两种券列表结构 */
function asUsed(data: unknown): UserCoupon[] {
  if (Array.isArray(data)) return data as UserCoupon[];
  return (data as { list?: UserCoupon[] })?.list ?? [];
}
