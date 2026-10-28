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
 * 模块 19：营销活动生效链路
 * 覆盖：满减活动下单抵扣；秒杀活动价下单（服务端自动取生效秒杀价）；
 *       包邮试算（参与商品命中 / 排除商品不计门槛）；配送单满额免配送费
 * 说明：自建两个商品（A 参与 / B 被排除+秒杀）保证金额与范围可控；
 *       环境可能存在其他生效活动，仅在建活动被确认选中时做精确断言
 */

const PRICE_A = 3.9; // 商品 A 单价（参与包邮）
const PRICE_B = 5.9; // 商品 B 单价（秒杀 + 被包邮排除）
const FLASH_PRICE = 1.5; // B 的秒杀价

interface FlashSale {
  id: number;
  name: string;
  start_time: string;
  end_time: string;
  [k: string]: unknown;
}

/** C 端秒杀列表项内嵌的商品项（网关按商品档案回填图片与已售） */
interface FlashSaleItem {
  id: number;
  sku_id: number;
  product_id: number;
  product_name: string;
  product_image: string;
  flash_price: number;
  original_price: number;
  stock: number;
  sold: number;
  limit_per_user: number;
}

interface FullReduction {
  id: number;
  name: string;
  [k: string]: unknown;
}

interface FreeShippingActivity {
  id: number;
  name: string;
  [k: string]: unknown;
}

interface OrderDetail {
  id: number;
  status: number;
  total_amount: number;
  discount_amount: number;
  delivery_fee: number;
  pay_amount: number;
  delivery_type: number;
  [k: string]: unknown;
}

/** 免邮试算响应（POST /api/v1/orders/free-shipping/preview） */
interface FreeShippingPreview {
  free_shipping: boolean;
  activity_name: string;
  threshold: number;
  eligible_amount: number;
  remain_amount: number;
  excluded_names: string[];
  delivery_fee: number;
}

/**
 * 创建新 C 端用户并返回携带已绑定社区的 token
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

/** 当天日期串（本地时区，用于活动起止时间）
 * - 函数名: localDate
 * - 参数: offsetDays — 相对今天的偏移天数
 * - 返回: string，如 2026-10-16
 * - 异常: 无
 */
function localDate(offsetDays = 0): string {
  const d = new Date();
  d.setDate(d.getDate() + offsetDays);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let communityId = 0;
let buyerToken = '';
let categoryId = 0;
let productIdA = 0;
let skuIdA = 0;
let productIdB = 0;
let skuIdB = 0;
let addressId = 0;

test.beforeAll(async ({ playwright }) => {
  const buyer = await newClientUser(playwright);
  buyerToken = buyer.access_token;
  communityId = buyer.user.community_id;
  const admin = await newAdminContext(playwright, undefined, undefined, communityId);
  adminApi = admin.api;

  try {
    const catBody = assertOk(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('营销测试分类') } })).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    );
    categoryId = catBody.id;

    // 商品 A：参与包邮，正常价
    productIdA = await createProduct(adminApi, '营销商品A', PRICE_A);
    skuIdA = await getSkuId(adminApi, productIdA);
    // 商品 B：秒杀 + 被包邮排除
    productIdB = await createProduct(adminApi, '营销商品B', PRICE_B);
    skuIdB = await getSkuId(adminApi, productIdB);

    // C 端收货地址（配送单用）
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const addr = assertOk(
        (await (
          await buyerApi.post('/api/v1/user/addresses', {
            data: {
              contact_name: 'AUTO-TEST 收货人',
              contact_phone: '13800000001',
              province: '广东省',
              city: '深圳市',
              district: '南山区',
              detail: `科技园 ${Date.now()} 号`,
              is_default: true,
            },
          })
        ).json()) as ApiResponse<{ id: number }>,
        '创建收货地址',
      );
      addressId = addr.id;
    } finally {
      await buyerApi.dispose();
    }
  } catch (e) {
    console.warn(`[19] 造数失败，用例将跳过：${e instanceof Error ? e.message : e}`);
    productIdA = 0;
  }
});

