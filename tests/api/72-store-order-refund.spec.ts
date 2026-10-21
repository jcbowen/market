import { test, expect, type Playwright } from '@playwright/test';
import {
  newAdminContext,
  clientLogin,
  newAuthedRequest,
  pickSku,
  ensureStock,
  assertOk,
  asList,
  type ApiResponse,
  type SkuRef,
} from '../helpers/api';
import { autoName, uniquePhone } from '../helpers/data';

/**
 * 模块 72：到店单退款（门店售出订单的售后闭环）
 * 背景：到店单（delivery_type=3，收银台即买即走）收款时在同一事务内被直接终态化为已完成(5)，
 *   而代客退款的状态门禁原先只允许 2/3/4，导致门店售出的订单永远退不了款、
 *   班次退款统计对到店单形同虚设（收银台前端却已按「已完成可退」渲染按钮，点了必报错）。
 * 覆盖：
 *   - 到店现金单收款即完结(5) 的现状锁定（无「已支付」停留态）
 *   - 整单代客退款成功：退款单落已退款(5)、类型整单退(1)，订单落已退款(8)，流转日志 5→7→8
 *   - 退款后库存回补与销量回退（净成交口径，与 68 号一致）
 *   - 退款列表可见且金额等于订单实付
 *   - 部分退款后订单回到已完成(5) 而非已支付(2)（锁 lastStatusBeforeRefundTx 的 from=5 分支）
 *   - 口径边界：自提单已完成(5) 仍不可退（本轮只放开到店单，未扩张到全部已完成单）
 *   - C 端入口不放开：到店单（source=2）C 端申请退款仍被拒「门店订单请联系门店处理」
 * 数据惯例：POS 单与退款单无删除接口，本轮按「只增不删」留档，备注统一带 AUTO-TEST 与模块号；
 *   不新建商品，复用环境在售 SKU 并补足库存，销量/库存以「相对基线的增量」断言，避免受既有数据影响。
 * 说明：用例 6 会留下一张已完成且不可退的自提单，属该用例的必要产物，随备注可人工识别。
 */

/** 业务参数错误码（服务层 InvalidArgument 与网关 binding 同口径） */
const PARAM_ERROR = 10001;

/** 订单状态常量，与 model/order.go 及前端 constants/order.ts 对齐 */
const ORDER_STATUS = { paid: 2, completed: 5, refunding: 7, refunded: 8 } as const;

/** 退款单状态：5=已退款（成功） */
const REFUND_DONE = 5;

interface StatusLog {
  from_status: number;
  to_status: number;
  remark?: string;
}

interface OrderItem {
  id?: number;
  sku_id?: number;
  quantity?: number;
}

interface AdminOrderDetail {
  id?: number;
  order_no?: string;
  status?: number;
  status_text?: string;
  delivery_type?: number;
  source?: number;
  pay_amount?: number;
  shift_id?: number;
  pay_time?: string;
  complete_time?: string;
  status_logs?: StatusLog[];
  items?: OrderItem[];
}

interface RefundRow {
  id?: number;
  order_id?: number;
  refund_no?: string;
  amount?: number;
  status?: number;
  type?: number;
}

