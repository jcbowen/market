package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Order 订单模型
// 对应订单主表，记录订单基本信息、金额、状态、配送信息等
// 状态：1待支付 2已支付 3备货中 4配送中 5已完成 6已取消 7退款中 8已退款
//
//nolint:lll
//go:generate go tool gormgen -struct Order
type Order struct {
	base.MysqlBaseModel
	ID      int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	OrderNo string `gorm:"column:order_no;type:varchar(50);not null;unique;comment:订单号" json:"order_no"`
	// 社区维度复合索引（community_id,status,created_at）：覆盖社区订单列表、营收报表按状态聚合
	CommunityID int64 `gorm:"column:community_id;type:bigint;not null;index:idx_orders_community_status_created,priority:1;comment:社区 ID" json:"community_id"`
	UserID      int64 `gorm:"column:user_id;type:bigint;not null;index;comment:用户 ID" json:"user_id"`
	// 状态参与三个复合索引：社区列表/报表（community_status_created）、履约指派（delivery_status）、
	// 调度器按状态扫描（status_updated / status_created，见下方时间列）
	Status         int32   `gorm:"column:status;type:tinyint;default:1;index:idx_orders_community_status_created,priority:2;index:idx_orders_delivery_status,priority:2;index:idx_orders_status_updated,priority:1;index:idx_orders_status_created,priority:1;comment:1待支付 2已支付 3备货中 4配送中 5已完成 6已取消 7退款中 8已退款" json:"status"`
	TotalAmount    float64 `gorm:"column:total_amount;type:decimal(10,2);not null;comment:订单总额" json:"total_amount"`
	DiscountAmount float64 `gorm:"column:discount_amount;type:decimal(10,2);default:0.00;comment:优惠金额，默认0" json:"discount_amount"`
	DeliveryFee    float64 `gorm:"column:delivery_fee;type:decimal(10,2);default:0.00;comment:配送费，默认0" json:"delivery_fee"`
	PayAmount      float64 `gorm:"column:pay_amount;type:decimal(10,2);not null;comment:实付金额" json:"pay_amount"`
	DeliveryType   int32   `gorm:"column:delivery_type;type:tinyint;default:1;comment:1自提 2配送 3到店" json:"delivery_type"`
	DeliveryTime   string  `gorm:"column:delivery_time;type:varchar(50);comment:期望配送时间" json:"delivery_time"`
	AddressID      int64   `gorm:"column:address_id;type:bigint;default:0;comment:地址 ID" json:"address_id"`
	ContactName    string  `gorm:"column:contact_name;type:varchar(50);comment:联系人" json:"contact_name"`
	ContactPhone   string  `gorm:"column:contact_phone;type:varchar(20);comment:联系电话" json:"contact_phone"`
	ContactAddress string  `gorm:"column:contact_address;type:varchar(255);comment:收货联系人地址" json:"contact_address"`
	ExpressCompany string  `gorm:"column:express_company;type:varchar(50);comment:物流公司（发货时写入）" json:"express_company"`
	ExpressNo      string  `gorm:"column:express_no;type:varchar(50);comment:物流单号（发货时写入）" json:"express_no"`
	Remark         string  `gorm:"column:remark;type:varchar(255);comment:用户备注" json:"remark"`
	CouponID       int64   `gorm:"column:coupon_id;type:bigint;default:0;comment:使用的优惠券 ID" json:"coupon_id"`
	// 自提核销码：仅自提单（delivery_type=1）生成，配送单为 NULL（unique 允许多个 NULL）
	PickupCode *string `gorm:"column:pickup_code;type:varchar(20);unique;comment:自提核销码" json:"pickup_code"`
	// 订单来源：1=C端用户自下单（默认，零值 Create 时走 DB default）2=后台收银台代客下单
	Source int32 `gorm:"column:source;type:tinyint;default:1;comment:订单来源 1C端用户 2收银台代客" json:"source"`
	// 经手人（收银员/管理员）：source=2 时记录，便于对账与追责
	OperatorID   int64  `gorm:"column:operator_id;type:bigint;default:0;comment:经手人 ID" json:"operator_id"`
	OperatorName string `gorm:"column:operator_name;type:varchar(50);comment:经手人姓名快照" json:"operator_name"`
	// 手动优惠总额（收银台抹零/整单改价折算）：实付 = max(0, 应付 - manual_discount)
	ManualDiscount float64 `gorm:"column:manual_discount;type:decimal(10,2);default:0.00;comment:手动优惠总额（抹零/整单改价）" json:"manual_discount"`
	// 收银班次 ID（0=非收银单）：退款/交班统计按订单归属班次口径
	ShiftID int64 `gorm:"column:shift_id;type:bigint;default:0;index:idx_shift_id;comment:收银班次 ID 0=非收银单" json:"shift_id"`
	// 履约工作台（B）：配送指派与签收凭证
	// 指派的履约人员（0=未指派）；姓名快照避免列表页再 RPC 查会员
	DeliveryUserID      int64   `gorm:"column:delivery_user_id;type:bigint;default:0;index:idx_orders_delivery_status,priority:1;comment:指派履约人员 ID 0=未指派" json:"delivery_user_id"`
	DeliveryUserName    string  `gorm:"column:delivery_user_name;type:varchar(50);comment:履约人员姓名快照" json:"delivery_user_name"`
	DeliveryProofImages string  `gorm:"column:delivery_proof_images;type:text;comment:送达签收凭证照片 URL JSON 数组" json:"delivery_proof_images"`
	PayTime             *string `gorm:"column:pay_time;type:datetime;comment:支付时间" json:"pay_time"`
	DeliverTime         *string `gorm:"column:deliver_time;type:datetime;comment:发货时间" json:"deliver_time"`
	CompleteTime        *string `gorm:"column:complete_time;type:datetime;comment:完成时间" json:"complete_time"`
	CancelTime          *string `gorm:"column:cancel_time;type:datetime;comment:取消时间" json:"cancel_time"`
	CancelReason        string  `gorm:"column:cancel_reason;type:varchar(255);comment:取消原因" json:"cancel_reason"`
	// 用户侧软删除标记（C端「删除订单」）：1=用户已删；C 端列表与角标统计排除，
	// 后台管理端不受影响；详情链路（售后进度/支付详情）保留可达
	UserDeleted int32 `gorm:"column:user_deleted;type:tinyint;default:0;comment:用户软删除 0否 1是" json:"user_deleted"`
	// 时间列参与调度器复合索引：超时扫描按 (status, created_at)、
	// 卡单恢复与抢占按 (status, updated_at)；此前的 (community_id,status,created_at)
	// 因缺少 community_id 前导列无法被这些查询使用
	CreatedAt string `gorm:"column:created_at;type:datetime;index:idx_orders_community_status_created,priority:3;index:idx_orders_status_created,priority:2;comment:创建时间" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime;index:idx_orders_status_updated,priority:2;comment:更新时间" json:"updated_at"`
}

