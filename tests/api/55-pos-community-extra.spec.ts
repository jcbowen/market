import { test, expect, type Playwright } from '@playwright/test';
import {
  clientLogin,
  newAdminContext,
  pickSku,
  ensureStock,
  assertOk,
  asList,
  type ApiResponse,
  type PageData,
  type ClientLoginInfo,
  type SkuRef,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';

/**
 * 模块 55：POS 与社区补充
 * 覆盖：
 *   - 挂单跨社区删除被拒：社区 A 挂单在社区 B 语境删除 → 10003「挂单不存在或已删除」
 *     （数据仍归属社区 A，回 A 语境可正常删除）
 *   - GET /admin/pos/shifts?operator_id=：开工后按当前操作员过滤严格命中；
 *     operator_id=99999999（不存在）返回空列表
 *   - C 端 GET /communities?longitude=&latitude= LBS 附近社区：双坐标均非 0 才走附近排序
 *     （距离升序，同距离按创建时间 id asc 兜底；固定最近 10 个；仅营业中 status=1）
 *   - 单边坐标（只传经度）回退普通列表（不报错、结构合法）
 * 说明：挂单创建/列表/删除正向、开工交班正向 14 号已覆盖，此处只补隔离与过滤维度
 */

interface HeldOrder {
  id: number;
  community_id?: number;
  [k: string]: unknown;
}

interface ShiftRow {
  id: number;
  operator_id: number;
  community_id: number;
  status: number;
  [k: string]: unknown;
}

interface CommunityRow {
  id: number;
  name: string;
  status: number;
  longitude?: number;
  latitude?: number;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let adminId = 0;
let homeCommunityId = 0;
let otherCommunityId = 0;
let sku: SkuRef | null = null;
let buyer: ClientLoginInfo | null = null;

test.beforeAll(async ({ playwright }) => {
  buyer = await clientLogin(playwright.request, uniquePhone());
  homeCommunityId = buyer.user.community_id;
  const admin = await newAdminContext(playwright, undefined, undefined, homeCommunityId);
  adminApi = admin.api;
  // 当前操作员 ID：/admin/auth/info 返回当前管理员资料
  const info = assertOk(
    (await (await adminApi.get('/api/v1/admin/auth/info')).json()) as ApiResponse<{ id: number }>,
    '获取当前管理员信息',
  );
  adminId = info.id;
  try {
    sku = await pickSku(adminApi);
    await ensureStock(adminApi, sku.id, 20);
  } catch {
    sku = null;
  }
});

test.afterAll(async () => {
  await adminApi?.dispose();
});

test.describe.serial('POS 挂单与班次', () => {
  let heldId = 0;

  test('1. 前置：建隔离社区 B + 社区 A 挂单', async ({ playwright }) => {
    test.skip(!sku, '环境无库存 SKU');
    // 社区 B：挂单跨社区删除的「他店」语境
    const created = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/communities', {
          data: {
            name: `AUTO-TEST 55 隔离社区 ${Date.now()}`,
            address: 'AUTO-TEST 55 地址',
            status: 1,
            delivery_fee: 0,
            min_order_amount: 0,
          },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建隔离社区',
    );
    otherCommunityId = created.id;

    // 社区 A 挂单
    const held = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/pos/held-orders', {
          data: {
            user_id: 0,
            user_name: 'AUTO-TEST 55 挂单顾客',
            remark: 'AUTO-TEST 55 跨社区隔离',
            items: [{ sku_id: sku!.id, quantity: 1, product_name: '挂单商品' }],
          },
        })
      ).json()) as ApiResponse<HeldOrder>,
      '创建挂单',
    );
    heldId = held.id;
    expect(heldId).toBeGreaterThan(0);
  });

  test('2. 社区 B 语境删除社区 A 挂单被拒（10003）', async ({ playwright }) => {
    test.skip(!heldId || !otherCommunityId, '前置未就绪');
    const other = await newAdminContext(playwright, undefined, undefined, otherCommunityId);
    try {
      const del = (await (
        await other.api.delete(`/api/v1/admin/pos/held-orders/${heldId}`)
      ).json()) as ApiResponse;
      expect(del.code, '跨社区删除挂单应被拒').toBe(10003);
      expect(del.message).toContain('挂单不存在或已删除');
    } finally {
      await other.api.dispose();
    }
    // 数据未受影响：社区 A 语境仍可见
    const list = assertOk(
      (await (await adminApi.get('/api/v1/admin/pos/held-orders')).json()) as ApiResponse<
        HeldOrder[] | PageData<HeldOrder>
      >,
      '挂单列表',
    );
    const rows = Array.isArray(list) ? list : asList<HeldOrder>(list);
    expect(rows.some((h) => h.id === heldId), '被拒后挂单应仍在社区 A').toBe(true);
  });

  test('3. 社区 A 语境删除成功（清理）', async () => {
    test.skip(!heldId, '挂单未创建');
    const del = (await (
      await adminApi.delete(`/api/v1/admin/pos/held-orders/${heldId}`)
    ).json()) as ApiResponse;
    expect(del.code, `本社区删除挂单失败: ${del.message}`).toBe(0);
  });

  test('4. shifts?operator_id= 过滤指定操作员', async ({ playwright }) => {
    // 确保有本操作员的班次记录（开工；已有进行中班次属环境残留，记录不失败）
    const start = (await (
      await adminApi.post('/api/v1/admin/pos/shifts/start', { data: { note: 'AUTO-TEST 55 开工' } })
    ).json()) as ApiResponse;
    if (start.code !== 0) console.log(`[55] 开工 code=${start.code}（可能已有进行中班次）`);

    const mine = assertOk(
      (await (
        await adminApi.get('/api/v1/admin/pos/shifts', { params: { page: 1, page_size: 50, operator_id: adminId } })
      ).json()) as ApiResponse<PageData<ShiftRow>>,
      '班次列表(按操作员过滤)',
    );
    const rows = asList<ShiftRow>(mine);
    for (const row of rows) {
      expect(row.operator_id, '过滤 operator_id 应严格命中').toBe(adminId);
      expect(row.community_id, '班次应归属当前社区').toBe(homeCommunityId);
    }

    // 不存在的操作员 → 空列表
    const none = assertOk(
      (await (
        await adminApi.get('/api/v1/admin/pos/shifts', { params: { page: 1, page_size: 50, operator_id: 99999999 } })
      ).json()) as ApiResponse<PageData<ShiftRow>>,
      '班次列表(不存在操作员)',
    );
    expect(asList<ShiftRow>(none), '不存在的操作员应返回空列表').toHaveLength(0);
  });
});

