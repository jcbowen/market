package service

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/services/user/model"
)

/**
 * 区划差异覆盖（ApplyRegionUpdateTask）集成回归测试，直连测试库，env 守卫跳过
 *
 * 运行方式（需先起本地 MySQL）：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/user/service/ -run TestApplyRegionUpdateTask -v
 *
 * 覆盖点（对应 E2「循环内逐条写改批量」后的行为契约）：
 *   - added   批量插入生效，created_at 显式赋值不为空
 *   - updated 整批 upsert 覆盖名称/父码/层级/状态，且不回写已存在行的 created_at
 *   - updated 命中不到行（被并发删除）时按远端数据补回，与「以远端为准覆盖」一致
 *   - removed 软标记停用，不物理删除
 *   - applied 标记幂等：重复应用报错，差异不会二次落库
 *
 * 测试数据统一使用 TEST 前缀区划代码（真实区划代码恒为纯数字，不可能冲突），结束即清理。
 */

// regionTestCodes 本用例写入的区划代码清单，作为清理依据
var regionTestCodes = []string{"TESTADD1", "TESTUPD1", "TESTUPD2", "TESTDEL1"}

// regionTestTaskSeq 任务序号，保证同一轮内 source 标记唯一
var regionTestTaskSeq int64

// regionTestEnv 测试环境句柄
type regionTestEnv struct {
	t       *testing.T
	db      *mysql.Instance
	service *RegionService
}

// regionEnvOr 读取测试环境变量，缺省时回落本地默认值
//
// 参数：
//   - key: 环境变量名
//   - def: 缺省值
//
// 返回值：
//   - string: 环境变量值或缺省值
func regionEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// newRegionTestEnv 建测试库连接、迁移区划相关表，并注册清理钩子
// 表结构统一走 AutoMigrate（与 main.go 同一模型清单），本地存量库与 CI 空库均幂等
//
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *regionTestEnv: 被测服务句柄；TEST_MYSQL_PASSWORD 未设置时 skip
func newRegionTestEnv(t *testing.T) *regionTestEnv {
	t.Helper()
	if os.Getenv("TEST_MYSQL_PASSWORD") == "" {
		t.Skip("TEST_MYSQL_PASSWORD 未设置，跳过区划差异覆盖集成测试")
	}
	dbInst, err := mysql.New(jcbaseGo.DbStruct{
		Host:      regionEnvOr("TEST_MYSQL_HOST", "127.0.0.1"),
		Port:      regionEnvOr("TEST_MYSQL_PORT", "3306"),
		Dbname:    regionEnvOr("TEST_MYSQL_DBNAME", "market"),
		Username:  regionEnvOr("TEST_MYSQL_USER", "root"),
		Password:  os.Getenv("TEST_MYSQL_PASSWORD"),
		Charset:   "utf8mb4",
		ParseTime: "True",
		Loc:       "Local",
	})
	if err != nil {
		t.Fatalf("连接测试 MySQL 失败: %v", err)
	}
	if err := dbInst.GetDb().AutoMigrate(&model.Region{}, &model.RegionUpdateTask{}); err != nil {
		t.Fatalf("迁移区划表失败: %v", err)
	}
	// 先删净历史残留（上一轮异常中断时钩子不会执行），再注册本轮清理
	clearRegionTestData(t, dbInst)
	t.Cleanup(func() { clearRegionTestData(t, dbInst) })
	return &regionTestEnv{t: t, db: dbInst, service: NewRegionService(dbInst)}
}

// clearRegionTestData 删除本用例写入的测试区划与任务
//
// 参数：
//   - t: 测试对象（清理失败仅告警，不影响断言结论）
//   - dbInst: 测试库句柄
func clearRegionTestData(t *testing.T, dbInst *mysql.Instance) {
	t.Helper()
	if err := dbInst.GetDb().Where("code IN ?", regionTestCodes).Delete(&model.Region{}).Error; err != nil {
		t.Logf("清理测试区划失败（需手工核对 TEST 前缀行）: %v", err)
	}
	if err := dbInst.GetDb().Where("source LIKE ?", "REGIONTEST-%").Delete(&model.RegionUpdateTask{}).Error; err != nil {
		t.Logf("清理测试任务失败（需手工核对 REGIONTEST- 前缀任务）: %v", err)
	}
}

// seedRegion 写入一条测试区划并把 created_at 固定为哨兵值
// 需插入后二次回写：base 的 BeforeCreate 钩子无条件覆盖创建时间，
// 只有显式更新才能验证「upsert 不覆盖已存在行的 created_at」
//
// 参数：
//   - code: 区划代码
//   - name: 区划名称
//   - status: 状态
//
// 返回值：
//   - error: 写库失败原因
func (env *regionTestEnv) seedRegion(code, name string, status int32) error {
	row := &model.Region{Code: code, Name: name, ParentCode: "TESTPAR", Level: 3, Status: status}
	if err := env.db.GetDb().Create(row).Error; err != nil {
		return err
	}
	return env.db.GetDb().Model(&model.Region{}).Where("code = ?", code).
		Update("created_at", "2020-01-01 00:00:00").Error
}

