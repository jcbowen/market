package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
)

// TestWriteErrorBusinessPassthrough 业务错误按原状态码与文案透出
// 参数：
//   - t: 测试上下文
//
// 返回值：无
func TestWriteErrorBusinessPassthrough(t *testing.T) {
	err := writeError(bizerr.New(codes.InvalidArgument, "不支持的图片格式"), "上传图片")
	st := status.Convert(err)
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("业务错误状态码应保持 InvalidArgument，实际 %v", st.Code())
	}
	if st.Message() != "不支持的图片格式" {
		t.Fatalf("业务文案应原样透出，实际 %q", st.Message())
	}
}

// TestWriteErrorRecordNotFound 原生 gorm 记录不存在转 NotFound
// 参数：
//   - t: 测试上下文
//
// 返回值：无
func TestWriteErrorRecordNotFound(t *testing.T) {
	st := status.Convert(writeError(fmt.Errorf("查询附件记录失败: %w", gorm.ErrRecordNotFound), "确认上传"))
	if st.Code() != codes.NotFound {
		t.Fatalf("记录不存在应转 NotFound，实际 %v", st.Code())
	}
}

// TestWriteErrorInternalHidden 内部故障只回通用文案，原文不出服务边界
// 参数：
//   - t: 测试上下文
//
// 返回值：无
func TestWriteErrorInternalHidden(t *testing.T) {
	raw := "Error 1146 (42S02): Table 'market.attachment' doesn't exist"
	st := status.Convert(writeError(errors.New(raw), "查询附件列表"))
	if st.Code() != codes.Internal {
		t.Fatalf("内部故障应归 Internal，实际 %v", st.Code())
	}
	if strings.Contains(st.Message(), "1146") || strings.Contains(st.Message(), "market.attachment") {
		t.Fatalf("内部错误原文不得透出，实际 %q", st.Message())
	}
}

// TestWriteErrorStatusPassthrough 已是 gRPC 状态错误时原样返回（避免二次包装丢码）
// 参数：
//   - t: 测试上下文
//
// 返回值：无
func TestWriteErrorStatusPassthrough(t *testing.T) {
	origin := status.Error(codes.Aborted, "版本冲突")
	got := writeError(origin, "保存配置")
	if got != origin {
		t.Fatalf("状态错误应原样返回，实际 %v", got)
	}
}
