import { test, expect } from '@playwright/test';
import { newAdminContext, assertOk, type ApiResponse } from '../helpers/api';

/**
 * 模块 47：支付/退款配置写操作
 * 覆盖：
 *   - GET/PUT /admin/settings/refund：运费退还策略（枚举 always/never/user_decide）
 *     非法枚举值被 binding 拒绝；写入生效可回读
 *   - 支付配置多行制（2026-10-30 支付渠道重构后的契约）：POST 建空行、GET 返回列表与派生回调地址、
 *     PUT /:id 整行覆写；支付宝行绑定进客户端渠道 pay_methods（<platform>:<config_id>）后即持久化，
 *     C 端是否下发该支付方式取决于行的 ready 就绪状态（fail-closed）
 *   - 渠道保存的服务端门槛：绑定微信支付但渠道 AppID 为空被拒（10004）且不改写已存绑定
 *   - 支付宝加签方式（公钥模式/证书模式）读写贯通：mode 可保存回读，证书/私钥内容不回传明文，
 *     证书未齐时判未就绪并给出与模式对应的原因文案
 *   - 异步通知地址唯一来源是部署域名：前端入参（含伪造地址）不落库，回读恒为派生值
 *   - 保存携带行 updated_at 作版本号做乐观锁比对，过期版本提交被拒（10007）且不改写库中该行
 *   - 写后恢复原绑定、删除临时行，不污染环境（读→写→验证→恢复闭环）
 * 说明：证书上传（multipart）需真实证书文件，不在本模块范围，由 payment 服务离线测试覆盖
 */

/** 退款配置（运费退还策略） */
interface RefundSetting {
  refund_freight_policy: string;
}

/** 支付配置行视图（与网关 PayConfigView 对齐；多行制，2026-10-30 支付渠道重构后契约） */
interface PayConfigRow {
  id: number;
  name: string;
  platform: string;
  enabled: boolean;
  wechat_mch_id?: string;
  wechat_notify_url?: string;
  alipay_app_id?: string;
  /** 加签方式：'' / public 为公钥模式，cert 为证书模式 */
  alipay_mode?: string;
  alipay_notify_url?: string;
  alipay_return_url?: string;
  alipay_app_cert_uploaded?: boolean;
  alipay_public_cert_uploaded?: boolean;
  alipay_root_cert_uploaded?: boolean;
  /** 就绪状态（启用 + 当前平台必填齐全 + 密钥自检通过），决定 C 端是否下发 */
  ready?: boolean;
  ready_message?: string;
  /** 版本号（= 最后更新时间），保存时乐观锁比对 */
  updated_at?: string;
  [k: string]: unknown;
}

/** 客户端渠道视图（与网关 ClientChannelView 对齐；pay_methods 为有序绑定） */
interface ClientChannelRow {
  id: number;
  code: string;
  name: string;
  app_id: string;
  app_secret: string;
  enabled: boolean;
  pay_methods: string[];
  configured: boolean;
  updated_at: string;
  [k: string]: unknown;
}

/** 本模块临时支付宝配置行名：beforeAll 创建、afterAll 删除，不留残留 */
const TEMP_ALIPAY_NAME = 'AUTO-TEST 支付宝配置';
/** 数据已被他人修改（errcode.DataConflict） */
const CODE_DATA_CONFLICT = 10007;
/** 前置条件不满足（errcode.OperationNotAllowed，网关由 gRPC FailedPrecondition 映射） */
const CODE_OPERATION_NOT_ALLOWED = 10004;

const POLICIES = ['always', 'never', 'user_decide'];

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
/** 临时支付宝配置行 ID，beforeAll 创建后赋值 */
let alipayRowID = 0;
/** h5 与 pos 渠道进入模块时的原始绑定，afterAll 无条件恢复 */
let h5Original: ClientChannelRow | null = null;
let posOriginal: ClientChannelRow | null = null;

/**
 * 读取支付配置列表与派生回调地址
 * - 函数名: readPaymentConfigs
 * - 参数: 无
 * - 返回值: Promise<{ list: PayConfigRow[]; suggested: Record<string, string> }>
 * - 异常: 业务码非 0 时由 assertOk 抛出
 * - 使用示例: const { list, suggested } = await readPaymentConfigs()
 */
