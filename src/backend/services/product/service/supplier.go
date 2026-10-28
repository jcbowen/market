package service

import (
	"context"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/likeutil"
	"github.com/jcbowen/market/src/backend/common/pageparam"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// SupplierService 供应商业务服务
type SupplierService struct {
	db *mysql.Instance
}

// NewSupplierService 创建供应商业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *SupplierService: 服务实例
func NewSupplierService(db *mysql.Instance) *SupplierService {
	return &SupplierService{db: db}
}

// ListSuppliers 查询供应商列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *product.ListSuppliersResp: 分页列表
//   - error: 错误信息
func (s *SupplierService) ListSuppliers(ctx context.Context, req *product.ListSuppliersReq) (*product.ListSuppliersResp, error) {
	page, pageSize := pageparam.Clamp(int(req.Pagination.GetPage()), int(req.Pagination.GetPageSize()))

	db := s.db.GetDb().WithContext(ctx).Model(&model.Supplier{})
	if req.CommunityId > 0 {
		db = db.Where("community_id = ?", req.CommunityId)
	}
	if req.Keyword != "" {
		kw := likeutil.Contains(req.Keyword)
		db = db.Where("name LIKE ? OR contact_name LIKE ? OR phone LIKE ?", kw, kw, kw)
	}
	if req.Status > 0 {
		db = db.Where("status = ?", req.Status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}
	offset := (page - 1) * pageSize
	var list []model.Supplier
	if err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, err
	}

	respList := make([]*product.SupplierInfo, 0, len(list))
	for _, sp := range list {
		respList = append(respList, ToSupplierInfo(&sp))
	}
	return &product.ListSuppliersResp{
		List: respList,
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}, nil
}

// CreateSupplier 创建供应商
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *product.SupplierInfo: 供应商信息
//   - error: 错误信息
func (s *SupplierService) CreateSupplier(ctx context.Context, req *product.CreateSupplierReq) (*product.SupplierInfo, error) {
	if req.Name == "" {
		return nil, invalidArg("供应商名称不能为空")
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	sp := &model.Supplier{
		CommunityID: req.CommunityId,
		Name:        req.Name,
		ContactName: req.ContactName,
		Phone:       req.Phone,
		Address:     req.Address,
		Remark:      req.Remark,
		Status:      1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.db.GetDb().WithContext(ctx).Create(sp).Error; err != nil {
		return nil, err
	}
	return ToSupplierInfo(sp), nil
}

// UpdateSupplier 更新供应商（归属校验：跨社区直接拒绝）
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *product.SupplierInfo: 供应商信息
//   - error: 错误信息
func (s *SupplierService) UpdateSupplier(ctx context.Context, req *product.UpdateSupplierReq) (*product.SupplierInfo, error) {
	var sp model.Supplier
	if err := s.db.GetDb().WithContext(ctx).First(&sp, req.Id).Error; err != nil {
		return nil, invalidArg("供应商不存在")
	}
	if req.CommunityId > 0 && sp.CommunityID != req.CommunityId {
		return nil, invalidArg("供应商不属于当前社区")
	}
	updates := map[string]interface{}{
		"name":         req.Name,
		"contact_name": req.ContactName,
		"phone":        req.Phone,
		"address":      req.Address,
		"remark":       req.Remark,
		"status":       req.Status,
		"updated_at":   time.Now().Format("2006-01-02 15:04:05"),
	}
	if err := s.db.GetDb().WithContext(ctx).Model(&model.Supplier{}).Where("id = ?", sp.ID).Updates(updates).Error; err != nil {
		return nil, err
	}
	sp.Name = req.Name
	sp.ContactName = req.ContactName
	sp.Phone = req.Phone
	sp.Address = req.Address
	sp.Remark = req.Remark
	sp.Status = req.Status
	return ToSupplierInfo(&sp), nil
}

// DeleteSupplier 删除供应商（被采购单/入库单引用时拒绝删除）
// 参数：
//   - ctx: 上下文
//   - req: 删除请求
//
// 返回值：
//   - error: 错误信息
func (s *SupplierService) DeleteSupplier(ctx context.Context, req *product.DeleteSupplierReq) error {
	var sp model.Supplier
	if err := s.db.GetDb().WithContext(ctx).First(&sp, req.Id).Error; err != nil {
		return invalidArg("供应商不存在")
	}
	if req.CommunityId > 0 && sp.CommunityID != req.CommunityId {
		return invalidArg("供应商不属于当前社区")
	}

	db := s.db.GetDb().WithContext(ctx)
	var poCount, inCount int64
	if err := db.Model(&model.PurchaseOrder{}).Where("supplier_id = ?", sp.ID).Count(&poCount).Error; err != nil {
		return err
	}
	if err := db.Model(&model.StockInOrder{}).Where("supplier_id = ?", sp.ID).Count(&inCount).Error; err != nil {
		return err
	}
	if poCount > 0 || inCount > 0 {
		return invalidArg("供应商已被采购/入库单引用，不能删除（可停用）")
	}
	return db.Delete(&sp).Error
}

// ToSupplierInfo 将供应商模型转换为 proto 信息
// 参数：
//   - sp: 供应商模型
//
// 返回值：
//   - *product.SupplierInfo: proto 供应商信息
func ToSupplierInfo(sp *model.Supplier) *product.SupplierInfo {
	if sp == nil {
		return nil
	}
	return &product.SupplierInfo{
		Id:          sp.ID,
		CommunityId: sp.CommunityID,
		Name:        sp.Name,
		ContactName: sp.ContactName,
		Phone:       sp.Phone,
		Address:     sp.Address,
		Remark:      sp.Remark,
		Status:      sp.Status,
		CreatedAt:   sp.CreatedAt,
	}
}
