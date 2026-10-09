package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// specAliveCond 规格表存活记录过滤条件（deleted_at 为 NULL 视为存活）
// 注意：不能用 `deleted_at = ''` 比较 —— datetime 列与空串比较在 strict 模式下报 Error 1292
const specAliveCond = "deleted_at IS NULL"

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
		return fmt.Errorf("请填写商品价格与库存")
	}
	if !p.IsMultiSpec && len(skuItems) > 1 {
		return fmt.Errorf("单规格商品只允许一个 SKU")
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
	keepSkuIDs, minPrice, maxPrice, totalStock, err := s.saveSkus(tx, p, skuItems, tempMap, specNameMap, valueNameMap, valueSpecMap)
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
			"min_price": minPrice,
			"max_price": maxPrice,
			"stock":     totalStock,
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
			return nil, nil, fmt.Errorf("第 %d 个规格名不能为空", i+1)
		}
		if len(sp.Values) == 0 {
			return nil, nil, fmt.Errorf("规格「%s」至少需要一个规格值", sp.Name)
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
				return nil, nil, fmt.Errorf("规格「%s」的规格值不能为空", sp.Name)
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
//   - int32: 汇总库存
//   - error: 校验或落库错误信息
func (s *ProductService) saveSkus(
	tx *gorm.DB,
	p *model.Product,
	skuItems []*product.SkuItemReq,
	tempMap map[string]int64,
	specNameMap, valueNameMap map[int64]string,
	valueSpecMap map[int64]int64,
) ([]int64, float64, float64, int32, error) {
	var (
		keepSkuIDs   = make([]int64, 0, len(skuItems))
		minPrice     = 0.0
		maxPrice     = 0.0
		totalStock   = int32(0)
		seenCombo    = make(map[string]bool)
		defaultSkuID = int64(0)
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
			return nil, 0, 0, 0, fmt.Errorf("第 %d 个 SKU 的价格必须大于 0", i+1)
		}
		if item.Stock < 0 {
			return nil, 0, 0, 0, fmt.Errorf("第 %d 个 SKU 的库存不能为负数", i+1)
		}

		// 解析规格值 ID：临时 ID 替换为真实 ID
		resolved := make([]int64, 0, len(item.SpecValueIds))
		for _, raw := range item.SpecValueIds {
			id, err := resolveSpecValueID(raw, tempMap, valueNameMap)
			if err != nil {
				return nil, 0, 0, 0, fmt.Errorf("第 %d 个 SKU：%v", i+1, err)
			}
			resolved = append(resolved, id)
		}
		sort.Slice(resolved, func(a, b int) bool { return resolved[a] < resolved[b] })

		// 组合键唯一性校验
		comboKey := joinSpecValueIDs(resolved)
		if comboKey != "" {
			if seenCombo[comboKey] {
				return nil, 0, 0, 0, fmt.Errorf("SKU 规格组合重复: %s", skuDisplayName(resolved, valueNameMap))
			}
			seenCombo[comboKey] = true
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
			"cost_price":     item.CostPrice,
			"stock":          item.Stock,
			"safety_stock":   item.SafetyStock,
			"image":          item.Image,
			"status":         1,
		}
		if skuCode != "" {
			updates["sku_code"] = skuCode
		}

		if skuID > 0 {
			// 校验 SKU 归属，防止误传其他商品的 ID 导致本商品 SKU 被全部禁用
			var cnt int64
			if err := tx.Model(&model.Sku{}).
				Where("id = ? AND product_id = ?", skuID, p.ID).
				Count(&cnt).Error; err != nil {
				return nil, 0, 0, 0, err
			}
			if cnt == 0 {
				return nil, 0, 0, 0, fmt.Errorf("第 %d 个 SKU（id=%d）不存在或不属于该商品", i+1, skuID)
			}
			if err := tx.Model(&model.Sku{}).
				Where("id = ? AND product_id = ?", skuID, p.ID).
				Updates(updates).Error; err != nil {
				return nil, 0, 0, 0, err
			}
		} else {
			row := &model.Sku{
				ProductID:    p.ID,
				CommunityID:  p.CommunityID,
				SkuCode:      skuCode,
				SpecValueIDs: comboKey,
				SpecName:     specName,
				SpecJSON:     specJSON,
				Price:        item.Price,
				CostPrice:    item.CostPrice,
				Stock:        item.Stock,
				SafetyStock:  item.SafetyStock,
				Image:        item.Image,
				Status:       1,
			}
			if err := tx.Create(row).Error; err != nil {
				return nil, 0, 0, 0, err
			}
			skuID = row.ID
		}

		keepSkuIDs = append(keepSkuIDs, skuID)
		if minPrice == 0 || item.Price < minPrice {
			minPrice = item.Price
		}
		if item.Price > maxPrice {
			maxPrice = item.Price
		}
		totalStock += item.Stock
	}
	return keepSkuIDs, minPrice, maxPrice, totalStock, nil
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
		return 0, fmt.Errorf("规格值 ID 不能为空")
	}
	if id, ok := tempMap[raw]; ok {
		return id, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("无法识别的规格值引用: %s", raw)
	}
	if _, ok := valueNameMap[id]; !ok {
		return 0, fmt.Errorf("规格值 %d 不存在于本次提交的规格树中", id)
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
