import { describe, expect, it } from 'vitest'
import { DELIVERY_TYPE, ORDER_STATUS, PAY_TYPE, canAdminRefund, deliveryTypeText, orderStatusTagType, payTypeText } from '../order'

/**
 * 订单口径常量单测：这些函数被订单查询/退款门禁/状态标签多处引用，
 * 行为回归由本文件锁定（改动前先确认下游口径）
 */

describe('canAdminRefund 后台可退款门禁', () => {
  it('已收款流转态（2 已支付/3 备货中/4 已发货）可退', () => {
    expect(canAdminRefund({ status: ORDER_STATUS.PAID })).toBe(true)
    expect(canAdminRefund({ status: ORDER_STATUS.STOCKING })).toBe(true)
    expect(canAdminRefund({ status: ORDER_STATUS.SHIPPING })).toBe(true)
  })

  it('到店单已完成（5+delivery_type=3）可退（收银台当场结清单）', () => {
    expect(canAdminRefund({ status: ORDER_STATUS.COMPLETED, delivery_type: DELIVERY_TYPE.STORE })).toBe(true)
  })

  it('已完成单后台可退（含自提/配送，售后窗口仅约束 C 端自助申请）', () => {
    expect(canAdminRefund({ status: ORDER_STATUS.COMPLETED, delivery_type: DELIVERY_TYPE.SELF_PICKUP })).toBe(true)
    expect(canAdminRefund({ status: ORDER_STATUS.COMPLETED, delivery_type: DELIVERY_TYPE.DELIVERY })).toBe(true)
  })

  it('待支付/已取消/已退款不可走代客退款', () => {
    expect(canAdminRefund({ status: ORDER_STATUS.PENDING_PAY })).toBe(false)
    expect(canAdminRefund({ status: ORDER_STATUS.CANCELLED })).toBe(false)
    expect(canAdminRefund({ status: ORDER_STATUS.REFUNDED })).toBe(false)
  })
})

describe('orderStatusTagType 状态标签颜色（订单列表醒目化）', () => {
  it('待支付=warning，已支付/备货中=info，配送中=primary', () => {
    expect(orderStatusTagType(ORDER_STATUS.PENDING_PAY)).toBe('warning')
    expect(orderStatusTagType(ORDER_STATUS.PAID)).toBe('info')
    expect(orderStatusTagType(ORDER_STATUS.STOCKING)).toBe('info')
    expect(orderStatusTagType(ORDER_STATUS.SHIPPING)).toBe('primary')
  })

  it('已完成=success，退款中/已退款=error，未知=default', () => {
    expect(orderStatusTagType(ORDER_STATUS.COMPLETED)).toBe('success')
    expect(orderStatusTagType(ORDER_STATUS.REFUNDING)).toBe('error')
    expect(orderStatusTagType(ORDER_STATUS.REFUNDED)).toBe('error')
    expect(orderStatusTagType(999)).toBe('default')
  })
})

describe('payTypeText / deliveryTypeText 文案', () => {
  it('支付方式文案：含收银台扩展渠道', () => {
    expect(payTypeText(PAY_TYPE.WECHAT)).toBe('微信')
    expect(payTypeText(PAY_TYPE.ALIPAY)).toBe('支付宝')
    expect(payTypeText(PAY_TYPE.CASH)).toBe('现金')
    expect(payTypeText(PAY_TYPE.WECHAT_CODE)).toBe('微信付款码')
    expect(payTypeText(PAY_TYPE.ALIPAY_CODE)).toBe('支付宝付款码')
  })

  it('配送方式文案', () => {
    expect(deliveryTypeText(DELIVERY_TYPE.SELF_PICKUP)).toBe('自提')
    expect(deliveryTypeText(DELIVERY_TYPE.DELIVERY)).toBe('配送')
    expect(deliveryTypeText(DELIVERY_TYPE.STORE)).toBe('到店')
  })
})
