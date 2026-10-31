package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * 导出任务集成测试（直连本地开发库，env 守卫跳过）
 *
 * 运行方式（需本地 docker 栈已启动，MySQL 端口映射到宿主机）：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/order/service/ -run 'TestExportTask|TestListOrdersFilter' -v
 *
 * 覆盖：
 *   - TestExportTaskLifecycle           全生命周期：创建（快照/文件名）→ 异步生成（状态/行数/文件内容与筛选一致）→
 *                                       下载留痕（次数+留痕 JSON）→ 超管删除（文件与记录一并删除）
 *   - TestExportTaskCreateLimit         在途任务限流：2 条待处理即拒绝新任务；未知导出类型拒绝
 *   - TestExportTaskVisibility          可见性：普通管理员仅见自己的记录与角标、跨创建人下载被拒、
 *                                       超管全量可见且社区语境过滤、非完成态不可下载
 *   - TestExportTaskCleanupExpired      过期清理：删文件+删记录，未过期保留
 *   - TestListOrdersFilterPayNoProduct  ListOrders 新过滤：pay_no 命中集/空集恒假、product_id 含全部规格
 *
 * 说明：复用 scheduler_test.go 的造数工具（seedSchedOrder/seedSchedItem/cleanupSchedTestData），
 * 订单数据沿用 SCHEDTEST- 前缀便于统一清理；导出任务按管理员 ID 段单独清理。
 * 仅依赖 MySQL（导出链路不触 Redis），grpc 客户端为 nil 时会员名/支付方式留空继续，不影响断言。
 */

// expTestAdminBase 导出测试管理员 ID 起始值（远超真实后台管理员区间，清理依据）
const expTestAdminBase = int64(997001000)

// newExportTestEnv 构建导出任务测试环境：真实 DB + OrderService/ExportTaskService
// 迁移与 order 服务 main.go 同一份模型清单并追加 export_tasks，保证空库（CI）也可跑
//
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *mysql.Instance: DB 实例
//   - *OrderService: 订单服务
//   - *ExportTaskService: 导出任务服务
func newExportTestEnv(t *testing.T) (*mysql.Instance, *OrderService, *ExportTaskService) {
	t.Helper()
	if os.Getenv("TEST_MYSQL_PASSWORD") == "" {
		t.Skip("TEST_MYSQL_PASSWORD 未设置，跳过导出任务集成测试（需本地 docker 栈）")
	}
	dbCfg := jcbaseGo.DbStruct{
		Host:      envOr("TEST_MYSQL_HOST", "127.0.0.1"),
		Port:      envOr("TEST_MYSQL_PORT", "3306"),
		Dbname:    envOr("TEST_MYSQL_DBNAME", "market"),
		Username:  envOr("TEST_MYSQL_USER", "root"),
		Password:  os.Getenv("TEST_MYSQL_PASSWORD"),
		Charset:   "utf8mb4",
		ParseTime: "True",
		Loc:       "Local",
	}
	dbInst, err := mysql.New(dbCfg)
	if err != nil {
		t.Fatalf("连接测试 MySQL 失败: %v", err)
	}
	// 表结构演进统一走 AutoMigrate（与 services/order/main.go 同一模型清单 + 导出任务表）
	if err := dbInst.GetDb().AutoMigrate(
		&model.Order{}, &model.OrderItem{}, &model.OrderStatusLog{},
		&model.Refund{}, &model.RefundItem{}, &model.RefundConfig{}, &model.RefundLog{},
		&model.SchedulerFailure{}, &model.PosHeldOrder{}, &model.PosShift{}, &model.AppPreference{},
		&model.ExportTask{},
	); err != nil {
		t.Fatalf("测试库迁移失败: %v", err)
	}
	svc := NewOrderService(dbInst, nil)
	t.Cleanup(func() { cleanupSchedTestData(t, dbInst) })
	return dbInst, svc, NewExportTaskService(dbInst, svc)
}

