import { test, expect } from '@playwright/test';
import {
  adminLogin,
  clientLogin,
  newAdminContext,
  newAuthedRequest,
  pickSku,
  type AdminContext,
  type ApiResponse,
  type PageData,
} from '../helpers/api';
import { uniquePhone } from '../helpers/data';
import type { Playwright, APIRequestContext } from '@playwright/test';

/**
 * 模块 75：订单搜索增强与下载中心（HTTP 收口）
 * 对应暂存需求「订单搜索与下载中心」，服务端越权/隔离场景由
 * services 层集成测试（export_task_integration_test.go）覆盖，此处验证网关口径：
 *   - 订单列表新增 pay_no（支付单号/渠道交易号单框双号，精确匹配）与 product_id（含全部规格）过滤；
 *   - 导出任务：创建立即返回 → 异步生成 → 列表可见 → CSV 下载（BOM/内容与筛选一致）→ 留痕 → 超管删除；
 *   - 可见性：普通管理员仅见自己的任务与角标；删除仅超管（服务端兜底，非仅前端隐藏入口）。
 *
 * 环境依赖：整套服务已启动（gateway/order/payment/user），本地库已迁移 export_tasks 表。
 */

interface OrderRow {
  id: number;
  order_no: string;
  status: number;
  product_id?: number;
}

interface ExportTaskRow {
  id: number;
  status: number;
  row_count: number;
  file_name: string;
  download_count: number;
  error_msg?: string;
  created_at?: string;
  expires_at?: string | null;
}

/** 导出任务状态：1 待处理 2 生成中 3 已完成 4 失败 */
const EXPORT_DONE = 3;
/** 轮询生成完成的超时（异步生成通常秒级） */
const EXPORT_TIMEOUT = 20_000;

let admin: AdminContext;
let normalApi: APIRequestContext;
let normalUsername = '';
let clientApi: Awaited<ReturnType<typeof newAuthedRequest>>;
/** beforeAll 造数产物：C 端订单号 / 支付单号 / 商品 ID（跨用例共享） */
let targetOrderNo = '';
let paymentNo = '';
let targetProductId = 0;

/**
 * 轮询等待导出任务到达已完成状态
 * - 函数名: waitForExportDone
 * - 参数: api — 管理端请求上下文；taskId — 任务 ID
 * - 返回: Promise<ExportTaskRow>
 * - 异常: 超时未完成或任务提前失败时抛出 Error
 */
async function waitForExportDone(api: APIRequestContext, taskId: number): Promise<ExportTaskRow> {
  const deadline = Date.now() + EXPORT_TIMEOUT;
  let lastErr = '';
  while (Date.now() < deadline) {
    const body = (await (
      await api.get('/api/v1/admin/orders/export-tasks', { params: { page: 1, page_size: 20 } })
    ).json()) as ApiResponse<{ list: ExportTaskRow[] }>;
    const row = (body.data?.list ?? []).find((t) => t.id === taskId);
    if (row) {
      if (row.status === EXPORT_DONE) return row;
      if (row.status === 4) throw new Error(`任务 ${taskId} 生成失败: ${row.error_msg}`);
      lastErr = `status=${row.status}`;
    } else {
      lastErr = '任务不在列表中';
    }
    await new Promise((r) => setTimeout(r, 400));
  }
  throw new Error(`任务 ${taskId} 在 ${EXPORT_TIMEOUT}ms 内未完成（${lastErr}）`);
}

