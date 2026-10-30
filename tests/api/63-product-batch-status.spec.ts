import { test, expect } from '@playwright/test';
import { newAdminContext, assertOk, type ApiResponse } from '../helpers/api';
import { autoName } from '../helpers/data';

/**
 * 模块 63：商品批量上下架与状态统计口径
 * 覆盖：
 *   - POST /admin/products/batch-status：批量下架（status=0）与上架（status=1）
 *   - GET /admin/products/status-stats：total/on_sale/sold_out/warehouse 四字段互斥且合计等于 total，
 *     其中 on_sale=在售且有库存、sold_out=在售且库存归零、warehouse=仓库中（status=0），口径排除已删除（status=2）
 *   - 参数校验：ids 空数组、缺 status、status 非 0/1 均被拒
 *   - 静默语义：请求为单条 UPDATE（WHERE id IN ? AND status <> 2），
 *     不存在或越权的 ID 被过滤而非报错，部分有效时有效项照常生效且响应仍为成功
 *   - 社区隔离由网关注入 CommunityId，超管语境（0）跨社区生效
 * 说明：自建分类与 3 个商品隔离数据，afterAll 逆序清理
 *
 * 已知缺陷（本轮不改代码，仅规避）：status-stats 的售罄口径读 products.stock 汇总列，
 * 而该列仅在商品编辑保存时由 saveSkus 回写，入库单/出库单/下单扣减只更新 product_skus.stock，
 * 导致 products.stock 与实际 SKU 库存脱节。实测库内已存在 product_stock=150 而 SKU 之和=144 的商品。
 * 因此本模块以「建品即零库存」验证统计口径本身，不经由出库链路造售罄。
 */

interface ProductRow {
  id: number;
  name: string;
  status?: number;
  stock?: number;
  [k: string]: unknown;
}

interface ProductStatusStats {
  total?: number;
  on_sale?: number;
  sold_out?: number;
  warehouse?: number;
}

