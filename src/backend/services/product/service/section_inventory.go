package service

import (
	"context"
	"time"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// 本文件是数据包的「采购档案」与「进销存样本」两段（scope=community, init_only=true）：
//   - procurement：供应商档案 + 在途采购单（已下单未入库，入库后由服务生成批次，
//     种子不预置批次避免与 stock 汇总重复计数）；
//   - inventory：入库/出库单据 + 盘点草稿 + 库存流水（只落单据与流水，不动库存——
//     批次及 SKU 汇总库存已由目录段按终态创建，单据按批次号回查批次 ID 咬合）。

// SeedSupplier 演示供应商（按社区内名称幂等）
type SeedSupplier struct {
	Key          string `json:"key"`
	CommunityKey string `json:"community_key"`
	Name         string `json:"name"`
	ContactName  string `json:"contact_name"`
	Phone        string `json:"phone"`
	Address      string `json:"address"`
	Remark       string `json:"remark"`
	Status       int32  `json:"status"`
}

// SeedPurchaseOrderItem 演示采购明细（sku 引用目录段 key）
type SeedPurchaseOrderItem struct {
	SkuKey           string  `json:"sku_key"`
	Quantity         int32   `json:"quantity"`
	ReceivedQuantity int32   `json:"received_quantity"`
	PurchasePrice    float64 `json:"purchase_price"`
}

// SeedPurchaseOrder 演示采购单（状态：1草稿 2已下单 3部分入库 4已完结 5已取消）
type SeedPurchaseOrder struct {
	Key          string                  `json:"key"`
	CommunityKey string                  `json:"community_key"`
	PurchaseNo   string                  `json:"purchase_no"`
	SupplierKey  string                  `json:"supplier_key"`
	Status       int32                   `json:"status"`
	TotalAmount  float64                 `json:"total_amount"`
	OperatorKey  string                  `json:"operator_key"`
	Remark       string                  `json:"remark"`
	CreatedAt    *SeedTime               `json:"created_at"`
	Items        []SeedPurchaseOrderItem `json:"items"`
}

// SeedStockInItem 演示入库单明细（batch_no 与目录段批次咬合，导入时回查批次 ID）
type SeedStockInItem struct {
	SkuKey        string  `json:"sku_key"`
	Quantity      int32   `json:"quantity"`
	UnitCost      float64 `json:"unit_cost"`
	BatchNo       string  `json:"batch_no"`
	ProductionAgo int32   `json:"production_ago"`
	ExpireIn      int32   `json:"expire_in"`
}

// SeedStockInOrder 演示入库单（类型：1采购入库 2直接入库）
type SeedStockInOrder struct {
	Key          string            `json:"key"`
	CommunityKey string            `json:"community_key"`
	InNo         string            `json:"in_no"`
	SupplierName string            `json:"supplier_name"`
	Type         int32             `json:"type"`
	TotalAmount  float64           `json:"total_amount"`
	OperatorKey  string            `json:"operator_key"`
	Remark       string            `json:"remark"`
	CreatedAt    *SeedTime         `json:"created_at"`
	Items        []SeedStockInItem `json:"items"`
}

// SeedStockOutOrder 演示出库单（类型：1报损 2领用 3调整出库）
type SeedStockOutOrder struct {
	Key          string    `json:"key"`
	CommunityKey string    `json:"community_key"`
	OutNo        string    `json:"out_no"`
	Type         int32     `json:"type"`
	OperatorKey  string    `json:"operator_key"`
	Remark       string    `json:"remark"`
	CreatedAt    *SeedTime `json:"created_at"`
	SkuKey       string    `json:"sku_key"`
	Quantity     int32     `json:"quantity"`
}

// SeedStockCheckItem 演示盘点明细（system_stock 以导入时实际库存为准回填，种子值仅作断言参考）
type SeedStockCheckItem struct {
	SkuKey      string `json:"sku_key"`
	SystemStock int32  `json:"system_stock"`
	ActualStock int32  `json:"actual_stock"`
}

// SeedStockCheck 演示盘点单（草稿态：差异不回写库存）
type SeedStockCheck struct {
	Key          string               `json:"key"`
	CommunityKey string               `json:"community_key"`
	CheckNo      string               `json:"check_no"`
	OperatorKey  string               `json:"operator_key"`
	Status       int32                `json:"status"` // 1待提交 2已提交
	Remark       string               `json:"remark"`
	CreatedAt    *SeedTime            `json:"created_at"`
	Items        []SeedStockCheckItem `json:"items"`
}

// SeedStockRecord 演示库存流水（仅单据产生的变动；销售扣减流水由下单链路实时产生）
type SeedStockRecord struct {
	Key           string    `json:"key"`
	CommunityKey  string    `json:"community_key"`
	SkuKey        string    `json:"sku_key"`
	Type          int32     `json:"type"` // 1入库 2出库 3盘点 4释放
	Quantity      int32     `json:"quantity"`
	BeforeStock   int32     `json:"before_stock"`
	AfterStock    int32     `json:"after_stock"`
	BatchNo       string    `json:"batch_no"`
	ProductionAgo int32     `json:"production_ago"`
	ExpireIn      int32     `json:"expire_in"`
	RefNo         string    `json:"ref_no"`
	OperatorKey   string    `json:"operator_key"`
	Remark        string    `json:"remark"`
	CreatedAt     *SeedTime `json:"created_at"`
}

// ============================================================
// procurementSection 供应商与采购单
// ============================================================

type procurementSection struct {
	svc *DataImportService
}

func (s *procurementSection) Key() string    { return "procurement" }
func (s *procurementSection) Name() string   { return "供应商与采购" }
func (s *procurementSection) Scope() string  { return SectionScopeCommunity }
func (s *procurementSection) InitOnly() bool { return true }
func (s *procurementSection) Description() string {
	return "供应商档案与在途采购单样本，仅随初始化导入"
}

func (s *procurementSection) ItemCount(pkg *SeedPackage) int32 {
	return int32(len(pkg.Suppliers) + len(pkg.PurchaseOrders))
}

func (s *procurementSection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
}

func (s *procurementSection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	now := time.Now()
	nowStr := now.Format("2006-01-02 15:04:05")
	for i := range pkg.Suppliers {
		sup := &pkg.Suppliers[i]
		if !isImportable(req, sup.Key) {
			continue
		}
		if sup.CommunityKey == "" || sup.CommunityKey != req.CommunityKey {
			continue
		}
		var exist model.Supplier
		if err := s.svc.db.GetDb().WithContext(ctx).
			Where("community_id = ? AND name = ?", req.CommunityID, sup.Name).
			First(&exist).Error; err == nil {
			req.Refs.Set("supplier:"+sup.Key, exist.ID)
			appendImportResult(items, s.Key(), sup.Key, sup.Name, "skipped", "同名供应商已存在")
			continue
		}
		row := &model.Supplier{
			CommunityID: req.CommunityID,
			Name:        sup.Name,
			ContactName: sup.ContactName,
			Phone:       sup.Phone,
			Address:     sup.Address,
			Remark:      sup.Remark,
			Status:      sup.Status,
			CreatedAt:   nowStr,
			UpdatedAt:   nowStr,
		}
		if err := s.svc.db.GetDb().WithContext(ctx).Create(row).Error; err != nil {
			appendImportResult(items, s.Key(), sup.Key, sup.Name, "failed", "创建供应商失败："+err.Error())
			continue
		}
		req.Refs.Set("supplier:"+sup.Key, row.ID)
		appendImportResult(items, s.Key(), sup.Key, sup.Name, "created", "供应商已创建")
		created++
	}

	for i := range pkg.PurchaseOrders {
		po := &pkg.PurchaseOrders[i]
		if !isImportable(req, po.Key) {
			continue
		}
		if po.CommunityKey == "" || po.CommunityKey != req.CommunityKey {
			continue
		}
		var count int64
		s.svc.db.GetDb().WithContext(ctx).Model(&model.PurchaseOrder{}).
			Where("purchase_no = ?", po.PurchaseNo).Count(&count)
		if count > 0 {
			appendImportResult(items, s.Key(), po.Key, po.PurchaseNo, "skipped", "采购单已存在")
			continue
		}
		supplierID, _ := req.Refs.Get("supplier:" + po.SupplierKey)
		operatorID, err := s.svc.resolveSeedOperatorRef(ctx, pkg, req.Refs, "admin", po.OperatorKey)
		if err != nil {
			appendImportResult(items, s.Key(), po.Key, po.PurchaseNo, "failed", "操作员解析失败："+err.Error())
			continue
		}
		createdAt := nowStr
		if po.CreatedAt != nil {
			createdAt = po.CreatedAt.Resolve(now)
		}
		row := &model.PurchaseOrder{
			CommunityID: req.CommunityID,
			PurchaseNo:  po.PurchaseNo,
			SupplierID:  supplierID,
			Status:      po.Status,
			TotalAmount: po.TotalAmount,
			OperatorID:  operatorID,
			Remark:      po.Remark,
			CreatedAt:   createdAt,
			UpdatedAt:   createdAt,
		}
		if err := s.svc.db.GetDb().WithContext(ctx).Create(row).Error; err != nil {
			appendImportResult(items, s.Key(), po.Key, po.PurchaseNo, "failed", "创建采购单失败："+err.Error())
			continue
		}
		for j := range po.Items {
			pi := &po.Items[j]
			skuID, _ := req.Refs.Get(pi.SkuKey)
			item := &model.PurchaseOrderItem{
				PurchaseOrderID:  row.ID,
				SkuID:            skuID,
				Quantity:         pi.Quantity,
				ReceivedQuantity: pi.ReceivedQuantity,
				PurchasePrice:    pi.PurchasePrice,
				CreatedAt:        createdAt,
			}
			if err := s.svc.db.GetDb().WithContext(ctx).Create(item).Error; err != nil {
				appendImportResult(items, s.Key(), po.Key, po.PurchaseNo, "failed", "创建采购明细失败："+err.Error())
				break
			}
		}
		appendImportResult(items, s.Key(), po.Key, po.PurchaseNo, "created", "采购单样本已导入")
		created++
	}
	return created
}

// ============================================================
// inventorySection 进销存单据与库存流水
// ============================================================

type inventorySection struct {
	svc *DataImportService
}

func (s *inventorySection) Key() string    { return "inventory" }
func (s *inventorySection) Name() string   { return "进销存样本" }
func (s *inventorySection) Scope() string  { return SectionScopeCommunity }
func (s *inventorySection) InitOnly() bool { return true }
func (s *inventorySection) Description() string {
	return "入库/出库单据、盘点草稿与库存流水（不动库存，批次与目录段咬合），仅随初始化导入"
}

func (s *inventorySection) ItemCount(pkg *SeedPackage) int32 {
	return int32(len(pkg.StockIns) + len(pkg.StockOuts) + len(pkg.StockChecks) + len(pkg.StockRecords))
}

func (s *inventorySection) Preview(ctx context.Context, pkg *SeedPackage, req *PreviewRequest, items *[]*product.DataImportItem) {
}

func (s *inventorySection) Import(ctx context.Context, pkg *SeedPackage, req *ImportRequest, items *[]*product.DataImportItem) (created int32) {
	now := time.Now()
	for i := range pkg.StockIns {
		in := &pkg.StockIns[i]
		if !isImportable(req, in.Key) {
			continue
		}
		if in.CommunityKey == "" || in.CommunityKey != req.CommunityKey {
			continue
		}
		if n := s.countByNo(ctx, "stock_in_orders", "in_no", in.InNo); n > 0 {
			appendImportResult(items, s.Key(), in.Key, in.InNo, "skipped", "入库单已存在")
			continue
		}
		if err := s.importStockIn(ctx, pkg, req, in, now); err != nil {
			appendImportResult(items, s.Key(), in.Key, in.InNo, "failed", "入库单导入失败："+err.Error())
			continue
		}
		appendImportResult(items, s.Key(), in.Key, in.InNo, "created", "入库单样本已导入")
		created++
	}

	for i := range pkg.StockOuts {
		out := &pkg.StockOuts[i]
		if !isImportable(req, out.Key) {
			continue
		}
		if out.CommunityKey == "" || out.CommunityKey != req.CommunityKey {
			continue
		}
		if n := s.countByNo(ctx, "stock_out_orders", "out_no", out.OutNo); n > 0 {
			appendImportResult(items, s.Key(), out.Key, out.OutNo, "skipped", "出库单已存在")
			continue
		}
		if err := s.importStockOut(ctx, pkg, req, out, now); err != nil {
			appendImportResult(items, s.Key(), out.Key, out.OutNo, "failed", "出库单导入失败："+err.Error())
			continue
		}
		appendImportResult(items, s.Key(), out.Key, out.OutNo, "created", "出库单样本已导入")
		created++
	}

	for i := range pkg.StockChecks {
		chk := &pkg.StockChecks[i]
		if !isImportable(req, chk.Key) {
			continue
		}
		if chk.CommunityKey == "" || chk.CommunityKey != req.CommunityKey {
			continue
		}
		if n := s.countByNo(ctx, "stock_checks", "check_no", chk.CheckNo); n > 0 {
			appendImportResult(items, s.Key(), chk.Key, chk.CheckNo, "skipped", "盘点单已存在")
			continue
		}
		if err := s.importStockCheck(ctx, pkg, req, chk, now); err != nil {
			appendImportResult(items, s.Key(), chk.Key, chk.CheckNo, "failed", "盘点单导入失败："+err.Error())
			continue
		}
		appendImportResult(items, s.Key(), chk.Key, chk.CheckNo, "created", "盘点单样本已导入")
		created++
	}

	for i := range pkg.StockRecords {
		rec := &pkg.StockRecords[i]
		if !isImportable(req, rec.Key) {
			continue
		}
		if rec.CommunityKey == "" || rec.CommunityKey != req.CommunityKey {
			continue
		}
		// 幂等：同 SKU + 来源单号 + 类型已存在即跳过（stock_records 无唯一键）
		skuID, _ := req.Refs.Get(rec.SkuKey)
		if skuID == 0 {
			appendImportResult(items, s.Key(), rec.Key, rec.RefNo, "failed",
				"SKU 引用缺失（"+rec.SkuKey+"），请一并导入商品目录")
			continue
		}
		var count int64
		s.svc.db.GetDb().WithContext(ctx).Model(&model.StockRecord{}).
			Where("sku_id = ? AND ref_no = ? AND type = ?", skuID, rec.RefNo, rec.Type).
			Count(&count)
		if count > 0 {
			appendImportResult(items, s.Key(), rec.Key, rec.RefNo, "skipped", "库存流水已存在")
			continue
		}
		if err := s.importStockRecord(ctx, pkg, req, rec, now, skuID); err != nil {
			appendImportResult(items, s.Key(), rec.Key, rec.RefNo, "failed", "库存流水导入失败："+err.Error())
			continue
		}
		appendImportResult(items, s.Key(), rec.Key, rec.RefNo, "created", "库存流水已导入")
		created++
	}
	return created
}

// countByNo 按单据号判重（表名列名均为包内常量，无注入面）
func (s *inventorySection) countByNo(ctx context.Context, table, column, no string) int64 {
	var n int64
	s.svc.db.GetDb().WithContext(ctx).Table(table).
		Where(column+" = ?", no).Count(&n)
	return n
}

// batchIDByNo 按「SKU + 批次号」回查目录段创建的批次 ID
func (s *inventorySection) batchIDByNo(ctx context.Context, skuID int64, batchNo string) int64 {
	if skuID == 0 || batchNo == "" {
		return 0
	}
	var batch model.StockBatch
	if err := s.svc.db.GetDb().WithContext(ctx).
		Where("sku_id = ? AND batch_no = ?", skuID, batchNo).First(&batch).Error; err != nil {
		return 0
	}
	return batch.ID
}

// resolveStockOperator 解析单据操作员（admin key）
func (s *inventorySection) resolveStockOperator(ctx context.Context, pkg *SeedPackage, req *ImportRequest, key string) int64 {
	id, err := s.svc.resolveSeedOperatorRef(ctx, pkg, req.Refs, "admin", key)
	if err != nil {
		return 0
	}
	return id
}

func (s *inventorySection) importStockIn(ctx context.Context, pkg *SeedPackage, req *ImportRequest, in *SeedStockInOrder, now time.Time) error {
	db := s.svc.db.GetDb().WithContext(ctx)
	createdAt := in.CreatedAt.Resolve(now)
	var supplierID int64
	if in.SupplierName != "" {
		var sup model.Supplier
		if err := db.Where("community_id = ? AND name = ?", req.CommunityID, in.SupplierName).
			First(&sup).Error; err == nil {
			supplierID = sup.ID
		}
	}
	row := &model.StockInOrder{
		CommunityID: req.CommunityID,
		InNo:        in.InNo,
		SupplierID:  supplierID,
		Type:        in.Type,
		TotalAmount: in.TotalAmount,
		OperatorID:  s.resolveStockOperator(ctx, pkg, req, in.OperatorKey),
		Remark:      in.Remark,
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
	}
	if err := db.Create(row).Error; err != nil {
		return err
	}
	backfillTimestamps(db, &model.StockInOrder{}, row.ID, createdAt, createdAt)
	for i := range in.Items {
		it := &in.Items[i]
		skuID, _ := req.Refs.Get(it.SkuKey)
		productionDate := dateOffsetString(-int(it.ProductionAgo))
		expireDate := dateOffsetString(int(it.ExpireIn))
		item := &model.StockInOrderItem{
			InOrderID:      row.ID,
			SkuID:          skuID,
			Quantity:       it.Quantity,
			UnitCost:       it.UnitCost,
			BatchNo:        it.BatchNo,
			ProductionDate: &productionDate,
			ExpireDate:     &expireDate,
			BatchID:        s.batchIDByNo(ctx, skuID, it.BatchNo),
			CreatedAt:      createdAt,
		}
		if err := db.Create(item).Error; err != nil {
			return err
		}
		backfillTimestamps(db, &model.StockInOrderItem{}, item.ID, createdAt, "")
	}
	return nil
}

func (s *inventorySection) importStockOut(ctx context.Context, pkg *SeedPackage, req *ImportRequest, out *SeedStockOutOrder, now time.Time) error {
	db := s.svc.db.GetDb().WithContext(ctx)
	createdAt := out.CreatedAt.Resolve(now)
	row := &model.StockOutOrder{
		CommunityID: req.CommunityID,
		OutNo:       out.OutNo,
		Type:        out.Type,
		OperatorID:  s.resolveStockOperator(ctx, pkg, req, out.OperatorKey),
		Remark:      out.Remark,
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
	}
	if err := db.Create(row).Error; err != nil {
		return err
	}
	backfillTimestamps(db, &model.StockOutOrder{}, row.ID, createdAt, createdAt)
	skuID, _ := req.Refs.Get(out.SkuKey)
	item := &model.StockOutOrderItem{
		OutOrderID: row.ID,
		SkuID:      skuID,
		Quantity:   out.Quantity,
		CreatedAt:  createdAt,
	}
	if err := db.Create(item).Error; err != nil {
		return err
	}
	backfillTimestamps(db, &model.StockOutOrderItem{}, item.ID, createdAt, "")
	return nil
}

func (s *inventorySection) importStockCheck(ctx context.Context, pkg *SeedPackage, req *ImportRequest, chk *SeedStockCheck, now time.Time) error {
	db := s.svc.db.GetDb().WithContext(ctx)
	createdAt := chk.CreatedAt.Resolve(now)
	row := &model.StockCheck{
		CheckNo:     chk.CheckNo,
		CommunityID: req.CommunityID,
		OperatorID:  s.resolveStockOperator(ctx, pkg, req, chk.OperatorKey),
		Status:      chk.Status,
		TotalItems:  int32(len(chk.Items)),
		Remark:      chk.Remark,
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
	}
	// 主单先落库再写明细：明细为空时盘点单本身仍应存在（草稿态可为空单）
	if err := db.Create(row).Error; err != nil {
		return err
	}
	backfillTimestamps(db, &model.StockCheck{}, row.ID, createdAt, createdAt)
	diffItems := int32(0)
	for i := range chk.Items {
		it := &chk.Items[i]
		skuID, _ := req.Refs.Get(it.SkuKey)
		// system_stock 以导入时实际库存为准（与盘点单「系统库存」业务语义一致）
		var sku model.Sku
		systemStock := it.SystemStock
		if err := db.Where("id = ?", skuID).First(&sku).Error; err == nil {
			systemStock = sku.Stock
		}
		if systemStock != it.ActualStock {
			diffItems++
		}
		item := &model.StockCheckItem{
			CheckID:      row.ID,
			SkuID:        skuID,
			SystemStock:  systemStock,
			ActualStock:  it.ActualStock,
			DiffQuantity: it.ActualStock - systemStock,
			CreatedAt:    createdAt,
		}
		if err := db.Create(item).Error; err != nil {
			return err
		}
		backfillTimestamps(db, &model.StockCheckItem{}, item.ID, createdAt, "")
	}
	return db.Model(&model.StockCheck{}).Where("id = ?", row.ID).
		Updates(map[string]any{"diff_items": diffItems}).Error
}

func (s *inventorySection) importStockRecord(ctx context.Context, pkg *SeedPackage, req *ImportRequest, rec *SeedStockRecord, now time.Time, skuID int64) error {
	db := s.svc.db.GetDb().WithContext(ctx)
	productionDate := dateOffsetString(-int(rec.ProductionAgo))
	expireDate := dateOffsetString(int(rec.ExpireIn))
	createdAt := rec.CreatedAt.Resolve(now)
	row := &model.StockRecord{
		SkuID:          skuID,
		CommunityID:    req.CommunityID,
		Type:           rec.Type,
		Quantity:       rec.Quantity,
		BeforeStock:    rec.BeforeStock,
		AfterStock:     rec.AfterStock,
		BatchID:        s.batchIDByNo(ctx, skuID, rec.BatchNo),
		BatchNo:        rec.BatchNo,
		ProductionDate: &productionDate,
		ExpireDate:     &expireDate,
		RefNo:          rec.RefNo,
		OperatorID:     s.resolveStockOperator(ctx, pkg, req, rec.OperatorKey),
		Remark:         rec.Remark,
		CreatedAt:      createdAt,
	}
	if err := db.Create(row).Error; err != nil {
		return err
	}
	// BeforeCreate 钩子无条件覆盖 CreatedAt：回填种子历史时间
	backfillTimestamps(db, &model.StockRecord{}, row.ID, createdAt, "")
	return nil
}
