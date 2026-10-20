package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jcbowen/jcbaseGo"
	jcMiddleware "github.com/jcbowen/jcbaseGo/middleware"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
	"github.com/jcbowen/market/src/backend/common/response"
	"github.com/jcbowen/market/src/backend/gateway/brand"
	"github.com/jcbowen/market/src/backend/gateway/client"
	"github.com/jcbowen/market/src/backend/gateway/handler"
	gwMiddleware "github.com/jcbowen/market/src/backend/gateway/middleware"
)

/**
 * main Gateway 主入口
 * 统一入口：JWT 鉴权、路由分组、静态资源、gRPC 转发
 */
func main() {
	cfg := loadConfig()

	jwtManager := jwt.NewManager(jwt.Config{
		Secret:          cfg.JWT.Secret,
		AccessTokenTTL:  time.Duration(cfg.JWT.AccessTokenTTL) * time.Second,
		RefreshTokenTTL: time.Duration(cfg.JWT.RefreshTokenTTL) * time.Second,
	})

	clients, err := client.NewServiceClients(cfg)
	if err != nil {
		log.Fatalf("初始化 gRPC 客户端失败: %v", err)
	}
	defer func() { _ = clients.Close() }()

	// 站点设置（部署域名与品牌）内存缓存：支付回调派生、附件外链基址、HTML 品牌注入共用
	brand.SetDefault(brand.New(clients.UserClient, os.Getenv("SITE_BASE_URL")))

	r := gin.New()
	applyTrustedProxies(r, cfg)
	r.Use(gin.Recovery())
	r.Use(gwMiddleware.RequestLoggerMiddleware())
	r.Use(gwMiddleware.SecurityHeadersMiddleware())
	r.Use(gwMiddleware.BlockTraceMiddleware())
	r.Use(gwMiddleware.RealIPMiddleware())
	r.Use(gwMiddleware.RateLimitMiddleware(100, 150))

	baseMw := jcMiddleware.Base{}
	r.Use(baseMw.Cors())
	r.Use(baseMw.SetGPC(r))
	r.Use(staticCacheMiddleware())

	registerRoutes(r, jwtManager, clients, cfg)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.WebServer.Port),
		Handler: r,
	}

	go func() {
		log.Printf("Gateway 启动于 :%d", cfg.WebServer.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Gateway 启动失败: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shutdown(srv)
}

// applyTrustedProxies 配置可信反向代理网段，决定 ClientIP 是否采信代理头
// gin 缺省信任所有代理，客户端自填 X-Forwarded-For 就能任意伪造来源 IP——限流按 IP 分桶会被直接绕过，
// 访问日志与 IP 定位也会记下假 IP。故这里显式落配置：未配置网段即不信任任何代理（只认直连地址）
// 参数：
//   - r: gin 引擎
//   - cfg: 全局配置
//
// 异常：
//   - 网段格式非法（既非 CIDR 也非 IP）时拒绝启动，避免带着「以为已放行反代、实际全不采信」的误配上线
func applyTrustedProxies(r *gin.Engine, cfg *config.Config) {
	proxies := cfg.Security.TrustedProxies
	if err := r.SetTrustedProxies(proxies); err != nil {
		log.Fatalf("可信代理网段配置非法: %v（security.trusted_proxies 仅接受 CIDR 或单个 IP）", err)
	}
	if len(proxies) == 0 {
		log.Printf("未配置可信代理（security.trusted_proxies / TRUSTED_PROXIES）：一律按直连 IP 处理；" +
			"若前置 nginx 等反向代理，必须放行其网段，否则限流与日志会把所有用户合并成代理 IP")
		return
	}
	log.Printf("已信任反向代理网段: %v", proxies)
}

// loadConfig 加载全局配置
// 优先读取 CONFIG_FILE 环境变量指定的配置文件，未设置时使用可执行文件同级目录下的 config.yaml；
// 加载后应用环境变量覆盖（MYSQL_PASSWORD/REDIS_PASSWORD/JWT_SECRET），
// 并在生产环境对默认 JWT 密钥 fail-fast
// 返回：
//   - *config.Config: 全局配置指针
func loadConfig() *config.Config {
	path := getConfigPath()
	config.SetConfigPath(path)
	opt := jcbaseGo.New(jcbaseGo.Option{
		ConfigType:   jcbaseGo.ConfigTypeYAML,
		ConfigSource: path,
		ConfigData:   &config.Config{},
	})
	cfg := (*opt.GetConfig()).(*config.Config)
	config.ApplyEnvOverrides(cfg)
	if err := config.EnsureSecureJWT(cfg); err != nil {
		log.Fatalf("配置安全校验失败: %v", err)
	}
	return cfg
}

// getConfigPath 获取配置文件路径
// 优先读取 CONFIG_FILE 环境变量，未设置时使用可执行文件同级目录下的 config.yaml
// 返回：
//   - string: 配置文件路径
func getConfigPath() string {
	if envPath := os.Getenv("CONFIG_FILE"); envPath != "" {
		return envPath
	}
	return filepath.Join(getBinDir(), "config.yaml")
}

// registerRoutes 注册 API 路由分组
// 参数：
//   - r: gin 引擎
//   - jwtManager: JWT 管理器
//   - clients: gRPC 服务客户端集合
//   - cfg: 全局配置
func registerRoutes(r *gin.Engine, jwtManager *jwt.Manager, clients *client.ServiceClients, cfg *config.Config) {
	// HTML 品牌注入须先于路由注册：中间件在路由前命中前端入口页，
	// 既避免与 r.Static 的 /b/*filepath 通配路由冲突，也让 SPA 深链兜底同样带上品牌
	h5Path := filepath.Join(getBinDir(), "static", "h5")
	adminPath := filepath.Join(getBinDir(), "static", "admin")
	registerHTMLInjection(r, adminPath, h5Path)

	r.GET("/health", func(c *gin.Context) {
		response.Success(c, gin.H{
			"service": "gateway",
			"time":    time.Now().Unix(),
		})
	})

	userHandler := handler.NewUserHandler(clients.UserClient)
	productHandler := handler.NewProductHandler(clients.ProductClient)
	cartHandler := handler.NewCartHandler(clients.CartClient)
	orderHandler := handler.NewOrderHandler(clients.OrderClient, clients.UserClient, clients.PaymentClient, clients.CartClient, cfg.Business.OrderTimeout)
	// C 端上传（履约签收照片等）：独立 handler 持有 product 客户端，IsDisplay=false
	fulfillmentUploadHandler := handler.NewFulfillmentUploadHandler(clients.ProductClient)
	paymentHandler := handler.NewPaymentHandler(clients.PaymentClient, clients.OrderClient, clients.UserClient)
	authHandler := handler.NewAuthHandler(jwtManager, clients.UserClient)
	// IP 定位：本地 GeoLite2-City 数据库，相对路径以可执行文件所在目录为基准
	geoipHandler := handler.NewGeoIPHandler(resolveBinPath(cfg.GeoIP.DBPath))
	// 行政区划：级联选择器懒加载、IP 定位反查区划、后台管理（列表/检查更新/差异覆盖）
	regionHandler := handler.NewRegionHandler(clients.UserClient, resolveBinPath(cfg.GeoIP.DBPath))

	apiV1 := r.Group("/api/v1")
	{
		// 公开接口
		public := apiV1.Group("")
		{
			public.GET("/communities", userHandler.ListCommunities)
			public.GET("/communities/:id", userHandler.GetCommunity)
			public.GET("/products", productHandler.ListProducts)
			// 条形码精确查询：静态段路由需注册在 /products/:id 之前（gin 静态优先，防吞参数）
			public.GET("/products/barcode/:code", productHandler.GetProductByBarcode)
			public.GET("/products/:id", productHandler.GetProduct)
			public.GET("/skus", productHandler.ListSkus)
			public.GET("/skus/:id", productHandler.GetSkuById)
			public.GET("/categories", productHandler.ListCategories)
			public.GET("/coupons", productHandler.ListCoupons)
			public.GET("/flash-sales", productHandler.ListFlashSales)
			public.GET("/full-reductions", productHandler.ListFullReductions)
			public.GET("/free-shippings/active", productHandler.GetActiveFreeShipping)
			public.GET("/banners", productHandler.ListBanners)

			public.GET("/geoip/locate", geoipHandler.Locate)
			public.GET("/regions/children", regionHandler.ListChildren)
			public.GET("/regions/locate", regionHandler.Locate)

			// 认证入口独立收紧限流（D1）：全局 IP 桶（100 r/s）对「批量试探登录」形同不设防。
			// 缺口的真实防线在按账号的失败锁定（后台登录）与按 IP 的独立桶（C 端登录）：
			// C 端小区/NAT 出口共用 IP，额度给到能容纳真实并发登录的量级，仍远低于全局限流
			cAuthLimiter := gwMiddleware.RateLimitMiddleware(10, 30)
			public.POST("/auth/phone-code", cAuthLimiter, authHandler.SendPhoneCode)
			public.POST("/auth/login/phone", cAuthLimiter, authHandler.PhoneLogin)
			public.POST("/auth/weixin-login", cAuthLimiter, authHandler.WechatLogin)
			public.POST("/auth/refresh", cAuthLimiter, authHandler.RefreshToken)
			// 登出仅需清除 httpOnly refresh cookie，无需登录态（access 可能已过期仍要能登出）
			public.POST("/auth/logout", cAuthLimiter, authHandler.Logout)

			public.POST("/payments/callback/wechat", paymentHandler.WechatCallback)
			// 支付宝回调：payment 服务内先做 RSA2 验签（支付宝公钥），验签失败拒绝处理
			public.POST("/payments/callback/alipay", paymentHandler.AlipayCallback)
			public.GET("/payments/channels", paymentHandler.PaymentChannels)
		}

		// 用户端需登录
		authUser := apiV1.Group("")
		authUser.Use(gwMiddleware.AuthMiddleware(jwtManager, jwt.TokenTypeUser))
		// 拦截被拉黑会员：登录后的所有请求统一返回「系统错误，请联系工作人员」
		authUser.Use(gwMiddleware.UserStatusMiddleware(clients.UserClient))
		{
			authUser.GET("/user/profile", userHandler.Profile)
			authUser.PUT("/user/profile", userHandler.UpdateUserProfile)
			authUser.PUT("/user/community", userHandler.UpdateCommunity)
			authUser.GET("/user/addresses", userHandler.ListAddresses)
			authUser.POST("/user/addresses", userHandler.CreateAddress)
			authUser.PUT("/user/addresses/:id", userHandler.UpdateAddress)
			authUser.PUT("/user/addresses/:id/default", userHandler.SetDefaultAddress)
			authUser.DELETE("/user/addresses/:id", userHandler.DeleteAddress)

			authUser.GET("/cart", cartHandler.GetCart)
			authUser.POST("/cart/items", cartHandler.AddItem)
			authUser.PUT("/cart/items/:id", cartHandler.UpdateItem)
			authUser.DELETE("/cart/items/:id", cartHandler.RemoveItem)
			authUser.POST("/cart/select", cartHandler.SelectAll)
			authUser.DELETE("/cart/invalid", cartHandler.ClearInvalidItems)

			authUser.POST("/orders", gwMiddleware.UserRateLimitMiddleware(0.08, 5), orderHandler.CreateOrder)
			authUser.GET("/orders", orderHandler.ListOrders)
			authUser.GET("/orders/status-counts", orderHandler.GetUserOrderStatusCounts)
			authUser.GET("/orders/frequent-products", orderHandler.ListFrequentProducts)
			authUser.POST("/orders/free-shipping/preview", orderHandler.PreviewFreeShipping)
			authUser.GET("/orders/:id", orderHandler.GetOrder)
			authUser.PUT("/orders/:id/cancel", orderHandler.CancelOrder)
			authUser.POST("/orders/:id/receive", orderHandler.ConfirmReceive)
			authUser.POST("/orders/:id/refund", orderHandler.ApplyRefund)
			authUser.GET("/refund/config", orderHandler.GetRefundConfig)

			// 履约工作台（B）：handler 内统一校验履约人员身份
			authUser.GET("/user/fulfillment-status", orderHandler.GetFulfillmentStatus)
			authUser.GET("/fulfillment/orders", orderHandler.ListFulfillmentOrders)
			authUser.POST("/fulfillment/orders/:id/start-delivery", orderHandler.StartDelivery)
			authUser.POST("/fulfillment/orders/:id/complete-delivery", orderHandler.CompleteDelivery)
			authUser.POST("/fulfillment/orders/:id/verify-pickup", orderHandler.VerifyPickupByStaff)

			// C 端图片上传（履约签收照片等凭证用途，IsDisplay=false 不进附件选择器）
			authUser.POST("/upload/image", fulfillmentUploadHandler.UploadImage)

			authUser.POST("/payments", paymentHandler.CreatePayment)
			authUser.GET("/payments/:id", paymentHandler.GetPayment)

			authUser.POST("/coupons/receive", productHandler.ReceiveCoupon)
			authUser.GET("/user/coupons", productHandler.ListUserCoupons)
		}
	}

	adminHandler := handler.NewAdminHandler(jwtManager, clients.UserClient, clients.ProductClient, clients.OrderClient, clients.PaymentClient, cfg)
	// 站点品牌信息：登录页与 C 端首屏免登录读取，仅含非敏感字段
	apiV1.GET("/site-info", adminHandler.GetPublicSiteInfo)
	// 多社区模型：权限随当前社区变化，注入实时权限上下文解析器（user 服务）
	gwMiddleware.SetPermissionResolver(clients.UserClient)
	admin := r.Group("/api/v1/admin")
	{
		// 后台登录独立收紧（D1）：突发 50、持续 5 r/s，仅作洪水兜底；
		// 猜密码的真正防线是 login_guard 的按账号失败锁定（计数存 Redis、跨 IP 有效），
		// 本额度必须容纳 NAT 后多人同时登录与自动化全量回归（每轮数百次登录），故不按最激进口径设
		adminAuthLimiter := gwMiddleware.RateLimitMiddleware(5, 50)
		admin.POST("/auth/login", adminAuthLimiter, adminHandler.Login)
		admin.POST("/auth/refresh", adminAuthLimiter, adminHandler.RefreshToken)
		// 登出仅需清除 httpOnly refresh cookie，无需登录态（access 已过期也要能登出）
		admin.POST("/auth/logout", adminAuthLimiter, adminHandler.AdminLogout)

		authAdmin := admin.Group("")
		authAdmin.Use(gwMiddleware.AuthMiddleware(jwtManager, jwt.TokenTypeAdmin))
		// 校验请求头声明的当前社区（X-Community-Id）是否越权，越权直接 403
		authAdmin.Use(gwMiddleware.RequireCommunityAccess())
		{
			// 当前管理员信息：仅要求登录，不要求具体权限（前端刷新页面后恢复状态使用）
			authAdmin.GET("/auth/info", adminHandler.Info)
			// 当前账号可进入的社区列表（社区选择页用，仅要求登录）
			authAdmin.GET("/my/communities", adminHandler.MyCommunities)
			// 系统信息（管理后台「关于」页）：版本号 + MySQL/Redis 版本，仅要求登录
			aboutHandler := handler.NewAboutHandler(cfg)
			authAdmin.GET("/about", aboutHandler.Get)
			// 更新检测（Docker Hub 最新版本比对，需环境变量 DOCKERHUB_REPO 启用），仅要求登录
			authAdmin.GET("/about/updates", aboutHandler.LatestVersion)
			authAdmin.GET("/dashboard", gwMiddleware.RequirePermission("dashboard:view"), adminHandler.GetDashboard)
			authAdmin.GET("/roles", gwMiddleware.RequirePermission("role:view"), adminHandler.ListRoles)
			authAdmin.POST("/roles", gwMiddleware.RequirePermission("role:create"), adminHandler.CreateRole)
			authAdmin.PUT("/roles/:id", gwMiddleware.RequirePermission("role:update"), adminHandler.UpdateRole)
			authAdmin.DELETE("/roles/:id", gwMiddleware.RequirePermission("role:delete"), adminHandler.DeleteRole)

			authAdmin.GET("/admins", gwMiddleware.RequirePermission("admin:view"), adminHandler.ListAdmins)
			authAdmin.POST("/admins", gwMiddleware.RequirePermission("admin:create"), adminHandler.CreateAdmin)
			authAdmin.PUT("/admins/:id", gwMiddleware.RequirePermission("admin:update"), adminHandler.UpdateAdmin)
			authAdmin.DELETE("/admins/:id", gwMiddleware.RequirePermission("admin:delete"), adminHandler.DeleteAdmin)
			authAdmin.PUT("/admins/:id/password", gwMiddleware.RequirePermission("admin:update"), adminHandler.ChangeAdminPassword)

			authAdmin.GET("/categories", gwMiddleware.RequirePermission("product:view"), adminHandler.ListCategories)
			authAdmin.POST("/categories", gwMiddleware.RequirePermission("product:create"), adminHandler.CreateCategory)
			authAdmin.PUT("/categories/:id", gwMiddleware.RequirePermission("product:update"), adminHandler.UpdateCategory)
			authAdmin.DELETE("/categories/:id", gwMiddleware.RequirePermission("product:delete"), adminHandler.DeleteCategory)

			authAdmin.GET("/products", gwMiddleware.RequirePermission("product:view"), adminHandler.ListAdminProducts)
			// 收银台扫码直达：条形码查询随下单链路走 order:create（收银员可能无 product:view）
			authAdmin.GET("/products/barcode/:code", gwMiddleware.RequirePermission("order:create"), productHandler.GetProductByBarcode)
			authAdmin.GET("/products/:id", gwMiddleware.RequirePermission("product:view"), adminHandler.GetAdminProduct)
			authAdmin.POST("/products", gwMiddleware.RequirePermission("product:create"), adminHandler.CreateProduct)
			authAdmin.PUT("/products/:id", gwMiddleware.RequirePermission("product:update"), adminHandler.UpdateProduct)
			authAdmin.DELETE("/products/:id", gwMiddleware.RequirePermission("product:delete"), adminHandler.DeleteProduct)
			authAdmin.POST("/products/:id/status", gwMiddleware.RequirePermission("product:update"), adminHandler.UpdateProductStatus)
			authAdmin.POST("/products/batch-status", gwMiddleware.RequirePermission("product:update"), adminHandler.BatchUpdateProductStatus)
			authAdmin.GET("/products/status-stats", gwMiddleware.RequirePermission("product:view"), adminHandler.GetProductStatusStats)
			authAdmin.PUT("/products/:id/name", gwMiddleware.RequirePermission("product:update"), adminHandler.UpdateProductName)

			authAdmin.GET("/orders", gwMiddleware.RequirePermission("order:view"), adminHandler.ListAdminOrders)
			authAdmin.GET("/orders/:id", gwMiddleware.RequirePermission("order:view"), adminHandler.GetAdminOrder)
			authAdmin.PUT("/orders/:id/status", gwMiddleware.RequirePermission("order:update"), adminHandler.UpdateOrderStatus)
			authAdmin.POST("/orders/:id/cancel", gwMiddleware.RequirePermission("order:update"), adminHandler.AdminCancelOrder)
			authAdmin.POST("/orders/:id/ship", gwMiddleware.RequirePermission("order:update"), adminHandler.ShipOrder)
			authAdmin.PUT("/orders/:id/complete", gwMiddleware.RequirePermission("order:update"), adminHandler.CompleteOrder)
			authAdmin.POST("/orders/:id/verify-pickup", gwMiddleware.RequirePermission("order:update"), adminHandler.AdminVerifyPickup)
			// 履约工作台（B）：指派配送（履约人员 = 本社区任职且绑定会员的员工，配置入口在员工编辑页）
			authAdmin.PUT("/orders/:id/assign-delivery", gwMiddleware.RequirePermission("order:update"), adminHandler.AssignDelivery)
			authAdmin.GET("/fulfillers", gwMiddleware.RequirePermission("user:view"), adminHandler.ListFulfillers)
			// 会员常买商品排行（会员订单弹窗板块）：订单/会员任一查看权限即可
			authAdmin.GET("/users/:id/frequent-products", gwMiddleware.RequirePermission("order:view", "user:view"), adminHandler.ListUserFrequentProducts)
			authAdmin.POST("/orders/:id/refund", gwMiddleware.RequirePermission("refund:update"), adminHandler.AdminCreateRefund)

			// 收银台代客下单（G）：下单/挂单写操作用 order:create，挂单查看用 order:view
			authAdmin.POST("/pos/orders", gwMiddleware.RequirePermission("order:create"), adminHandler.CreatePosOrder)
			authAdmin.POST("/pos/held-orders", gwMiddleware.RequirePermission("order:create"), adminHandler.CreatePosHeldOrder)
			authAdmin.GET("/pos/held-orders", gwMiddleware.RequirePermission("order:view"), adminHandler.ListPosHeldOrders)
			authAdmin.DELETE("/pos/held-orders/:id", gwMiddleware.RequirePermission("order:create"), adminHandler.DeletePosHeldOrder)

			// 收银台交班体系：开工/当前班次/交班用 order:create（收银员即可）；
			// 记录查看分两路——我的班次用 order:create（默认仅自己），
			// 本店全部班次用 order:view（无 order:view 的收银员无法查他人班次）
			authAdmin.POST("/pos/shifts/start", gwMiddleware.RequirePermission("order:create"), adminHandler.PosStartShift)
			authAdmin.GET("/pos/shifts/current", gwMiddleware.RequirePermission("order:create"), adminHandler.PosGetCurrentShift)
			authAdmin.POST("/pos/shifts/end", gwMiddleware.RequirePermission("order:create"), adminHandler.PosEndShift)
			authAdmin.GET("/pos/shifts/mine", gwMiddleware.RequirePermission("order:create"), adminHandler.PosListShiftsMine)
			authAdmin.GET("/pos/shifts", gwMiddleware.RequirePermission("order:view"), adminHandler.PosListShifts)

			// 应用偏好（应用页「常用应用」，社区级共享）：仅要求登录，应用页对所有管理员可见
			authAdmin.GET("/app-preferences", adminHandler.GetAppPreferences)
			authAdmin.PUT("/app-preferences", adminHandler.SaveAppPreferences)

			authAdmin.GET("/statistics/orders", gwMiddleware.RequirePermission("order:view", "statistics:view"), adminHandler.GetOrderStatistics)
			authAdmin.GET("/statistics/sales", gwMiddleware.RequirePermission("order:view", "statistics:view"), adminHandler.GetSalesStatistics)
			authAdmin.GET("/statistics/rankings", gwMiddleware.RequirePermission("order:view", "statistics:view"), adminHandler.GetProductRankings)
			authAdmin.GET("/statistics/revenue", gwMiddleware.RequirePermission("order:view", "statistics:view"), adminHandler.GetRevenueReport)

			authAdmin.GET("/inventory/entries", gwMiddleware.RequirePermission("inventory:view"), adminHandler.ListStockRecords)
			authAdmin.POST("/inventory/purchase-order", gwMiddleware.RequirePermission("inventory:view"), adminHandler.GeneratePurchaseOrder)
			authAdmin.GET("/inventory/warnings", gwMiddleware.RequirePermission("inventory:view"), adminHandler.GetStockWarnings)
			authAdmin.GET("/inventory/skus", gwMiddleware.RequirePermission("inventory:view"), adminHandler.ListSkuStock)
			authAdmin.GET("/inventory/checks", gwMiddleware.RequirePermission("inventory:view"), adminHandler.ListStockChecks)
			authAdmin.GET("/inventory/checks/:id", gwMiddleware.RequirePermission("inventory:view"), adminHandler.GetStockCheckDetail)
			authAdmin.POST("/inventory/checks", gwMiddleware.RequirePermission("inventory:create"), adminHandler.CreateStockCheck)
			authAdmin.POST("/inventory/checks/:id/submit", gwMiddleware.RequirePermission("inventory:update"), adminHandler.SubmitStockCheck)

			// 进销存重构：供应商 / 采购单 / 入库单 / 出库单 / 批次 / 报表
			authAdmin.GET("/inventory/suppliers", gwMiddleware.RequirePermission("inventory:view"), adminHandler.ListSuppliers)
			authAdmin.POST("/inventory/suppliers", gwMiddleware.RequirePermission("inventory:create"), adminHandler.CreateSupplier)
			authAdmin.PUT("/inventory/suppliers/:id", gwMiddleware.RequirePermission("inventory:update"), adminHandler.UpdateSupplier)
			authAdmin.DELETE("/inventory/suppliers/:id", gwMiddleware.RequirePermission("inventory:update"), adminHandler.DeleteSupplier)

			authAdmin.GET("/inventory/purchase-orders", gwMiddleware.RequirePermission("inventory:view"), adminHandler.ListPurchaseOrders)
			authAdmin.POST("/inventory/purchase-orders", gwMiddleware.RequirePermission("inventory:create"), adminHandler.CreatePurchaseOrder)
			authAdmin.GET("/inventory/purchase-orders/:id", gwMiddleware.RequirePermission("inventory:view"), adminHandler.GetPurchaseOrder)
			authAdmin.PUT("/inventory/purchase-orders/:id", gwMiddleware.RequirePermission("inventory:update"), adminHandler.UpdatePurchaseOrder)
			authAdmin.POST("/inventory/purchase-orders/:id/submit", gwMiddleware.RequirePermission("inventory:update"), adminHandler.SubmitPurchaseOrder)
			authAdmin.POST("/inventory/purchase-orders/:id/cancel", gwMiddleware.RequirePermission("inventory:update"), adminHandler.CancelPurchaseOrder)

			authAdmin.GET("/inventory/in-orders", gwMiddleware.RequirePermission("inventory:view"), adminHandler.ListStockInOrders)
			authAdmin.POST("/inventory/in-orders", gwMiddleware.RequirePermission("inventory:create"), adminHandler.CreateStockInOrder)
			authAdmin.GET("/inventory/in-orders/:id", gwMiddleware.RequirePermission("inventory:view"), adminHandler.GetStockInOrder)

			authAdmin.GET("/inventory/out-orders", gwMiddleware.RequirePermission("inventory:view"), adminHandler.ListStockOutOrders)
			authAdmin.POST("/inventory/out-orders", gwMiddleware.RequirePermission("inventory:create"), adminHandler.CreateStockOutOrder)
			authAdmin.GET("/inventory/out-orders/:id", gwMiddleware.RequirePermission("inventory:view"), adminHandler.GetStockOutOrder)

			authAdmin.GET("/inventory/batches", gwMiddleware.RequirePermission("inventory:view"), adminHandler.ListStockBatches)

			authAdmin.GET("/inventory/reports/summary", gwMiddleware.RequirePermission("inventory:view"), adminHandler.GetInventorySummaryReport)
			authAdmin.GET("/inventory/reports/profit", gwMiddleware.RequirePermission("inventory:view"), adminHandler.GetProfitReport)

			authAdmin.GET("/coupons", gwMiddleware.RequirePermission("marketing:view"), adminHandler.ListAdminCoupons)
			authAdmin.POST("/coupons", gwMiddleware.RequirePermission("marketing:create"), adminHandler.CreateCoupon)
			authAdmin.PUT("/coupons/:id", gwMiddleware.RequirePermission("marketing:update"), adminHandler.UpdateCoupon)
			authAdmin.DELETE("/coupons/:id", gwMiddleware.RequirePermission("marketing:delete"), adminHandler.DeleteCoupon)
			// 手动发放优惠券（M②期）+ 领取/发放记录
			authAdmin.POST("/coupons/:id/grant", gwMiddleware.RequirePermission("marketing:update"), adminHandler.GrantCoupon)
			authAdmin.GET("/coupon-records", gwMiddleware.RequirePermission("marketing:view"), adminHandler.ListCouponRecords)

			authAdmin.GET("/flash-sales", gwMiddleware.RequirePermission("marketing:view"), adminHandler.ListAdminFlashSales)
			authAdmin.POST("/flash-sales", gwMiddleware.RequirePermission("marketing:create"), adminHandler.CreateFlashSale)
			authAdmin.PUT("/flash-sales/:id", gwMiddleware.RequirePermission("marketing:update"), adminHandler.UpdateFlashSale)

			authAdmin.GET("/full-reductions", gwMiddleware.RequirePermission("marketing:view"), adminHandler.ListFullReductions)
			authAdmin.POST("/full-reductions", gwMiddleware.RequirePermission("marketing:create"), adminHandler.CreateFullReduction)
			authAdmin.PUT("/full-reductions/:id", gwMiddleware.RequirePermission("marketing:update"), adminHandler.UpdateFullReduction)
			authAdmin.GET("/free-shippings", gwMiddleware.RequirePermission("marketing:view"), adminHandler.ListFreeShippings)
			authAdmin.POST("/free-shippings", gwMiddleware.RequirePermission("marketing:create"), adminHandler.CreateFreeShipping)
			authAdmin.PUT("/free-shippings/:id", gwMiddleware.RequirePermission("marketing:update"), adminHandler.UpdateFreeShipping)

			authAdmin.GET("/banners", gwMiddleware.RequirePermission("marketing:view"), adminHandler.ListAdminBanners)
			authAdmin.POST("/banners", gwMiddleware.RequirePermission("marketing:create"), adminHandler.CreateBanner)
			authAdmin.PUT("/banners/:id", gwMiddleware.RequirePermission("marketing:update"), adminHandler.UpdateBanner)
			authAdmin.DELETE("/banners/:id", gwMiddleware.RequirePermission("marketing:delete"), adminHandler.DeleteBanner)

			authAdmin.GET("/settings/site", gwMiddleware.RequirePermission("settings:view"), adminHandler.GetSiteSetting)
			authAdmin.PUT("/settings/site", gwMiddleware.RequirePermission("settings:update"), adminHandler.UpdateSiteSetting)
			authAdmin.GET("/settings/payment", gwMiddleware.RequirePermission("settings:view"), adminHandler.GetPaymentSetting)
			authAdmin.PUT("/settings/payment", gwMiddleware.RequirePermission("settings:update"), adminHandler.UpdatePaymentSetting)
			authAdmin.POST("/settings/payment/cert", gwMiddleware.RequirePermission("settings:update"), adminHandler.UploadPaymentCert)

			// 渠道管理（系统空间）：客户端渠道主数据（凭证/启用/排序），登录与支付链路按渠道取用
			authAdmin.GET("/settings/channels", gwMiddleware.RequirePermission("settings:view"), adminHandler.GetClientChannels)
			authAdmin.PUT("/settings/channels/:id", gwMiddleware.RequirePermission("settings:update"), adminHandler.UpdateClientChannel)

			// 退款配置（运费退还策略）
			authAdmin.GET("/settings/refund", gwMiddleware.RequirePermission("settings:view"), adminHandler.GetRefundSetting)
			authAdmin.PUT("/settings/refund", gwMiddleware.RequirePermission("settings:update"), adminHandler.UpdateRefundSetting)

			// 附件：配置管理、本地上传与 OSS 预签名直传
			attachmentHandler := handler.NewAttachmentHandler(clients.ProductClient)
			authAdmin.GET("/settings/attachment", gwMiddleware.RequirePermission("settings:view"), attachmentHandler.GetAttachmentSetting)
			authAdmin.PUT("/settings/attachment", gwMiddleware.RequirePermission("settings:update"), attachmentHandler.UpdateAttachmentSetting)
			authAdmin.POST("/upload/image", gwMiddleware.RequirePermission("settings:update"), attachmentHandler.UploadImage)
			authAdmin.POST("/attachment/presign", gwMiddleware.RequirePermission("settings:update"), attachmentHandler.AttachmentPresign)
			authAdmin.POST("/attachment/confirm", gwMiddleware.RequirePermission("settings:update"), attachmentHandler.AttachmentConfirm)
			// 附件选择器：数据按上传管理员隔离，登录即可访问（不要求 settings 权限）
			authAdmin.GET("/attachment/list", attachmentHandler.ListAttachments)

			// 附件迁移（本地 → OSS）：清单扫描、条目迁移/验证/删除本地文件
			authAdmin.POST("/attachment/migrate/scan", gwMiddleware.RequirePermission("settings:update"), attachmentHandler.ScanAttachmentMigrate)
			authAdmin.GET("/attachment/migrate/items", gwMiddleware.RequirePermission("settings:view"), attachmentHandler.ListAttachmentMigrate)
			authAdmin.POST("/attachment/migrate/delete-verified", gwMiddleware.RequirePermission("settings:update"), attachmentHandler.DeleteAttachmentLocalVerified)
			authAdmin.POST("/attachment/migrate/:id/migrate", gwMiddleware.RequirePermission("settings:update"), attachmentHandler.MigrateAttachmentItem)
			authAdmin.POST("/attachment/migrate/:id/verify", gwMiddleware.RequirePermission("settings:update"), attachmentHandler.VerifyAttachmentItem)
			authAdmin.POST("/attachment/migrate/:id/delete-local", gwMiddleware.RequirePermission("settings:update"), attachmentHandler.DeleteAttachmentLocal)

			authAdmin.GET("/users", gwMiddleware.RequirePermission("user:view"), adminHandler.ListUsers)
			authAdmin.POST("/users", gwMiddleware.RequirePermission("user:create"), adminHandler.CreateUser)
			authAdmin.PUT("/users/:id/status", gwMiddleware.RequirePermission("user:update"), adminHandler.SetUserStatus)

			// 全局账号管理（系统管理空间，处理器内强制超管）：仅管账号本身，任职走各社区员工管理
			authAdmin.GET("/accounts", adminHandler.ListAllAccounts)
			authAdmin.POST("/accounts", adminHandler.CreateAccount)
			authAdmin.GET("/accounts/:id/assignments", adminHandler.GetAccountAssignments)
			authAdmin.PUT("/accounts/:id/status", adminHandler.UpdateAccountStatus)
			authAdmin.PUT("/accounts/:id/password", adminHandler.ResetAccountPassword)

			authAdmin.GET("/communities", gwMiddleware.RequirePermission("community:view"), adminHandler.ListAdminCommunities)
			authAdmin.GET("/communities/:id", gwMiddleware.RequirePermission("community:view"), adminHandler.GetCommunity)
			authAdmin.POST("/communities", gwMiddleware.RequirePermission("community:create"), adminHandler.CreateCommunity)
			authAdmin.PUT("/communities/:id", gwMiddleware.RequirePermission("community:update"), adminHandler.UpdateCommunity)
			authAdmin.DELETE("/communities/:id", gwMiddleware.RequirePermission("community:delete"), adminHandler.DeleteCommunity)
			// 门店设置：社区管理员维护本社区资料与配送配置（白名单字段，
			// 不含名称/地址/经纬度/负责人电话等仅超管可改的信息），目标社区取当前社区语境
			authAdmin.GET("/community/profile", gwMiddleware.RequirePermission("community:config"), adminHandler.GetCommunityProfile)
			authAdmin.PUT("/community/profile", gwMiddleware.RequirePermission("community:config"), adminHandler.UpdateCommunityProfile)
			// 社区权限分配：读写该社区权限上限（内置社区管理员角色权限集），处理器内强制超管
			authAdmin.GET("/communities/:id/permissions", gwMiddleware.RequirePermission("community:view"), adminHandler.GetCommunityPermissions)
			authAdmin.PUT("/communities/:id/permissions", gwMiddleware.RequirePermission("community:view"), adminHandler.UpdateCommunityPermissions)
			// 社区管理员指定（社区编辑页，处理器内强制超管）
			authAdmin.GET("/communities/:id/admins", adminHandler.ListCommunityAdmins)
			authAdmin.POST("/communities/:id/admins", adminHandler.AddCommunityAdmin)
			authAdmin.DELETE("/communities/:id/admins/:adminId", adminHandler.RemoveCommunityAdmin)

			// 行政区划：列表查看 + 更新任务（检查 → 轮询差异 → 确认覆盖）
			authAdmin.GET("/regions", gwMiddleware.RequirePermission("region:view"), regionHandler.ListRegions)
			authAdmin.POST("/regions/update/check", gwMiddleware.RequirePermission("region:update"), regionHandler.CreateUpdateTask)
			authAdmin.GET("/regions/update/tasks/:id", gwMiddleware.RequirePermission("region:view"), regionHandler.GetUpdateTask)
			authAdmin.POST("/regions/update/tasks/:id/apply", gwMiddleware.RequirePermission("region:update"), regionHandler.ApplyUpdateTask)

			authAdmin.GET("/refunds", gwMiddleware.RequirePermission("refund:view"), adminHandler.ListRefunds)
			authAdmin.POST("/refunds/:id/approve", gwMiddleware.RequirePermission("refund:update"), adminHandler.ApproveRefund)
			authAdmin.POST("/refunds/:id/reject", gwMiddleware.RequirePermission("refund:update"), adminHandler.RejectRefund)
			authAdmin.POST("/refunds/:id/retry", gwMiddleware.RequirePermission("refund:update"), adminHandler.RetryRefund)
		}
	}

	// 根路径自动跳转到 H5 静态资源入口
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/s/")
	})

	r.Static("/s", h5Path)
	r.Static("/b", adminPath)

	// 本地附件静态服务：与 product 服务落盘目录（<bin>/attachment/）保持一致，
	// URL 形如 /uploads/images/{Ymd}/{dd}/xxx.jpg
	attachmentPath := filepath.Join(getBinDir(), "attachment")
	r.Static("/uploads", attachmentPath)

	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/b/") {
			c.File(filepath.Join(adminPath, "index.html"))
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/s/") {
			c.File(filepath.Join(h5Path, "index.html"))
			return
		}
		response.Error(c, errcode.NotFound)
	})
}

