package middleware

// KnownPermissionCodes 后台权限点目录（权威清单）
// 与 src/admin/src/constants/permissions.ts 的 permissionGroups 保持一一对应：
// 前端菜单/路由守卫与权限分配页（角色管理、社区权限）均以该清单渲染，
// 后端 RequirePermission/HasPermission 只允许使用目录内的权限码。
//
// 同步约定：新增权限点时必须同时更新本清单与前端 constants/permissions.ts，
// permission_catalog_test.go 会在 go test 时扫描路由注册与 handler 内的实际使用
// 进行双向比对，漏更新会在 CI 直接失败
var KnownPermissionCodes = []string{
	"dashboard:view",
	"product:view", "product:create", "product:update", "product:delete",
	"inventory:view", "inventory:create", "inventory:update",
	"order:view", "order:update", "order:create",
	"refund:view", "refund:update",
	// 支付报文原文含顾客标识与签名信息，系统级权限点，不进社区权限上限
	"payment:raw:view",
	"user:view", "user:create", "user:update",
	"marketing:view", "marketing:create", "marketing:update", "marketing:delete",
	"statistics:view",
	// 门店（社区级）
	"community:config",
	// 社区（系统级）
	"community:view", "community:create", "community:update", "community:delete",
	// 行政区划（系统级）
	"region:view", "region:update",
	// 系统设置（系统级）
	"settings:view", "settings:update",
	"admin:view", "admin:create", "admin:update", "admin:delete",
	"role:view", "role:create", "role:update", "role:delete",
}
