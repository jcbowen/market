import { test, expect } from '@playwright/test';
import { newAdminContext, assertOk, type ApiResponse } from '../helpers/api';
import { autoName } from '../helpers/data';

/**
 * 模块 64：商品行内改名接口
 * 覆盖：PUT /api/v1/admin/products/:id/name（网关权限点 product:update）
 *   - 正常改名：响应回显入参，详情接口回读落库值
 *   - 校验分支：空串被网关 binding:"required" 拒绝；纯空格经服务层 TrimSpace 后判空；
 *     超过 100 字（按 rune 计数，中文 1 字 = 1）拒绝；恰好 100 字为可通过边界
 *   - 归属校验：不存在或已删除（status=2）的商品统一「商品不存在或无权操作」
 *   - 路径参数非数字 / 非正数在网关 ParseInt 阶段拦截，不进服务层
 *   - 名称无唯一性约束：与既有商品同名允许（如实反映当前语义，非缺陷判定）
 * 错误码口径：服务层 bizerr InvalidArgument→10001、NotFound→10003，
 * 网关 TranslateGRPCError 对这两个有业务语义的状态码原样透传中文文案
 * 已知语义差异：响应 data.name 回显的是未经裁剪的入参原串，落库值才去首尾空格，
 * 用例 6 以此建立断言，避免前端误用响应回显值展示
 * 说明：自建分类与 2 个商品隔离数据，afterAll 逆序清理
 */

interface ProductRow {
  id: number;
  name: string;
  status?: number;
  [k: string]: unknown;
}

/** 参数错误（网关 binding 与服务层 InvalidArgument 同口径） */
const PARAM_ERROR = 10001;
/** 资源不存在 */
const NOT_FOUND = 10003;
/** 名称长度上限（服务层按 rune 计数） */
const NAME_MAX = 100;

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let categoryId = 0;
/** 改名主对象 */
let productId = 0;
/** 同名用例的参照商品，仅用于证明重名不报错 */
let otherProductId = 0;

/**
 * 创建一条商品记录并直接置为在售
 * - 函数名: createProduct
 * - 参数: label — 名称标识，用于区分同批造数
 * - 返回: Promise<number> 新商品 ID
 * - 异常: 建品或上架业务失败时抛出 Error（造数前置，不作为用例断言）
 * - 使用示例: const id = await createProduct('改名主对象');
 */
async function createProduct(label: string): Promise<number> {
  const created = assertOk<{ product?: { id: number }; id?: number }>(
    (await (
      await adminApi.post('/api/v1/admin/products', {
        data: {
          category_id: categoryId,
          name: autoName(`改名商品${label}`),
          unit: '份',
          is_multi_spec: false,
          sku_items: [{ price: 3.5, stock: 5, safety_stock: 0 }],
        },
      })
    ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
    '创建商品',
  );
  const id = created.product?.id ?? created.id ?? 0;
  if (!id) throw new Error('创建商品未返回 ID');
  const on = (await (
    await adminApi.post(`/api/v1/admin/products/${id}/status`, { data: { status: 1 } })
  ).json()) as ApiResponse;
  if (on.code !== 0) throw new Error(`上架失败 code=${on.code} message=${on.message}`);
  return id;
}

/**
 * 读取商品详情中的名称
 * - 函数名: readName
 * - 参数: id — 商品 ID
 * - 返回: Promise<string> 落库名称，详情缺失时返回空串
 * - 异常: 详情接口业务失败时由 assertOk 抛出
 */
async function readName(id: number): Promise<string> {
  const body = (await (await adminApi.get(`/api/v1/admin/products/${id}`)).json()) as ApiResponse<{
    product?: ProductRow;
  }>;
  return assertOk(body, '商品详情').product?.name ?? '';
}

/**
 * 发起改名请求
 * - 函数名: rename
 * - 参数: id — 商品 ID；name — 新名称（可缺省以覆盖 binding 校验分支）
 * - 返回: Promise<ApiResponse<{ id?: number; name?: string }>> 原始响应，业务码交由调用方断言
 * - 异常: 无
 */
async function rename(
  id: number | string,
  name?: string,
): Promise<ApiResponse<{ id?: number; name?: string }>> {
  const data = name === undefined ? {} : { name };
  const resp = await adminApi.put(`/api/v1/admin/products/${id}/name`, { data });
  return (await resp.json()) as ApiResponse<{ id?: number; name?: string }>;
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;

  try {
    categoryId = assertOk<{ id: number }>(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('改名分类') } })).json()) as ApiResponse<{
        id: number;
      }>,
      '创建分类',
    ).id;
    productId = await createProduct('主对象');
    otherProductId = await createProduct('参照');
  } catch (e) {
    console.warn(`[64] 造数失败，用例将跳过：${e instanceof Error ? e.message : e}`);
    productId = 0;
  }
});

