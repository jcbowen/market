// market-admin 超级管理员账号管理工具
//
// 两种使用方式：
//  1. `init` 子命令（all-in-one 镜像 entrypoint 自动调用）：
//     等待 admin_users 表就绪（user-service AutoMigrate 完成）后，若不存在
//     超管则创建初始超管——用户名取 MARKET_ADMIN_USER（默认 admin）、密码取
//     MARKET_ADMIN_PASSWORD（未配置时随机生成）；凭据打印到 stdout（进入
//     docker logs）并写入凭证文件（随宿主机 runtime 目录持久化）。幂等：
//     已有超管时跳过，绝不重置既有密码。
//     随后导入演示种子（非 production 且 communities 为空时）：数据来自 product
//     服务内嵌演示数据包（full-demo.json），经 SeedDemoBootstrap 直连库导入，
//     替代原 deploy/init.dev.sql 的 SQL 导入链路（详见 seed.go）。
//     等待各服务自动迁移的过程会每 10 秒打印进度与缺失表名；超时或种子残缺时
//     以退出码 1 结束（不再静默成功），可用 `init --rerun` 逐条幂等补导。
//     再做初始社区兜底：库中不存在任何社区时创建一个默认社区（名称取
//     MARKET_DEFAULT_COMMUNITY，默认「默认社区」），并初始化内置社区管理员
//     角色（与后台创建社区同链路），避免全新部署时超管被卡在「社区选择」页
//     死循环（无社区 → 强制选择页 → 选择页为空且无创建入口）。
//  2. 无参数交互式菜单（docker exec -it <容器名> ./market-admin）：
//     查看超管列表 / 查看初始或最近一次设置的密码 / 重置指定超管密码
//     （随机生成或手动输入）。
//
// 凭证文件：默认与配置文件同目录（容器内 /app/runtime/admin_credentials.json，
// 经 bind mount 落宿主机），权限 0600，仅记录由本工具设置/重置的明文密码；
// 之后在管理后台改过的密码不反映在该文件中。
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"github.com/jcbowen/market/src/backend/common/pwdpolicy"
	"github.com/jcbowen/market/src/backend/internal/bootstrap"
	"github.com/jcbowen/market/src/backend/services/user/model"
	"github.com/jcbowen/market/src/backend/services/user/service"
)

const (
	// defaultAdminUsername 初始超管默认用户名（可用 MARKET_ADMIN_USER 覆盖）
	defaultAdminUsername = "admin"
	// defaultCommunityName 默认社区名称（可用 MARKET_DEFAULT_COMMUNITY 覆盖）
	defaultCommunityName = "默认社区"
	// minPasswordLength 密码最小长度（手动输入与 MARKET_ADMIN_PASSWORD 同口径，
	// 与后台账号口令策略一致，避免初始账号比 API 建号更宽松）
	minPasswordLength = pwdpolicy.MinLength
	// passwordAlphabet 随机密码字符集：剔除 i/l/I/0/O/1 等易混淆字符
	passwordAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	// shutdownMarker 容器优雅停止标记：entrypoint 广播 TERM 前创建，
	// init 检测到时立即让路，避免阻塞容器退出
	shutdownMarker = "/tmp/.market-shutdown"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "init" {
		timeout := 300
		rerun := false
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--rerun":
				rerun = true
			case "--timeout":
				if i+1 < len(args) {
					if v, err := strconv.Atoi(args[i+1]); err == nil && v > 0 {
						timeout = v
					}
				}
			}
		}
		runInit(timeout, rerun)
		return
	}
	if len(args) > 0 {
		fmt.Println("用法:")
		fmt.Println("  market-admin           交互式管理菜单（查看列表/查看密码/重置密码）")
		fmt.Println("  market-admin init [--timeout N]        初始化超管与默认社区（entrypoint 自动调用）")
		fmt.Println("  market-admin init --rerun [--timeout N] 幂等复跑演示种子各段（补上次残缺，不受空库短路限制）")
		os.Exit(2)
	}
	runInteractive()
}

