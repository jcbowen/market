import type { Component } from 'vue'
import { CashOutline, CloudDownloadOutline } from '@vicons/ionicons5'

/**
 * 应用注册表：应用中心宫格展示与入口路由的统一来源
 * 新增独立应用时在此追加一条即可，应用中心页面自动渲染
 */

/** 应用元信息 */
export interface AppMeta {
  /** 应用唯一 key（常用应用偏好存储标识） */
  key: string
  name: string
  description: string
  icon: Component
  /** 入口路由 */
  path: string
  /** 所需权限码；缺省表示登录即可 */
  permission?: string
  /** 仅超级管理员可见 */
  superOnly?: boolean
}

/** 全部注册应用（顺序即应用中心默认展示顺序） */
export const APP_REGISTRY: AppMeta[] = [
  {
    key: 'pos-cashier',
    name: '收银台',
    description: '门店收银、取货核销与订单处理',
    icon: CashOutline,
    path: '/pos/cashier',
    permission: 'order:create',
  },
  {
    key: 'data-import',
    name: '数据导入',
    description: '按需导入演示数据包：分类、商品、库存、Banner 与营销活动',
    icon: CloudDownloadOutline,
    path: '/apps/data-import',
    permission: 'product:create',
    superOnly: true,
  },
]

/**
 * 按 key 查找应用定义
 * @param key 应用唯一 key
 * @returns 应用元信息；未注册返回 undefined
 */
export function findAppMeta(key: string): AppMeta | undefined {
  return APP_REGISTRY.find((app) => app.key === key)
}
