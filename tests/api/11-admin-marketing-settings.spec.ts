import { test, expect } from '@playwright/test';
import { newAdminContext, asList, type ApiResponse, type PageData } from '../helpers/api';
import { autoName } from '../helpers/data';
import type { Playwright } from '@playwright/test';

/**
 * 模块 11：管理端营销与设置
 * 覆盖：优惠券/秒杀/满减/包邮/banner 管理、支付/退款/附件设置读取、
 *       员工与用户列表、应用偏好。营销活动创建类用例按接口实际契约造数并显式断言
 *       （旧口径「失败即 skip」会把用例自身的字段写错伪装成环境问题，属假绿）。
 */

let api: Awaited<ReturnType<typeof newAdminContext>['api']>;

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  const ctx = await newAdminContext(playwright);
  api = ctx.api;
});

test.afterAll(async () => {
  await api?.dispose();
});

test.describe('营销管理', () => {
  test('优惠券活动列表可查', async () => {
    const resp = await api.get('/api/v1/admin/coupons');
    const body = (await resp.json()) as ApiResponse<PageData<{ id: number }>>;
    expect(body.code).toBe(0);
  });

  test('秒杀活动列表可查', async () => {
    const resp = await api.get('/api/v1/admin/flash-sales');
    const body = (await resp.json()) as ApiResponse<PageData<{ id: number }>>;
    expect(body.code).toBe(0);
  });

  test('满减活动列表可查', async () => {
    const resp = await api.get('/api/v1/admin/full-reductions');
    const body = (await resp.json()) as ApiResponse<PageData<{ id: number }>>;
    expect(body.code).toBe(0);
  });

  test('包邮活动列表可查', async () => {
    const resp = await api.get('/api/v1/admin/free-shippings');
    const body = (await resp.json()) as ApiResponse<PageData<{ id: number }>>;
    expect(body.code).toBe(0);
  });

  test('banner 列表可查', async () => {
    const resp = await api.get('/api/v1/admin/banners');
    const body = (await resp.json()) as ApiResponse<PageData<{ id: number }>>;
    expect(body.code).toBe(0);
  });

  test('领取/发放记录可查', async () => {
    const resp = await api.get('/api/v1/admin/coupon-records');
    const body = (await resp.json()) as ApiResponse<PageData>;
    expect(body.code).toBe(0);
  });

  // 契约口径（gateway/handler/admin.go 的 CreateFullReductionReq）：门槛字段是 `threshold`，
  // 且**没有** `status`（服务层恒置 1）与 `full_amount`。旧用例发的是 `full_amount`，
  // 必填的 threshold 缺失 → 参数错误 → 用例把「自己的字段写错」当成环境差异 skip 掉，属假绿。
  test('创建满减活动（未来时间窗）成功并可回读', async () => {
    const start = new Date(Date.now() + 3600_000).toISOString().slice(0, 19).replace('T', ' ');
    const end = new Date(Date.now() + 2 * 3600_000).toISOString().slice(0, 19).replace('T', ' ');
    const name = autoName('满减');
    const resp = await api.post('/api/v1/admin/full-reductions', {
      data: { name, threshold: 100, reduce_amount: 10, start_time: start, end_time: end },
    });
    const body = (await resp.json()) as ApiResponse<{ id?: number; threshold?: number; reduce_amount?: number; status?: number }>;
    expect(body.code, `创建满减应成功，实际 ${body.code} ${body.message}`).toBe(0);
    const id = body.data?.id ?? 0;
    expect(id, '应返回新建活动 ID').toBeGreaterThan(0);
    // 服务层直接回模型：门槛/减免与提交一致，status 由服务端置 1（不由请求决定）
    expect(body.data?.threshold, '门槛应回显提交值').toBe(100);
    expect(body.data?.reduce_amount, '减免金额应回显提交值').toBe(10);
    expect(body.data?.status, '新建活动默认启用').toBe(1);

    try {
      // 回读：管理端列表按 id desc，新建项应在首页内可见
      const list = (await (await api.get('/api/v1/admin/full-reductions')).json()) as ApiResponse<
        { id: number; name: string; threshold: number }[]
      >;
      expect(list.code).toBe(0);
      const row = asList(list.data).find((r) => r.id === id);
      expect(row, '列表应能查到新建活动').toBeDefined();
      expect(row?.name, '列表名称应与创建一致').toBe(name);
    } finally {
      // 清理：置 status=0 停用，避免残留活动影响后续轮次的满减试算
      await api.put(`/api/v1/admin/full-reductions/${id}`, { data: { status: 0 } });
    }
  });

  // 反向口径：threshold 是必填且 >0，缺失或为 0 必须被参数校验挡下（旧用例只测正向、
  // 且把失败当环境差异跳过，等于这条契约从未被验证过）
  test('创建满减缺少门槛被拒绝', async () => {
    const resp = await api.post('/api/v1/admin/full-reductions', {
      data: { name: autoName('满减'), reduce_amount: 10 },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '缺少 threshold 必须被拒绝').not.toBe(0);
  });
});

test.describe('设置模块', () => {
  test('支付配置可读', async () => {
    const resp = await api.get('/api/v1/admin/settings/payment');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('退款配置可读', async () => {
    const resp = await api.get('/api/v1/admin/settings/refund');
    const body = (await resp.json()) as ApiResponse<{ freight_refund_policy?: string }>;
    expect(body.code).toBe(0);
  });

  test('附件配置可读', async () => {
    const resp = await api.get('/api/v1/admin/settings/attachment');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('附件列表可查', async () => {
    const resp = await api.get('/api/v1/admin/attachment/list');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });
});

test.describe('用户与员工', () => {
  test('C 端用户列表可查', async () => {
    const resp = await api.get('/api/v1/admin/users', { params: { page: 1, page_size: 10 } });
    const body = (await resp.json()) as ApiResponse<PageData<{ id: number }>>;
    expect(body.code).toBe(0);
  });

  test('履约人员列表可查', async () => {
    const resp = await api.get('/api/v1/admin/fulfillers');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('全局账号列表可查', async () => {
    const resp = await api.get('/api/v1/admin/accounts');
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
  });

  test('应用偏好读写', async () => {
    // 请求体字段是 app_keys（后端 SaveAppPreferences 只绑该字段）；
    // 原先传 favorites 会被静默忽略，写进去的是空列表，用例等于只测了「接口不报错」
    const keys = ['product'];
    const save = (await (
      await api.put('/api/v1/admin/app-preferences', { data: { app_keys: keys } })
    ).json()) as ApiResponse<{ app_keys: string[] }>;
    expect(save.code).toBe(0);
    const read = (await (await api.get('/api/v1/admin/app-preferences')).json()) as ApiResponse<{ app_keys: string[] }>;
    expect(read.code).toBe(0);
    expect(read.data?.app_keys).toEqual(keys);
  });
});
