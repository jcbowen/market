import { test, expect } from '@playwright/test';
import { newAdminContext, assertOk, asList, type ApiResponse, type PageData } from '../helpers/api';

/**
 * 模块 54：行政区划管理
 * 覆盖（管理端 GET /admin/regions，需超管语境）：
 *   - 分页结构合法（page/page_size/total 与 items 一致）
 *   - keyword 筛选：名称（重庆）与编码（5001）双通道 LIKE
 *   - parent_code 筛选：重庆市(50) 下级均为 level>=2 且 parent_code=50
 *   - level 筛选：level=1 均为省级
 *   - status 筛选：status=1 均为启用
 * 数据口径：演示种子包（full-demo.json）导入后 regions 仅保留重庆市（1 省级 + 37 区县），
 * 用例一律以重庆为锚点；需要全国数据时在后台「行政区划 → 检查更新」同步
 * 覆盖（C 端 GET /regions/locate）：
 *   - 结构合法：code=0、province/city 为 null（回环定位未命中）、longitude/latitude 数字
 * 探索性（更新任务闭环，环境需访问外网数据源，失败不阻断）：
 *   - POST /admin/regions/update/check?source_type=default → 轮询 GET tasks/:id
 *   - 终态 4（完成）且未 applied → POST apply 差异入库（幂等回读 applied=true）
 *   - 终态 5（失败）或超时 → 记录现象不判定为缺陷（容器出网受环境限制）
 */

interface RegionRow {
  id: number;
  code: string;
  name: string;
  parent_code: string;
  level: number;
  status: number;
  [k: string]: unknown;
}

interface UpdateTask {
  id: number;
  status: number;
  source: string;
  total_remote?: number;
  total_local?: number;
  added_count?: number;
  updated_count?: number;
  removed_count?: number;
  error?: string;
  applied?: boolean;
  finished_at?: string | null;
}

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];

/**
 * 查询行政区划列表
 * - 函数名: listRegions
 * - 参数: params — 筛选与分页参数
 * - 返回: Promise<{ items: RegionRow[]; total: number }>（items 缺省为 []）
 * - 异常: 业务码非 0 时经 assertOk 抛出
 * - 使用示例: const { items, total } = await listRegions({ page: 1, page_size: 20 });
 */
async function listRegions(params: Record<string, string | number>): Promise<{ items: RegionRow[]; total: number }> {
  const body = (await (
    await adminApi.get('/api/v1/admin/regions', { params })
  ).json()) as ApiResponse<PageData<RegionRow>>;
  const page = assertOk(body, `区划列表查询失败: ${JSON.stringify(params)}`);
  return { items: asList<RegionRow>(page), total: page.total ?? 0 };
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
});

test.afterAll(async () => {
  await adminApi?.dispose();
});

