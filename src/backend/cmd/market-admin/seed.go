package main

// 演示种子初始化（原 deploy/init.dev.sql 的替代链路）：
// 数据全部来自 product 服务内嵌的演示数据包（seeddata/full-demo.json），
// 经 DataImportService.SeedDemoBootstrap 直连库导入（无 HTTP/token 依赖）：
//   1. 全局段：演示社区、行政区划、管理员账号、C 端用户、站点配置（各表幂等判重）
//   2. 社区域段：商品目录 / Banner / 营销 / 满免邮 / 用户券与购物车 / 演示样本
//     （逐社区导入，条目按 community_key 只落自身归属社区）
// 口径与原 SQL 导入一致：仅 communities 表为空时执行，已有业务数据一律跳过；
// APP_ENV=production 一律拒绝（演示数据含弱口令账号）；非生产环境还须显式设置
// MARKET_ALLOW_DEMO_SEED=1 才导入，避免漏配 APP_ENV 的对外环境静默灌入弱口令账号。
//
// 补导链路：SeedDemoBootstrap 的「communities 非空即跳过」使上次的残缺条目无法经普通
// init 修复，故另开 `init --rerun` → DataImportService.ReRunDemoSections（跳过空库短路，
// 逐条按各自幂等键判重，已有数据全部 skip）。

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/services/product/service"
)

// demoSeedGuard 检查演示种子是否允许导入（环境与显式开关）
// 参数：
//   - requireOptIn: true 时还要求 MARKET_ALLOW_DEMO_SEED=1（自动 init 链路，防漏配
//     APP_ENV 的对外环境静默灌入弱口令）；false 用于 `init --rerun`（人工显式动作）
//
// 返回：
//   - bool: 允许导入返回 true；不允许/应跳过返回 false（原因已打印，本函数不返回错误）
//
// 使用示例：
//
//	if !demoSeedGuard(true) { return nil }
func demoSeedGuard(requireOptIn bool) bool {
	// 生产环境一律拒绝：演示数据含弱口令账号与测试业务数据
	if os.Getenv("APP_ENV") == "production" {
		fmt.Println("==> [market-admin] APP_ENV=production，跳过演示种子导入")
		return false
	}
	if v := strings.TrimSpace(os.Getenv("MARKET_SKIP_DEMO_SEED")); v == "1" || strings.EqualFold(v, "true") {
		fmt.Println("==> [market-admin] MARKET_SKIP_DEMO_SEED 已设置，跳过演示种子导入")
		return false
	}
	if requireOptIn {
		// 非生产的二次确认：漏配 APP_ENV（如对外环境忘设 production）时，此前会静默灌入
		// 演示数据——其中 admin/admin123、13800138000 是随 git 公开的弱口令。改为必须显式
		// 设置 MARKET_ALLOW_DEMO_SEED=1 才导入（本地开发 compose 已内置），否则跳过并告警。
		if v := strings.TrimSpace(os.Getenv("MARKET_ALLOW_DEMO_SEED")); v != "1" && !strings.EqualFold(v, "true") {
			fmt.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
			fmt.Printf("!! [market-admin] 未设置 MARKET_ALLOW_DEMO_SEED=1，跳过演示种子导入（APP_ENV=%q）\n", os.Getenv("APP_ENV"))
			fmt.Println("!!   演示数据含公开弱口令账号（admin/admin123、13800138000），对外可达环境严禁导入；")
			fmt.Println("!!   仅本地开发/演示需要时显式设置 MARKET_ALLOW_DEMO_SEED=1（或改用 APP_ENV=production 明确上线）")
			fmt.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
			return false
		}
	}
	// 确认开关齐备后仍重复告警：演示账号一旦落库即可登录后台/C 端
	fmt.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	fmt.Printf("!! [market-admin] 正在导入演示种子（APP_ENV=%q，含公开弱口令账号），导入后请立即改密\n", os.Getenv("APP_ENV"))
	fmt.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	return true
}

// newDemoImportService 构造直连库的演示导入服务
// 参数：
//   - inst: MySQL 实例
//
// 返回：
//   - *service.DataImportService: 已注册演示数据包的导入服务
//     （各服务构造仅需 DB 连接，CLI 直连库与 product 服务内同构）
//   - 异常：本函数不做 IO，不返回错误
func newDemoImportService(inst *mysql.Instance) *service.DataImportService {
	return service.NewDataImportService(
		inst,
		service.NewProductService(inst),
		service.NewBannerService(inst),
		service.NewMarketingService(inst),
	)
}

