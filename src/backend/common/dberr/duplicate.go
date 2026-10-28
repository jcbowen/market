// Package dberr 数据库错误的语义判定：把「驱动返回的原始错误」翻译成业务口径，
// 避免各处用裸子串匹配（如 strings.Contains(err.Error(), "1062")）导致的误判。
package dberr

import (
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// mysqlErrDuplicateEntry MySQL 唯一键冲突错误号
const mysqlErrDuplicateEntry = 1062

// IsDuplicate 判断错误是否为 MySQL 唯一键（含主键）冲突
// 按错误号精确判定，不做文本泛匹配：GORM/驱动的错误文本会带上 SQL 参数原文，
// 参数值里恰好出现 "1062"（如金额 1062.00 被驱动转述）时子串匹配会把普通报错
// 误判成唯一键冲突，业务侧据此返回「已存在」就是掩盖真实故障
//
// 参数：
//   - err: 数据库调用返回的错误（可为 nil，也可为被 %w 包裹过的错误）
//   - indexNames: 可选的索引名收窄条件；给出时要求冲突键名命中其中任意一个，
//     用于同一张表有多把唯一索引、只有特定一把才代表业务上的「重复」的场景
//
// 返回值：
//   - bool: 是否为（指定索引上的）唯一键冲突
//
// 使用示例：
//
//	if dberr.IsDuplicate(err) { return bizerr.New(codes.AlreadyExists, "编码已存在") }
//	if dberr.IsDuplicate(err, "uq_shift_active") { return "您已有进行中的班次" }
func IsDuplicate(err error, indexNames ...string) bool {
	if err == nil {
		return false
	}
	var myErr *mysql.MySQLError
	if !errors.As(err, &myErr) || myErr.Number != mysqlErrDuplicateEntry {
		return false
	}
	if len(indexNames) == 0 {
		return true
	}
	// MySQL 1062 原文形如：Duplicate entry '1-2' for key 'uq_shift_active'
	// （8.0 起可能是 '表名.索引名'），故按包含判定并容忍前缀
	for _, name := range indexNames {
		if name != "" && strings.Contains(myErr.Message, name) {
			return true
		}
	}
	return false
}