interface PosOrderResp {
  id: number;
  order_no: string;
  pay_amount: number;
  paid: boolean;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let communityId = 0;
let sku: SkuRef | null = null;

/**
 * 创建到店现金单（收银台代客，pay_type=4 现金、delivery_type=3 到店）
 * 收款在同一事务内完成，订单直接落已完成(5)
 * - 函数名: createStoreOrder
 * - 参数: quantity — 购买件数；userId — 会员 ID（0 表示散客）；remark — 备注
 * - 返回: Promise<PosOrderResp> 建单响应（含订单 ID 与实付金额）
 * - 异常: 建单业务失败时经 assertOk 抛出
 * - 使用示例: const order = await createStoreOrder(2, 0, 'AUTO-TEST 72');
 */
async function createStoreOrder(quantity: number, userId: number, remark: string): Promise<PosOrderResp> {
  return assertOk<PosOrderResp>(
    (await (
      await adminApi.post('/api/v1/admin/pos/orders', {
        data: {
          user_id: userId,
          pay_type: 4,
          delivery_type: 3,
          items: [{ sku_id: sku!.id, quantity }],
          remark,
        },
      })
    ).json()) as ApiResponse<PosOrderResp>,
    `到店单创建(${remark})`,
  );
}

/**
 * 读取管理端订单详情
 * - 函数名: orderDetail
 * - 参数: id — 订单 ID
 * - 返回: Promise<AdminOrderDetail>
 * - 异常: 查询业务失败时经 assertOk 抛出
 */
async function orderDetail(id: number): Promise<AdminOrderDetail> {
  return assertOk<AdminOrderDetail>(
    (await (await adminApi.get(`/api/v1/admin/orders/${id}`)).json()) as ApiResponse<AdminOrderDetail>,
    `订单详情(${id})`,
  );
}

/**
 * 发起后台代客退款并返回原始响应（业务码交由调用方断言）
 * - 函数名: adminRefund
 * - 参数: id — 订单 ID；data — 退款请求体（reason 必填，mode/items 可选）
 * - 返回: Promise<ApiResponse<RefundRow>>
 * - 异常: 无（HTTP 恒 200，业务码在 body.code）
 */
async function adminRefund(id: number, data: Record<string, unknown>): Promise<ApiResponse<RefundRow>> {
  return (await (await adminApi.post(`/api/v1/admin/orders/${id}/refund`, { data })).json()) as ApiResponse<RefundRow>;
}

/**
 * 读取指定 SKU 的当前库存（走 C 端公开接口，不依赖管理端汇总列 products.stock）
 * - 函数名: skuStock
 * - 参数: productId — 商品 ID；skuId — SKU ID
 * - 返回: Promise<number> 库存数量
 * - 异常: 商品无 SKU 或查询失败时抛出 Error
 */
async function skuStock(productId: number, skuId: number): Promise<number> {
  const body = (await (
    await adminApi.get('/api/v1/skus', { params: { product_id: productId } })
  ).json()) as ApiResponse<{ id: number; stock: number }[]>;
  const row = (body.data ?? []).find((s) => s.id === skuId);
  if (!row) throw new Error(`SKU ${skuId} 不在商品 ${productId} 的规格列表中`);
  return row.stock;
}

/**
 * 读取商品的净成交销量（sales_count 为原始值，不含 virtual_sales）
 * - 函数名: productSales
 * - 参数: productId — 商品 ID
 * - 返回: Promise<number> 销量（proto 零值也下发，缺失按 0 处理）
 * - 异常: 查询业务失败时经 assertOk 抛出
 */
async function productSales(productId: number): Promise<number> {
  const body = (await (
    await adminApi.get(`/api/v1/admin/products/${productId}`)
  ).json()) as ApiResponse<{ product?: { sales_count?: number } }>;
  return assertOk(body, `商品详情(${productId})`).product?.sales_count ?? 0;
}

/**
 * 取一个绑定到管理端语境社区的 C 端买家（新建会员，必要时改绑社区后重登刷新 JWT）
 * - 函数名: memberIn
 * - 参数: playwright — Playwright 工厂；target — 目标社区 ID
 * - 返回: Promise<{ token: string; userId: number }>
 * - 异常: 登录或改绑失败时抛出 Error
 * - 使用示例: const buyer = await memberIn(playwright, communityId);
 */
async function memberIn(playwright: Playwright, target: number): Promise<{ token: string; userId: number }> {
  const phone = uniquePhone();
  const first = await clientLogin(playwright.request, phone);
  if (first.user.community_id === target) return { token: first.access_token, userId: first.user.id };

  const ctx = await newAuthedRequest(playwright, first.access_token);
  try {
    assertOk(
      (await (await ctx.put('/api/v1/user/community', { data: { community_id: target } })).json()) as ApiResponse,
      `改绑社区(${target})`,
    );
  } finally {
    await ctx.dispose();
  }
  // 改绑发生在登录之后，JWT 内社区仍是旧值，需重登刷新
  const relogin = await clientLogin(playwright.request, phone);
  if (relogin.user.community_id !== target) {
    throw new Error(`买家社区改绑失败：期望 ${target}，实际 ${relogin.user.community_id}`);
  }
  return { token: relogin.access_token, userId: relogin.user.id };
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  communityId = admin.communityId;
  try {
    sku = await pickSku(adminApi, undefined, communityId);
    await ensureStock(adminApi, sku.id, 200);
  } catch {
    sku = null;
  }

  // 到店单必须由收银台产生，需有进行中班次；先关掉遗留班次再从干净班次开始
  const cur = (await (
    await adminApi.get('/api/v1/admin/pos/shifts/current')
  ).json()) as ApiResponse<{ id?: number }>;
  if ((cur.data?.id ?? 0) > 0) {
    const end = (await (
      await adminApi.post('/api/v1/admin/pos/shifts/end', { data: { note: autoName('72 清理遗留班次') } })
    ).json()) as ApiResponse;
    console.log(`[API72] 清理遗留班次 code=${end.code}`);
  }
  const start = (await (
    await adminApi.post('/api/v1/admin/pos/shifts/start', { data: { note: autoName('72 开工') } })
  ).json()) as ApiResponse<{ id?: number }>;
  expect(start.code, `开工失败: ${start.message}`).toBe(0);
});

