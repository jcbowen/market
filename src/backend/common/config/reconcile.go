package config

// 支付对账开关的取值口径。
// 三个布尔项在配置结构里均为 *bool：框架的 CheckAndSetDefault 无法区分
// 「未配置」（零值 false）与「显式 false」，用 bool + default:"true" 会让显式关闭失效，
// 故用指针表达三态，由本文件的访问器统一解释默认值。

// reconcileBool 解析 *bool 三态：nil 回落到给定默认值
// 参数：
//   - v: 配置指针，nil 表示未配置
//   - def: 未配置时采用的默认值
//
// 返回值：
//   - bool: 生效值
func reconcileBool(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

// ReconcileEnabled 渠道主动回查与关闭渠道交易总开关
// 关闭时行为退回本轮改动前：仅依赖渠道回调确认，关单只改本地状态
//
// 返回值：
//   - bool: 未配置视为 true
func (c *Config) ReconcileEnabled() bool {
	if c == nil {
		return true
	}
	return reconcileBool(c.Business.PaymentReconcile.Enabled, true)
}

// CancelGuardEnabled 取消前置渠道确认开关（防「已收款订单被取消」形成资金挂账）
// 仅在总开关开启时生效
//
// 返回值：
//   - bool: 未配置视为 true
func (c *Config) CancelGuardEnabled() bool {
	if c == nil {
		return true
	}
	if !c.ReconcileEnabled() {
		return false
	}
	return reconcileBool(c.Business.PaymentReconcile.CancelGuard, true)
}

// AutoRefundStuckEnabled 挂账单自动退款开关
// 默认关闭：渠道退款回执需真实商户号联调验证后才能开启
//
// 返回值：
//   - bool: 未配置视为 false
func (c *Config) AutoRefundStuckEnabled() bool {
	if c == nil {
		return false
	}
	if !c.ReconcileEnabled() {
		return false
	}
	return reconcileBool(c.Business.PaymentReconcile.AutoRefundStuck, false)
}

// ReconcileInt 读取对账数值配置，非正值回落默认（框架会填充 default 标签，此处仅防御零值配置）
// 参数：
//   - value: 配置值
//   - fallback: 非正时使用的默认值
//
// 返回值：
//   - int: 生效值
func ReconcileInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}
