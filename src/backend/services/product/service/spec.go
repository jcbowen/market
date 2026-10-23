package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// specAliveCond 规格表存活记录过滤条件（deleted_at 为 NULL 视为存活）
// 注意：不能用 `deleted_at = ”` 比较 —— datetime 列与空串比较在 strict 模式下报 Error 1292
const specAliveCond = "deleted_at IS NULL"

// ErrBarcodeNotFound 条形码未命中业务错误（对扫码方等价于"未找到商品"）
var ErrBarcodeNotFound = bizerr.New(codes.NotFound, "未找到该条码对应的商品")

// saveSpecsAndSkus 在事务内保存商品的规格树与 SKU 组合，并回写商品汇总字段
// 多规格：先落规格名/规格值（前端临时 ID 映射为真实 ID），再落 SKU 组合，
// diff 软删除本次未提交的记录；单规格：维护唯一一条默认 SKU。
// 最后回写 products 的 min_price / max_price / stock。
//
// 参数：
//   - tx: GORM 事务对象
//   - p: 商品模型（需已包含 ID / CommunityID / IsMultiSpec）
//   - specs: 规格树保存项（多规格时必填，全量提交）
//   - skuItems: SKU 保存项（全量提交，单规格时仅一条且不带规格值）
//
// 返回值：
//   - error: 校验或落库错误信息
func (s *ProductService) saveSpecsAndSkus(tx *gorm.DB, p *model.Product, specs []*product.SpecReq, skuItems []*product.SkuItemReq) error {
	if len(skuItems) == 0 {
		return bizerr.New(codes.InvalidArgument, "请填写商品价格与库存")
	}
	if !p.IsMultiSpec && len(skuItems) > 1 {
		return bizerr.New(codes.InvalidArgument, "单规格商品只允许一个 SKU")
	}

	now := time.Now().Format("2006-01-02 15:04:05")

	// 规格名 ID -> 名称
	specNameMap := make(map[int64]string)
	// 规格值 ID -> 名称 / 所属规格 ID
	valueNameMap := make(map[int64]string)
	valueSpecMap := make(map[int64]int64)
	// 前端临时 ID -> 真实规格值 ID
	tempMap := make(map[string]int64)

	if p.IsMultiSpec {
		keepSpecIDs, keepValueIDs, err := s.saveSpecTree(tx, p, specs, specNameMap, valueNameMap, valueSpecMap, tempMap)
		if err != nil {
			return err
		}
		if err := s.softDeleteSpecs(tx, p.ID, now, keepSpecIDs, keepValueIDs); err != nil {
			return err
		}
	}

	// 落 SKU 并统计汇总
	keepSkuIDs, minPrice, maxPrice, minOriginalPrice, totalStock, err := s.saveSkus(tx, p, skuItems, tempMap, specNameMap, valueNameMap, valueSpecMap)
	if err != nil {
		return err
	}

	// 单规格时，本次未提交的旧规格记录全部软删
	if !p.IsMultiSpec {
		if err := s.softDeleteSpecs(tx, p.ID, now, nil, nil); err != nil {
			return err
		}
	}
	if err := s.softDeleteSkus(tx, p.ID, now, keepSkuIDs); err != nil {
		return err
	}

	// 回写商品汇总字段
	return tx.Model(&model.Product{}).
		Where("id = ?", p.ID).
		Updates(map[string]interface{}{
			"min_price":          minPrice,
			"max_price":          maxPrice,
			"min_original_price": minOriginalPrice,
			"stock":              totalStock,
		}).Error
}

