package service

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/product"
)

// TestRunRejectsWrongConfirmText 校验执行清理的强确认文案闸门：
// 文案不匹配 / 空入参必须被拒绝且不触库（db 为 nil 也不应 panic——校验先于任何查询）。
func TestRunRejectsWrongConfirmText(t *testing.T) {
	svc := NewDataCleanupService(nil, nil)
	cases := []struct {
		name string
		req  *product.DataCleanupRunReq
	}{
		{"文案不匹配", &product.DataCleanupRunReq{ConfirmText: "清空数据"}},
		{"文案为空", &product.DataCleanupRunReq{ConfirmText: ""}},
		{"入参为空", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := svc.Run(context.Background(), c.req)
			if err == nil {
				t.Fatalf("文案不匹配应被拒绝，却返回了 %v", resp)
			}
			var bizErr *bizerr.Error
			if !errors.As(err, &bizErr) || bizErr.Code() != codes.InvalidArgument {
				t.Fatalf("期望业务码 InvalidArgument，得到 %v", err)
			}
		})
	}
}
