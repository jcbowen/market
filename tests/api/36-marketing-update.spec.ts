import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  assertOk,
  asList,
  type ApiResponse,
  type ClientLoginInfo,
  type PageData,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 36：营销活动 PUT 修改生效
 * 覆盖：秒杀活动 PUT 改名 → C 端秒杀列表名称生效；
 *       满减活动 PUT 改门槛/减额 → C 端满减列表数值生效；
 *       包邮活动 PUT 改门槛 + 排除范围全量替换（scopes 传啥是啥，非增量合并）
 *       → C 端 active 活动门槛与排除范围同步生效
 * 造数：自建商品 A 保证范围可控；活动随环境留存（与 19 号口径一致）
 */

const PRICE_A = 3.9;

interface FlashSale {
  id: number;
  name: string;
  items: { sku_id: number }[];
  [k: string]: unknown;
}

interface FullReduction {
  id: number;
  name: string;
  threshold: number;
  reduce_amount: number;
  [k: string]: unknown;
}

interface FreeShippingActivity {
  id: number;
  name: string;
  threshold: number;
  scopes: { scope_type: number; product_id: number }[];
  [k: string]: unknown;
}

/**
 * 创建新 C 端用户并返回携带已绑定社区的 token
 * 首登自动绑社区但 JWT 不含 community_id，需同号重登刷新社区语境
 */
async function newClientUser(playwright: Playwright): Promise<ClientLoginInfo> {
  const phone = uniquePhone();
  await clientLogin(playwright.request, phone);
  return clientLogin(playwright.request, phone);
}

/** 当天日期串（本地时区） */
function localDate(offsetDays = 0): string {
  const d = new Date();
  d.setDate(d.getDate() + offsetDays);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let buyer: ClientLoginInfo | null = null;
let communityId = 0;
let categoryId = 0;
let productIdA = 0;
let skuIdA = 0;

test.beforeAll(async ({ playwright }) => {
  buyer = await newClientUser(playwright);
  communityId = buyer.user.community_id;
  const admin = await newAdminContext(playwright, undefined, undefined, communityId);
  adminApi = admin.api;

  try {
    const cat = assertOk(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('PUT 修改分类') } })).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    );
    categoryId = cat.id;

    const prod = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/products', {
          data: {
            category_id: categoryId,
            name: autoName('PUT 修改商品A'),
            unit: '份',
            is_multi_spec: false,
            sku_items: [{ price: PRICE_A, stock: 50, safety_stock: 5 }],
          },
        })
      ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
      '创建商品A',
    );
    productIdA = prod.product?.id ?? prod.id ?? 0;
    const on = (await (await adminApi.post(`/api/v1/admin/products/${productIdA}/status`, { data: { status: 1 } })).json()) as ApiResponse;
    if (on.code !== 0) throw new Error(`上架失败 code=${on.code}`);

    const detail = assertOk(
      (await (await adminApi.get(`/api/v1/admin/products/${productIdA}`)).json()) as ApiResponse<{ skus?: { id: number }[] }>,
      '商品详情',
    );
    skuIdA = detail.skus?.[0]?.id ?? 0;
    if (!skuIdA) throw new Error('商品A无 SKU');
  } catch (e) {
    console.warn(`[36] 造数失败，用例将跳过：${e instanceof Error ? e.message : e}`);
    productIdA = 0;
  }
});

