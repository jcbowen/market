import type { APIRequest, APIRequestContext, Playwright } from '@playwright/test';

/** 请求来源：可为现成的 APIRequestContext，或 Playwright.request 工厂（自动 newContext） */
export type RequestSource = APIRequestContext | APIRequest;

/**
 * 归一化请求上下文
 * - 函数名: ensureContext
 * - 参数: src — APIRequestContext 或 Playwright.request 工厂
 * - 返回: Promise<APIRequestContext>（工厂入参时新建上下文，调用方需 dispose）
 * - 异常: 底层创建失败时抛出
 */
export async function ensureContext(src: RequestSource): Promise<APIRequestContext> {
  if ('post' in src) return src as APIRequestContext;
  return (src as APIRequest).newContext();
}

/** 统一响应包装结构（对应后端 common/response/response.go） */
export interface ApiResponse<T = unknown> {
  code: number;
  message: string;
  data?: T;
}

/** 分页响应 data 结构 */
export interface PageData<T = unknown> {
  list: T[];
  total: number;
  page: number;
  page_size: number;
}

/** C 端登录返回（auth.go loginResult，snake_case） */
export interface ClientLoginInfo {
  access_token: string;
  refresh_token: string;
  expires_in: number;
  user: {
    id: number;
    nickname: string;
    avatar: string;
    phone: string;
    community_id: number;
  };
}

/** 管理端登录返回（admin.go AdminLoginResp，snake_case） */
export interface AdminLoginInfo {
  access_token: string;
  refresh_token: string;
  expires_in: number;
  admin?: Record<string, unknown>;
  [k: string]: unknown;
}

/** 管理端默认账号（init.dev.sql 预置） */
export const ADMIN_USERNAME = process.env.TEST_ADMIN_USER ?? 'admin';
export const ADMIN_PASSWORD = process.env.TEST_ADMIN_PASS ?? 'admin123';

/**
 * C 端手机号直登（免验证码免密码）
 * - 函数名: clientLogin
 * - 参数: request — Playwright 请求上下文；phone — 合法 11 位手机号
 * - 返回: Promise<ClientLoginInfo>，含 access_token/refresh_token/user（community_id 保证 > 0）
 * - 异常: HTTP 非 200、业务码非 0、无可用社区时抛出 Error
 * - 使用示例: const info = await clientLogin(request, '13800138000');
 */
export async function clientLogin(request: RequestSource, phone: string): Promise<ClientLoginInfo> {
  const ctx = await ensureContext(request);
  try {
    const resp = await ctx.post('/api/v1/auth/login/phone', { data: { phone } });
    if (!resp.ok()) throw new Error(`C 端登录 HTTP ${resp.status()}：${await resp.text()}`);
    const body = (await resp.json()) as ApiResponse<ClientLoginInfo>;
    const info = assertOk(body, 'C 端登录');

    // 新用户未绑定社区时自动选择第一个可用社区（下单等操作的前置条件）
    if (!info.user?.community_id) {
      const comms = (await (await ctx.get('/api/v1/communities')).json()) as ApiResponse<
        PageData<{ id: number; status: number }>
      >;
      const first = asList<{ id: number; status: number }>(comms.data).find((c) => c.status === 1);
      if (!first) throw new Error('环境无可用社区，新用户无法完成社区绑定');
      const sel = (await (
        await ctx.put('/api/v1/user/community', {
          data: { community_id: first.id },
          headers: { Authorization: `Bearer ${info.access_token}` },
        })
      ).json()) as ApiResponse;
      if (sel.code !== 0) throw new Error(`自动选择社区失败 code=${sel.code} message=${sel.message}`);
      info.user.community_id = first.id;
    }
    return info;
  } finally {
    if (ctx !== request) await ctx.dispose();
  }
}

/**
 * 管理端账号密码登录
 * - 函数名: adminLogin
 * - 参数: request — Playwright 请求上下文；username/password — 管理员凭据（默认 admin/admin123）
 * - 返回: Promise<AdminLoginInfo>，含 access_token
 * - 异常: 登录失败（凭据错误/服务异常）时抛出 Error
 * - 使用示例: const info = await adminLogin(request);
 */
export async function adminLogin(
  request: RequestSource,
  username: string = ADMIN_USERNAME,
  password: string = ADMIN_PASSWORD,
): Promise<AdminLoginInfo> {
  const ctx = await ensureContext(request);
  try {
    const resp = await ctx.post('/api/v1/admin/auth/login', { data: { username, password } });
    if (!resp.ok()) throw new Error(`管理端登录 HTTP ${resp.status()}：${await resp.text()}`);
    const body = (await resp.json()) as ApiResponse<AdminLoginInfo>;
    return assertOk(body, '管理端登录');
  } finally {
    if (ctx !== request) await ctx.dispose();
  }
}

/**
 * 创建携带 Authorization 头的新请求上下文
 * - 函数名: newAuthedRequest
 * - 参数: playwright — Playwright 实例；token — 访问令牌
 * - 返回: Promise<APIRequestContext>，后续请求自动带 Bearer token；调用方负责 dispose()
 * - 异常: 底层上下文创建失败时抛出
 * - 使用示例: const api = await newAuthedRequest(playwright, token);
 */
export async function newAuthedRequest(playwright: Playwright, token: string): Promise<APIRequestContext> {
  return playwright.request.newContext({
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });
}