async function readPaymentConfigs(): Promise<{ list: PayConfigRow[]; suggested: Record<string, string> }> {
  const body = (await (
    await adminApi.get('/api/v1/admin/settings/payment')
  ).json()) as ApiResponse<{ list?: PayConfigRow[]; suggested_urls?: Record<string, string> }>;
  const data = assertOk(body, '读取支付配置');
  return { list: data.list ?? [], suggested: data.suggested_urls ?? {} };
}

/**
 * 按 ID 读取单条支付配置行
 * 每次写入都会推进版本号，写入前需重取该行再提交，避免误触乐观锁
 */
async function readPayRow(id: number): Promise<PayConfigRow> {
  const { list } = await readPaymentConfigs();
  const row = list.find((r) => r.id === id);
  if (!row) throw new Error(`支付配置行 ${id} 不存在`);
  return row;
}

/**
 * 按渠道 code 读取客户端渠道视图
 */
async function readChannel(code: string): Promise<ClientChannelRow> {
  const body = (await (
    await adminApi.get('/api/v1/admin/settings/channels')
  ).json()) as ApiResponse<{ channels?: ClientChannelRow[] }>;
  const row = (assertOk(body, '读取客户端渠道').channels ?? []).find((ch) => ch.code === code);
  if (!row) throw new Error(`客户端渠道 ${code} 不存在`);
  return row;
}

/**
 * 读取显式部署域名（支付回调地址的唯一派生源）
 * 站点设置 base_url 或 SITE_BASE_URL 任一有值即返回该基址；两者都空时来源标记为 request，
 * 按请求现推的域名只作页面展示、不落库，回调地址口径为空串（该行由就绪判定自然判未就绪）
 * - 函数名: readExplicitBaseURL
 * - 返回值: Promise<string> 形如 https://host，未配置显式部署域名时为空串
 * - 使用示例: const base = await readExplicitBaseURL()
 */
async function readExplicitBaseURL(): Promise<string> {
  const body = (await (
    await adminApi.get('/api/v1/admin/settings/site')
  ).json()) as ApiResponse<{ base_url_source?: string; effective_base_url?: string }>;
  const data = assertOk(body, '读取站点设置');
  return data.base_url_source === 'db' || data.base_url_source === 'env' ? data.effective_base_url ?? '' : '';
}

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;

  // 建一条凭证留空、默认停用的支付宝配置行：本模块的加签方式/就绪判定/回调派生都需要有行可写，
  // 而本机没有真实商户凭据（也不应写入），空行既能承载读写又不参与下单
  const created = (await (
    await adminApi.post('/api/v1/admin/settings/payment', {
      data: { name: TEMP_ALIPAY_NAME, platform: 'alipay', enabled: false },
    })
  ).json()) as ApiResponse<PayConfigRow>;
  alipayRowID = assertOk(created, `创建临时支付宝配置行失败: ${created.message}`).id;

  h5Original = await readChannel('h5');
  posOriginal = await readChannel('pos');
});

