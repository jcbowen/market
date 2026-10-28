/**
 * 安全策略常量
 *
 * 与后端 `src/backend/common/pwdpolicy/pwdpolicy.go` 同口径：后端是权威，
 * 这里的数字只是把拦截前置到表单，改一处必须改两处（否则前端放行、后端报参数错误）。
 */

/** 后台账号口令最小长度（字节） */
export const MIN_PASSWORD_LENGTH = 10

/** 口令长度提示文案（表单校验与输入框占位共用，避免同一句话写四遍） */
export const PASSWORD_LENGTH_TIP = `密码长度至少 ${MIN_PASSWORD_LENGTH} 位`

/**
 * 判断口令是否短于下限
 * @param value 口令明文（表单里的原始输入）
 */
export function isPasswordTooShort(value: string): boolean {
  return (value || '').length < MIN_PASSWORD_LENGTH
}
