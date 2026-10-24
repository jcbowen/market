import { test, expect, type Playwright, type APIRequestContext } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  pickSku,
  assertOk,
  type ApiResponse,
  type SkuRef,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 77：C 端订单「再来一单」
 * 口径（2026-10-24）：
 *   - 仅用户本人的终态订单（已完成 5 / 已取消 6 / 已退款 8）可复购；
 *   - 门店单（source=2，收银台代客）C 端只读，不提供复购入口；
 *   - 按订单明细逐项加入订单所属社区购物车，数量取原订单数量；
 *   - 商品下架/规格失效等逐项跳过，全部失败返回 10004 并汇总原因。
 * 数据惯例：本模块创建的订单均为「下单 → 取消」或收银台到店单，不删除历史数据；
 *   下架用例临时改动环境商品状态，用 finally 恢复上架，workers=1 串行执行不影响其他模块。
 */

/** 订单状态常量，与 model/order.go 对齐 */
const ORDER_STATUS = { pending: 1, canceled: 6 } as const;

/** 业务错误码 */
const ORDER_STATUS_NOT_ALLOWED = 13002;
const OPERATION_NOT_ALLOWED = 10004;
const NOT_FOUND = 10003;
const NO_PERMISSION = 11005;

interface OrderInfo {
  id: number;
  order_no?: string;
  status: number;
  source?: number;
  community_id?: number;
}

/** 再来一单返回结构（gateway OrderHandler.Reorder） */
interface ReorderResult {
  added: number;
  skipped: { product_name: string; spec_name: string; reason: string }[];
}

interface CartItem {
  sku_id: number;
  quantity: number;
  community_id: number;
  invalid?: boolean;
}

let api: APIRequestContext;
let adminApi: APIRequestContext;
let userId = 0;
let communityId = 0;
let sku: SkuRef;

/**
 * 创建一笔自提订单（C 端，待支付）
 * - 函数名: createOrder
 * - 参数: quantity — 购买数量（错开重复下单指纹窗口）
 * - 返回: Promise<OrderInfo>，含订单 ID/状态
 * - 异常: 下单业务失败时抛出 Error
 * - 使用示例: const order = await createOrder(1);
 */
async function createOrder(quantity = 1): Promise<OrderInfo> {
  const resp = await api.post('/api/v1/orders', {
    data: { delivery_type: 1, address_id: 0, items: [{ sku_id: sku.id, quantity }] },
  });
  const body = (await resp.json()) as ApiResponse<OrderInfo>;
  if (body.code !== 0) throw new Error(`创建订单失败 code=${body.code} message=${body.message}`);
  return body.data as OrderInfo;
}

/**
 * 取消订单（把待支付单转为终态 6 已取消）
 * - 函数名: cancelOrder
 * - 参数: id — 订单 ID
 * - 返回: Promise<void>
 * - 异常: 取消失败时抛出 Error
 * - 使用示例: await cancelOrder(order.id);
 */
async function cancelOrder(id: number): Promise<void> {
  const body = (await (await api.put(`/api/v1/orders/${id}/cancel`)).json()) as ApiResponse;
  if (body.code !== 0) throw new Error(`取消订单失败 code=${body.code} message=${body.message}`);
}

/**
 * 调用再来一单接口（业务码交由调用方断言）
 * - 函数名: reorder
 * - 参数: id — 订单 ID
 * - 返回: Promise<ApiResponse<ReorderResult>>（HTTP 恒 200，业务码在 body.code）
 * - 异常: 无
 * - 使用示例: const body = await reorder(order.id);
 */
async function reorder(id: number): Promise<ApiResponse<ReorderResult>> {
  return (await (await api.post(`/api/v1/orders/${id}/reorder`)).json()) as ApiResponse<ReorderResult>;
}

/**
 * 读取当前用户购物车全部商品（跨社区）
 * - 函数名: getCartItems
 * - 参数: 无
 * - 返回: Promise<CartItem[]>
 * - 异常: 查询业务失败时经 assertOk 抛出
 * - 使用示例: const items = await getCartItems();
 */
async function getCartItems(): Promise<CartItem[]> {
  const body = (await (await api.get('/api/v1/cart')).json()) as ApiResponse<{ items: CartItem[] }>;
  return assertOk(body, '购物车查询').items ?? [];
}

/**
 * 取指定 SKU 在购物车中的行
 * - 函数名: findCartRow
 * - 参数: items — 购物车商品列表；skuId — SKU ID
 * - 返回: CartItem | undefined（未加入时返回 undefined）
 * - 异常: 无
 * - 使用示例: const row = findCartRow(items, sku.id);
 */
function findCartRow(items: CartItem[], skuId: number): CartItem | undefined {
  return items.find((i) => i.sku_id === skuId);
}

/**
 * 结束遗留班次后重新开工（到店单必须由收银台产生，需进行中班次）
 * - 函数名: openShift
 * - 参数: 无
 * - 返回: Promise<void>
 * - 异常: 开工业务失败时抛出 Error
 * - 使用示例: await openShift();
 */
async function openShift(): Promise<void> {
  const cur = (await (await adminApi.get('/api/v1/admin/pos/shifts/current')).json()) as ApiResponse<{ id?: number }>;
  if ((cur.data?.id ?? 0) > 0) {
    await adminApi.post('/api/v1/admin/pos/shifts/end', { data: { note: autoName('77 清理遗留班次') } });
  }
  const start = (await (
    await adminApi.post('/api/v1/admin/pos/shifts/start', { data: { note: autoName('77 开工') } })
  ).json()) as ApiResponse<{ id?: number }>;
  expect(start.code, `开工失败: ${start.message}`).toBe(0);
}

