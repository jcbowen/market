import { test, expect } from '@playwright/test';
import { newAdminContext, assertOk, asList, newAuthedRequest, adminLogin, type ApiResponse, type PageData } from '../helpers/api';
import { autoName } from '../helpers/data';

/**
 * 模块 66：供应商维护与引用保护
 * 覆盖：
 *   - CRUD /api/v1/admin/inventory/suppliers（POST/PUT/DELETE/GET 列表）
 *   - keyword 命中名称、联系人、电话三列（DB 侧 OR LIKE）
 *   - 引用保护：被采购单或入库单引用的供应商禁止删除，只能停用（PUT status=0）
 *   - 社区隔离：请求语境社区与供应商归属社区不一致时拒绝改删
 *   - 真实语义记录（非缺陷判定，按现状锁定）：
 *     ① PUT 为全量覆盖，proto3 数值字段缺省即 0，只传 name 会把 status 静默置为停用并清空
 *        联系人/电话/地址/备注；
 *     ② 列表 status 过滤仅在 status>0 时生效，status=0 与不传等价（返回含停用项的全量），
 *        即无法通过该参数单独筛出停用供应商；
 *     ③ 名称无唯一性约束；供应商不存在与跨社区均以 InvalidArgument 返回（映射 10001 而非 10003）
 * 数据惯例：被引用的供应商无法删除，本轮留 1 条档案在库（名称带 AUTO-TEST 与本轮 tag 可识别）；
 * 商品与分类在 afterAll 清理
 */

/** 业务参数错误码 */
const PARAM_ERROR = 10001;

interface SupplierRow {
  id: number;
  name?: string;
  contact_name?: string;
  phone?: string;
  address?: string;
  remark?: string;
  status?: number;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let communityId = 0;
const tag = Date.now();
let categoryId = 0;
let skuId = 0;
let productId = 0;
/** 供引用保护用例使用，删除被拒后留档 */
let referencedSupplierId = 0;
/** 供删除成功用例使用 */
let deletableSupplierId = 0;

/**
 * 构造供应商全量字段（PUT 为覆盖语义，未传字段会被清零）
 * - 函数名: supplierPayload
 * - 参数: name — 供应商名；suffix — 联系方式后缀，便于 keyword 命中
 * - 返回: Record<string, unknown> 可直接作为 POST/PUT 请求体
 * - 异常: 无
 */
function supplierPayload(name: string, suffix: string): Record<string, unknown> {
  return {
    name,
    contact_name: `联系人${suffix}`,
    phone: `1390000${suffix}`,
    address: `AUTO-TEST 地址 ${suffix}`,
    remark: `AUTO-TEST 备注 ${suffix}`,
    status: 1,
  };
}

/**
 * 查询供应商列表并返回命中指定 ID 的行
 * - 函数名: findSupplier
 * - 参数: params — 查询参数（keyword/status/page_size 等）；id — 目标供应商 ID
 * - 返回: Promise<SupplierRow | undefined>
 * - 异常: 接口业务失败时由 assertOk 抛出
 */
async function findSupplier(params: Record<string, string | number>, id: number): Promise<SupplierRow | undefined> {
  const body = (await (await adminApi.get('/api/v1/admin/inventory/suppliers', { params })).json()) as ApiResponse<
    PageData<SupplierRow>
  >;
  return asList<SupplierRow>(assertOk(body, '供应商列表')).find((r) => r.id === id);
}

/**
 * 提交供应商写操作并返回原始响应
 * - 函数名: submitSupplier
 * - 参数: method — 'post' | 'put' | 'delete'；path — 请求路径；data — 请求体（DELETE 可省）
 * - 返回: Promise<ApiResponse> 业务码交由调用方断言
 * - 异常: 无
 */
async function submitSupplier(method: 'post' | 'put' | 'delete', path: string, data?: unknown): Promise<ApiResponse> {
  const resp =
    method === 'delete' ? await adminApi.delete(path) : await adminApi[method](path, { data: data ?? {} });
  return (await resp.json()) as ApiResponse;
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  communityId = admin.communityId;

  try {
    categoryId = assertOk<{ id: number }>(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('供应商分类') } })).json()) as ApiResponse<{ id: number }>,
      '创建分类',
    ).id;
    const created = assertOk<{ product?: { id: number }; id?: number }>(
      (await (
        await adminApi.post('/api/v1/admin/products', {
          data: {
            category_id: categoryId,
            name: autoName('供应商商品'),
            unit: '件',
            is_multi_spec: false,
            virtual_sales: 0,
            sku_items: [{ price: 9.9, stock: 10, safety_stock: 0 }],
          },
        })
      ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
      '创建商品',
    );
    productId = created.product?.id ?? created.id ?? 0;
    const skus = assertOk<{ id: number }[]>(
      (await (await adminApi.get('/api/v1/skus', { params: { product_id: productId } })).json()) as ApiResponse<{ id: number }[]>,
      'SKU 列表',
    );
    skuId = (skus ?? [])[0]?.id ?? 0;
  } catch (e) {
    // 假绿收敛：栈已起时造数失败即回归，显式失败而非静默 skip
    throw new Error(`[66] 造数失败：${e instanceof Error ? e.message : e}`);
  }
});

