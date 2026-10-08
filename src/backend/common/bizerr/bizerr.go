// Package bizerr 提供业务错误类型与到 gRPC 状态的统一转换。
//
// 服务层用 New 返回携带 gRPC 状态码的业务错误，使网关能够把"库存不足、
// 订单状态不允许退款"等业务失败翻译成可区分的错误码，而不是统一归为
// codes.Internal；同时对外仅暴露业务文案，不泄露内部实现细节。
package bizerr

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Error 业务错误
// 携带一个 gRPC 状态码与面向用户的文案
type Error struct {
	code codes.Code
	msg  string
}

// Error 实现 error 接口，返回业务文案
// 返回值：
//   - string: 业务文案
func (e *Error) Error() string {
	return e.msg
}

// Code 返回业务错误携带的 gRPC 状态码
// 返回值：
//   - codes.Code: 状态码
func (e *Error) Code() codes.Code {
	return e.code
}

// New 创建业务错误
// 参数：
//   - code: gRPC 状态码，用于网关区分错误类型
//   - msg: 面向用户的业务文案
//
// 返回值：
//   - *Error: 业务错误
func New(code codes.Code, msg string) *Error {
	return &Error{code: code, msg: msg}
}

// ToStatus 将服务层错误统一转换为 gRPC 状态错误
// 规则：
//   - 业务错误（*Error）按其状态码与文案转换
//   - 已是 gRPC 状态错误的原样返回
//   - 其余未知错误归为 codes.Internal
//
// 参数：
//   - err: 服务层返回的错误
//
// 返回值：
//   - error: gRPC 状态错误
func ToStatus(err error) error {
	var be *Error
	if errors.As(err, &be) {
		return status.Error(be.code, be.msg)
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Error(codes.Internal, err.Error())
}
