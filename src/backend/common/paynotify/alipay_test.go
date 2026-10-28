package paynotify

import "testing"

// TestMaskAlipayParams 支付宝通知参数脱敏口径
// 锁定：买家身份字段掩码、验签与核对字段原样、入参不被改写
func TestMaskAlipayParams(t *testing.T) {
	params := map[string]string{
		"out_trade_no":   "ZF1",
		"total_amount":   "12.30",
		"sign":           "abc123signature",
		"sign_type":      "RSA2",
		"buyer_logon_id": "13800001111",
		"buyer_user_id":  "2088000000000001",
		"buyer_open_id":  "064abcxyz-open",
	}
	got := MaskAlipayParams(params)

	if got["buyer_logon_id"] != "138****1111" {
		t.Fatalf("buyer_logon_id = %q, want %q", got["buyer_logon_id"], "138****1111")
	}
	if got["buyer_user_id"] == params["buyer_user_id"] || got["buyer_user_id"] == "" {
		t.Fatalf("buyer_user_id 未脱敏: %q", got["buyer_user_id"])
	}
	if got["buyer_open_id"] == params["buyer_open_id"] || got["buyer_open_id"] == "" {
		t.Fatalf("buyer_open_id 未脱敏: %q", got["buyer_open_id"])
	}
	// 验签与资金核对字段必须原样：脱敏它们会让验签类故障无法复盘
	for _, key := range []string{"out_trade_no", "total_amount", "sign", "sign_type"} {
		if got[key] != params[key] {
			t.Fatalf("%s = %q, want %q", key, got[key], params[key])
		}
	}
	// 入参不可被改写：调用方可能复用同一份参数做验签
	if params["buyer_logon_id"] != "13800001111" {
		t.Fatalf("入参被改写: %q", params["buyer_logon_id"])
	}
}

// TestMaskAlipayParamsEmpty 空参数与缺字段不 panic、不凭空造键
func TestMaskAlipayParamsEmpty(t *testing.T) {
	if got := MaskAlipayParams(nil); len(got) != 0 {
		t.Fatalf("nil 入参应返回空映射，实际 %v", got)
	}
	got := MaskAlipayParams(map[string]string{"out_trade_no": "ZF1"})
	if len(got) != 1 || got["out_trade_no"] != "ZF1" {
		t.Fatalf("缺买家字段时不应增删键，实际 %v", got)
	}
}
