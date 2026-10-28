import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  assertOk,
  type ApiResponse,
  type ClientLoginInfo,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 85：券可用性试算（H7 回归）
 * 背景：C 端确认页原按「全车金额」判定券门槛，后端按「券适用范围内商品金额」校验
 *       （order.go applyCoupon → couponEligibleAmount），含不适用商品的订单在提交时被打回。
 * 覆盖：适用范围券的试算口径（范围内金额 / 快照门槛 / 预计抵扣）
 *       → 全车金额达标但范围内不足时判不可用（关键回归）
 *       → 试算结论与实际下单校验一致
 */

const PRICE = 10; // 两个自建商品单价一致，便于用数量构造金额

/** 试算结果行 */
interface EligibilityRow {
  coupon_id: number;
  available: boolean;
  reason: string;
  eligible_amount: number;
  threshold: number;
  discount_amount: number;
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

/**
 * 通过管理端建一个自建商品并上架
 * - 函数名: createProduct
 * - 参数: api — 管理端已认证上下文；categoryId — 归属分类；name — 商品名前缀
 * - 返回: Promise<{ productId: number; skuId: number }> 商品与首个 SKU 的 ID
 * - 异常: 造数失败时抛出（由 beforeAll 统一捕获降级为 skip）
 */
async function createProduct(
  api: Awaited<ReturnType<typeof newAdminContext>>['api'],
  categoryId: number,
  name: string,
): Promise<{ productId: number; skuId: number }> {
  const prodBody = assertOk(
    (await (
      await api.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name: autoName(name),
          unit: '份',
          is_multi_spec: false,
          sku_items: [{ price: PRICE, stock: 50, safety_stock: 5 }],
        },
      })
    ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
    '创建商品',
  );
  const productId = prodBody.product?.id ?? prodBody.id ?? 0;
  const on = (await (
    await api.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })
  ).json()) as ApiResponse;
  if (on.code !== 0) throw new Error(`商品上架失败 code=${on.code}`);
  const detail = assertOk(
    (await (await api.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<{ skus?: { id: number }[] }>,
    '商品详情',
  );
  const skuId = detail.skus?.[0]?.id ?? 0;
  if (!skuId) throw new Error('商品 SKU 缺失');
  return { productId, skuId };
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let communityId = 0;
let buyerToken = '';
let categoryId = 0;
let productAId = 0;
let productBId = 0;
let skuA = 0;
let skuB = 0;
let couponId = 0;

test.beforeAll(async ({ playwright }) => {
  const buyer = await newClientUser(playwright);
  buyerToken = buyer.access_token;
  communityId = buyer.user.community_id;
  const admin = await newAdminContext(playwright, undefined, undefined, communityId);
  adminApi = admin.api;

  try {
    const catBody = assertOk(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('券试算分类') } })).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    );
    categoryId = catBody.id;
    const prodA = await createProduct(adminApi, categoryId, '券试算商品A');
    productAId = prodA.productId;
    skuA = prodA.skuId;
    const prodB = await createProduct(adminApi, categoryId, '券试算商品B');
    productBId = prodB.productId;
    skuB = prodB.skuId;
    // 满 15 减 2，仅适用商品 A：范围内金额（A）与全车金额（A+B）分离，才能验证 H7 口径
    const couponBody = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/coupons', {
          data: {
            name: autoName('范围券-满15减2'),
            type: 1,
            threshold: 15,
            discount_value: 2,
            total_count: 50,
            per_user_limit: 2,
            expire_type: 1,
            valid_days: 7,
            publish_status: 1,
            rule_text: 'AUTO-TEST 满15减2 仅限商品A',
            scopes: [{ scope_type: 3, product_id: productAId }],
          },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建范围券',
    );
    couponId = couponBody.id;
  } catch (e) {
    console.warn(`[85] 造数失败，用例将跳过：${e instanceof Error ? e.message : e}`);
    couponId = 0;
  }
});