/** 批量操作接口的业务参数错误码（网关 binding 与 service InvalidArgument 同口径） */
const PARAM_ERROR = 10001;

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let categoryId = 0;
/** 三个自建商品 ID，用例按顺序共享其状态 */
const productIds: number[] = [];
/** 售罄口径用例临时建的商品，afterAll 一并清理 */
const extraProductIds: number[] = [];

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;

  try {
    categoryId = assertOk<{ id: number }>(
      (await (await adminApi.post('/api/v1/admin/categories', { data: { name: autoName('批量状态分类') } })).json()) as ApiResponse<{
        id: number;
      }>,
      '创建分类',
    ).id;

    for (const label of ['甲', '乙', '丙']) {
      const created = assertOk<{ product?: { id: number }; id?: number }>(
        (await (
          await adminApi.post('/api/v1/admin/products', {
            data: {
              category_id: categoryId,
              name: autoName(`批量状态商品${label}`),
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
      productIds.push(id);
    }
  } catch (e) {
    // 假绿收敛：栈已起时造数失败即回归，显式失败而非静默 skip
    throw new Error(`[63] 造数失败：${e instanceof Error ? e.message : e}`);
  }
});

test.afterAll(async () => {
  try {
    for (const id of [...productIds, ...extraProductIds]) await adminApi.delete(`/api/v1/admin/products/${id}`);
    if (categoryId) await adminApi.delete(`/api/v1/admin/categories/${categoryId}`);
  } catch {
    /* 清理失败不影响测试结果 */
  }
  await adminApi?.dispose();
});

/**
 * 读取本模块分类下的状态统计
 * - 函数名: readStats
 * - 参数: 无（按 beforeAll 建立的 categoryId 过滤，隔离外部商品）
 * - 返回: Promise<ProductStatusStats>，接口业务失败时抛出 Error
 * - 异常: code !== 0 时由 assertOk 抛出
 * - 使用示例: const stats = await readStats();
 */
async function readStats(): Promise<ProductStatusStats> {
  const body = (await (
    await adminApi.get('/api/v1/admin/products/status-stats', { params: { category_id: categoryId } })
  ).json()) as ApiResponse<ProductStatusStats>;
  return assertOk(body, '商品状态统计');
}

/**
 * 读取单个商品的 status
 * - 函数名: readStatus
 * - 参数: id — 商品 ID
 * - 返回: Promise<number> 商品状态（0 仓库中 / 1 在售 / 2 已删除）
 * - 异常: 详情接口业务失败时抛出 Error
 */
async function readStatus(id: number): Promise<number> {
  const body = (await (await adminApi.get(`/api/v1/admin/products/${id}`)).json()) as ApiResponse<{
    product?: ProductRow;
  }>;
  return assertOk(body, '商品详情').product?.status ?? -1;
}

/**
 * 发起批量上下架请求
 * - 函数名: batchStatus
 * - 参数: data — 请求体（ids / status 可缺省以覆盖校验分支）
 * - 返回: Promise<ApiResponse> 原始响应，交由调用方断言业务码
 * - 异常: 无（不校验业务码）
 */
async function batchStatus(data: { ids?: number[]; status?: number }): Promise<ApiResponse> {
  const resp = await adminApi.post('/api/v1/admin/products/batch-status', { data });
  return (await resp.json()) as ApiResponse;
}

test.describe.serial('商品批量上下架与状态统计', () => {
  test('1. 基线统计：三商品在售，四字段互斥且合计等于 total', async () => {
    test.skip(productIds.length !== 3, '造数未完成');
    const stats = await readStats();
    expect(stats.total, '自建商品数应为 3').toBe(3);
    expect(stats.on_sale, '初始全部在售有库存').toBe(3);
    expect(stats.sold_out ?? 0, '初始无售罄').toBe(0);
    expect(stats.warehouse ?? 0, '初始无仓库中').toBe(0);
    expect((stats.on_sale ?? 0) + (stats.sold_out ?? 0) + (stats.warehouse ?? 0), '三类之和应等于 total').toBe(
      stats.total,
    );
  });

  test('2. 批量下架：三商品状态落 0，统计转 warehouse', async () => {
    test.skip(productIds.length !== 3, '造数未完成');
    const body = await batchStatus({ ids: productIds, status: 0 });
    expect(body.code, `批量下架失败: ${body.message}`).toBe(0);

    for (const id of productIds) {
      expect(await readStatus(id), `商品 ${id} 应转为仓库中`).toBe(0);
    }
    const stats = await readStats();
    expect(stats.warehouse, '三商品应计入仓库中').toBe(3);
    expect(stats.on_sale ?? 0, '下架后不再计入在售').toBe(0);
    expect(stats.total, '统计总数不受上下架影响').toBe(3);
  });

  test('3. 批量上架：状态回 1，统计复现 on_sale', async () => {
    test.skip(productIds.length !== 3, '造数未完成');
    const body = await batchStatus({ ids: productIds, status: 1 });
    expect(body.code, `批量上架失败: ${body.message}`).toBe(0);

    for (const id of productIds) {
      expect(await readStatus(id), `商品 ${id} 应恢复在售`).toBe(1);
    }
    const stats = await readStats();
    expect(stats.on_sale, '恢复在售 3').toBe(3);
    expect(stats.warehouse ?? 0, '仓库中应清零').toBe(0);
  });

  test('4. 售罄口径：在售且汇总库存为 0 计入 sold_out 而非 on_sale', async () => {
    test.skip(productIds.length !== 3, '造数未完成');
    const created = assertOk<{ product?: { id: number }; id?: number }>(
      (await (
        await adminApi.post('/api/v1/admin/products', {
          data: {
            category_id: categoryId,
            name: autoName('批量状态售罄商品'),
            unit: '份',
            is_multi_spec: false,
            sku_items: [{ price: 3.5, stock: 0 }],
          },
        })
      ).json()) as ApiResponse<{ product?: { id: number }; id?: number }>,
      '创建零库存商品',
    );
    const zeroId = created.product?.id ?? created.id ?? 0;
    expect(zeroId, '零库存商品应创建成功').toBeGreaterThan(0);
    extraProductIds.push(zeroId);

    const on = (await (
      await adminApi.post(`/api/v1/admin/products/${zeroId}/status`, { data: { status: 1 } })
    ).json()) as ApiResponse;
    expect(on.code, `上架零库存商品失败: ${on.message}`).toBe(0);

    const stats = await readStats();
    expect(stats.total, '统计集合应包含新增的零库存商品').toBe(4);
    expect(stats.sold_out, '在售且汇总库存为 0 应计入售罄').toBe(1);
    expect(stats.on_sale, '有库存的三商品仍计入在售').toBe(3);
    expect((stats.on_sale ?? 0) + (stats.sold_out ?? 0) + (stats.warehouse ?? 0), '三类之和仍等于 total').toBe(
      stats.total,
    );
  });

  test('5. 混入无效 ID：有效项照常生效，无效项被静默过滤', async () => {
    test.skip(productIds.length !== 3, '造数未完成');
    const valid = productIds[0];
    const body = await batchStatus({ ids: [valid, 999999999], status: 0 });
    expect(body.code, `混合 ID 请求应成功（无部分失败语义）: ${body.message}`).toBe(0);
    expect(await readStatus(valid), '有效商品应被下架').toBe(0);

    // 全部为无效 ID 时影响行数为 0，服务端仍返回成功
    const noop = await batchStatus({ ids: [999999998], status: 1 });
    expect(noop.code, '全无效 ID 应返回成功而非报错').toBe(0);

    // 复位，保持后续用例基线
    await batchStatus({ ids: [valid], status: 1 });
  });

  test('6. ids 为空数组被拒绝', async () => {
    test.skip(productIds.length !== 3, '造数未完成');
    const body = await batchStatus({ ids: [], status: 1 });
    expect(body.code, '空 ids 应被参数校验拒绝').toBe(PARAM_ERROR);
  });

  test('7. 缺 status 字段被拒绝', async () => {
    test.skip(productIds.length !== 3, '造数未完成');
    const body = await batchStatus({ ids: [productIds[0]] });
    expect(body.code, '缺 status 应被参数校验拒绝').toBe(PARAM_ERROR);
  });

  test('8. status 非 0/1 被拒绝，不得借批量接口删除商品', async () => {
    test.skip(productIds.length !== 3, '造数未完成');
    const before = await readStats();
    const body = await batchStatus({ ids: productIds, status: 2 });
    expect(body.code, 'status=2 应被拒绝').not.toBe(0);

    // 断言与操作前一致而非写死基数：用例 4 会额外建一个零库存商品，集合大小已变
    const after = await readStats();
    expect(after.total, '非法批量操作不应改变商品集合').toBe(before.total);
    expect(after.warehouse ?? 0, '非法批量操作不应把商品打入仓库中').toBe(before.warehouse ?? 0);
  });
});
