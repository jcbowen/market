import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  pickSku,
  ensureStock,
  assertOk,
  type ApiResponse,
  type SkuRef,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 49：通用状态流转 PUT /api/v1/admin/orders/:id/status
 * 覆盖（状态机 orderTransitions：2:{1} 3:{2} 4:{2,3} 5:{2,4} 7:{2,3,4} 8:{7}）：
 *   - 缺 status 被参数校验拒绝（10001）
 *   - 目标状态 6 被网关拒绝（10001「取消订单请使用专用取消接口」）
 *   - 非法状态值 9 被状态机拒绝（10004）
 *   - 跳状态 1→3 被状态机拒绝（10004）
 *   - 合法主线 1→2→3→4→5：pay_time/deliver_time/complete_time 落库、status_logs 记录流转
 *   - 终态已完成(5) 再流转被拒
 *   - 自提单 2→5 直接完成
 *   - 已取消(6) 单再流转被拒
 * 说明：
 *   - 7→8（退款中→已退款）由退款审批链路自动置位（refund.go handleRefundCompleted），
 *     15/20/41 号已覆盖退款链路，此处不重复构造
 *   - 后台取消专用接口 POST /:id/cancel 的取消语义（库存回补/原因落库）由 46 号覆盖，
 *     本模块仅借其构造已取消单作为终态前置数据
 */

interface OrderResult {
  id: number;
  order_no: string;
  [k: string]: unknown;
}

interface StatusLog {
  from_status: number;
  to_status: number;
  operator_type: string;
  remark?: string;
  created_at: string;
}

