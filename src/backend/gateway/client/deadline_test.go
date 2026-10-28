package client

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
)

// TestDefaultDeadlineUnaryInterceptor 验证缺省 deadline 兜底：
// 无截止时间的调用被补上缺省值，长任务按方法名覆写，调用方已有的 deadline 不被改写
func TestDefaultDeadlineUnaryInterceptor(t *testing.T) {
	var got time.Duration
	var called bool
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		called = true
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("调用上下文缺少 deadline")
		}
		got = time.Until(deadline)
		return nil
	}

	t.Run("缺省超时", func(t *testing.T) {
		if err := defaultDeadlineUnaryInterceptor(context.Background(), "/product.ProductService/GetProduct", nil, nil, nil, invoker); err != nil {
			t.Fatal(err)
		}
		if !called || got > defaultRPCTimeout || got < defaultRPCTimeout-2*time.Second {
			t.Fatalf("缺省 deadline 异常: %v", got)
		}
	})

	t.Run("长任务按方法名覆写", func(t *testing.T) {
		err := defaultDeadlineUnaryInterceptor(context.Background(),
			"/product.ProductService/ImportDataPackage", nil, nil, nil, invoker)
		if err != nil {
			t.Fatal(err)
		}
		if got < 10*time.Minute-time.Second || got > 10*time.Minute {
			t.Fatalf("导入任务 deadline 应为 10 分钟，实得 %v", got)
		}
	})

	t.Run("调用方已有 deadline 原样透传", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		if err := defaultDeadlineUnaryInterceptor(ctx, "/product.ProductService/GetProduct", nil, nil, nil, invoker); err != nil {
			t.Fatal(err)
		}
		if got < time.Hour-time.Minute {
			t.Fatalf("不应缩短调用方的 deadline，实得 %v", got)
		}
	})
}
