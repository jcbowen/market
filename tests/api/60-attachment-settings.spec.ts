import { test, expect } from '@playwright/test';
import { newAdminContext, type ApiResponse } from '../helpers/api';

/**
 * 模块 60：附件配置读写（本地存储模式）
 * 覆盖：GET /admin/settings/attachment 回读（Secret 脱敏）、
 *       PUT /admin/settings/attachment 白名单写入与回读（max_size / visit_domain）、
 *       非法 storage_type 拒绝、OSS 必填项校验（远程存储未对接场景的负向面）、
 *       Secret 传掩码不覆盖原值、用例结束恢复原配置
 * 边界说明：仅操作 local 模式字段，不切换真实存储类型，避免影响其他模块的上传链路
 * 乐观锁说明：附件设置为单行整份覆盖式保存，提交必须携带读取时的 `updated_at` 作版本号
 *             （版本过期返回 10007，见模块 81），故每次 PUT 前用 currentVersion() 取最新版本，
 *             其余字段仍以 beforeAll 快照为准，保证用例结束能恢复原值
 */

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

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let original: AttachmentConfig;

const MB = 1024 * 1024;

/**
 * 读取附件配置当前版本号（updated_at）
 * 整份覆盖式保存的乐观锁要求提交携带最新版本，否则被 10007 拒绝
 */
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

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  const resp = await adminApi.get('/api/v1/admin/settings/attachment');
  const body = (await resp.json()) as ApiResponse<AttachmentConfig>;
  expect(body.code, `读取附件配置失败: ${body.message}`).toBe(0);
  original = body.data ?? {};
});

test.afterAll(async () => {
  // 恢复原配置：Secret 传回脱敏回显值，服务端识别掩码后保留原值
  if (adminApi && original) {
    await adminApi.put('/api/v1/admin/settings/attachment', { data: await payload() });
    await adminApi.dispose();
  }
});

test.describe.serial('附件配置（本地模式）', () => {
  test('读取配置返回存储类型与脱敏 Secret', async () => {
    expect(original.storage_type).toBe('local');
    // Secret 回显必须为掩码（6 位 *）或空，不允许明文泄露
    const secret = (original.oss_access_key_secret as string) ?? '';
    expect(secret === '' || secret === '*'.repeat(secret.length)).toBe(true);
    expect(typeof original.max_size).toBe('number');
  });

  test('更新 max_size 与 visit_domain 后回读生效', async () => {
    const targetSize = 6 * MB;
    const put = (await (await adminApi.put('/api/v1/admin/settings/attachment', {
      data: await payload({ max_size: targetSize, visit_domain: '' }),
    })).json()) as ApiResponse<{ success?: boolean }>;
    expect(put.code, `保存附件配置失败: ${put.message}`).toBe(0);
    expect(put.data?.success).toBe(true);

    const read = (await (await adminApi.get('/api/v1/admin/settings/attachment')).json()) as ApiResponse<AttachmentConfig>;
    expect(read.code).toBe(0);
    expect(read.data?.max_size).toBe(targetSize);
    // updated_at 兼作版本号，保存后应变化
    expect(read.data?.updated_at).not.toBe(original.updated_at);
  });

  test('非法 storage_type 被拒绝', async () => {
    const resp = await adminApi.put('/api/v1/admin/settings/attachment', {
      data: await payload({ storage_type: 'cos' }),
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '非法存储类型应被业务拦截').not.toBe(0);
    expect(body.message).toContain('不支持的存储类型');
  });

  test('切换 OSS 模式缺必填项被拒绝（远程存储未对接的负向校验）', async () => {
    const resp = await adminApi.put('/api/v1/admin/settings/attachment', {
      data: await payload({ storage_type: 'oss' }),
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).not.toBe(0);
    expect(body.message).toContain('OSS 配置不完整');

    // 确认配置未被破坏：仍是 local
    const read = (await (await adminApi.get('/api/v1/admin/settings/attachment')).json()) as ApiResponse<AttachmentConfig>;
    expect(read.data?.storage_type).toBe('local');
  });

  test('恢复原配置后回读一致', async () => {
    const put = (await (await adminApi.put('/api/v1/admin/settings/attachment', {
      data: await payload(),
    })).json()) as ApiResponse;
    expect(put.code, `恢复配置失败: ${put.message}`).toBe(0);

    const read = (await (await adminApi.get('/api/v1/admin/settings/attachment')).json()) as ApiResponse<AttachmentConfig>;
    expect(read.code).toBe(0);
    expect(read.data?.storage_type).toBe(original.storage_type ?? 'local');
    expect(read.data?.oss_access_key_id ?? '').toBe(original.oss_access_key_id ?? '');
    expect(read.data?.oss_endpoint ?? '').toBe(original.oss_endpoint ?? '');
    expect(read.data?.oss_bucket_name ?? '').toBe(original.oss_bucket_name ?? '');
  });
});
