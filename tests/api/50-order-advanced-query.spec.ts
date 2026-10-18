import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  pickSku,
  ensureStock,
  assertOk,
  asList,
  type ApiResponse,
  type PageData,
  type ClientLoginInfo,
  type SkuRef,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 50：订单高级查询
 * 覆盖（管理端 GET /admin/orders）：
 *   - receiver_name 模糊匹配（contact_name LIKE %x%）
 *   - receiver_phone 模糊匹配（contact_phone LIKE %x%，后 4 位唯一命中）
 *   - user_id 会员过滤（会员弹窗查单口径：只含该会员订单）
 *   - start_time/end_time 日期范围（YYYY-MM-DD 闭区间，end 自动补 23:59:59）
 *   - 组合条件（user_id + receiver_phone）取交集
 * 覆盖（C 端 GET /orders）：
 *   - 不传 community_id：返回该用户全部订单（跨社区口径，2026-10-15 确认）
 *   - 显式传 community_id>0：仅返回该社区订单；传其他社区 ID 时不包含本组单
 * 说明：31 号已覆盖 status 聚合 90/9、source、pickup_code、order_no 前缀；本模块补 receiver/日期/user_id
 */

interface OrderRow {
  id: number;
  order_no: string;
  user_id: number;
  community_id: number;
  status: number;
  [k: string]: unknown;
}

/** 本模块可识别的单据标记（备注不可模糊查询，联系人/手机号作为查询锚点） */
const NAME_A = 'AUTO50收货人甲';
const NAME_B = 'AUTO50收货人乙';
const NAME_MARK = 'AUTO50收货人';
const PHONE_A = '13900005001';
const PHONE_B = '13900005002';
const PHONE_MARK = '1390000500';

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let buyerA: ClientLoginInfo | null = null;
let buyerB: ClientLoginInfo | null = null;
let sku: SkuRef | null = null;
/** 前置单据 ID（跨 describe 共享） */
let orderA1 = 0;
let orderA2 = 0;
let orderB1 = 0;

/**
 * 买家创建收货地址并返回地址 ID
 * - 函数名: createAddress
 * - 参数: buyerApi — 买家请求上下文；name — 联系人；phone — 联系电话
 * - 返回: Promise<number> 地址 ID
 * - 异常: 业务码非 0 时经 assertOk 抛出
 * - 使用示例: const addrId = await createAddress(buyerApi, NAME_A, PHONE_A);
 */
async function createAddress(
  buyerApi: Awaited<ReturnType<typeof newAuthedRequest>>,
  name: string,
  phone: string,
): Promise<number> {
  const body = assertOk(
    (await (
      await buyerApi.post('/api/v1/user/addresses', {
        data: { contact_name: name, contact_phone: phone, detail: 'AUTO-TEST 50 测试路' },
      })
    ).json()) as ApiResponse<{ id?: number }>,
    '创建收货地址',
  );
  return body.id ?? 0;
}

/**
 * 买家下配送单并现金支付（配送单写入地址联系人/电话，供 receiver_* 查询锚定）
 * - 函数名: createPaidDeliveryOrder
 * - 参数: buyerToken — 买家 token；skuRef — SKU；name — 联系人；phone — 联系电话；pay — 是否支付；
 *         extraQty — 在起送门槛折算量上追加的数量（同用户连续多单须错开数量以避免重复下单指纹碰撞）
 * - 返回: Promise<number> 订单 ID
 * - 异常: 业务码非 0 时抛出
 * - 使用示例: const id = await createPaidDeliveryOrder(token, sku, NAME_A, PHONE_A, true);
 */
async function createPaidDeliveryOrder(
  buyerToken: string,
  skuRef: SkuRef,
  name: string,
  phone: string,
  pay: boolean,
  extraQty = 0,
): Promise<number> {
  const buyerApi = await newAuthedRequest(globalThis.__pw, buyerToken);
  try {
    const addrId = await createAddress(buyerApi, name, phone);
    // 配送单 15 元起送门槛，按单价折算数量
    const skuResp = (await (await buyerApi.get(`/api/v1/skus/${skuRef.id}`)).json()) as ApiResponse<{ price: number }>;
    const price = skuResp.data?.price ?? 0;
    const qty = (price > 0 ? Math.max(Math.ceil(15 / price), 1) : 1) + extraQty;
    const created = assertOk(
      (await (
        await buyerApi.post('/api/v1/orders', {
          data: {
            delivery_type: 2,
            address_id: addrId,
            remark: 'AUTO-TEST 50 高级查询',
            items: [{ sku_id: skuRef.id, quantity: qty }],
          },
        })
      ).json()) as ApiResponse<{ id: number }>,
      'C 端下单',
    );
    if (pay) {
      const paid = (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: created.id, channel: 'cash' } })
      ).json()) as ApiResponse;
      expect(paid.code, `现金支付失败: ${paid.message}`).toBe(0);
    }
    return created.id;
  } finally {
    await buyerApi.dispose();
  }
}

