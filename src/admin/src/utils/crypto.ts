import JSEncrypt from 'jsencrypt'
import { request, CODE_PWD_ENCRYPTED_INVALID, type BizError } from '@/api/request'
import type { ApiResult } from '@/api/types'

/**
 * 密码传输加密工具（RSA + PKCS#1 v1.5，与后端 handler/password_crypto.go 协议对应）
 *
 * 背景：登录/改密类接口密码字段以密文上送，链路抓包不再可见密码明文。
 * 约定：密码字段承载 base64 密文，并附带 pwd_alg / pwd_key 声明字段；
 * 后端按 pwd_key 匹配私钥解密，未声明时按明文兼容。
 *
 * 密钥管理：公钥经 GET /api/v1/admin/auth/pubkey 实时拉取、内存缓存（不持久化），
 * 服务端轮换密钥后刷新页面即无感切换；提交返回 11010 时由 requestWithEncryptedPasswords
 * 自动重拉公钥重试一次，覆盖「拉公钥后服务端恰好轮换」的窗口。
 */

/** 公钥下发接口出参 */
interface PubkeyInfo {
  key_id: string
  public_key: string
  alg: string
}

/** 内存公钥缓存（不持久化：刷新页面重建，天然跟随服务端轮换） */
let cachedPubkey: PubkeyInfo | null = null

/**
 * 拉取密码加密公钥（带内存缓存）
 * 拉取失败时返回 null 并降级明文（后端明文兼容通道兜底）——加密是纵深防御手段，
 * 不应因其故障阻塞登录主流程；控制台告警保留现场便于排查
 * @param force true = 忽略缓存强制重拉（密钥轮换重试场景）
 * @returns 公钥信息；接口失败时 null
 */
async function fetchPubkey(force = false): Promise<PubkeyInfo | null> {
  if (!force && cachedPubkey) return cachedPubkey
  try {
    const res = await request<ApiResult<PubkeyInfo>>({
      url: '/api/v1/admin/auth/pubkey',
      method: 'GET',
    })
    const data = res.data?.data
    if (!data?.public_key || !data?.key_id) return null
    cachedPubkey = data
    return data
  } catch {
    console.warn('[crypto] 密码加密公钥获取失败，本次密码将明文提交（后端兼容通道）')
    return null
  }
}

/**
 * 使公钥缓存失效（密钥轮换重试前调用，强制下次重拉）
 */
function invalidatePubkey() {
  cachedPubkey = null
}

/**
 * 密码加密器：一次请求内加密一个或多个密码字段（如改密的新旧密码），共享同一批声明字段
 */
export interface PasswordEncryptor {
  /**
   * 加密密码字段组
   * @param fields 明文字段 map，如 { password: 'x' } 或 { old_password: 'a', new_password: 'b' }
   * @returns 可直接并入请求体的字段 map：密文字段 + pwd_alg/pwd_key；空值字段原样保留（后端跳过）；
   *          公钥不可得或单字段加密失败时该字段降级明文（后端明文兼容通道）
   */
  encryptFields(fields: Record<string, string>): Promise<Record<string, unknown>>
}

/**
 * 创建密码加密器（拉取公钥）
 */
async function createEncryptor(): Promise<PasswordEncryptor> {
  const pubkey = await fetchPubkey()
  return {
    async encryptFields(fields) {
      const out: Record<string, unknown> = { ...fields }
      // 公钥不可得：整组降级明文（不带声明字段，后端按明文兼容处理）
      if (!pubkey) return out

      const cipher = new JSEncrypt()
      cipher.setPublicKey(pubkey.public_key)
      for (const [name, plain] of Object.entries(fields)) {
        // 空值原样保留（如创建员工不设初始密码），不发声明字段由后端跳过解密
        if (!plain) continue
        // RSA-PKCS1-v1.5 单块容量 245 字节（2048 位），密码长度远小于此；false = 公钥非法等
        const encrypted = cipher.encrypt(plain)
        if (encrypted) {
          out[name] = encrypted
        } else {
          console.warn(`[crypto] 字段 ${name} RSA 加密失败，已降级明文提交`)
        }
      }
      // 仅当至少一个字段真正加密成功才携带声明（全降级时保持明文语义）
      if (Object.keys(fields).some((name) => out[name] !== fields[name])) {
        out.pwd_alg = pubkey.alg
        out.pwd_key = pubkey.key_id
      }
      return out
    },
  }
}

/**
 * 判断错误是否为密码密文失效（11010：服务端密钥已轮换或密文无效）
 */
function isPwdKeyRotatedError(error: unknown): boolean {
  return (error as BizError)?.code === CODE_PWD_ENCRYPTED_INVALID
}

/**
 * 加密并提交密码类请求的统一入口
 * 首次提交返回 11010（服务端密钥已轮换）时：作废公钥缓存、重拉新公钥、
 * 用新公钥重新加密并重试一次；仍失败则抛出（拦截器恢复全局错误提示）
 *
 * @param buildData 请求体构造器：入参为加密器，保证重试时以新公钥重新加密而非重放旧密文
 * @param doRequest 发起请求：入参为 buildData 产出的请求体
 * @returns doRequest 的返回值原样透传
 *
 * @example
 *   return requestWithEncryptedPasswords(
 *     async (enc) => ({ username: params.username, ...(await enc.encryptFields({ password: params.password })) }),
 *     (data) => request({ url: '/api/v1/admin/auth/login', method: 'POST', data }),
 *   )
 */
export async function requestWithEncryptedPasswords<R>(
  buildData: (enc: PasswordEncryptor) => Promise<Record<string, unknown>>,
  doRequest: (data: Record<string, unknown>) => Promise<R>,
): Promise<R> {
  try {
    return await doRequest(await buildData(await createEncryptor()))
  } catch (error) {
    if (!isPwdKeyRotatedError(error)) throw error
    invalidatePubkey()
    return doRequest(await buildData(await createEncryptor()))
  }
}
