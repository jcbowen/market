// Package likeutil LIKE 模式串的转义与拼装。
//
// LIKE 的第二个参数不是「值」而是「模式」：`%` 匹配任意串、`_` 匹配任意单字符，
// 未转义时用户在搜索框输入一个 `%` 就能把条件变成「匹配全部」，
// 既让精确搜索失效，也会让前缀/后缀模式退化成整表扫描（走不到索引）。
package likeutil

import "strings"

// escapeReplacer 按 MySQL 默认转义符（反斜杠）转义 LIKE 模式元字符
// 顺序由 NewReplacer 保证一次性替换，不会出现把 `\%` 里的反斜杠再二次转义
var escapeReplacer = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Escape 转义字符串中的 LIKE 元字符，使其按字面量匹配
//
// 参数：
//   - value: 原始输入（可为空串）
//
// 返回值：
//   - string: 转义后的字符串（`\`→`\\`、`%`→`\%`、`_`→`\_`）
//
// 使用示例：
//
//	db.Where("name LIKE ?", "%"+likeutil.Escape(keyword)+"%")
func Escape(value string) string {
	return escapeReplacer.Replace(value)
}

// Contains 把用户输入转义后拼成「包含匹配」模式串（前后各加 %）
// 用于所有以关键字做子串搜索的列表接口，调用方不要再手工拼 "%" + kw + "%"
//
// 参数：
//   - value: 用户输入的关键词
//
// 返回值：
//   - string: 可直接作为 LIKE 参数的模式串
//
// 使用示例：
//
//	kw := likeutil.Contains(strings.TrimSpace(req.Keyword))
//	db = db.Where("name LIKE ? OR remark LIKE ?", kw, kw)
func Contains(value string) string {
	return "%" + Escape(value) + "%"
}

// Prefix 把用户输入转义后拼成「前缀匹配」模式串（只在末尾加 %）
// 用于单号、编码这类「从头输入」的检索：保留前缀语义才能用上列上的索引
//
// 参数：
//   - value: 用户输入的关键词
//
// 返回值：
//   - string: 可直接作为 LIKE 参数的模式串
func Prefix(value string) string {
	return Escape(value) + "%"
}
