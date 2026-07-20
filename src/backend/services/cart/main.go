package main

import (
	"log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/jcbowen/market/src/backend/common/proto/cart"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/internal/bootstrap"
)

/**
 * main cart-service 主入口
 * 购物车服务：购物车增删改查，基于 Redis 实现
 */
func main() {
	cfg := bootstrap.LoadConfig()

	rdb := bootstrap.InitRedis(cfg)
	if rdb == nil {
		log.Fatalf("初始化 Redis 失败")
	}

	// 连接商品服务，用于加购时补齐商品名称、价格、库存等展示字段
	productConn, err := grpc.NewClient(cfg.Grpc.Product,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("连接商品服务失败: %v", err)
	}
	defer productConn.Close()
	productClient := product.NewProductServiceClient(productConn)

	srv := grpc.NewServer()
	cart.RegisterCartServiceServer(srv, newServer(rdb, productClient))
	bootstrap.RegisterHealth(srv)

	if err := bootstrap.RunGRPCServer(srv, cfg.CartService.GrpcPort); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}