// openDB 加载配置并连接 MySQL（复用各微服务的 bootstrap 加载逻辑，
// 配置路径经 CONFIG_FILE 环境变量解析，敏感项经 MYSQL_PASSWORD 覆盖）
func openDB() (*mysql.Instance, error) {
	cfg := bootstrap.LoadConfig()
	return bootstrap.InitMySQL(cfg)
}

// ============================================================
// init 子命令
// ============================================================

// runInit 初始化超管账号与默认社区（幂等）
// 参数：
//   - timeoutSec: 等待各服务自动迁移的上限（秒）
//   - rerun: true 时演示种子走幂等复跑（不受「communities 非空即跳过」限制），
//     用于补齐上次导入的残缺条目
//
// 返回：
//   - 无返回值，异常经进程退出码表达：0 成功或明确让路退出；
//     1 失败（连库失败 / 等待迁移超时 / 超管创建失败 / 演示种子未完整落地）
//
// 使用示例：
//
//	docker exec market-gateway ./market-admin init --timeout 300
//	docker exec market-gateway ./market-admin init --rerun
func runInit(timeoutSec int, rerun bool) {
	fmt.Println("==> [market-admin] 初始化开始")
	inst, err := openDB()
	if err != nil {
		fmt.Printf("==> [market-admin] 初始化失败: %v\n", err)
		fmt.Println("    多为 MySQL 不可达或 market 库不存在（DROP DATABASE 后 mysql 镜像不会自动重建库，")
		fmt.Println("    见 docs/需求文档/10-部署方案/02-本地启动与测试.md「整库重置」）")
		os.Exit(1)
	}
	db := inst.GetDb()

	// 等待用户服务表迁移完成（admin_users 等）：user-service 启动时 AutoMigrate 建表，
	// 此前表不存在属正常现象；进度与缺失表名由 waitForTables 打印，超时按失败退出
	if err := waitForTables(db, userModels(), timeoutSec, "user-service 自动迁移"); err != nil {
		fmt.Printf("==> [market-admin] 初始化中止: %v\n", err)
		fmt.Println("    稍后可执行: docker exec <容器名> ./market-admin init  重试")
		os.Exit(1)
	}

	// 演示种子导入（原 init.dev.sql 的替代链路，非 production 且空库时执行）：
	// 必须先于 ensureSuperAdmin/ensureDefaultCommunity——
	//   - 演示超管（admin/admin123）优先生效，ensureSuperAdmin 随后跳过；
	//     已配置 MARKET_ADMIN_PASSWORD 时演示段让位（种子内部判断）；
	//   - 演示社区先建，ensureDefaultCommunity 检测到社区已存在即不再创建「默认社区」，
	//     仅补建缺内置角色的社区（覆盖两个演示社区）
	// 失败只记录不中断：超管/默认社区兜底仍须执行，最后统一以退出码反馈
	var seedErr error
	if rerun {
		seedErr = rerunDemoSeed(inst, timeoutSec)
	} else {
		seedErr = seedDemoPackage(inst, timeoutSec)
	}

	if err := ensureSuperAdmin(db); err != nil {
		fmt.Printf("==> [market-admin] 超管初始化失败: %v\n", err)
		os.Exit(1)
	}

	ensureDefaultCommunity(inst)

	if seedErr != nil {
		fmt.Printf("==> [market-admin] 初始化未完全成功（演示种子）: %v\n", seedErr)
		fmt.Println("    补齐方式: docker exec <容器名> ./market-admin init --rerun（逐条幂等，已有数据不受影响）")
		os.Exit(1)
	}
	fmt.Println("==> [market-admin] 初始化完成")
}