test.afterAll(async () => {
  // 环境恢复：渠道绑定回到进入模块时的原值（临时行可能已被绑定），再删除临时行
  try {
    for (const snapshot of [h5Original, posOriginal]) {
      if (!snapshot) continue;
      const current = await readChannel(snapshot.code);
      await adminApi.put(`/api/v1/admin/settings/channels/${current.id}`, {
        data: {
          code: current.code,
          name: current.name,
          app_id: current.app_id,
          app_secret: '',
          enabled: current.enabled,
          pay_methods: snapshot.pay_methods ?? [],
        },
      });
    }
    if (alipayRowID > 0) {
      await adminApi.delete(`/api/v1/admin/settings/payment/${alipayRowID}`);
    }
  } finally {
    await adminApi?.dispose();
  }
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

test.describe.serial('支付配置多行制与渠道绑定', () => {
  test('5. GET 返回列表与派生回调地址', async () => {
    const { list, suggested } = await readPaymentConfigs();
    expect(Array.isArray(list), '支付配置应为多行列表').toBe(true);
    for (const row of list) {
      expect(['wechat', 'alipay'], `配置行 platform 应为合法平台，实为 ${row.platform}`).toContain(row.platform);
      expect(row.updated_at ?? '', '每行应回传版本号').not.toBe('');
    }
    const temp = list.find((r) => r.id === alipayRowID);
    expect(temp, '前置：临时支付宝配置行应在列表中').toBeTruthy();
    expect(temp?.platform, '临时行平台应为 alipay').toBe('alipay');
    // 建议值与落库同源：只按显式部署域名派生，未配置时为空（不给管理员一个不会落库的地址）
    const base = await readExplicitBaseURL();
    expect(
      suggested.alipay_notify_url ?? '',
      `支付宝通知地址应按显式部署域名派生（当前域名来源为 ${base ? '已配置' : '未配置'}）`,
    ).toBe(base === '' ? '' : `${base}/api/v1/payments/callback/alipay`);
  });

  test('6. 渠道绑定支付宝：绑定持久化保留，C 端下发与否取决于就绪状态', async ({ request }) => {
    const entry = `alipay:${alipayRowID}`;
    const methods = [...(h5Original?.pay_methods ?? []), entry];
    const h5 = await readChannel('h5');
    const saved = (await (
      await adminApi.put(`/api/v1/admin/settings/channels/${h5.id}`, {
        data: { code: h5.code, name: h5.name, app_id: h5.app_id, app_secret: '', enabled: h5.enabled, pay_methods: methods },
      })
    ).json()) as ApiResponse<ClientChannelRow>;
    expect(saved.code, `保存渠道绑定失败: ${saved.message}`).toBe(0);

    const after = await readChannel('h5');
    expect(after.pay_methods, '绑定条目应被持久化').toContain(entry);
    for (const m of h5Original?.pay_methods ?? []) {
      expect(after.pay_methods, `原绑定 ${m} 应保留`).toContain(m);
    }
    // 顺序即收银台展示顺序，新增条目应落在末尾
    expect(after.pay_methods[after.pay_methods.length - 1], '新增绑定应排在末尾').toBe(entry);

    // 绑定与能力就绪是两件事：凭证未配齐的行必然未就绪（fail-closed），C 端不得下发该方式，
    // 否则用户点进一个必然失败的支付方式；线上配齐密钥后 ready=true 则应当下发。
    const row = await readPayRow(alipayRowID);
    const cBody = (await (await request.get('/api/v1/payments/channels')).json()) as ApiResponse<{
      channels?: { channel: string }[];
    }>;
    expect(cBody.code, `读取 C 端支付方式失败: ${cBody.message}`).toBe(0);
    const offered = (cBody.data?.channels ?? []).map((c) => c.channel);
    if (row.ready === true) {
      expect(offered, '能力已就绪且已绑定时 C 端应下发 alipay').toContain('alipay');
    } else {
      expect(offered, '能力未就绪时 C 端不得下发 alipay').not.toContain('alipay');
      expect(row.ready_message, '未就绪时应返回原因文案，否则管理员无从下手').toBeTruthy();
    }
  });

  test('7. 恢复 h5 原绑定（不污染环境）', async () => {
    if (!h5Original) test.skip(true, 'h5 渠道未读取');
    const h5 = await readChannel('h5');
    const resp = (await (
      await adminApi.put(`/api/v1/admin/settings/channels/${h5.id}`, {
        data: {
          code: h5.code, name: h5.name, app_id: h5.app_id, app_secret: '',
          enabled: h5.enabled, pay_methods: h5Original.pay_methods ?? [],
        },
      })
    ).json()) as ApiResponse;
    expect(resp.code, `恢复渠道绑定失败: ${resp.message}`).toBe(0);
    const after = await readChannel('h5');
    expect(after.pay_methods, '绑定应恢复原值').toEqual(h5Original?.pay_methods ?? []);
  });

  test('8. 绑定微信支付但渠道 AppID 为空应被拒绝（10004）', async () => {
    if (!posOriginal) test.skip(true, 'pos 渠道未读取');
    const wechatRow = (await readPaymentConfigs()).list.find((r) => r.platform === 'wechat');
    if (!wechatRow) test.skip(true, '库中无微信配置行，无法构造绑定');

    // pos 无登录凭证，AppID 只能由管理员显式填写；此处故意留空提交绑定微信的请求
    const pos = await readChannel('pos');
    const body = (await (
      await adminApi.put(`/api/v1/admin/settings/channels/${pos.id}`, {
        data: {
          code: pos.code, name: pos.name, app_id: '', app_secret: '', enabled: pos.enabled,
          pay_methods: [...(pos.pay_methods ?? []), `wechat:${wechatRow.id}`],
        },
      })
    ).json()) as ApiResponse;
    expect(body.code, `绑定微信但缺 AppID 应被拒绝，实为 code=${body.code}`).toBe(CODE_OPERATION_NOT_ALLOWED);
    expect(body.message, '应透传服务端门槛文案').toContain('绑定微信支付需先填写渠道 AppID');

    const after = await readChannel('pos');
    expect(after.pay_methods, '拒绝后渠道绑定不应被改写').toEqual(pos.pay_methods);
  });
});

test.describe.serial('支付宝加签方式与通知地址（按配置行读写）', () => {
  /**
   * 读取临时支付宝配置行的当前视图
   * 每次写入都会推进版本号，写入前需重取该行再提交，避免误触乐观锁
   */
  async function readRow(): Promise<PayConfigRow> {
    return readPayRow(alipayRowID);
  }

  test('9. GET 回传加签方式与三证书上传标志，且证书内容不出现在视图里', async () => {
    const row = await readRow();
    // 新建行 mode 为空串，后台按公钥模式展示，接口仍应稳定回传该字段
    expect(typeof row.alipay_mode, '应回传加签方式字段').toBe('string');
    for (const key of ['alipay_app_cert_uploaded', 'alipay_public_cert_uploaded', 'alipay_root_cert_uploaded']) {
      expect(typeof row[key], `${key} 应为布尔标志`).toBe('boolean');
    }
    // 证书与私钥内容只由上传接口写入，读视图永不回传明文（连字段都不该出现）
    for (const key of ['app_cert', 'alipay_public_cert', 'alipay_root_cert', 'alipay_private_key']) {
      expect(row[key], `视图不应出现证书/私钥字段 ${key}`).toBeUndefined();
    }
  });

  test('10. 保存为证书模式：证书未齐即判未就绪且原因按模式给出', async () => {
    const current = await readRow();
    const resp = (await (
      await adminApi.put(`/api/v1/admin/settings/payment/${current.id}`, {
        data: { ...current, alipay_mode: 'cert', updated_at: current.updated_at },
      })
    ).json()) as ApiResponse;
    expect(resp.code, `保存证书模式失败: ${resp.message}`).toBe(0);

    const saved = await readRow();
    expect(saved.alipay_mode, '加签方式应持久化为证书模式').toBe('cert');
    const allUploaded = ['alipay_app_cert_uploaded', 'alipay_public_cert_uploaded', 'alipay_root_cert_uploaded']
      .every((k) => saved[k] === true);
    if (!allUploaded) {
      expect(saved.ready, '证书未齐时该配置行不应判就绪').toBe(false);
      expect(saved.ready_message ?? '', '未就绪原因应指向证书模式必填项').toContain('证书模式');
    }
  });

  // 站点设置落地后，回调地址唯一来源是显式部署域名：前端入参（含伪造的非法地址）不参与落库，
  // payment 侧的格式兜底校验只对 gRPC 直连调用生效，管理端不再提供手填入口
  test('11. 异步通知地址为派生值，前端入参不被采信', async () => {
    const base = await readExplicitBaseURL();
    const derived = base === '' ? '' : `${base}/api/v1/payments/callback/alipay`;
    const { suggested } = await readPaymentConfigs();
    expect(suggested.alipay_notify_url ?? '', '建议值应与显式部署域名派生口径一致').toBe(derived);

    const current = await readRow();
    const resp = (await (
      await adminApi.put(`/api/v1/admin/settings/payment/${current.id}`, {
        data: { ...current, alipay_notify_url: 'market.example.com/callback', updated_at: current.updated_at },
      })
    ).json()) as ApiResponse;
    expect(resp.code, `保存支付配置失败: ${resp.message}`).toBe(0);

    const after = await readRow();
    expect(after.alipay_notify_url ?? '', '伪造的通知地址应被派生值覆写').toBe(derived);
  });

  test('12. 过期版本号提交被拒且该行未被改写（乐观锁）', async () => {
    const current = await readRow();
    const resp = (await (
      await adminApi.put(`/api/v1/admin/settings/payment/${current.id}`, {
        data: { ...current, alipay_mode: 'public', name: `${TEMP_ALIPAY_NAME}-并发覆盖`, updated_at: '2000-01-01 00:00:00' },
      })
    ).json()) as ApiResponse;
    expect(resp.code, `过期版本提交应被乐观锁拒绝，实为 code=${resp.code}`).toBe(CODE_DATA_CONFLICT);
    expect(resp.message, '应透传服务端冲突文案').toContain('支付配置已被其他操作更新');

    const after = await readRow();
    expect(after.name, '拒绝后行名称不应被改写').toBe(current.name);
    expect(after.alipay_mode, '拒绝后加签方式不应被改写').toBe(current.alipay_mode);
  });
});
