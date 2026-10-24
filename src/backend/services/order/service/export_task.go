package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

// exportTaskBatchSize 导出分批查询的批次大小：游标分批流式写文件，
// 单批 500 行在内存与 RPC 往返间取得平衡，不随总行数增长
const exportTaskBatchSize = 500

// exportTaskMaxRunning 同管理员同时在途（待处理/生成中）任务数上限，防连点刷任务拖垮服务
const exportTaskMaxRunning = 2

// ExportTaskService 后台导出任务服务（下载中心）
// 职责：创建异步导出任务、分批生成 CSV、任务列表/下载留痕/删除、过期清理
type ExportTaskService struct {
	db *mysql.Instance
	// orderService 复用订单列表过滤构建（applyOrderListFilters），保证「所见即所导」
	orderService *OrderService
}

// NewExportTaskService 创建导出任务服务
// 参数：
//   - db: MySQL 实例
//   - orderService: 订单服务（复用列表过滤构建）
//
// 返回值：
//   - *ExportTaskService: 服务实例
func NewExportTaskService(db *mysql.Instance, orderService *OrderService) *ExportTaskService {
	return &ExportTaskService{db: db, orderService: orderService}
}

// exportQuery 导出查询条件快照（与 ListOrdersReq 过滤字段对齐，JSON 存任务行）
type exportQuery struct {
	CommunityID    int64    `json:"community_id"`
	UserID         int64    `json:"user_id"`
	Source         int32    `json:"source"`
	Status         int32    `json:"status"`
	ShiftID        int64    `json:"shift_id"`
	ProductID      int64    `json:"product_id"`
	OrderNo        string   `json:"order_no"`
	StartTime      string   `json:"start_time"`
	EndTime        string   `json:"end_time"`
	ReceiverName   string   `json:"receiver_name"`
	ReceiverPhone  string   `json:"receiver_phone"`
	PickupCode     string   `json:"pickup_code"`
	HasPayNo       bool     `json:"has_pay_no"`
	PayOrderNos    []string `json:"pay_order_nos"`
	IncludeDeleted bool     `json:"include_deleted"`
}

// toListOrdersReq 把查询快照还原为 ListOrdersReq（复用列表过滤构建的适配层）
// 返回值：
//   - *order.ListOrdersReq: 过滤请求
func (q *exportQuery) toListOrdersReq() *order.ListOrdersReq {
	return &order.ListOrdersReq{
		CommunityId:        q.CommunityID,
		UserId:             q.UserID,
		IncludeUserDeleted: q.IncludeDeleted,
		Source:             q.Source,
		Status:             q.Status,
		ShiftId:            q.ShiftID,
		ProductId:          q.ProductID,
		HasPayNo:           q.HasPayNo,
		PayOrderNos:        q.PayOrderNos,
		OrderNo:            q.OrderNo,
		StartTime:          q.StartTime,
		EndTime:            q.EndTime,
		ReceiverName:       q.ReceiverName,
		ReceiverPhone:      q.ReceiverPhone,
		PickupCode:         q.PickupCode,
	}
}