// seedDemoPackage 首次初始化时导入演示数据包（仅空库生效）
// 参数：
//   - inst: MySQL 实例
//   - timeoutSec: 等待各服务表迁移的上限（秒）
//
// 返回：
//   - error: 等待迁移超时、导入报错或存在失败条目（调用方据此以非 0 退出码反馈）；
//     环境/开关不允许或库已初始化时返回 nil（属正常跳过）
//
// 使用示例：
//
//	if err := seedDemoPackage(inst, 300); err != nil { ... }
func seedDemoPackage(inst *mysql.Instance, timeoutSec int) error {
	if !demoSeedGuard(true) {
		return nil
	}

	db := inst.GetDb()
	// 等待全部五个服务的表迁移完成（种子各段写入 user/product/order/cart/payment 域的表，
	// 清单由 model.Models() 单一来源给出；此前只等 7 张表，会在订单域未就绪时开写出残缺态）
	if err := waitForTables(db, seedModels(), timeoutSec, "演示种子相关表（五个服务）"); err != nil {
		fmt.Printf("==> [market-admin] 本次跳过演示种子: %v\n", err)
		fmt.Println("    稍后可执行: docker exec <容器名> ./market-admin init --rerun  补导")
		return err
	}

	res, err := newDemoImportService(inst).SeedDemoBootstrap(context.Background(), logSeed)
	if err != nil {
		fmt.Printf("==> [market-admin] 演示种子导入失败: %v\n", err)
		return err
	}
	if !res.Ran {
		// 空库短路命中：库里已有社区，视为已初始化。上次若中断留下的残缺条目不会被本次补齐，
		// 需要显式 --rerun（逐条幂等），不能说「重跑 init 即可」——那是错的
		fmt.Println("    提示: 若怀疑上次演示种子有残缺，执行 ./market-admin init --rerun 逐条幂等补导")
		return nil
	}
	fmt.Println("==================================================")
	fmt.Printf("==> 演示种子导入完成：新建 %d / 跳过 %d / 失败 %d\n", res.Created, res.Skipped, res.Failed)
	reportDemoAdmin(res)
	fmt.Println("==================================================")
	if res.Failed > 0 {
		// 逐条幂等复跑收敛到完整态：整体事务式回滚会把后台 Excel 导入共用的
		// 「单条失败不中断整包」语义一起改掉，故此处以「可见 + 可补齐」收口
		return fmt.Errorf("演示种子有 %d 条条目导入失败（明细见上方 [段名] 行），库处于部分导入态", res.Failed)
	}
	return nil
}

// rerunDemoSeed 幂等复跑全部演示段，补齐上次残缺（`init --rerun`）
// 参数：
//   - inst: MySQL 实例
//   - timeoutSec: 等待各服务表迁移的上限（秒）
//
// 返回：
//   - error: 等待迁移超时或仍有失败条目；补齐无失败时返回 nil
//
// 使用示例：
//
//	docker exec market-gateway ./market-admin init --rerun
func rerunDemoSeed(inst *mysql.Instance, timeoutSec int) error {
	if !demoSeedGuard(false) {
		return nil
	}
	db := inst.GetDb()
	if err := waitForTables(db, seedModels(), timeoutSec, "演示种子相关表（五个服务）"); err != nil {
		fmt.Printf("==> [market-admin] 补导中止: %v\n", err)
		return err
	}

	fmt.Println("==> [market-admin] 幂等复跑演示段（已有条目跳过，不覆盖不改密）")
	created, skipped, failed, err := newDemoImportService(inst).ReRunDemoSections(context.Background(), logSeed)
	if err != nil {
		fmt.Printf("==> [market-admin] 补导失败: %v\n", err)
		return err
	}
	fmt.Println("==================================================")
	fmt.Printf("==> 演示种子补导完成：新建 %d / 跳过 %d / 失败 %d\n", created, skipped, failed)
	fmt.Println("==================================================")
	if failed > 0 {
		return fmt.Errorf("补导后仍有 %d 条失败，库处于部分导入态（明细见上方 [段名] 行）", failed)
	}
	return nil
}

// logSeed 演示种子导入的进度打印器（统一加 [market-admin] 前缀）
// 参数：
//   - format: 格式串
//   - args: 格式参数
//
// 返回：无；异常：不返回错误
func logSeed(format string, args ...any) {
	fmt.Printf("==> [market-admin] "+format+"\n", args...)
}

// reportDemoAdmin 打印本次新建的演示超管凭证并落凭证文件
// 参数：
//   - res: 演示种子导入结果
//
// 返回：无；异常：凭证文件写入失败忽略（与本工具创建超管同口径，stdout 已有凭据）
func reportDemoAdmin(res *service.DemoSeedResult) {
	if !res.DemoAdminCreated {
		return
	}
	fmt.Printf("    演示超管: %s / %s（弱口令，对外演示环境上线前务必改密）\n",
		res.DemoAdminUsername, res.DemoAdminPassword)
	// 与本工具创建的超管同口径写入凭证文件（幂等覆盖为最近一次记录）
	_ = saveCredentials(adminCredentials{
		Username:  res.DemoAdminUsername,
		Password:  res.DemoAdminPassword,
		UpdatedAt: time.Now().Format("2006-01-02 15:04:05"),
		Note:      "演示种子内置账号（弱口令，仅限演示环境）",
	})
}
