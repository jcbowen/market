import { test, expect } from '@playwright/test';
import { newAdminContext, assertOk, type ApiResponse } from '../helpers/api';

/**
 * 模块 47：支付/退款配置写操作
 * 覆盖：
 *   - GET/PUT /admin/settings/refund：运费退还策略（枚举 always/never/user_decide）
 *     非法枚举值被 binding 拒绝；写入生效可回读
 *   - GET/PUT /admin/settings/payment：渠道开关与微信/支付宝配置
 *     alipay 写入后可持久化保留（写侧不再强制过滤），但 C 端是否下发取决于 alipay_ready 就绪状态
 *     保存携带 updated_at 作版本号做乐观锁比对，过期版本提交被拒且不改写库中配置
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
  /** 支付宝能力就绪标志（字段齐全且密钥自检通过才为 true），决定 C 端是否下发该渠道 */
  alipay_ready?: boolean;
  /** 支付宝密钥自检失败原因，空表示通过；就绪为 false 时后台据此提示 */
  alipay_ready_message?: string;
  /** 配置版本号（= 最后更新时间），保存时乐观锁比对 */
  updated_at?: string;
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

  test('6. 写入含 alipay 的渠道数组：配置持久化保留，C 端下发与否取决于就绪状态', async ({ request }) => {
    if (!original) test.skip(true, '支付配置未读取');
    const channels = [...(original.enabled_channels ?? []), 'alipay'];
    const resp = await adminApi.put('/api/v1/admin/settings/payment', { data: { ...original, enabled_channels: channels } });
    const body = (await resp.json()) as ApiResponse<{ success: boolean }>;
    expect(body.code, `保存支付配置失败: ${body.message}`).toBe(0);

    const after = (await (
      await adminApi.get('/api/v1/admin/settings/payment')
    ).json()) as ApiResponse<PaymentSetting>;
    const saved = assertOk(after, '回读支付配置');
    // 支付宝已完成接入，写侧不再有「保持关闭」的强制过滤：勾选即持久化
    expect(saved.enabled_channels, 'alipay 勾选后应被持久化保留').toContain('alipay');
    for (const ch of original.enabled_channels ?? []) {
      expect(saved.enabled_channels, `原渠道 ${ch} 应保留`).toContain(ch);
    }

    // 勾选与能力就绪是两件事：本机无商户配置时 alipay_ready=false，C 端不得出现该渠道，
    // 否则用户点进一个必然失败的支付方式；线上配齐密钥后 ready=true 则应当下发。
    // 断言按就绪标志分支，使本用例在两种环境下都成立，不会因开通商户配置而反向翻红。
    const ready = saved.alipay_ready === true;
    // 未就绪时后台需拿到可读原因（字段缺失或密钥自检失败），否则管理员只看到「未配置」无从下手
    if (!ready) {
      expect(saved.alipay_ready_message, '未就绪时应返回原因文案').toBeTruthy();
    }
    const cBody = (await (await request.get('/api/v1/payments/channels')).json()) as ApiResponse<{
      channels?: { channel: string }[];
    }>;
    expect(cBody.code, `读取 C 端支付渠道失败: ${cBody.message}`).toBe(0);
    const offered = (cBody.data?.channels ?? []).map((c) => c.channel);
    if (ready) {
      expect(offered, '能力已就绪且已勾选时 C 端应下发 alipay').toContain('alipay');
    } else {
      expect(offered, '能力未就绪时 C 端不得下发 alipay').not.toContain('alipay');
    }

    // 微信配置字段应回读一致（私钥类字段不回传明文，仅比对 AppID）
    expect(saved.wechat?.app_id ?? saved.wechat?.appId, '微信 AppID 应保持原值').toBe(
      original.wechat?.app_id ?? original.wechat?.appId,
    );
  });

  test('7. 恢复原渠道配置（不污染环境）', async () => {
    if (!original) test.skip(true, '支付配置未读取');
    // 保存带版本号做乐观锁比对，上一个用例已推进版本，故恢复前必须重读取最新版本
    const fresh = (await (
      await adminApi.get('/api/v1/admin/settings/payment')
    ).json()) as ApiResponse<PaymentSetting>;
    const current = assertOk(fresh, '恢复前读取最新配置');
    const resp = await adminApi.put('/api/v1/admin/settings/payment', {
      data: { ...original, updated_at: current.updated_at },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, `恢复支付配置失败: ${body.message}`).toBe(0);
    const after = (await (
      await adminApi.get('/api/v1/admin/settings/payment')
    ).json()) as ApiResponse<PaymentSetting>;
    const restored = assertOk(after, '回读支付配置');
    expect([...(restored.enabled_channels ?? [])].sort(), '渠道配置应恢复原值').toEqual(
      [...(original.enabled_channels ?? [])].sort(),
    );
  });

  test('8. 携带过期版本号保存应被拒绝（乐观锁防并发覆盖）', async () => {
    if (!original) test.skip(true, '支付配置未读取');
    const fresh = (await (
      await adminApi.get('/api/v1/admin/settings/payment')
    ).json()) as ApiResponse<PaymentSetting>;
    const current = assertOk(fresh, '读取最新配置');

    // 模拟另一管理员先行改写：本实例再提交读取时的旧版本，必须被拒且不改写库
    const resp = await adminApi.put('/api/v1/admin/settings/payment', {
      data: {
        ...current,
        updated_at: '2000-01-01 00:00:00',
        enabled_channels: [...(current.enabled_channels ?? []), 'alipay'],
      },
    });
    const body = (await resp.json()) as ApiResponse;
    expect(body.code, '过期版本提交应被乐观锁拒绝').not.toBe(0);

    const after = (await (
      await adminApi.get('/api/v1/admin/settings/payment')
    ).json()) as ApiResponse<PaymentSetting>;
    const saved = assertOk(after, '回读支付配置');
    expect(saved.enabled_channels ?? [], '冲突拒绝后渠道配置不应被改写').toEqual(
      current.enabled_channels ?? [],
    );
  });
});