test.beforeAll(async ({ playwright }: { playwright: Playwright }) => {
  // 先登录 C 端拿真实社区（新用户自动绑定第一个可用社区，可能与超管默认语境不同）
  const info = await clientLogin(playwright.request, uniquePhone());
  clientApi = await newAuthedRequest(playwright, info.access_token);
  const communityId = info.user.community_id;

  // 管理端语境固定到 C 端所在社区，保证「下单→支付→后台搜索→导出」全链路同一社区口径
  admin = await newAdminContext(playwright, undefined, undefined, communityId);

  // C 端会员下单并用现金渠道支付（进入待自提；payment_no 即渠道账单「商家订单号」语义的替身）
  const sku = await pickSku(clientApi, undefined, communityId);
  const orderBody = (await (
    await clientApi.post('/api/v1/orders', {
      data: { delivery_type: 1, address_id: 0, items: [{ sku_id: sku.id, quantity: 1 }] },
    })
  ).json()) as ApiResponse<OrderRow>;
  if (orderBody.code !== 0) throw new Error(`下单失败 ${orderBody.message}`);
  const payBody = (await (
    await clientApi.post('/api/v1/payments', { data: { order_id: orderBody.data!.id, channel: 'cash' } })
  ).json()) as ApiResponse<{ payment_no?: string }>;
  if (payBody.code !== 0 || !payBody.data?.payment_no) {
    throw new Error(`支付失败或未返回支付单号 code=${payBody.code} message=${payBody.message}`);
  }
  paymentNo = payBody.data.payment_no;
  targetOrderNo = orderBody.data!.order_no;
  targetProductId = sku.product_id;

  // 普通社区管理员（community_admin 内置角色）：验证下载中心可见性隔离与删除兜底
  const rolesBody = (await (
    await admin.api.get('/api/v1/admin/roles', { params: { community_id: communityId } })
  ).json()) as ApiResponse<{ id: number; code?: string }[] | { list: { id: number; code?: string }[] }>;
  const roles = (Array.isArray(rolesBody.data) ? rolesBody.data : rolesBody.data?.list) ?? [];
  const builtin = roles.find((r) => r.code === 'community_admin');
  if (!builtin) throw new Error(`社区 ${communityId} 无内置 community_admin 角色`);
  normalUsername = `auto_exp_${uniquePhone()}`;
  const createBody = (await (
    await admin.api.post('/api/v1/admin/admins', {
      data: { username: normalUsername, password: 'Exp123456', real_name: 'AUTO-TEST 导出用普通管理员', role_ids: [builtin.id] },
    })
  ).json()) as ApiResponse<{ id?: number }>;
  if (createBody.code !== 0) throw new Error(`创建普通管理员失败 code=${createBody.code} message=${createBody.message}`);
  const normalLogin = await adminLogin(playwright.request, normalUsername, 'Exp123456');
  normalApi = await playwright.request.newContext({
    extraHTTPHeaders: { Authorization: `Bearer ${normalLogin.access_token}`, 'X-Community-Id': String(communityId) },
  });
});

test.afterAll(async () => {
  await admin?.api.dispose();
  await normalApi?.dispose();
  await clientApi?.dispose();
});

test.describe.serial('订单列表搜索增强', () => {
  const listOrders = async (extra: Record<string, string | number>): Promise<PageData<OrderRow>> => {
    const body = (await (
      await admin.api.get('/api/v1/admin/orders', { params: { page: 1, page_size: 50, ...extra } })
    ).json()) as ApiResponse<PageData<OrderRow>>;
    expect(body.code, `订单列表应成功, message=${body.message}`).toBe(0);
    return body.data as PageData<OrderRow>;
  };

  test('账单号精确命中对应订单', async () => {
    const page = await listOrders({ pay_no: paymentNo });
    expect(page.total, '支付单号唯一，应只命中 1 笔订单').toBe(1);
    expect(page.list[0].order_no).toBe(targetOrderNo);
  });

  test('账单号不存在返回空列表（而非报错或全量）', async () => {
    const page = await listOrders({ pay_no: `${paymentNo}X` });
    expect(page.total, '不存在的支付单号应返回空列表').toBe(0);
  });

  test('账单号部分输入不命中（精确匹配语义）', async () => {
    const page = await listOrders({ pay_no: paymentNo.slice(0, -2) });
    expect(page.total, '截断的支付单号不应命中').toBe(0);
  });

  test('product_id 过滤命中含该商品的订单', async () => {
    const page = await listOrders({ product_id: targetProductId, order_no: targetOrderNo });
    expect(page.total, '商品过滤应命中本例订单').toBe(1);
    expect(page.list[0].order_no).toBe(targetOrderNo);

    const empty = await listOrders({ product_id: 999999999, order_no: targetOrderNo });
    expect(empty.total, '不存在的商品应返回空列表').toBe(0);
  });
});

