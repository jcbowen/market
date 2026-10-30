import { test, expect } from '@playwright/test';
import { newAdminContext, assertOk, type ApiResponse } from '../helpers/api';
import { autoName, uniqueSuffix } from '../helpers/data';

/**
 * 模块 71：站点设置（平台级品牌与部署域名）
 * 覆盖：
 *   - GET/PUT /admin/settings/site：单行配置读写，每次保存 version 递增（网关内存快照与 favicon 长缓存的失效依据）
 *   - 免登录公开接口 /api/v1/site-info：只回非敏感品牌字段
 *   - HTML 品牌注入：/b/ 与 /s/ 入口的 <title>、meta 取自站点设置，后台入口带 noindex、C 端入口带 og 标签
 *   - 服务端兜底：部署域名缺协议/带路径、站点名超长、logo 非法外链整行拒绝且不落库
 *   - 部署域名唯一来源：三条支付回调地址一律按生效域名派生；支付配置页提交伪造 notify_url 被忽略，
 *     站点域名保存后网关会把派生值回写全部支付配置行（handler/site.go syncPayCallbackURLs）
 *   - 仅超管可读写：settings:* 是系统级权限点，不进社区权限上限，社区角色勾了也会在解析时被剥离，
 *     非超管读与写一律 11005
 * 环境说明：用例把 base_url 设为独立测试域名，期间附件外链与支付回调地址随之派生；
 *           afterAll 无条件恢复原值，回调地址由站点保存的联动回写自动退回按请求域名派生的本机口径，
 *           不把测试域名留在库里；
 *           站点域名保存的联动补写会把空的附件「访问域名」写成测试域名（图片出库地址前缀随之改变，
 *           跨模块共享同一单行），故 afterAll 同时恢复附件配置快照，回读期望值按当前访问域名现算
 */

/** 站点设置视图（与网关 handler.SiteSetting 对齐） */
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
  effective_base_url: string;
  base_url_source: string;
  wechat_notify_url: string;
  alipay_notify_url: string;
  alipay_return_url: string;
}

/** 附件配置视图（仅本模块的域名联动补写与恢复所需字段） */
interface AttachmentConfig {
  storage_type?: string;
  visit_domain?: string;
  updated_at?: string;
  [k: string]: unknown;
}

/** 支付配置行视图（与网关 PayConfigView 对齐；多行制，仅断言回调地址派生相关字段） */
interface PayConfigRow {
  id: number;
  name: string;
  platform: string;
  enabled: boolean;
  wechat_notify_url?: string;
  alipay_notify_url?: string;
  alipay_return_url?: string;
  /** 版本号（= 最后更新时间），保存时乐观锁比对 */
  updated_at?: string;
  [k: string]: unknown;
}

/** GET /admin/settings/payment 响应体 */
interface PayConfigListResp {
  list?: PayConfigRow[];
  suggested_urls?: { base_url?: string; wechat_notify_url?: string; alipay_notify_url?: string };
}

const WECHAT_PATH = '/api/v1/payments/callback/wechat';
const ALIPAY_PATH = '/api/v1/payments/callback/alipay';
const ALIPAY_RETURN_PATH = '/s/#/pages/pay/result';
/** 部署域名用例值：与本机 localhost 明显不同，用于证明派生跟随配置而非请求 host */
const TEST_DOMAIN = 'https://site-test.example.com';

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let communityId = 0;

/**
 * 读取支付配置列表与按当前生效域名派生的回调地址建议
 * - 函数名: readPayConfigs
 * - 参数: 无（使用模块级 adminApi）
 * - 返回: Promise<PayConfigListResp>
 * - 异常: 业务码非 0 时由 assertOk 抛出
 * - 使用示例: const { list } = await readPayConfigs();
 */
async function readPayConfigs(): Promise<PayConfigListResp> {
  const body = (await (
    await adminApi.get('/api/v1/admin/settings/payment')
  ).json()) as ApiResponse<PayConfigListResp>;
  return assertOk(body, '读取支付配置');
}