/**
 * 在 C 端订单列表中查找指定订单（用例 1 校验取消结果）
 * - 函数名: findInList
 * - 参数: id — 订单 ID
 * - 返回: Promise<OrderInfo | undefined>
 * - 异常: 列表查询失败时抛出 Error
 * - 使用示例: await findInList(order.id);
 */
async function findInList(id: number): Promise<OrderInfo | undefined> {
  const body = (await (
    await api.get('/api/v1/orders', { params: { page: 1, page_size: 100 } })
  ).json()) as ApiResponse<{ list: OrderInfo[] }>;
  if (body.code !== 0) throw new Error(`订单列表查询失败 code=${body.code} message=${body.message}`);
  return (body.data?.list ?? []).find((o) => o.id === id);
}

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  api = await newAuthedRequest(playwright, info.access_token);
  userId = info.user.id;
  communityId = info.user.community_id;
  sku = await pickSku(api, undefined, communityId);

  adminApi = (await newAdminContext(playwright, undefined, undefined, communityId)).api;
});

test.afterAll(async () => {
  await api?.dispose();
  await adminApi?.dispose();
});

test.describe.serial('C 端再来一单', () => {
  /** 用例 2 创建的已取消订单，用例 3/5 复用（避免小窗口内同 SKU 同数量重复下单被指纹拦截） */
  let canceledOrderId = 0;

  test('1. 待支付订单不可再来一单', async () => {
    const pending = await createOrder(1);
    const body = await reorder(pending.id);
    expect(body.code, '待支付订单应拒绝复购').toBe(ORDER_STATUS_NOT_ALLOWED);
    // 清理：取消该单，避免遗留待支付单影响后续轮次
    await cancelOrder(pending.id);
    expect((await findInList(pending.id))?.status).toBe(ORDER_STATUS.canceled);
  });

  test('2. 已取消订单再来一单成功，商品按原数量进入订单所属社区购物车', async () => {
    const order = await createOrder(2);
    await cancelOrder(order.id);
    canceledOrderId = order.id;

    const body = await reorder(order.id);
    expect(body.code, `终态订单复购失败: ${body.message}`).toBe(0);
    expect(body.data?.added, '应成功加入 1 个商品项').toBe(1);
    expect(body.data?.skipped ?? [], '在售商品不应被跳过').toHaveLength(0);

    const row = findCartRow(await getCartItems(), sku.id);
    expect(row, '购物车应出现订单中的 SKU').toBeDefined();
    expect(row?.quantity, '加购数量应等于原订单数量').toBe(2);
    expect(row?.community_id, '应按订单所属社区加购').toBe(order.community_id ?? communityId);
  });

  test('3. 他人订单不可再来一单（归属校验）', async ({ playwright }) => {
    const other = await clientLogin(playwright.request, uniquePhone());
    const otherApi = await newAuthedRequest(playwright, other.access_token);
    try {
      const body = (await (
        await otherApi.post(`/api/v1/orders/${canceledOrderId}/reorder`)
      ).json()) as ApiResponse<ReorderResult>;
      expect([NOT_FOUND, NO_PERMISSION], '跨用户复购应被拒绝（订单按不可见处理）').toContain(body.code);
    } finally {
      await otherApi.dispose();
    }
  });

  test('4. 门店单（source=2）不可再来一单', async () => {
    await openShift();
    try {
      const pos = assertOk<OrderInfo>(
        (await (
          await adminApi.post('/api/v1/admin/pos/orders', {
            data: {
              user_id: userId,
              pay_type: 4,
              delivery_type: 3,
              items: [{ sku_id: sku.id, quantity: 1 }],
              remark: autoName('77 门店单'),
            },
          })
        ).json()) as ApiResponse<OrderInfo>,
        '到店单创建',
      );

      const body = await reorder(pos.id);
      expect(body.code, '门店单应拒绝 C 端复购').toBe(10001);
      expect(body.message).toContain('门店');
    } finally {
      // 交班恢复环境默认（无进行中班次），避免与 14/44/69/72 互踩
      await adminApi.post('/api/v1/admin/pos/shifts/end', { data: { note: autoName('77 收尾交班') } });
    }
  });

  test('5. 商品下架后全部失败：返回 10004 且汇总原因，购物车不变', async () => {
    expect(canceledOrderId, '前置用例应已创建终态订单').toBeGreaterThan(0);
    const before = (await getCartItems()).reduce((sum, i) => sum + i.quantity, 0);

    await adminApi.post(`/api/v1/admin/products/${sku.product_id}/status`, { data: { status: 0 } });
    try {
      const body = await reorder(canceledOrderId);
      expect(body.code, '商品全部失效应返回业务错误').toBe(OPERATION_NOT_ALLOWED);
      expect(body.message, '错误文案应汇总无法购买的商品').toContain('无法购买');
      expect((await getCartItems()).reduce((sum, i) => sum + i.quantity, 0), '失败不应改变购物车').toBe(before);
    } finally {
      await adminApi.post(`/api/v1/admin/products/${sku.product_id}/status`, { data: { status: 1 } });
    }
  });
});