// CreateExportTask 创建导出任务并异步启动生成（创建接口立即返回，不阻塞请求）
// 同管理员在途任务超过 exportTaskMaxRunning 时拒绝，防止连点刷任务
// 参数：
//   - ctx: 上下文
//   - req: 创建请求（export_type/query_json/query_desc/创建人信息/反查订单号集合）
//
// 返回值：
//   - int64: 任务 ID
//   - error: 任务超限或落库失败
//
// 使用示例：
//	id, err := svc.CreateExportTask(ctx, req)
func (s *ExportTaskService) CreateExportTask(ctx context.Context, req *order.CreateExportTaskReq) (int64, error) {
	if req.ExportType != model.ExportTypeOrders {
		return 0, bizerr.New(codes.InvalidArgument, "不支持的导出类型")
	}
	// 在途任务限流（含历史遗留的待处理任务，防积压）
	var running int64
	if err := s.db.GetDb().WithContext(ctx).Model(&model.ExportTask{}).
		Where("admin_id = ? AND status IN ?", req.AdminId, []int32{model.ExportTaskStatusPending, model.ExportTaskStatusRunning}).
		Count(&running).Error; err != nil {
		return 0, bizerr.New(codes.Internal, "查询导出任务失败")
	}
	if running >= exportTaskMaxRunning {
		return 0, bizerr.New(codes.FailedPrecondition, "您有待完成的导出任务，请稍后再试")
	}

	taskNo := newExportTaskNo()
	now := time.Now()
	expiresAt := now.AddDate(0, 0, model.ExportRetentionDays).Format("2006-01-02 15:04:05")
	task := &model.ExportTask{
		TaskNo:      taskNo,
		ExportType:  req.ExportType,
		Status:      model.ExportTaskStatusPending,
		QueryCond:   req.QueryJson,
		QueryDesc:   req.QueryDesc,
		FileName:    fmt.Sprintf("%s-%s-%s.csv", req.ExportType, now.Format("20060102"), shortTaskNo(taskNo)),
		AdminID:     req.AdminId,
		AdminName:   req.AdminName,
		CommunityID: req.CommunityId,
		ExpiresAt:   &expiresAt,
	}
	// 支付单号反查命中集并入快照（order 服务无法反查 payment，gateway 反查后随快照下发）
	if len(req.PayOrderNos) > 0 {
		var q exportQuery
		if req.QueryJson != "" {
			if err := json.Unmarshal([]byte(req.QueryJson), &q); err != nil {
				return 0, bizerr.New(codes.InvalidArgument, "查询条件快照解析失败")
			}
		}
		q.HasPayNo = true
		q.PayOrderNos = req.PayOrderNos
		q.IncludeDeleted = true
		if b, err := json.Marshal(q); err == nil {
			task.QueryCond = string(b)
		}
	}
	if err := s.db.GetDb().WithContext(ctx).Create(task).Error; err != nil {
		return 0, bizerr.New(codes.Internal, "创建导出任务失败")
	}

	// 异步生成：使用独立 context（请求 ctx 随 HTTP 返回而取消，任务必须跑完）
	go s.runExport(context.WithoutCancel(ctx), task)
	return task.ID, nil
}

