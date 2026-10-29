package model

// Models 订单服务全部迁移模型（AutoMigrate 的模型清单，按建表依赖顺序）
// 参数：无
// 返回：
//   - []any: 模型值指针切片，可直接传给 db.AutoMigrate
//   - 异常：本函数不做 IO，不返回错误
//
// 使用示例：
//
//	db.GetDb().AutoMigrate(model.Models()...)
//
// 为什么导出：与 user 服务同口径——服务启动与 market-admin init 的「等待表就绪」共用
// 同一份清单；演示种子包含订单/售后/收银班次/进销存段，init 此前只等 user/cart/product
// 域的表，订单域未迁移完就开写会留下「已建社区、缺订单」的半导入态（见 user/model.Models）。
func Models() []any {
	return []any{
		&Order{},
		&OrderItem{},
		&OrderStatusLog{},
		&Refund{},
		&RefundItem{},
		&RefundConfig{},
		&RefundLog{},
		&SchedulerFailure{},
		&PosHeldOrder{},
		&PosShift{},
		&AppPreference{},
		&ExportTask{},
	}
}
