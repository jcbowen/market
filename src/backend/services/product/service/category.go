package service

import (
	"context"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// CategoryService 分类业务服务
type CategoryService struct {
	db *mysql.Instance
}

// NewCategoryService 创建分类业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *CategoryService: 分类业务服务实例
func NewCategoryService(db *mysql.Instance) *CategoryService {
	return &CategoryService{db: db}
}

// ListCategories 查询社区分类列表
// 默认仅返回启用分类（C 端公开接口口径）；includeDisabled=true 时返回全部（管理端分类管理页）
//
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//   - includeDisabled: 是否包含禁用分类
//
// 返回值：
//   - []*model.Category: 分类列表（按 sort asc, id asc 排序）
//   - error: 错误信息
func (s *CategoryService) ListCategories(ctx context.Context, communityID int64, includeDisabled bool) ([]*model.Category, error) {
	var list []*model.Category
	db := s.db.GetDb().WithContext(ctx)
	if !includeDisabled {
		db = db.Where("status = ?", 1)
	}
	if communityID > 0 {
		db = db.Where("community_id = ?", communityID)
	}
	err := db.Order("sort asc, id asc").Find(&list).Error
	return list, err
}

// GetCategory 根据 ID 查询分类
// 参数：
//   - ctx: 上下文
//   - id: 分类 ID
//   - communityId: 操作者社区语境（>0 时校验归属，见 scope.go）
//
// 返回值：
//   - *model.Category: 分类模型
//   - error: 错误信息
func (s *CategoryService) GetCategory(ctx context.Context, id, communityId int64) (*model.Category, error) {
	var c model.Category
	err := s.db.GetDb().WithContext(ctx).First(&c, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, bizerr.New(codes.NotFound, "分类不存在")
	}
	if err != nil {
		return nil, err
	}
	if err := EnsureScope(communityId, c.CommunityID, "分类"); err != nil {
		return nil, err
	}
	return &c, nil
}

// CreateCategory 创建分类
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.Category: 分类模型
//   - error: 错误信息
func (s *CategoryService) CreateCategory(ctx context.Context, req *product.CreateCategoryReq) (*model.Category, error) {
	c := &model.Category{
		CommunityID: req.CommunityId,
		ParentID:    req.ParentId,
		Name:        req.Name,
		Icon:        req.Icon,
		Sort:        req.Sort,
		Status:      1,
	}
	if err := s.db.GetDb().WithContext(ctx).Create(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateCategory 更新分类
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *model.Category: 分类模型
//   - error: 错误信息
func (s *CategoryService) UpdateCategory(ctx context.Context, req *product.UpdateCategoryReq) (*model.Category, error) {
	c, err := s.GetCategory(ctx, req.Id, req.CommunityId)
	if err != nil {
		return nil, err
	}
	c.Name = req.Name
	c.Icon = req.Icon
	c.Sort = req.Sort
	// status 由管理端全量提交（编辑表单显式传值），直接赋值以支持启用/禁用双向切换；
	// 旧逻辑 `if req.Status != 0` 会把 0（禁用）当作未传，导致分类永远无法禁用
	c.Status = req.Status
	if err := s.db.GetDb().WithContext(ctx).Save(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

// DeleteCategory 删除分类
// 参数：
//   - ctx: 上下文
//   - id: 分类 ID
//   - communityId: 操作者社区语境（>0 时校验归属，见 scope.go）
//
// 返回值：
//   - error: 错误信息
func (s *CategoryService) DeleteCategory(ctx context.Context, id, communityId int64) error {
	if _, err := s.GetCategory(ctx, id, communityId); err != nil {
		return err
	}
	var count int64
	if err := s.db.GetDb().WithContext(ctx).Model(&model.Category{}).Where("parent_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return bizerr.New(codes.FailedPrecondition, "存在子分类，无法删除")
	}
	// 物理删除前校验商品引用：MysqlBaseModel 无 DeletedAt，删了就是真删，
	// 不拦会让商品 category_id 悬空（列表/详情展示与编辑均失锚）；
	// 软删商品（status=2）已不可售且不再出现在任何列表，不应永久卡住分类删除
	var productCount int64
	if err := s.db.GetDb().WithContext(ctx).Model(&model.Product{}).
		Where("category_id = ? AND status <> ?", id, 2).Count(&productCount).Error; err != nil {
		return err
	}
	if productCount > 0 {
		return bizerr.New(codes.FailedPrecondition, "分类下存在商品，无法删除")
	}
	return s.db.GetDb().WithContext(ctx).Delete(&model.Category{}, id).Error
}

// BatchSortCategories 分类批量排序（管理端拖拽后整表提交）
// 事务内逐项校验归属并写入目标 sort，任一项越权即整体失败回滚；
// 校验走 GetCategory（含 EnsureScope），与单条编辑的权限口径一致
//
// 参数：
//   - ctx: 上下文
//   - items: 全量有序列表（id + 目标 sort，建议按显示顺序 ×10 留空隙）
//   - communityId: 操作者社区语境（>0 时逐项校验分类归属）
//
// 返回值：
//   - error: 参数非法/越权/数据库错误
//
// 使用示例：
//
//	err := svc.BatchSortCategories(ctx, []*product.CategorySortItem{{Id: 3, Sort: 10}, {Id: 1, Sort: 20}}, 456)
func (s *CategoryService) BatchSortCategories(ctx context.Context, items []*product.CategorySortItem, communityId int64) error {
	if len(items) == 0 {
		return bizerr.New(codes.InvalidArgument, "请提供排序数据")
	}
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range items {
			if item.GetId() <= 0 {
				return bizerr.New(codes.InvalidArgument, "分类 ID 非法")
			}
			if _, err := s.GetCategory(ctx, item.GetId(), communityId); err != nil {
				return err
			}
			if err := tx.Model(&model.Category{}).Where("id = ?", item.GetId()).
				Update("sort", item.GetSort()).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ToCategoryInfo 将分类模型转换为 proto 分类信息
// 参数：
//   - c: 分类模型
//
// 返回值：
//   - *product.CategoryInfo: proto 分类信息
func ToCategoryInfo(c *model.Category) *product.CategoryInfo {
	if c == nil {
		return nil
	}
	return &product.CategoryInfo{
		Id:          c.ID,
		CommunityId: c.CommunityID,
		ParentId:    c.ParentID,
		Name:        c.Name,
		Icon:        c.Icon,
		Sort:        c.Sort,
		Status:      c.Status,
		Children:    []*product.CategoryInfo{},
	}
}

// BuildCategoryTree 将扁平分类列表构建为树形结构
// 参数：
//   - list: 扁平分类列表
//
// 返回值：
//   - []*product.CategoryInfo: 树形分类列表
func BuildCategoryTree(list []*model.Category) []*product.CategoryInfo {
	if len(list) == 0 {
		return []*product.CategoryInfo{}
	}
	m := make(map[int64]*product.CategoryInfo, len(list))
	var roots []*product.CategoryInfo
	for _, c := range list {
		info := ToCategoryInfo(c)
		m[c.ID] = info
	}
	for _, c := range list {
		info := m[c.ID]
		if c.ParentID == 0 {
			roots = append(roots, info)
			continue
		}
		if parent, ok := m[c.ParentID]; ok {
			parent.Children = append(parent.Children, info)
		} else {
			roots = append(roots, info)
		}
	}
	return roots
}