/**
 * 逐行原样重存支付配置，使落库回调地址重新按当前生效域名派生
 * 站点域名清空后网关不会再回写（只认显式配置域名），库中会留着上一次派生出的测试域名，
 * 而下单时通知地址直接取库值——恢复现场时主动重存一次，不把测试域名留在库里
 * 每次写入都会推进版本号，故逐行重取最新 updated_at 再提交
 */
async function resavePayConfigs(): Promise<void> {
  const { list } = await readPayConfigs();
  for (const row of list) {
    const fresh = ((await readPayConfigs()).list ?? []).find((r) => r.id === row.id);
    if (!fresh) continue;
    const resp = (await (
      await adminApi.put(`/api/v1/admin/settings/payment/${fresh.id}`, { data: fresh })
    ).json()) as ApiResponse;
    if (resp.code !== 0) console.log(`[站点设置] 重存支付配置行 ${fresh.id} 失败 code=${resp.code} message=${resp.message}`);
  }
}
/** 进入模块时的原值，afterAll 无条件恢复 */
let original: SiteSetting | null = null;
/**
 * 附件配置原快照，afterAll 无条件恢复
 * 用例把站点域名设为测试域名时，站点保存的联动补写会把空的「附件访问域名」写成该测试域名，
 * 而图片/品牌资源出库按附件访问域名拼完整地址——不恢复会改变后续模块读到的 URL 形态（跨模块共享单行配置）
 */
let originalAttachment: AttachmentConfig | null = null;

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  communityId = admin.communityId;
  const attach = (await (
    await adminApi.get('/api/v1/admin/settings/attachment')
  ).json()) as ApiResponse<AttachmentConfig>;
  if (attach.code === 0) originalAttachment = attach.data ?? {};
});

test.afterAll(async () => {
  if (!adminApi) return;
  try {
    if (original) {
      // 每次保存都会推进版本号，恢复时须携带最新版本提交，否则被乐观锁拒绝（10007）
      const latest = await readSite();
      const back = (await (
        await adminApi.put('/api/v1/admin/settings/site', { data: { ...original, version: latest.version } })
      ).json()) as ApiResponse;
      if (back.code !== 0) console.log(`[站点设置] 恢复原值失败 code=${back.code} message=${back.message}`);

      // 站点域名恢复为空后网关不再回写回调地址，逐行重存把派生值退回按请求域名口径的本机地址
      await resavePayConfigs();
    }
    // 附件访问域名放在最后恢复：站点域名与回调地址的回写都可能再次触发联动补写
    if (originalAttachment) {
      const latestAttach = (await (
        await adminApi.get('/api/v1/admin/settings/attachment')
      ).json()) as ApiResponse<AttachmentConfig>;
      const backAttach = (await (
        await adminApi.put('/api/v1/admin/settings/attachment', {
          data: { ...originalAttachment, updated_at: latestAttach.data?.updated_at ?? '' },
        })
      ).json()) as ApiResponse;
      if (backAttach.code !== 0) console.log(`[站点设置] 恢复附件访问域名失败 code=${backAttach.code} message=${backAttach.message}`);
    }
  } finally {
    await adminApi.dispose();
  }
});

/**
 * 读取站点设置
 * 每次写入都会推进版本号，写入前需重取再提交
 * - 函数名: readSite
 * - 参数: 无（使用模块级 adminApi）
 * - 返回: Promise<SiteSetting>
 * - 异常: 业务码非 0 时抛 Error
 * - 使用示例: const cfg = await readSite();
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
 * - 使用示例: const domain = (await readAttachment()).visit_domain;
 */
async function readAttachment(): Promise<AttachmentConfig> {
  const body = (await (
    await adminApi.get('/api/v1/admin/settings/attachment')
  ).json()) as ApiResponse<AttachmentConfig>;
  return assertOk(body, '读取附件配置');
}