// runExport 执行导出生成：置生成中 → 游标分批流式写 CSV → 完成置已完成并刷新过期时间；
// 失败删除半成品文件并置失败态（错误原因落任务行，可在下载中心查看）
// 参数：
//   - ctx: 后台上下文（脱离请求生命周期）
//   - task: 任务行（含查询条件快照）
func (s *ExportTaskService) runExport(ctx context.Context, task *model.ExportTask) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[export-task] 任务 %d 生成 panic 已恢复: %v", task.ID, r)
			s.failExport(task.ID, "生成过程异常，请重新发起导出")
		}
	}()

	if err := s.db.GetDb().Model(&model.ExportTask{}).Where("id = ?", task.ID).
		Update("status", model.ExportTaskStatusRunning).Error; err != nil {
		log.Printf("[export-task] 任务 %d 置生成中失败: %v", task.ID, err)
		return
	}

	// 还原查询快照
	var q exportQuery
	if task.QueryCond != "" {
		if err := json.Unmarshal([]byte(task.QueryCond), &q); err != nil {
			s.failExport(task.ID, "查询条件快照解析失败")
			return
		}
	}
	q.IncludeDeleted = true // 后台导出与列表口径一致：看全量（含用户软删单）

	// 打开输出文件（attachment/exports 目录，与 gateway /uploads 静态目录同级但不走公开访问）
	root, err := exportRootDir()
	if err != nil {
		s.failExport(task.ID, err.Error())
		return
	}
	fullPath := filepath.Join(root, task.FileName)
	f, err := os.Create(fullPath)
	if err != nil {
		s.failExport(task.ID, "创建导出文件失败")
		return
	}

	success := false
	defer func() {
		if !success {
			_ = f.Close()
			_ = os.Remove(fullPath) // 删半成品，失败任务不留脏文件
		}
	}()

	// BOM 保证 Excel 正确识别 UTF-8
	if _, err := f.WriteString("\ufeff"); err != nil {
		s.failExport(task.ID, "写入导出文件失败")
		return
	}
	if _, err := f.WriteString(exportCSVHeader); err != nil {
		s.failExport(task.ID, "写入导出文件失败")
		return
	}

	// 游标分批：按 id asc 逐批写，行数封顶 ExportMaxRows
	rows, truncated, err := s.writeRows(ctx, &q, f)
	if err != nil {
		s.failExport(task.ID, "生成导出数据失败")
		return
	}
	info, err := f.Stat()
	if err != nil {
		s.failExport(task.ID, "读取导出文件失败")
		return
	}
	if err := f.Close(); err != nil {
		s.failExport(task.ID, "写入导出文件失败")
		return
	}
	success = true

	// 过期时间从生成完成时刻起算 3 天（创建时的临时过期时间仅兜底失败/中断任务）
	exp := time.Now().AddDate(0, 0, model.ExportRetentionDays).Format("2006-01-02 15:04:05")
	updates := map[string]interface{}{
		"status":     model.ExportTaskStatusDone,
		// file_path 与实际写入路径一致（相对导出根目录的文件名）：
		// root 已是 …/attachment/exports，再带 exports/ 前缀会让下载与清理落到
		// …/exports/exports/ 下找不到文件（删除函数两个路径都删正是历史矛盾的痕迹）
		"file_path":  task.FileName,
		"file_size":  info.Size(),
		"row_count":  rows,
		"expires_at": exp,
		"error_msg":  "",
	}
	if truncated {
		// 达到行数上限截断：错误信息字段复用为提示语（完成态下前端展示为「部分导出」提示）
		updates["error_msg"] = fmt.Sprintf("已达单次导出上限 %d 行，仅导出前 %d 行，请缩小筛选范围", model.ExportMaxRows, rows)
	}
	if err := s.db.GetDb().Model(&model.ExportTask{}).Where("id = ?", task.ID).Updates(updates).Error; err != nil {
		log.Printf("[export-task] 任务 %d 置完成失败: %v", task.ID, err)
	}
}

// writeRows 游标分批查询订单并写 CSV 数据行
// 参数：
//   - ctx: 上下文
//   - q: 查询快照
//   - f: 输出文件
//
// 返回值：
//   - int64: 已写数据行数
//   - bool: 是否因行数上限截断
//   - error: 查询或写文件错误
func (s *ExportTaskService) writeRows(ctx context.Context, q *exportQuery, f *os.File) (int64, bool, error) {
	var written int64
	var lastID int64
	for {
		var list []*model.Order
		db := s.orderService.applyOrderListFilters(ctx, q.toListOrdersReq()).
			Where("id > ?", lastID).
			Order("id asc").Limit(exportTaskBatchSize)
		if err := db.Find(&list).Error; err != nil {
			return written, false, err
		}
		if len(list) == 0 {
			return written, false, nil
		}

		// 批内装配：商品明细（含规格/数量）、会员名、支付方式，一次批量查询
		ids := make([]int64, 0, len(list))
		userIDs := make([]int64, 0, len(list))
		for _, o := range list {
			ids = append(ids, o.ID)
			if o.UserID > 0 {
				userIDs = append(userIDs, o.UserID)
			}
		}
		itemMap, err := s.orderItemsOf(ctx, ids)
		if err != nil {
			return written, false, err
		}
		nameMap := s.userNamesOf(ctx, userIDs, q.CommunityID)
		payMap := s.payTypesOf(ctx, ids)

		var buf []byte
		for _, o := range list {
			line, err := exportCSVRow(o, itemMap[o.ID], nameMap[o.UserID], payTypesText(payMap[o.ID]))
			if err != nil {
				return written, false, err
			}
			buf = append(buf, line...)
			written++
			lastID = o.ID
			if written >= model.ExportMaxRows {
				if _, err := f.Write(buf); err != nil {
					return written, true, err
				}
				return written, true, nil
			}
		}
		if err := ctx.Err(); err != nil {
			return written, false, err
		}
		if _, err := f.Write(buf); err != nil {
			return written, false, err
		}
		if len(list) < exportTaskBatchSize {
			return written, false, nil
		}
	}
}

