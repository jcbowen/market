package model

// Models 购物车服务全部迁移模型（AutoMigrate 的模型清单）
// 参数：无
// 返回：
//   - []any: 模型值指针切片，可直接传给 db.AutoMigrate
//   - 异常：本函数不做 IO，不返回错误
//
// 使用示例：
//
//	db.GetDb().AutoMigrate(model.Models()...)
//
// 为什么导出：与 user 服务同口径——服务启动与 market-admin init 的「等待表就绪」共用同一份清单。
func Models() []any {
	return []any{&Cart{}}
}
