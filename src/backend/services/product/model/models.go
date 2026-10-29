package model

// Models 商品服务全部迁移模型（AutoMigrate 的模型清单，按建表依赖顺序）
// 参数：无
// 返回：
//   - []any: 模型值指针切片，可直接传给 db.AutoMigrate
//   - 异常：本函数不做 IO，不返回错误
//
// 使用示例：
//
//	db.GetDb().AutoMigrate(model.Models()...)
//
// 为什么导出：与 user 服务同口径——服务启动与 market-admin init 的「等待表就绪」
// 共用同一份清单，避免新增模型只改服务侧、init 等待面漏表（详见 user/model.Models 注释）。
// 注意：本服务 main.go 的 autoMigrate 在 AutoMigrate 之后还有索引收敛步骤，那些不属于清单。
func Models() []any {
	return []any{
		&Category{},
		&Product{},
		&ProductImage{},
		&ProductSpec{},
		&ProductSpecValue{},
		&Sku{},
		&CommunityPricing{},
		&StockRecord{},
		&StockOpLog{},
		&StockBatch{},
		&Supplier{},
		&PurchaseOrder{},
		&PurchaseOrderItem{},
		&StockInOrder{},
		&StockInOrderItem{},
		&StockOutOrder{},
		&StockOutOrderItem{},
		&StockCheck{},
		&StockCheckItem{},
		&Coupon{},
		&UserCoupon{},
		&CouponProductScope{},
		&FlashSale{},
		&FlashSaleItem{},
		&FullReduction{},
		&FreeShippingActivity{},
		&FreeShippingScope{},
		&Banner{},
		&AttachmentConfig{},
		&Attachment{},
		&AttachmentMigrateItem{},
	}
}