// applyDiff 构造一个已完成待覆盖的任务并执行差异覆盖
//
// 参数：
//   - added: 新增差异明细
//   - updated: 更名差异明细
//   - removed: 停用差异明细
//
// 返回值：
//   - int64: 任务 ID
//   - error: ApplyRegionUpdateTask 返回的错误
//
// 异常：
//   - 差异 JSON 序列化或任务写入失败时直接 t.Fatal（属测试脚手架故障）
func (env *regionTestEnv) applyDiff(added, updated, removed []RegionDiffItem) (int64, error) {
	task := &model.RegionUpdateTask{
		Status: model.RegionTaskStatusDone,
		Source: "REGIONTEST-" + strconv.FormatInt(atomic.AddInt64(&regionTestTaskSeq, 1), 10),
	}
	for _, pair := range []struct {
		items []RegionDiffItem
		dest  *string
	}{
		{added, &task.AddedJSON},
		{updated, &task.UpdatedJSON},
		{removed, &task.RemovedJSON},
	} {
		raw, err := json.Marshal(pair.items)
		if err != nil {
			env.t.Fatalf("序列化差异明细失败: %v", err)
		}
		*pair.dest = string(raw)
	}
	if err := env.db.GetDb().Create(task).Error; err != nil {
		env.t.Fatalf("写入测试任务失败: %v", err)
	}
	return task.ID, env.service.ApplyRegionUpdateTask(context.Background(), task.ID)
}

// TestApplyRegionUpdateTask 验证差异覆盖的三类写入行为与 applied 幂等标记
//
// 参数：
//   - t: 测试对象
func TestApplyRegionUpdateTask(t *testing.T) {
	env := newRegionTestEnv(t)
	db := env.db.GetDb()

	// 预置待更名行与待停用行；TESTUPD2 故意不预置，用于验证 upsert 命中不到行时补回
	for _, seed := range []struct{ code, name string }{
		{"TESTUPD1", "旧名"},
		{"TESTDEL1", "待停用"},
	} {
		if err := env.seedRegion(seed.code, seed.name, model.RegionStatusActive); err != nil {
			t.Fatalf("预置区划 %s 失败: %v", seed.code, err)
		}
	}

	taskID, err := env.applyDiff(
		[]RegionDiffItem{{Code: "TESTADD1", Name: "新增县", ParentCode: "TESTPAR", Level: 3}},
		[]RegionDiffItem{
			{Code: "TESTUPD1", Name: "旧名", NewName: "新名", ParentCode: "TESTPARENT", Level: 2},
			{Code: "TESTUPD2", Name: "已被删除", NewName: "补回名", ParentCode: "TESTPAR", Level: 3},
		},
		[]RegionDiffItem{{Code: "TESTDEL1", Name: "待停用", ParentCode: "TESTPAR", Level: 3}},
	)
	if err != nil {
		t.Fatalf("覆盖差异失败: %v", err)
	}

	var added model.Region
	if err := db.Where("code = ?", "TESTADD1").First(&added).Error; err != nil {
		t.Fatalf("新增区划未写入: %v", err)
	}
	if added.Status != model.RegionStatusActive || added.Name != "新增县" {
		t.Errorf("新增区划字段不符: %+v", added)
	}
	if added.CreatedAt == "" {
		t.Error("批量插入未显式赋值 created_at，写入 datetime 会报 1292")
	}

	var renamed model.Region
	if err := db.Where("code = ?", "TESTUPD1").First(&renamed).Error; err != nil {
		t.Fatalf("更名区划查询失败: %v", err)
	}
	if renamed.Name != "新名" || renamed.ParentCode != "TESTPARENT" || renamed.Level != 2 {
		t.Errorf("更名差异未按整批 upsert 覆盖: %+v", renamed)
	}
	// upsert 覆盖列不含 created_at：已存在行的创建时间必须原样保留
	// （连接串开启 parseTime，DATETIME 回读为 RFC3339 形态，故只比对日期部分）
	if !strings.HasPrefix(renamed.CreatedAt, "2020-01-01") {
		t.Errorf("更名覆盖不应回写 created_at, got %q", renamed.CreatedAt)
	}

	var reinserted model.Region
	if err := db.Where("code = ?", "TESTUPD2").First(&reinserted).Error; err != nil {
		t.Fatalf("更新差异命中不到行时应按远端数据补回: %v", err)
	}
	if reinserted.Name != "补回名" || reinserted.Status != model.RegionStatusActive {
		t.Errorf("补回区划字段不符: %+v", reinserted)
	}

	var disabled model.Region
	if err := db.Where("code = ?", "TESTDEL1").First(&disabled).Error; err != nil {
		t.Fatalf("停用区划不应被物理删除: %v", err)
	}
	if disabled.Status != model.RegionStatusDisabled {
		t.Errorf("停用区划状态不符: %+v", disabled)
	}

	// 幂等：applied 置位后重复覆盖应报错，差异不会二次落库
	if err := env.service.ApplyRegionUpdateTask(context.Background(), taskID); err == nil {
		t.Error("重复应用差异应报错")
	} else if !strings.Contains(err.Error(), "已应用") {
		t.Errorf("重复应用错误信息应提示已应用, got %v", err)
	}
}