// orderItemsOf 批量取订单商品明细（按订单分组，含名称/规格/数量快照）
// 参数：
//   - ctx: 上下文
//   - orderIds: 订单 ID 列表
//
// 返回值：
//   - map[int64][]model.OrderItem: 订单 ID -> 明细列表
//   - error: 查询错误
func (s *ExportTaskService) orderItemsOf(ctx context.Context, orderIds []int64) (map[int64][]model.OrderItem, error) {
	result := make(map[int64][]model.OrderItem)
	if len(orderIds) == 0 {
		return result, nil
	}
	var items []model.OrderItem
	if err := s.db.GetDb().WithContext(ctx).Where("order_id IN ?", orderIds).Find(&items).Error; err != nil {
		return nil, err
	}
	for _, it := range items {
		result[it.OrderID] = append(result[it.OrderID], it)
	}
	return result, nil
}

// userNamesOf 批量取会员展示名（昵称优先，回落手机号；RPC 失败留空不阻断导出）
// 参数：
//   - ctx: 上下文
//   - userIDs: 会员 ID 列表
//   - communityID: 社区语境（0=跨社区，与列表批量查询口径一致）
//
// 返回值：
//   - map[int64]string: 会员 ID -> 展示名
func (s *ExportTaskService) userNamesOf(ctx context.Context, userIDs []int64, communityID int64) map[int64]string {
	names := make(map[int64]string)
	if len(userIDs) == 0 || grpcclient.UserClient == nil {
		return names
	}
	resp, err := grpcclient.UserClient.BatchGetUsers(ctx, &user.BatchGetUsersReq{Ids: userIDs, CommunityId: communityID})
	if err != nil {
		log.Printf("[export-task] 批量取会员名失败（留空继续）: %v", err)
		return names
	}
	for _, u := range resp.List {
		name := u.Nickname
		if name == "" {
			name = u.Phone
		}
		names[u.Id] = name
	}
	return names
}

// payTypesOf 批量取订单支付方式（RPC 失败留空不阻断导出）
// 参数：
//   - ctx: 上下文
//   - orderIds: 订单 ID 列表
//
// 返回值：
//   - map[int64]int32: 订单 ID -> 支付类型（0=未支付/未知）
func (s *ExportTaskService) payTypesOf(ctx context.Context, orderIds []int64) map[int64]int32 {
	result := make(map[int64]int32)
	if len(orderIds) == 0 || grpcclient.PaymentClient == nil {
		return result
	}
	resp, err := grpcclient.PaymentClient.BatchGetPayments(ctx, &payment.BatchGetPaymentsReq{OrderIds: orderIds})
	if err != nil {
		log.Printf("[export-task] 批量取支付方式失败（留空继续）: %v", err)
		return result
	}
	for _, p := range resp.List {
		result[p.OrderId] = p.PayType
	}
	return result
}