// saveSpecTree 保存规格名与规格值，建立临时 ID 到真实 ID 的映射
// 参数：
//   - tx: GORM 事务对象
//   - p: 商品模型
//   - specs: 规格树保存项
//   - specNameMap: 输出，规格名 ID -> 名称
//   - valueNameMap: 输出，规格值 ID -> 名称
//   - valueSpecMap: 输出，规格值 ID -> 所属规格名 ID
//   - tempMap: 输出，前端临时 ID -> 真实规格值 ID
//
// 返回值：
//   - []int64: 本次保留的规格名 ID 列表
//   - []int64: 本次保留的规格值 ID 列表
//   - error: 校验或落库错误信息
func (s *ProductService) saveSpecTree(
	tx *gorm.DB,
	p *model.Product,
	specs []*product.SpecReq,
	specNameMap, valueNameMap map[int64]string,
	valueSpecMap map[int64]int64,
	tempMap map[string]int64,
) ([]int64, []int64, error) {
	keepSpecIDs := make([]int64, 0, len(specs))
	keepValueIDs := make([]int64, 0)

	for i, sp := range specs {
		if sp.Name == "" {
			return nil, nil, bizerr.New(codes.InvalidArgument, fmt.Sprintf("第 %d 个规格名不能为空", i+1))
		}
		if len(sp.Values) == 0 {
			return nil, nil, bizerr.New(codes.InvalidArgument, fmt.Sprintf("规格「%s」至少需要一个规格值", sp.Name))
		}

		var specID int64
		if sp.Id > 0 {
			specID = sp.Id
			if err := tx.Model(&model.ProductSpec{}).
				Where("id = ? AND product_id = ?", sp.Id, p.ID).
				Updates(map[string]interface{}{"name": sp.Name, "sort": sp.Sort, "deleted_at": nil}).
				Error; err != nil {
				return nil, nil, err
			}
		} else {
			row := &model.ProductSpec{
				ProductID:   p.ID,
				CommunityID: p.CommunityID,
				Name:        sp.Name,
				Sort:        sp.Sort,
			}
			if err := tx.Create(row).Error; err != nil {
				return nil, nil, err
			}
			specID = row.ID
		}
		keepSpecIDs = append(keepSpecIDs, specID)
		specNameMap[specID] = sp.Name

		for _, v := range sp.Values {
			if v.Name == "" {
				return nil, nil, bizerr.New(codes.InvalidArgument, fmt.Sprintf("规格「%s」的规格值不能为空", sp.Name))
			}

			var valueID int64
			if v.Id > 0 {
				valueID = v.Id
				if err := tx.Model(&model.ProductSpecValue{}).
					Where("id = ? AND product_id = ?", v.Id, p.ID).
					Updates(map[string]interface{}{
						"spec_id":    specID,
						"name":       v.Name,
						"sort":       v.Sort,
						"deleted_at": nil,
					}).Error; err != nil {
					return nil, nil, err
				}
			} else {
				row := &model.ProductSpecValue{
					SpecID:      specID,
					ProductID:   p.ID,
					CommunityID: p.CommunityID,
					Name:        v.Name,
					Sort:        v.Sort,
				}
				if err := tx.Create(row).Error; err != nil {
					return nil, nil, err
				}
				valueID = row.ID
			}
			if v.TempId != "" {
				tempMap[v.TempId] = valueID
			}
			keepValueIDs = append(keepValueIDs, valueID)
			valueNameMap[valueID] = v.Name
			valueSpecMap[valueID] = specID
		}
	}
	return keepSpecIDs, keepValueIDs, nil
}