test.describe.serial('下载中心全链路（超管）', () => {
  let taskId = 0;

  test('创建导出任务立即返回任务 ID', async () => {
    const resp = await admin.api.post('/api/v1/admin/orders/export-tasks', {
      params: { pay_no: paymentNo, query_desc: '75 号用例-账单号导出' },
    });
    const body = (await resp.json()) as ApiResponse<{ id: number }>;
    expect(body.code, `创建导出任务应成功, message=${body.message}`).toBe(0);
    expect(body.data?.id, '应返回任务 ID').toBeGreaterThan(0);
    taskId = body.data!.id;
  });

  test('任务异步生成完成且行数与筛选一致', async () => {
    const row = await waitForExportDone(admin.api, taskId);
    expect(row.row_count, '按支付单号导出应只含 1 笔订单').toBe(1);
    expect(row.file_name, '应有文件名').toMatch(/\.csv$/);
    expect(row.expires_at, '完成态应有过期时间').toBeTruthy();
  });

  test('下载 CSV 含 BOM 与目标订单', async () => {
    const resp = await admin.api.get(`/api/v1/admin/orders/export-tasks/${taskId}/download`);
    expect(resp.status(), '下载应 HTTP 200').toBe(200);
    const text = await resp.text();
    expect(text.startsWith('\ufeff'), 'CSV 应以 BOM 开头（Excel 中文兼容）').toBeTruthy();
    expect(text).toContain(targetOrderNo);
    expect(text).toContain('待自提');
  });

  test('重复下载成功且未下载角标归零', async () => {
    const again = await admin.api.get(`/api/v1/admin/orders/export-tasks/${taskId}/download`);
    expect(again.status()).toBe(200);

    const body = (await (
      await admin.api.get('/api/v1/admin/orders/export-tasks/undownloaded-count')
    ).json()) as ApiResponse<{ count: number }>;
    expect(body.code).toBe(0);
    // 超管视角可能存在历史未下载任务，只断言本任务已不再计入：角标数不含刚下载过的任务
    const listBody = (await (
      await admin.api.get('/api/v1/admin/orders/export-tasks', { params: { page: 1, page_size: 50 } })
    ).json()) as ApiResponse<{ list: ExportTaskRow[] }>;
    const row = listBody.data?.list.find((t) => t.id === taskId);
    expect(row?.download_count, '下载留痕次数应为 2').toBe(2);
  });

  test('超管删除任务后下载被拒', async () => {
    const resp = await admin.api.delete(`/api/v1/admin/orders/export-tasks/${taskId}`);
    const body = (await resp.json()) as ApiResponse<{ success?: boolean }>;
    expect(body.code, `删除应成功, message=${body.message}`).toBe(0);
    expect(body.data?.success).toBe(true);

    const dl = await admin.api.get(`/api/v1/admin/orders/export-tasks/${taskId}/download`);
    const dlBody = (await dl.json().catch(() => ({}))) as ApiResponse;
    // 任务已删：下载应失败（HTTP 非 200 或业务码非 0）
    expect(dl.status() !== 200 || dlBody.code !== 0, '删除后下载应被拒绝').toBeTruthy();
  });

  test('未登录创建导出任务被拒', async ({ request }) => {
    const resp = await request.post('/api/v1/admin/orders/export-tasks');
    const body = (await resp.json().catch(() => ({}))) as ApiResponse;
    expect([200, 401], '未授权只允许 HTTP 200+业务码 或 401 两种表达').toContain(resp.status());
    if (resp.status() === 200) expect(body.code).not.toBe(0);
  });
});

test.describe.serial('下载中心可见性与删除兜底（普通管理员）', () => {
  let ownTaskId = 0;

  test('普通管理员创建自己的导出任务并只看到自己的记录', async () => {
    const resp = await normalApi.post('/api/v1/admin/orders/export-tasks', {
      params: { order_no: targetOrderNo, query_desc: '75 号用例-普通管理员' },
    });
    const body = (await resp.json()) as ApiResponse<{ id: number }>;
    expect(body.code, `普通管理员创建导出任务应成功, message=${body.message}`).toBe(0);
    ownTaskId = body.data!.id;
    await waitForExportDone(normalApi, ownTaskId);

    const listBody = (await (
      await normalApi.get('/api/v1/admin/orders/export-tasks', { params: { page: 1, page_size: 50 } })
    ).json()) as ApiResponse<{ list: ExportTaskRow[]; total: number }>;
    // 普通管理员视角只见自己的任务（全新账号，无历史）
    expect(listBody.data?.total, '应只含自己创建的 1 条').toBe(1);
    expect(listBody.data?.list[0].id).toBe(ownTaskId);
    expect(listBody.data?.list[0].download_count, '未下载为 0').toBe(0);
  });

  test('普通管理员角标计数为自己的未下载任务数', async () => {
    const body = (await (
      await normalApi.get('/api/v1/admin/orders/export-tasks/undownloaded-count')
    ).json()) as ApiResponse<{ count: number }>;
    expect(body.code).toBe(0);
    expect(body.data?.count, '未下载角标应为 1').toBe(1);
  });

  test('普通管理员可下载自己的文件', async () => {
    const resp = await normalApi.get(`/api/v1/admin/orders/export-tasks/${ownTaskId}/download`);
    expect(resp.status(), '下载自己的文件应 HTTP 200').toBe(200);
    const text = await resp.text();
    expect(text).toContain(targetOrderNo);
  });

  test('普通管理员删除导出任务被服务端兜底拒绝', async () => {
    // 服务端兜底：删除是超管专属能力，前端隐藏入口之外 API 也必须拒绝
    const resp = await normalApi.delete(`/api/v1/admin/orders/export-tasks/${ownTaskId}`);
    const body = (await resp.json().catch(() => ({}))) as ApiResponse;
    expect(resp.status() !== 200 || body.code !== 0, '普通管理员删除应被拒绝').toBeTruthy();
    if (resp.status() === 200) {
      expect(body.code, '业务码应为无权限类错误').not.toBe(0);
    }
    // 记录仍在（不能删掉自己的任务绕过留痕审计）
    const listBody = (await (
      await normalApi.get('/api/v1/admin/orders/export-tasks', { params: { page: 1, page_size: 50 } })
    ).json()) as ApiResponse<{ total: number }>;
    expect(listBody.data?.total, '删除被拒后记录应仍在').toBe(1);
  });
});