// waitExportStatus 轮询等待任务到达期望状态（异步生成不阻塞创建，测试侧轮询收敛）
//
// 参数：
//   - t: 测试对象
//   - db: DB 实例
//   - id: 任务 ID
//   - want: 期望状态
//   - timeout: 最长等待时长
//
// 返回值：
//   - model.ExportTask: 到达期望状态的任务行
func waitExportStatus(t *testing.T, db *mysql.Instance, id int64, want int32, timeout time.Duration) model.ExportTask {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var task model.ExportTask
		if err := db.GetDb().First(&task, id).Error; err != nil {
			t.Fatalf("轮询任务 %d 失败: %v", id, err)
		}
		if task.Status == want {
			return task
		}
		if task.Status == model.ExportTaskStatusFailed {
			t.Fatalf("任务 %d 提前失败: %s", id, task.ErrorMsg)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("任务 %d 在 %v 内未到达状态 %d", id, timeout, want)
	return model.ExportTask{}
}

// cleanExportTasks 删除导出测试产生的任务行与文件（按管理员 ID 段圈定）
func cleanExportTasks(t *testing.T, db *mysql.Instance) {
	t.Helper()
	var tasks []model.ExportTask
	if err := db.GetDb().Where("admin_id >= ?", expTestAdminBase).Find(&tasks).Error; err != nil {
		t.Logf("清理导出任务查询失败: %v", err)
		return
	}
	if root, err := exportRootDir(); err == nil {
		for _, task := range tasks {
			if task.FileName != "" {
				_ = os.Remove(filepath.Join(root, task.FileName))
			}
			if task.FilePath != "" {
				_ = os.Remove(filepath.Join(root, filepath.FromSlash(task.FilePath)))
			}
		}
	}
	_ = db.GetDb().Where("admin_id >= ?", expTestAdminBase).Delete(&model.ExportTask{}).Error
}

// exportRoot 测试用导出根目录（exportRootDir 便捷包装，失败直接 Fatal）
func exportRoot(t *testing.T) string {
	t.Helper()
	root, err := exportRootDir()
	if err != nil {
		t.Fatalf("定位导出目录失败: %v", err)
	}
	return root
}

// buildQueryJSON 组装与 gateway 口径一致的查询条件快照
func buildQueryJSON(t *testing.T, orderNoPrefix string) string {
	t.Helper()
	b, err := json.Marshal(map[string]interface{}{
		"community_id":    1, // seedSchedOrder 固定写入社区 1
		"order_no":        orderNoPrefix,
		"include_deleted": true,
	})
	if err != nil {
		t.Fatalf("组装查询快照失败: %v", err)
	}
	return string(b)
}

// TestExportTaskLifecycle 创建→异步生成→下载留痕→超管删除 全链路
func TestExportTaskLifecycle(t *testing.T) {
	db, _, exportSvc := newExportTestEnv(t)
	ctx := context.Background()
	defer cleanExportTasks(t, db)

	// 造数：同前缀 2 笔订单（-A 含商品明细 2 件），前缀查询只命中这两笔
	prefix := fmt.Sprintf("%sexp-life-%d", schedTestOrderNoPrefix, time.Now().UnixNano())
	o1 := seedSchedOrder(t, db, "exp-life-a", 2, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	seedSchedItem(t, db, o1, 45001, 2)
	_ = db.GetDb().Exec("UPDATE orders SET order_no = ? WHERE id = ?", prefix+"-A", o1).Error
	o2 := seedSchedOrder(t, db, "exp-life-b", 5, schedTimeAgo(2*time.Hour), schedTimeAgo(2*time.Hour))
	_ = db.GetDb().Exec("UPDATE orders SET order_no = ? WHERE id = ?", prefix+"-B", o2).Error

	req := &order.CreateExportTaskReq{
		ExportType:  model.ExportTypeOrders,
		QueryJson:   buildQueryJSON(t, prefix),
		QueryDesc:   "测试前缀筛选",
		AdminId:     expTestAdminBase + 1,
		AdminName:   "导出测试员",
		CommunityId: 1,
	}

	t.Run("创建立即返回并异步完成", func(t *testing.T) {
		id, err := exportSvc.CreateExportTask(ctx, req)
		if err != nil {
			t.Fatalf("创建导出任务失败: %v", err)
		}
		task := waitExportStatus(t, db, id, model.ExportTaskStatusDone, 10*time.Second)

		// 行数与筛选一致：仅命中 2 笔前缀订单
		if task.RowCount != 2 {
			t.Fatalf("row_count = %d, want 2（前缀筛选应只命中 2 笔订单）", task.RowCount)
		}
		if task.FileSize <= 0 || task.FilePath == "" {
			t.Fatalf("文件信息缺失: path=%q size=%d", task.FilePath, task.FileSize)
		}
		if task.ExpiresAt == nil || *task.ExpiresAt == "" {
			t.Fatal("完成态应刷新过期时间")
		}
		if task.AdminName != "导出测试员" || task.CommunityID != 1 {
			t.Fatalf("创建人快照不符: %+v", task)
		}

		// 文件内容：BOM + 表头 + 两笔订单号 + 商品明细与状态文案口径
		full, err := os.ReadFile(filepath.Join(exportRoot(t), filepath.FromSlash(task.FilePath)))
		if err != nil {
			t.Fatalf("读取导出文件失败: %v", err)
		}
		content := string(full)
		if !strings.HasPrefix(content, "\ufeff") {
			t.Error("导出文件应以 BOM 开头（Excel 中文兼容）")
		}
		for _, want := range []string{prefix + "-A", prefix + "-B", "调度器测试商品 x2", "待发货", "已完成"} {
			if !strings.Contains(content, want) {
				t.Errorf("CSV 缺少 %q，实际:\n%s", want, content)
			}
		}
	})

	t.Run("下载留痕次数与名单", func(t *testing.T) {
		id, err := exportSvc.CreateExportTask(ctx, req)
		if err != nil {
			t.Fatalf("创建导出任务失败: %v", err)
		}
		waitExportStatus(t, db, id, model.ExportTaskStatusDone, 10*time.Second)

		for i := 0; i < 2; i++ {
			if _, _, err := exportSvc.GetExportTaskFile(ctx, &order.GetExportTaskFileReq{
				Id: id, AdminId: expTestAdminBase + 1, AdminName: "导出测试员",
			}); err != nil {
				t.Fatalf("第 %d 次下载失败: %v", i+1, err)
			}
		}
		var task model.ExportTask
		if err := db.GetDb().First(&task, id).Error; err != nil {
			t.Fatalf("回查任务失败: %v", err)
		}
		if task.DownloadCount != 2 {
			t.Fatalf("download_count = %d, want 2", task.DownloadCount)
		}
		var records []map[string]interface{}
		if err := json.Unmarshal([]byte(task.Downloaders), &records); err != nil {
			t.Fatalf("留痕 JSON 非法: %v", err)
		}
		if len(records) != 2 || records[1]["admin_name"] != "导出测试员" {
			t.Fatalf("留痕名单不符: %s", task.Downloaders)
		}
	})

	t.Run("超管删除文件与记录一并删除", func(t *testing.T) {
		id, err := exportSvc.CreateExportTask(ctx, req)
		if err != nil {
			t.Fatalf("创建导出任务失败: %v", err)
		}
		task := waitExportStatus(t, db, id, model.ExportTaskStatusDone, 10*time.Second)
		full := filepath.Join(exportRoot(t), filepath.FromSlash(task.FilePath))
		if _, err := os.Stat(full); err != nil {
			t.Fatalf("生成文件应存在: %v", err)
		}
		// 服务层自带超管闸：绕过网关直调 gRPC 也拦得住
		if err := exportSvc.DeleteExportTask(ctx, id, false); err == nil {
			t.Fatal("非超管删除应被拒绝")
		}
		if err := exportSvc.DeleteExportTask(ctx, id, true); err != nil {
			t.Fatalf("删除导出任务失败: %v", err)
		}
		if _, err := os.Stat(full); !os.IsNotExist(err) {
			t.Error("删除任务后文件应一并删除")
		}
		var n int64
		db.GetDb().Model(&model.ExportTask{}).Where("id = ?", id).Count(&n)
		if n != 0 {
			t.Fatal("删除任务后记录应不存在")
		}
	})
}

// TestExportTaskCreateLimit 在途任务限流与类型校验
func TestExportTaskCreateLimit(t *testing.T) {
	db, _, exportSvc := newExportTestEnv(t)
	ctx := context.Background()
	defer cleanExportTasks(t, db)

	adminID := expTestAdminBase + 2
	// 直插 2 条待处理任务（绕过异步生成，保证计数确定性）
	for i := 0; i < int(exportTaskMaxRunning); i++ {
		row := &model.ExportTask{
			TaskNo:      fmt.Sprintf("EXPORTTASK-limit-%d-%d", adminID, i),
			ExportType:  model.ExportTypeOrders,
			Status:      model.ExportTaskStatusPending,
			AdminID:     adminID,
			CommunityID: 1,
		}
		if err := db.GetDb().Create(row).Error; err != nil {
			t.Fatalf("预置在途任务失败: %v", err)
		}
	}
	_, err := exportSvc.CreateExportTask(ctx, &order.CreateExportTaskReq{
		ExportType: model.ExportTypeOrders, AdminId: adminID, CommunityId: 1,
	})
	if err == nil {
		t.Fatal("在途任务满 2 条后应拒绝创建")
	}
	if got := bizCodeOf(err); got != codes.FailedPrecondition {
		t.Fatalf("错误码 = %v, want FailedPrecondition, err=%v", got, err)
	}

	// 不支持的导出类型直接拒绝
	_, err = exportSvc.CreateExportTask(ctx, &order.CreateExportTaskReq{ExportType: "unknown", AdminId: adminID})
	if got := bizCodeOf(err); got != codes.InvalidArgument {
		t.Fatalf("未知导出类型错误码 = %v, want InvalidArgument", got)
	}
}

// TestExportTaskVisibility 可见性与下载权限
func TestExportTaskVisibility(t *testing.T) {
	db, _, exportSvc := newExportTestEnv(t)
	ctx := context.Background()
	defer cleanExportTasks(t, db)

	// 直插已完成任务：普通管理员 A（社区 1）/ 普通管理员 B（社区 1）/ 超管任务（社区 0）
	// 文件实体不存在，可见性校验通过后应报「文件不存在」而非「无权」
	seedDone := func(adminID int64, name string, community int64) int64 {
		row := &model.ExportTask{
			TaskNo:     fmt.Sprintf("EXPORTTASK-vis-%d-%d", adminID, time.Now().UnixNano()),
			ExportType: model.ExportTypeOrders, Status: model.ExportTaskStatusDone,
			FileName: fmt.Sprintf("orders-%d.csv", adminID), AdminID: adminID, AdminName: name,
			CommunityID: community, FilePath: fmt.Sprintf("orders-%d.csv", adminID),
		}
		if err := db.GetDb().Create(row).Error; err != nil {
			t.Fatalf("预置任务失败: %v", err)
		}
		return row.ID
	}
	adminA, adminB, superID := expTestAdminBase+11, expTestAdminBase+12, expTestAdminBase+13
	idA, idB, idSuper := seedDone(adminA, "管理员A", 1), seedDone(adminB, "管理员B", 1), seedDone(superID, "超管", 0)

	t.Run("普通管理员仅见自己的记录与角标", func(t *testing.T) {
		list, total, undownloaded, err := exportSvc.ListExportTasks(ctx, &order.ListExportTasksReq{AdminId: adminA})
		if err != nil {
			t.Fatalf("查询任务列表失败: %v", err)
		}
		if total != 1 || len(list) != 1 || list[0].Id != idA {
			t.Fatalf("管理员 A 应只看到自己的 1 条记录, total=%d list=%+v", total, list)
		}
		if undownloaded != 1 {
			t.Fatalf("管理员 A 未下载数 = %d, want 1", undownloaded)
		}
		count, err := exportSvc.CountUndownloaded(ctx, &order.CountUndownloadedReq{AdminId: adminB})
		if err != nil || count != 1 {
			t.Fatalf("管理员 B 角标 = %d, want 1, err=%v", count, err)
		}
	})

	t.Run("超管全量可见且社区语境过滤", func(t *testing.T) {
		_, total, _, err := exportSvc.ListExportTasks(ctx, &order.ListExportTasksReq{AdminId: superID, SuperAdmin: true})
		if err != nil {
			t.Fatalf("超管查询失败: %v", err)
		}
		if total < 3 {
			t.Fatalf("超管应至少看到 3 条记录（A/B/超管，含其他残留不封顶）, total=%d", total)
		}
		// 限定社区 1：社区 1 创建人任务 + 跨社区（0）任务均可见
		listC1, totalC1, _, err := exportSvc.ListExportTasks(ctx, &order.ListExportTasksReq{AdminId: superID, SuperAdmin: true, CommunityId: 1})
		if err != nil || totalC1 < 3 {
			t.Fatalf("超管限定社区 1 应看到 A/B/超管任务, total=%d err=%v", totalC1, err)
		}
		seen := map[int64]bool{}
		for _, it := range listC1 {
			seen[it.Id] = true
		}
		for _, want := range []int64{idA, idB, idSuper} {
			if !seen[want] {
				t.Fatalf("超管限定社区 1 的列表应含任务 %d, 实际 %+v", want, listC1)
			}
		}
	})

	t.Run("跨创建人下载被拒本人放行", func(t *testing.T) {
		_, _, err := exportSvc.GetExportTaskFile(ctx, &order.GetExportTaskFileReq{
			Id: idA, AdminId: adminB, SuperAdmin: false, CommunityId: 1,
		})
		if got := bizCodeOf(err); got != codes.PermissionDenied {
			t.Fatalf("管理员 B 下载 A 的文件错误码 = %v, want PermissionDenied", got)
		}
		if _, _, err := exportSvc.GetExportTaskFile(ctx, &order.GetExportTaskFileReq{
			Id: idA, AdminId: adminA, SuperAdmin: false, CommunityId: 1,
		}); err != nil {
			if got := bizCodeOf(err); got == codes.PermissionDenied {
				t.Fatalf("创建人本人下载不应被拒: %v", err)
			}
		}
		if _, _, err := exportSvc.GetExportTaskFile(ctx, &order.GetExportTaskFileReq{
			Id: idA, AdminId: superID, SuperAdmin: true, CommunityId: 0,
		}); err != nil {
			if got := bizCodeOf(err); got == codes.PermissionDenied {
				t.Fatalf("超管下载不应被拒: %v", err)
			}
		}
	})

	t.Run("非完成态与不存在任务不可下载", func(t *testing.T) {
		pending := &model.ExportTask{
			TaskNo:     fmt.Sprintf("EXPORTTASK-pending-%d", time.Now().UnixNano()),
			ExportType: model.ExportTypeOrders, Status: model.ExportTaskStatusPending,
			AdminID: adminA, CommunityID: 1,
		}
		if err := db.GetDb().Create(pending).Error; err != nil {
			t.Fatalf("预置待处理任务失败: %v", err)
		}
		_, _, err := exportSvc.GetExportTaskFile(ctx, &order.GetExportTaskFileReq{
			Id: pending.ID, AdminId: adminA, SuperAdmin: false, CommunityId: 1,
		})
		if got := bizCodeOf(err); got != codes.FailedPrecondition {
			t.Fatalf("非完成态下载错误码 = %v, want FailedPrecondition", got)
		}
		if _, _, err := exportSvc.GetExportTaskFile(ctx, &order.GetExportTaskFileReq{
			Id: 0, AdminId: adminA,
		}); bizCodeOf(err) != codes.NotFound {
			t.Fatal("不存在的任务应返回 NotFound")
		}
	})
}

// TestExportTaskCleanupExpired 过期清理：删文件与记录，未过期保留
func TestExportTaskCleanupExpired(t *testing.T) {
	db, _, exportSvc := newExportTestEnv(t)
	ctx := context.Background()
	defer cleanExportTasks(t, db)

	// 造真实文件：过期任务持有实体文件，清理后应消失
	root := exportRoot(t)
	expiredFile := filepath.Join(root, "orders-expired-test.csv")
	if err := os.WriteFile(expiredFile, []byte("a,b\n"), 0o644); err != nil {
		t.Fatalf("预置过期文件失败: %v", err)
	}
	stale := &model.ExportTask{
		TaskNo: "EXPORTTASK-stale", ExportType: model.ExportTypeOrders,
		Status: model.ExportTaskStatusDone, FileName: "orders-expired-test.csv",
		FilePath: "orders-expired-test.csv", AdminID: expTestAdminBase + 21, CommunityID: 1,
		ExpiresAt: ptrString(time.Now().Add(-time.Hour).Format("2006-01-02 15:04:05")),
	}
	fresh := &model.ExportTask{
		TaskNo: "EXPORTTASK-fresh", ExportType: model.ExportTypeOrders,
		Status: model.ExportTaskStatusDone, AdminID: expTestAdminBase + 21, CommunityID: 1,
		ExpiresAt: ptrString(time.Now().Add(time.Hour).Format("2006-01-02 15:04:05")),
	}
	for _, row := range []*model.ExportTask{stale, fresh} {
		if err := db.GetDb().Create(row).Error; err != nil {
			t.Fatalf("预置任务失败: %v", err)
		}
	}

	cleaned := exportSvc.CleanupExpired(ctx)
	if cleaned == 0 {
		t.Fatal("应至少清理 1 条过期任务")
	}
	if _, err := os.Stat(expiredFile); !os.IsNotExist(err) {
		t.Error("过期任务文件应被删除")
	}
	var nStale, nFresh int64
	db.GetDb().Model(&model.ExportTask{}).Where("task_no = ?", "EXPORTTASK-stale").Count(&nStale)
	db.GetDb().Model(&model.ExportTask{}).Where("task_no = ?", "EXPORTTASK-fresh").Count(&nFresh)
	if nStale != 0 || nFresh != 1 {
		t.Fatalf("清理结果不符: 过期=%d 未过期=%d（应 0/1）", nStale, nFresh)
	}
}

// TestListOrdersFilterPayNoProduct ListOrders 新过滤：pay_no 命中集/空集、product_id 含全部规格
func TestListOrdersFilterPayNoProduct(t *testing.T) {
	db, svc, _ := newExportTestEnv(t)
	ctx := context.Background()

	prefix := fmt.Sprintf("%sexp-filter-%d", schedTestOrderNoPrefix, time.Now().UnixNano())
	o1 := seedSchedOrder(t, db, "exp-filter-a", 2, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	seedSchedItem(t, db, o1, 46001, 1)
	seedSchedItem(t, db, o1, 46002, 2) // 同单多商品行
	// seedSchedItem 固定写 product_id=1（桩数据），按商品过滤前回填真实商品 ID
	_ = db.GetDb().Exec("UPDATE order_items SET product_id = 46001 WHERE order_id = ? AND sku_id = 46001", o1).Error
	_ = db.GetDb().Exec("UPDATE order_items SET product_id = 46002 WHERE order_id = ? AND sku_id = 46002", o1).Error
	_ = db.GetDb().Exec("UPDATE orders SET order_no = ? WHERE id = ?", prefix+"-A", o1).Error
	o2 := seedSchedOrder(t, db, "exp-filter-b", 2, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	seedSchedItem(t, db, o2, 46003, 1)
	_ = db.GetDb().Exec("UPDATE order_items SET product_id = 46003 WHERE order_id = ? AND sku_id = 46003", o2).Error
	_ = db.GetDb().Exec("UPDATE orders SET order_no = ? WHERE id = ?", prefix+"-B", o2).Error

	t.Run("pay_no 命中集精确过滤", func(t *testing.T) {
		list, total, err := svc.ListOrders(ctx, &order.ListOrdersReq{
			CommunityId: 1, OrderNo: prefix, HasPayNo: true, PayOrderNos: []string{prefix + "-A"},
		})
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		if total != 1 || len(list) != 1 || list[0].OrderNo != prefix+"-A" {
			t.Fatalf("pay_no 命中集应只返回 -A 单, total=%d list=%d", total, len(list))
		}
	})

	t.Run("pay_no 反查无命中返回空列表", func(t *testing.T) {
		_, total, err := svc.ListOrders(ctx, &order.ListOrdersReq{
			CommunityId: 1, OrderNo: prefix, HasPayNo: true, PayOrderNos: nil,
		})
		if err != nil || total != 0 {
			t.Fatalf("空命中集应返回空列表, total=%d err=%v", total, err)
		}
	})

	t.Run("product_id 命中含该商品任意规格的订单", func(t *testing.T) {
		// 46001/46002 都只存在于 -A 单：任一商品过滤都应只命中 -A
		for _, pid := range []int64{46001, 46002} {
			list, total, err := svc.ListOrders(ctx, &order.ListOrdersReq{CommunityId: 1, OrderNo: prefix, ProductId: pid})
			if err != nil || total != 1 || len(list) != 1 || list[0].OrderNo != prefix+"-A" {
				t.Fatalf("product_id=%d 应只命中 -A 单, total=%d list=%d err=%v", pid, total, len(list), err)
			}
		}
		// 无此商品的过滤返回空
		_, total, err := svc.ListOrders(ctx, &order.ListOrdersReq{CommunityId: 1, OrderNo: prefix, ProductId: 46999})
		if err != nil || total != 0 {
			t.Fatalf("不存在的商品应返回空列表, total=%d err=%v", total, err)
		}
	})

	t.Run("不过滤时两单齐全", func(t *testing.T) {
		_, total, err := svc.ListOrders(ctx, &order.ListOrdersReq{CommunityId: 1, OrderNo: prefix})
		if err != nil || total != 2 {
			t.Fatalf("基线应为 2 单, total=%d err=%v", total, err)
		}
	})
}

// TestRefundedAmountFill 已退金额批量回填口径（列表售后标记/导出「已退金额」列共用）
// 覆盖：仅统计成功退款单（status=5），退款中/已拒绝不占用；多笔部分退累加；
// 无退款订单按零值处理；空集合不发查询；ListOrders 链路回填生效
//
// 参数：
//   - t: 测试对象
func TestRefundedAmountFill(t *testing.T) {
	db, svc, _ := newExportTestEnv(t)
	ctx := context.Background()

	prefix := fmt.Sprintf("%sexp-refund-%d", schedTestOrderNoPrefix, time.Now().UnixNano())
	oHas := seedSchedOrder(t, db, "exp-refund-a", 2, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	_ = db.GetDb().Exec("UPDATE orders SET order_no = ? WHERE id = ?", prefix+"-A", oHas).Error
	oNone := seedSchedOrder(t, db, "exp-refund-b", 2, schedTimeAgo(time.Hour), schedTimeAgo(time.Hour))
	_ = db.GetDb().Exec("UPDATE orders SET order_no = ? WHERE id = ?", prefix+"-B", oNone).Error
	// 退款单按订单 ID 圈定清理（LIFO：先于订单清理执行）
	t.Cleanup(func() {
		_ = db.GetDb().Exec("DELETE FROM refunds WHERE order_id IN ?", []int64{oHas, oNone}).Error
	})

	// -A：成功 3.50 + 成功 2.00 + 退款中 10.00 + 已拒绝 1.00 → 应只统计 5.50
	seedRefund(t, db, oHas, 5, 3.50)
	seedRefund(t, db, oHas, 5, 2.00)
	seedRefund(t, db, oHas, 4, 10.00)
	seedRefund(t, db, oHas, 3, 1.00)

	t.Run("批量统计仅累计成功退款", func(t *testing.T) {
		amounts, err := refundedAmountsByOrders(db.GetDb().WithContext(ctx), []int64{oHas, oNone})
		if err != nil {
			t.Fatalf("统计失败: %v", err)
		}
		if got := amounts[oHas]; got != 5.50 {
			t.Errorf("已成功退款合计 = %.2f, want 5.50（退款中/已拒绝不计入）", got)
		}
		if _, ok := amounts[oNone]; ok {
			t.Errorf("无退款订单不应出现在结果集中: %v", amounts)
		}
	})

	t.Run("空集合不发查询", func(t *testing.T) {
		amounts, err := refundedAmountsByOrders(db.GetDb().WithContext(ctx), nil)
		if err != nil || len(amounts) != 0 {
			t.Fatalf("空集合应返回空映射, err=%v amounts=%v", err, amounts)
		}
	})

	t.Run("ListOrders 链路回填生效", func(t *testing.T) {
		list, total, err := svc.ListOrders(ctx, &order.ListOrdersReq{CommunityId: 1, OrderNo: prefix})
		if err != nil || total != 2 {
			t.Fatalf("应返回 2 单, total=%d err=%v", total, err)
		}
		byNo := make(map[string]float64, len(list))
		for _, o := range list {
			byNo[o.OrderNo] = o.RefundedAmount
		}
		if got := byNo[prefix+"-A"]; got != 5.50 {
			t.Errorf("列表 -A 已退金额 = %.2f, want 5.50", got)
		}
		if got := byNo[prefix+"-B"]; got != 0 {
			t.Errorf("列表 -B 已退金额 = %.2f, want 0", got)
		}
	})
}

// ptrString 字符串指针便捷包装（expires_at 可空列测试造数用）
func ptrString(s string) *string { return &s }

// bizCodeOf 提取 bizerr 业务错误码（非 bizerr 错误返回 Unknown）
// 服务层返回 bizerr.Error 而非 gRPC status，需 errors.As 解包后再比对
//
// 参数：
//   - err: 服务层返回的错误
//
// 返回值：
//   - codes.Code: 业务错误码
func bizCodeOf(err error) codes.Code {
	var biz *bizerr.Error
	if !errors.As(err, &biz) {
		return codes.Unknown
	}
	return biz.Code()
}
