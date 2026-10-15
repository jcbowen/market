import { get, post, upload } from './request'
import type { Order } from './types'

/**
 * 履约人员身份查询结果
 */
export interface FulfillmentStatus {
  is_fulfiller: boolean
  community_id: number
}

/**
 * 履约工作台订单（在 C 端 Order 基础上扩展履约字段）
 * 后端 OrderInfo 含 contact_name/phone/address、pickup_code 等
 */
export interface FulfillmentOrder extends Order {
  contact_name?: string
  contact_phone?: string
  contact_address?: string
  /** 指派的履约人员（0=未指派）与姓名快照 */
  delivery_user_id?: number
  delivery_user_name?: string
  /** 送达签收凭证照片 URL（决策点②） */
  delivery_proof_images?: string[]
  /** 自提核销码（核销输入比对用，不在列表直接展示防截图冒领） */
  pickup_code?: string
}

/**
 * 查询当前用户履约人员身份（我的页工作台入口判断）
 */
export function getFulfillmentStatus() {
  return get<FulfillmentStatus>('/user/fulfillment-status')
}

/**
 * 工作台查单
 * @param params order_type: 1=自提核销 2=送货上门；status_group: 0=进行中 5=已完成/已核销
 */
export function getFulfillmentOrders(params: {
  order_type: number
  status_group: number
  page?: number
  page_size?: number
}) {
  return get<{ list: FulfillmentOrder[]; total: number; page: number; page_size: number }>(
    '/fulfillment/orders',
    params,
  )
}

/**
 * 开始配送（status 2/3 → 4）
 */
export function startDelivery(id: number) {
  return post(`/fulfillment/orders/${id}/start-delivery`)
}

/**
 * 送达完成（status 4 → 5，强制签收照片，决策点②）
 * @param proofImages 签收照片 URL（先经 uploadImage 上传）
 */
export function completeDelivery(id: number, proofImages: string[]) {
  return post(`/fulfillment/orders/${id}/complete-delivery`, { proof_images: proofImages })
}

/**
 * 核销自提单（履约人员输顾客出示的取货码）
 */
export function verifyPickupByStaff(id: number, pickupCode: string) {
  return post(`/fulfillment/orders/${id}/verify-pickup`, { pickup_code: pickupCode })
}

/**
 * 上传图片（C 端上传通道，签收凭证等用途，不进入后台附件选择器）
 * @param filePath 本地文件路径（uni.chooseImage 返回）
 * @returns 图片访问 URL
 */
export function uploadImage(filePath: string) {
  return upload<{ attach_id: number; url: string }>('/upload/image', filePath)
}
