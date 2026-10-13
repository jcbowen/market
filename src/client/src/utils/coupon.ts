/**
 * 优惠券工具
 * 折扣计算与文案格式化，供结算页与"我的优惠券"页共用
 *
 * 金额计算与后端保持逐行一致：
 * src/backend/services/order/service/order.go 的 calculateDiscount
 * （type=1 满减券减免 value，type=2 折扣券减免 totalAmount*(1-value)，均不超过商品总额）
 */

import type { UserCoupon } from '@/api/types'
import { formatPrice } from './format'

/** 券类型：满减券（value 为减免金额） */
export const COUPON_TYPE_FULL_REDUCTION = 1
/** 券类型：折扣券（value 为折扣率，如 0.85 表示 85 折） */
export const COUPON_TYPE_DISCOUNT = 2

/**
 * 计算优惠券抵扣金额（与后端 calculateDiscount 逻辑一致）
 * @param totalAmount 商品总额（门槛按商品总额校验，与后端口径一致）
 * @param coupon 用户优惠券，可为 null
 * @returns 抵扣金额，不满足门槛或无效券返回 0
 */
export function calcCouponDiscount(totalAmount: number, coupon: UserCoupon | null): number {
  if (!coupon) return 0
  if (totalAmount < coupon.threshold) return 0
  if (coupon.type === COUPON_TYPE_FULL_REDUCTION) {
    // 满减券：减免 value，不超过商品总额
    return Math.min(coupon.value, totalAmount)
  }
  if (coupon.type === COUPON_TYPE_DISCOUNT) {
    // 折扣券：value 为折扣率（0.85 = 85 折），减免总额的 (1-value)
    return Math.min(totalAmount * (1 - coupon.value), totalAmount)
  }
  return 0
}

/**
 * 生成券摘要文案：满减券"满X减Y"，折扣券"满X享N折"
 * @param coupon 用户优惠券
 * @returns 摘要文案
 */
export function couponTitle(coupon: UserCoupon): string {
  if (coupon.type === COUPON_TYPE_DISCOUNT) {
    return `满${formatPrice(coupon.threshold)}享${discountRateText(coupon.value)}折`
  }
  return `满${formatPrice(coupon.threshold)}减${formatPrice(coupon.value)}`
}

/**
 * 券面额展示文本：满减券"¥20.00"，折扣券"8.5折"
 * @param coupon 含 type 与 value 的券对象（Coupon / UserCoupon 均可）
 * @returns 面额文本，满减券已含 ¥ 前缀（来自 formatPrice），调用方不要再拼接货币符号
 */
export function couponValueText(coupon: Pick<UserCoupon, 'type' | 'value'>): string {
  if (coupon.type === COUPON_TYPE_DISCOUNT) {
    return `${discountRateText(coupon.value)}折`
  }
  return formatPrice(coupon.value)
}

/**
 * 将折扣率换算为习惯的"折"数值文案（0.85 → 8.5）
 * @param rate 折扣率
 * @returns 折数文本，去除多余的 0
 */
function discountRateText(rate: number): string {
  return String(Math.round(rate * 100) / 10).replace(/\.0$/, '')
}

/**
 * 判断优惠券是否已过期
 * @param coupon 用户优惠券
 * @returns 是否已过期（end_time 缺失或早于当前时间）
 */
export function isCouponExpired(coupon: UserCoupon): boolean {
  if (!coupon.end_time) return false
  const end = new Date(coupon.end_time.replace(' ', 'T'))
  if (Number.isNaN(end.getTime())) return false
  return Date.now() > end.getTime()
}

/**
 * 优惠券使用状态文案：区分未使用 / 已使用 / 已过期
 * @param coupon 用户优惠券
 * @returns 状态文案
 */
export function couponStatusText(coupon: UserCoupon): string {
  if (coupon.status === 1 && isCouponExpired(coupon)) return '已过期'
  if (coupon.status === 1) return '未使用'
  return '已使用'
}