/**
 * 管理端订单列表查询
 * - 函数名: listAdminOrders
 * - 参数: query — URL 查询串（不含 ?）
 * - 返回: Promise<OrderRow[]> 列表行
 * - 异常: 业务码非 0 时经 assertOk 抛出
 * - 使用示例: const rows = await listAdminOrders(`receiver_name=${NAME_MARK}`);
 */
async function listAdminOrders(query: string): Promise<OrderRow[]> {
  const body = (await (
    await adminApi.get(`/api/v1/admin/orders?${query}`)
  ).json()) as ApiResponse<PageData<OrderRow>>;
  const page = assertOk(body, `管理端订单查询(${query})`);
  return asList<OrderRow>(page);
}

test.beforeAll(async ({ playwright }) => {
  globalThis.__pw = playwright;
  buyerA = await clientLogin(playwright.request, uniquePhone());
  buyerB = await clientLogin(playwright.request, uniquePhone());
  const admin = await newAdminContext(playwright, undefined, undefined, buyerA.user.community_id);
  adminApi = admin.api;
  try {
    sku = await pickSku(adminApi, undefined, buyerA.user.community_id);
    await ensureStock(adminApi, sku.id, 200);
  } catch {
    sku = null;
  }
});

test.afterAll(async () => {
  await adminApi?.dispose();
  delete (globalThis as Record<string, unknown>).__pw;
});

