package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// SkuService SKU 业务服务
type SkuService struct {
	db *mysql.Instance
}

// NewSkuService 创建 SKU 业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *SkuService: SKU 业务服务实例
func NewSkuService(db *mysql.Instance) *SkuService {
	return &SkuService{db: db}
}

// ListSkus 根据商品 ID 查询 SKU 列表
// 参数：
//   - ctx: 上下文
//   - productID: 商品 ID
//
// 返回值：
//   - []*model.Sku: SKU 列表
//   - error: 错误信息
func (s *SkuService) ListSkus(ctx context.Context, productID int64) ([]*model.Sku, error) {
	var list []*model.Sku
	err := s.db.GetDb().WithContext(ctx).
		Where("product_id = ?", productID).
		Where("status = ?", 1).
		Order("id asc").
		Find(&list).Error
	return list, err
}

// GetSkuByID 根据 ID 查询 SKU
// 参数：
//   - ctx: 上下文
//   - id: SKU ID
//
// 返回值：
//   - *model.Sku: SKU 模型
//   - error: 错误信息
func (s *SkuService) GetSkuByID(ctx context.Context, id int64) (*model.Sku, error) {
	var sku model.Sku
	err := s.db.GetDb().WithContext(ctx).First(&sku, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, bizerr.New(codes.NotFound, "SKU 不存在")
	}
	return &sku, err
}

// BatchGetSkus 按 ID 批量查询 SKU（下单/收银按明细取价取库存，替代逐行 GetSkuByID 的 N 次往返）
// 任一 ID 不存在或不属于操作者社区即整体报错：订单明细不允许部分命中，
// 缺项继续算价会造成金额与库存双口径偏差
//
// 参数：
//   - ctx: 上下文
//   - ids: SKU ID 列表（重复 ID 只查一次，返回值按首次出现顺序去重）
//   - communityID: 操作者社区语境，<=0 跳过归属校验
//
// 返回值：
//   - []*model.Sku: SKU 模型列表
//   - error: 参数非法/不存在/越权/数据库错误
//
// 使用示例：
//
//	skus, err := svc.BatchGetSkus(ctx, []int64{1, 2, 3}, 456)
func (s *SkuService) BatchGetSkus(ctx context.Context, ids []int64, communityID int64) ([]*model.Sku, error) {
	ordered := dedupIDs(ids)
	if len(ordered) == 0 {
		return nil, bizerr.New(codes.InvalidArgument, "请选择要查询的 SKU")
	}
	var rows []*model.Sku
	if err := s.db.GetDb().WithContext(ctx).Find(&rows, ordered).Error; err != nil {
		return nil, err
	}
	byID := make(map[int64]*model.Sku, len(rows))
	for _, sku := range rows {
		if err := EnsureScope(communityID, sku.CommunityID, "SKU"); err != nil {
			return nil, err
		}
		byID[sku.ID] = sku
	}
	list := make([]*model.Sku, 0, len(ordered))
	for _, id := range ordered {
		sku, ok := byID[id]
		if !ok {
			return nil, bizerr.New(codes.NotFound, fmt.Sprintf("SKU %d 不存在", id))
		}
		list = append(list, sku)
	}
	return list, nil
}

// FindSkusByIDs 按 ID 集合批量取 SKU，返回 ID->SKU 映射；缺失的 ID 直接不出现在结果里
// 与 BatchGetSkus 的差别：本方法用于「取到就填充、取不到就留空」的列表回填，
// 个别 SKU 被删除不应让整页报错；因此也不做社区越权校验，
// ID 必须来自服务端已过滤的数据（如秒杀商品项表），不可直接透传客户端入参。
//
// 参数：
//   - ctx: 上下文
//   - ids: SKU ID 集合（重复、0 与负数自动忽略）
//
// 返回值：
//   - map[int64]*model.Sku: SKU ID -> SKU 模型
//   - error: 数据库错误
//
// 使用示例：
//
//	skuByID, err := svc.FindSkusByIDs(ctx, []int64{1, 2})
func (s *SkuService) FindSkusByIDs(ctx context.Context, ids []int64) (map[int64]*model.Sku, error) {
	positive := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			positive = append(positive, id)
		}
	}
	if len(positive) == 0 {
		return map[int64]*model.Sku{}, nil
	}
	var rows []*model.Sku
	if err := s.db.GetDb().WithContext(ctx).Find(&rows, dedupIDs(positive)).Error; err != nil {
		return nil, err
	}
	byID := make(map[int64]*model.Sku, len(rows))
	for _, sku := range rows {
		byID[sku.ID] = sku
	}
	return byID, nil
}

// dedupIDs 去重并保持首次出现顺序
//
// 参数：
//   - ids: 原始 ID 列表
//
// 返回值：
//   - []int64: 按首次出现顺序排列的唯一 ID
func dedupIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	ordered := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ordered = append(ordered, id)
	}
	return ordered
}

