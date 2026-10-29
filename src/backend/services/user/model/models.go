package model

// Models 用户服务全部迁移模型（AutoMigrate 的模型清单，按建表依赖顺序）
// 参数：无
// 返回：
//   - []any: 模型值指针切片，可直接传给 db.AutoMigrate
//   - 异常：本函数不做 IO，不返回错误
//
// 使用示例：
//
//	db.GetDb().AutoMigrate(model.Models()...)          // services/user/main.go
//	missing := waitMissingTables(db, model.Models()...) // cmd/market-admin
//
// 为什么导出：init（market-admin）需要在导入演示种子前确认「本服务所有表都已迁移」，
// 清单曾散落在两处、新增模型时漏改导致种子写入未迁移表产生半状态；
// 现由本函数单一来源，服务启动与 init 等待面共用同一份表清单。
func Models() []any {
	return []any{
		&User{},
		&UserCommunity{},
		&Address{},
		&Community{},
		&Admin{},
		&AdminCommunity{},
		&Role{},
		&Region{},
		&RegionUpdateTask{},
		&ClientChannel{},
		&UserChannelIdentity{},
		&SiteConfig{},
	}
}
