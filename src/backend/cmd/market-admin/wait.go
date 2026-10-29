package main

// init 的「等待表就绪」公共设施（进度输出 + 失败可见）。
//
// 为什么要这一层：原先 runInit 与 seedDemoPackage 各写一个静默轮询循环，
// 全新库首启时最长 300 秒一个字都不打（外部看就是「卡在初始化开始」），
// 且超时后以 os.Exit(0) 结束——start-all 与 entrypoint 都无法判定失败；
// 种子侧的等待清单还漏了 order/payment 域的表，会在迁移未完成的库上开写，
// 留下「社区已建、业务段残缺」的半导入态。
// 现表清单由各服务 model.Models() 单一来源提供，等待过程按 10 秒节奏打印进度与缺失表名。

import (
	"fmt"
	"os"
	"sort"
	"time"

	"gorm.io/gorm"

	cartModel "github.com/jcbowen/market/src/backend/services/cart/model"
	orderModel "github.com/jcbowen/market/src/backend/services/order/model"
	paymentModel "github.com/jcbowen/market/src/backend/services/payment/model"
	productModel "github.com/jcbowen/market/src/backend/services/product/model"
	userModel "github.com/jcbowen/market/src/backend/services/user/model"
)

// progressEvery 进度打印节奏：太密会淹没导入明细日志，太疏等于没有输出
const progressEvery = 10 * time.Second

// userModels 用户服务迁移模型（超管账号、社区、区划、站点配置等 init 主流程直接依赖的表）
func userModels() []any { return userModel.Models() }

// seedModels 演示种子写入所需的全部模型（五个服务 model.Models() 的并集）
// 返回：
//   - []any: 模型值指针切片，供 waitForTables 解析出表名
//   - 异常：本函数不返回错误
//
// 使用示例：
//
//	waitForTables(db, seedModels(), timeoutSec, "演示种子相关表")
//
// 单一来源口径：新增业务模型只改所属服务的 model.Models()，本清单自动跟随，
// 不再出现「种子段写了一张没等过的表」。
func seedModels() []any {
	out := make([]any, 0, 64)
	out = append(out, userModel.Models()...)
	out = append(out, productModel.Models()...)
	out = append(out, orderModel.Models()...)
	out = append(out, cartModel.Models()...)
	out = append(out, paymentModel.Models()...)
	return out
}

// tableNames 解析模型对应的物理表名（走 GORM 命名策略，与 AutoMigrate 建表同名）
// 参数：
//   - db: GORM 连接（提供 NamingStrategy）
//   - models: 模型值指针切片
//
// 返回：
//   - []string: 去重并排序后的表名；解析失败的模型打印告警后跳过
//   - 异常：本函数不返回错误（解析失败降级为告警，由调用方按「表缺失」处理）
func tableNames(db *gorm.DB, models []any) []string {
	seen := make(map[string]struct{}, len(models))
	out := make([]string, 0, len(models))
	for _, m := range models {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(m); err != nil {
			fmt.Printf("==> [market-admin] 警告: 模型 %T 表名解析失败，按缺失处理: %v\n", m, err)
			continue
		}
		if _, ok := seen[stmt.Table]; ok {
			continue
		}
		seen[stmt.Table] = struct{}{}
		out = append(out, stmt.Table)
	}
	sort.Strings(out)
	return out
}

// missingTables 一次快照比对：返回尚未出现的表名
// 参数：
//   - db: GORM 连接
//   - want: 期望存在的表名（tableNames 的输出）
//
// 返回：
//   - []string: 缺失表名（全部就绪时为空）
//   - error: 读取 information_schema 失败（库不存在/连接中断等，等待也不会自愈，调用方应尽快失败）
func missingTables(db *gorm.DB, want []string) ([]string, error) {
	existing, err := db.Migrator().GetTables()
	if err != nil {
		return nil, err
	}
	has := make(map[string]struct{}, len(existing))
	for _, t := range existing {
		has[t] = struct{}{}
	}
	missing := make([]string, 0, len(want))
	for _, t := range want {
		if _, ok := has[t]; !ok {
			missing = append(missing, t)
		}
	}
	return missing, nil
}

// briefTables 表名列表的简短呈现：超过 6 个只列前 6 个并标注总数，避免进度行过长
func briefTables(tables []string) string {
	if len(tables) == 0 {
		return "无"
	}
	if len(tables) <= 6 {
		return joinNames(tables)
	}
	return fmt.Sprintf("%s 等 %d 张", joinNames(tables[:6]), len(tables))
}

// joinNames 逗号拼接表名
// 参数：
//   - names: 表名切片
//
// 返回：
//   - string: 逗号分隔结果；异常：不返回错误
func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

// waitForTables 轮询等待模型对应的表全部就绪，期间打印进度，超时返回错误
// 参数：
//   - db: GORM 连接
//   - models: 期望已迁移的模型清单（userModels/seedModels）
//   - timeoutSec: 等待上限（秒），沿用 init --timeout
//   - stage: 进度行标识（如「user-service 自动迁移」）
//
// 返回：
//   - error: 超时（含缺失表清单）、数据库不可达，或读表失败；全部就绪返回 nil
//   - 说明：连库类错误连续 3 次即放弃——服务侧同样连不上库，多等只会掩盖真因
//
// 使用示例：
//
//	if err := waitForTables(db, userModels(), timeoutSec, "user-service 自动迁移"); err != nil {
//	    fmt.Printf("==> [market-admin] 初始化中止: %v\n", err)
//	    os.Exit(1)
//	}
func waitForTables(db *gorm.DB, models []any, timeoutSec int, stage string) error {
	want := tableNames(db, models)
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	start := time.Now()
	lastPrint := time.Now()
	consecutiveErr := 0

	fmt.Printf("==> [market-admin] 等待 %s（共 %d 张表，上限 %ds）...\n", stage, len(want), timeoutSec)
	for {
		missing, err := missingTables(db, want)
		if err != nil {
			consecutiveErr++
			if time.Now().After(deadline) || consecutiveErr >= 3 {
				return fmt.Errorf("读取数据库表清单失败（第 %d 次）: %w；请确认 market 库已建、MySQL 可达（口令经 MYSQL_PASSWORD 注入）", consecutiveErr, err)
			}
		} else {
			consecutiveErr = 0
			if len(missing) == 0 {
				fmt.Printf("==> [market-admin] %s 已完成（用时 %ds）\n", stage, int(time.Since(start).Seconds()))
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("等待超时（%ds），仍缺 %d 张表：%s；对应服务未完成自动迁移（多为服务启动失败或连不上库，见 docker compose logs）",
					timeoutSec, len(missing), briefTables(missing))
			}
		}
		if _, err := os.Stat(shutdownMarker); err == nil {
			fmt.Println("==> [market-admin] 检测到容器停止信号，让路退出")
			os.Exit(0)
		}
		if time.Since(lastPrint) >= progressEvery {
			lastPrint = time.Now()
			fmt.Printf("==> [market-admin] 已等待 %ds/%ds（%s），仍缺 %d 张表: %s\n",
				int(time.Since(start).Seconds()), timeoutSec, stage, len(missing), briefTables(missing))
		}
		time.Sleep(2 * time.Second)
	}
}