test.afterAll(async () => {
  try {
    if (productAId) await adminApi.delete(`/api/v1/admin/products/${productAId}`);
    if (productBId) await adminApi.delete(`/api/v1/admin/products/${productBId}`);
    if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

/**
 * 调用券可用性试算接口并返回首行结果
 * - 函数名: preview
 * - 参数: buyerApi — 买家已认证上下文；items — 商品清单（sku_id + quantity）
 * - 返回: Promise<EligibilityRow> 首行试算结果
 * - 异常: 接口业务失败时抛出 Error
 */
async function preview(
  buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>,
  items: { sku_id: number; quantity: number }[],
): Promise<EligibilityRow> {
  const body = assertOk(
    (await (
      await buyerApi.post('/api/v1/orders/coupon-eligibility/preview', {
        data: { items, coupon_ids: [couponId], community_id: communityId },
      })
    ).json()) as ApiResponse<EligibilityRow[]>,
    '券可用性试算',
  );
  expect(body.length, '试算应返回请求的券').toBe(1);
  return body[0];
}

test.describe.serial('券可用性试算', () => {
  test('1. 买家领取范围券', async ({ playwright }) => {
    test.skip(couponId <= 0, '环境造数失败');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      assertOk(
        (await (await buyerApi.post('/api/v1/coupons/receive', { data: { coupon_id: couponId } })).json()) as ApiResponse,
        '领取范围券',
      );
    } finally {
      await buyerApi.dispose();
    }
  });

  test('2. 仅买范围内商品且未达门槛：范围内金额不足判不可用', async ({ playwright }) => {
    test.skip(couponId <= 0, '环境造数失败');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const row = await preview(buyerApi, [{ sku_id: skuA, quantity: 1 }]);
      expect(row.coupon_id).toBe(couponId);
      expect(row.available).toBe(false);
      expect(row.eligible_amount).toBe(PRICE);
      expect(row.threshold).toBe(15);
      expect(row.discount_amount).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('3. 全车金额达标但范围内不足：仍判不可用（H7 关键回归）', async ({ playwright }) => {
    test.skip(couponId <= 0, '环境造数失败');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const row = await preview(buyerApi, [
        { sku_id: skuA, quantity: 1 },
        { sku_id: skuB, quantity: 1 },
      ]);
      // 全车 20 ≥ 门槛 15，但适用范围内只有 A 的 10 —— 旧口径会误判为可用
      expect(row.available).toBe(false);
      expect(row.eligible_amount).toBe(PRICE);
      expect(row.threshold).toBe(15);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('4. 范围内金额达门槛：可用且按范围内金额抵扣', async ({ playwright }) => {
    test.skip(couponId <= 0, '环境造数失败');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const row = await preview(buyerApi, [{ sku_id: skuA, quantity: 2 }]);
      expect(row.available).toBe(true);
      expect(row.reason).toBe('');
      expect(row.eligible_amount).toBe(PRICE * 2);
      expect(row.discount_amount).toBe(2);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('5. 试算结论与实际下单校验一致：不可用时下单被拒', async ({ playwright }) => {
    test.skip(couponId <= 0, '环境造数失败');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const row = await preview(buyerApi, [
        { sku_id: skuA, quantity: 1 },
        { sku_id: skuB, quantity: 1 },
      ]);
      expect(row.available).toBe(false);
      const orderBody = (await (
        await buyerApi.post('/api/v1/orders', {
          data: {
            delivery_type: 1,
            address_id: 0,
            remark: 'AUTO-TEST 范围券试算一致性',
            coupon_id: couponId,
            items: [
              { sku_id: skuA, quantity: 1 },
              { sku_id: skuB, quantity: 1 },
            ],
          },
        })
      ).json()) as ApiResponse;
      expect(orderBody.code).not.toBe(0);
      expect(String(orderBody.message)).toContain('使用门槛');
    } finally {
      await buyerApi.dispose();
    }
  });

  test('6. 未持有的券 ID 判为不可用', async ({ playwright }) => {
    test.skip(couponId <= 0, '环境造数失败');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders/coupon-eligibility/preview', {
            data: { items: [{ sku_id: skuA, quantity: 2 }], coupon_ids: [999999999], community_id: communityId },
          })
        ).json()) as ApiResponse<EligibilityRow[]>,
        '未持有券试算',
      );
      expect(body.length).toBe(1);
      expect(body[0].available).toBe(false);
      expect(body[0].reason).toContain('失效');
    } finally {
      await buyerApi.dispose();
    }
  });
});