// CreateSku 创建 SKU
// 参数：
//   - ctx: 上下文
//   - req: 创建请求
//
// 返回值：
//   - *model.Sku: SKU 模型
//   - error: 错误信息
func (s *SkuService) CreateSku(ctx context.Context, req *product.CreateSkuReq) (*model.Sku, error) {
	// spec_json 为 JSON 列，空字符串非法，归一化为空对象
	specJSON := req.SpecJson
	if specJSON == "" {
		specJSON = "{}"
	}
	sku := &model.Sku{
		ProductID:   req.ProductId,
		CommunityID: req.CommunityId,
		SkuCode:     req.SkuCode,
		SpecName:    req.SpecName,
		SpecJSON:    specJSON,
		Price:       req.Price,
		CostPrice:   req.CostPrice,
		Stock:       req.Stock,
		SafetyStock: req.SafetyStock,
		Image:       req.Image,
		Barcode:     strings.TrimSpace(req.Barcode),
		Status:      1,
	}
	// 建品与初始批次同事务：批次库存是扣减的唯一来源，汇总库存必须有对应批次，
	// 批次落库失败时连同 SKU 一并回滚，避免产生"有汇总库存无批次"的不可售 SKU
	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(sku).Error; err != nil {
			return err
		}
		if req.Stock > 0 {
			now := time.Now().Format("2006-01-02 15:04:05")
			if _, err := createInitBatch(tx, req.CommunityId, sku.ID, req.Stock, req.CostPrice, "INIT", now); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return sku, nil
}

// UpdateSku 更新 SKU
// 参数：
//   - ctx: 上下文
//   - req: 更新请求
//
// 返回值：
//   - *model.Sku: SKU 模型
//   - error: 错误信息
func (s *SkuService) UpdateSku(ctx context.Context, req *product.UpdateSkuReq) (*model.Sku, error) {
	sku, err := s.GetSkuByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	// 社区隔离：本接口可改售价/成本价，跨社区改动即他社区资损，须强校验
	if err := EnsureScope(req.CommunityId, sku.CommunityID, "SKU"); err != nil {
		return nil, err
	}

	if req.SpecName != "" {
		sku.SpecName = req.SpecName
	}
	if req.SpecJson != "" {
		sku.SpecJSON = req.SpecJson
	}
	if req.Price > 0 {
		sku.Price = req.Price
	}
	if req.CostPrice > 0 {
		sku.CostPrice = req.CostPrice
	}
	if req.SafetyStock > 0 {
		sku.SafetyStock = req.SafetyStock
	}
	if req.Image != "" {
		sku.Image = req.Image
	}
	if req.Status != 0 {
		sku.Status = req.Status
	}
	if req.Barcode != "" {
		sku.Barcode = strings.TrimSpace(req.Barcode)
	}

	if err := s.db.GetDb().WithContext(ctx).Save(sku).Error; err != nil {
		return nil, err
	}
	return sku, nil
}

// SkuWithProduct SKU + 冗余商品名（SearchSkus join 查询结果载体）
type SkuWithProduct struct {
	model.Sku
	ProductName string `gorm:"column:product_name" json:"product_name"`
}

// SearchSkus 社区内 SKU 级搜索（join products）
// keyword 匹配商品名 / SKU 编码 / 条形码（LIKE 模糊）；keyword 为空时返回全量（分页）。
// 仅含启用 SKU 与未删除商品（products.status=2 为软删）。
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//   - keyword: 关键字（可为空）
//   - page: 页码（从 1 开始）
//   - pageSize: 每页条数
//
// 返回值：
//   - []*SkuWithProduct: SKU 列表（含商品名）
//   - int64: 总数
//   - error: 错误信息
func (s *SkuService) SearchSkus(ctx context.Context, communityID int64, keyword string, page, pageSize int) ([]*SkuWithProduct, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}

	query := s.db.GetDb().WithContext(ctx).
		Table("product_skus AS s").
		Joins("JOIN products AS p ON p.id = s.product_id AND p.status <> 2").
		Where("s.community_id = ?", communityID).
		Where("s.status = ?", 1)
	if kw := strings.TrimSpace(keyword); kw != "" {
		like := "%" + kw + "%"
		query = query.Where("(p.name LIKE ? OR s.sku_code LIKE ? OR s.barcode LIKE ?)", like, like, like)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []*SkuWithProduct
	err := query.
		Select("s.*, p.name AS product_name").
		Order("p.id DESC, s.id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Scan(&list).Error
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ToSkuInfo 将 SKU 模型转换为 proto SKU 信息
// 参数：
//   - sku: SKU 模型
//
// 返回值：
//   - *product.SkuInfo: proto SKU 信息
func ToSkuInfo(sku *model.Sku) *product.SkuInfo {
	if sku == nil {
		return nil
	}
	return &product.SkuInfo{
		Id:            sku.ID,
		ProductId:     sku.ProductID,
		CommunityId:   sku.CommunityID,
		SkuCode:       sku.SkuCode,
		SpecName:      sku.SpecName,
		SpecJson:      sku.SpecJSON,
		Price:         sku.Price,
		OriginalPrice: sku.OriginalPrice,
		CostPrice:     sku.CostPrice,
		Stock:         sku.Stock,
		SafetyStock:   sku.SafetyStock,
		Image:         sku.Image,
		Status:        sku.Status,
		SpecValueIds:  sku.SpecValueIDs,
		Barcode:       sku.Barcode,
	}
}