/**
 * 基于当前配置提交站点设置（覆盖式写入：未传字段即清空，故必须带全量底稿）
 * - 函数名: saveSite
 * - 参数: patch — 需要改写的字段
 * - 返回: Promise<ApiResponse> 保存响应（不校验业务码，交由用例断言）
 * - 异常: 网络异常时抛出
 * - 使用示例: await saveSite({ base_url: 'https://shop.example.com' });
 */
async function saveSite(patch: Partial<SiteSetting>): Promise<ApiResponse> {
  const current = await readSite();
  return (await (
    await adminApi.put('/api/v1/admin/settings/site', { data: { ...current, ...patch } })
  ).json()) as ApiResponse;
}

test.describe.serial('站点设置读写与品牌注入', () => {
  test('1. GET 返回完整字段，域名来源与派生回调地址齐备', async () => {
    const data = await readSite();
    original = data;
    for (const key of ['base_url', 'site_name', 'logo_url', 'favicon_url', 'footer_html', 'seo_description', 'seo_keywords']) {
      expect(typeof data[key as keyof SiteSetting], `${key} 应为字符串`).toBe('string');
    }
    expect(typeof data.version, '应返回配置版本号').toBe('number');
    // 未配置域名时按当前请求现推，派生地址不能留空，否则支付配置页无从展示
    expect(data.effective_base_url, '生效域名不应为空').toBeTruthy();
    expect(['env', 'db', 'request'], `域名来源标记异常：${data.base_url_source}`).toContain(data.base_url_source);
    expect(data.wechat_notify_url).toBe(`${data.effective_base_url}${WECHAT_PATH}`);
    expect(data.alipay_notify_url).toBe(`${data.effective_base_url}${ALIPAY_PATH}`);
    expect(data.alipay_return_url).toBe(`${data.effective_base_url}${ALIPAY_RETURN_PATH}`);
  });

  test('2. 保存品牌与 SEO 字段可回读，版本号递增', async () => {
    const before = original?.version ?? 0;
    // logo/favicon 入库归一为站内相对路径、出库一律拼成完整地址（services/user/server.go toSiteConfigOut）：
    // 前缀取附件「访问域名」，未配置时按当前请求域名现推。附件配置是跨模块共享的单行环境态，
    // 故期望值按当时的访问域名/生效域名现算，不写死相对路径（写死必然与服务端回读不一致）
    const domain = ((await readAttachment()).visit_domain ?? '').replace(/\/+$/, '');
    const prefix = domain || (await readSite()).effective_base_url;
    const brand = {
      site_name: autoName('站点'),
      logo_url: `${prefix}/uploads/auto-test-logo.png`,
      favicon_url: `${prefix}/uploads/auto-test-favicon.ico`,
      footer_html: `<p><b>© AUTO-TEST 社区商超 版权所有</b></p><p><a href="https://beian.miit.gov.cn/">京ICP备${uniqueSuffix()}号</a></p>`,
      seo_description: 'AUTO-TEST 社区商超：就近自提与配送到家的社区电商',
      seo_keywords: '社区团购,商超,自提',
    };
    const body = await saveSite(brand);
    expect(body.code, `保存站点设置失败: ${body.message}`).toBe(0);

    const saved = await readSite();
    const { footer_html: _footer, ...rest } = brand;
    expect(saved, '品牌与 SEO 字段应逐项回读一致').toMatchObject(rest);
    // footer 中的 <a> 由消毒器统一追加 target/rel/style，整串等值不成立，改断言稳定片段
    expect(saved.footer_html, '页脚文本应原样保留').toContain('© AUTO-TEST 社区商超 版权所有');
    expect(saved.footer_html, '备案号链接应保留').toContain('https://beian.miit.gov.cn/');
    expect(saved.footer_html).toContain('京ICP备');
    expect(saved.version, `版本号应在 ${before} 基础上递增`).toBeGreaterThan(before);
    // updated_at 精度到秒：用例 1 的读取与本次保存常落在同一秒内，旧断言要求「与快照不相等」
    // 会偶发失败（flaky 源在此，非产品缺陷）。时间戳只断言不回退，保存推进的确定性依据是 version
    expect(new Date(saved.updated_at).getTime(), '更新时间不得早于保存前快照')
      .toBeGreaterThanOrEqual(new Date(original!.updated_at).getTime());
  });

  test('3. 页脚富文本消毒：脚本/事件属性/危险链接被清洗后落库', async () => {
    const evil =
      '<p onclick="alert(1)" onmouseover="alert(1)">© AUTO-TEST 消毒用例</p>' +
      '<script>alert(2)</script><iframe src="https://evil.example.com"></iframe>' +
      '<a href="javascript:alert(3)" style="color:red">链接文字</a>' +
      '<a href="https://beian.miit.gov.cn/">正常链接</a>';
    const body = await saveSite({ footer_html: evil });
    expect(body.code, `保存站点设置失败: ${body.message}`).toBe(0);

    const saved = await readSite();
    expect(saved.footer_html, '不应残留脚本标签').not.toContain('script');
    expect(saved.footer_html, '不应残留内联框架').not.toContain('iframe');
    expect(saved.footer_html, '不应残留事件属性').not.toContain('onclick');
    expect(saved.footer_html, '不应残留事件属性').not.toContain('onmouseover');
    expect(saved.footer_html, '不应残留 javascript 协议').not.toContain('javascript');
    expect(saved.footer_html, '文本内容应保留').toContain('© AUTO-TEST 消毒用例');
    expect(saved.footer_html, '合法链接应保留').toContain('https://beian.miit.gov.cn/');
    expect(saved.footer_html, 'href 被剔除后链接文字应保留').toContain('链接文字');
  });

  test('4. 公开接口免登录，且不泄露配置类字段', async ({ request }) => {
    const current = await readSite();
    const resp = await request.get('/api/v1/site-info');
    expect(resp.status(), '免登录接口不应要求鉴权').toBe(200);
    const body = (await resp.json()) as ApiResponse<Record<string, unknown>>;
    const data = assertOk(body, '公开站点信息');
    expect(data.site_name).toBe(current.site_name);
    expect(data.footer_html).toBe(current.footer_html);
    // 公开面只给品牌字段：支付渠道绑定与商户凭证类配置不得出现在这里
    for (const key of ['pay_methods', 'client_channels', 'enabled_channels', 'wechat_mch_id', 'alipay_app_id']) {
      expect(data[key], `公开接口不应返回 ${key}`).toBeUndefined();
    }
  });

  test('5. HTML 入口注入品牌：标题与 meta 取站点设置，后台禁止收录', async ({ request }) => {
    const current = await readSite();
    expect(current.site_name, '前置：站点名称已配置').toBeTruthy();

    const adminResp = await request.get('/b/');
    expect(adminResp.status()).toBe(200);
    const adminHtml = await adminResp.text();
    expect(adminHtml, '后台标题应注入站点名称').toContain(`<title>${current.site_name} - 后台管理</title>`);
    expect(adminHtml, 'favicon 应带版本号查询串').toContain('/uploads/auto-test-favicon.ico?v=');
    expect(adminResp.headers()['x-robots-tag'] ?? '', '后台入口应禁止搜索引擎收录').toContain('noindex');

    const h5Resp = await request.get('/s/');
    expect(h5Resp.status()).toBe(200);
    const h5Html = await h5Resp.text();
    expect(h5Html, 'C 端标题应注入站点名称').toContain(`<title>${current.site_name}</title>`);
    expect(h5Html).toContain(`<meta name="description" content="${current.seo_description}" />`);
    expect(h5Html).toContain('<meta property="og:site_name"');
    expect(h5Resp.headers()['x-robots-tag'] ?? '', 'C 端入口不应被禁止收录').toBe('');
    // 锚点不得残留在响应里（说明替换真实发生，而非把占位符原样吐给爬虫）
    expect(h5Html, '注入锚点应被替换').not.toContain('name="brand-head"');
  });

  test('6. 非法入参整行拒绝且不落库', async () => {
    const before = await readSite();

    const cases: { label: string; patch: Partial<SiteSetting>; hint: string }[] = [
      { label: '部署域名缺协议', patch: { base_url: 'site-test.example.com' }, hint: '部署域名' },
      { label: '部署域名带路径', patch: { base_url: 'https://site-test.example.com/pay' }, hint: '路径' },
      { label: '部署域名非 http(s)', patch: { base_url: 'ftp://site-test.example.com' }, hint: '协议' },
      { label: '站点名称超长', patch: { site_name: '社'.repeat(33) }, hint: '站点名称' },
      { label: 'logo 非法外链', patch: { logo_url: 'javascript:alert(1)' }, hint: 'logo' },
    ];
    for (const c of cases) {
      const body = await saveSite(c.patch);
      expect(body.code, `${c.label} 应被服务端拒绝`).not.toBe(0);
      expect(body.message ?? '', `${c.label} 拒绝原因应透传`).toContain(c.hint);
    }

    const after = await readSite();
    expect({ ...after, version: 0, updated_at: '' }, '拒绝后配置不应被改写').toEqual({ ...before, version: 0, updated_at: '' });
  });

  test('7. 部署域名生效：来源标记为站点设置，回调地址按配置域名派生', async () => {
    const body = await saveSite({ base_url: TEST_DOMAIN });
    expect(body.code, `保存部署域名失败: ${body.message}`).toBe(0);

    const data = await readSite();
    expect(data.base_url, '应回读到大写协议已归一的域名').toBe(TEST_DOMAIN);
    // 站点设置显式保存后恒为最高优先级：即便部署侧设置了 SITE_BASE_URL，也只在 DB 未配置时兜底
    expect(data.base_url_source, '显式保存后来源应为站点设置').toBe('db');
    expect(data.effective_base_url).toBe(TEST_DOMAIN);
    const eff = data.effective_base_url;
    expect(data.wechat_notify_url).toBe(`${eff}${WECHAT_PATH}`);
    expect(data.alipay_notify_url).toBe(`${eff}${ALIPAY_PATH}`);
    expect(data.alipay_return_url).toBe(`${eff}${ALIPAY_RETURN_PATH}`);
  });

  test('8. 支付配置回调地址只读派生并回写全部配置行，前端入参被忽略', async () => {
    const current = await readSite();
    const eff = current.effective_base_url;
    const wechatNotify = `${eff}${WECHAT_PATH}`;
    const alipayNotify = `${eff}${ALIPAY_PATH}`;
    const alipayReturn = `${eff}${ALIPAY_RETURN_PATH}`;

    const pay = await readPayConfigs();
    expect(pay.suggested_urls?.base_url, '引导基址应与生效域名一致').toBe(eff);
    expect(pay.suggested_urls?.wechat_notify_url, '引导的微信通知地址应按生效域名派生').toBe(wechatNotify);
    expect(pay.suggested_urls?.alipay_notify_url, '引导的支付宝通知地址应按生效域名派生').toBe(alipayNotify);

    // 用例 7 保存部署域名后，网关已把派生地址逐行回写配置（handler/site.go syncPayCallbackURLs）
    const rows = pay.list ?? [];
    for (const row of rows) {
      if (row.platform === 'wechat') {
        expect(row.wechat_notify_url, `微信行 ${row.id} 通知地址应为派生值`).toBe(wechatNotify);
      }
      if (row.platform === 'alipay') {
        expect(row.alipay_notify_url, `支付宝行 ${row.id} 通知地址应为派生值`).toBe(alipayNotify);
        expect(row.alipay_return_url, `支付宝行 ${row.id} 回跳地址应为派生值`).toBe(alipayReturn);
      }
    }
    if (rows.length === 0) test.skip(true, '库中无支付配置行，无法验证前端入参不被采信');

    // 提交伪造回调地址：保存应成功（其余字段照常落库），但三个地址一律被派生值覆盖
    const target = rows[0];
    const evil = 'https://evil.example.com/callback';
    const forged = (await (
      await adminApi.put(`/api/v1/admin/settings/payment/${target.id}`, {
        data: { ...target, wechat_notify_url: evil, alipay_notify_url: evil, alipay_return_url: evil },
      })
    ).json()) as ApiResponse;
    expect(forged.code, `保存支付配置失败: ${forged.message}`).toBe(0);

    const saved = (await readPayConfigs()).list?.find((r) => r.id === target.id);
    if (target.platform === 'wechat') {
      expect(saved?.wechat_notify_url, '微信通知地址不得被前端改写').toBe(wechatNotify);
    } else {
      expect(saved?.alipay_notify_url, '支付宝通知地址不得被前端改写').toBe(alipayNotify);
      expect(saved?.alipay_return_url, '支付宝回跳地址不得被前端改写').toBe(alipayReturn);
    }
  });

  // 站点设置是平台级配置：读写都要求 settings:* 系统级权限点，而系统级码不进社区权限上限
  // （《04-业务需求/05-权限与数据隔离》），社区语境下 GetAdminContext 取交集后必然被剥离，
  // 因此即便给社区角色显式勾上 settings:view/update，非超管读与写都应当被拒。
  // 旧口径断言「有 settings:view 即可读」把「角色表里配了」当成「解析后还有」，与设计的
  // 系统级收窄相悖，属用例写错而非产品缺陷。
  test('9. 非超管即便被勾了 settings:* 权限点也不得读写站点设置', async ({ playwright }) => {
    const role = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/roles', {
          data: {
            name: autoName('站点配置角色'),
            code: `site_${uniqueSuffix()}`,
            permissions: ['settings:view', 'settings:update'],
            community_id: communityId,
          },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建站点配置角色',
    );
    const username = `site_${uniqueSuffix()}`;
    const emp = assertOk(
      (await (
        await adminApi.post('/api/v1/admin/admins', {
          data: { username, password: 'Site@12345', real_name: 'AUTO-TEST 站点员工', role_ids: [role.id], community_id: communityId },
        })
      ).json()) as ApiResponse<{ id: number }>,
      '创建站点员工',
    );

    const loginCtx = await playwright.request.newContext();
    const login = (await (
      await loginCtx.post('/api/v1/admin/auth/login', { data: { username, password: 'Site@12345' } })
    ).json()) as ApiResponse<{ access_token: string }>;
    await loginCtx.dispose();
    expect(login.code, `员工登录失败: ${login.message}`).toBe(0);

    const empCtx = await playwright.request.newContext({
      extraHTTPHeaders: { Authorization: `Bearer ${login.data!.access_token}`, 'X-Community-Id': String(communityId) },
    });
    try {
      const read = (await (await empCtx.get('/api/v1/admin/settings/site')).json()) as ApiResponse;
      expect(read.code, '系统级权限点不进社区权限上限，非超管读站点设置必须被拒').toBe(11005);

      const nameBefore = (await readSite()).site_name;
      const write = (await (
        await empCtx.put('/api/v1/admin/settings/site', { data: { ...(original as SiteSetting), site_name: autoName('越权站点') } })
      ).json()) as ApiResponse;
      // 网关恒返回 HTTP 200 + 业务码，权限拒绝表达为 11005（NoPermission）
      expect(write.code, '站点设置为平台级配置，仅超管可写').toBe(11005);
      expect((await readSite()).site_name, '拒绝后站点名称不应被改写').toBe(nameBefore);
    } finally {
      await empCtx.dispose();
      await adminApi.delete(`/api/v1/admin/admins/${emp.id}`);
      await adminApi.delete(`/api/v1/admin/roles/${role.id}`);
    }
  });
});
