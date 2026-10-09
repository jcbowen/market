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

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gwMiddleware.RequestLoggerMiddleware())
	r.Use(gwMiddleware.SecurityHeadersMiddleware())
	r.Use(gwMiddleware.BlockTraceMiddleware())
	r.Use(gwMiddleware.RateLimitMiddleware(100, 150))

	baseMw := jcMiddleware.Base{}
	r.Use(baseMw.Cors())
	r.Use(baseMw.SetRealIP(true))
	r.Use(baseMw.SetGPC(r))

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

// loadConfig 加载全局配置
// 优先读取 CONFIG_FILE 环境变量指定的配置文件，未设置时使用可执行文件同级目录下的 config.yaml
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
	return (*opt.GetConfig()).(*config.Config)
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
	r.GET("/health", func(c *gin.Context) {
		response.Success(c, gin.H{
			"service": "gateway",
			"time":    time.Now().Unix(),
		})
	})

	userHandler := handler.NewUserHandler(clients.UserClient)
	productHandler := handler.NewProductHandler(clients.ProductClient)
	cartHandler := handler.NewCartHandler(clients.CartClient)
	orderHandler := handler.NewOrderHandler(clients.OrderClient, clients.UserClient, clients.PaymentClient)
	paymentHandler := handler.NewPaymentHandler(clients.PaymentClient, clients.OrderClient, clients.UserClient)
	authHandler := handler.NewAuthHandler(jwtManager, clients.UserClient, cfg.Wechat)

	apiV1 := r.Group("/api/v1")
	{
		// 公开接口
		public := apiV1.Group("")
		{
			public.GET("/communities", userHandler.ListCommunities)
			public.GET("/products", productHandler.ListProducts)
			public.GET("/products/:id", productHandler.GetProduct)
			public.GET("/skus", productHandler.ListSkus)
			public.GET("/skus/:id", productHandler.GetSkuById)
			public.GET("/categories", productHandler.ListCategories)
			public.GET("/coupons", productHandler.ListCoupons)
			public.GET("/flash-sales", productHandler.ListFlashSales)
			public.GET("/full-reductions", productHandler.ListFullReductions)
			public.GET("/banners", productHandler.ListBanners)

			public.POST("/auth/phone-code", authHandler.SendPhoneCode)
			public.POST("/auth/login/phone", authHandler.PhoneLogin)
			public.POST("/auth/weixin-login", authHandler.WechatLogin)
			public.POST("/auth/refresh", authHandler.RefreshToken)

			public.POST("/payments/callback/wechat", paymentHandler.WechatCallback)
			public.POST("/payments/callback/alipay", paymentHandler.AlipayCallback)
			public.GET("/payments/channels", paymentHandler.PaymentChannels)
		}

		// 用户端需登录
		authUser := apiV1.Group("")
		authUser.Use(gwMiddleware.AuthMiddleware(jwtManager, jwt.TokenTypeUser))
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

			authUser.POST("/orders", orderHandler.CreateOrder)
			authUser.GET("/orders", orderHandler.ListOrders)
			authUser.GET("/orders/:id", orderHandler.GetOrder)
			authUser.PUT("/orders/:id/cancel", orderHandler.CancelOrder)
			authUser.POST("/orders/:id/receive", orderHandler.ConfirmReceive)
			authUser.POST("/orders/:id/refund", orderHandler.ApplyRefund)

			authUser.POST("/payments", paymentHandler.CreatePayment)
			authUser.GET("/payments/:id", paymentHandler.GetPayment)

			authUser.POST("/coupons/receive", productHandler.ReceiveCoupon)
			authUser.GET("/user/coupons", productHandler.ListUserCoupons)
		}
	}

	adminHandler := handler.NewAdminHandler(jwtManager, clients.UserClient, clients.ProductClient, clients.OrderClient, clients.PaymentClient, cfg)
	admin := r.Group("/api/v1/admin")
	{
		admin.POST("/auth/login", adminHandler.Login)
		admin.POST("/auth/refresh", adminHandler.RefreshToken)

		authAdmin := admin.Group("")
		authAdmin.Use(gwMiddleware.AuthMiddleware(jwtManager, jwt.TokenTypeAdmin))
		{
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
			authAdmin.GET("/products/:id", gwMiddleware.RequirePermission("product:view"), adminHandler.GetAdminProduct)
			authAdmin.POST("/products", gwMiddleware.RequirePermission("product:create"), adminHandler.CreateProduct)
			authAdmin.PUT("/products/:id", gwMiddleware.RequirePermission("product:update"), adminHandler.UpdateProduct)
			authAdmin.DELETE("/products/:id", gwMiddleware.RequirePermission("product:delete"), adminHandler.DeleteProduct)
			authAdmin.POST("/products/:id/status", gwMiddleware.RequirePermission("product:update"), adminHandler.UpdateProductStatus)

			authAdmin.GET("/orders", gwMiddleware.RequirePermission("order:view"), adminHandler.ListAdminOrders)
			authAdmin.GET("/orders/:id", gwMiddleware.RequirePermission("order:view"), adminHandler.GetAdminOrder)
			authAdmin.PUT("/orders/:id/status", gwMiddleware.RequirePermission("order:update"), adminHandler.UpdateOrderStatus)
			authAdmin.POST("/orders/:id/ship", gwMiddleware.RequirePermission("order:update"), adminHandler.ShipOrder)
			authAdmin.PUT("/orders/:id/complete", gwMiddleware.RequirePermission("order:update"), adminHandler.CompleteOrder)

			authAdmin.GET("/statistics/orders", gwMiddleware.RequirePermission("order:view", "statistics:view"), adminHandler.GetOrderStatistics)
			authAdmin.GET("/statistics/sales", gwMiddleware.RequirePermission("order:view", "statistics:view"), adminHandler.GetSalesStatistics)
			authAdmin.GET("/statistics/rankings", gwMiddleware.RequirePermission("order:view", "statistics:view"), adminHandler.GetProductRankings)
			authAdmin.GET("/statistics/revenue", gwMiddleware.RequirePermission("order:view", "statistics:view"), adminHandler.GetRevenueReport)

			authAdmin.GET("/inventory/entries", gwMiddleware.RequirePermission("inventory:view"), adminHandler.ListStockRecords)
			authAdmin.POST("/inventory/entries", gwMiddleware.RequirePermission("inventory:create"), adminHandler.StockEntry)
			authAdmin.POST("/inventory/out", gwMiddleware.RequirePermission("inventory:create"), adminHandler.StockOut)
			authAdmin.POST("/inventory/purchase-order", gwMiddleware.RequirePermission("inventory:view"), adminHandler.GeneratePurchaseOrder)
			authAdmin.GET("/inventory/warnings", gwMiddleware.RequirePermission("inventory:view"), adminHandler.GetStockWarnings)
			authAdmin.GET("/inventory/skus", gwMiddleware.RequirePermission("inventory:view"), adminHandler.ListSkuStock)
			authAdmin.GET("/inventory/checks", gwMiddleware.RequirePermission("inventory:view"), adminHandler.ListStockChecks)
			authAdmin.GET("/inventory/checks/:id", gwMiddleware.RequirePermission("inventory:view"), adminHandler.GetStockCheckDetail)
			authAdmin.POST("/inventory/checks", gwMiddleware.RequirePermission("inventory:create"), adminHandler.CreateStockCheck)
			authAdmin.POST("/inventory/checks/:id/submit", gwMiddleware.RequirePermission("inventory:update"), adminHandler.SubmitStockCheck)

			authAdmin.GET("/coupons", gwMiddleware.RequirePermission("marketing:view"), adminHandler.ListAdminCoupons)
			authAdmin.POST("/coupons", gwMiddleware.RequirePermission("marketing:create"), adminHandler.CreateCoupon)
			authAdmin.PUT("/coupons/:id", gwMiddleware.RequirePermission("marketing:update"), adminHandler.UpdateCoupon)
			authAdmin.DELETE("/coupons/:id", gwMiddleware.RequirePermission("marketing:delete"), adminHandler.DeleteCoupon)

			authAdmin.GET("/flash-sales", gwMiddleware.RequirePermission("marketing:view"), adminHandler.ListAdminFlashSales)
			authAdmin.POST("/flash-sales", gwMiddleware.RequirePermission("marketing:create"), adminHandler.CreateFlashSale)
			authAdmin.PUT("/flash-sales/:id", gwMiddleware.RequirePermission("marketing:update"), adminHandler.UpdateFlashSale)

			authAdmin.GET("/full-reductions", gwMiddleware.RequirePermission("marketing:view"), adminHandler.ListFullReductions)
			authAdmin.POST("/full-reductions", gwMiddleware.RequirePermission("marketing:create"), adminHandler.CreateFullReduction)
			authAdmin.PUT("/full-reductions/:id", gwMiddleware.RequirePermission("marketing:update"), adminHandler.UpdateFullReduction)

			authAdmin.GET("/banners", gwMiddleware.RequirePermission("marketing:view"), adminHandler.ListAdminBanners)
			authAdmin.POST("/banners", gwMiddleware.RequirePermission("marketing:create"), adminHandler.CreateBanner)
			authAdmin.PUT("/banners/:id", gwMiddleware.RequirePermission("marketing:update"), adminHandler.UpdateBanner)
			authAdmin.DELETE("/banners/:id", gwMiddleware.RequirePermission("marketing:delete"), adminHandler.DeleteBanner)

			authAdmin.GET("/settings/payment", gwMiddleware.RequirePermission("settings:view"), adminHandler.GetPaymentSetting)
			authAdmin.PUT("/settings/payment", gwMiddleware.RequirePermission("settings:update"), adminHandler.UpdatePaymentSetting)
			authAdmin.POST("/settings/payment/cert", gwMiddleware.RequirePermission("settings:update"), adminHandler.UploadPaymentCert)

			authAdmin.GET("/users", gwMiddleware.RequirePermission("user:view"), adminHandler.ListUsers)

			authAdmin.GET("/communities", gwMiddleware.RequirePermission("community:view"), adminHandler.ListAdminCommunities)
			authAdmin.POST("/communities", gwMiddleware.RequirePermission("community:create"), adminHandler.CreateCommunity)
			authAdmin.PUT("/communities/:id", gwMiddleware.RequirePermission("community:update"), adminHandler.UpdateCommunity)
			authAdmin.DELETE("/communities/:id", gwMiddleware.RequirePermission("community:delete"), adminHandler.DeleteCommunity)

			authAdmin.GET("/refunds", gwMiddleware.RequirePermission("refund:view"), adminHandler.ListRefunds)
			authAdmin.POST("/refunds/:id/approve", gwMiddleware.RequirePermission("refund:update"), adminHandler.ApproveRefund)
			authAdmin.POST("/refunds/:id/reject", gwMiddleware.RequirePermission("refund:update"), adminHandler.RejectRefund)
		}
	}

	h5Path := filepath.Join(getBinDir(), "static", "h5")

	// 根路径自动跳转到 H5 静态资源入口
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/s/")
	})

	r.Static("/s", h5Path)

	adminPath := filepath.Join(getBinDir(), "static", "admin")
	r.Static("/b", adminPath)

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

// _ 占位，避免 errcode 未使用告警
var _ = errcode.Success