/** 管理端已就绪上下文：已登录、已绑定默认社区语境 */
export interface AdminContext {
  api: APIRequestContext;
  communityId: number;
  refreshToken: string;
}

/**
 * 创建管理端测试上下文（登录 → 确定社区 → 上下文自动带社区头）
 * 管理端写操作与多数列表接口要求 X-Community-Id 社区语境，统一在此注入；
 * 未指定 communityId 时取 my/communities 第一个
 * - 函数名: newAdminContext
 * - 参数: playwright — Playwright 实例；username/password — 管理员凭据（可选）；communityId — 指定社区语境（可选）
 * - 返回: Promise<AdminContext>（api 需调用方 dispose()）
 * - 异常: 登录失败或无可用社区时抛出 Error
 * - 使用示例: const { api, communityId } = await newAdminContext(playwright);
 */
export async function newAdminContext(
  playwright: Playwright,
  username: string = ADMIN_USERNAME,
  password: string = ADMIN_PASSWORD,
  communityId?: number,
): Promise<AdminContext> {
  const info = await adminLogin(playwright.request, username, password);
  const probe = await playwright.request.newContext({
    extraHTTPHeaders: { Authorization: `Bearer ${info.access_token}` },
  });
  try {
    let cid = communityId ?? 0;
    if (!cid) {
      const resp = await probe.get('/api/v1/admin/my/communities');
      const body = (await resp.json()) as ApiResponse<{ id: number }[]>;
      const list = asList<{ id: number }>(body.data);
      if (body.code !== 0 || list.length === 0) {
        throw new Error(`管理端无可选社区，无法建立测试语境 code=${body.code}`);
      }
      cid = list[0].id;
    }
    const api = await playwright.request.newContext({
      extraHTTPHeaders: { Authorization: `Bearer ${info.access_token}`, 'X-Community-Id': String(cid) },
    });
    return { api, communityId: cid, refreshToken: info.refresh_token };
  } finally {
    await probe.dispose();
  }
}

/**
 * 校验业务码并返回 data
 * - 函数名: assertOk
 * - 参数: body — 接口响应体；label — 报错时的场景描述
 * - 返回: data 字段（泛型 T）
 * - 异常: code !== 0 时抛出含 message 的 Error
 * - 使用示例: const data = assertOk<PageData>(body, '商品列表');
 */
export function assertOk<T>(body: ApiResponse<T>, label: string): T {
  if (body.code !== 0) {
    throw new Error(`${label} 业务失败 code=${body.code} message=${body.message}`);
  }
  return body.data as T;
}

/**
 * 判断业务码是否等于期望值（用于负向用例断言，不抛异常）
 * - 函数名: expectCode
 * - 参数: body — 接口响应体；code — 期望业务码
 * - 返回: boolean
 * - 异常: 无
 * - 使用示例: expect(expectCode(body, 401)).toBeTruthy();
 */
export function expectCode(body: ApiResponse, code: number): boolean {
  return body.code === code;
}

/**
 * 兼容两种列表响应结构（data 为数组 / data 为 PageData 对象）
 * - 函数名: asList
 * - 参数: data — 接口 data 字段原始值
 * - 返回: T[]（两结构皆空时返回空数组）
 * - 异常: 无
 * - 使用示例: const list = asList<Role>(body.data);
 */
export function asList<T>(data: unknown): T[] {
  if (Array.isArray(data)) return data as T[];
  const page = data as PageData<T> | undefined | null;
  return page?.list ?? [];
}

/** 可下单 SKU 引用 */
export interface SkuRef {
  id: number;
  product_id: number;
  stock: number;
}

/**
 * 从环境选取一个有库存的在售 SKU
 * 流程：公开商品列表取第一个在售商品 → GET /api/v1/skus?product_id= 取库存>0 的 SKU
 * - 函数名: pickSku
 * - 参数: api — 请求上下文（鉴权与否均可，走公开接口）；keywords — 可选名称过滤
 * - 返回: Promise<SkuRef>
 * - 异常: 环境无在售商品/无库存 SKU 时抛出 Error
 * - 使用示例: const sku = await pickSku(api);
 */
export async function pickSku(api: APIRequestContext, keywords?: string): Promise<SkuRef> {
  const params: Record<string, string | number> = { page: 1, page_size: 20, status: 1 };
  if (keywords) params.keywords = keywords;
  const listResp = await api.get('/api/v1/products', { params });
  const listBody = (await listResp.json()) as ApiResponse<PageData<{ id: number; name: string }>>;
  const products = asList<{ id: number }>(listBody.data);
  if (listBody.code !== 0 || products.length === 0) throw new Error('环境无在售商品');

  // 优先取新造的商品（id 更大），保证数据干净
  for (const p of [...products].sort((a, b) => b.id - a.id)) {
    const skuResp = await api.get('/api/v1/skus', { params: { product_id: p.id } });
    const skuBody = (await skuResp.json()) as ApiResponse<{ id: number; product_id: number; stock: number; status: number }[]>;
    const sku = (skuBody.data ?? []).find((s) => s.status === 1 && s.stock > 0);
    if (sku) return { id: sku.id, product_id: sku.product_id, stock: sku.stock };
  }
  throw new Error('环境商品均无有库存的 SKU');
}