test.describe.serial('管理端订单高级查询', () => {
  test('1. 前置：创建三个订单（甲×2 支付、乙×1 未支付）', async () => {
    test.skip(!sku, '环境无库存 SKU');
    orderA1 = await createPaidDeliveryOrder(buyerA!.access_token, sku!, NAME_A, PHONE_A, true);
    // extraQty=1：与 orderA1 同用户同 SKU 背靠背下单，错开数量避免重复下单指纹（5s 窗口）碰撞
    orderA2 = await createPaidDeliveryOrder(buyerA!.access_token, sku!, NAME_A, PHONE_A, true, 1);
    orderB1 = await createPaidDeliveryOrder(buyerB!.access_token, sku!, NAME_B, PHONE_B, false);
    expect(orderA1).toBeGreaterThan(0);
    expect(orderA2).toBeGreaterThan(0);
    expect(orderB1).toBeGreaterThan(0);
  });

  test('2. receiver_name 模糊匹配：公共标记命中 2 单，唯一子串命中 1 单', async () => {
    test.skip(!sku || !orderA1, '前置数据未创建');
    const rowsMark = await listAdminOrders(`receiver_name=${encodeURIComponent(NAME_MARK)}`);
    const idsMark = rowsMark.map((r) => r.id);
    expect(idsMark).toContain(orderA1);
    expect(idsMark).toContain(orderA2);
    expect(idsMark).toContain(orderB1);

    // 「乙」只出现在乙的联系人中，命中且仅命中乙单
    const rowsB = await listAdminOrders(`receiver_name=${encodeURIComponent('收货人乙')}`);
    const idsB = rowsB.map((r) => r.id);
    expect(idsB).toContain(orderB1);
    expect(idsB).not.toContain(orderA1);
    expect(idsB).not.toContain(orderA2);
  });

  test('3. receiver_phone 模糊匹配：后 4 位唯一命中', async () => {
    test.skip(!sku || !orderA1, '前置数据未创建');
    // 前 10 位为两单公共前缀
    const rowsMark = await listAdminOrders(`receiver_phone=${PHONE_MARK}`);
    const idsMark = rowsMark.map((r) => r.id);
    expect(idsMark).toContain(orderA1);
    expect(idsMark).toContain(orderA2);

    const rowsA = await listAdminOrders(`receiver_phone=${PHONE_A}`);
    const idsA = rowsA.map((r) => r.id);
    expect(idsA).toContain(orderA1);
    expect(idsA).not.toContain(orderB1);
  });

  test('4. user_id 会员过滤：仅含该会员订单', async () => {
    test.skip(!sku || !orderA1, '前置数据未创建');
    const rowsA = await listAdminOrders(`user_id=${buyerA!.user.id}`);
    const rowsB = await listAdminOrders(`user_id=${buyerB!.user.id}`);
    for (const row of rowsA) expect(row.user_id, 'user_id 过滤应严格匹配').toBe(buyerA!.user.id);
    const idsA = rowsA.map((r) => r.id);
    expect(idsA).toContain(orderA1);
    expect(idsA).toContain(orderA2);
    expect(idsA, '甲的查询不应包含乙单').not.toContain(orderB1);
    const idsB = rowsB.map((r) => r.id);
    expect(idsB).toContain(orderB1);
    expect(idsB).not.toContain(orderA1);
  });

  test('5. 日期范围 YYYY-MM-DD 闭区间', async () => {
    test.skip(!sku || !orderA1, '前置数据未创建');
    const today = new Date();
    const fmt = (d: Date): string =>
      `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
    const yesterday = new Date(today.getTime() - 24 * 3600 * 1000);

    // 今日闭区间：本组三单均为今日创建，应全部命中
    const rowsToday = await listAdminOrders(`start_time=${fmt(today)}&end_time=${fmt(today)}`);
    const idsToday = rowsToday.map((r) => r.id);
    expect(idsToday).toContain(orderA1);
    expect(idsToday).toContain(orderA2);
    expect(idsToday).toContain(orderB1);

    // end=昨日：本组单不应命中（created_at 为今日）
    const rowsBefore = await listAdminOrders(`start_time=2020-01-01&end_time=${fmt(yesterday)}`);
    const idsBefore = rowsBefore.map((r) => r.id);
    expect(idsBefore).not.toContain(orderA1);
    expect(idsBefore).not.toContain(orderB1);

    // start=明日：同样不命中
    const tomorrow = new Date(today.getTime() + 24 * 3600 * 1000);
    const rowsAfter = await listAdminOrders(`start_time=${fmt(tomorrow)}`);
    expect(rowsAfter.map((r) => r.id)).not.toContain(orderA1);
  });

  test('6. 组合条件 user_id + receiver_phone 取交集', async () => {
    test.skip(!sku || !orderA1, '前置数据未创建');
    // 甲的 user_id + 乙的联系电话 → 交集为空
    const rowsEmpty = await listAdminOrders(`user_id=${buyerA!.user.id}&receiver_phone=${PHONE_B}`);
    expect(rowsEmpty.map((r) => r.id)).not.toContain(orderA1);
    expect(rowsEmpty.map((r) => r.id)).not.toContain(orderB1);
    // 甲的 user_id + 甲的联系电话 → 命中甲单
    const rowsHit = await listAdminOrders(`user_id=${buyerA!.user.id}&receiver_phone=${PHONE_A}`);
    const idsHit = rowsHit.map((r) => r.id);
    expect(idsHit).toContain(orderA1);
    expect(idsHit).not.toContain(orderB1);
  });
});

test.describe.serial('C 端订单列表社区过滤', () => {
  test('7. 不传 community_id 返回全部订单；显式传本社区/他社区按社区过滤', async ({ playwright }) => {
    test.skip(!sku || !orderA1, '前置数据未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerA!.access_token);
    try {
      // 不传：返回该用户全部订单（含本组两单）
      const all = assertOk(
        (await (await buyerApi.get('/api/v1/orders')).json()) as ApiResponse<PageData<OrderRow>>,
        'C 端订单列表(不传 community_id)',
      );
      const allIds = asList<OrderRow>(all).map((r) => r.id);
      expect(allIds).toContain(orderA1);
      expect(allIds).toContain(orderA2);

      // 显式传本社区：本组单均属该社区，仍命中
      const own = assertOk(
        (await (
          await buyerApi.get(`/api/v1/orders?community_id=${buyerA!.user.community_id}`)
        ).json()) as ApiResponse<PageData<OrderRow>>,
        'C 端订单列表(本社区)',
      );
      const ownIds = asList<OrderRow>(own).map((r) => r.id);
      expect(ownIds).toContain(orderA1);
      expect(ownIds).toContain(orderA2);

      // 显式传其他社区（>0）：不包含本组单
      const other = assertOk(
        (await (await buyerApi.get('/api/v1/orders?community_id=999999')).json()) as ApiResponse<
          PageData<OrderRow>
        >,
        'C 端订单列表(他社区)',
      );
      const otherIds = asList<OrderRow>(other).map((r) => r.id);
      expect(otherIds).not.toContain(orderA1);
      expect(otherIds).not.toContain(orderA2);
    } finally {
      await buyerApi.dispose();
    }
  });
});

declare global {
  // eslint-disable-next-line no-var
  var __pw: Playwright;
}