// TableName 返回订单表名
// 返回值：
//   - string: 表名
func (Order) TableName() string {
	return "orders"
}

// OrderItem 订单商品项模型
// 对应订单商品明细表
//
//nolint:lll
//go:generate go tool gormgen -struct OrderItem
type OrderItem struct {
	base.MysqlBaseModel
	ID           int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	OrderID      int64   `gorm:"column:order_id;type:bigint;not null;index;comment:订单 ID" json:"order_id"`
	ProductID    int64   `gorm:"column:product_id;type:bigint;not null;comment:商品 ID" json:"product_id"`
	SkuID        int64   `gorm:"column:sku_id;type:bigint;not null;index;comment:SKU ID" json:"sku_id"`
	ProductName  string  `gorm:"column:product_name;type:varchar(100);not null;comment:商品名称快照" json:"product_name"`
	SpecName     string  `gorm:"column:spec_name;type:varchar(100);comment:规格名称快照" json:"spec_name"`
	ProductImage string  `gorm:"column:product_image;type:varchar(255);comment:商品图片快照" json:"product_image"`
	Price        float64 `gorm:"column:price;type:decimal(10,2);not null;comment:下单时单价" json:"price"`
	Quantity     int32   `gorm:"column:quantity;type:int;not null;comment:数量" json:"quantity"`
	TotalPrice   float64 `gorm:"column:total_price;type:decimal(10,2);not null;comment:小计" json:"total_price"`
	// 收银台改价痕迹：OriginalPrice=服务端实时单价快照（未改价时与 Price 相同）；
	// PriceModified=1 表示该行单价被收银员手动修改（仅对当前订单生效，不改商品档案价）
	OriginalPrice float64 `gorm:"column:original_price;type:decimal(10,2);default:0.00;comment:服务端实时单价快照（改价痕迹）" json:"original_price"`
	PriceModified int32   `gorm:"column:price_modified;type:tinyint;default:0;comment:单价是否被手动修改 0否 1是" json:"price_modified"`
	CreatedAt     string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
}

// TableName 返回订单商品项表名
// 返回值：
//   - string: 表名
func (OrderItem) TableName() string {
	return "order_items"
}

// OrderStatusLog 订单状态流转日志模型
//
//nolint:lll
//go:generate go tool gormgen -struct OrderStatusLog
type OrderStatusLog struct {
	base.MysqlBaseModel
	ID           int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	OrderID      int64  `gorm:"column:order_id;type:bigint;not null;index;comment:订单 ID" json:"order_id"`
	FromStatus   int32  `gorm:"column:from_status;type:tinyint;default:0;comment:变更前状态" json:"from_status"`
	ToStatus     int32  `gorm:"column:to_status;type:tinyint;not null;comment:变更后状态" json:"to_status"`
	OperatorType string `gorm:"column:operator_type;type:varchar(20);comment:操作者类型（user / system / admin）" json:"operator_type"`
	OperatorID   int64  `gorm:"column:operator_id;type:bigint;default:0;comment:操作者 ID" json:"operator_id"`
	Remark       string `gorm:"column:remark;type:varchar(255);comment:备注" json:"remark"`
	CreatedAt    string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
}