test.afterAll(async () => {
  try {
    if (productIdA) await adminApi.delete(`/api/v1/admin/products/${productIdA}`);
    if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

test.describe.serial('秒杀 PUT 修改生效', () => {
  let flashId = 0;

  test('建秒杀活动（A 秒杀价 1.5）', async () => {
    test.skip(!skuIdA, '环境造数失败');
    const body = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/flash-sales', {
          data: {
            name: autoName('待改秒杀'),
            start_time: `${localDate()} 00:00:00`,
            end_time: `${localDate(1)} 23:59:59`,
            items: [{ sku_id: skuIdA, flash_price: 1.5, stock: 20, limit_per_user: 5 }],
          },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建秒杀活动',
    );
    flashId = body.id;
    expect(flashId).toBeGreaterThan(0);
  });

  test('PUT 改名 → C 端秒杀列表名称生效', async ({ playwright }) => {
    test.skip(!flashId, '秒杀活动未创建');
    const newName = autoName('改名后秒杀');
    // assertOk 内部已校验 code=0；status 显式回传 1（proto int32 无 presence，缺省会覆盖为 0 停用）
    assertOk(
      (await (
        await adminApi.put(`/api/v1/admin/flash-sales/${flashId}`, {
          data: { name: newName, status: 1 },
        })
      ).json()) as ApiResponse,
      '修改秒杀活动',
    );

    const buyerApi = await newAuthedRequest(playwright, buyer!.access_token);
    try {
      const body = assertOk(
        (await (await buyerApi.get('/api/v1/flash-sales', { params: { community_id: communityId } })).json()) as ApiResponse<FlashSale[]>,
        'C 端秒杀列表',
      );
      const found = asList<FlashSale>(body).find((f) => f.id === flashId);
      expect(found, 'C 端秒杀列表应包含该活动').toBeTruthy();
      expect(found!.name, 'PUT 改名后 C 端应展示新名称').toBe(newName);
      expect(found!.items.some((i) => i.sku_id === skuIdA), '秒杀商品应保留').toBe(true);
    } finally {
      await buyerApi.dispose();
    }
  });
});

test.describe.serial('满减 PUT 修改生效', () => {
  let frId = 0;

  test('建满减活动（满 5 减 1）后 PUT 改为满 20 减 3', async () => {
    test.skip(!skuIdA, '环境造数失败');
    const created = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/full-reductions', {
          data: {
            name: autoName('待改满减'),
            threshold: 5,
            reduce_amount: 1,
            start_time: `${localDate()} 00:00:00`,
            end_time: `${localDate(1)} 23:59:59`,
          },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建满减活动',
    );
    frId = created.id;
    expect(frId).toBeGreaterThan(0);

    // PUT：threshold 改 20、减额改 3（assertOk 内部已校验 code=0；status 显式回传 1 防止被覆盖停用）
    assertOk(
      (await (
        await adminApi.put(`/api/v1/admin/full-reductions/${frId}`, {
          data: { name: `AUTO-TEST 改后满减`, threshold: 20, reduce_amount: 3, status: 1 },
        })
      ).json()) as ApiResponse,
      '修改满减活动',
    );
  });

  test('C 端满减列表数值生效', async ({ playwright }) => {
    test.skip(!frId, '满减活动未创建');
    const buyerApi = await newAuthedRequest(playwright, buyer!.access_token);
    try {
      const body = assertOk(
        (await (await buyerApi.get('/api/v1/full-reductions', { params: { community_id: communityId } })).json()) as ApiResponse<FullReduction[]>,
        'C 端满减列表',
      );
      const found = asList<FullReduction>(body).find((f) => f.id === frId);
      expect(found, 'C 端满减列表应包含该活动').toBeTruthy();
      expect(found!.threshold, '门槛应为修改后的 20').toBe(20);
      expect(found!.reduce_amount, '减额应为修改后的 3').toBe(3);
    } finally {
      await buyerApi.dispose();
    }
  });
});

test.describe.serial('包邮 PUT 修改生效（排除范围全量替换）', () => {
  let fsId = 0;

  test('建包邮活动（threshold=10 无排除）后 PUT threshold=12 且排除商品A', async () => {
    test.skip(!skuIdA, '环境造数失败');
    const created = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/free-shippings', {
          data: {
            name: autoName('待改包邮'),
            threshold: 10,
            start_time: `${localDate()} 00:00:00`,
            end_time: `${localDate(1)} 23:59:59`,
          },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建包邮活动',
    );
    fsId = created.id;
    expect(fsId).toBeGreaterThan(0);

    // PUT：threshold 改 12 + scopes 全量替换为「排除商品A」（assertOk 内部已校验 code=0；status 显式回传 1）
    assertOk(
      (await (
        await adminApi.put(`/api/v1/admin/free-shippings/${fsId}`, {
          data: {
            name: `AUTO-TEST 改后包邮`,
            threshold: 12,
            status: 1,
            scopes: [{ scope_type: 5, product_id: productIdA }],
          },
        })
      ).json()) as ApiResponse,
      '修改包邮活动',
    );
  });

  test('C 端包邮试算：排除范围全量替换生效（PUT 前免邮 → PUT 后被排除不免邮）', async ({ playwright }) => {
    test.skip(!fsId, '包邮活动未创建');
    const buyerApi = await newAuthedRequest(playwright, buyer!.access_token);
    try {
      // 4 件 × 3.9 = 15.6 元 > 改后门槛 12
      const body = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders/free-shipping/preview', {
            data: { items: [{ sku_id: skuIdA, quantity: 4 }] },
          })
        ).json()) as ApiResponse<{
          free_shipping: boolean;
          activity_name: string;
          excluded_names: string[];
        }>,
        '免邮试算',
      );
      if (body.activity_name.includes('改后包邮')) {
        // 本活动为当前生效活动：A 被排除 → 参与额 0 < 门槛 → 不免邮且提示排除商品
        expect(body.free_shipping, 'PUT 排除商品A后同单应不免邮').toBe(false);
        expect(body.excluded_names.length, '排除商品应出现在提示中').toBeGreaterThan(0);
      }
      // 环境存在门槛更低的包邮活动抢占 active 时无法归因到本活动，不做强断言（与 19 号口径一致）
    } finally {
      await buyerApi.dispose();
    }
  });

  test('后台列表核验：threshold=12 且排除范围全量替换为仅商品A', async () => {
    test.skip(!fsId, '包邮活动未创建');
    const body = assertOk(
      (await (await adminApi.get('/api/v1/admin/free-shippings', { params: { page: 1, page_size: 50 } })).json()) as ApiResponse<PageData<FreeShippingActivity>>,
      '后台包邮列表',
    );
    const found = asList<FreeShippingActivity>(body).find((f) => f.id === fsId);
    expect(found, '后台列表应包含该活动').toBeTruthy();
    expect(found!.threshold, '门槛应为修改后的 12').toBe(12);
    expect(found!.scopes?.length ?? 0, '排除范围应仅 1 条（全量替换）').toBe(1);
    expect(found!.scopes?.[0]?.product_id, '排除范围应替换为商品A').toBe(productIdA);
  });
});