// saveSkus 保存 SKU 组合并统计价格区间与汇总库存
// 逐条写入是有意的、不做整批插入：每行新建后需立即用其 ID 建初始批次（createInitBatch），
// 批量插入只能靠切片顺序反查 ID，映射易错而 SKU 本就只有数个到数十个，收益低于回归风险
// 参数：
//   - tx: GORM 事务对象
//   - p: 商品模型
//   - skuItems: SKU 保存项
//   - tempMap: 前端临时 ID -> 真实规格值 ID
//   - specNameMap: 规格名 ID -> 名称
//   - valueNameMap: 规格值 ID -> 名称
//   - valueSpecMap: 规格值 ID -> 所属规格名 ID
//
// 返回值：
//   - []int64: 本次保留的 SKU ID 列表
//   - float64: 最低价
//   - float64: 最高价
//   - float64: 划线价（最低价 SKU 的划线价，0 表示未配置）
//   - int32: 汇总库存
//   - error: 校验或落库错误信息
func (s *ProductService) saveSkus(
	tx *gorm.DB,
	p *model.Product,
	skuItems []*product.SkuItemReq,
	tempMap map[string]int64,
	specNameMap, valueNameMap map[int64]string,
	valueSpecMap map[int64]int64,
) ([]int64, float64, float64, float64, int32, error) {
	var (
		keepSkuIDs       = make([]int64, 0, len(skuItems))
		minPrice         = 0.0
		maxPrice         = 0.0
		minOriginalPrice = 0.0
		totalStock       = int32(0)
		seenCombo        = make(map[string]bool)
		seenBarcode      = make(map[string]bool)
		defaultSkuID     = int64(0)
	)

	// 单规格：定位现有默认 SKU（spec_value_ids 为空/NULL 且启用），避免重复创建
	// 无论前端是否回传 SKU ID 都先查询，保证二次保存复用记录而非新建
	if !p.IsMultiSpec {
		var existing model.Sku
		err := tx.Where("product_id = ? AND (spec_value_ids = '' OR spec_value_ids IS NULL) AND status = 1", p.ID).
			Order("id asc").First(&existing).Error
		if err == nil {
			defaultSkuID = existing.ID
		}
	}

	for i, item := range skuItems {
		if item.Price <= 0 {
			return nil, 0, 0, 0, 0, bizerr.New(codes.InvalidArgument, fmt.Sprintf("第 %d 个 SKU 的价格必须大于 0", i+1))
		}
		if item.Stock < 0 {
			return nil, 0, 0, 0, 0, bizerr.New(codes.InvalidArgument, fmt.Sprintf("第 %d 个 SKU 的库存不能为负数", i+1))
		}

		// 解析规格值 ID：临时 ID 替换为真实 ID
		resolved := make([]int64, 0, len(item.SpecValueIds))
		for _, raw := range item.SpecValueIds {
			id, err := resolveSpecValueID(raw, tempMap, valueNameMap)
			if err != nil {
				return nil, 0, 0, 0, 0, fmt.Errorf("第 %d 个 SKU：%w", i+1, err)
			}
			resolved = append(resolved, id)
		}
		sort.Slice(resolved, func(a, b int) bool { return resolved[a] < resolved[b] })

		// 组合键唯一性校验
		comboKey := joinSpecValueIDs(resolved)
		if comboKey != "" {
			if seenCombo[comboKey] {
				return nil, 0, 0, 0, 0, bizerr.New(codes.InvalidArgument, fmt.Sprintf("SKU 规格组合重复: %s", skuDisplayName(resolved, valueNameMap)))
			}
			seenCombo[comboKey] = true
		}

		// 条形码：归一化 + 格式校验 + 本次提交内去重 + 社区内跨商品唯一校验
		// 条码类业务校验统一走 bizerr（InvalidArgument），包装用 %w 保留错误链——
		// 纯 fmt.Errorf 会被 ToStatus 归为 Internal，网关侧吞成"服务器内部错误"
		barcode := strings.TrimSpace(item.Barcode)
		if barcode != "" {
			if err := validateBarcode(barcode); err != nil {
				return nil, 0, 0, 0, 0, fmt.Errorf("第 %d 个 SKU：%w", i+1, err)
			}
			if seenBarcode[barcode] {
				return nil, 0, 0, 0, 0, fmt.Errorf("第 %d 个 SKU：%w", i+1,
					bizerr.New(codes.InvalidArgument, fmt.Sprintf("条形码 %s 在本商品内重复", barcode)))
			}
			seenBarcode[barcode] = true
			if err := s.ensureBarcodeAvailable(tx, p.CommunityID, p.ID, barcode); err != nil {
				return nil, 0, 0, 0, 0, fmt.Errorf("第 %d 个 SKU：%w", i+1, err)
			}
		}

		// 规格展示信息
		specName, specJSON := buildSkuSpecInfo(resolved, specNameMap, valueNameMap, valueSpecMap)

		// 解析 SKU ID：优先用前端回传 ID；单规格未回传时复用已存在的默认 SKU
		skuID := item.Id
		if !p.IsMultiSpec && skuID == 0 {
			skuID = defaultSkuID
		}

		// SKU 编码：前端显式传入时使用；更新已有 SKU 且未传入时保留原编码不覆盖；
		// 新建且未传入时自动生成（随机后缀避免与历史/禁用记录撞唯一索引）
		skuCode := item.SkuCode
		if skuCode == "" && skuID <= 0 {
			skuCode = fmt.Sprintf("P%d-%s", p.ID, uuid.NewString()[:8])
		}

		// 更新字段集（sku_code 为空时不纳入更新，即保留原编码）
		updates := map[string]interface{}{
			"spec_value_ids": comboKey,
			"spec_name":      specName,
			"spec_json":      specJSON,
			"price":          item.Price,
			"original_price": item.OriginalPrice,
			"cost_price":     item.CostPrice,
			"stock":          item.Stock,
			"safety_stock":   item.SafetyStock,
			"image":          item.Image,
			"barcode":        barcode,
			"status":         1,
		}
		if skuCode != "" {
			updates["sku_code"] = skuCode
		}

		if skuID > 0 {
			// 校验 SKU 归属，防止误传其他商品的 ID 导致本商品 SKU 被全部禁用；
			// 同时读取原库存/成本，用于库存增减时同步批次（批次是扣减唯一来源）
			var old model.Sku
			if err := tx.Select("id", "stock", "cost_price", "sku_code", "community_id").
				Where("id = ? AND product_id = ?", skuID, p.ID).
				Take(&old).Error; err != nil {
				return nil, 0, 0, 0, 0, bizerr.New(codes.NotFound, fmt.Sprintf("第 %d 个 SKU（id=%d）不存在或不属于该商品", i+1, skuID))
			}
			if err := tx.Model(&model.Sku{}).
				Where("id = ? AND product_id = ?", skuID, p.ID).
				Updates(updates).Error; err != nil {
				return nil, 0, 0, 0, 0, err
			}
			// 表单直接改汇总库存：调大补「表单调整」批次，调小按 FEFO 扣回批次，
			// 保证 product_skus.stock 恒等于 Σ 在库批次 quantity（否则后续扣减必失败）
			if err := syncSkuStockBatches(tx, p.CommunityID, &old, item.Stock); err != nil {
				return nil, 0, 0, 0, 0, fmt.Errorf("第 %d 个 SKU：%w", i+1, err)
			}
		} else {
			row := &model.Sku{
				ProductID:     p.ID,
				CommunityID:   p.CommunityID,
				SkuCode:       skuCode,
				SpecValueIDs:  comboKey,
				SpecName:      specName,
				SpecJSON:      specJSON,
				Price:         item.Price,
				OriginalPrice: item.OriginalPrice,
				CostPrice:     item.CostPrice,
				Stock:         item.Stock,
				SafetyStock:   item.SafetyStock,
				Image:         item.Image,
				Barcode:       barcode,
				Status:        1,
			}
			if err := tx.Create(row).Error; err != nil {
				return nil, 0, 0, 0, 0, err
			}
			skuID = row.ID
			// 建品即带初始库存：生成初始批次，保证汇总库存可被 FEFO 扣减
			if item.Stock > 0 {
				now := time.Now().Format("2006-01-02 15:04:05")
				if _, err := createInitBatch(tx, p.CommunityID, skuID, item.Stock, batchUnitCost(item.CostPrice, 0), "INIT", now); err != nil {
					return nil, 0, 0, 0, 0, err
				}
			}
		}

		keepSkuIDs = append(keepSkuIDs, skuID)
		// 划线价取最低价 SKU 的划线价，与列表展示口径（price=min_price）保持同一规格
		if minPrice == 0 || item.Price < minPrice {
			minPrice = item.Price
			minOriginalPrice = item.OriginalPrice
		}
		if item.Price > maxPrice {
			maxPrice = item.Price
		}
		totalStock += item.Stock
	}
	return keepSkuIDs, minPrice, maxPrice, minOriginalPrice, totalStock, nil
}