// TableName 返回订单状态日志表名
// 返回值：
//   - string: 表名
func (OrderStatusLog) TableName() string {
	return "order_status_logs"
}

// Refund 退款申请模型
// 状态：1待审核 2已同意 3已拒绝 4退款中 5已退款
// 类型：1整单退 2部分退
//
//nolint:lll
//go:generate go tool gormgen -struct Refund
type Refund struct {
	base.MysqlBaseModel
	ID            int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	RefundNo      string  `gorm:"column:refund_no;type:varchar(50);not null;unique;comment:退款单号" json:"refund_no"`
	OrderID       int64   `gorm:"column:order_id;type:bigint;not null;index;comment:订单 ID" json:"order_id"`
	UserID        int64   `gorm:"column:user_id;type:bigint;not null;index;comment:用户 ID" json:"user_id"`
	PaymentNo     string  `gorm:"column:payment_no;type:varchar(50);comment:关联支付单号，申请退款时自动获取" json:"payment_no"`
	Amount        float64 `gorm:"column:amount;type:decimal(10,2);not null;comment:退款金额（商品应退 + 运费，服务端汇算为准，用户仅可调低）" json:"amount"`
	FreightAmount float64 `gorm:"column:freight_amount;type:decimal(10,2);not null;default:0.00;comment:本单退还的运费金额" json:"freight_amount"`
	Type          int32   `gorm:"column:type;type:tinyint;not null;default:1;comment:1整单退 2部分退 3仅金额退（全额退完时升级为1）" json:"type"`
	Reason        string  `gorm:"column:reason;type:varchar(255);comment:退款原因" json:"reason"`
	// 状态参与调度器扫描索引：超时审批按 (status, created_at)、
	// 卡单重试按 (status, updated_at)，此前 refunds 无任何 status 索引，两处均全表扫
	Status       int32   `gorm:"column:status;type:tinyint;default:1;index:idx_refunds_status_created,priority:1;index:idx_refunds_status_updated,priority:1;comment:1待审核 2已同意 3已拒绝 4退款中 5已退款" json:"status"`
	RejectReason string  `gorm:"column:reject_reason;type:varchar(255);comment:拒绝原因" json:"reject_reason"`
	OperatorID   int64   `gorm:"column:operator_id;type:bigint;default:0;comment:审核操作人 ID" json:"operator_id"`
	RefundTime   *string `gorm:"column:refund_time;type:datetime;comment:退款成功时间" json:"refund_time"`
	CreatedAt    string  `gorm:"column:created_at;type:datetime;index:idx_refunds_status_created,priority:2;comment:创建时间" json:"created_at"`
	UpdatedAt    string  `gorm:"column:updated_at;type:datetime;index:idx_refunds_status_updated,priority:2;comment:更新时间" json:"updated_at"`

	// OrderNo 关联订单号（非表字段，列表查询时填充）
	OrderNo string `gorm:"-" json:"order_no"`
	// Items 退款商品明细（非表字段，查询时批量填充）
	Items []*RefundItem `gorm:"-" json:"items"`
	// Logs 审批/重试操作日志（非表字段，查询时批量填充）
	Logs []*RefundLog `gorm:"-" json:"logs"`
}

// TableName 返回退款申请表名
// 返回值：
//   - string: 表名
func (Refund) TableName() string {
	return "refunds"
}

// RefundItem 退款商品明细模型
// 记录退款申请涉及的具体商品行及汇算后应退金额
//
//nolint:lll
//go:generate go tool gormgen -struct RefundItem
type RefundItem struct {
	base.MysqlBaseModel
	ID           int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	RefundID     int64   `gorm:"column:refund_id;type:bigint;not null;index:idx_refund_items_refund_id;comment:退款单 ID" json:"refund_id"`
	OrderItemID  int64   `gorm:"column:order_item_id;type:bigint;not null;index;comment:订单商品行 ID" json:"order_item_id"`
	SkuID        int64   `gorm:"column:sku_id;type:bigint;not null;comment:SKU ID（退款成功后按此释放库存）" json:"sku_id"`
	ProductName  string  `gorm:"column:product_name;type:varchar(100);not null;comment:商品名称快照" json:"product_name"`
	SpecName     string  `gorm:"column:spec_name;type:varchar(100);comment:规格名称快照" json:"spec_name"`
	Quantity     int32   `gorm:"column:quantity;type:int;not null;comment:退货数量" json:"quantity"`
	RefundAmount float64 `gorm:"column:refund_amount;type:decimal(10,2);not null;comment:汇算后应退金额" json:"refund_amount"`
	CreatedAt    string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
}

// TableName 返回退款商品明细表名
// 返回值：
//   - string: 表名
func (RefundItem) TableName() string {
	return "refund_items"
}
