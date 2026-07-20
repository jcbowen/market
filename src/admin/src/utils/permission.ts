/**
 * 判断权限列表中是否包含指定权限码
 * @param permissions 当前管理员权限码数组
 * @param code 目标权限码
 * @returns 是否拥有权限
 */
export function hasPermission(permissions: string[], code: string): boolean {
  return permissions.includes(code)
}
