package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/jcbowen/jcbaseGo/component/attachment/remote"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	cartModel "github.com/jcbowen/market/src/backend/services/cart/model"
	orderModel "github.com/jcbowen/market/src/backend/services/order/model"
	payModel "github.com/jcbowen/market/src/backend/services/payment/model"
	"github.com/jcbowen/market/src/backend/services/product/model"
	userModel "github.com/jcbowen/market/src/backend/services/user/model"
)

// 本文件是「应用中心 · 数据清理」模块：一键清空全部业务数据，用于把环境重置为
// 空数据 / 重新导入演示数据做测试。与 dataimport.go（导入）互为逆向操作。
//
// 保留白名单（不清理，硬编码于下方域定义的排除口径）：
//   - 用户指定：支付配置（pay_config）、附件配置（attachment_config）、站点设置（site_config）；
//   - 系统骨架：社区（communities）、管理员（admins）、任职（admin_communities）、
//     角色（roles）、行政区划（regions）、客户端渠道凭证（client_channels）；
//   - 单行系统配置：售后配置（refund_config）、应用偏好（app_preferences）；
//   - 演示种子图片：种子导入不写 attachment 表，图片 URL（/uploads/seed/...）直写业务字段，
//     文件固定在 uploads/seed/ 下，本模块不触碰该目录。
//
// 清理口径：按「售后 → 订单 → 支付流水 → 进销存 → 用户资产 → 营销 → 目录 → 附件 → 用户」
// 逆依赖顺序分域执行；域内子表在前；全部硬删（Unscoped，含软删行）；
// 逐域独立事务，单域失败记录明细并继续后续域。
// 附件域额外删除文件本体（本地 uploads 文件 / OSS 对象），文件删除失败不回滚记录。

// dataCleanupConfirmText 执行清理必须输入的确认文案（前后端约定，防误触）
const dataCleanupConfirmText = "确认清空全部业务数据"

// cleanupSectionKeyAttachments 附件域 key（文件清理特判用）
const cleanupSectionKeyAttachments = "attachments"

// cleanupSection 清理域定义
type cleanupSection struct {
	Key    string
	Name   string
	Desc   string
	Tables []any // 按「子表在前」的依赖顺序排列，逐表硬删（Unscoped）
}

// dataCleanupSections 清理域注册表（顺序即执行顺序，禁止调整先后关系）
var dataCleanupSections = []cleanupSection{
	{Key: "refunds", Name: "售后 / 退款", Desc: "售后单、售后明细、退款流水日志", Tables: []any{
		orderModel.RefundItem{}, orderModel.Refund{}, orderModel.RefundLog{},
	}},
	{Key: "orders", Name: "订单 / POS", Desc: "订单及明细、状态日志、POS 班次与挂单、导出任务、调度残留", Tables: []any{
		orderModel.OrderItem{}, orderModel.OrderStatusLog{}, orderModel.Order{},
		orderModel.PosHeldOrder{}, orderModel.PosShift{}, orderModel.ExportTask{}, orderModel.SchedulerFailure{},
	}},
	{Key: "payments", Name: "支付流水", Desc: "支付单流水、退款支付单、回调通知日志、调度残留（支付配置保留）", Tables: []any{
		payModel.RefundPayment{}, payModel.Payment{}, payModel.NotifyLog{}, payModel.SchedulerFailure{},
	}},
	{Key: "inventory", Name: "进销存", Desc: "采购单、供应商、出入库/盘点单及明细、库存流水/批次/操作日志", Tables: []any{
		model.PurchaseOrderItem{}, model.PurchaseOrder{}, model.Supplier{},
		model.StockInOrderItem{}, model.StockInOrder{}, model.StockOutOrderItem{}, model.StockOutOrder{},
		model.StockCheckItem{}, model.StockCheck{}, model.StockRecord{}, model.StockOpLog{}, model.StockBatch{},
	}},
	{Key: "user_assets", Name: "用户资产", Desc: "购物车、用户持券", Tables: []any{
		cartModel.Cart{}, model.UserCoupon{},
	}},
	{Key: "marketing", Name: "营销活动", Desc: "秒杀活动及明细、优惠券及适用范围、满减、满免邮", Tables: []any{
		model.FlashSaleItem{}, model.FlashSale{}, model.CouponProductScope{}, model.Coupon{},
		model.FullReduction{}, model.FreeShippingScope{}, model.FreeShippingActivity{},
	}},
	{Key: "catalog", Name: "商品目录", Desc: "商品图、规格、SKU、社区定价、商品、分类、Banner", Tables: []any{
		model.ProductImage{}, model.ProductSpecValue{}, model.ProductSpec{}, model.Sku{},
		model.CommunityPricing{}, model.Product{}, model.Category{}, model.Banner{},
	}},
	{Key: cleanupSectionKeyAttachments, Name: "附件", Desc: "上传附件记录硬删 + 文件本体删除（附件配置保留；演示种子图片不受影响）", Tables: []any{
		model.AttachmentMigrateItem{}, model.Attachment{},
	}},
	{Key: "users", Name: "C 端用户", Desc: "用户、收货地址、会员-社区关联、渠道身份、区划更新任务（管理员/社区/角色保留）", Tables: []any{
		userModel.Address{}, userModel.UserCommunity{}, userModel.UserChannelIdentity{},
		userModel.User{}, userModel.RegionUpdateTask{},
	}},
}

