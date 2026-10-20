import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  createFulfillerStaff,
  newAdminContext,
  newAuthedRequest,
  pickSku,
  ensureStock,
  removeFulfillerStaff,
  assertOk,
  asList,
  type ApiResponse,
  type ClientLoginInfo,
  type SkuRef,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 32：履约协同进阶（签收凭证与配送员姓名快照可见性）
 * 覆盖：后台指派 → 开始配送 → 送达拍照（强制 1~N 张）→
 *       C 端订单详情可见签收凭证（delivery_proof_images）与配送员姓名快照（delivery_user_name）；
 *       后台订单详情同样可见（需求：订单列表/详情展示配送员与签收凭证图片）
 * 前置：履约人员与买家均为 C 端账号；履约身份 = 本社区任职且绑定会员的员工
 *       （is_fulfiller 标记已废弃并在启动时 DropColumn，后台无独立履约开关接口），
 *       故身份通过 createFulfillerStaff 造、结束后解绑回收。
 *       造数失败一律抛错或让用例失败，不再 try/catch 置空后 test.skip。
 */

const PROOF_IMAGES = ['https://cdn.example.com/proof/auto-32-1.jpg', 'https://cdn.example.com/proof/auto-32-2.jpg'];

interface OrderResult {
  id: number;
  order_no: string;
}

interface FulfillmentOrder {
  id: number;
  order_id?: number;
  status?: number;
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

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let fulfiller: ClientLoginInfo | null = null;
/** 绑定履约会员的员工账号 ID（创建用例写入，afterAll 回收） */
let staffId = 0;
let buyerToken = '';
let buyerCommunityId = 0;
let sku: SkuRef | null = null;

test.beforeAll(async ({ playwright }) => {
  fulfiller = await newClientUser(playwright);
  const buyer = await newClientUser(playwright);
  buyerToken = buyer.access_token;
  buyerCommunityId = buyer.user.community_id;
  const admin = await newAdminContext(playwright, undefined, undefined, buyerCommunityId);
  adminApi = admin.api;
  // 不包 try/catch：环境无在售 SKU 或补库存失败时必须让整轮红
  sku = await pickSku(adminApi, undefined, buyerCommunityId);
  await ensureStock(adminApi, sku.id, 100);
});

test.afterAll(async () => {
  // 回收本轮员工：解绑会员（履约身份必须即时失效）后尽力删除账号
  if (adminApi && staffId) {
    for (const warn of await removeFulfillerStaff(adminApi, staffId)) console.warn(`[32] ${warn}`);
  }
  await adminApi?.dispose();
});

test.describe.serial('签收凭证与配送员姓名快照', () => {
  let orderId = 0;
  let fulfillOrderId = 0;

  test('履约人员设置昵称（姓名快照取昵称，直登新号默认无昵称）', async ({ playwright }) => {
    const fApi = await newAuthedRequest(playwright, fulfiller!.access_token);
    try {
      const resp = await fApi.put('/api/v1/user/profile', {
        data: { nickname: `配送员32-${Date.now() % 100000}` },
      });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `设置昵称失败: ${body.message}`).toBe(0);
    } finally {
      await fApi.dispose();
    }
  });

  test('创建员工并绑定履约会员（即成为本社区履约人员）', async () => {
    staffId = await createFulfillerStaff(adminApi, buyerCommunityId, fulfiller!.user.id);
  });

  test('买家下配送单并现金支付', async ({ playwright }) => {
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const addrResp = (await (await buyerApi.post('/api/v1/user/addresses', {
        data: { contact_name: 'AUTO-TEST 32', contact_phone: '13800001234', detail: '测试路 32 号' },
      })).json()) as ApiResponse<{ id: number }>;
      assertOk(addrResp, '创建地址');

      // 配送单受起送金额（15 元）约束，按 SKU 单价估算数量
      const skuResp = (await (await buyerApi.get(`/api/v1/skus/${sku!.id}`)).json()) as ApiResponse<{ price: number }>;
      const price = skuResp.data?.price ?? 0;
      const qty = price > 0 ? Math.max(2, Math.ceil(15 / price)) : 2;

      const created = assertOk(
        (await (
          await buyerApi.post('/api/v1/orders', {
            data: { delivery_type: 2, address_id: addrResp.data!.id, remark: 'AUTO-TEST 32 凭证', items: [{ sku_id: sku!.id, quantity: qty }] },
          })
        ).json()) as ApiResponse<OrderResult>,
        'C 端下单',
      );
      const paid = (await (await buyerApi.post('/api/v1/payments', { data: { order_id: created.id, channel: 'cash' } })).json()) as ApiResponse;
      expect(paid.code, `现金支付失败: ${paid.message}`).toBe(0);
      orderId = created.id;
    } finally {
      await buyerApi.dispose();
    }
  });