// ensureSuperAdmin 确保存在超管账号（幂等：已有超管则跳过，绝不重置既有密码）
func ensureSuperAdmin(db *gorm.DB) error {
	var count int64
	if err := db.Model(&model.Admin{}).Where("is_super = ?", true).Count(&count).Error; err != nil {
		return fmt.Errorf("查询超管失败: %w", err)
	}
	if count > 0 {
		fmt.Printf("==> [market-admin] 已存在 %d 个超管账号，跳过初始化（不会重置既有密码）\n", count)
		return nil
	}

	username := strings.TrimSpace(os.Getenv("MARKET_ADMIN_USER"))
	if username == "" {
		username = defaultAdminUsername
	}

	// 用户名冲突保护：同名账号已存在时不覆盖（超管视为已初始化，非超管则报错换名）
	var exist model.Admin
	if err := db.Where("username = ?", username).First(&exist).Error; err == nil {
		if exist.IsSuper {
			fmt.Printf("==> [market-admin] 用户名 %q 已是超管，视为初始化完成\n", username)
			return nil
		}
		return fmt.Errorf("用户名 %q 已存在且不是超管，请通过 MARKET_ADMIN_USER 指定其他用户名", username)
	}

	envPwd := os.Getenv("MARKET_ADMIN_PASSWORD")
	var password string
	generated := false
	if envPwd != "" {
		if len(envPwd) < minPasswordLength {
			return fmt.Errorf("MARKET_ADMIN_PASSWORD 长度不足（至少 %d 位）", minPasswordLength)
		}
		password = envPwd
	} else {
		p, err := generatePassword(16)
		if err != nil {
			return fmt.Errorf("生成随机密码失败: %w", err)
		}
		password = p
		generated = true
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("密码哈希失败: %w", err)
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	admin := &model.Admin{
		Username:  username,
		Password:  string(hashed),
		RealName:  "超级管理员",
		IsSuper:   true,
		Status:    1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.Create(admin).Error; err != nil {
		return fmt.Errorf("创建超管失败: %w", err)
	}

	_ = saveCredentials(adminCredentials{
		Username:    username,
		Password:    password,
		IsGenerated: generated,
		UpdatedAt:   now,
		Note:        "由 market-admin init 创建",
	})

	fmt.Println("==================================================")
	fmt.Println("==> 初始超级管理员账号已创建")
	fmt.Printf("    用户名: %s\n", username)
	fmt.Printf("    密码:   %s\n", password)
	if generated {
		fmt.Println("    （随机生成密码，请尽快登录后台修改；本条日志随 docker logs 可查）")
	}
	fmt.Printf("    凭证文件: %s\n", credentialsPath())
	fmt.Println("==================================================")
	return nil
}

// ensureDefaultCommunity 初始社区兜底（幂等）：库中不存在任何社区时创建一个默认社区，
// 与后台「创建社区」同链路；避免全新部署超管被「社区选择」页死循环卡住。
// 无论是否新建，都再为缺内置角色的存量社区补建社区管理员角色（见 backfillCommunityAdminRoles）。
// 任何失败仅打印日志不中断（可在后台手动创建）。
func ensureDefaultCommunity(inst *mysql.Instance) {
	db := inst.GetDb()

	var count int64
	if err := db.Model(&model.Community{}).Count(&count).Error; err != nil {
		fmt.Printf("==> [market-admin] 查询社区数失败，跳过默认社区初始化: %v\n", err)
		return
	}
	if count == 0 {
		name := strings.TrimSpace(os.Getenv("MARKET_DEFAULT_COMMUNITY"))
		if name == "" {
			name = defaultCommunityName
		}
		now := time.Now().Format("2006-01-02 15:04:05")
		c := &model.Community{Name: name, Status: 1, CreatedAt: now, UpdatedAt: now}
		if err := db.Create(c).Error; err != nil {
			fmt.Printf("==> [market-admin] 创建默认社区失败: %v\n", err)
			return
		}

		fmt.Println("==================================================")
		fmt.Printf("==> 已创建默认社区 %q（ID: %d）\n", name, c.ID)
		fmt.Println("    超管登录后台选择该社区进入后，可在「社区管理」完善资料")
		fmt.Println("==================================================")
	}

	backfillCommunityAdminRoles(inst)
}

// backfillCommunityAdminRoles 为缺内置社区管理员角色的社区幂等补建该角色。
// 该角色是「社区权限上限」的载体：预置数据（演示种子包）里已有社区但没有角色时，
// 分配社区管理员、按内置角色造员工都会失败，故每次初始化都补齐一次。
func backfillCommunityAdminRoles(inst *mysql.Instance) {
	db := inst.GetDb()
	var ids []int64
	if err := db.Model(&model.Community{}).
		Where("id NOT IN (?)", db.Model(&model.Role{}).
			Select("community_id").Where("code = ?", model.RoleCodeCommunityAdmin)).
		Pluck("id", &ids).Error; err != nil {
		fmt.Printf("==> [market-admin] 查询待补建角色的社区失败: %v\n", err)
		return
	}
	if len(ids) == 0 {
		return
	}

	roleService := service.NewRoleService(inst)
	for _, id := range ids {
		if _, err := roleService.EnsureCommunityAdminRole(context.Background(), id); err != nil {
			fmt.Printf("==> [market-admin] 社区 %d 补建内置社区管理员角色失败: %v\n", id, err)
			continue
		}
		fmt.Printf("==> [market-admin] 社区 %d 已补建内置社区管理员角色\n", id)
	}
}

// ============================================================
// 交互式菜单
// ============================================================

// runInteractive 无参数交互式菜单（docker exec -it <容器> ./market-admin）
func runInteractive() {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("====================================")
	fmt.Println("  market 超级管理员管理工具")
	fmt.Println("====================================")

	db, err := openDB()
	if err != nil {
		fmt.Printf("错误: %v\n", err)
		fmt.Println("提示: 若容器刚启动，MySQL 可能尚未就绪，请稍后重试")
		os.Exit(1)
	}
	gdb := db.GetDb()

	for {
		fmt.Println()
		fmt.Println("请选择操作：")
		fmt.Println("  1. 查看超管账号列表")
		fmt.Println("  2. 查看初始/最近一次设置的账号密码")
		fmt.Println("  3. 重置超管账号密码")
		fmt.Println("  0. 退出")
		fmt.Print("请输入编号: ")
		line, _ := reader.ReadString('\n')
		switch strings.TrimSpace(line) {
		case "1":
			_ = listSuperAdmins(gdb)
		case "2":
			showCredentials()
		case "3":
			resetPasswordFlow(gdb, reader)
		case "0", "q", "Q", "exit", "quit":
			fmt.Println("再见")
			return
		default:
			fmt.Println("无效选择，请输入 0-3")
		}
	}
}

// listSuperAdmins 列出全部超管账号，返回列表（供重置流程复用）
func listSuperAdmins(db *gorm.DB) []*model.Admin {
	var admins []*model.Admin
	if err := db.Where("is_super = ?", true).Order("id asc").Find(&admins).Error; err != nil {
		fmt.Printf("查询失败: %v\n", err)
		return nil
	}
	if len(admins) == 0 {
		fmt.Println("当前没有超管账号（可执行 ./market-admin init 初始化）")
		return nil
	}
	fmt.Printf("%-6s %-20s %-14s %-6s %-20s\n", "ID", "用户名", "姓名", "状态", "最后登录")
	for _, a := range admins {
		status := "启用"
		if a.Status != 1 {
			status = "禁用"
		}
		last := "-"
		if a.LastLoginAt != nil && *a.LastLoginAt != "" {
			last = *a.LastLoginAt
		}
		fmt.Printf("%-6d %-20s %-14s %-6s %-20s\n", a.ID, a.Username, a.RealName, status, last)
	}
	return admins
}

// showCredentials 查看凭证文件（初始或最近一次由本工具设置的密码）
func showCredentials() {
	p := credentialsPath()
	data, err := os.ReadFile(p)
	if err != nil {
		fmt.Printf("未找到凭证文件（%s）\n", p)
		fmt.Println("说明: 仅当超管由 market-admin init 创建、或经本工具重置过密码时才存在记录；")
		fmt.Println("      之后在管理后台修改过的密码不会反映在该文件中（可改用「重置密码」）。")
		return
	}
	var c adminCredentials
	if err := json.Unmarshal(data, &c); err != nil {
		fmt.Printf("凭证文件解析失败（%s）: %v\n", p, err)
		return
	}
	fmt.Println("---------- 账号信息 ----------")
	fmt.Printf("用户名: %s\n", c.Username)
	fmt.Printf("密码:   %s\n", c.Password)
	fmt.Printf("更新时间: %s\n", c.UpdatedAt)
	fmt.Printf("来源: %s\n", c.Note)
	fmt.Println("------------------------------")
}

// resetPasswordFlow 交互式重置指定超管密码（随机生成或手动输入），并同步凭证文件
func resetPasswordFlow(db *gorm.DB, reader *bufio.Reader) {
	admins := listSuperAdmins(db)
	if len(admins) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("请选择要重置密码的超管账号：")
	for i, a := range admins {
		fmt.Printf("  %d. %s（ID: %d）\n", i+1, a.Username, a.ID)
	}
	fmt.Println("  0. 取消")
	fmt.Print("请输入编号: ")
	line, _ := reader.ReadString('\n')
	idx, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || idx < 0 || idx > len(admins) {
		fmt.Println("无效选择，已取消")
		return
	}
	if idx == 0 {
		fmt.Println("已取消")
		return
	}
	target := admins[idx-1]

	fmt.Println()
	fmt.Println("请选择新密码生成方式：")
	fmt.Println("  1. 随机生成（16 位，无易混淆字符）")
	fmt.Println("  2. 手动输入")
	fmt.Print("请输入编号: ")
	choice, _ := reader.ReadString('\n')
	var password string
	generated := false
	switch strings.TrimSpace(choice) {
	case "1":
		p, err := generatePassword(16)
		if err != nil {
			fmt.Printf("生成密码失败: %v\n", err)
			return
		}
		password = p
		generated = true
	case "2":
		fmt.Print("请输入新密码（至少 8 位，输入过程明文可见）: ")
		pw, _ := reader.ReadString('\n')
		password = strings.TrimSpace(pw)
		if len(password) < minPasswordLength {
			fmt.Printf("密码长度不足（至少 %d 位），已取消\n", minPasswordLength)
			return
		}
	default:
		fmt.Println("无效选择，已取消")
		return
	}

	fmt.Println()
	fmt.Printf("即将重置超管 %q（ID: %d）的密码为: %s\n", target.Username, target.ID, password)
	fmt.Print("确认操作？(y/N): ")
	confirm, _ := reader.ReadString('\n')
	confirm = strings.ToLower(strings.TrimSpace(confirm))
	if confirm != "y" && confirm != "yes" {
		fmt.Println("已取消")
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Printf("密码哈希失败: %v\n", err)
		return
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	res := db.Model(&model.Admin{}).Where("id = ?", target.ID).Updates(map[string]any{
		"password":   string(hashed),
		"updated_at": now,
	})
	if res.Error != nil {
		fmt.Printf("重置失败: %v\n", res.Error)
		return
	}

	_ = saveCredentials(adminCredentials{
		Username:    target.Username,
		Password:    password,
		IsGenerated: generated,
		UpdatedAt:   now,
		Note:        fmt.Sprintf("由 market-admin 重置（账号 ID %d）", target.ID),
	})
	fmt.Println("重置成功，请使用新密码登录；新密码已同步写入凭证文件。")
}

// ============================================================
// 凭证文件与随机密码
// ============================================================

// adminCredentials 凭证文件结构（明文保存最近一次由本工具设置的密码）
type adminCredentials struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	IsGenerated bool   `json:"is_generated"`
	UpdatedAt   string `json:"updated_at"`
	Note        string `json:"note"`
}

// credentialsPath 凭证文件路径：MARKET_ADMIN_CREDENTIALS_FILE >
// CONFIG_FILE 同目录（容器内 /app/runtime/）> 当前目录
func credentialsPath() string {
	if v := os.Getenv("MARKET_ADMIN_CREDENTIALS_FILE"); v != "" {
		return v
	}
	if cfgPath := os.Getenv("CONFIG_FILE"); cfgPath != "" {
		return filepath.Join(filepath.Dir(cfgPath), "admin_credentials.json")
	}
	return "admin_credentials.json"
}

// saveCredentials 写入凭证文件（0600 权限，仅当前用户可读）
func saveCredentials(c adminCredentials) error {
	p := credentialsPath()
	if dir := filepath.Dir(p); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

// generatePassword 生成 n 位随机密码（crypto/rand，无易混淆字符）
func generatePassword(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = passwordAlphabet[idx.Int64()]
	}
	return string(b), nil
}