// batchUnitCost 批次单位成本取值：表单未填成本（<=0）时回落既有成本，
// 避免 0 成本批次稀释移动加权平均成本
// 参数：
//   - formCost: 表单提交的成本价
//   - currentCost: SKU 当前成本价（改动前）
//
// 返回值：
//   - float64: 批次单位成本
func batchUnitCost(formCost, currentCost float64) float64 {
	if formCost > 0 {
		return formCost
	}
	return currentCost
}

// createInitBatch 生成来源为「表单/初始」的批次（SourceType=1）
// 参数：
//   - tx: GORM 事务对象
//   - communityID: 社区 ID
//   - skuID: SKU ID
//   - quantity: 批次数量
//   - unitCost: 单位成本
//   - refNo: 来源单号（如 INIT）
//   - now: 当前时间串
//
// 返回值：
//   - int64: 生成的批次 ID
//   - error: 落库错误信息
func createInitBatch(tx *gorm.DB, communityID, skuID int64, quantity int32, unitCost float64, refNo, now string) (int64, error) {
	batch := &model.StockBatch{
		CommunityID: communityID,
		SkuID:       skuID,
		Quantity:    quantity,
		UnitCost:    unitCost,
		Status:      1,
		SourceType:  1,
		RefNo:       refNo,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := tx.Create(batch).Error; err != nil {
		return 0, err
	}
	return batch.ID, nil
}

// syncSkuStockBatches 将商品表单的汇总库存改动同步到批次库存并落库存流水
// 调大库存：补一条「表单调整」批次（SourceType=1）；
// 调小库存：按 FEFO 从既有批次扣回，批次不足时拒绝调低（提示走出库单/盘点调整），
// 防止汇总库存大于批次合计导致后续扣减失败。
// 同步落 stock_records 流水（ref_no=FORM，与销售出库的空单号流水区分，
// 避免毛利报表把表单调低误算为销售），保证进销存报表口径完整。
// 参数：
//   - tx: GORM 事务对象
//   - communityID: 社区 ID
//   - old: 改动前的 SKU（含原库存与成本）
//   - newStock: 表单提交的新汇总库存
//
// 返回值：
//   - error: 扣减失败（批次不足）或落库错误
func syncSkuStockBatches(tx *gorm.DB, communityID int64, old *model.Sku, newStock int32) error {
	delta := newStock - old.Stock
	if delta == 0 {
		return nil
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	recordType := int32(1)
	batchID := int64(0)
	if delta > 0 {
		// 调大库存：补「表单调整」批次
		var err error
		if batchID, err = createInitBatch(tx, communityID, old.ID, delta, batchUnitCost(0, old.CostPrice), "INIT", now); err != nil {
			return err
		}
	} else {
		// 调小库存：按 FEFO 扣回批次（扣减数量为正）
		sku := &model.Sku{ID: old.ID, SkuCode: old.SkuCode, Stock: old.Stock}
		takes, err := deductBatches(tx, sku, -delta, now)
		if err != nil {
			return fmt.Errorf("库存需调低 %d，批次扣减失败（可改通过出库单或盘点调整库存）：%w", -delta, err)
		}
		if len(takes) > 0 {
			batchID = takes[0].Batch.ID
		}
		recordType = 2
	}
	record := &model.StockRecord{
		SkuID:       old.ID,
		CommunityID: communityID,
		Type:        recordType,
		Quantity:    delta,
		BeforeStock: old.Stock,
		AfterStock:  newStock,
		BatchID:     batchID,
		RefNo:       "FORM",
		Remark:      "商品表单调整库存",
		CreatedAt:   now,
	}
	return tx.Create(record).Error
}

// softDeleteSpecs 软删除商品下未保留的规格名与规格值
// 保留列表为空时，删除该商品下全部存活规格记录（单规格切换场景）
//
// 参数：
//   - tx: GORM 事务对象
//   - productID: 商品 ID
//   - now: 软删除时间
//   - keepSpecIDs: 保留的规格名 ID 列表
//   - keepValueIDs: 保留的规格值 ID 列表
//
// 返回值：
//   - error: 落库错误信息
func (s *ProductService) softDeleteSpecs(tx *gorm.DB, productID int64, now string, keepSpecIDs, keepValueIDs []int64) error {
	if err := deleteNotIn(tx, &model.ProductSpec{}, productID, keepSpecIDs, now); err != nil {
		return err
	}
	return deleteNotIn(tx, &model.ProductSpecValue{}, productID, keepValueIDs, now)
}

// softDeleteSkus 禁用商品下未保留的 SKU（Sku 无软删字段，使用 status=0 禁用）
// 参数：
//   - tx: GORM 事务对象
//   - productID: 商品 ID
//   - now: 时间（预留，当前未使用）
//   - keepSkuIDs: 保留的 SKU ID 列表
//
// 返回值：
//   - error: 落库错误信息
func (s *ProductService) softDeleteSkus(tx *gorm.DB, productID int64, now string, keepSkuIDs []int64) error {
	db := tx.Where("product_id = ? AND status = 1", productID)
	if len(keepSkuIDs) > 0 {
		db = db.Where("id NOT IN ?", keepSkuIDs)
	}
	return db.Model(&model.Sku{}).Update("status", 0).Error
}

// deleteNotIn 软删除表中未保留的记录（deleted_at 置为当前时间）
// 参数：
//   - tx: GORM 事务对象
//   - dest: 目标模型
//   - productID: 商品 ID
//   - keepIDs: 保留的记录 ID 列表
//   - now: 软删除时间
//
// 返回值：
//   - error: 落库错误信息
func deleteNotIn(tx *gorm.DB, dest interface{}, productID int64, keepIDs []int64, now string) error {
	db := tx.Where("product_id = ? AND "+specAliveCond, productID)
	if len(keepIDs) > 0 {
		db = db.Where("id NOT IN ?", keepIDs)
	}
	return db.Model(dest).Update("deleted_at", now).Error
}

// resolveSpecValueID 解析规格值 ID：临时 ID 查映射表，否则按数字解析
// 参数：
//   - raw: 原始 ID 字符串（真实数字 ID 或前端临时 ID）
//   - tempMap: 前端临时 ID -> 真实规格值 ID
//   - valueNameMap: 规格值 ID -> 名称（用于校验真实 ID 存在）
//
// 返回值：
//   - int64: 真实规格值 ID
//   - error: 无法解析或规格值不存在时的错误信息
func resolveSpecValueID(raw string, tempMap map[string]int64, valueNameMap map[int64]string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, bizerr.New(codes.InvalidArgument, "规格值 ID 不能为空")
	}
	if id, ok := tempMap[raw]; ok {
		return id, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, bizerr.New(codes.InvalidArgument, fmt.Sprintf("无法识别的规格值引用: %s", raw))
	}
	if _, ok := valueNameMap[id]; !ok {
		return 0, bizerr.New(codes.NotFound, fmt.Sprintf("规格值 %d 不存在于本次提交的规格树中", id))
	}
	return id, nil
}

// joinSpecValueIDs 将规格值 ID 列表转为逗号分隔字符串（组合键），空列表返回空串
// 参数：
//   - ids: 规格值 ID 列表（需已排序）
//
// 返回值：
//   - string: 逗号分隔的组合键
func joinSpecValueIDs(ids []int64) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

// buildSkuSpecInfo 生成 SKU 的规格展示名称与规格 JSON
// 多规格：specName 形如 "红色 / L"，specJSON 形如 {"颜色":"红色","尺寸":"L"}；
// 单规格：两者均为空/空对象。
//
// 参数：
//   - ids: 解析后的规格值 ID 列表
//   - specNameMap: 规格名 ID -> 名称
//   - valueNameMap: 规格值 ID -> 名称
//   - valueSpecMap: 规格值 ID -> 所属规格名 ID
//
// 返回值：
//   - string: 规格展示名称
//   - string: 规格 JSON 字符串
func buildSkuSpecInfo(ids []int64, specNameMap, valueNameMap map[int64]string, valueSpecMap map[int64]int64) (string, string) {
	if len(ids) == 0 {
		return "", "{}"
	}

	parts := make([]string, 0, len(ids))
	pairs := make(map[string]string, len(ids))
	for _, id := range ids {
		valueName := valueNameMap[id]
		parts = append(parts, valueName)
		pairs[specNameMap[valueSpecMap[id]]] = valueName
	}
	specJSON, _ := json.Marshal(pairs)
	return strings.Join(parts, " / "), string(specJSON)
}

// validateBarcode 条形码宽松格式校验：仅允许纯数字，长度 6-20 位
// 覆盖 EAN-8 / UPC-E(8) / UPC-A(12) / EAN-13(13) / ITF-14(14) 等常见码制；
// 不做校验位强校验，避免临期称重码等特殊码被误拦
//
// 参数：
//   - barcode: 已去空格的条形码
//
// 返回值：
//   - error: 格式不合法时的错误信息
func validateBarcode(barcode string) error {
	if len(barcode) < 6 || len(barcode) > 20 {
		return bizerr.New(codes.InvalidArgument, "条形码长度需在 6-20 位之间")
	}
	for _, r := range barcode {
		if r < '0' || r > '9' {
			return bizerr.New(codes.InvalidArgument, "条形码仅支持数字（EAN/UPC 码制）")
		}
	}
	return nil
}

// ensureBarcodeAvailable 校验条形码在本社区内未被其他在售商品的 SKU 占用
// 仅校验启用（status=1）的 SKU：被移除规格的条码可回收复用；
// 排除本商品自身（本商品内重复已在提交循环内校验，且本商品其他 SKU 若未保留将在同事务内被禁用）
//
// 参数：
//   - tx: GORM 事务对象
//   - communityID: 社区 ID
//   - excludeProductID: 排除的商品 ID（当前保存的商品）
//   - barcode: 已归一化的条形码
//
// 返回值：
//   - error: 条码被占用时的错误信息（含占用商品名）
func (s *ProductService) ensureBarcodeAvailable(tx *gorm.DB, communityID, excludeProductID int64, barcode string) error {
	var sku model.Sku
	// 仅统计未删除商品（status != 2）的 SKU：商品软删后其条码自动释放可复用
	err := tx.Where("community_id = ? AND barcode = ? AND status = 1 AND product_id != ?"+
		" AND product_id IN (?)", communityID, barcode, excludeProductID,
		tx.Model(&model.Product{}).Select("id").Where("status != ?", 2)).
		Order("id asc").First(&sku).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	var p model.Product
	if err := tx.Select("name").Where("id = ?", sku.ProductID).First(&p).Error; err != nil {
		return bizerr.New(codes.InvalidArgument, fmt.Sprintf("条形码 %s 已被其他商品占用", barcode))
	}
	return bizerr.New(codes.InvalidArgument, fmt.Sprintf("条形码 %s 已被商品「%s」使用", barcode, p.Name))
}

// GetByBarcode 按条形码精确查询商品与命中的 SKU（社区隔离，仅上架商品与启用 SKU）
//
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//   - barcode: 条形码（调用方需已去空格）
//
// 返回值：
//   - *model.Product: 命中的商品；未命中返回 ErrBarcodeNotFound
//   - *model.Sku: 命中的 SKU（多规格商品为具体规格）
//   - error: 未命中或查询错误
func (s *ProductService) GetByBarcode(ctx context.Context, communityID int64, barcode string) (*model.Product, *model.Sku, error) {
	var sku model.Sku
	err := s.db.GetDb().WithContext(ctx).
		Where("community_id = ? AND barcode = ? AND status = 1", communityID, barcode).
		Order("id asc").First(&sku).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrBarcodeNotFound
		}
		return nil, nil, err
	}

	var p model.Product
	if err := s.db.GetDb().WithContext(ctx).
		Where("id = ? AND status = 1", sku.ProductID).
		First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// SKU 存在但商品已下架/删除：对扫码方等价于未命中
			return nil, nil, ErrBarcodeNotFound
		}
		return nil, nil, err
	}
	return &p, &sku, nil
}

