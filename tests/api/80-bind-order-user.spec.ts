import { test, expect, type Playwright } from '@playwright/test';
import {
  newAdminContext,
  clientLogin,
  pickSku,
  ensureStock,
  assertOk,
  type ApiResponse,
  type SkuRef,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 80：散客订单补绑会员（2026-10-25 新增 POST /admin/orders/:id/bind-user）
 * 背景：收银台收款后顾客要求把消费挂到会员名下（积分等权益前置），散客单（user_id=0）
 *   支持补绑一次，绑后不可换绑/解绑（order 服务条件更新 WHERE user_id=0 保证）。
 * 覆盖：
 *   - 散客到店现金单绑定成功：详情 user_id/user_name 回填
 *   - 口径边界：重复绑定拒绝（10004）/ 非散客单拒绝（10004）/ 待支付单拒绝（10004）
 *   - 跨社区会员拒绝：他社区会员挂账本社区订单被网关预检拦截（10003 会员不存在）
 *   - 禁用会员拒绝：预检同口径拦截（10004），与 order 服务兜底码值一致
 *   - C 端可见性：绑定后订单按 user_id 进入会员「我的订单」，详情可查，
 *     C 端退款/取消仍被拒「门店订单请联系门店处理」（source=2 只读口径不因绑定放开）
 * 数据惯例：POS 单无删除接口，按「只增不删」留档，备注统一带 AUTO-TEST 与模块号；
 *   会员为本轮新建（uniquePhone），禁用会员用例结束后恢复 status=1。
 */

/** 业务错误码（与 common/errcode 对齐） */
const NOT_FOUND = 10003;
const OPERATION_NOT_ALLOWED = 10004;

/** 订单状态常量，与 model/order.go 对齐 */
const ORDER_STATUS = { pending: 1, paid: 2, completed: 5 } as const;

interface PosOrderResult {
  id: number;
  order_no: string;
  pay_amount: number;
  paid: boolean;
  payment_no?: string;
}

interface AdminOrderDetail {
  id?: number;
  order_no?: string;
  status?: number;
  user_id?: number;
  user_name?: string;
  source?: number;
  delivery_type?: number;
}

interface RegisterResult {
  id: number;
  phone: string;
  nickname: string;
  status: number;
  is_new: boolean;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let communityId = 0;
let secondCommunity = 0;
let sku: SkuRef | null = null;

/** 本轮造的会员：绑定用主会员 / 跨社区会员 / 禁用会员（afterAll 恢复） */
let memberMain = { id: 0, phone: '' };
let memberCrossCommunity = { id: 0, phone: '' };
let memberBlocked = { id: 0, phone: '' };

/**
 * 创建一笔到店现金单（pay_type=4、delivery_type=3，收款即完结 status=5）
 * - 函数名: createStoreOrder
 * - 参数: userId — 会员 ID（0=散客）；remark — 备注
 * - 返回: Promise<PosOrderResult>
 * - 异常: 建单业务失败时经 assertOk 抛出
 * - 使用示例: const order = await createStoreOrder(0, 'AUTO-TEST 80 散客单');
 */
async function createStoreOrder(userId: number, remark: string): Promise<PosOrderResult> {
  return assertOk<PosOrderResult>(
    (await (
      await adminApi.post('/api/v1/admin/pos/orders', {
        data: {
          user_id: userId,
          pay_type: 4,
          delivery_type: 3,
          items: [{ sku_id: sku!.id, quantity: 1 }],
          remark,
        },
      })
    ).json()) as ApiResponse<PosOrderResult>,
    `到店单创建(${remark})`,
  );
}

/**
 * 读取管理端订单详情
 * - 函数名: orderDetail
 * - 参数: id — 订单 ID
 * - 返回: Promise<AdminOrderDetail>
 * - 异常: 查询业务失败时经 assertOk 抛出
 * - 使用示例: const d = await orderDetail(orderId);
 */
async function orderDetail(id: number): Promise<AdminOrderDetail> {
  return assertOk<AdminOrderDetail>(
    (await (await adminApi.get(`/api/v1/admin/orders/${id}`)).json()) as ApiResponse<AdminOrderDetail>,
    '订单详情查询',
  );
}

/**
 * 散客单补绑会员请求体
 * - 函数名: bindUser
 * - 参数: orderId — 订单 ID；userId — 会员 ID
 * - 返回: Promise<ApiResponse>（原始业务码交调用方断言）
 * - 异常: 无
 * - 使用示例: const body = await bindUser(orderId, memberMain.id);
 */
async function bindUser(orderId: number, userId: number): Promise<ApiResponse> {
  return (await (
    await adminApi.post(`/api/v1/admin/orders/${orderId}/bind-user`, { data: { user_id: userId } })
  ).json()) as ApiResponse;
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  communityId = admin.communityId;

  // 班次兜底：收银前置要求有进行中班次（网关+order 双校验）。
  // 先尽力关闭遗留班次（其他模块残留会顶住开工），再开工本轮班次
  await adminApi.post('/api/v1/admin/pos/shifts/end', { data: { note: 'AUTO-TEST 80 清理遗留班次' } });
  const startResp = (await (
    await adminApi.post('/api/v1/admin/pos/shifts/start', { data: { note: 'AUTO-TEST 80 开工' } })
  ).json()) as ApiResponse;
  if (startResp.code !== 0) throw new Error(`开工失败 code=${startResp.code} message=${startResp.message}`);

  // 找第二个社区做跨社区会员用例（环境只有单社区时跳过）
  try {
    const resp = await adminApi.get('/api/v1/admin/my/communities');
    const body = (await resp.json()) as ApiResponse<{ id: number }[]>;
    const list = (Array.isArray(body.data) ? body.data : []).filter((c) => c.id !== communityId);
    secondCommunity = list[0]?.id ?? 0;
  } catch {
    secondCommunity = 0;
  }

  sku = await pickSku(adminApi, undefined, communityId);
  await ensureStock(adminApi, sku.id, 50);

  // 主会员：后台注册（scene=pos，自动关联当前社区），绑定与 C 端可见性用例共用
  const mainPhone = uniquePhone();
  const main = assertOk<RegisterResult>(
    (await (
      await adminApi.post('/api/v1/admin/users', { data: { phone: mainPhone, scene: 'pos' } })
    ).json()) as ApiResponse<RegisterResult>,
    '注册主会员',
  );
  memberMain = { id: main.id, phone: mainPhone };

  // 禁用会员：注册后立即拉黑（用例结束恢复）
  const blockedPhone = uniquePhone();
  const blocked = assertOk<RegisterResult>(
    (await (
      await adminApi.post('/api/v1/admin/users', { data: { phone: blockedPhone, scene: 'pos' } })
    ).json()) as ApiResponse<RegisterResult>,
    '注册禁用会员',
  );
  memberBlocked = { id: blocked.id, phone: blockedPhone };
  assertOk(
    (await (
      await adminApi.put(`/api/v1/admin/users/${blocked.id}/status`, { data: { status: 0 } })
    ).json()) as ApiResponse,
    '拉黑会员',
  );
});

test.afterAll(async () => {
  // 交班（尽力而为）：本轮收银单已归属该班次，交班落 statistics_json 快照
  if (adminApi) {
    await adminApi.post('/api/v1/admin/pos/shifts/end', { data: { note: 'AUTO-TEST 80 交班' } });
  }
  // 禁用会员恢复为正常状态（C 端拉黑拦截有 30s 缓存，恢复动作本身幂等）
  if (memberBlocked.id > 0) {
    await adminApi.put(`/api/v1/admin/users/${memberBlocked.id}/status`, { data: { status: 1 } });
  }
  await adminApi?.dispose();
});

test.describe.serial('散客订单补绑会员', () => {
  let boundOrderId = 0;
  let boundOrderNo = '';

  test('1. 散客到店现金单绑定成功：详情回填 user_id/user_name', async () => {
    const order = await createStoreOrder(0, 'AUTO-TEST 80 散客绑定');
    boundOrderId = order.id;
    boundOrderNo = order.order_no;
    expect(order.paid, '现金到店单应收款即完结').toBe(true);

    const d0 = await orderDetail(order.id);
    expect(d0.user_id, '建单时应为散客').toBe(0);
    expect(d0.status, '到店单应直接完结(5)').toBe(ORDER_STATUS.completed);

    const body = await bindUser(order.id, memberMain.id);
    expect(body.code, `补绑失败: ${body.message}`).toBe(0);

    const d1 = await orderDetail(order.id);
    expect(d1.user_id, '绑定后应挂到会员名下').toBe(memberMain.id);
    expect(d1.user_name, '会员名应回填').toBeTruthy();
  });

  test('2. 已绑定订单重复绑定拒绝（10004，条件更新防换绑）', async () => {
    const body = await bindUser(boundOrderId, memberMain.id);
    expect(body.code, '重复绑定应被拒绝').toBe(OPERATION_NOT_ALLOWED);
    expect(body.message).toContain('不可重复绑定');
  });

  test('3. 非散客单（带会员建单）拒绝绑定（10004）', async () => {
    const order = await createStoreOrder(memberMain.id, 'AUTO-TEST 80 非散客');
    expect(order.paid).toBe(true);
    const body = await bindUser(order.id, memberMain.id);
    expect(body.code, '带会员建单不应允许再绑').toBe(OPERATION_NOT_ALLOWED);
    expect(body.message).toContain('不可重复绑定');
  });

  test('4. 待支付单拒绝绑定（10004，应走「继续收款」时选会员）', async ({ playwright }) => {
    // 付款码渠道占位值：优先 5（微信/双渠道），仅支付宝就绪时用 6
    const methods = assertOk<{ list: Array<{ pay_type: number }> }>(
      (await (await adminApi.get('/api/v1/admin/pos/pay-methods')).json()) as ApiResponse<{
        list: Array<{ pay_type: number }>;
      }>,
      '支付方式查询',
    );
    const codePayType = methods.list.find((m) => m.pay_type === 5 || m.pay_type === 6)?.pay_type ?? 0;
    test.skip(codePayType === 0, '环境未配置任何付款码渠道，无法造待支付单');

    const pending = assertOk<PosOrderResult>(
      (await (
        await adminApi.post('/api/v1/admin/pos/orders', {
          data: {
            user_id: 0,
            pay_type: codePayType,
            delivery_type: 3,
            manual_amount: 6.6,
            remark: 'AUTO-TEST 80 待支付',
          },
        })
      ).json()) as ApiResponse<PosOrderResult>,
      '待支付直接收款单创建',
    );
    expect(pending.paid, '付款码两步化第一步应为待支付').toBe(false);
    expect((await orderDetail(pending.id)).status).toBe(ORDER_STATUS.pending);

    const body = await bindUser(pending.id, memberMain.id);
    expect(body.code, '待支付单应拒绝绑定').toBe(OPERATION_NOT_ALLOWED);
    expect(body.message).toContain('仅已收款的订单可绑定会员');
  });

  test('5. 跨社区会员拒绝绑定（10003，网关预检按社区语境判「会员不存在」）', async ({ playwright }) => {
    test.skip(secondCommunity <= 0, '环境仅有单社区，无法验证跨社区');
    const other = await newAdminContext(playwright, undefined, undefined, secondCommunity);
    try {
      const phone = uniquePhone();
      const created = assertOk<RegisterResult>(
        (await (
          await other.api.post('/api/v1/admin/users', { data: { phone, scene: 'pos' } })
        ).json()) as ApiResponse<RegisterResult>,
        '注册他社区会员',
      );
      memberCrossCommunity = { id: created.id, phone };

      const body = await bindUser(boundOrderId, created.id);
      expect(body.code, '他社区会员挂账本社区订单应被预检拦截').toBe(NOT_FOUND);
      expect(body.message).toContain('会员不存在');
    } finally {
      await other.api.dispose();
    }
  });

  test('6. 禁用会员拒绝绑定（10004，预检与 order 服务兜底码值一致）', async () => {
    const body = await bindUser(boundOrderId, memberBlocked.id);
    expect(body.code, '禁用会员应被拒绝').toBe(OPERATION_NOT_ALLOWED);
    expect(body.message).toContain('已被禁用');
  });

  test('7. 绑定后 C 端可见：进入会员订单列表/详情，退款与取消仍被门店口径拦截', async ({ playwright }) => {
    const info = await clientLogin(playwright.request, memberMain.phone);
    const client = await playwright.request.newContext({
      extraHTTPHeaders: { Authorization: `Bearer ${info.access_token}` },
    });
    try {
      // 列表可见（C 端按 user_id 查，补绑后到店单计入会员名下）
      const listBody = (await (
        await client.get('/api/v1/orders', { params: { page: 1, page_size: 20 } })
      ).json()) as ApiResponse<{ list?: Array<{ order_no?: string; id?: number }> }>;
      expect(listBody.code, `C 端订单列表失败: ${listBody.message}`).toBe(0);
      const hit = (listBody.data?.list ?? []).find((o) => o.order_no === boundOrderNo);
      expect(hit, '补绑订单应出现在会员 C 端订单列表').toBeTruthy();

      // 详情可查（归属校验按 user_id 通过）
      const detailBody = (await (
        await client.get(`/api/v1/orders/${boundOrderId}`)
      ).json()) as ApiResponse<{ status?: number; order_no?: string }>;
      expect(detailBody.code, `C 端订单详情失败: ${detailBody.message}`).toBe(0);
      expect(detailBody.data?.order_no).toBe(boundOrderNo);
      expect(detailBody.data?.status).toBe(ORDER_STATUS.completed);

      // C 端申请退款：source=2 只读口径不因补绑放开
      const refundBody = (await (
        await client.post(`/api/v1/orders/${boundOrderId}/refund`, {
          data: { reason: 'AUTO-TEST 80 门店单退款拦截' },
        })
      ).json()) as ApiResponse;
      expect(refundBody.code, '门店单 C 端退款应被拒绝').not.toBe(0);
      expect(refundBody.message).toContain('门店订单请联系门店处理');

      // C 端取消同理（防现金单被按「货到付款」口径取消造成资损）
      const cancelBody = (await (
        await client.put(`/api/v1/orders/${boundOrderId}/cancel`)
      ).json()) as ApiResponse;
      expect(cancelBody.code, '门店单 C 端取消应被拒绝').not.toBe(0);
      expect(cancelBody.message).toContain('门店订单请联系门店处理');
    } finally {
      await client.dispose();
    }
  });
});