// ListExportTasks 下载中心任务列表（普通管理员仅见自己创建的，超管见全部）
// 同时返回「已完成且未下载」任务数供右上角图标角标
// 参数：
//   - ctx: 上下文
//   - req: 列表请求（admin_id/super_admin/community_id/分页）
//
// 返回值：
//   - []*order.ExportTaskInfo: 任务列表
//   - int32: 总数
//   - int32: 未下载数
//   - error: 查询错误
func (s *ExportTaskService) ListExportTasks(ctx context.Context, req *order.ListExportTasksReq) ([]*order.ExportTaskInfo, int32, int32, error) {
	page, pageSize := int(req.Page), int(req.PageSize)
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.ExportTask{})
	if !req.SuperAdmin {
		db = db.Where("admin_id = ?", req.AdminId)
	} else if req.CommunityId > 0 {
		// 超管限定社区语境时仅看该社区创建人的任务（跨社区任务 0 仍全部可见）
		db = db.Where("community_id IN ?", []int64{req.CommunityId, 0})
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, 0, bizerr.New(codes.Internal, "查询导出任务失败")
	}

	var list []model.ExportTask
	if err := db.Order("id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, 0, bizerr.New(codes.Internal, "查询导出任务失败")
	}

	// 未下载角标：与列表同一可见性口径（普通管理员只数自己的）
	countDb := s.db.GetDb().WithContext(ctx).Model(&model.ExportTask{}).
		Where("status = ? AND download_count = 0", model.ExportTaskStatusDone)
	if !req.SuperAdmin {
		countDb = countDb.Where("admin_id = ?", req.AdminId)
	} else if req.CommunityId > 0 {
		countDb = countDb.Where("community_id IN ?", []int64{req.CommunityId, 0})
	}
	var undownloaded int64
	if err := countDb.Count(&undownloaded).Error; err != nil {
		return nil, 0, 0, bizerr.New(codes.Internal, "查询导出任务失败")
	}

	infos := make([]*order.ExportTaskInfo, 0, len(list))
	for _, t := range list {
		infos = append(infos, toExportTaskInfo(&t))
	}
	return infos, int32(total), int32(undownloaded), nil
}

// CountUndownloaded 统计当前可见范围内「已完成且未下载」的任务数（下载图标角标）
// 参数：
//   - ctx: 上下文
//   - req: 请求（admin_id/super_admin/community_id）
//
// 返回值：
//   - int32: 未下载数
//   - error: 查询错误
func (s *ExportTaskService) CountUndownloaded(ctx context.Context, req *order.CountUndownloadedReq) (int32, error) {
	db := s.db.GetDb().WithContext(ctx).Model(&model.ExportTask{}).
		Where("status = ? AND download_count = 0", model.ExportTaskStatusDone)
	if !req.SuperAdmin {
		db = db.Where("admin_id = ?", req.AdminId)
	} else if req.CommunityId > 0 {
		db = db.Where("community_id IN ?", []int64{req.CommunityId, 0})
	}
	var count int64
	if err := db.Count(&count).Error; err != nil {
		return 0, bizerr.New(codes.Internal, "查询导出任务失败")
	}
	return int32(count), nil
}