test.afterAll(async () => {
  try {
    // 收尾交班，恢复环境默认（无进行中班次），避免与 14/44/69 号互踩
    await adminApi?.post('/api/v1/admin/pos/shifts/end', { data: { note: autoName('72 收尾交班') } });
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

test.describe.serial('到店单退款', () => {
  /** 整单退用例的到店单与其基线数据 */
  let fullOrderId = 0;
  let fullPayAmount = 0;
  let fullItemId = 0;
  let stockBefore = 0;
  let salesBefore = 0;
  /** 部分退用例的到店单 */
  let partOrderId = 0;
  let partItemId = 0;

  test('1. 到店现金单收款即完结(5)，记录库存与销量基线', async () => {
    test.skip(!sku, '环境无库存 SKU');
    stockBefore = await skuStock(sku!.product_id, sku!.id);
    salesBefore = await productSales(sku!.product_id);

    const order = await createStoreOrder(2, 0, autoName('72 整单退'));
    fullOrderId = order.id;
    fullPayAmount = order.pay_amount;
    expect(order.paid, '现金到店单建单即收款').toBe(true);

    const detail = await orderDetail(fullOrderId);
    expect(detail.status, '到店单应直接落已完成(5)而非已支付(2)').toBe(ORDER_STATUS.completed);
    expect(detail.status_text).toBe('已完成');
    expect(detail.delivery_type).toBe(3);
    expect(detail.source, '收银台代客单来源应为 2').toBe(2);
    expect(detail.complete_time ?? detail.pay_time, 'complete_time 应非空').toBeTruthy();

    fullItemId = detail.items?.find((i) => i.sku_id === sku!.id)?.id ?? 0;
    expect(fullItemId, '订单缺少目标 SKU 明细行').toBeGreaterThan(0);
    expect(await skuStock(sku!.product_id, sku!.id), '售出 2 件后库存应减少 2').toBe(stockBefore - 2);
    expect(await productSales(sku!.product_id), '售出 2 件后销量应增加 2').toBe(salesBefore + 2);
  });

  test('2. 整单代客退款成功：退款单落已退款，订单落 8 并留 5→7→8 流转', async () => {
    test.skip(fullOrderId <= 0, '到店单未创建');
    const body = await adminRefund(fullOrderId, { reason: autoName('72 到店整单退') });
    expect(body.code, `到店单代客退款失败: ${body.message}`).toBe(0);
    expect(body.data?.status, '发起即执行，退款单应为已退款(5)').toBe(REFUND_DONE);
    expect(body.data?.type, '全额退应为整单退(1)').toBe(1);
    expect(body.data?.amount ?? 0, '退款金额应等于订单实付').toBeCloseTo(fullPayAmount, 2);

    const detail = await orderDetail(fullOrderId);
    expect(detail.status, '整单退款后订单应为已退款(8)').toBe(ORDER_STATUS.refunded);
    const logs = detail.status_logs ?? [];
    expect(
      logs.some((l) => l.from_status === ORDER_STATUS.completed && l.to_status === ORDER_STATUS.refunding),
      '应有 5→7（发起退款）流转日志',
    ).toBe(true);
    expect(
      logs.some((l) => l.from_status === ORDER_STATUS.refunding && l.to_status === ORDER_STATUS.refunded),
      '应有 7→8（退款完成）流转日志',
    ).toBe(true);
  });

  test('3. 退款后库存回补、销量回退到基线', async () => {
    test.skip(fullOrderId <= 0 || !sku, '前置未就绪');
    expect(await skuStock(sku!.product_id, sku!.id), '整单退款应回补 2 件库存').toBe(stockBefore);
    expect(await productSales(sku!.product_id), '整单退款应回退 2 件销量').toBe(salesBefore);
  });

  test('4. 退款列表可见该记录且金额等于实付', async () => {
    test.skip(fullOrderId <= 0, '到店单未创建');
    const body = (await (
      await adminApi.get('/api/v1/admin/refunds', { params: { page: 1, page_size: 100 } })
    ).json()) as ApiResponse<unknown>;
    const rows = asList<RefundRow>(assertOk(body, '退款列表')).filter((r) => r.order_id === fullOrderId);
    expect(rows.length, '退款列表应含到店单的退款记录').toBeGreaterThan(0);
    expect(rows[0].status, '退款单状态应为已退款(5)').toBe(REFUND_DONE);
    expect(rows[0].amount ?? 0, '列表金额与订单实付一致').toBeCloseTo(fullPayAmount, 2);
  });

  test('5. 部分退款后订单回到已完成(5)而非已支付(2)', async () => {
    test.skip(!sku, '环境无库存 SKU');
    const order = await createStoreOrder(3, 0, autoName('72 部分退'));
    partOrderId = order.id;
    const detail = await orderDetail(partOrderId);
    expect(detail.status, '前置：到店单应为已完成(5)').toBe(ORDER_STATUS.completed);
    partItemId = detail.items?.find((i) => i.sku_id === sku!.id)?.id ?? 0;
    expect(partItemId, '订单缺少目标 SKU 明细行').toBeGreaterThan(0);

    const body = await adminRefund(partOrderId, {
      mode: 1,
      reason: autoName('72 到店部分退 1 件'),
      items: [{ order_item_id: partItemId, quantity: 1 }],
    });
    expect(body.code, `到店单部分退款失败: ${body.message}`).toBe(0);
    expect(body.data?.status).toBe(REFUND_DONE);
    expect(body.data?.type, '未退满应为部分退(2)').toBe(2);

    const after = await orderDetail(partOrderId);
    // 关键断言：退款前状态回溯须认 from=5，否则到店单部分退后会被错置为已支付(2)，
    // 后台会把它当作待核销/待发货单处理
    expect(after.status, '部分退款完成后应回到已完成(5)').toBe(ORDER_STATUS.completed);
    expect(
      (after.status_logs ?? []).some(
        (l) => l.from_status === ORDER_STATUS.refunding && l.to_status === ORDER_STATUS.completed,
      ),
      '应有 7→5（部分退款完成回到已完成）流转日志',
    ).toBe(true);
  });

  test('6. 口径边界：自提单已完成(5) 仍不可退', async ({ playwright }) => {
    test.skip(!sku, '环境无库存 SKU');
    const buyer = await memberIn(playwright, communityId);
    const buyerApi = await newAuthedRequest(playwright, buyer.token);
    let pickupOrderId = 0;
    try {
      const created = assertOk<{ id: number }>(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: {
              delivery_type: 1,
              address_id: 0,
              remark: autoName('72 自提已完成'),
              items: [{ sku_id: sku!.id, quantity: 1 }],
            },
          })
        ).json()) as ApiResponse<{ id: number }>,
        'C 端自提下单',
      );
      pickupOrderId = created.id;
      assertOk(
        (await (
          await buyerApi.post('/api/v1/payments', { data: { order_id: pickupOrderId, channel: 'cash' } })
        ).json()) as ApiResponse,
        '现金支付',
      );
    } finally {
      await buyerApi.dispose();
    }

    // 自提单收款后停在已支付(2)，由后台推进到已完成(5)
    assertOk(
      (await (await adminApi.put(`/api/v1/admin/orders/${pickupOrderId}/complete`)).json()) as ApiResponse,
      '后台完成自提单',
    );
    expect((await orderDetail(pickupOrderId)).status, '前置：自提单应为已完成(5)').toBe(ORDER_STATUS.completed);

    const body = await adminRefund(pickupOrderId, { reason: autoName('72 自提已完成尝试退款') });
    expect(body.code, '自提已完成单不在本轮放开范围，应被拒').toBe(PARAM_ERROR);
    expect(body.message, '拒绝文案应说明状态不允许退款').toContain('订单状态不允许退款');
    expect((await orderDetail(pickupOrderId)).status, '被拒后订单状态不应变化').toBe(ORDER_STATUS.completed);
  });

  test('7. C 端入口不放开：到店单申请退款仍被拒', async ({ playwright }) => {
    test.skip(!sku, '环境无库存 SKU');
    const buyer = await memberIn(playwright, communityId);
    const order = await createStoreOrder(1, buyer.userId, autoName('72 会员到店单'));
    expect((await orderDetail(order.id)).status, '前置：会员到店单应为已完成(5)').toBe(ORDER_STATUS.completed);

    const buyerApi = await newAuthedRequest(playwright, buyer.token);
    try {
      const body = (await (
        await buyerApi.post(`/api/v1/orders/${order.id}/refund`, { data: { reason: autoName('72 C端申请退款') } })
      ).json()) as ApiResponse;
      expect(body.code, '门店单 C 端只读，申请退款应被拒').not.toBe(0);
      expect(body.message, '应提示联系门店处理').toContain('门店订单请联系门店处理');
    } finally {
      await buyerApi.dispose();
    }

    // C 端被拒后订单不应进入退款中，后台入口仍可退（与用例 2 同口径）
    expect((await orderDetail(order.id)).status, 'C 端被拒后订单仍为已完成(5)').toBe(ORDER_STATUS.completed);
  });
});
