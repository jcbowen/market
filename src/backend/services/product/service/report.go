package service

import (
	"context"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/product"
)

// ReportService 进销存报表业务服务
// 口径说明：
//   - 进销存汇总：期初/期末取 stock_records 首末流水快照，入/出按流水类型聚合；
//   - 毛利分析：近似口径 = 期间销售出库数量（type=2 且无出库单号的流水）× 当前售价/当前移动加权成本。
type ReportService struct {
	db *mysql.Instance
}

// NewReportService 创建报表业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *ReportService: 服务实例
func NewReportService(db *mysql.Instance) *ReportService {
	return &ReportService{db: db}
}

// GetInventorySummaryReport 进销存汇总报表（期初 + 入 - 出 = 期末）
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *product.GetInventorySummaryReportResp: 分页汇总行
//   - error: 错误信息
func (s *ReportService) GetInventorySummaryReport(ctx context.Context, req *product.GetInventorySummaryReportReq) (*product.GetInventorySummaryReportResp, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	start, end := reportRange(req.StartDate, req.EndDate)

	type summaryRow struct {
		SkuID        int64   `gorm:"column:sku_id"`
		SkuCode      string  `gorm:"column:sku_code"`
		SpecName     string  `gorm:"column:spec_name"`
		ProductName  string  `gorm:"column:product_name"`
		OpeningStock int32   `gorm:"column:opening_stock"`
		InQuantity   int32   `gorm:"column:in_quantity"`
		OutQuantity  int32   `gorm:"column:out_quantity"`
		ClosingStock int32   `gorm:"column:closing_stock"`
		CostPrice    float64 `gorm:"column:cost_price"`
		Price        float64 `gorm:"column:price"`
	}

	baseWhere := `
		FROM product_skus s
		INNER JOIN products p ON p.id = s.product_id
		LEFT JOIN stock_records fr ON fr.id = (
			SELECT MIN(sr.id) FROM stock_records sr
			WHERE sr.sku_id = s.id AND sr.created_at >= ? AND sr.created_at <= ?
		)
		LEFT JOIN stock_records lr ON lr.id = (
			SELECT MAX(sr.id) FROM stock_records sr
			WHERE sr.sku_id = s.id AND sr.created_at >= ? AND sr.created_at <= ?
		)
		LEFT JOIN (
			SELECT sku_id,
				SUM(CASE WHEN quantity > 0 THEN quantity ELSE 0 END) AS q_in,
				SUM(CASE WHEN quantity < 0 THEN -quantity ELSE 0 END) AS q_out
			FROM stock_records
			WHERE created_at >= ? AND created_at <= ?
			GROUP BY sku_id
		) agg ON agg.sku_id = s.id
		WHERE s.community_id = ? AND s.status = 1`

	args := []interface{}{start, end, start, end, start, end, req.CommunityId}
	if req.Keyword != "" {
		baseWhere += " AND (p.name LIKE ? OR s.sku_code LIKE ?)"
		kw := "%" + req.Keyword + "%"
		args = append(args, kw, kw)
	}

	var total int64
	countArgs := append([]interface{}{}, args...)
	if err := s.db.GetDb().WithContext(ctx).Raw("SELECT COUNT(*) "+baseWhere, countArgs...).Scan(&total).Error; err != nil {
		return nil, err
	}

	query := `
		SELECT s.id AS sku_id, s.sku_code, s.spec_name, p.name AS product_name,
			COALESCE(fr.before_stock, s.stock) AS opening_stock,
			COALESCE(agg.q_in, 0) AS in_quantity,
			COALESCE(agg.q_out, 0) AS out_quantity,
			COALESCE(lr.after_stock, s.stock) AS closing_stock,
			s.cost_price, s.price` + baseWhere + `
		ORDER BY s.id ASC LIMIT ? OFFSET ?`
	args = append(args, pageSize, (page-1)*pageSize)

	var rows []summaryRow
	if err := s.db.GetDb().WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}

	list := make([]*product.InventorySummaryItem, 0, len(rows))
	for _, r := range rows {
		list = append(list, &product.InventorySummaryItem{
			SkuId:        r.SkuID,
			SkuCode:      r.SkuCode,
			SpecName:     r.SpecName,
			ProductName:  r.ProductName,
			OpeningStock: r.OpeningStock,
			InQuantity:   r.InQuantity,
			OutQuantity:  r.OutQuantity,
			ClosingStock: r.ClosingStock,
			CostPrice:    r.CostPrice,
			Price:        r.Price,
		})
	}
	return &product.GetInventorySummaryReportResp{
		List: list,
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}, nil
}