// GetExportTaskFile 取导出文件（下载入口）：校验可见性后返回文件绝对路径与下载名，
// 并记录下载人/次数（留痕 JSON 追加，完整名单仅超管可见）
// 参数：
//   - ctx: 上下文
//   - req: 请求（任务 ID + 下载人信息）
//
// 返回值：
//   - string: 文件绝对路径（服务端 attachment 目录下）
//   - string: 下载文件名
//   - error: 任务不存在或无权下载
func (s *ExportTaskService) GetExportTaskFile(ctx context.Context, req *order.GetExportTaskFileReq) (string, string, error) {
	var task model.ExportTask
	if err := s.db.GetDb().WithContext(ctx).First(&task, req.Id).Error; err != nil {
		return "", "", bizerr.New(codes.NotFound, "导出任务不存在")
	}
	if !req.SuperAdmin && (task.AdminID != req.AdminId || (req.CommunityId > 0 && task.CommunityID != req.CommunityId)) {
		return "", "", bizerr.New(codes.PermissionDenied, "无权下载该文件")
	}
	if task.Status != model.ExportTaskStatusDone || task.FilePath == "" {
		return "", "", bizerr.New(codes.FailedPrecondition, "文件尚未生成完成")
	}
	root, err := exportRootDir()
	if err != nil {
		return "", "", bizerr.New(codes.Internal, "定位导出目录失败")
	}
	fullPath := filepath.Join(root, filepath.FromSlash(task.FilePath))
	if _, err := os.Stat(fullPath); err != nil {
		return "", "", bizerr.New(codes.NotFound, "导出文件已过期或不存在")
	}

	// 下载留痕：次数 +1，留痕 JSON 追加（仅保留最近 50 条，防止长文本无限膨胀）
	downloaders := appendDownloaderRecord(task.Downloaders, req.AdminId, req.AdminName)
	if err := s.db.GetDb().Model(&model.ExportTask{}).Where("id = ?", task.ID).Updates(map[string]interface{}{
		"download_count": gorm.Expr("download_count + 1"),
		"downloaders":    downloaders,
	}).Error; err != nil {
		log.Printf("[export-task] 任务 %d 记录下载留痕失败: %v", task.ID, err)
	}
	return fullPath, task.FileName, nil
}

// DeleteExportTask 删除导出任务（仅超管）：文件与记录一并删除
// 文件删除失败不阻断记录删除（避免孤儿记录卡住操作），孤儿文件由过期清理兜底
// 参数：
//   - ctx: 上下文
//   - id: 任务 ID
//
// 返回值：
//   - error: 任务不存在或删除失败
func (s *ExportTaskService) DeleteExportTask(ctx context.Context, id int64) error {
	var task model.ExportTask
	if err := s.db.GetDb().WithContext(ctx).First(&task, id).Error; err != nil {
		return bizerr.New(codes.NotFound, "导出任务不存在")
	}
	// 删除在途任务时同时尝试清理半成品（文件名在创建时已确定）
	if task.FileName != "" {
		if root, err := exportRootDir(); err == nil {
			_ = os.Remove(filepath.Join(root, task.FileName))
			if task.FilePath != "" {
				_ = os.Remove(filepath.Join(root, filepath.FromSlash(task.FilePath)))
			}
		}
	}
	if err := s.db.GetDb().WithContext(ctx).Delete(&task).Error; err != nil {
		return bizerr.New(codes.Internal, "删除导出任务失败")
	}
	return nil
}

// CleanupExpired 清理过期导出任务：删除过期文件与记录（调度器每轮调用）
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - int: 本次清理的任务数
func (s *ExportTaskService) CleanupExpired(ctx context.Context) int {
	var tasks []model.ExportTask
	now := time.Now().Format("2006-01-02 15:04:05")
	if err := s.db.GetDb().WithContext(ctx).
		Where("expires_at IS NOT NULL AND expires_at < ?", now).
		Limit(200).Find(&tasks).Error; err != nil {
		log.Printf("[export-task] 扫描过期导出任务失败: %v", err)
		return 0
	}
	root, err := exportRootDir()
	if err != nil {
		return 0
	}
	cleaned := 0
	for _, t := range tasks {
		if t.FilePath != "" {
			_ = os.Remove(filepath.Join(root, filepath.FromSlash(t.FilePath)))
		}
		if err := s.db.GetDb().WithContext(ctx).Delete(&model.ExportTask{}, t.ID).Error; err != nil {
			log.Printf("[export-task] 删除过期导出任务 %d 失败: %v", t.ID, err)
			continue
		}
		cleaned++
	}
	return cleaned
}

// failExport 任务置失败态（错误原因落库；半成品文件由调用方 defer 删除）
// 参数：
//   - id: 任务 ID
//   - msg: 失败原因
func (s *ExportTaskService) failExport(id int64, msg string) {
	err := s.db.GetDb().Model(&model.ExportTask{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":    model.ExportTaskStatusFailed,
		"error_msg": msg,
	}).Error
	if err != nil {
		log.Printf("[export-task] 任务 %d 置失败失败: %v", id, err)
	}
}

