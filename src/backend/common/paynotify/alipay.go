// Package paynotify 支付渠道通知报文的落库/落调试库口径
// 支付回调原文是排障的唯一凭据（验签失败必须能复盘「渠道发了什么、我们为什么拒」），
// 但其中夹带的买家身份字段与资金核对无关。网关（debug_logs 取证字段）与支付服务
// （payment_notify_logs.raw_body）是同一份通知的两处落点，脱敏规则必须一处定义，
// 否则两处口径漂移会让「用网关原文复算支付服务流水」这种对照排查失真。
package paynotify

import "github.com/jcbowen/jcbaseGo/component/helper"

// alipayBuyerKeys 支付宝通知中的买家身份字段（脱敏目标）
//   - buyer_logon_id：买家支付宝登录账号（手机/邮箱），隐私属性最强；
//   - buyer_user_id：买家在支付宝侧的用户 ID（2088 开头）；
//   - buyer_open_id：买家在本应用下的支付宝用户标识（应用内伪匿名，仍属个人标识）。
//
// 三者都只回答「谁付的」，不参与验签（sign 不覆盖它们的校验逻辑）、金额核对与幂等判定，
// 脱敏后不影响任何排障分支。
var alipayBuyerKeys = []string{"buyer_logon_id", "buyer_user_id", "buyer_open_id"}

// MaskAlipayParams 返回支付宝通知参数的脱敏副本（不改动入参）
//
// 只掩码买家身份字段，sign/sign_type 保持原文：验签类故障必须拿到签名原文才能离线复算
// 待验签串，掩码 sign 会让这类故障永久无法定位；签名把 out_trade_no/total_amount 一并签死，
// 脱离原文既无法伪造也无法重放（重放真实通知只会命中同一笔已支付单的幂等分支），
// 且原文仅在失败场景落库、受 payment:raw:view 权限收口、按保留天数清理，隐私面可控。
//
// 参数：
//   - params: 原始通知参数（form 字段映射）
//
// 返回值：
//   - map[string]string: 脱敏副本；入参为 nil 时返回空映射
func MaskAlipayParams(params map[string]string) map[string]string {
	masked := make(map[string]string, len(params))
	for key, value := range params {
		masked[key] = value
	}
	for _, key := range alipayBuyerKeys {
		if value := masked[key]; value != "" {
			masked[key] = helper.MaskString(value, 3, 4, '*')
		}
	}
	return masked
}
