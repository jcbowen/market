import { test, expect, type Playwright } from '@playwright/test';
import { newAdminContext, assertOk, asList, type ApiResponse } from '../helpers/api';
import { autoName, uniqueSuffix } from '../helpers/data';

/**
 * 模块 24：RBAC 进阶（角色/员工/权限上限/社区管理员/门店设置）
 * 覆盖：超管建角色+员工 → 员工有权限 API 200 / 无权限 API 403
 *       → 员工越权建角色拒绝（权限须 ⊆ 自身权限）→ 员工设内置社区管理员角色拒绝
 *       → 员工改社区权限上限拒绝（超管专用）→ 上限收紧后员工 API 即时 403（还原）
 *       → 收紧为空拒绝 → 自建社区上验证社区管理员增删（每社区 1 人自动替换）
 *       → 门店 profile 读写一致
 *       → 社区角色跨社区总览（scope=all 带社区名/任职人数、无归属建角色被拒、跨社区指派文案透传）
 * 注意：主社区权限上限测试后还原原值；社区管理员增删在自建社区上做，测试后删除
 */

interface Role {
  id: number;
  name: string;
  code: string;
  permissions: string[];
  community_id: number;
  [k: string]: unknown;
}

interface AdminVO {
  id: number;
  username: string;
  roles?: { id: number; code: string }[];
  [k: string]: unknown;
}

const STAFF_USER = `t${uniqueSuffix()}`;
const STAFF_PASS = 'Test123456';
const ROLE_PERMS = ['product:view', 'product:update', 'order:view'];

let superApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let primaryCommunity = 0;
let roleId = 0;
let staffId = 0;
let originalCeiling: string[] = [];
let testCommunity = 0;

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  superApi = admin.api;
  primaryCommunity = admin.communityId;
  // 记录主社区当前权限上限（测试后还原）
  try {
    const body = assertOk(
      (await (await superApi.get(`/api/v1/admin/communities/${primaryCommunity}/permissions`)).json()) as ApiResponse<{ permissions: string[] }>,
      '读取社区权限上限',
    );
    originalCeiling = body.permissions ?? [];
  } catch {
    originalCeiling = [];
  }
});

