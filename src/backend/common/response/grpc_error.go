package response

import (
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/errcode"
)

// grpcCodeMap gRPC 状态码到业务错误码的映射
// 仅收敛有明确业务语义的状态码，其余一律归为服务器内部错误
var grpcCodeMap = map[codes.Code]int{
	codes.NotFound:           errcode.NotFound,
	codes.InvalidArgument:    errcode.ParamError,
	codes.AlreadyExists:      errcode.DataAlreadyExists,
	codes.PermissionDenied:   errcode.NoPermission,
	codes.Unauthenticated:    errcode.NotLoginOrTokenExpired,
	codes.FailedPrecondition: errcode.OperationNotAllowed,
	codes.ResourceExhausted:  errcode.ProductStockInsufficient,
}

// rpcWrapperPattern 匹配 gRPC 错误字符串中的 "rpc error: code = Xxx desc = " 包装
// 服务层以 %w 包装依赖错误时该前缀会残留在消息中，对外展示前需剥除
var rpcWrapperPattern = regexp.MustCompile(`rpc error: code = \w+ desc = `)

// infraCodes 基础设施类 gRPC 状态码：消息可能包含连接串、SQL 报错等内部细节，
// 一律不透传原始消息，统一返回对应业务错误码的通用文案
var infraCodes = map[codes.Code]bool{
	codes.Internal:         true,
	codes.Unavailable:      true,
	codes.DeadlineExceeded: true,
	codes.Canceled:         true,
	codes.DataLoss:         true,
	codes.Unimplemented:    true,
	codes.Unauthenticated:  true,
}

// TranslateGRPCError 将 gRPC 错误翻译为业务错误码与对外提示
// 剥离 "rpc error: code = xxx desc = ..." 包装，只暴露服务返回的业务文案；
// 按状态码归类（不再一律返回 10002）；基础设施类状态码（服务不可用/超时/内部错误等）
// 统一返回通用文案，防止内部实现细节泄露给客户端
// 参数：
//   - err: gRPC 调用返回的错误
//
// 返回值：
//   - int: 业务错误码
//   - string: 对外提示文案
func TranslateGRPCError(err error) (int, string) {
	st, ok := status.FromError(err)
	if !ok {
		return errcode.InternalServerError, errcode.GetMessage(errcode.InternalServerError)
	}

	code, mapped := grpcCodeMap[st.Code()]
	if !mapped {
		code = errcode.InternalServerError
	}

	msg := rpcWrapperPattern.ReplaceAllString(st.Message(), "")
	// 基础设施类错误与非业务语义状态码不透传服务端原始消息
	if msg == "" || !mapped || infraCodes[st.Code()] {
		return code, errcode.GetMessage(code)
	}
	return code, msg
}

// GRPCError 返回 gRPC 错误对应的统一错误响应
// 参数：
//   - c: gin 上下文
//   - err: gRPC 调用返回的错误
func GRPCError(c *gin.Context, err error) {
	code, msg := TranslateGRPCError(err)
	c.JSON(http.StatusOK, Response{
		Code:    code,
		Message: msg,
	})
}