// shutdown 优雅关闭 HTTP 服务
// 参数：
//   - srv: HTTP 服务实例
func shutdown(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Gateway 关闭异常: %v", err)
	}
}

// getBinDir 获取可执行文件所在目录
// 返回：
//   - string: 目录路径
func getBinDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// resolveBinPath 将相对路径解析为可执行文件所在目录下的绝对路径，绝对路径原样返回
// 用于数据文件（如 GeoLite2-City 数据库）等运行时资源定位
// 参数：
//   - p: 配置中的路径
//
// 返回：
//   - string: 绝对路径
func resolveBinPath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(getBinDir(), p)
}

// staticCacheMiddleware 静态资源缓存策略中间件
//   - 带 hash 文件名的 /b/assets/、/s/assets/ 资源内容不可变，允许一年长缓存（immutable），
//     避免重复下载大体积 JS/CSS
//   - 前端 HTML 入口与路由兜底页（/b、/s 前缀其余路径）禁止缓存（no-cache），
//     确保重新构建后浏览器立即获取引用新 hash 资源的最新 HTML，
//     避免旧 HTML 引用已被构建删除的旧 hash 资源导致 404 白屏
//
// 使用示例：r.Use(staticCacheMiddleware())
// 参数：
//   - 无
//
// 返回：
//   - gin.HandlerFunc: 中间件处理函数
func staticCacheMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		switch {
		case strings.HasPrefix(p, "/b/assets/"), strings.HasPrefix(p, "/s/assets/"):
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		case p == "/b" || strings.HasPrefix(p, "/b/"),
			p == "/s" || strings.HasPrefix(p, "/s/"):
			c.Header("Cache-Control", "no-cache")
		}
		c.Next()
	}
}

// _ 占位，避免 errcode 未使用告警
var _ = errcode.Success
