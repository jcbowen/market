package model

import "testing"

/**
 * users.phone 可空口径测试（P2 C3）
 *
 * 背景：phone 列带唯一索引，模型却用 `string` 表示「未绑定」——MySQL 唯一索引
 * 允许多行 NULL，但空串是一个具体值，第二个无手机号会员插入即撞 1062，
 * 纯支付宝/微信授权（拿不到手机号）的会员注册走不通。
 * 修法：字段改 `*string`，未绑定写 NULL；读写只经本文件两个入口，
 * 出口统一把 NULL 归一为空串，避免 proto/前端出现 nil 语义分裂。
 *
 * 运行方式（离线，无需 DB）：
 *   go test ./services/user/model/ -run TestUserPhone -v
 *
 * 注意：唯一索引「多行 NULL 不冲突」本身属库侧行为，需在真实库上跑 AutoMigrate
 * 后插两条不带 phone 的记录验证（记入 `11-开发计划.md` 上线前置项）。
 */

// TestNewPhoneStoresNullForEmpty 空串一律转 NULL（nil），非空原样保留
func TestNewPhoneStoresNullForEmpty(t *testing.T) {
	if got := NewPhone(""); got != nil {
		t.Errorf("空手机号应存 NULL，得到 %q", *got)
	}
	if got := NewPhone("   "); got == nil || *got != "   " {
		t.Error("空白串非空手机号语义由调用方 trim，本入口只按空串判定，不得改写取值")
	}
	phone := "13800138000"
	got := NewPhone(phone)
	if got == nil || *got != phone {
		t.Errorf("手机号应原样入库，得到 %v", got)
	}
	if got == &phone {
		t.Error("返回值不应与入参同一地址（调用方复用变量时会互相影响）")
	}
}

// TestPhoneValueNormalizesNull NULL 与 nil 接收者都归一为空串，读取侧不出现 panic
func TestPhoneValueNormalizesNull(t *testing.T) {
	var nilUser *User
	if got := nilUser.PhoneValue(); got != "" {
		t.Errorf("nil 接收者应返回空串，得到 %q", got)
	}
	if got := (&User{}).PhoneValue(); got != "" {
		t.Errorf("未绑定（NULL）应返回空串，得到 %q", got)
	}
	phone := "13900139000"
	if got := (&User{Phone: &phone}).PhoneValue(); got != phone {
		t.Errorf("已绑定手机号读取错误，得到 %q", got)
	}
}
