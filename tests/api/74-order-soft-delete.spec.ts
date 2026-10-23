import { test, expect } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  pickSku,
  type ApiResponse,
  type PageData,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';
import type { Playwright, APIRequestContext } from '@playwright/test';

/**
 * 模块 74：C 端订单软删除与后台恢复（HTTP 收口）
 * 覆盖 76af883 审查修复的对外行为：
 *   - 仅终态单（已完成 5 / 已取消 6 / 已退款 8）可删，进行中订单删除被拒；
 *   - 删除是软删：C 端列表与角标不再出现，后台按会员查单仍全量可见并带 user_deleted=1；
 *   - 重复删除幂等成功（不报错、不回改标记）；
 *   - 他人订单不可删（归属校验走服务端用户上下文）；
 *   - 后台 restore 复位标记后，订单重新回到 C 端列表。
 */

interface OrderInfo {
  id: number;
  order_no: string;
  status: number;
  user_deleted?: number;
}

let api: Awaited<ReturnType<typeof newAuthedRequest>>;
let userId = 0;
let communityId = 0;
let skuId = 0;
let adminApi: APIRequestContext;

/**
 * 创建一笔自提订单
 * - 函数名: createOrder
 * - 参数: quantity — 购买数量（错开重复下单指纹窗口）
 * - 返回: Promise<OrderInfo>
 * - 异常: 下单业务失败时抛出 Error
 */
async function createOrder(quantity = 1): Promise<OrderInfo> {
  const resp = await api.post('/api/v1/orders', {
    data: { delivery_type: 1, address_id: 0, items: [{ sku_id: skuId, quantity }] },
  });
  const body = (await resp.json()) as ApiResponse<OrderInfo>;
  if (body.code !== 0) throw new Error(`创建订单失败 code=${body.code} message=${body.message}`);
  return body.data as OrderInfo;
}

/**
 * 在 C 端订单列表（全状态）中查找指定订单
 * - 函数名: findInClientList
 * - 参数: id — 订单 ID
 * - 返回: Promise<OrderInfo | undefined>（未出现返回 undefined）
 */
async function findInClientList(id: number): Promise<OrderInfo | undefined> {
  const resp = await api.get('/api/v1/orders', { params: { page: 1, page_size: 100 } });
  const body = (await resp.json()) as ApiResponse<PageData<OrderInfo>>;
  return body.data?.list.find((o) => o.id === id);
}

/**
 * 在后台按会员查单中查找指定订单
 * - 函数名: findInAdminList
 * - 参数: id — 订单 ID
 * - 返回: Promise<OrderInfo | undefined>
 * - 异常: 后台接口业务码非 0 时抛出 Error
 */
async function findInAdminList(id: number): Promise<OrderInfo | undefined> {
  const resp = await adminApi.get('/api/v1/admin/orders', {
    params: { page: 1, page_size: 100, user_id: userId },
  });
  const body = (await resp.json()) as ApiResponse<PageData<OrderInfo>>;
  if (body.code !== 0) throw new Error(`后台查单失败 code=${body.code} message=${body.message}`);
  return body.data?.list.find((o) => o.id === id);
}

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const info = await clientLogin(playwright.request, uniquePhone());
  api = await newAuthedRequest(playwright, info.access_token);
  userId = info.user.id;
  communityId = info.user.community_id;
  skuId = (await pickSku(api, undefined, communityId)).id;

  // 后台上下文自带 X-Community-Id 社区语境（多数管理端接口要求）；
  // 显式对齐测试用户所在社区，否则 restore 会因社区 scope 不匹配报「未找到需恢复的订单」
  adminApi = (await newAdminContext(playwright, undefined, undefined, communityId)).api;
});

test.afterAll(async () => {
  await api?.dispose();
  await adminApi?.dispose();
});

test.describe.serial('订单软删除与恢复', () => {
  let canceled: OrderInfo;

  test('进行中订单不可删除（仅终态可删）', async () => {
    const pending = await createOrder(1);
    const resp = await api.delete(`/api/v1/orders/${pending.id}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '待支付订单应拒绝删除').not.toBe(0);
    // 清理：取消该单，避免遗留待支付单影响后续轮次
    await api.put(`/api/v1/orders/${pending.id}/cancel`);
  });

  test('终态订单删除成功', async () => {
    canceled = await createOrder(2);
    const cancelResp = await api.put(`/api/v1/orders/${canceled.id}/cancel`);
    expect((await cancelResp.json()).code).toBe(0);

    const resp = await api.delete(`/api/v1/orders/${canceled.id}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `删除已取消订单应成功, message=${body.message}`).toBe(0);
  });

  test('软删后 C 端列表不再出现该单', async () => {
    expect(await findInClientList(canceled.id)).toBeUndefined();
  });

  test('重复删除幂等成功', async () => {
    const resp = await api.delete(`/api/v1/orders/${canceled.id}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '重复删除应幂等成功而非报错').toBe(0);
  });

  test('后台按会员查单仍可见，且带 user_deleted=1', async () => {
    const row = await findInAdminList(canceled.id);
    expect(row, '后台应仍可查到用户已删订单').toBeDefined();
    expect(row?.user_deleted).toBe(1);
  });

  test('他人订单不可删除（归属校验）', async ({ playwright }) => {
    const other = await clientLogin(playwright.request, uniquePhone());
    const otherApi = await newAuthedRequest(playwright, other.access_token);
    try {
      const resp = await otherApi.delete(`/api/v1/orders/${canceled.id}`);
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, '跨用户删除应被拒绝').not.toBe(0);
    } finally {
      await otherApi.dispose();
    }
  });

  test('后台恢复后订单重新回到 C 端列表', async () => {
    const resp = await adminApi.post(`/api/v1/admin/orders/${canceled.id}/restore`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `恢复订单应成功, message=${body.message}`).toBe(0);

    const row = await findInClientList(canceled.id);
    expect(row, '恢复后 C 端列表应重新出现该单').toBeDefined();
    expect(row?.user_deleted ?? 0).toBe(0);
  });

  test('未登录删除被拒绝', async ({ request }) => {
    const resp = await request.delete(`/api/v1/orders/${canceled.id}`);
    const body = (await resp.json().catch(() => ({}))) as ApiResponse;
    expect([200, 401], '未授权只允许 HTTP 200+业务码 或 401 两种表达').toContain(resp.status());
    if (resp.status() === 200) expect(body.code).not.toBe(0);
  });
});