  test('后台指派配送员 → 详情可见配送员姓名快照', async () => {
    const assign = (await (await adminApi.put(`/api/v1/admin/orders/${orderId}/assign-delivery`, {
      data: { delivery_user_id: fulfiller!.user.id },
    })).json()) as ApiResponse;
    expect(assign.code, `指派失败: ${assign.message}`).toBe(0);
    const detail = (await (await adminApi.get(`/api/v1/admin/orders/${orderId}`)).json()) as ApiResponse<{
      delivery_user_name?: string;
    }>;
    expect(detail.data?.delivery_user_name, '指派后详情应有配送员姓名快照').toBeTruthy();
  });

  test('履约工作台开始配送', async ({ playwright }) => {
    const fApi = await newAuthedRequest(playwright, fulfiller!.access_token);
    try {
      const listResp = (await (await fApi.get('/api/v1/fulfillment/orders', {
        params: { order_type: 2, status_group: 0, page: 1, page_size: 50 },
      })).json()) as ApiResponse<FulfillmentOrder[] | { list: FulfillmentOrder[] }>;
      assertOk(listResp, '工作台查单');
      const found = asList<FulfillmentOrder>(listResp.data).find((o) => o.id === orderId || o.order_id === orderId);
      expect(found, '工作台未见被指派订单').toBeTruthy();
      fulfillOrderId = found!.id;
      const start = (await (await fApi.post(`/api/v1/fulfillment/orders/${fulfillOrderId}/start-delivery`, { data: {} })).json()) as ApiResponse;
      expect(start.code, `开始配送失败: ${start.message}`).toBe(0);
    } finally {
      await fApi.dispose();
    }
  });

  test('送达拍照完成（2 张凭证）', async ({ playwright }) => {
    const fApi = await newAuthedRequest(playwright, fulfiller!.access_token);
    try {
      const resp = await fApi.post(`/api/v1/fulfillment/orders/${fulfillOrderId}/complete-delivery`, {
        data: { proof_images: PROOF_IMAGES },
      });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, `送达失败: ${body.message}`).toBe(0);
    } finally {
      await fApi.dispose();
    }
  });

  test('C 端订单详情可见签收凭证与配送员姓名快照', async ({ playwright }) => {
    const buyerApi = await newAuthedRequest(playwright, buyerToken);
    try {
      const detail = (await (await buyerApi.get(`/api/v1/orders/${orderId}`)).json()) as ApiResponse<{
        status: number;
        delivery_user_name?: string;
        delivery_proof_images?: string[];
      }>;
      expect(detail.code).toBe(0);
      expect(detail.data?.status, '送达完成订单应为状态 5').toBe(5);
      expect(detail.data?.delivery_user_name, 'C 端详情应有配送员姓名快照').toBeTruthy();
      expect(detail.data?.delivery_proof_images, 'C 端详情应有签收凭证').toEqual(PROOF_IMAGES);
    } finally {
      await buyerApi.dispose();
    }
  });

  test('后台订单详情同样可见签收凭证与配送员姓名', async () => {
    const detail = (await (await adminApi.get(`/api/v1/admin/orders/${orderId}`)).json()) as ApiResponse<{
      status: number;
      delivery_user_name?: string;
      delivery_proof_images?: string[];
    }>;
    expect(detail.code).toBe(0);
    expect(detail.data?.delivery_user_name).toBeTruthy();
    expect(detail.data?.delivery_proof_images).toEqual(PROOF_IMAGES);
  });
});