test.describe.serial('C 端 LBS 附近社区', () => {
  test('5. 双坐标非 0 走附近排序：结构合法、非空、仅营业中', async ({ request }) => {
    const body = (await (
      await request.get('/api/v1/communities', {
        params: { longitude: 116.404, latitude: 39.915, page: 1, page_size: 20 },
      })
    ).json()) as ApiResponse<PageData<CommunityRow>>;
    expect(body.code, `附近社区查询失败: ${body.message}`).toBe(0);
    const rows = asList<CommunityRow>(body.data);
    expect(rows.length, '附近社区列表应非空').toBeGreaterThan(0);
    expect(rows.length, '附近排序固定返回最近 10 个').toBeLessThanOrEqual(10);
    for (const row of rows) {
      expect(row.status, 'C 端仅展示营业中社区').toBe(1);
    }
  });

  test('6. 单边坐标回退普通列表（不报错）', async ({ request }) => {
    const body = (await (
      await request.get('/api/v1/communities', { params: { longitude: 116.404, page: 1, page_size: 20 } })
    ).json()) as ApiResponse<PageData<CommunityRow>>;
    expect(body.code, `仅传经度应回退普通列表: ${body.message}`).toBe(0);
    const rows = asList<CommunityRow>(body.data);
    expect(rows.length).toBeGreaterThan(0);
  });
});