interface AdminOrderDetail {
  id: number;
  status: number;
  pay_time?: string | null;
  deliver_time?: string | null;
  complete_time?: string | null;
  cancel_time?: string | null;
  status_logs?: StatusLog[];
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let buyerToken = '';
let sku: SkuRef | null = null;

/**
 * 创建待支付自提单（不支付，订单状态 1）
 * - 函数名: createPendingOrder
 * - 参数: playwright — Playwright 工厂；remark — 订单备注；quantity — 购买数量（默认 1，
 *         同用户连续多单须错开数量以避免重复下单指纹碰撞）
 * - 返回: Promise<number> 订单 ID（失败时断言抛出）
 * - 异常: 下单业务码非 0 时经 assertOk 抛出
 * - 使用示例: const id = await createPendingOrder(playwright, 'AUTO-TEST 49');
 */
async function createPendingOrder(playwright: Playwright, remark: string, quantity = 1): Promise<number> {
  const buyerApi = await newAuthedRequest(playwright, buyerToken);
  try {
    const created = assertOk(
      (await (
        await buyerApi.post('/api/v1/orders', {
          data: {
            delivery_type: 1,
            address_id: 0,
            remark,
            items: [{ sku_id: sku!.id, quantity }],
          },
        })
      ).json()) as ApiResponse<OrderResult>,
      'C 端下单',
    );
    return created.id;
  } finally {
    await buyerApi.dispose();
  }
}

/**
 * 读取后台订单详情
 * - 函数名: getAdminOrder
 * - 参数: orderId — 订单 ID
 * - 返回: Promise<AdminOrderDetail>（含 status/time 字段/status_logs）
 * - 异常: 业务码非 0 时经 assertOk 抛出
 * - 使用示例: const detail = await getAdminOrder(orderId);
 */
async function getAdminOrder(orderId: number): Promise<AdminOrderDetail> {
  const body = (await (
    await adminApi.get(`/api/v1/admin/orders/${orderId}`)
  ).json()) as ApiResponse<AdminOrderDetail>;
  return assertOk(body, `后台订单详情(${orderId})`);
}

/**
 * 通用状态流转请求
 * - 函数名: putStatus
 * - 参数: orderId — 订单 ID；status — 目标状态；remark — 备注（可省略）
 * - 返回: Promise<ApiResponse<{ id: number; status: number }>>
 * - 异常: 网络错误由 Playwright 抛出（业务错误通过 code 判断）
 * - 使用示例: const body = await putStatus(orderId, 2, '备货');
 */
async function putStatus(
  orderId: number,
  status: number | undefined,
  remark?: string,
): Promise<ApiResponse<{ id: number; status: number }>> {
  const data: Record<string, unknown> = {};
  if (status !== undefined) data.status = status;
  if (remark !== undefined) data.remark = remark;
  return (await (
    await adminApi.put(`/api/v1/admin/orders/${orderId}/status`, { data })
  ).json()) as ApiResponse<{ id: number; status: number }>;
}

test.beforeAll(async ({ playwright }) => {
  const buyer = await clientLogin(playwright.request, uniquePhone());
  buyerToken = buyer.access_token;
  const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
  adminApi = admin.api;
  try {
    sku = await pickSku(adminApi, undefined, buyer.user.community_id);
    await ensureStock(adminApi, sku.id, 50);
  } catch {
    sku = null;
  }
});

test.afterAll(async () => {
  await adminApi?.dispose();
});

test.describe.serial('通用状态流转', () => {
  let mainOrderId = 0;

  test('1. 缺 status 被参数校验拒绝（10001）', async ({ playwright }) => {
    test.skip(!sku, '环境无库存 SKU');
    mainOrderId = await createPendingOrder(playwright, 'AUTO-TEST 49 主线流转');
    expect(mainOrderId).toBeGreaterThan(0);
    const body = await putStatus(mainOrderId, undefined, '缺状态');
    expect(body.code, '缺 status 应被参数校验拒绝').toBe(10001);
  });

  test('2. 目标状态 6 被网关拒绝（取消必须走专用接口）', async () => {
    if (!mainOrderId) test.skip(true, '订单未创建');
    const body = await putStatus(mainOrderId, 6);
    expect(body.code, 'status=6 应被网关拒绝').toBe(10001);
    expect(body.message).toContain('专用取消接口');
    // 校验失败不落库：订单仍为待支付
    const detail = await getAdminOrder(mainOrderId);
    expect(detail.status).toBe(1);
  });

  test('3. 非法状态值 9 被状态机拒绝（10004）', async () => {
    if (!mainOrderId) test.skip(true, '订单未创建');
    const body = await putStatus(mainOrderId, 9);
    expect(body.code, '非法状态值应被拒绝').toBe(10004);
    expect(body.message).toContain('订单状态不允许流转');
  });

  test('4. 跳状态 1→3 被状态机拒绝（10004）', async () => {
    if (!mainOrderId) test.skip(true, '订单未创建');
    const body = await putStatus(mainOrderId, 3);
    expect(body.code, '待支付不可直接备货').toBe(10004);
    const detail = await getAdminOrder(mainOrderId);
    expect(detail.status, '被拒后保持待支付').toBe(1);
  });

  test('5. 合法流转 1→2（pay_time 落库 + 状态日志）', async () => {
    if (!mainOrderId) test.skip(true, '订单未创建');
    const body = await putStatus(mainOrderId, 2, 'AUTO-TEST 49 手动置已支付');
    expect(body.code, `1→2 失败: ${body.message}`).toBe(0);
    expect(body.data?.status).toBe(2);
    const detail = await getAdminOrder(mainOrderId);
    expect(detail.status).toBe(2);
    expect(detail.pay_time, 'pay_time 应落库').toBeTruthy();
    const lastLog = detail.status_logs?.[detail.status_logs.length - 1];
    expect(lastLog?.from_status, '状态日志应记录 1→2').toBe(1);
    expect(lastLog?.to_status).toBe(2);
    expect(lastLog?.operator_type).toBe('admin');
    expect(lastLog?.remark).toBe('AUTO-TEST 49 手动置已支付');
  });

  test('6. 合法流转 2→3（备货中）', async () => {
    if (!mainOrderId) test.skip(true, '订单未创建');
    const body = await putStatus(mainOrderId, 3, 'AUTO-TEST 49 备货');
    expect(body.code, `2→3 失败: ${body.message}`).toBe(0);
    expect(body.data?.status).toBe(3);
    const detail = await getAdminOrder(mainOrderId);
    expect(detail.status).toBe(3);
  });

  test('7. 合法流转 3→4（发货，deliver_time 落库）', async () => {
    if (!mainOrderId) test.skip(true, '订单未创建');
    const body = await putStatus(mainOrderId, 4, 'AUTO-TEST 49 发货');
    expect(body.code, `3→4 失败: ${body.message}`).toBe(0);
    expect(body.data?.status).toBe(4);
    const detail = await getAdminOrder(mainOrderId);
    expect(detail.status).toBe(4);
    expect(detail.deliver_time, 'deliver_time 应落库').toBeTruthy();
  });

  test('8. 合法流转 4→5（完成，complete_time 落库）', async () => {
    if (!mainOrderId) test.skip(true, '订单未创建');
    const body = await putStatus(mainOrderId, 5, 'AUTO-TEST 49 完成');
    expect(body.code, `4→5 失败: ${body.message}`).toBe(0);
    expect(body.data?.status).toBe(5);
    const detail = await getAdminOrder(mainOrderId);
    expect(detail.status).toBe(5);
    expect(detail.complete_time, 'complete_time 应落库').toBeTruthy();
  });

  test('9. 终态已完成(5) 再流转被拒（10004）', async () => {
    if (!mainOrderId) test.skip(true, '订单未创建');
    const body = await putStatus(mainOrderId, 3);
    expect(body.code, '已完成订单不可再备货').toBe(10004);
    const detail = await getAdminOrder(mainOrderId);
    expect(detail.status, '终态保持不变').toBe(5);
  });

  test('10. 自提单 2→5 直接完成', async ({ playwright }) => {
    test.skip(!sku, '环境无库存 SKU');
    // 自提单现金支付（状态 2），确认收货语义允许直接完成
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    let pickupOrderId = 0;
    try {
      const created = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: {
              delivery_type: 1,
              address_id: 0,
              remark: 'AUTO-TEST 49 自提直接完成',
              // quantity=2：与测试 1 的主线单（quantity=1）错开，避免重复下单指纹碰撞
              items: [{ sku_id: sku!.id, quantity: 2 }],
            },
          })
        ).json()) as ApiResponse<OrderResult>,
        'C 端下单',
      );
      pickupOrderId = created.id;
      const paid = (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: pickupOrderId, channel: 'cash' } })
      ).json()) as ApiResponse;
      expect(paid.code, `现金支付失败: ${paid.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
    const body = await putStatus(pickupOrderId, 5, 'AUTO-TEST 49 自提核销语义');
    expect(body.code, `2→5 失败: ${body.message}`).toBe(0);
    expect(body.data?.status).toBe(5);
    const detail = await getAdminOrder(pickupOrderId);
    expect(detail.status).toBe(5);
    expect(detail.complete_time).toBeTruthy();
  });

  test('11. 已取消(6) 单再流转被拒（10004）', async ({ playwright }) => {
    test.skip(!sku, '环境无库存 SKU');
    // 构造已取消单：待支付单走后台专用取消接口（取消语义由 46 号覆盖）
    // quantity=3：与测试 1（1）/测试 10（2）错开，避免重复下单指纹碰撞
    const cancelOrderId = await createPendingOrder(playwright, 'AUTO-TEST 49 已取消单', 3);
    const cancelled = (await (
      await adminApi.post(`/api/v1/admin/orders/${cancelOrderId}/cancel`, {
        data: { reason: 'AUTO-TEST 49 构造终态' },
      })
    ).json()) as ApiResponse<{ status: number }>;
    expect(cancelled.code, `后台取消失败: ${cancelled.message}`).toBe(0);

    const body = await putStatus(cancelOrderId, 2);
    expect(body.code, '已取消订单不可再置已支付').toBe(10004);
    const detail = await getAdminOrder(cancelOrderId);
    expect(detail.status, '取消态保持不变').toBe(6);
    expect(detail.cancel_time).toBeTruthy();
  });
});