test.afterAll(async () => {
  try {
    for (const id of [productId, otherProductId]) {
      if (id) await adminApi.delete(`/api/v1/admin/products/${id}`);
    }
    if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

test.describe.serial('商品行内改名', () => {
  test('1. 正常改名：接口成功且详情回读为新名称', async () => {
    test.skip(productId === 0, '造数未完成');
    const newName = autoName('改名后的商品');
    const body = await rename(productId, newName);
    expect(body.code, `改名失败: ${body.message}`).toBe(0);
    expect(body.data?.id, '响应应回显商品 ID').toBe(productId);
    expect(body.data?.name, '响应应回显新名称').toBe(newName);
    expect(await readName(productId), '详情应回读新名称').toBe(newName);
  });

  test('2. 重复改名：连续改名以最后一次为准', async () => {
    test.skip(productId === 0, '造数未完成');
    const first = autoName('第一次改名');
    const second = autoName('第二次改名');
    expect((await rename(productId, first)).code, '第一次改名应成功').toBe(0);
    expect((await rename(productId, second)).code, '第二次改名应成功').toBe(0);
    expect(await readName(productId), '落库名称应为最后一次提交值').toBe(second);
  });

  test('3. 缺 name 字段与空串均被参数校验拒绝，原名称保持不变', async () => {
    test.skip(productId === 0, '造数未完成');
    const keep = await readName(productId);

    const missing = await rename(productId);
    expect(missing.code, '缺 name 应被 binding required 拒绝').toBe(PARAM_ERROR);

    const blank = await rename(productId, '');
    expect(blank.code, '空串应被 binding required 拒绝').toBe(PARAM_ERROR);

    expect(await readName(productId), '校验失败不应改动名称').toBe(keep);
  });

  test('4. 纯空格名称按空处理，报错文案为「商品名称不能为空」', async () => {
    test.skip(productId === 0, '造数未完成');
    const keep = await readName(productId);
    const body = await rename(productId, '   ');
    expect(body.code, '纯空格应判空拒绝').toBe(PARAM_ERROR);
    expect(body.message, '应透传服务层中文文案').toContain('商品名称不能为空');
    expect(await readName(productId), '名称不应被空格覆盖').toBe(keep);
  });

  test('5. 长度边界：恰好 100 字通过，101 字拒绝并报「不能超过 100 字」', async () => {
    test.skip(productId === 0, '造数未完成');
    // 以造数名打底补足长度，保证超长名仍带 AUTO-TEST 前缀可识别
    const base = autoName('长度边界');
    const pad = (len: number) => (base + '字'.repeat(Math.max(0, len - [...base].length))).slice(0, len);

    const atLimit = pad(NAME_MAX);
    expect([...atLimit].length, '边界名应为 100 字').toBe(NAME_MAX);
    const okBody = await rename(productId, atLimit);
    expect(okBody.code, `100 字边界应通过: ${okBody.message}`).toBe(0);
    expect(await readName(productId), '100 字名称应落库').toBe(atLimit);

    const overLimit = pad(NAME_MAX + 1);
    expect([...overLimit].length, '超长名应为 101 字').toBe(NAME_MAX + 1);
    const overBody = await rename(productId, overLimit);
    expect(overBody.code, '101 字应被拒绝').toBe(PARAM_ERROR);
    expect(overBody.message, '应透传长度上限文案').toContain('不能超过 100 字');
    expect(await readName(productId), '超长名不应落库').toBe(atLimit);
  });

  test('6. 首尾空格落库时被裁剪，但响应回显的是入参原串', async () => {
    test.skip(productId === 0, '造数未完成');
    const trimmed = autoName('裁剪空格');
    const body = await rename(productId, `  ${trimmed}  `);
    expect(body.code, `带空格改名应成功: ${body.message}`).toBe(0);
    expect(body.data?.name, '响应回显未裁剪的原串（前端展示应改用详情值）').toBe(`  ${trimmed}  `);
    expect(await readName(productId), '落库值应已裁剪首尾空格').toBe(trimmed);
  });

  test('7. 不存在或无权商品：报「商品不存在或无权操作」', async () => {
    test.skip(productId === 0, '造数未完成');
    const body = await rename(999999999, autoName('不存在商品改名'));
    expect(body.code, '不存在 ID 应返回资源不存在').toBe(NOT_FOUND);
    expect(body.message, '应透传归属校验文案').toContain('商品不存在或无权操作');
  });

  test('8. 路径 ID 非法：网关解析阶段拒绝，不进入服务层', async () => {
    test.skip(productId === 0, '造数未完成');
    expect((await rename('abc', autoName('非法路径'))).code, '非数字 ID 应被参数校验拒绝').toBe(PARAM_ERROR);
    expect((await rename('0', autoName('非法路径'))).code, '非正数 ID 应被参数校验拒绝').toBe(PARAM_ERROR);
  });

  test('9. 名称无唯一性约束：与既有商品同名允许', async () => {
    test.skip(productId === 0 || otherProductId === 0, '造数未完成');
    const duplicated = await readName(otherProductId);
    const body = await rename(productId, duplicated);
    expect(body.code, `重名应照常受理: ${body.message}`).toBe(0);
    expect(await readName(productId), '两个商品名称应相同').toBe(duplicated);
    expect(await readName(otherProductId), '参照商品名称不受影响').toBe(duplicated);
  });
});
