import { test, expect } from '@playwright/test';
import { newAdminContext, assertOk, type ApiResponse } from '../helpers/api';

/**
 * 模块 47：支付/退款配置写操作
 * 覆盖：
 *   - GET/PUT /admin/settings/refund：运费退还策略（枚举 always/never/user_decide）
 *     非法枚举值被 binding 拒绝；写入生效可回读
 *   - GET/PUT /admin/settings/payment：渠道开关与微信/支付宝配置
 *     alipay 渠道被服务端强制过滤（保持关闭）；原配置字段回读一致
 *   - 写后恢复原值，不污染环境（读→写→验证→恢复闭环）
 * 说明：证书上传（multipart/OSS 类）不在本模块范围
 */

/** 退款配置（运费退还策略） */
interface RefundSetting {
  refund_freight_policy: string;
}

/** 支付配置视图（与后端 PaymentSetting 对齐，断言相关字段） */
interface PaymentSetting {
  enabled_channels?: string[];
  wechat?: Record<string, unknown>;
  alipay?: Record<string, unknown>;
  [k: string]: unknown;
}

const POLICIES = ['always', 'never', 'user_decide'];

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
});

test.afterAll(async () => {
  await adminApi?.dispose();
});

test.describe.serial('退款配置（运费退还策略）', () => {
  let original = '';

  test('1. GET 读取原配置', async () => {
    const body = (await (
      await adminApi.get('/api/v1/admin/settings/refund')
    ).json()) as ApiResponse<RefundSetting>;
    original = assertOk(body, '读取退款配置').refund_freight_policy;
    expect(POLICIES, `原策略 ${original} 应为合法枚举`).toContain(original);
  });

  test('2. 非法枚举值被拒绝', async () => {
    const resp = await adminApi.put('/api/v1/admin/settings/refund', {
      data: { refund_freight_policy: 'auto_magic' },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '非法策略值应被参数校验拒绝').not.toBe(0);
    // 校验失败不落库
    const after = (await (
      await adminApi.get('/api/v1/admin/settings/refund')
    ).json()) as ApiResponse<RefundSetting>;
    expect(after.data?.refund_freight_policy).toBe(original);
  });

  test('3. 切换为另一合法策略并回读生效', async () => {
    const target = POLICIES.find((p) => p !== original)!;
    const resp = await adminApi.put('/api/v1/admin/settings/refund', {
      data: { refund_freight_policy: target },
    });
    const body = (await resp.json()) as ApiResponse<RefundSetting>;
    expect(body.code, `写入策略失败: ${body.message}`).toBe(0);
    const after = (await (
      await adminApi.get('/api/v1/admin/settings/refund')
    ).json()) as ApiResponse<RefundSetting>;
    expect(after.data?.refund_freight_policy, '写入后应回读到新策略').toBe(target);
  });

  test('4. 恢复原策略（不污染环境）', async () => {
    const resp = await adminApi.put('/api/v1/admin/settings/refund', {
      data: { refund_freight_policy: original },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
    const after = (await (
      await adminApi.get('/api/v1/admin/settings/refund')
    ).json()) as ApiResponse<RefundSetting>;
    expect(after.data?.refund_freight_policy, '环境应恢复原值').toBe(original);
  });
});

test.describe.serial('支付配置（渠道开关）', () => {
  let original: PaymentSetting | null = null;

  test('5. GET 读取原配置', async () => {
    const body = (await (
      await adminApi.get('/api/v1/admin/settings/payment')
    ).json()) as ApiResponse<PaymentSetting>;
    original = assertOk(body, '读取支付配置');
    expect(Array.isArray(original.enabled_channels), '渠道开关应为数组').toBe(true);
  });

  test('6. 写入含 alipay 的渠道数组：alipay 被服务端过滤，原渠道保留', async () => {
    if (!original) test.skip(true, '支付配置未读取');
    const channels = [...(original.enabled_channels ?? []), 'alipay'];
    const resp = await adminApi.put('/api/v1/admin/settings/payment', { data: { ...original, enabled_channels: channels } });
    const body = (await resp.json()) as ApiResponse<{ success: boolean }>;
    expect(body.code, `保存支付配置失败: ${body.message}`).toBe(0);

    const after = (await (
      await adminApi.get('/api/v1/admin/settings/payment')
    ).json()) as ApiResponse<PaymentSetting>;
    const saved = assertOk(after, '回读支付配置');
    expect(saved.enabled_channels, 'alipay 应被服务端强制过滤').not.toContain('alipay');
    for (const ch of original.enabled_channels ?? []) {
      expect(saved.enabled_channels, `原渠道 ${ch} 应保留`).toContain(ch);
    }
    // 微信配置字段应回读一致（私钥类字段不回传明文，仅比对 AppID）
    expect(saved.wechat?.app_id ?? saved.wechat?.appId, '微信 AppID 应保持原值').toBe(
      original.wechat?.app_id ?? original.wechat?.appId,
    );
  });

  test('7. 恢复原渠道配置（不污染环境）', async () => {
    if (!original) test.skip(true, '支付配置未读取');
    const resp = await adminApi.put('/api/v1/admin/settings/payment', { data: original });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code).toBe(0);
    const after = (await (
      await adminApi.get('/api/v1/admin/settings/payment')
    ).json()) as ApiResponse<PaymentSetting>;
    const restored = assertOk(after, '回读支付配置');
    expect([...(restored.enabled_channels ?? [])].sort(), '渠道配置应恢复原值').toEqual(
      [...(original.enabled_channels ?? [])].sort(),
    );
  });
});
