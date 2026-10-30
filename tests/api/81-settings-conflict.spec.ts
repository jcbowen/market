import { test, expect } from '@playwright/test';
import { newAdminContext, assertOk, type ApiResponse } from '../helpers/api';

/**
 * 模块 81：设置类整份覆盖保存的乐观并发冲突（业务错误码 10007）
 * 背景：站点/附件是「单行 + 整份 PUT」、支付配置自 2026-10-30 重构起是「多行 + 按行 PUT」，
 *       不加并发保护时，后保存者会把先保存者的改动静默丢掉（多管理员或多标签页必然踩到）。
 *       三处统一改为提交时携带读取到的版本号，版本过期即拒绝写入并回 10007：
 *         - 站点设置：网关按 site_configs.version 比对（handler/site.go）
 *         - 支付/附件设置：服务层按 updated_at 比对，以 gRPC Aborted 承载（payconfig.go / attachment.go）
 * 覆盖：
 *   - 三处过期版本提交均返回 10007，且库中配置逐项未被改写
 *   - 不携带版本号（version=0 / updated_at 空串）保持原行为，兼容未参与并发写入的旧调用方
 *   - 版本号冲突经网关翻译后保留服务层原始提示文案（前端据此提示「重新载入后再保存」）
 * 环境说明：用例只做负向提交（必然被拒），唯一一次正向提交是附件的兼容口径用例，
 *           提交内容与当前配置一致且 afterAll 无条件按最新版本号恢复原值，不留残留
 */

/** 站点设置视图（与网关 handler.SiteSetting 对齐，只声明本模块用到的字段） */
interface SiteSetting {
  base_url: string;
  site_name: string;
  logo_url: string;
  favicon_url: string;
  footer_html: string;
  seo_description: string;
  seo_keywords: string;
  updated_at: string;
  version: number;
  [k: string]: unknown;
}

/** 附件配置视图（Secret 为脱敏回显，原样提交即表示沿用原值） */
interface AttachmentConfig {
  storage_type?: string;
  visit_domain?: string;
  oss_access_key_id?: string;
  oss_access_key_secret?: string;
  oss_endpoint?: string;
  oss_bucket_name?: string;
  oss_custom_visit_domain?: string;
  max_size?: number;
  updated_at?: string;
  [k: string]: unknown;
}

/** 支付配置行视图（多行制，本模块只关心版本号与该行的可改字段） */
interface PayConfigRow {
  id: number;
  name?: string;
  platform?: string;
  enabled?: boolean;
  updated_at?: string;
  [k: string]: unknown;
}

/** 数据已被他人修改（errcode.DataConflict） */
const CODE_DATA_CONFLICT = 10007;
/** 明显早于库中任何版本的时间串：用于构造「另一个管理员先改过」的过期版本 */
const STALE_VERSION_TIME = '2000-01-01 00:00:00';

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
/** 进入模块时的附件原配置，afterAll 按最新版本号恢复 */
let originalAttachment: AttachmentConfig | null = null;

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
});

test.afterAll(async () => {
  if (!adminApi || !originalAttachment) {
    await adminApi?.dispose();
    return;
  }
  try {
    const current = await readAttachment();
    const back = (await (
      await adminApi.put('/api/v1/admin/settings/attachment', {
        data: { ...originalAttachment, updated_at: current.updated_at ?? '' },
      })
    ).json()) as ApiResponse;
    if (back.code !== 0) console.log(`[设置冲突] 恢复附件原配置失败 code=${back.code} message=${back.message}`);
  } finally {
    await adminApi.dispose();
  }
});

/**
 * 读取站点设置
 * - 函数名: readSite
 * - 参数: 无（使用模块级 adminApi）
 * - 返回: Promise<SiteSetting>
 * - 异常: 业务码非 0 时经 assertOk 抛 Error
 */
async function readSite(): Promise<SiteSetting> {
  const body = (await (
    await adminApi.get('/api/v1/admin/settings/site')
  ).json()) as ApiResponse<SiteSetting>;
  return assertOk(body, '读取站点设置');
}

/**
 * 读取附件配置
 * - 函数名: readAttachment
 * - 参数: 无（使用模块级 adminApi）
 * - 返回: Promise<AttachmentConfig>
 * - 异常: 业务码非 0 时经 assertOk 抛 Error
 */
async function readAttachment(): Promise<AttachmentConfig> {
  const body = (await (
    await adminApi.get('/api/v1/admin/settings/attachment')
  ).json()) as ApiResponse<AttachmentConfig>;
  return assertOk(body, '读取附件配置');
}

/**
 * 读取支付配置
 * - 函数名: readPayment
 * - 参数: 无（使用模块级 adminApi）
 * - 返回: Promise<PaymentSetting>
 * - 异常: 业务码非 0 时经 assertOk 抛 Error
 */