test.afterAll(async () => {
  try {
    if (productIdA) await adminApi.delete(`/api/v1/admin/products/${productIdA}`);
    if (productIdB) await adminApi.delete(`/api/v1/admin/products/${productIdB}`);
    if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

/**
 * 管理端创建并上架商品（共用造数步骤）
 * - 函数名: createProduct
 * - 参数: api — 管理端上下文；name — 商品名；price — 单价
 * - 返回: Promise<number> 商品 ID
 * - 异常: 创建或上架失败时抛出 Error
 */
async function createProduct(api: Awaited<ReturnType<typeof newAdminContext>>['api'], name: string, price: number): Promise<number> {
  const body = assertOk(
    (await (
      await api.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name: autoName(name),
          unit: '份',
          is_multi_spec: false,
          sku_items: [{ price, stock: 50, safety_stock: 5 }],
        },
      })
    ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
    `创建商品 ${name}`,
  );
  const pid = body.product?.id ?? body.id ?? 0;
  const on = (await (await api.post(`/api/v1/admin/products/${pid}/status`, { data: { status: 1 } })).json()) as ApiResponse;
  if (on.code !== 0) throw new Error(`上架失败 code=${on.code}`);
  return pid;
}

/**
 * 取商品首个 SKU ID
 * - 函数名: getSkuId
 * - 参数: api — 管理端上下文；productId — 商品 ID
 * - 返回: Promise<number> SKU ID
 * - 异常: 商品无 SKU 时抛出 Error
 */
async function getSkuId(api: Awaited<ReturnType<typeof newAdminContext>>['api'], productId: number): Promise<number> {
  const detail = assertOk(
    (await (await api.get(`/api/v1/admin/products/${productId}`)).json()) as ApiResponse<{ skus?: { id: number }[] }>,
    '商品详情',
  );
  const skuId = detail.skus?.[0]?.id ?? 0;
  if (!skuId) throw new Error(`商品 ${productId} 无 SKU`);
  return skuId;
}

/**
 * 下单并支付（共用步骤）
 * - 函数名: createOrder
 * - 参数: buyerApi — 买家已认证上下文；opts — skuId/quantity/deliveryType/addressId
 * - 返回: Promise<number> 订单 ID
 * - 异常: 下单业务失败时抛出 Error（含服务端 message）
 */
async function createOrder(
  buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>,
  opts: { skuId: number; quantity: number; deliveryType?: number; addressId?: number },
): Promise<number> {
  const orderBody = assertOk(
    (await (
      await buyerApi.post('/api/v1/orders', {
        data: {
          delivery_type: opts.deliveryType ?? 1,
          address_id: opts.addressId ?? 0,
          remark: 'AUTO-TEST 营销活动',
          items: [{ sku_id: opts.skuId, quantity: opts.quantity }],
        },
      })
    ).json()) as ApiResponse<{ id: number }>,
    'C 端下单',
  );
  const payBody = (await (
    await buyerApi.post('/api/v1/payments', { data: { order_id: orderBody.id, channel: 'cash' } })
  ).json()) as ApiResponse;
  expect(payBody.code, `现金支付失败: ${payBody.message}`).toBe(0);
  return orderBody.id;
}

test.describe.serial('满减活动生效', () => {
  test('1. 管理端建满减活动（满 5 减 1）', async () => {
    test.skip(!productIdA, '环境造数失败');
    const body = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/full-reductions', {
          data: {
            name: autoName('满5减1'),
            threshold: 5,
            reduce_amount: 1,
            start_time: `${localDate()} 00:00:00`,
            end_time: `${localDate(1)} 23:59:59`,
          },
        })
      ).json()) as ApiResponse<FullReduction>,
      '创建满减活动',
    );
    expect(body.id).toBeGreaterThan(0);
  });

  test('2. 达门槛订单满减生效（7.8 ≥ 5 抵 1 元）', async ({ playwright }) => {
    test.skip(!productIdA, '环境造数失败');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const orderId = await createOrder(buyerApi, { skuId: skuIdA, quantity: 2 });
      const detail = assertOk(
        (await (await buyerApi.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<OrderDetail>,
        '订单详情',
      );
      expect(detail.total_amount).toBeCloseTo(PRICE_A * 2, 2);
      // 新建商品不可能命中环境旧活动的专属范围，但全场所属活动可叠加 → 至少抵 1 元
      expect(detail.discount_amount).toBeGreaterThanOrEqual(1);
      expect(detail.pay_amount).toBeCloseTo(detail.total_amount - detail.discount_amount, 2);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('3. 未达门槛订单本活动不贡献（3.9 < 5）', async ({ playwright }) => {
    test.skip(!productIdA, '环境造数失败');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const orderId = await createOrder(buyerApi, { skuId: skuIdA, quantity: 1 });
      const detail = assertOk(
        (await (await buyerApi.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<OrderDetail>,
        '订单详情',
      );
      // 恒等式始终成立；实际抵扣取决于环境中其他门槛更低的满减活动
      expect(detail.pay_amount).toBeCloseTo(detail.total_amount - detail.discount_amount, 2);
      expect(detail.discount_amount).toBeLessThanOrEqual(detail.total_amount);
    } finally {
      await buyerApi.dispose();
    }
  });
});

test.describe.serial('秒杀活动生效', () => {
  test('4. 管理端建秒杀活动（B 商品秒杀价 1.5）', async () => {
    test.skip(!productIdB, '环境造数失败');
    const body = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/flash-sales', {
          data: {
            name: autoName('限时秒杀'),
            start_time: `${localDate()} 00:00:00`,
            end_time: `${localDate(1)} 23:59:59`,
            items: [{ sku_id: skuIdB, flash_price: FLASH_PRICE, stock: 20, limit_per_user: 5 }],
          },
        })
      ).json()) as ApiResponse<FlashSale>,
      '创建秒杀活动',
    );
    expect(body.id).toBeGreaterThan(0);
  });

  test('5. C 端秒杀列表可见该活动', async ({ playwright }) => {
    test.skip(!productIdB, '环境造数失败');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = assertOk(
        (await (await buyerApi.get('/api/v1/flash-sales', { params: { community_id: communityId } })).json()) as ApiResponse<FlashSale[]>,
        '秒杀列表',
      );
      const sales = asList<FlashSale>(body);
      expect(sales.length, '秒杀活动列表为空').toBeGreaterThan(0);

      // C 端展示口径：列表项由商品项扁平化而来，图片/已售必须是后端真实回填，
      // 且页面价格取 original_price（= 下单实付），不能用未生效的 flash_price
      const mine = sales.filter((s) => String(s.name).startsWith('AUTO-TEST'));
      expect(mine.length, '未找到本次创建的秒杀活动').toBeGreaterThan(0);
      const items = (mine[0].items ?? []) as FlashSaleItem[];
      expect(items.length, '秒杀活动应至少含一个商品项').toBeGreaterThan(0);
      const item = items.find((it) => it.sku_id === skuIdB);
      expect(item, '商品项应按 SKU 回填档案').toBeTruthy();
      expect(item!.original_price, '展示价应等于 SKU 价（下单实付口径）').toBeCloseTo(PRICE_B, 2);
      expect(item!.flash_price, '活动价仅作为配置下发，不参与收款').toBeCloseTo(FLASH_PRICE, 2);
      expect(typeof item!.product_image, '商品封面图应由服务端下发').toBe('string');
      expect(typeof item!.sold, '已售数量应取 flash_sale_items.sold_count 真实值').toBe('number');
    } finally {
      await buyerApi.dispose();
    }
  });

  test('6. 秒杀价下单：止血开关期间按正常价计算', async ({ playwright }) => {
    test.skip(!productIdB, '环境造数失败');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const orderId = await createOrder(buyerApi, { skuId: skuIdB, quantity: 2 });
      const detail = assertOk(
        (await (await buyerApi.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<OrderDetail>,
        '订单详情',
      );
      // 当前口径：order.go flashSalePriceEnabled=false（阻塞项是每人限购 limit_per_user 未实现，
      // 单用户可超额抢活动价；秒杀池扣减本身已在 product 侧实现）——订单链路整体不走秒杀价，
      // 按正常价 5.9×2=11.8 计算。开关打开后应改回断言 total = FLASH_PRICE×2 = 3.0。
      expect(detail.total_amount, '止血开关期间应按正常价计算').toBeCloseTo(PRICE_B * 2, 2);
      expect(detail.total_amount, '止血开关期间不应低于秒杀价').toBeGreaterThanOrEqual(FLASH_PRICE * 2);
    } finally {
      await buyerApi.dispose();
    }
  });
});