test.afterAll(async () => {
  // 清理：删除自建社区、员工、角色；还原主社区权限上限
  try {
    if (testCommunity) await superApi.delete(`/api/v1/admin/communities/${testCommunity}`);
    if (staffId) await superApi.delete(`/api/v1/admin/admins/${staffId}`);
    if (roleId) await superApi.delete(`/api/v1/admin/roles/${roleId}`);
    if (originalCeiling.length > 0 && primaryCommunity) {
      await superApi.put(`/api/v1/admin/communities/${primaryCommunity}/permissions`, {
        data: { permissions: originalCeiling },
      });
    }
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await superApi?.dispose();
});

test.describe.serial('员工与角色权限边界', () => {
  let staffApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
  let staffCommunity = 0;
  let probeProductId = 0;
  let categoryId = 0;

  test('1. 超管建社区角色与员工，员工可登录', async ({ playwright }) => {
    // 角色须归属员工任职社区（服务端校验角色社区归属，community_id=0 的遗留角色不能用于社区任职）
    const role = assertOk(
      (await (
        await superApi.post('/api/v1/admin/roles', {
          data: { name: autoName('测试角色'), code: `t_${uniqueSuffix()}`, permissions: ROLE_PERMS, description: 'AUTO-TEST', community_id: primaryCommunity },
        })
      ).json()) as ApiResponse<Role>,
      '创建角色',
    );
    roleId = role.id;
    expect(roleId).toBeGreaterThan(0);

    const staff = assertOk(
      (await (
        await superApi.post('/api/v1/admin/admins', {
          data: { username: STAFF_USER, password: STAFF_PASS, real_name: 'AUTO-TEST 员工', phone: '13900002222', role_ids: [roleId] },
        })
      ).json()) as ApiResponse<AdminVO>,
      '创建员工',
    );
    staffId = staff.id;
    expect(staffId).toBeGreaterThan(0);

    const staffCtx = await newAdminContext(playwright, STAFF_USER, STAFF_PASS);
    staffApi = staffCtx.api;
    staffCommunity = staffCtx.communityId;
    expect(staffCommunity).toBe(primaryCommunity);
  });

  test('2. 员工调用有权限的 API 成功（product:view）', async () => {
    const resp = await staffApi.get('/api/v1/admin/products', { params: { page: 1, page_size: 5 } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `员工查商品列表应成功: ${body.message}`).toBe(0);
  });

  test('3. 员工调用无权限的 API 被拒绝 403（coupon:create）', async () => {
    const resp = await staffApi.post('/api/v1/admin/coupons', {
      data: { name: autoName('越权券'), type: 1, threshold: 0, discount_value: 1, total_count: 1, expire_type: 1, valid_days: 1 },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '员工无 coupon:create 应被拒绝').not.toBe(0);
  });

  test('4. 员工越权建角色被拒绝（permissions 超出自身权限）', async () => {
    const resp = await staffApi.post('/api/v1/admin/roles', {
      data: { name: autoName('越权角色'), code: `t_${uniqueSuffix()}`, permissions: ['order:refund', 'order:view'] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '员工建角色含自身没有的权限点应被拒绝').not.toBe(0);
  });

  test('5. 员工给自己分配内置社区管理员角色被拒绝', async () => {
    // 查本社区内置社区管理员角色
    const rolesBody = assertOk(
      (await (await superApi.get('/api/v1/admin/roles', { params: { community_id: primaryCommunity } })).json()) as ApiResponse<Role[]>,
      '查社区角色列表',
    );
    const builtin = asList<Role>(rolesBody).find((r) => r.code === 'community_admin');
    test.skip(!builtin, '社区无内置社区管理员角色');
    const resp = await staffApi.post('/api/v1/admin/admins', {
      data: { username: `t${uniqueSuffix()}`, password: STAFF_PASS, real_name: '越权任职', role_ids: [builtin!.id] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '非超管不得设置社区管理员角色').not.toBe(0);
  });

  test('6. 员工修改社区权限上限被拒绝（超管专用）', async () => {
    const resp = await staffApi.put(`/api/v1/admin/communities/${staffCommunity}/permissions`, {
      data: { permissions: ['product:view'] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '员工不得修改社区权限上限').not.toBe(0);
  });

  test('7. 员工执行有权限的写操作成功（product:update 改商品名）', async ({ playwright }) => {
    // 造一个临时分类+商品供员工修改，测完删除
    const cat = assertOk(
      (await (await superApi.post('/api/v1/admin/categories', { data: { name: autoName('RBAC 分类') } })).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    );
    categoryId = cat.id;
    const prod = assertOk(
      (await (
        await superApi.post('/api/v1/admin/products', {
          data: { category_id: categoryId, name: autoName('RBAC 商品'), unit: '份', is_multi_spec: false, sku_items: [{ price: 1, stock: 5, safety_stock: 1 }] },
        })
      ).json()) as ApiResponse<{ product: { id: number } }>,
      '创建商品',
    );
    probeProductId = prod.product.id;

    const resp = await staffApi.put(`/api/v1/admin/products/${probeProductId}`, {
      data: {
        category_id: categoryId,
        name: autoName('RBAC 商品-已改'),
        unit: '份',
        is_multi_spec: false,
        sku_items: [{ price: 1.2, stock: 5, safety_stock: 1 }],
      },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `员工改商品应成功: ${body.message}`).toBe(0);
  });

  test('8. 上限收紧后员工写操作即时 403（product:update 被剔除）', async () => {
    // 收紧：剔除 product:update
    // 注意：primaryCommunity 取 my/communities 第一个，随环境漂移；存量社区上限可能是
    // ["*"] 通配（历史数据），filter 单项对它无效，必须整体替换为具体权限集才能验证收紧
    const base = originalCeiling.includes('*') ? ['product:view', 'order:view'] : originalCeiling;
    const narrowed = base.filter((p) => p !== 'product:update');
    test.skip(narrowed.length === 0, '原权限上限仅含 product:update，无法安全收紧');
    const put = (await (
      await superApi.put(`/api/v1/admin/communities/${primaryCommunity}/permissions`, { data: { permissions: narrowed } })
    ).json()) as ApiResponse;
    expect(put.code, `收紧权限上限失败: ${put.message}`).toBe(0);

    // 员工再改商品 → 即时 403
    const resp = await staffApi.put(`/api/v1/admin/products/${probeProductId}`, {
      data: {
        category_id: categoryId,
        name: autoName('收紧后不应生效'),
        unit: '份',
        is_multi_spec: false,
        sku_items: [{ price: 1.2, stock: 5, safety_stock: 1 }],
      },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '上限收紧后员工 product:update 应被拒').not.toBe(0);

    // 还原上限
    const restore = (await (
      await superApi.put(`/api/v1/admin/communities/${primaryCommunity}/permissions`, { data: { permissions: originalCeiling } })
    ).json()) as ApiResponse;
    expect(restore.code, `还原权限上限失败: ${restore.message}`).toBe(0);

    // 还原后员工恢复可用
    const ok = await staffApi.put(`/api/v1/admin/products/${probeProductId}`, {
      data: {
        category_id: categoryId,
        name: autoName('RBAC 商品-还原后'),
        unit: '份',
        is_multi_spec: false,
        sku_items: [{ price: 1.2, stock: 5, safety_stock: 1 }],
      },
    });
    const okBody = (await ok.json()) as ApiResponse;
    expect(okBody.code, '还原后员工应恢复写权限').toBe(0);
  });

  test('9. 权限上限收紧为空被拒绝', async () => {
    const resp = await superApi.put(`/api/v1/admin/communities/${primaryCommunity}/permissions`, {
      data: { permissions: [] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '权限上限不可为空').not.toBe(0);
    expect(body.message).toContain('至少保留一个权限点');
  });

  test('10. 清理探针商品与分类', async () => {
    if (probeProductId) await superApi.delete(`/api/v1/admin/products/${probeProductId}`);
    if (categoryId) await superApi.delete(`/api/v1/admin/categories/${categoryId}`);
    probeProductId = 0;
    categoryId = 0;
  });
});

test.describe.serial('社区管理员与门店设置', () => {
  const CM_PASS = 'Cm12345678';

  test('11. 自建社区并设置社区管理员', async () => {
    const comm = assertOk(
      (await (
        await superApi.post('/api/v1/admin/communities', {
          data: { name: autoName('RBAC 测试社区'), address: 'AUTO-TEST 地址', status: 1, delivery_fee: 0, min_order_amount: 0 },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建测试社区',
    );
    testCommunity = comm.id;

    const resp = await superApi.post(`/api/v1/admin/communities/${testCommunity}/admins`, {
      data: { username: `cm${uniqueSuffix()}`, password: CM_PASS, real_name: 'AUTO-TEST 社区管理员', phone: '13900003333' },
    });
    const body = (await resp.json()) as ApiResponse<AdminVO>;
    expect(body.code, `设置社区管理员失败: ${body.message}`).toBe(0);
  });

  test('12. 社区管理员列表含唯一管理员，重复设置自动替换', async () => {
    const list1 = assertOk(
      (await (await superApi.get(`/api/v1/admin/communities/${testCommunity}/admins`)).json()) as ApiResponse<AdminVO[]>,
      '社区管理员列表',
    );
    const admins1 = asList<AdminVO>(list1);
    expect(admins1.length, '每社区仅一名社区管理员').toBe(1);
    const firstName = admins1[0].username;

    // 再次设置：自动替换
    const resp = await superApi.post(`/api/v1/admin/communities/${testCommunity}/admins`, {
      data: { username: `cm${uniqueSuffix()}`, password: CM_PASS, real_name: 'AUTO-TEST 替换管理员', phone: '13900004444' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `替换社区管理员失败: ${body.message}`).toBe(0);

    const list2 = assertOk(
      (await (await superApi.get(`/api/v1/admin/communities/${testCommunity}/admins`)).json()) as ApiResponse<AdminVO[]>,
      '替换后管理员列表',
    );
    const admins2 = asList<AdminVO>(list2);
    expect(admins2.length).toBe(1);
    expect(admins2[0].username, '应替换为新管理员').not.toBe(firstName);
  });

  test('13. 社区管理员可登录并读取门店设置', async ({ playwright }) => {
    const list = assertOk(
      (await (await superApi.get(`/api/v1/admin/communities/${testCommunity}/admins`)).json()) as ApiResponse<AdminVO[]>,
      '社区管理员列表',
    );
    const cm = asList<AdminVO>(list)[0];
    const cmCtx = await newAdminContext(playwright, cm.username, CM_PASS);
    try {
      expect(cmCtx.communityId, '社区管理员登录语境应为任职社区').toBe(testCommunity);
      const profile = assertOk(
        (await (await cmCtx.api.get('/api/v1/admin/community/profile')).json()) as ApiResponse<{ id: number; name: string; delivery_fee: number }>,
        '读取门店设置',
      );
      expect(profile.id).toBe(testCommunity);
    } finally {
      await cmCtx.api.dispose();
    }
  });

  test('14. 社区管理员更新门店设置且回读一致', async ({ playwright }) => {
    const list = assertOk(
      (await (await superApi.get(`/api/v1/admin/communities/${testCommunity}/admins`)).json()) as ApiResponse<AdminVO[]>,
      '社区管理员列表',
    );
    const cm = asList<AdminVO>(list)[0];
    const cmCtx = await newAdminContext(playwright, cm.username, CM_PASS);
    try {
      const fee = 3.5;
      const put = (await (
        await cmCtx.api.put('/api/v1/admin/community/profile', {
          data: { delivery_fee: fee, min_order_amount: 10, business_hours: '08:00-22:00', enable_self_pickup: true, enable_delivery: true },
        })
      ).json()) as ApiResponse;
      expect(put.code, `更新门店设置失败: ${put.message}`).toBe(0);

      const profile = assertOk(
        (await (await cmCtx.api.get('/api/v1/admin/community/profile')).json()) as ApiResponse<{ delivery_fee: number; min_order_amount: number; business_hours: string }>,
        '回读门店设置',
      );
      expect(profile.delivery_fee).toBeCloseTo(fee, 2);
      expect(profile.min_order_amount).toBe(10);
      expect(profile.business_hours).toBe('08:00-22:00');
    } finally {
      await cmCtx.api.dispose();
    }
  });
});

test.describe.serial('社区角色跨社区总览（超管只读入口）', () => {
  test('15. scope=all 返回跨社区角色并回填社区名与任职人数', async () => {
    const all = asList<Role>(
      assertOk((await (await superApi.get('/api/v1/admin/roles', { params: { scope: 'all' } })).json()) as ApiResponse<Role[]>),
    );
    const mine = all.find((r) => r.id === roleId);
    expect(mine, '总览应包含第 1 条用例创建的测试角色').toBeTruthy();
    expect(mine!.community_id, '测试角色应归属主社区').toBe(primaryCommunity);
    expect(mine!.community_name, '跨社区总览应回填所属社区名').toBeTruthy();
    expect(mine!.assignment_count, '测试角色已指派给员工，任职人数应不少于 1').toBeGreaterThanOrEqual(1);

    // 跨社区：社区仍存在时社区名必须与社区列表一致；社区已删除的孤儿角色允许为空（前端回落显示「社区 N」）
    const communities = asList<{ id: number; name: string }>(
      assertOk(
        (await (await superApi.get('/api/v1/admin/communities', { params: { page: 1, page_size: 100 } })).json()) as ApiResponse<unknown>,
        '社区列表',
      ),
    );
    const nameOf = new Map(communities.map((c) => [c.id, c.name]));
    const scoped = all.filter((r) => (r.community_id ?? 0) > 0);
    expect(scoped.length, '总览应覆盖各社区角色').toBeGreaterThan(0);
    scoped
      .filter((r) => nameOf.has(r.community_id))
      .forEach((r) => expect(r.community_name, `角色 ${r.id} 的社区名应与社区列表一致`).toBe(nameOf.get(r.community_id)));
  });

  test('16. 废弃入口 scope=global 明确报错，提示改用 scope=all', async () => {
    const resp = await superApi.get('/api/v1/admin/roles', { params: { scope: 'global' } });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '旧的全局角色入口应被拒绝').not.toBe(0);
    expect(body.message).toContain('scope=all');
  });

  test('17. 不传 community_id 建角色被拒（角色一律社区私有）', async () => {
    const resp = await superApi.post('/api/v1/admin/roles', {
      data: { name: autoName('无归属角色'), code: `t_${uniqueSuffix()}`, permissions: ['product:view'] },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '超管不指定社区时建角色应被拒绝').not.toBe(0);
    expect(body.message, '应提示角色必须归属社区').toContain('社区');
  });

  test('18. 跨社区指派角色被拒且业务文案透传（不再退化为 10002）', async () => {
    const comm = assertOk(
      (await (
        await superApi.post('/api/v1/admin/communities', {
          data: { name: autoName('RBAC 越界社区'), address: 'AUTO-TEST 地址', status: 1, delivery_fee: 0, min_order_amount: 0 },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建越界社区',
    );
    const otherCommunity = comm.id;
    let otherRoleId = 0;
    try {
      const role = assertOk(
        (await (
          await superApi.post('/api/v1/admin/roles', {
            data: { name: autoName('越界角色'), code: `t_${uniqueSuffix()}`, permissions: ['product:view'], community_id: otherCommunity },
          })
        ).json()) as ApiResponse<Role>,
        '在越界社区建角色',
      );
      otherRoleId = role.id;

      // 员工落在超管当前社区语境（primaryCommunity），却指派另一社区的角色 → 应被服务端拒绝
      const resp = await superApi.post('/api/v1/admin/admins', {
        data: { username: `t${uniqueSuffix()}`, password: STAFF_PASS, real_name: 'AUTO-TEST 越界任职', role_ids: [otherRoleId] },
      });
      const body = (await resp.json()) as ApiResponse;
      expect(body.code, '跨社区指派角色应被拒绝').not.toBe(0);
      expect(body.message, '服务端的业务拒绝文案应透传到调用方，而不是笼统的服务器内部错误').toContain('不属于当前社区');
    } finally {
      if (otherRoleId) await superApi.delete(`/api/v1/admin/roles/${otherRoleId}`);
      await superApi.delete(`/api/v1/admin/communities/${otherCommunity}`);
    }
  });
});
