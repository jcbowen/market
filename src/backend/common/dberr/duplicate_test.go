package dberr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// TestIsDuplicateByErrorNumber 按错误号判定唯一键冲突，且不采信错误文本里的数字
// 背景：旧口径用 strings.Contains(err.Error(), "1062") 泛匹配，
// 驱动转述的 SQL 参数原文里出现 1062（如金额 '1062.00'、订单号含 1062）时，
// 普通数据库错误会被误判成「已存在」，真实故障被业务文案掩盖。
func TestIsDuplicateByErrorNumber(t *testing.T) {
	dup := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry '9-1' for key 'carts.uk_user_sku'"}
	other := &mysql.MySQLError{Number: 1264, Message: "Out of range value for column 'amount' at row 1"}
	// 错误号不是 1062、但文本里带 1062 的错误：泛匹配会命中，精确判定必须放行到上抛分支
	tricky := fmt.Errorf("执行失败: %w", &mysql.MySQLError{
		Number:  1406,
		Message: "Data too long for column 'remark' —— 参数值 '1062.00' 无法写入",
	})

	if !IsDuplicate(dup) {
		t.Error("1062 应判定为唯一键冲突")
	}
	if !IsDuplicate(fmt.Errorf("insert: %w", dup)) {
		t.Error("被 %w 包裹的 1062 应沿错误链判定为唯一键冲突")
	}
	if IsDuplicate(other) {
		t.Error("非 1062 的 MySQL 错误不得判定为唯一键冲突")
	}
	if IsDuplicate(tricky) {
		t.Error("错误文本里的 1062 数字不得当成冲突错误号")
	}
	if IsDuplicate(errors.New("connection refused")) {
		t.Error("非 MySQL 错误不得判定为唯一键冲突")
	}
	if IsDuplicate(nil) {
		t.Error("nil 不得判定为唯一键冲突")
	}
}

// TestIsDuplicateWithIndexName 索引名收窄：同表多把唯一索引时只认业务相关的那把
func TestIsDuplicateWithIndexName(t *testing.T) {
	shiftDup := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry '1-2-1' for key 'pos_shifts.uq_shift_active'"}
	codeDup := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'x' for key 'roles.uk_role_community_code'"}

	if !IsDuplicate(shiftDup, "uq_shift_active") {
		t.Error("命中目标索引名时应判定为冲突")
	}
	if IsDuplicate(shiftDup, "uk_role_community_code") {
		t.Error("撞的是别的索引时不得判定为目标冲突")
	}
	if !IsDuplicate(codeDup, "uq_shift_active", "uk_role_community_code") {
		t.Error("多个候选索引名命中任意一个即应判定为冲突")
	}
	// 传空串不收窄语义：避免调用方误以为「没写索引名」等于「匹配全部」
	if IsDuplicate(codeDup, "") {
		t.Error("空索引名不应命中")
	}
}
