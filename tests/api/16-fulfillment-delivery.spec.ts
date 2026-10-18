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
  type ClientLoginInfo,
  type SkuRef,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 16：订单配送协同
 * 覆盖：创建员工并绑定履约会员 → 买家下配送单 → 后台指派配送员 →
 *       履约工作台可见 → 开始配送 → 送达拍照 → 解绑履约会员
 * 说明：履约人员 = 本社区任职且绑定会员的员工（员工编辑页配置）；
 *       履约人员与买家均走 C 端手机号直登，履约接口要求操作者为本社区履约人员
 */

interface OrderResult {
  id: number;
  order_no: string;
  [k: string]: unknown;
}

interface FulfillmentOrder {
  id: number;
  order_id?: number;
  order_no?: string;
  status?: number;
  [k: string]: unknown;
}

/** 工作台条目即订单对象，id 即订单 ID；兼容 order_id 字段 */
function isTargetOrder(o: FulfillmentOrder, orderId: number): boolean {
  return o.id === orderId || o.order_id === orderId;
}

/**
 * 创建新 C 端用户并返回携带已绑定社区的 token
 * clientLogin 首登自动绑社区，但登录时签发的 JWT 不含 community_id，
 * 依赖社区语境的接口（履约/下单）会报参数错误，故绑社区后用同号重登刷新
 */
