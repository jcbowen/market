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

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/services/cart/model"
	productModel "github.com/jcbowen/market/src/backend/services/product/model"
	"github.com/jcbowen/market/src/backend/services/product/service"
	userModel "github.com/jcbowen/market/src/backend/services/user/model"
)

// seedDemoPackage 导入演示数据包（幂等，失败仅打印日志不中断 init 流程：
// 超管/默认社区兜底仍需继续执行；可在服务就绪后重新运行 init 重试）
func seedDemoPackage(inst *mysql.Instance, timeoutSec int) {
	// 生产环境一律拒绝：演示数据含弱口令账号与测试业务数据
	if os.Getenv("APP_ENV") == "production" {
		fmt.Println("==> [market-admin] APP_ENV=production，跳过演示种子导入")
		return
	}
	// 非生产的二次确认：漏配 APP_ENV（如对外环境忘设 production）时，此前会静默灌入
	// 演示数据——其中 admin/admin123、13800138000 是随 git 公开的弱口令。改为必须显式
	// 设置 MARKET_ALLOW_DEMO_SEED=1 才导入（本地开发 compose 已内置），否则跳过并告警。
	if v := strings.TrimSpace(os.Getenv("MARKET_ALLOW_DEMO_SEED")); v != "1" && !strings.EqualFold(v, "true") {
		fmt.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
		fmt.Printf("!! [market-admin] 未设置 MARKET_ALLOW_DEMO_SEED=1，跳过演示种子导入（APP_ENV=%q）\n", os.Getenv("APP_ENV"))
		fmt.Println("!!   演示数据含公开弱口令账号（admin/admin123、13800138000），对外可达环境严禁导入；")
		fmt.Println("!!   仅本地开发/演示需要时显式设置 MARKET_ALLOW_DEMO_SEED=1（或改用 APP_ENV=production 明确上线）")
		fmt.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
		return
	}
	// 显式关闭开关（如长期演示环境已改密、不希望每次 init 重复校验时）
	if v := strings.TrimSpace(os.Getenv("MARKET_SKIP_DEMO_SEED")); v == "1" || strings.EqualFold(v, "true") {
		fmt.Println("==> [market-admin] MARKET_SKIP_DEMO_SEED 已设置，跳过演示种子导入")
		return
	}
	// 确认开关齐备后仍重复告警：演示账号一旦落库即可登录后台/C 端
	fmt.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	fmt.Printf("!! [market-admin] 正在导入演示种子（APP_ENV=%q，含公开弱口令账号），导入后请立即改密\n", os.Getenv("APP_ENV"))
	fmt.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")

	db := inst.GetDb()
	// 等待三个服务的表迁移完成（admin 表由 runInit 主流程等待，这里补齐其余表）
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for {
		ready := db.Migrator().HasTable(&userModel.Community{}) &&
			db.Migrator().HasTable(&userModel.User{}) &&
			db.Migrator().HasTable(&userModel.Region{}) &&
			db.Migrator().HasTable(&userModel.SiteConfig{}) &&
			db.Migrator().HasTable(&productModel.Category{}) &&
			db.Migrator().HasTable(&productModel.Coupon{}) &&
			db.Migrator().HasTable(&model.Cart{})
		if ready {
			break
		}
		if time.Now().After(deadline) {
			fmt.Println("==> [market-admin] 等待演示种子相关表超时（user/cart/product 服务可能未完成迁移），本次跳过演示种子")
			fmt.Println("    稍后可执行: docker exec <容器名> ./market-admin init  重试")
			return
		}
		if _, err := os.Stat(shutdownMarker); err == nil {
			fmt.Println("==> [market-admin] 检测到容器停止信号，让路退出")
			os.Exit(0)
		}
		time.Sleep(2 * time.Second)
	}

	// 构造导入服务（各服务构造仅需 DB 连接，CLI 直连库与 product 服务内同构）
	importSvc := service.NewDataImportService(
		inst,
		service.NewProductService(inst),
		service.NewBannerService(inst),
		service.NewMarketingService(inst),
	)

	res, err := importSvc.SeedDemoBootstrap(context.Background(), func(format string, args ...any) {
		fmt.Printf("==> [market-admin] "+format+"\n", args...)
	})
	if err != nil {
		fmt.Printf("==> [market-admin] 演示种子导入失败: %v\n", err)
		return
	}
	if !res.Ran {
		return
	}
	fmt.Println("==================================================")
	fmt.Printf("==> 演示种子导入完成：新建 %d / 跳过 %d / 失败 %d\n", res.Created, res.Skipped, res.Failed)
	if res.Failed > 0 {
		fmt.Println("    存在失败条目（见上方明细），可重新运行 init 幂等补导")
	}
	if res.DemoAdminCreated {
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
	fmt.Println("==================================================")
}
