/**
 * 权限点清单（共享常量）
 * 角色管理、社区权限分配等页面共用，新增权限点时在此统一维护
 */

export interface PermissionOption {
  label: string
  value: string
}

export interface PermissionGroup {
  label: string
  options: PermissionOption[]
}

/** 按模块分组的全量权限点 */
export const permissionGroups: PermissionGroup[] = [
  {
    label: '工作台',
    options: [{ label: '工作台', value: 'dashboard:view' }],
  },
  {
    label: '商品',
    options: [
      { label: '商品查看', value: 'product:view' },
      { label: '商品创建', value: 'product:create' },
      { label: '商品更新', value: 'product:update' },
      { label: '商品删除', value: 'product:delete' },
    ],
  },
  {
    label: '库存',
    options: [
      { label: '库存查看', value: 'inventory:view' },
      { label: '库存创建', value: 'inventory:create' },
      { label: '库存更新', value: 'inventory:update' },
    ],
  },
  {
    label: '订单与售后',
    options: [
      { label: '订单查看', value: 'order:view' },
      { label: '订单更新', value: 'order:update' },
      { label: '收银台下单', value: 'order:create' },
      { label: '退款查看', value: 'refund:view' },
      { label: '退款更新', value: 'refund:update' },
    ],
  },
  {
    // 支付报文原文含顾客标识与签名信息，社区管理员权限上限不含此项，实际仅超级管理员可勾选生效
    label: '支付（系统级）',
    options: [{ label: '支付报文查看', value: 'payment:raw:view' }],
  },
  {
    label: '会员',
    options: [
      { label: '会员查看', value: 'user:view' },
      { label: '会员创建', value: 'user:create' },
      { label: '会员更新', value: 'user:update' },
    ],
  },
  {
    label: '营销',
    options: [
      { label: '营销查看', value: 'marketing:view' },
      { label: '营销创建', value: 'marketing:create' },
      { label: '营销更新', value: 'marketing:update' },
      { label: '营销删除', value: 'marketing:delete' },
    ],
  },
  {
    label: '统计',
    options: [{ label: '统计查看', value: 'statistics:view' }],
  },
  {
    label: '门店（社区级）',
    options: [{ label: '门店设置', value: 'community:config' }],
  },
  {
    label: '社区（系统级）',
    options: [
      { label: '社区查看', value: 'community:view' },
      { label: '社区创建', value: 'community:create' },
      { label: '社区更新', value: 'community:update' },
      { label: '社区删除', value: 'community:delete' },
    ],
  },
  {
    label: '行政区划（系统级）',
    options: [
      { label: '区划查看', value: 'region:view' },
      { label: '区划更新', value: 'region:update' },
    ],
  },
  {
    label: '系统设置（系统级）',
    options: [
      { label: '设置查看', value: 'settings:view' },
      { label: '设置更新', value: 'settings:update' },
    ],
  },
  {
    label: '员工',
    options: [
      { label: '员工查看', value: 'admin:view' },
      { label: '员工创建', value: 'admin:create' },
      { label: '员工更新', value: 'admin:update' },
      { label: '员工删除', value: 'admin:delete' },
    ],
  },
  {
    label: '角色',
    options: [
      { label: '角色查看', value: 'role:view' },
      { label: '角色创建', value: 'role:create' },
      { label: '角色更新', value: 'role:update' },
      { label: '角色删除', value: 'role:delete' },
    ],
  },
]

/** 全量权限点平铺清单 */
export const allPermissionOptions: PermissionOption[] = permissionGroups.flatMap((g) => g.options)