// DataCleanupService 数据清理业务服务
type DataCleanupService struct {
	db            *mysql.Instance
	attachmentSvc *AttachmentService // 预留：附件配置读取与存储能力同包复用
}

// NewDataCleanupService 创建数据清理服务
//
// 参数：
//   - db: 数据库实例
//   - attachmentSvc: 附件服务（预留扩展，当前文件删除直接复用同包工具函数）
//
// 返回值：
//   - *DataCleanupService: 数据清理服务
//
// 使用示例：
//
//	svc := service.NewDataCleanupService(db, attachmentSvc)
func NewDataCleanupService(db *mysql.Instance, attachmentSvc *AttachmentService) *DataCleanupService {
	return &DataCleanupService{db: db, attachmentSvc: attachmentSvc}
}

// Preview 清理预检：逐域统计将清理的记录行数（附件域附文件数），只读不写库。
// 行数按 Unscoped 口径统计（含软删行，与执行时硬删的行数一致）。
//
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - *product.DataCleanupPreviewResp: 各域行数统计（按执行顺序排列）
//   - error: 统计查询失败时返回包装错误
//
// 使用示例：
//
//	resp, err := svc.Preview(ctx)
func (s *DataCleanupService) Preview(ctx context.Context) (*product.DataCleanupPreviewResp, error) {
	resp := &product.DataCleanupPreviewResp{}
	db := s.db.GetDb().WithContext(ctx)
	for _, sec := range dataCleanupSections {
		info := &product.DataCleanupSectionInfo{Key: sec.Key, Name: sec.Name, Description: sec.Desc}
		for _, t := range sec.Tables {
			var n int64
			if err := db.Unscoped().Model(t).Count(&n).Error; err != nil {
				return nil, fmt.Errorf("统计 %s 行数失败: %w", sec.Name, err)
			}
			info.RowCount += n
		}
		// 附件域文件数 = 附件记录数（文件本体与记录一一对应；path 为空的记录无文件）
		if sec.Key == cleanupSectionKeyAttachments {
			info.FileCount = info.RowCount
		}
		resp.Sections = append(resp.Sections, info)
	}
	return resp, nil
}