// GetProfitReport 毛利分析报表（近似口径，详见 ReportService 注释）
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *product.GetProfitReportResp: 分页毛利行
//   - error: 错误信息
func (s *ReportService) GetProfitReport(ctx context.Context, req *product.GetProfitReportReq) (*product.GetProfitReportResp, error) {
	page, pageSize := 1, 10
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	start, end := reportRange(req.StartDate, req.EndDate)

	type profitRow struct {
		SkuID       int64   `gorm:"column:sku_id"`
		SkuCode     string  `gorm:"column:sku_code"`
		SpecName    string  `gorm:"column:spec_name"`
		ProductName string  `gorm:"column:product_name"`
		Quantity    int32   `gorm:"column:quantity"`
		Price       float64 `gorm:"column:price"`
		CostPrice   float64 `gorm:"column:cost_price"`
	}

	baseWhere := `
		FROM product_skus s
		INNER JOIN products p ON p.id = s.product_id
		LEFT JOIN (
			SELECT sku_id, SUM(-quantity) AS q
			FROM stock_records
			WHERE type = 2 AND ref_no = '' AND created_at >= ? AND created_at <= ?
			GROUP BY sku_id
		) agg ON agg.sku_id = s.id
		WHERE s.community_id = ? AND s.status = 1 AND COALESCE(agg.q, 0) > 0`

	args := []interface{}{start, end, req.CommunityId}
	if req.Keyword != "" {
		baseWhere += " AND (p.name LIKE ? OR s.sku_code LIKE ?)"
		kw := "%" + req.Keyword + "%"
		args = append(args, kw, kw)
	}

	var total int64
	countArgs := append([]interface{}{}, args...)
	if err := s.db.GetDb().WithContext(ctx).Raw("SELECT COUNT(*) "+baseWhere, countArgs...).Scan(&total).Error; err != nil {
		return nil, err
	}

	query := `
		SELECT s.id AS sku_id, s.sku_code, s.spec_name, p.name AS product_name,
			COALESCE(agg.q, 0) AS quantity, s.price, s.cost_price` + baseWhere + `
		ORDER BY quantity DESC LIMIT ? OFFSET ?`
	args = append(args, pageSize, (page-1)*pageSize)

	var rows []profitRow
	if err := s.db.GetDb().WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}

	list := make([]*product.ProfitReportItem, 0, len(rows))
	for _, r := range rows {
		salesAmount := round2(float64(r.Quantity) * r.Price)
		costAmount := round2(float64(r.Quantity) * r.CostPrice)
		list = append(list, &product.ProfitReportItem{
			SkuId:       r.SkuID,
			SkuCode:     r.SkuCode,
			SpecName:    r.SpecName,
			ProductName: r.ProductName,
			Quantity:    r.Quantity,
			SalesAmount: salesAmount,
			CostAmount:  costAmount,
			Profit:      round2(salesAmount - costAmount),
		})
	}
	return &product.GetProfitReportResp{
		List: list,
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	}, nil
}

// reportRange 归一化报表时间范围（空值回退：近 30 天）
// 参数：
//   - startDate: 起始日期（YYYY-MM-DD，可空）
//   - endDate: 结束日期（YYYY-MM-DD，可空）
//
// 返回值：
//   - string: 起始时间（含）
//   - string: 结束时间（含）
func reportRange(startDate, endDate string) (string, string) {
	if endDate == "" {
		endDate = time.Now().Format("2006-01-02") + " 23:59:59"
	} else {
		endDate += " 23:59:59"
	}
	if startDate == "" {
		startDate = time.Now().AddDate(0, 0, -30).Format("2006-01-02") + " 00:00:00"
	} else {
		startDate += " 00:00:00"
	}
	return startDate, endDate
}
