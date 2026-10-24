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
 *   - 部署域名唯一来源：三条支付回调地址一律按生效域名派生；支付配置页提交伪造 notify_url 被忽略
 *   - 仅超管可写：具备 settings:update 权限的社区管理员写入返回 11005（读仍开放）
 * 环境说明：用例把 base_url 设为独立测试域名，期间附件外链与支付回调地址随之派生；
 *           afterAll 无条件恢复原值，并把支付回调地址退回按请求域名派生的本机口径，不把测试域名留在库里
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

/** 支付配置视图（仅断言回调地址派生相关字段） */
interface PaymentSetting {
  wechat?: { notify_url?: string; [k: string]: unknown };
  alipay?: { notify_url?: string; return_url?: string; [k: string]: unknown };
  suggested_urls?: { base_url?: string; wechat_notify_url?: string; alipay_notify_url?: string };
  updated_at?: string;
  [k: string]: unknown;
}

const WECHAT_PATH = '/api/v1/payments/callback/wechat';
const ALIPAY_PATH = '/api/v1/payments/callback/alipay';
const ALIPAY_RETURN_PATH = '/s/#/pages/pay/result';
/** 部署域名用例值：与本机 localhost 明显不同，用于证明派生跟随配置而非请求 host */
const TEST_DOMAIN = 'https://site-test.example.com';

let adminApi: Awaited<ReturnType<typeof newAdminContext>>['api'];
let communityId = 0;
/** 进入模块时的原值，afterAll 无条件恢复 */
let original: SiteSetting | null = null;

test.beforeAll(async ({ playwright }) => {
  const admin = await newAdminContext(playwright);
  adminApi = admin.api;
  communityId = admin.communityId;
});

test.afterAll(async () => {
  if (!adminApi) return;
  try {
    if (original) {
      const back = (await (
        await adminApi.put('/api/v1/admin/settings/site', { data: original })
      ).json()) as ApiResponse;
      if (back.code !== 0) console.log(`[站点设置] 恢复原值失败 code=${back.code} message=${back.message}`);

      // 站点域名恢复为空后，网关按请求 host 派生，回写一次即可把回调地址从测试域名退回本机口径
      const pay = (await (
        await adminApi.get('/api/v1/admin/settings/payment')
      ).json()) as ApiResponse<PaymentSetting>;
      if (pay.code === 0) {
        const again = (await (
          await adminApi.put('/api/v1/admin/settings/payment', { data: { ...pay.data, updated_at: pay.data?.updated_at } })
        ).json()) as ApiResponse;
        if (again.code !== 0) console.log(`[站点设置] 回调地址退回本机口径失败 code=${again.code} message=${again.message}`);
      }
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
    const brand = {
      site_name: autoName('站点'),
      logo_url: '/uploads/auto-test-logo.png',
      favicon_url: '/uploads/auto-test-favicon.ico',
      footer_html: `<p><b>© AUTO-TEST 社区商超 版权所有</b></p><p><a href="https://beian.miit.gov.cn/">京ICP备${uniqueSuffix()}号</a></p>`,
      seo_description: 'AUTO-TEST 社区商超：就近自提与配送到家的社区电商',
      seo_keywords: '社区团购,商超,自提',
    };
    const body = await saveSite(brand);
    expect(body.code, `保存站点设置失败: ${body.message}`).toBe(0);

    const saved = await readSite();
    expect(saved, '品牌与 SEO 字段应逐项回读一致').toMatchObject(brand);
    expect(saved.version, `版本号应在 ${before} 基础上递增`).toBeGreaterThan(before);
    expect(saved.updated_at, '更新时间应刷新').not.toBe(original?.updated_at);
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
    // 公开面只给品牌字段：支付渠道与密钥类配置不得出现在这里
    for (const key of ['wechat', 'alipay', 'enabled_channels']) {
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

  test('8. 支付配置页回调地址只读派生，前端入参被忽略', async () => {
    const current = await readSite();
    const eff = current.effective_base_url;

    const read = (await (
      await adminApi.get('/api/v1/admin/settings/payment')
    ).json()) as ApiResponse<PaymentSetting>;
    const pay = assertOk(read, '读取支付配置');
    expect(pay.suggested_urls?.base_url, '引导基址应与生效域名一致').toBe(eff);
    expect(pay.wechat?.notify_url).toBe(`${eff}${WECHAT_PATH}`);
    expect(pay.alipay?.notify_url).toBe(`${eff}${ALIPAY_PATH}`);
    expect(pay.alipay?.return_url).toBe(`${eff}${ALIPAY_RETURN_PATH}`);

    // 提交伪造回调地址：保存应成功（其它字段照常落库），但三个地址一律被派生值覆盖
    const evil = 'https://evil.example.com/callback';
    const forged = (await (
      await adminApi.put('/api/v1/admin/settings/payment', {
        data: {
          ...pay,
          wechat: { ...pay.wechat, notify_url: evil },
          alipay: { ...pay.alipay, notify_url: evil, return_url: evil },
          updated_at: pay.updated_at,
        },
      })
    ).json()) as ApiResponse;
    expect(forged.code, `保存支付配置失败: ${forged.message}`).toBe(0);

    const after = (await (
      await adminApi.get('/api/v1/admin/settings/payment')
    ).json()) as ApiResponse<PaymentSetting>;
    const saved = assertOk(after, '回读支付配置');
    expect(saved.wechat?.notify_url, '微信通知地址不得被前端改写').toBe(`${eff}${WECHAT_PATH}`);
    expect(saved.alipay?.notify_url, '支付宝通知地址不得被前端改写').toBe(`${eff}${ALIPAY_PATH}`);
    expect(saved.alipay?.return_url, '支付宝回跳地址不得被前端改写').toBe(`${eff}${ALIPAY_RETURN_PATH}`);
  });

  test('9. 非超管即便有 settings:update 权限也不得写入站点设置', async ({ playwright }) => {
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
      expect(read.code, '有 settings:view 即可读品牌配置').toBe(0);

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
