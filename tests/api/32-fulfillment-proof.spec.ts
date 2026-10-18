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
 * 模块 32：履约协同进阶（签收凭证与配送员姓名快照可见性）
 * 覆盖：后台指派 → 开始配送 → 送达拍照（强制 1~N 张）→
 *       C 端订单详情可见签收凭证（delivery_proof_images）与配送员姓名快照（delivery_user_name）；
 *       后台订单详情同样可见（需求：订单列表/详情展示配送员与签收凭证图片）
 * 前置：履约人员与买家均为 C 端账号；履约接口要求操作者为本社区履约人员
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
  try {
    sku = await pickSku(adminApi, undefined, buyerCommunityId);
    await ensureStock(adminApi, sku.id, 100);
  } catch {
    sku = null;
  }
});

test.afterAll(async () => {
  // 恢复履约账号为普通用户
  if (adminApi && fulfiller) {
    await adminApi.put(`/api/v1/admin/users/${fulfiller.user.id}/fulfiller`, { data: { is_fulfiller: false } });
  }
  await adminApi?.dispose();
});

test.describe.serial('签收凭证与配送员姓名快照', () => {
  let orderId = 0;
  let fulfillOrderId = 0;

  test('履约人员设置昵称（姓名快照取昵称，直登新号默认无昵称）', async ({ playwright }) => {
    test.skip(!fulfiller, '履约账号未创建');
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

  test('后台设置履约人员', async () => {
    test.skip(!fulfiller, '履约账号未创建');
    const resp = await adminApi.put(`/api/v1/admin/users/${fulfiller!.user.id}/fulfiller`, {
      data: { is_fulfiller: true },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `设置履约人员失败: ${body.message}`).toBe(0);
  });

  test('买家下配送单并现金支付', async ({ playwright }) => {
    test.skip(!sku || !buyerToken, '环境缺 SKU 或买家');
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
    test.skip(!orderId || !fulfiller, '订单或履约账号未就绪');
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
    test.skip(!orderId || !fulfiller, '订单或履约账号未就绪');
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
    test.skip(!fulfillOrderId, '工作台订单未定位');
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
    test.skip(!orderId, '订单未创建');
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
    test.skip(!orderId, '订单未创建');
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