// Run 执行清理：校验确认文案 → 逆依赖顺序逐域清理 → 返回统计与明细。
// 逐域独立事务，单域失败记录明细并继续；附件域文件删除失败不阻断记录清理。
//
// 参数：
//   - ctx: 上下文
//   - req: 执行入参（confirm_text 必须与 dataCleanupConfirmText 完全一致）
//
// 返回值：
//   - *product.DataCleanupRunResp: 域数/行数/文件数统计与逐域明细
//   - error: 确认文案不匹配返回 InvalidArgument；统计附件清单失败返回包装错误
//
// 使用示例：
//
//	resp, err := svc.Run(ctx, &product.DataCleanupRunReq{ConfirmText: "确认清空全部业务数据"})
func (s *DataCleanupService) Run(ctx context.Context, req *product.DataCleanupRunReq) (*product.DataCleanupRunResp, error) {
	if req == nil || req.ConfirmText != dataCleanupConfirmText {
		return nil, bizerr.New(codes.InvalidArgument, "确认文案不匹配，请输入："+dataCleanupConfirmText)
	}
	resp := &product.DataCleanupRunResp{SectionsTotal: int32(len(dataCleanupSections))}
	for _, sec := range dataCleanupSections {
		rows, err := s.runSection(ctx, sec, resp)
		if err != nil {
			resp.SectionsFailed++
			resp.Details = append(resp.Details, &product.DataCleanupItem{
				SectionKey: sec.Key, Name: sec.Name, Status: "failed",
				Message: fmt.Sprintf("清理失败: %v", err),
			})
			log.Printf("[data-cleanup] 域 %s(%s) 清理失败: %v", sec.Name, sec.Key, err)
			continue
		}
		resp.RowsDeleted += rows
		resp.Details = append(resp.Details, &product.DataCleanupItem{
			SectionKey: sec.Key, Name: sec.Name, Status: "cleaned", Rows: rows,
		})
	}
	log.Printf("[data-cleanup] 完成：域 %d/%d 成功，删除记录 %d 行，文件 %d 成功/%d 失败",
		resp.SectionsTotal-resp.SectionsFailed, resp.SectionsTotal, resp.RowsDeleted, resp.FilesDeleted, resp.FilesFailed)
	return resp, nil
}

// runSection 清理单个数据域：通用域单事务逐表硬删；附件域走文件+记录专用流程。
//
// 参数：
//   - ctx: 上下文
//   - sec: 清理域定义
//   - resp: 执行响应（附件域直接把文件删除成败明细写入其中）
//
// 返回值：
//   - int64: 本次删除的记录行数
//   - error: 事务失败或附件清单查询失败时返回错误
func (s *DataCleanupService) runSection(ctx context.Context, sec cleanupSection, resp *product.DataCleanupRunResp) (int64, error) {
	if sec.Key == cleanupSectionKeyAttachments {
		return s.cleanupAttachments(ctx, resp)
	}
	var rows int64
	err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, t := range sec.Tables {
			res := tx.Unscoped().Where("1 = 1").Delete(t)
			if res.Error != nil {
				return fmt.Errorf("%T: %w", t, res.Error)
			}
			rows += res.RowsAffected
		}
		return nil
	})
	return rows, err
}

