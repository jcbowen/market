package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Product 商品模型
// 对应商品主表，包含基础信息、多规格标识、销售状态等
type Product struct {
	base.MysqlBaseModel
	ID               int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CommunityID      int64   `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	CategoryID       int64   `gorm:"column:category_id;type:bigint;not null;index;comment:分类 ID" json:"category_id"`
	Name             string  `gorm:"column:name;type:varchar(100);not null;comment:商品名称" json:"name"`
	Description      string  `gorm:"column:description;type:text;comment:商品描述" json:"description"`
	CoverImage       string  `gorm:"column:cover_image;type:varchar(255);comment:封面图 URL" json:"cover_image"`
	Images           string  `gorm:"column:images;type:json;comment:商品图片 JSON 数组" json:"images"`
	Unit             string  `gorm:"column:unit;type:varchar(20);comment:单位（件/斤/瓶/箱）" json:"unit"`
	IsMultiSpec      bool    `gorm:"column:is_multi_spec;type:tinyint(1);default:0;comment:是否多规格，默认0" json:"is_multi_spec"`
	MinPrice         float64 `gorm:"column:min_price;type:decimal(10,2);default:0.00;comment:价格区间下限(汇总)" json:"min_price"`
	MaxPrice         float64 `gorm:"column:max_price;type:decimal(10,2);default:0.00;comment:价格区间上限(汇总)" json:"max_price"`
	MinOriginalPrice float64 `gorm:"column:min_original_price;type:decimal(10,2);default:0.00;comment:划线价(汇总，最低价SKU的划线价，0 不展示)" json:"min_original_price"`
	ShowSales        bool    `gorm:"column:show_sales;type:tinyint(1);comment:C端是否显示销量" json:"show_sales"`
	VirtualSales     int32   `gorm:"column:virtual_sales;type:int;default:0;comment:虚拟销量（展示口径=真实销量+虚拟销量）" json:"virtual_sales"`
	Stock            int32   `gorm:"column:stock;type:int;default:0;comment:汇总库存(所有SKU之和)" json:"stock"`
	MinBuy           int32   `gorm:"column:min_buy;type:int;comment:最低购买份数" json:"min_buy"`
	Status           int32   `gorm:"column:status;type:tinyint;default:1;index:idx_products_status_on_sale,priority:1;comment:0下架 1上架 2删除" json:"status"`
	Sort             int32   `gorm:"column:sort;type:int;default:0;comment:排序，默认0" json:"sort"`
	SalesCount       int32   `gorm:"column:sales_count;type:int;default:0;comment:累计销量，默认0" json:"sales_count"`
	// OnSaleAt 定时上架时间：与 Status 组成复合索引 idx_products_status_on_sale，
	// 调度器每分钟的到点扫描（WHERE status=0 AND on_sale_at<=NOW()）走索引，不再全表 UPDATE
	OnSaleAt *string `gorm:"column:on_sale_at;type:datetime;index:idx_products_status_on_sale,priority:2;comment:定时上架时间，到点由调度器自动上架(NULL=不定时)" json:"on_sale_at"`
	CreatedAt        string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt        string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回商品表名
// 返回值：
//   - string: 表名
func (Product) TableName() string {
	return "products"
}
