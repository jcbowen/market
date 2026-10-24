import { createPublicKey, publicEncrypt, constants } from 'node:crypto';

import type { APIRequest, APIRequestContext, Playwright } from '@playwright/test';

import { PRESET_PHONE, recordTestUser, uniquePhone } from './data';

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
 * 本进程已登记过的会员 ID
 * newClientUser 的标准做法是「同号登两次」（首登绑社区后重登刷新 JWT 社区语境），
 * 无此去重集合会让登记表每轮翻倍；跨进程去重由 readTestUserRegistry 按 ID 兜底
 */
const registeredUsers = new Set<number>();

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
      // 绑定发生在登录之后，JWT 中的 community_id 仍为旧值（0），
      // 按 JWT 社区语境过滤的接口（如 banner 列表）会查不到社区数据，需重登刷新 token
      const relogin = (await (
        await ctx.post('/api/v1/auth/login/phone', { data: { phone } })
      ).json()) as ApiResponse<ClientLoginInfo>;
      if (relogin.code === 0 && relogin.data?.access_token) {
        relogin.data.user.community_id = first.id;
        registerLoginUser(relogin.data, phone);
        return relogin.data;
      }
    }
    registerLoginUser(info, phone);
    return info;
  } finally {
    if (ctx !== request) await ctx.dispose();
  }
}

/**
 * 把登录到的会员登记进本轮回收清单（B5）
 * 预置账号（PRESET_PHONE）与固定人工账号不登记：它们跨轮共享，停用会破坏别的用例
 * - 函数名: registerLoginUser
 * - 参数: info — 登录返回；phone — 登录手机号
 * - 返回: void
 * - 异常: 无（登记失败由 recordTestUser 抛错，正常写文件不会失败）
 * - 使用示例: registerLoginUser(info, '13900000001');
 */
function registerLoginUser(info: ClientLoginInfo, phone: string): void {
  if (phone === PRESET_PHONE || !info.user?.id) return;
  const id = info.user.id;
  if (registeredUsers.has(id)) return;
  registeredUsers.add(id);
  recordTestUser({
    user_id: id,
    phone,
    community_id: info.user.community_id ?? 0,
  });
}

/** 密码加密公钥缓存（进程级；服务端密钥轮换后重启测试进程即重建） */
let cachedPubkey: { keyId: string; publicKey: string; alg: string } | null = null;

/**
 * 拉取密码传输加密公钥（带缓存；失败返回 null，调用方降级明文走后端兼容通道）
 * - 函数名: fetchPwdPubkey
 * - 参数: request — Playwright 请求上下文
 * - 返回: Promise<{ keyId, publicKey, alg } | null>
 * - 异常: 不抛出（网络失败按降级处理）
 */
async function fetchPwdPubkey(request: RequestSource): Promise<{ keyId: string; publicKey: string; alg: string } | null> {
  if (cachedPubkey) return cachedPubkey;
  const ctx = await ensureContext(request);
  try {
    const resp = await ctx.get('/api/v1/admin/auth/pubkey');
    const body = (await resp.json()) as ApiResponse<{ key_id: string; public_key: string; alg: string }>;
    const data = body?.data;
    if (body.code !== 0 || !data?.public_key || !data?.key_id) return null;
    cachedPubkey = { keyId: data.key_id, publicKey: data.public_key, alg: data.alg };
    return cachedPubkey;
  } catch {
    return null;
  } finally {
    if (ctx !== request) await ctx.dispose();
  }
}

/**
 * 构造密码类请求体：密码字段 RSA(OAEP-SHA256) 加密上送，与网关 handler/password_crypto.go 协议一致
 * 公钥不可得时原样返回明文字段（后端明文兼容通道兜底，测试仍可运行）
 * - 函数名: encryptedCredentials
 * - 参数: request — Playwright 请求上下文；fields — 明文字段 map（如 { password } 或 { old_password, new_password }）
 * - 返回: Promise<Record<string, unknown>>：密文字段 + pwd_alg/pwd_key 声明（或降级明文）
 * - 异常: 不抛出
 * - 使用示例: const data = await encryptedCredentials(request, { password: 'admin123' });
 */
