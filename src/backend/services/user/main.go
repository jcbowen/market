package main

import (
	stdctx "context"
	"log"

	"google.golang.org/grpc"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/internal/bootstrap"
	"github.com/jcbowen/market/src/backend/services/user/model"
	"github.com/jcbowen/market/src/backend/services/user/service"
)

/**
 * main user-service 主入口
 * 用户服务：注册/登录、授权、地址管理、会员管理、社区管理
 */
func main() {
	cfg := bootstrap.LoadConfig()

	db, err := bootstrap.InitMySQL(cfg)
	if err != nil {
		log.Fatalf("初始化 MySQL 失败: %v", err)
	}
	if err := autoMigrate(db); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}
	// 受控例外（已登记 docs/需求文档/05-数据库设计/01-设计规范.md 的「手写 DDL 受控例外台账」）：
	// AutoMigrate 只增不减，废弃列清理属其能力缺口，不是绕过规范的随手 DDL。
	// 一次性清理：is_fulfiller 废弃列（履约人员已改为员工绑定会员，见 admin_users.user_id）；
	// HasColumn 守卫使清理逻辑跑完一次后即为空操作，仍满足「第二次启动无 DDL」的幂等验收。
	// 移除条件：各环境（测试服与生产）确认 information_schema 里该列已不存在后删除本段
	if db.GetDb().Migrator().HasColumn(&model.UserCommunity{}, "is_fulfiller") {
		if err := db.GetDb().Migrator().DropColumn(&model.UserCommunity{}, "is_fulfiller"); err != nil {
			log.Printf("清理 user_communities.is_fulfiller 废弃列失败（不阻塞启动）: %v", err)
		}
	}
	// 受控例外（同上台账）：一次性迁移——版权文字与 ICP 备案号两列合并为页脚富文本 footer_html
	// （列改名 + 数据搬迁同样超出 AutoMigrate 能力，HasColumn 守卫跑完一次后即为空操作）；
	// 迁移成功才清理旧列，失败时旧列保留、下次启动重试。
	// 移除条件：各环境确认两列均已不存在、且后台页脚按富文本口径回显无误后删除本段
	if db.GetDb().Migrator().HasColumn(&model.SiteConfig{}, "copyright_text") ||
		db.GetDb().Migrator().HasColumn(&model.SiteConfig{}, "icp_number") {
		if err := service.MigrateFooterHTML(db); err != nil {
			log.Printf("页脚富文本存量迁移失败（不阻塞启动，旧列保留待重试）: %v", err)
		} else {
			for _, col := range []string{"copyright_text", "icp_number"} {
				if db.GetDb().Migrator().HasColumn(&model.SiteConfig{}, col) {
					if err := db.GetDb().Migrator().DropColumn(&model.SiteConfig{}, col); err != nil {
						log.Printf("清理 site_configs.%s 废弃列失败（不阻塞启动）: %v", col, err)
					}
				}
			}
		}
	}
	// 存量会员-社区关联幂等回填（users.community_id > 0 且无关联记录的会员）
	if err := service.NewUserService(db).BackfillUserCommunities(stdctx.Background()); err != nil {
		log.Printf("会员-社区关联回填失败（不阻塞启动）: %v", err)
	}
	// 回收上次进程遗留的「进行中」区划任务：任务的 active_flag 占位只在终态释放，
	// 不回收会让后续每次同步都幂等返回同一条死任务（区划同步整体不可用）
	if n, err := service.NewRegionService(db).ReleaseStaleRegionTasks(stdctx.Background()); err != nil {
		log.Printf("区划更新任务占位回收失败（不阻塞启动）: %v", err)
	} else if n > 0 {
		log.Printf("已回收 %d 条中断的区划更新任务占位", n)
	}
	// 内置客户端渠道幂等种子（小程序/公众号/App 待配置，h5 默认启用）
	if err := service.NewChannelService(db).EnsureDefaultClientChannels(); err != nil {
		log.Printf("客户端渠道种子初始化失败（不阻塞启动）: %v", err)
	}
	// Redis：权限变更事件发布用（WS 提醒在线管理员刷新权限快照），失败不阻塞启动
	rds := bootstrap.InitRedis(cfg)

	srv := grpc.NewServer()
	user.RegisterUserServiceServer(srv, newServer(db, rds.GetClient()))
	bootstrap.RegisterHealth(srv)

	if err := bootstrap.RunGRPCServer(srv, cfg.UserService.GrpcPort); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

// autoMigrate 自动迁移用户服务相关数据表
// 表清单来自 model.Models()（与 market-admin init 的等待面同源，新增模型只改那一处）
// 参数：
//   - db: MySQL 实例
//
// 返回：
//   - error: 迁移错误信息
func autoMigrate(db *mysql.Instance) error {
	return db.GetDb().AutoMigrate(model.Models()...)
}
