import { test, expect } from '@playwright/test';
import { newAdminContext, type ApiResponse } from '../helpers/api';
import { autoName } from '../helpers/data';

/**
 * 模块 87：附件访问域名（visit_domain）口径
 * 覆盖 2026-10-29 行为变更：运行期「按首请求域名补写入库」已删除，visit_domain
 * 只接受显式配置（后台手填 / 站点设置 base_url / SITE_BASE_URL 联动）。
 * 回归点：
 *  1. visit_domain 为空（未做显式配置的常态）时，附件出库为站内相对路径 /uploads/<path>
 *     （随访问域名解析，任何入口开箱即用，不出现「首访者域名抢注」的绝对地址）
 *  2. 出库访问不会反向写库：请求后 visit_domain 仍为空（旧实现的回归即表现为被补写）
 *  3. 显式配置 visit_domain 后出库按配置域名拼接完整 URL
 * 边界：商品出库（admin 详情与 C 端列表）统一走 product 服务 ToMediaURL，
 *       与附件配置读取共用同一套口径；自造商品不依赖基线数据，用后清理
 * 乐观锁：附件配置整份覆盖式保存须携带 updated_at（同模块 60）
 */

interface AttachmentConfig {
  storage_type?: string;
  visit_domain?: string;
  updated_at?: string;
  [k: string]: unknown;
}

interface AdminProduct {
  id: number;
  cover_image?: string;
  name?: string;
  [k: string]: unknown;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let original: AttachmentConfig;
let productId = 0;
let catId = 0;
let productName = '';
/** 自造商品的封面图入库相对路径（不含 uploads 前缀） */
const coverRelPath = 'images/auto-test/87-visit-domain.png';
/** 空域名降级形态 = 站内绝对路径 */
const fallbackCover = `/uploads/${coverRelPath}`;
/** 显式域名拼接形态 */
const explicitDomain = 'http://visit-domain-explicit.example.com';
const explicitCover = `${explicitDomain}/uploads/${coverRelPath}`;

/** 读取附件配置当前版本号（乐观锁提交需要） */
async function currentVersion(): Promise<string> {
  const resp = await adminApi.get('/api/v1/admin/settings/attachment');
  const body = (await resp.json()) as ApiResponse<AttachmentConfig>;
  expect(body.code, `读取附件配置版本号失败: ${body.message}`).toBe(0);
  return body.data?.updated_at ?? '';
}

/** 以 beforeAll 快照为底、携带最新版本号构造提交体 */
async function payload(overrides: AttachmentConfig = {}): Promise<AttachmentConfig> {
  return { ...original, updated_at: await currentVersion(), ...overrides };
}

test.describe.serial('附件访问域名口径', () => {
  test.beforeAll(async ({ playwright }) => {
    const admin = await newAdminContext(playwright);
    adminApi = admin.api;

    // 快照附件配置并清空 visit_domain（模拟未做显式配置的全新部署）
    const resp = await adminApi.get('/api/v1/admin/settings/attachment');
    const body = (await resp.json()) as ApiResponse<AttachmentConfig>;
    expect(body.code, `读取附件配置失败: ${body.message}`).toBe(0);
    original = body.data ?? {};
    const clear = await adminApi.put('/api/v1/admin/settings/attachment', {
      data: await payload({ visit_domain: '' }),
    });
    expect((await clear.json()) as ApiResponse).toMatchObject({ code: 0 });

    // 自造专用分类 + 单规格商品（cover_image 存相对路径），不依赖基线数据
    const catResp = await adminApi.post('/api/v1/admin/categories', {
      data: { name: autoName('附件域名口径'), sort: 97 },
    });
    catId = ((await catResp.json()) as ApiResponse<{ id: number }>).data!.id;

    productName = autoName('附件域名口径商品');
    const productResp = await adminApi.post('/api/v1/admin/products', {
      data: {
        category_id: catId,
        name: productName,
        description: '自动化测试：visit_domain 出库口径验证',
        unit: '份',
        is_multi_spec: false,
        cover_image: coverRelPath,
        sku_items: [{ price: 1, cost_price: 1, stock: 10, safety_stock: 1 }],
      },
    });
    const productBody = (await productResp.json()) as ApiResponse<{ product?: AdminProduct; id?: number }>;
    expect(productBody.code, `创建测试商品失败: ${productBody.message}`).toBe(0);
    productId = productBody.data?.product?.id ?? productBody.data?.id ?? 0;
    expect(productId).toBeGreaterThan(0);
  });

  test.afterAll(async () => {
    if (adminApi) {
      // 恢复 visit_domain 快照，删除自造数据（逆序）
      if (productId) await adminApi.delete(`/api/v1/admin/products/${productId}`);
      if (catId) await adminApi.delete(`/api/v1/admin/categories/${catId}`);
      await adminApi.put('/api/v1/admin/settings/attachment', { data: await payload() });
      await adminApi.dispose();
    }
  });

  test('空 visit_domain 出库为站内相对路径（随访问域名解析）', async () => {
    const resp = await adminApi.get(`/api/v1/admin/products/${productId}`);
    const body = (await resp.json()) as ApiResponse<{ product?: AdminProduct }>;
    expect(body.code, `读取商品详情失败: ${body.message}`).toBe(0);
    // 详情响应结构 { product, skus, specs }；关键断言：不含任何绝对域名（旧缺陷形态是拼出 http://localhost:18080/...）
    expect(body.data?.product?.cover_image).toBe(fallbackCover);
  });

  test('出库访问后 visit_domain 仍为空（访问不触发补写入库）', async () => {
    // 上一个用例已发生真实出库请求；旧实现在此窗口内会把「首请求域名」写库
    const resp = await adminApi.get('/api/v1/admin/settings/attachment');
    const body = (await resp.json()) as ApiResponse<AttachmentConfig>;
    expect(body.code).toBe(0);
    expect(body.data?.visit_domain ?? '').toBe('');
  });

  test('显式配置 visit_domain 后按配置域名拼接', async () => {
    const save = await adminApi.put('/api/v1/admin/settings/attachment', {
      data: await payload({ visit_domain: explicitDomain }),
    });
    expect((await save.json()) as ApiResponse).toMatchObject({ code: 0 });

    const resp = await adminApi.get(`/api/v1/admin/products/${productId}`);
    const body = (await resp.json()) as ApiResponse<{ product?: AdminProduct }>;
    expect(body.code).toBe(0);
    expect(body.data?.product?.cover_image).toBe(explicitCover);
  });
});