export async function encryptedCredentials(
  request: RequestSource,
  fields: Record<string, string>,
): Promise<Record<string, unknown>> {
  const out: Record<string, unknown> = { ...fields };
  const pubkey = await fetchPwdPubkey(request);
  if (!pubkey) return out;
  let encryptedAny = false;
  for (const [name, plain] of Object.entries(fields)) {
    if (!plain) continue;
    // oaepHash 必须显式指定 SHA-256：Node 默认 OAEP 哈希为 SHA-1，与后端 DecryptOAEP(sha256) 不一致会解密失败
    out[name] = publicEncrypt(
      { key: createPublicKey(pubkey.publicKey), padding: constants.RSA_PKCS1_OAEP_PADDING, oaepHash: 'sha256' },
      Buffer.from(plain, 'utf8'),
    ).toString('base64');
    encryptedAny = true;
  }
  if (encryptedAny) {
    out.pwd_alg = pubkey.alg;
    out.pwd_key = pubkey.keyId;
  }
  return out;
}

/**
 * 管理端账号密码登录
 * 密码经 RSA 公钥加密后传输（公钥不可得时降级明文，后端兼容通道兜底），
 * 全量回归的每轮登录都走加密链路，保证密文协议持续被验证
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
    // 只有密码字段走加密：username 是查询凭据不是机密，误加密会导致后端按密文查账号必然登录失败
    const data = {
      username,
      ...(await encryptedCredentials(request, { password })),
    };
    const resp = await ctx.post('/api/v1/admin/auth/login', { data });
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
 * 未指定 communityId 时取 my/communities 中第一个非 AUTO-TEST 社区
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
      const body = (await resp.json()) as ApiResponse<{ id: number; name?: string }[]>;
      const list = asList<{ id: number; name?: string }>(body.data);
      if (body.code !== 0 || list.length === 0) {
        throw new Error(`管理端无可选社区，无法建立测试语境 code=${body.code}`);
      }
      // my/communities 按创建倒序返回，52/55 等模块往轮创建的 AUTO-TEST 空壳社区会占据列表头部
      // （无在售商品），导致默认语境跨轮复跑时绑错社区（27/38 曾因此报「环境无在售商品」）。
      // 默认语境跳过 AUTO-TEST 前缀社区，等效于干净环境下的「第一个种子社区」；
      // 全部社区均为 AUTO-TEST 时兜底取列表首项。
      const stable = list.find((c) => !String(c.name ?? '').startsWith('AUTO-TEST'));
      cid = (stable ?? list[0]).id;
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
 * - 参数: api — 请求上下文（鉴权与否均可，走公开接口）；keyword — 可选名称过滤（网关参数名为 keyword）；
 *         communityId — 可选社区收敛（公开列表仅认 community_id query，不传则跨社区取，后续按社区操作会报"不属于当前社区"）
 * - 返回: Promise<SkuRef>
 * - 异常: 环境无在售商品/无库存 SKU 时抛出 Error
 * - 使用示例: const sku = await pickSku(api, undefined, communityId);
 */
