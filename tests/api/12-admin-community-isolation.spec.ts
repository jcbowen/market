import { test, expect } from '@playwright/test';
import { adminLogin, newAdminContext, newAuthedRequest, type ApiResponse, type PageData } from '../helpers/api';
import { autoName } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 12：社区管理与数据隔离
 * 覆盖：社区 CRUD、社区权限读取、跨社区隔离（A 社区管理员的商品/订单不越界）
 * 隔离策略：超管 admin 建测试社区 → 在新社区语境（X-Community-Id）下列商品/订单
 *          → 断言与源社区数据无交集 → 删除测试社区
 */

interface Community {
  id: number;
  name: string;
  [k: string]: unknown;
}

interface Ref {
  id: number;
  [k: string]: unknown;
}

let api: Awaited<ReturnType<typeof newAdminContext>['api']>;
let defaultCommunityId = 0;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const ctx = await newAdminContext(playwright);
  api = ctx.api;
  defaultCommunityId = ctx.communityId;
});

test.afterAll(async () => {
  await api?.dispose();
});

test.describe.serial('社区管理与隔离', () => {
  let testCommunityId = 0;
  let sourceCommunityId = 0;
  // 越权用例的临时角色/普通管理员（用例内赋值，清理用例删除）
  let roleId = 0;
  let adminBId = 0;
  let adminBUsername = '';

  test('准备：确认默认社区可用', async () => {
    const comms = (await (await api.get('/api/v1/admin/communities')).json()) as ApiResponse<PageData<Community>>;
    expect(comms.code).toBe(0);
    expect(defaultCommunityId).toBeGreaterThan(0);
    sourceCommunityId = defaultCommunityId;
  });

  test('超管创建测试社区成功', async () => {
    const resp = await api.post('/api/v1/admin/communities', {
      data: {
        name: autoName('测试社区'),
        address: '自动化测试地址',
        longitude: 114.3,
        latitude: 30.5,
        contact_phone: '13900001111',
        pickup_address: '测试自提点',
        delivery_fee: 3,
        min_order_amount: 20,
        status: 1,
      },
    });
    const body = (await resp.json()) as ApiResponse<{ id?: number }>;
    expect(body.code).toBe(0);
    testCommunityId = body.data?.id ?? 0;
    expect(testCommunityId).toBeGreaterThan(0);
  });

  test('测试社区列表与详情可查', async () => {
    const detail = (await (await api.get(`/api/v1/admin/communities/${testCommunityId}`)).json()) as ApiResponse<Community>;
    expect(detail.code).toBe(0);
    expect(detail.data?.name).toContain('AUTO-TEST');
  });

  test('隔离：新社区语境下商品列表与源社区无交集', async () => {
    const src = (await (
      await api.get('/api/v1/admin/products', { params: { page: 1, page_size: 50 } })
    ).json()) as ApiResponse<PageData<Ref>>;
    const srcIds = new Set((src.data?.list ?? []).map((p) => p.id));

    const isolated = (await (
      await api.get('/api/v1/admin/products', {
        params: { page: 1, page_size: 50 },
        headers: { 'X-Community-Id': String(testCommunityId) },
      })
    ).json()) as ApiResponse<PageData<Ref>>;
    expect(isolated.code).toBe(0);
    const isoIds = (isolated.data?.list ?? []).map((p) => p.id);
    expect(isoIds.every((id) => !srcIds.has(id) || id === 0)).toBe(true);
  });

  test('隔离：新社区语境下订单列表与源社区无交集', async () => {
    const src = (await (
      await api.get('/api/v1/admin/orders', { params: { page: 1, page_size: 50 } })
    ).json()) as ApiResponse<PageData<Ref>>;
    const srcIds = new Set((src.data?.list ?? []).map((o) => o.id));

    const isolated = (await (
      await api.get('/api/v1/admin/orders', {
        params: { page: 1, page_size: 50 },
        headers: { 'X-Community-Id': String(testCommunityId) },
      })
    ).json()) as ApiResponse<PageData<Ref>>;
    expect(isolated.code).toBe(0);
    const isoIds = (isolated.data?.list ?? []).map((o) => o.id);
    expect(isoIds.every((id) => !srcIds.has(id) || id === 0)).toBe(true);
  });

  test('社区权限集可读取', async () => {
    const resp = await api.get(`/api/v1/admin/communities/${testCommunityId}/permissions`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('更新社区资料生效', async () => {
    // UpdateAdminCommunityReq 要求 name 必填，整表覆盖式更新
    const resp = await api.put(`/api/v1/admin/communities/${testCommunityId}`, {
      data: { name: autoName('改名社区'), address: '自动化测试地址', delivery_fee: 5, status: 1 },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('隔离：普通管理员跨社区读单据详情被拒绝', async ({ playwright }: { playwright: Playwright }) => {
    // 1) 建全局角色（含库存查看权限，供普通管理员通过权限中间件，聚焦社区归属校验层）
    const role = (await (
      await api.post('/api/v1/admin/roles', {
        data: { name: autoName('越权测试角色'), code: `auto_test_${Date.now()}`, permissions: ['inventory:view'] },
      })
    ).json()) as ApiResponse<{ id: number }>;
    expect(role.code, `建角色失败: ${role.message}`).toBe(0);
    roleId = role.data!.id;

    // 2) 在测试社区任职的普通管理员（X-Community-Id 声明任职社区，默认社区即该社区）
    adminBUsername = `auto_test_${Date.now()}`;
    const adminB = (await (
      await api.post('/api/v1/admin/admins', {
        headers: { 'X-Community-Id': String(testCommunityId) },
        data: { username: adminBUsername, password: 'AutoTest#123', real_name: '越权测试员', role_ids: [roleId] },
      })
    ).json()) as ApiResponse<{ id: number }>;
    expect(adminB.code, `建管理员失败: ${adminB.message}`).toBe(0);
    adminBId = adminB.data!.id;

    // 3) 超管在源社区语境建入库单（归属源社区）
    const skus = (await (
      await api.get('/api/v1/admin/inventory/skus', { params: { page: 1, page_size: 10 } })
    ).json()) as ApiResponse<PageData<{ id?: number; sku_id?: number; stock: number }>>;
    const sku = (skus.data?.list ?? []).find((s) => s.stock > 0);
    test.skip(!sku, '环境无库存 SKU，跳过越权用例');
    const bill = (await (
      await api.post('/api/v1/admin/inventory/in-orders', {
        data: { remark: 'AUTO-TEST 越权源单', items: [{ sku_id: sku!.id ?? sku!.sku_id, quantity: 1 }] },
      })
    ).json()) as ApiResponse<{ id: number }>;
    expect(bill.code, `建入库单失败: ${bill.message}`).toBe(0);
    const billId = bill.data!.id;

    // 4) 普通管理员（任职测试社区）读源社区单据 → 社区归属校验拒绝(11005)；
    //    若权限中间件先行拦截（社区权限上限截断 inventory:view），同样返回 11005，业务语义一致
    const bInfo = await adminLogin(playwright.request, adminBUsername, 'AutoTest#123');
    const bApi = await newAuthedRequest(playwright, bInfo.access_token);
    try {
      const denied = (await (await bApi.get(`/api/v1/admin/inventory/in-orders/${billId}`)).json()) as ApiResponse;
      expect(denied.code, `跨社区读单据应被拒，实际 ${denied.code} ${denied.message}`).toBe(11005);
    } finally {
      await bApi.dispose();
    }

    // 5) 对照：超管读同单据成功（证明拒绝源于越权而非接口故障）
    const ok = (await (await api.get(`/api/v1/admin/inventory/in-orders/${billId}`)).json()) as ApiResponse;
    expect(ok.code, `超管读单据应成功: ${ok.message}`).toBe(0);
  });

  test('清理：删除越权测试管理员与角色', async () => {
    if (adminBId) {
      const r = (await (await api.delete(`/api/v1/admin/admins/${adminBId}`)).json()) as ApiResponse;
      console.log(`[越权用例] 删除管理员 ${adminBId} code=${r.code}`);
    }
    if (roleId) {
      const r = (await (await api.delete(`/api/v1/admin/roles/${roleId}`)).json()) as ApiResponse;
      console.log(`[越权用例] 删除角色 ${roleId} code=${r.code}`);
    }
  });

  test('清理：删除测试社区', async () => {
    const resp = await api.delete(`/api/v1/admin/communities/${testCommunityId}`);
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('空名称社区被拒绝', async () => {
    const resp = await api.post('/api/v1/admin/communities', { data: { name: '' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
  });
});
