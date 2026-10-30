// Package channel 客户端渠道标识常量与工具
// 渠道由后台「渠道管理」统一维护（client_channels 表），登录与支付链路按渠道取用凭证；
// C 端请求通过 X-Channel-Code 请求头声明所在渠道。
// 渠道启用的支付方式（绑定哪些支付配置）由渠道侧维护（client_channels.pay_methods），
// 支付配置仅作为独立的凭证载体（pay_config 多行），不再反向勾选适用渠道
package channel

import "strings"

// 内置客户端渠道 code
const (
	// H5 浏览器 H5（微信外浏览器，走微信 H5 支付；无需配置凭证）
	H5 = "h5"
	// GZH 微信公众号（网页授权登录 + 微信内 JSAPI 支付，授权链路未开放）
	GZH = "gzh"
	// MP 微信小程序（Code2Session 登录 + JSAPI 支付）
	MP = "mp"
	// ALIMP 支付宝小程序（手机号登录 + 支付宝小程序内 alipay.trade.create 支付；
	// 服务端签名凭证复用支付配置的应用私钥，渠道侧仅配置 AppID）
	ALIMP = "alimp"
	// ANDROID 安卓 App（开放平台移动应用，App 支付链路未开放，禁用态展示）
	ANDROID = "android"
	// IOS iOS App（开放平台移动应用，App 支付链路未开放，禁用态展示）
	IOS = "ios"
	// POS 门店收银台（独立后台应用，B 扫 C 付款码 + 现金收款；
	// 特殊渠道：无凭证、恒启用、不参与 C 端登录与 X-Channel-Code 上报）
	POS = "pos"
)

// codes 全部内置渠道 code
var codes = map[string]bool{
	H5:      true,
	GZH:     true,
	MP:      true,
	ALIMP:   true,
	ANDROID: true,
	IOS:     true,
	POS:     true,
}

// fixedOrder 渠道管理固定展示顺序（按常用程度）：
// h5 → 微信公众号 → 微信小程序 → 支付宝小程序 → 安卓 App → iOS App → 门店收银台。
// 渠道排序功能已移除，列表一律按此顺序返回
var fixedOrder = []string{H5, GZH, MP, ALIMP, ANDROID, IOS, POS}

// disabledCodes 暂不支持的渠道（前端禁用态展示，不可配置凭证、不可启用）
var disabledCodes = map[string]bool{
	ANDROID: true,
	IOS:     true,
}

// IsValid 判断渠道 code 是否为内置渠道
//
// 参数：
//   - code: 渠道标识
//
// 返回：
//   - bool: 是否内置渠道
func IsValid(code string) bool {
	return codes[code]
}

// IsDisabled 渠道是否处于「暂不支持」禁用态（仅 android/ios；恒为禁用展示，与启用开关无关）
//
// 参数：
//   - code: 渠道标识
//
// 返回：
//   - bool: 是否禁用态渠道
func IsDisabled(code string) bool {
	return disabledCodes[code]
}

// Normalize 规整渠道 code（去空白、转小写）
//
// 参数：
//   - code: 原始渠道标识
//
// 返回：
//   - string: 规整后的渠道标识
func Normalize(code string) string {
	return strings.ToLower(strings.TrimSpace(code))
}

// All 返回全部内置渠道 code（按固定展示顺序）
//
// 返回：
//   - []string: 渠道 code 列表
func All() []string {
	out := make([]string, 0, len(fixedOrder))
	return append(out, fixedOrder...)
}

// OrderIndex 返回渠道在固定顺序中的位次（未知渠道排在最后）
//
// 参数：
//   - code: 渠道标识
//
// 返回：
//   - int: 顺序位次
func OrderIndex(code string) int {
	for i, c := range fixedOrder {
		if c == code {
			return i
		}
	}
	return len(fixedOrder)
}