// skuDisplayName 将规格值 ID 列表转为可读名称（用于错误提示）
// 参数：
//   - ids: 规格值 ID 列表
//   - valueNameMap: 规格值 ID -> 名称
//
// 返回值：
//   - string: 形如 "红色 / L" 的展示名称
func skuDisplayName(ids []int64, valueNameMap map[int64]string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, valueNameMap[id])
	}
	return strings.Join(parts, " / ")
}

// ListSpecs 查询商品规格树（规格名 + 层级规格值）
// 参数：
//   - ctx: 上下文
//   - productID: 商品 ID
//
// 返回值：
//   - []*product.SpecInfo: 规格树（无规格时返回空切片）
//   - error: 查询错误信息
func (s *ProductService) ListSpecs(ctx context.Context, productID int64) ([]*product.SpecInfo, error) {
	var specs []model.ProductSpec
	if err := s.db.GetDb().WithContext(ctx).
		Where("product_id = ? AND "+specAliveCond, productID).
		Order("sort asc, id asc").
		Find(&specs).Error; err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return []*product.SpecInfo{}, nil
	}

	var values []model.ProductSpecValue
	if err := s.db.GetDb().WithContext(ctx).
		Where("product_id = ? AND "+specAliveCond, productID).
		Order("sort asc, id asc").
		Find(&values).Error; err != nil {
		return nil, err
	}

	valuesBySpec := make(map[int64][]*product.SpecValueInfo)
	for _, v := range values {
		valuesBySpec[v.SpecID] = append(valuesBySpec[v.SpecID], &product.SpecValueInfo{
			Id:   v.ID,
			Name: v.Name,
			Sort: v.Sort,
		})
	}

	list := make([]*product.SpecInfo, 0, len(specs))
	for _, sp := range specs {
		list = append(list, &product.SpecInfo{
			Id:     sp.ID,
			Name:   sp.Name,
			Sort:   sp.Sort,
			Values: valuesBySpec[sp.ID],
		})
	}
	return list, nil
}