// toExportTaskInfo 模型转 proto 视图
// 参数：
//   - t: 任务模型
//
// 返回值：
//   - *order.ExportTaskInfo: proto 视图
func toExportTaskInfo(t *model.ExportTask) *order.ExportTaskInfo {
	info := &order.ExportTaskInfo{
		Id:            t.ID,
		ExportType:    t.ExportType,
		Status:        t.Status,
		QueryDesc:     t.QueryDesc,
		FileName:      t.FileName,
		FileSize:      t.FileSize,
		RowCount:      t.RowCount,
		AdminId:       t.AdminID,
		AdminName:     t.AdminName,
		CommunityId:   t.CommunityID,
		DownloadCount: t.DownloadCount,
		Downloaders:   t.Downloaders,
		ErrorMsg:      t.ErrorMsg,
		CreatedAt:     t.CreatedAt,
		QueryCond:     t.QueryCond,
	}
	if t.ExpiresAt != nil {
		info.ExpiresAt = *t.ExpiresAt
	}
	return info
}

// appendDownloaderRecord 追加下载留痕记录（JSON 数组，保留最近 50 条）
// 参数：
//   - raw: 原留痕 JSON
//   - adminID: 下载人 ID
//   - adminName: 下载人姓名
//
// 返回值：
//   - string: 新留痕 JSON
func appendDownloaderRecord(raw string, adminID int64, adminName string) string {
	type dlRecord struct {
		AdminID   int64  `json:"admin_id"`
		AdminName string `json:"admin_name"`
		At        string `json:"at"`
	}
	records := make([]dlRecord, 0, 8)
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &records)
	}
	records = append(records, dlRecord{AdminID: adminID, AdminName: adminName, At: time.Now().Format("2006-01-02 15:04:05")})
	if len(records) > 50 {
		records = records[len(records)-50:]
	}
	b, err := json.Marshal(records)
	if err != nil {
		return raw
	}
	return string(b)
}

// newExportTaskNo 生成任务号（UUID 去连字符，32 位）
// 返回值：
//   - string: 任务号
func newExportTaskNo() string {
	return uuid.NewString()
}

// shortTaskNo 取任务号前 8 位作文件名随机段
// 参数：
//   - taskNo: 任务号
//
// 返回值：
//   - string: 短任务号
func shortTaskNo(taskNo string) string {
	if len(taskNo) > 8 {
		return taskNo[:8]
	}
	return taskNo
}

// exportRootDir 导出文件根目录：<exe>/attachment/exports
// 与 gateway /uploads 静态目录同级但独立子目录，下载一律走鉴权接口流式下发，不公开访问
// 返回值：
//   - string: 目录绝对路径（不存在时自动创建）
//   - error: 定位可执行文件失败
func exportRootDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位可执行文件失败: %w", err)
	}
	dir := filepath.Join(filepath.Dir(exe), "attachment", "exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建导出目录失败: %w", err)
	}
	return dir, nil
}

// exportCSVHeader 导出 CSV 表头（与前端旧导出列一致，另补商品明细与支付方式）
const exportCSVHeader = "订单号,用户,配送方式,来源,经手人,商品明细,支付方式,订单金额,实付金额,状态,创建时间\n"