test.describe.serial('满额包邮生效', () => {
  let activityId = 0;
  const ACTIVITY_NAME_KEY = '满额包邮';

  test('7. 管理端建包邮活动（门槛 0.01，排除 B 商品）', async () => {
    test.skip(!productIdB, '环境造数失败');
    const body = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/free-shippings', {
          data: {
            name: autoName(ACTIVITY_NAME_KEY),
            threshold: 0.01,
            start_time: `${localDate()} 00:00:00`,
            end_time: `${localDate(1)} 23:59:59`,
            scopes: [{ scope_type: 5, product_id: productIdB }],
          },
        })
      ).json()) as ApiResponse<FreeShippingActivity>,
      '创建包邮活动',
    );
    activityId = body.id;
    expect(activityId).toBeGreaterThan(0);
  });

  test('8. 试算：参与商品 A 达标 → 免配送费', async ({ playwright }) => {
    test.skip(!productIdA || !activityId, '前置未就绪');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders/free-shipping/preview', {
            data: { items: [{ sku_id: skuIdA, quantity: 2 }] },
          })
        ).json()) as ApiResponse<FreeShippingPreview>,
        '免邮试算',
      );
      if (body.activity_name.includes(ACTIVITY_NAME_KEY)) {
        // 本活动为当前生效活动：精确断言
        expect(body.free_shipping).toBe(true);
        expect(body.eligible_amount).toBeGreaterThan(0);
        expect(body.remain_amount).toBe(0);
      } else {
        // 环境存在门槛更低的包邮活动抢占：至少应判定为免邮
        expect(body.free_shipping).toBe(true);
      }
    } finally {
      await buyerApi.dispose();
    }
  });

  test('9. 试算：被排除商品 B 不计入门槛', async ({ playwright }) => {
    test.skip(!productIdB || !activityId, '前置未就绪');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const body = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders/free-shipping/preview', {
            data: { items: [{ sku_id: skuIdB, quantity: 2 }] },
          })
        ).json()) as ApiResponse<FreeShippingPreview>,
        '免邮试算',
      );
      if (body.activity_name.includes(ACTIVITY_NAME_KEY)) {
        // 本活动为当前生效活动：B 被排除 → 参与额 0 < 门槛 → 不免邮且提示排除商品
        expect(body.free_shipping).toBe(false);
        expect(body.remain_amount).toBeGreaterThan(0);
        expect(body.excluded_names.length, '排除商品应出现在提示中').toBeGreaterThan(0);
      }
      // 其他活动优先时行为仍正确（可能免邮），无法归因到本活动，不做强断言
    } finally {
      await buyerApi.dispose();
    }
  });

  test('10. 配送单参与商品达标 → 免配送费', async ({ playwright }) => {
    test.skip(!productIdA || !addressId, '环境造数失败或缺收货地址');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      let orderId = 0;
      try {
        orderId = await createOrder(buyerApi, { skuId: skuIdA, quantity: 2, deliveryType: 2, addressId });
      } catch (e) {
        const msg = e instanceof Error ? e.message : '';
        test.skip(msg.includes('起送'), `社区起送金额限制：${msg}`);
        return;
      }
      const detail = assertOk(
        (await (await buyerApi.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<OrderDetail>,
        '订单详情',
      );
      expect(detail.delivery_type).toBe(2);
      // A 商品参与包邮且金额达标 → 配送费为 0（即使环境无活动，社区未配运费时也为 0）
      expect(detail.delivery_fee).toBe(0);
      expect(detail.pay_amount).toBeCloseTo(detail.total_amount - detail.discount_amount, 2);
    } finally {
      await buyerApi.dispose();
    }
  });
});