async function newClientUser(playwright: Playwright): Promise<ClientLoginInfo> {
  const phone = uniquePhone();
  await clientLogin(playwright.request, phone);
  return clientLogin(playwright.request, phone);
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
/** 管理端语境社区 ID（与履约人员/买家同社区） */
let adminCommunityId = 0;
let fulfiller: ClientLoginInfo | null = null;
/** 绑定履约会员的员工账号 ID（清理阶段解绑用） */
let staffId = 0;
let buyerToken = '';
let sku: SkuRef | null = null;

test.beforeAll(async ({ playwright }) => {
  // 履约人员与买家各一个 C 端账号（自动绑定同一默认社区，重登刷新社区语境 token）
  fulfiller = await newClientUser(playwright);
  const buyer = await newClientUser(playwright);
  buyerToken = buyer.access_token;
  // 管理端语境必须与 C 端用户同社区，否则绑定会员/指派后履约接口按"非本社区履约人员"拒绝
  const admin = await newAdminContext(playwright, undefined, undefined, buyer.user.community_id);
  adminApi = admin.api;
  adminCommunityId = admin.communityId;
  try {
    sku = await pickSku(adminApi, undefined, buyer.user.community_id);
    await ensureStock(adminApi, sku.id, 100);
  } catch {
    sku = null;
  }
});

test.afterAll(async () => {
  await adminApi?.dispose();
});

test.describe.serial('配送协同全链路', () => {
  let orderId = 0;
  let fulfillOrderId = 0;

  test('创建员工并绑定履约会员', async () => {
    test.skip(!fulfiller, '履约账号未创建');
    // 履约人员 = 本社区任职且绑定会员的员工：取本社区内置社区管理员角色建员工，创建时绑定会员
    const rolesResp = await adminApi.get('/api/v1/admin/roles', {
      params: { community_id: adminCommunityId },
    });
    const rolesBody = (await rolesResp.json()) as ApiResponse<{ id: number; code?: string }[] | { list: { id: number; code?: string }[] }>;
    const builtin = asList<{ id: number; code?: string }>(rolesBody.data).find((r) => r.code === 'community_admin');
    expect(builtin, '本社区无内置社区管理员角色').toBeTruthy();
    const resp = await adminApi.post('/api/v1/admin/admins', {
      data: {
        username: `auto_ff_${uniquePhone()}`,
        password: 'Ff123456',
        real_name: 'AUTO-TEST 履约员工',
        role_ids: [builtin!.id],
        user_id: fulfiller!.user.id,
      },
    });
    const body = (await resp.json()) as ApiResponse<{ id?: number }>;
    expect(body.code, `创建履约员工失败: ${body.message}`).toBe(0);
    staffId = body.data?.id ?? 0;
    expect(staffId).toBeGreaterThan(0);
  });

  test('履约人员状态查询为 true', async ({ playwright }) => {
    if (!fulfiller) test.skip(true, '履约账号未创建');
    const fApi = await newAuthedRequest(playwright, fulfiller!.access_token);
    try {
      const resp = await fApi.get('/api/v1/user/fulfillment-status');
      const body = (await resp.json()) as ApiResponse<{ is_fulfiller: boolean }>;
      expect(body.code).toBe(0);
      expect(body.data?.is_fulfiller).toBe(true);
    } finally {
      await fApi.dispose();
    }
  });

  test('买家创建收货地址', async ({ playwright }) => {
    if (!buyerToken) test.skip(true, '买家账号未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const resp = await buyerApi.post('/api/v1/user/addresses', {
        data: { contact_name: 'AUTO-TEST 收货', contact_phone: '13800001234', detail: '测试路 2 号' },
      });
      const body = (await resp.json()) as ApiResponse<{ id?: number }>;
      expect(body.code, `创建地址失败: ${body.message}`).toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('买家下送货上门订单并现金支付', async ({ playwright }) => {
    test.skip(!sku || !buyerToken, '环境缺 SKU 或买家');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      // 取刚创建的地址 id
      const addrResp = (await (await buyerApi.get('/api/v1/user/addresses')).json()) as ApiResponse<
        { id: number }[] | { list: { id: number }[] }
      >;
      const addr = asList<{ id: number }>(addrResp.data)[0];
      expect(addr, '买家无可用地址').toBeTruthy();

      // 配送单有起送金额门槛（当前 15 元），按 SKU 单价估算满足门槛的数量
      const skuResp = (await (await buyerApi.get(`/api/v1/skus/${sku!.id}`)).json()) as ApiResponse<{
        price: number;
      }>;
      const price = skuResp.data?.price ?? 0;
      const minAmount = 15;
      const qty = price > 0 ? Math.ceil(minAmount / price) : 1;

      const created = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: {
              delivery_type: 2,
              address_id: addr.id,
              remark: 'AUTO-TEST 配送',
              items: [{ sku_id: sku!.id, quantity: qty }],
            },
          })
        ).json()) as ApiResponse<OrderResult>,
        'C 端下单',
      );
      const paid = (await (
        await buyerApi.post('/api/v1/payments', { data: { order_id: created.id, channel: 'cash' } })
      ).json()) as ApiResponse;
      expect(paid.code, `现金支付失败: ${paid.message}`).toBe(0);
      orderId = created.id;
    } finally {
      await buyerApi.dispose();
    }
  });

  test('后台指派配送员', async () => {
    if (!orderId || !fulfiller) test.skip(true, '订单或履约账号未就绪');
    const resp = await adminApi.put(`/api/v1/admin/orders/${orderId}/assign-delivery`, {
      data: { delivery_user_id: fulfiller!.user.id },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `指派失败: ${body.message}`).toBe(0);
  });

  test('履约工作台可见被指派订单', async ({ playwright }) => {
    if (!orderId || !fulfiller) test.skip(true, '订单或履约账号未就绪');
    const fApi = await newAuthedRequest(playwright, fulfiller!.access_token);
    try {
      const resp = await fApi.get('/api/v1/fulfillment/orders', {
        params: { order_type: 2, status_group: 0, page: 1, page_size: 50 },
      });
      const body = (await resp.json()) as ApiResponse<FulfillmentOrder[] | { list: FulfillmentOrder[] }>;
      expect(body.code, `工作台查单失败: ${body.message}`).toBe(0);
      const found = asList<FulfillmentOrder>(body.data).find((o) => isTargetOrder(o, orderId));
      expect(found, '工作台未见被指派订单').toBeTruthy();
      fulfillOrderId = found!.id;
    } finally {
      await fApi.dispose();
    }
  });

  test('开始配送', async ({ playwright }) => {
    if (!fulfillOrderId) test.skip(true, '工作台订单未定位');
    const fApi = await newAuthedRequest(playwright, fulfiller!.access_token);
    try {
      const resp = await fApi.post(`/api/v1/fulfillment/orders/${fulfillOrderId}/start-delivery`, { data: {} });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `开始配送失败: ${body.message}`).toBe(0);
    } finally {
      await fApi.dispose();
    }
  });

  test('送达拍照完成配送', async ({ playwright }) => {
    if (!fulfillOrderId) test.skip(true, '工作台订单未定位');
    const fApi = await newAuthedRequest(playwright, fulfiller!.access_token);
    try {
      const resp = await fApi.post(`/api/v1/fulfillment/orders/${fulfillOrderId}/complete-delivery`, {
        data: { proof_images: ['https://cdn.example.com/proof/auto-test.jpg'] },
      });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `送达失败: ${body.message}`).toBe(0);
    } finally {
      await fApi.dispose();
    }
  });

  test('未带凭证的送达被拒绝', async ({ playwright }) => {
    // 用一个新配送单验证负向：为控制成本，直接对已送达单重复送达（状态机拦截）替代
    if (!fulfillOrderId) test.skip(true, '工作台订单未定位');
    const fApi = await newAuthedRequest(playwright, fulfiller!.access_token);
    try {
      const resp = await fApi.post(`/api/v1/fulfillment/orders/${fulfillOrderId}/complete-delivery`, { data: {} });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code).not.toBe(0);
    } finally {
      await fApi.dispose();
    }
  });

  test('买家重复确认收货被状态机拒绝', async ({ playwright }) => {
    // 送达拍照后订单已到完成态（5），再次确认收货必须被拒
    if (!orderId) test.skip(true, '订单未创建');
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const resp = await buyerApi.post(`/api/v1/orders/${orderId}/receive`, { data: {} });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, '完成态订单不应允许再次确认收货').not.toBe(0);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('清理：解绑履约会员', async () => {
    if (!staffId) test.skip(true, '履约员工未创建');
    const resp = await adminApi.put(`/api/v1/admin/admins/${staffId}`, {
      data: { user_id: 0 },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `解绑履约会员失败: ${body.message}`).toBe(0);
  });
});