// exportCSVRow 单行订单转 CSV 数据行（字段内引号转义，防 CSV 注入）
// 参数：
//   - o: 订单模型
//   - items: 订单商品明细
//   - userName: 会员展示名
//   - payTypeText: 支付方式文案
//
// 返回值：
//   - []byte: 含换行的 CSV 行
//   - error: 无（签名保留以便扩展）
func exportCSVRow(o *model.Order, items []model.OrderItem, userName, payTypeText string) ([]byte, error) {
	// 商品明细摘要：名称x数量（多商品分号分隔），含规格
	detail := ""
	for i, it := range items {
		if i > 0 {
			detail += "；"
		}
		if it.SpecName != "" {
			detail += fmt.Sprintf("%s（%s）x%d", it.ProductName, it.SpecName, it.Quantity)
		} else {
			detail += fmt.Sprintf("%s x%d", it.ProductName, it.Quantity)
		}
	}
	cells := []string{
		o.OrderNo,
		userName,
		deliveryTypeTextOf(o.DeliveryType),
		orderSourceTextOf(o.Source),
		o.OperatorName,
		detail,
		payTypeText,
		fmt.Sprintf("%.2f", o.TotalAmount),
		fmt.Sprintf("%.2f", o.PayAmount),
		orderStatusTextOf(o.Status, o.DeliveryType),
		o.CreatedAt,
	}
	for i, c := range cells {
		cells[i] = "\"" + escapeCSVCell(c) + "\""
	}
	return []byte(joinCSVCells(cells) + "\n"), nil
}

// escapeCSVCell 转义 CSV 单元格内的引号，并给以 =+-@ 开头的内容加制表符前缀防公式注入
// 参数：
//   - s: 原始文本
//
// 返回值：
//   - string: 安全文本
func escapeCSVCell(s string) string {
	// CSV 公式注入防护：Excel 会把 =/-/+/@ 开头的单元格当公式执行
	if len(s) > 0 && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@') {
		s = "\t" + s
	}
	return strings.ReplaceAll(s, "\"", "\"\"")
}

// joinCSVCells 逗号连接已转义的单元格
// 参数：
//   - cells: 单元格列表
//
// 返回值：
//   - string: CSV 行（不含换行）
func joinCSVCells(cells []string) string {
	return strings.Join(cells, ",")
}

// deliveryTypeTextOf 配送方式文案（与前端口径一致）
// 参数：
//   - t: 配送类型
//
// 返回值：
//   - string: 文案
func deliveryTypeTextOf(t int32) string {
	switch t {
	case 1:
		return "自提"
	case 2:
		return "配送"
	case 3:
		return "到店"
	default:
		return "未知"
	}
}

// orderSourceTextOf 订单来源文案
// 参数：
//   - s: 来源
//
// 返回值：
//   - string: 文案
func orderSourceTextOf(s int32) string {
	switch s {
	case 2:
		return "收银台"
	default:
		return "C端"
	}
}

// orderStatusTextOf 订单状态文案（与 gateway orderStatusText 口径一致）
// 参数：
//   - status: 订单状态
//   - deliveryType: 配送类型（状态 2/4 的文案随自提/到店变化）
//
// 返回值：
//   - string: 文案
func orderStatusTextOf(status, deliveryType int32) string {
	switch status {
	case 1:
		return "待支付"
	case 2:
		switch deliveryType {
		case 1:
			return "待自提"
		case 3:
			return "已支付"
		}
		return "待发货"
	case 3:
		return "备货中"
	case 4:
		if deliveryType == 1 {
			return "待自提"
		}
		if deliveryType == 3 {
			return "已支付"
		}
		return "已发货"
	case 5:
		return "已完成"
	case 6:
		return "已取消"
	case 7:
		return "退款中"
	case 8:
		return "已退款"
	default:
		return "未知"
	}
}

// payTypesText 支付方式文案（与 gateway payTypeTextOf 口径一致；0=未支付/未知留空）
// 参数：
//   - t: 支付类型
//
// 返回值：
//   - string: 文案
func payTypesText(t int32) string {
	switch t {
	case 1:
		return "微信支付"
	case 2:
		return "支付宝支付"
	case 3:
		return "货到付款"
	case 4:
		return "现金"
	case 5:
		return "微信付款码"
	case 6:
		return "支付宝付款码"
	default:
		return ""
	}
}