export async function pickSku(api: APIRequestContext, keyword?: string, communityId?: number): Promise<SkuRef> {
  const params: Record<string, string | number> = { page: 1, page_size: 20, status: 1 };
  if (communityId) params.community_id = communityId;
  if (keyword) params.keyword = keyword;
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

/**
 * 确保 SKU 库存充足（管理端直接入库补足）
 * 多轮测试会持续扣减环境库存，下单类用例前置调用避免"库存不足"误报
 * 进销存重构后走入库单接口（创建即生效，生成批次并回写移动加权成本）
 * - 函数名: ensureStock
 * - 参数: api — 管理端已认证请求上下文；skuId — SKU ID；qty — 入库数量
 * - 返回: Promise<void>
 * - 异常: 入库接口业务失败时抛 Error
 * - 使用示例: await ensureStock(adminApi, sku.id, 100);
 */
export async function ensureStock(api: APIRequestContext, skuId: number, qty: number): Promise<void> {
  const resp = await api.post('/api/v1/admin/inventory/in-orders', {
    data: { remark: 'AUTO-TEST 补库存', items: [{ sku_id: skuId, quantity: qty }] },
  });
  const body = (await resp.json()) as ApiResponse;
  if (body.code !== 0) throw new Error(`补库存失败 code=${body.code} message=${body.message}`);
}

/**
 * 造「本社区履约人员」：建员工并绑定 C 端会员
 * 口径（is_fulfiller 标记已废弃，user_communities.is_fulfiller 在 user-service 启动时 DropColumn，
 * 后台也没有 PUT /admin/users/:id/fulfiller 路由）：
 *   履约人员 = admin_users.user_id 绑定会员 + admin_communities 存在该社区任职行 + 员工 status=1，
 *   判定 SQL 见 services/user/service/user.go 的 CheckFulfiller，因此唯一的造数方式是建绑会员的员工。
 * 角色取本社区内置 community_admin（只为满足「有任职角色」的常规口径，履约判定本身不看角色）。
 * - 函数名: createFulfillerStaff
 * - 参数: api — 管理端已认证上下文（X-Community-Id 需与会员同社区）；communityId — 履约社区 ID；memberUserId — 绑定的会员 ID
 * - 返回: Promise<number> 员工账号 ID，供 removeFulfillerStaff 回收
 * - 异常: 本社区无内置角色或创建员工业务失败时抛 Error。这里坚持抛错而不是 test.skip：
 *         造数失败若跳过后续断言，履约链路坏了也会显示绿（假绿）
 * - 使用示例: staffId = await createFulfillerStaff(adminApi, communityId, fulfiller.user.id);
 */
export async function createFulfillerStaff(
  api: APIRequestContext,
  communityId: number,
  memberUserId: number,
): Promise<number> {
  const rolesResp = await api.get('/api/v1/admin/roles', { params: { community_id: communityId } });
  const rolesBody = (await rolesResp.json()) as ApiResponse<
    { id: number; code?: string }[] | { list: { id: number; code?: string }[] }
  >;
  const builtin = asList<{ id: number; code?: string }>(rolesBody.data).find((r) => r.code === 'community_admin');
  if (!builtin) throw new Error(`社区 ${communityId} 无内置 community_admin 角色，无法造履约人员`);

  const resp = await api.post('/api/v1/admin/admins', {
    data: {
      username: `auto_ff_${uniquePhone()}`,
      password: 'Ff123456',
      real_name: 'AUTO-TEST 履约员工',
      role_ids: [builtin.id],
      user_id: memberUserId,
    },
  });
  const body = (await resp.json()) as ApiResponse<{ id?: number }>;
  if (body.code !== 0) throw new Error(`创建履约员工失败 code=${body.code} message=${body.message}`);
  const staffId = body.data?.id ?? 0;
  if (!staffId) throw new Error('创建履约员工成功但未返回 ID');
  return staffId;
}

/**
 * 回收履约员工：先解绑会员，再尝试删除员工账号
 * 解绑是必须的一步（履约判定看绑定关系，留着会让后续轮次误判身份）；
 * 删除尽力而为——员工若已被历史订单引用，外键会拒绝删除，此时只告警不失败。
 * - 函数名: removeFulfillerStaff
 * - 参数: api — 管理端已认证上下文；staffId — createFulfillerStaff 返回的员工 ID
 * - 返回: Promise<string[]> 回收告警清单（空数组表示两步都干净）
 * - 异常: 不抛错。回收属清理动作，失败信息交给调用方 t.log/记录，避免掩盖用例本身的失败原因
 * - 使用示例: const warns = await removeFulfillerStaff(adminApi, staffId); warns.forEach((w) => t.log(w));
 */
export async function removeFulfillerStaff(api: APIRequestContext, staffId: number): Promise<string[]> {
  if (!staffId) return [];
  const warns: string[] = [];
  const unbind = (await (
    await api.put(`/api/v1/admin/admins/${staffId}`, { data: { user_id: 0 } })
  ).json()) as ApiResponse;
  if (unbind.code !== 0) warns.push(`解绑履约员工 ${staffId} 失败 code=${unbind.code} message=${unbind.message}`);

  const removed = (await (await api.delete(`/api/v1/admin/admins/${staffId}`)).json()) as ApiResponse;
  if (removed.code !== 0) {
    warns.push(`删除履约员工 ${staffId} 未成功（多为历史订单外键引用，可接受）code=${removed.code} message=${removed.message}`);
  }
  return warns;
}
