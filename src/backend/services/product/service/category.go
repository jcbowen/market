package service

import (
	"context"
	"fmt"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

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
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//
// 返回值：
//   - []*model.Category: 分类列表
//   - error: 错误信息
func (s *CategoryService) ListCategories(ctx context.Context, communityID int64) ([]*model.Category, error) {
	var list []*model.Category
	db := s.db.GetDb().WithContext(ctx).Where("status = ?", 1)
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
		return nil, fmt.Errorf("分类不存在")
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
	if req.Status != 0 {
		c.Status = req.Status
	}
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
		return fmt.Errorf("存在子分类，无法删除")
	}
	return s.db.GetDb().WithContext(ctx).Delete(&model.Category{}, id).Error
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
