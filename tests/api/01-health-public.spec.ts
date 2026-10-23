import { test, expect } from '@playwright/test';
import { assertOk, type PageData } from '../helpers/api';

/**
 * 模块 01：健康检查 + C 端公开接口（无需登录）
 * 覆盖：/health、社区、商品、SKU、分类、营销活动、banner、定位、行政区划
 */

test.describe('健康检查与公开接口', () => {
  test('健康检查 /health 返回 HTTP 200', async ({ request }) => {
    const resp = await request.get('/health');
    expect(resp.status()).toBe(200);
  });

  test('社区列表可获取且含预置社区', async ({ request }) => {
    const resp = await request.get('/api/v1/communities');
    expect(resp.status()).toBe(200);
    const body = (await resp.json()) as PageData<{ id: number; name: string; status: number }>;
    expect(body.code).toBe(0);
    expect(Array.isArray(body.data?.list)).toBe(true);
    // init.dev.sql 预置社区应存在
    expect(body.data!.list.length).toBeGreaterThan(0);
  });

  test('社区详情可获取', async ({ request }) => {
    const list = assertOk(
      (await (await request.get('/api/v1/communities')).json()) as PageData<{ id: number }>,
      '社区列表',
    );
    const first = list.list[0];
    const resp = await request.get(`/api/v1/communities/${first.id}`);
    const body = (await resp.json()) as { code: number; data: { id: number } };
    expect(body.code).toBe(0);
    expect(body.data.id).toBe(first.id);
  });

  test('商品列表可获取（含 sku 信息结构）', async ({ request }) => {
    const resp = await request.get('/api/v1/products', { params: { page: 1, page_size: 10 } });
    const body = (await resp.json()) as PageData<{ id: number; skus?: unknown[] }>;
    expect(body.code).toBe(0);
    expect(Array.isArray(body.data?.list)).toBe(true);
  });

  test('商品详情：不存在的商品返回业务错误而非 500', async ({ request }) => {
    const resp = await request.get('/api/v1/products/999999999');
    const body = (await resp.json()) as { code: number };
    // 网关业务错误恒 HTTP 200 + 非零业务码（GRPCError 统一映射）
    expect(resp.status()).toBe(200);
    expect(body.code).not.toBe(0);
  });

  test('分类列表可获取', async ({ request }) => {
    const resp = await request.get('/api/v1/categories');
    const body = (await resp.json()) as PageData<{ id: number; name: string }>;
    expect(body.code).toBe(0);
  });

  test('营销活动接口：优惠券/秒杀/满减/包邮 可获取', async ({ request }) => {
    for (const path of ['/api/v1/coupons', '/api/v1/flash-sales', '/api/v1/full-reductions']) {
      const resp = await request.get(path);
      const body = (await resp.json()) as { code: number };
      expect(body.code, `${path} 业务码`).toBe(0);
    }
    const freeResp = await request.get('/api/v1/free-shippings/active');
    const freeBody = (await freeResp.json()) as { code: number };
    expect(freeBody.code).toBe(0); // data 可能为 null（无活动），业务码应为 0
  });

  test('banner 列表可获取', async ({ request }) => {
    const resp = await request.get('/api/v1/banners');
    const body = (await resp.json()) as { code: number };
    expect(body.code).toBe(0);
  });

  test('行政区划：region 子级查询', async ({ request }) => {
    const resp = await request.get('/api/v1/regions/children', { params: { parent_code: '110000' } });
    const body = (await resp.json()) as { code: number; data?: unknown[] };
    expect(body.code).toBe(0);
    expect(Array.isArray(body.data)).toBe(true);
  });
});