// cleanupAttachments 附件域清理：先收集文件清单 → 事务硬删记录（附件迁移任务 + 附件记录）
// → 逐个删除文件本体（本地 uploads 文件 / OSS 对象）。
// 文件删除失败仅记明细（files_failed + failed 条目），不回滚记录；
// 演示种子图片不在 attachment 表，天然不受影响。
//
// 参数：
//   - ctx: 上下文
//   - resp: 执行响应（文件删除成败明细直接写入）
//
// 返回值：
//   - int64: 删除的附件记录行数（含迁移任务行）
//   - error: 配置缺失或清单查询/事务失败时返回错误
func (s *DataCleanupService) cleanupAttachments(ctx context.Context, resp *product.DataCleanupRunResp) (int64, error) {
	db := s.db.GetDb().WithContext(ctx)

	var cfg model.AttachmentConfig
	if err := db.Where("id = ?", attachmentConfigID).First(&cfg).Error; err != nil {
		return 0, fmt.Errorf("读取附件配置失败: %w", err)
	}
	// 文件清单：只取 path 非空的记录（path 为空 = 无文件本体）
	var paths []string
	if err := db.Unscoped().Model(&model.Attachment{}).
		Where("path <> ''").Pluck("path", &paths).Error; err != nil {
		return 0, fmt.Errorf("查询附件文件清单失败: %w", err)
	}

	var rows int64
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, t := range []any{model.AttachmentMigrateItem{}, model.Attachment{}} {
			res := tx.Unscoped().Where("1 = 1").Delete(t)
			if res.Error != nil {
				return fmt.Errorf("%T: %w", t, res.Error)
			}
			rows += res.RowsAffected
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	// 记录已删，文件本体逐个删除（失败仅记明细，不阻断）
	switch cfg.StorageType {
	case "oss":
		s.deleteOSSFiles(ctx, cfg, paths, resp)
	default:
		s.deleteLocalFiles(cfg, paths, resp)
	}
	return rows, nil
}

// deleteLocalFiles 删除本地存储的附件文件本体（uploads 目录下，按记录相对路径）。
//
// 参数：
//   - cfg: 附件配置（LocalDir 决定落盘根目录）
//   - paths: 待删文件相对路径清单
//   - resp: 执行响应（成败明细与计数直接写入）
func (s *DataCleanupService) deleteLocalFiles(cfg model.AttachmentConfig, paths []string, resp *product.DataCleanupRunResp) {
	root, err := attachmentRootDir(&cfg)
	if err != nil {
		resp.FilesFailed += int64(len(paths))
		resp.Details = append(resp.Details, &product.DataCleanupItem{
			SectionKey: cleanupSectionKeyAttachments, Name: "本地文件", Status: "failed",
			Message: fmt.Sprintf("定位附件根目录失败: %v", err),
		})
		return
	}
	for _, p := range paths {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(p))); err != nil && !os.IsNotExist(err) {
			s.appendFileFailed(resp, p, err)
			continue
		}
		resp.FilesDeleted++
	}
}

// deleteOSSFiles 删除 OSS 模式的附件对象（按记录的对象 Key 逐个删除）。
//
// 参数：
//   - ctx: 上下文
//   - cfg: 附件配置（OSS 凭证与 Endpoint）
//   - paths: 待删对象 Key 清单
//   - resp: 执行响应（成败明细与计数直接写入）
func (s *DataCleanupService) deleteOSSFiles(ctx context.Context, cfg model.AttachmentConfig, paths []string, resp *product.DataCleanupRunResp) {
	ossCfg, err := buildOSSConfig(&cfg)
	if err != nil {
		resp.FilesFailed += int64(len(paths))
		resp.Details = append(resp.Details, &product.DataCleanupItem{
			SectionKey: cleanupSectionKeyAttachments, Name: "OSS 对象", Status: "failed",
			Message: fmt.Sprintf("构建 OSS 配置失败: %v", err),
		})
		return
	}
	client, err := remote.NewOSSClient(*ossCfg)
	if err != nil {
		resp.FilesFailed += int64(len(paths))
		resp.Details = append(resp.Details, &product.DataCleanupItem{
			SectionKey: cleanupSectionKeyAttachments, Name: "OSS 对象", Status: "failed",
			Message: fmt.Sprintf("初始化 OSS 客户端失败: %v", err),
		})
		return
	}
	defer func() { _ = client.Close() }()
	for _, p := range paths {
		if err := client.Delete(ctx, p); err != nil {
			s.appendFileFailed(resp, p, err)
			continue
		}
		resp.FilesDeleted++
	}
}

// appendFileFailed 追加一条文件删除失败明细并累计失败数。
//
// 参数：
//   - resp: 执行响应
//   - path: 删除失败的文件相对路径
//   - cause: 失败原因
func (s *DataCleanupService) appendFileFailed(resp *product.DataCleanupRunResp, path string, cause error) {
	resp.FilesFailed++
	resp.Details = append(resp.Details, &product.DataCleanupItem{
		SectionKey: cleanupSectionKeyAttachments, Name: path, Status: "failed",
		Message: fmt.Sprintf("文件删除失败: %v", cause),
	})
}
