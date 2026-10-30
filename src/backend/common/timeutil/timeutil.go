package timeutil

import "time"

// NormalizeRFC3339 把 parseTime DSN 读回的 RFC3339 时间字面量（如
// "2026-10-30T15:04:05+08:00"）归一化为 MySQL DATETIME 列可直接接受的
// "2006-01-02 15:04:05" 形态。
//
// 背景：模型时间列用 string 承载时，经 parseTime DSN 读回的值是 RFC3339 形态；
// 整行 Save/Updates 时若不归一化，该字面量会被原样写库——MySQL 8.0.19+ 能解析
// 带时区偏移的 DATETIME 字面量，5.7 及更早版本直接 Error 1292。
// 非 RFC3339 输入（已是规范化形态、空串等）原样返回。
//
// 参数：
//   - s: 待归一化的时间字符串
//
// 返回值：
//   - string: 归一化后的时间字符串
func NormalizeRFC3339(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Local().Format("2006-01-02 15:04:05")
	}
	return s
}