test.afterAll(async () => {
  try {
    if (productId) await adminApi.delete(`/api/v1/admin/products/${productId}`);
    if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

test.describe.serial('供应商维护与引用保护', () => {
  test('1. 创建供应商：默认启用并回填全量字段', async () => {
    test.skip(categoryId === 0, '造数未完成');
    const name = autoName('供应商基础');
    const body = await submitSupplier('post', '/api/v1/admin/inventory/suppliers', supplierPayload(name, '1'));
    expect(body.code, `创建供应商失败: ${body.message}`).toBe(0);
    const sp = body.data as SupplierRow;
    expect(sp.id, '应返回供应商 ID').toBeGreaterThan(0);
    expect(sp.name).toBe(name);
    expect(sp.contact_name).toBe('联系人1');
    expect(sp.status, '新建默认启用').toBe(1);
    deletableSupplierId = sp.id;
  });

  test('2. 名称必填：空名以「供应商名称不能为空」拒绝', async () => {
    test.skip(categoryId === 0, '造数未完成');
    const body = await submitSupplier('post', '/api/v1/admin/inventory/suppliers', { ...supplierPayload('', '2'), name: '' });
    expect(body.code, '空名应被拒绝').toBe(PARAM_ERROR);
    expect(body.message).toContain('供应商名称不能为空');
  });

  test('3. keyword 命中名称/联系人/电话三列', async () => {
    test.skip(deletableSupplierId === 0, '前置供应商未建立');
    const name = autoName('供应商三列');
    const body = await submitSupplier('post', '/api/v1/admin/inventory/suppliers', supplierPayload(name, '3'));
    expect(body.code, `创建三列供应商失败: ${body.message}`).toBe(0);
    const id = (body.data as SupplierRow).id;

    for (const kw of [name, '联系人3', '13900003']) {
      const row = await findSupplier({ keyword: kw, page_size: 50 }, id);
      expect(row, `keyword「${kw}」应命中新建供应商`).toBeTruthy();
    }

    const miss = await findSupplier({ keyword: '不存在的供应商关键词XYZ', page_size: 50 }, id);
    expect(miss, '无关 keyword 不应命中').toBeFalsy();

    await submitSupplier('delete', `/api/v1/admin/inventory/suppliers/${id}`);
  });

  test('4. PUT 全量覆盖语义：仅传 name 会静默停用并清空其余字段', async () => {
    test.skip(deletableSupplierId === 0, '前置供应商未建立');
    const newName = autoName('供应商仅改名');
    const body = await submitSupplier('put', `/api/v1/admin/inventory/suppliers/${deletableSupplierId}`, { name: newName });
    expect(body.code, `仅传 name 的 PUT 应成功: ${body.message}`).toBe(0);
    const row = await findSupplier({ page_size: 50, keyword: newName }, deletableSupplierId);
    expect(row?.name, '名称应更新').toBe(newName);
    expect(row?.status ?? -1, 'status 缺省即 0，PUT 会把供应商置为停用').toBe(0);
    expect(row?.contact_name ?? '', '联系人应被清空').toBe('');
    expect(row?.phone ?? '', '电话应被清空').toBe('');
  });

  test('5. 停用与启用：PUT 传 status 生效', async () => {
    test.skip(deletableSupplierId === 0, '前置供应商未建立');
    const payload = supplierPayload(autoName('供应商停用态'), '5');
    const off = await submitSupplier('put', `/api/v1/admin/inventory/suppliers/${deletableSupplierId}`, {
      ...payload,
      status: 0,
    });
    expect(off.code, `停用应成功: ${off.message}`).toBe(0);
    expect(((off.data as SupplierRow)?.status ?? -1), '响应应回显停用').toBe(0);

    const on = await submitSupplier('put', `/api/v1/admin/inventory/suppliers/${deletableSupplierId}`, payload);
    expect(on.code, `重新启用应成功: ${on.message}`).toBe(0);
    expect((on.data as SupplierRow)?.status, '响应应回显启用').toBe(1);
  });

  test('6. status 过滤仅在 status>0 生效：status=0 与不传等价', async () => {
    test.skip(deletableSupplierId === 0, '前置供应商未建立');
    const all = assertOk<PageData<SupplierRow>>(
      (await (await adminApi.get('/api/v1/admin/inventory/suppliers', { params: { page_size: 100 } })).json()) as ApiResponse<
        PageData<SupplierRow>
      >,
      '不带状态过滤',
    );
    const noFilter = asList<SupplierRow>(all).length;

    const zero = assertOk<PageData<SupplierRow>>(
      (await (await adminApi.get('/api/v1/admin/inventory/suppliers', { params: { page_size: 100, status: 0 } })).json()) as ApiResponse<
        PageData<SupplierRow>
      >,
      'status=0 过滤',
    );
    expect(asList<SupplierRow>(zero).length, 'status=0 不应筛出停用项，应与不传一致').toBe(noFilter);

    const enabled = assertOk<PageData<SupplierRow>>(
      (await (await adminApi.get('/api/v1/admin/inventory/suppliers', { params: { page_size: 100, status: 1 } })).json()) as ApiResponse<
        PageData<SupplierRow>
      >,
      'status=1 过滤',
    );
    expect(asList<SupplierRow>(enabled).every((r) => r.status === 1), 'status=1 结果应全为启用').toBe(true);
  });

  test('7. 引用保护：被采购单引用的供应商不可删除，但可停用', async () => {
    test.skip(skuId === 0, '造数未完成');
    const name = autoName('供应商被引用');
    const created = await submitSupplier('post', '/api/v1/admin/inventory/suppliers', supplierPayload(name, '7'));
    expect(created.code, `创建被引用供应商失败: ${created.message}`).toBe(0);
    referencedSupplierId = (created.data as SupplierRow).id;

    const po = await submitSupplier('post', '/api/v1/admin/inventory/purchase-orders', {
      supplier_id: referencedSupplierId,
      remark: `AUTO-TEST 供应商引用 ${tag}`,
      items: [{ sku_id: skuId, quantity: 1, purchase_price: 1 }],
    });
    expect(po.code, `创建采购单失败: ${po.message}`).toBe(0);

    const del = await submitSupplier('delete', `/api/v1/admin/inventory/suppliers/${referencedSupplierId}`);
    expect(del.code, '被引用供应商应禁止删除').toBe(PARAM_ERROR);
    expect(del.message).toContain('不能删除（可停用）');

    const off = await submitSupplier('put', `/api/v1/admin/inventory/suppliers/${referencedSupplierId}`, {
      ...supplierPayload(name, '7'),
      status: 0,
    });
    expect(off.code, `停用被引用供应商应成功: ${off.message}`).toBe(0);
    expect((off.data as SupplierRow)?.status).toBe(0);
  });

  test('8. 删除未被引用的供应商：成功后列表不再命中', async () => {
    test.skip(deletableSupplierId === 0, '前置供应商未建立');
    const del = await submitSupplier('delete', `/api/v1/admin/inventory/suppliers/${deletableSupplierId}`);
    expect(del.code, `删除未被引用供应商失败: ${del.message}`).toBe(0);
    expect(await findSupplier({ keyword: '供应商', page_size: 100 }, deletableSupplierId), '列表中不应再存在该供应商').toBeFalsy();
  });

  test('9. 供应商不存在：以参数错误返回「供应商不存在」', async () => {
    test.skip(categoryId === 0, '造数未完成');
    const del = await submitSupplier('delete', '/api/v1/admin/inventory/suppliers/999999999');
    expect(del.code, '删除不存在供应商应报错').toBe(PARAM_ERROR);
    expect(del.message).toContain('供应商不存在');

    const put = await submitSupplier('put', '/api/v1/admin/inventory/suppliers/999999999', supplierPayload('不存在供应商', '9'));
    expect(put.code, '更新不存在供应商应报错').toBe(PARAM_ERROR);
    expect(put.message).toContain('供应商不存在');

    const badId = await submitSupplier('delete', '/api/v1/admin/inventory/suppliers/abc');
    expect(badId.code, '非数字 ID 应在网关拦截').toBe(PARAM_ERROR);
  });

  test('10. 社区隔离：切换社区语境后不可改删他社区供应商', async ({ playwright }) => {
    test.skip(referencedSupplierId === 0, '前置供应商未建立');
    const info = await adminLogin(playwright.request);
    const probe = await newAuthedRequest(playwright, info.access_token);
    const communitiesBody = (await (await probe.get('/api/v1/admin/my/communities')).json()) as ApiResponse<{
      id: number;
      name?: string;
    }>;
    await probe.dispose();
    const communities = asList<{ id: number }>(communitiesBody.data);
    expect(communitiesBody.code, `读取社区列表失败: ${communitiesBody.message}`).toBe(0);

    const other = communities.find((c) => c.id !== communityId);
    test.skip(!other, '环境缺少第二个社区，无法验证跨社区拦截');

    const ctx = await playwright.request.newContext({
      extraHTTPHeaders: { Authorization: `Bearer ${info.access_token}`, 'X-Community-Id': String(other!.id) },
    });
    try {
      const del = (await (await ctx.delete(`/api/v1/admin/inventory/suppliers/${referencedSupplierId}`)).json()) as ApiResponse;
      expect(del.code, '跨社区删除应被拒绝').toBe(PARAM_ERROR);
      expect(del.message).toContain('供应商不属于当前社区');

      const put = (await (
        await ctx.put(`/api/v1/admin/inventory/suppliers/${referencedSupplierId}`, { data: supplierPayload('跨社区改名', '10') })
      ).json()) as ApiResponse;
      expect(put.code, '跨社区更新应被拒绝').toBe(PARAM_ERROR);
      expect(put.message).toContain('供应商不属于当前社区');
    } finally {
      await ctx.dispose();
    }
  });

  test('11. 名称无唯一性约束：同名供应商可重复创建', async () => {
    test.skip(categoryId === 0, '造数未完成');
    const name = autoName('供应商重名');
    const first = await submitSupplier('post', '/api/v1/admin/inventory/suppliers', supplierPayload(name, '11'));
    expect(first.code, `首次创建失败: ${first.message}`).toBe(0);
    const second = await submitSupplier('post', '/api/v1/admin/inventory/suppliers', supplierPayload(name, '11'));
    expect(second.code, `同名再次创建应成功: ${second.message}`).toBe(0);

    for (const id of [(first.data as SupplierRow).id, (second.data as SupplierRow).id]) {
      await submitSupplier('delete', `/api/v1/admin/inventory/suppliers/${id}`);
    }
  });
});