/**
 * 读取支付配置行列表
 * - 函数名: readPayConfigs
 * - 参数: 无
 * - 返回值: Promise<PayConfigRow[]>，全部配置行（含停用）
 * - 异常: 业务码非 0 时由 assertOk 抛出
 * - 使用示例: const [row] = await readPayConfigs()
 */
async function readPayConfigs(): Promise<PayConfigRow[]> {
  const body = (await (
    await adminApi.get('/api/v1/admin/settings/payment')
  ).json()) as ApiResponse<{ list?: PayConfigRow[] }>;
  return assertOk(body, '读取支付配置').list ?? [];
}

test.describe.serial('设置类乐观并发冲突（10007）', () => {
  test('1. 站点设置携带过期 version 提交被拒，且配置未被改写', async () => {
    const current = await readSite();
    const body = (await (
      await adminApi.put('/api/v1/admin/settings/site', {
        data: {
          ...current,
          // 版本号取「未来值」：等价于另一管理员已保存过一版，本表单已陈旧
          version: current.version + 1,
          site_name: '不应落库的冲突站点名',
        },
      })
    ).json()) as ApiResponse;
    expect(body.code, `过期 version 提交应被乐观锁拒绝，实为 code=${body.code}`).toBe(CODE_DATA_CONFLICT);

    const after = await readSite();
    expect(after.version, '拒绝后版本号不应被推进').toBe(current.version);
    expect(after.site_name, '拒绝后站点名不应被改写').not.toBe('不应落库的冲突站点名');
  });

  test('2. 附件设置携带过期 updated_at 提交被拒，且配置未被改写', async () => {
    originalAttachment = await readAttachment();
    const current = await readAttachment();
    const body = (await (
      await adminApi.put('/api/v1/admin/settings/attachment', {
        data: {
          ...current,
          updated_at: STALE_VERSION_TIME,
          max_size: 1,
          storage_type: 'cos',
        },
      })
    ).json()) as ApiResponse;
    // 版本校验早于字段校验：陈旧版本连「非法存储类型」都轮不到报，统一回 10007
    expect(body.code, `过期 updated_at 提交应被乐观锁拒绝，实为 code=${body.code}`).toBe(CODE_DATA_CONFLICT);
    expect(body.message, '应透传服务端冲突文案').toContain('附件配置已被其他操作更新');

    const after = await readAttachment();
    expect(after.max_size, '拒绝后大小上限不应被改写').toBe(current.max_size);
    expect(after.storage_type, '拒绝后存储类型不应被改写').toBe(current.storage_type);
  });

  test('3. 支付配置行携带过期 updated_at 提交被拒，且该行未被改写', async () => {
    const rows = await readPayConfigs();
    if (rows.length === 0) test.skip(true, '库中无支付配置行');
    const current = rows[0];

    // 模拟另一管理员先行改写同一行：本实例提交读取时的旧版本，必须被拒且不改写库
    const body = (await (
      await adminApi.put(`/api/v1/admin/settings/payment/${current.id}`, {
        data: {
          ...current,
          updated_at: STALE_VERSION_TIME,
          name: `${current.name ?? '支付配置'}-并发覆盖`,
        },
      })
    ).json()) as ApiResponse;
    expect(body.code, `过期 updated_at 提交应被乐观锁拒绝，实为 code=${body.code}`).toBe(CODE_DATA_CONFLICT);
    expect(body.message, '应透传服务端冲突文案').toContain('支付配置已被其他操作更新');

    const after = (await readPayConfigs()).find((r) => r.id === current.id);
    expect(after?.name, '拒绝后该配置行名称不应被改写').toBe(current.name);
    expect(after?.updated_at, '拒绝后该行版本号不应被推进').toBe(current.updated_at);
  });

  test('4. 不携带版本号时保持原行为（兼容未参与并发写入的旧调用方）', async () => {
    const current = await readAttachment();
    // 与当前值不同即可证明确实写入库；afterAll 按 beforeAll 快照恢复附件配置
    const targetSize = current.max_size === 7 * 1024 * 1024 ? 8 * 1024 * 1024 : 7 * 1024 * 1024;
    const body = (await (
      await adminApi.put('/api/v1/admin/settings/attachment', {
        data: { ...current, updated_at: '', max_size: targetSize },
      })
    ).json()) as ApiResponse<{ success?: boolean }>;
    expect(body.code, `空版本号提交应正常保存，实为 code=${body.code} message=${body.message}`).toBe(0);
    expect(body.data?.success).toBe(true);

    const after = await readAttachment();
    expect(after.max_size, `空版本号提交应正常落库（目标 ${targetSize}）`).toBe(targetSize);
  });
});
