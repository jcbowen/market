// Package channel 客户端渠道标识常量与工具
// 渠道由后台「渠道管理」统一维护（client_channels 表），登录与支付链路按渠道取用凭证；
// C 端请求通过 X-Channel-Code 请求头声明所在渠道
package channel

import "strings"

// 内置客户端渠道 code
const (
	// MP 微信小程序（Code2Session 登录 + JSAPI 支付）
	MP = "mp"
	// GZH 微信公众号（网页授权登录 + 微信内 JSAPI 支付，授权链路未开放）
	GZH = "gzh"
	// APP 开放平台移动应用（App 支付，链路未开放）
	APP = "app"
	// H5 浏览器 H5（微信外浏览器，走微信 H5 支付；无需配置凭证）
	H5 = "h5"
)

// codes 全部内置渠道 code
var codes = map[string]bool{
	MP:  true,
	GZH: true,
	APP: true,
	H5:  true,
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

// All 返回全部内置渠道 code
//
// 返回：
//   - []string: 渠道 code 列表
func All() []string {
	return []string{MP, GZH, APP, H5}
}