test.describe.serial('行政区划管理', () => {
  test('1. 分页结构合法：total>0，page_size 截取生效', async () => {
    const p1 = await listRegions({ page: 1, page_size: 5 });
    expect(p1.total, '区划库应有数据').toBeGreaterThan(0);
    expect(p1.items.length, 'page_size=5 应截取 5 行').toBeLessThanOrEqual(5);
    for (const row of p1.items) {
      expect(row.code, '编码应存在').toBeTruthy();
      expect(row.name, '名称应存在').toBeTruthy();
      expect(row.level, '层级应为 1/2/3').toBeGreaterThanOrEqual(1);
    }
    // 翻页不重叠
    const p2 = await listRegions({ page: 2, page_size: 5 });
    if (p2.items.length > 0) {
      const ids1 = new Set(p1.items.map((r) => r.code));
      for (const row of p2.items) expect(ids1.has(row.code), '第 2 页不应与第 1 页重叠').toBe(false);
    }
  });

  test('2. keyword 名称与编码双通道筛选', async () => {
    // 名称通道：重庆
    const byName = await listRegions({ page: 1, page_size: 50, keyword: '重庆' });
    expect(byName.total, '关键词「重庆」应命中重庆市').toBeGreaterThanOrEqual(1);
    for (const row of byName.items) {
      const hit = String(row.name).includes('重庆') || String(row.code).includes('重庆');
      expect(hit, `命中行 ${row.code}/${row.name} 应与关键词相关`).toBe(true);
    }
    // 编码通道：5001（重庆市辖区县码前缀）
    const byCode = await listRegions({ page: 1, page_size: 50, keyword: '5001' });
    expect(byCode.total).toBeGreaterThanOrEqual(1);
    for (const row of byCode.items) {
      const hit = String(row.code).includes('5001') || String(row.name).includes('5001');
      expect(hit, `命中行 ${row.code}/${row.name} 应与关键词相关`).toBe(true);
    }
  });

  test('3. parent_code 筛选：重庆市(50) 下级 parent_code 严格命中', async () => {
    const children = await listRegions({ page: 1, page_size: 50, parent_code: '50' });
    expect(children.total, '重庆市应有下级区划').toBeGreaterThanOrEqual(1);
    for (const row of children.items) {
      expect(row.parent_code, '父编码应全为 50').toBe('50');
      expect(row.level, '下级层级应不低于 2（直辖市口径：区县直接挂省级下，为 level=3）').toBeGreaterThanOrEqual(2);
    }
  });

  test('4. level 筛选：level=1 均为省级且含重庆市', async () => {
    const provinces = await listRegions({ page: 1, page_size: 100, level: 1 });
    expect(provinces.total, '省级区划应存在').toBeGreaterThanOrEqual(1);
    for (const row of provinces.items) expect(row.level, '筛选 level=1 应严格命中').toBe(1);
    expect(provinces.items.map((r) => r.name)).toContain('重庆市');
  });

  test('5. status 筛选：status=1 均为启用', async () => {
    const active = await listRegions({ page: 1, page_size: 50, status: 1 });
    expect(active.total).toBeGreaterThanOrEqual(1);
    for (const row of active.items) expect(row.status, '筛选 status=1 应严格命中').toBe(1);
  });

  test('6. C 端 locate：回环定位未命中时 province/city 为 null', async ({ request }) => {
    const body = (await (
      await request.get('/api/v1/regions/locate', { params: { longitude: 0, latitude: 0 } })
    ).json()) as ApiResponse<{
      province: string | null;
      city: string | null;
      longitude: number;
      latitude: number;
    }>;
    expect(body.code, `定位请求失败: ${body.message}`).toBe(0);
    expect(body.data?.province, '回环坐标应无法命中省').toBeNull();
    expect(body.data?.city, '回环坐标应无法命中市').toBeNull();
    expect(typeof body.data?.longitude, '经度应回显').toBe('number');
    expect(typeof body.data?.latitude, '纬度应回显').toBe('number');
  });

  test('7. 更新任务闭环（探索性：出网受限时记录现象）', async () => {
    // 创建检查任务（幂等：已有进行中任务时返回同一任务）
    const create = (await (
      await adminApi.post('/api/v1/admin/regions/update/check', { params: { source_type: 'default' } })
    ).json()) as ApiResponse<UpdateTask>;
    expect(create.code, `创建更新任务失败: ${create.message}`).toBe(0);
    const taskId = create.data?.id ?? 0;
    expect(taskId).toBeGreaterThan(0);

    // 轮询至终态（最长 60s）：4=完成 / 5=失败
    let task: UpdateTask | null = null;
    for (let i = 0; i < 30; i++) {
      await new Promise((r) => setTimeout(r, 2000));
      const poll = (await (
        await adminApi.get(`/api/v1/admin/regions/update/tasks/${taskId}`)
      ).json()) as ApiResponse<UpdateTask>;
      expect(poll.code, `任务详情查询失败: ${poll.message}`).toBe(0);
      task = poll.data ?? null;
      if (task && (task.status === 4 || task.status === 5)) break;
    }
    expect(task, '任务应可轮询到').toBeTruthy();

    if (task!.status === 4) {
      // 完成态：结构断言 + apply 闭环
      expect(task!.total_remote, '远端总数应记录').toBeGreaterThanOrEqual(0);
      expect(task!.total_local, '本地总数应记录').toBeGreaterThanOrEqual(0);
      expect(task!.finished_at, '完成时间应落库').toBeTruthy();
      expect(task!.applied, '新建任务差异默认未应用').toBe(false);

      const apply = (await (
        await adminApi.post(`/api/v1/admin/regions/update/tasks/${taskId}/apply`)
      ).json()) as ApiResponse<{ id: number }>;
      expect(apply.code, `应用差异失败: ${apply.message}`).toBe(0);
      expect(apply.data?.id).toBe(taskId);

      // 幂等回读：applied 置位
      const after = (await (
        await adminApi.get(`/api/v1/admin/regions/update/tasks/${taskId}`)
      ).json()) as ApiResponse<UpdateTask>;
      expect(after.data?.applied, '应用后任务应标记已应用').toBe(true);
    } else if (task!.status === 5) {
      // 失败态：多为容器出网受限，记录现象（error 应可读），不判定为缺陷
      console.log(`[54-探索] 更新任务失败（环境出网受限现象记录）: ${task!.error}`);
      expect(task!.error, '失败原因应可读').toBeTruthy();
    } else {
      // 60s 未到终态：拉取慢（大数据量比对），记录现象跳过闭环
      console.log(`[54-探索] 任务 60s 未达终态（status=${task!.status}），跳过 apply 闭环`);
    }
  });
});
