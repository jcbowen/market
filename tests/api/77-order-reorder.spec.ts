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

/** 用例 6 自建多规格商品的造数返回结构（字段口径同 22 号用例，仅声明所需部分） */
interface CreatedSku {
  id: number;
  product_id: number;
  spec_value_ids: string;
  price: number;
  status: number;
}

interface CreatedSpecValue {
  id: number;
  name: string;
}

interface CreatedSpec {
  id: number;
  name: string;
  values: CreatedSpecValue[];
}

interface CreatedMultiSpecProduct {
  product: { id: number };
  skus: CreatedSku[];
  specs: CreatedSpec[];
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

  /**
   * 回归：单个规格失效不得阻断整单复购
   * 商品服务的 BatchGetSkus 是下单交易口径（任一明细失效即整体拒绝），复购若沿用该口径，
   * 一个被移除的规格会让同单其余可售商品全部加不进购物车；
   * 复购须走 tolerate_missing 口径：失效项逐项剔除并给出原因，其余照常加购。
   * 造数全部自建（分类 + 1 规格 × 2 规格值商品），结束即删，不改动环境商品
   */
  test('6. 部分规格失效：可售项照常加购，失效项逐项跳过', async () => {
    const categoryId = assertOk<{ id: number }>(
      (await (
        await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('77 复购分类') } })
      ).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    ).id;
    const productName = autoName('77 复购多规格');
    let productId = 0;

    try {
      const detail = assertOk<CreatedMultiSpecProduct>(
        (await (
          await adminApi.post('/api/v1/admin/products', {
            data: {
              category_id: categoryId,
              name: productName,
              unit: '份',
              is_multi_spec: true,
              specs: [
                {
                  name: '份量',
                  sort: 1,
                  values: [
                    { temp_id: 'v1', name: '小份', sort: 1 },
                    { temp_id: 'v2', name: '大份', sort: 2 },
                  ],
                },
              ],
              sku_items: [
                { spec_value_ids: ['v1'], price: 8.8, stock: 20, safety_stock: 1 },
                { spec_value_ids: ['v2'], price: 12.8, stock: 20, safety_stock: 1 },
              ],
            },
          })
        ).json()) as ApiResponse<CreatedMultiSpecProduct>,
        '创建多规格商品',
      );
      productId = detail.product.id;
      expect(detail.skus.length, '两个规格值应生成 2 个 SKU').toBe(2);

      // 按价格区分保留/移除的规格，避免依赖 spec_value_ids 的拼接顺序
      const keepSku = detail.skus.find((k) => k.price === 8.8)!;
      const dropSku = detail.skus.find((k) => k.price === 12.8)!;
      expect(keepSku.id, '应取到「小份」SKU').toBeGreaterThan(0);
      expect(dropSku.id, '应取到「大份」SKU').toBeGreaterThan(0);

      const on = (await (
        await adminApi.post(`/api/v1/admin/products/${productId}/status`, { data: { status: 1 } })
      ).json()) as ApiResponse;
      expect(on.code, `上架失败: ${on.message}`).toBe(0);

      // 下单含两个规格各 1 件，随即取消转为终态（不占用环境库存）
      const order = assertOk<OrderInfo>(
        (await (
          await api.post('/api/v1/orders', {
            data: {
              delivery_type: 1,
              address_id: 0,
              items: [{ sku_id: keepSku.id, quantity: 1 }, { sku_id: dropSku.id, quantity: 1 }],
            },
          })
        ).json()) as ApiResponse<OrderInfo>,
        '创建两规格订单',
      );
      await cancelOrder(order.id);

      // 移除「大份」规格：商品保持上架，仅该 SKU 被 saveSpecsAndSkus 的 diff 置为停用（status=0）
      const updateBody = (await (
        await adminApi.put(`/api/v1/admin/products/${productId}`, {
          data: {
            category_id: categoryId,
            name: productName,
            unit: '份',
            status: 1,
            is_multi_spec: true,
            specs: [
              {
                id: detail.specs[0].id,
                name: '份量',
                sort: 1,
                values: [{ id: detail.specs[0].values[0].id, name: '小份', sort: 1 }],
              },
            ],
            sku_items: [
              { id: keepSku.id, spec_value_ids: keepSku.spec_value_ids.split(','), price: 8.8, stock: 20, safety_stock: 1 },
            ],
          },
        })
      ).json()) as ApiResponse;
      expect(updateBody.code, `移除规格失败: ${updateBody.message}`).toBe(0);

      const body = await reorder(order.id);
      expect(body.code, `单个规格失效不应阻断复购: ${body.message}`).toBe(0);
      expect(body.data?.added, '可售规格应加入 1 项').toBe(1);
      expect(body.data?.skipped?.length, '失效规格应逐项跳过 1 项').toBe(1);
      expect(body.data?.skipped?.[0]?.reason, '跳过原因应指向规格失效/停售').toContain('规格已');

      const items = await getCartItems();
      expect(findCartRow(items, keepSku.id)?.quantity, '可售规格应按原数量加购').toBe(1);
      expect(findCartRow(items, dropSku.id), '失效规格不应进入购物车').toBeUndefined();
    } finally {
      if (productId) await adminApi.delete(`/api/v1/admin/products/${productId}`);
      await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
    }
  });
});

