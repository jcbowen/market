package response

import (
	"net/http"
	"reflect"

	"github.com/gin-gonic/gin"
	"github.com/jcbowen/market/src/backend/common/errcode"
)

// Response 统一响应结构体
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	Total   *int        `json:"total,omitempty"`
}

// PageData 分页数据
type PageData struct {
	List     interface{} `json:"list"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

// Success 返回成功响应
// 参数：
//   - c: gin 上下文
//   - data: 响应数据
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:    errcode.Success,
		Message: "success",
		Data:    data,
	})
}

// SuccessWithPage 返回分页成功响应
// 参数：
//   - c: gin 上下文
//   - list: 列表数据
//   - total: 总条数
//   - page: 当前页
//   - pageSize: 每页条数
func SuccessWithPage(c *gin.Context, list interface{}, total, page, pageSize int) {
	c.JSON(http.StatusOK, Response{
		Code:    errcode.Success,
		Message: "success",
		Data: PageData{
			List:     normalizeList(list),
			Total:    total,
			Page:     page,
			PageSize: pageSize,
		},
	})
}

// SuccessWithList 返回列表成功响应，自动将 nil 切片转换为空数组
// 参数：
//   - c: gin 上下文
//   - list: 列表数据
func SuccessWithList(c *gin.Context, list interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:    errcode.Success,
		Message: "success",
		Data:    normalizeList(list),
	})
}

// normalizeList 将 nil 切片转换为空数组，避免前端解析为 null
// 参数：
//   - list: 列表数据
//
// 返回值：
//   - interface{}: 非 nil 的列表数据
func normalizeList(list interface{}) interface{} {
	if list == nil {
		return []interface{}{}
	}
	v := reflect.ValueOf(list)
	if v.Kind() == reflect.Slice && v.IsNil() {
		return reflect.MakeSlice(v.Type(), 0, 0).Interface()
	}
	return list
}

// Error 返回错误响应
// 参数：
//   - c: gin 上下文
//   - code: 错误码
//   - message: 错误信息，为空时自动根据错误码获取
func Error(c *gin.Context, code int, message ...string) {
	msg := errcode.GetMessage(code)
	if len(message) > 0 && message[0] != "" {
		msg = message[0]
	}
	c.JSON(http.StatusOK, Response{
		Code:    code,
		Message: msg,
	})
}

// ErrorWithData 返回带数据的错误响应
// 参数：
//   - c: gin 上下文
//   - code: 错误码
//   - data: 响应数据
//   - message: 错误信息
func ErrorWithData(c *gin.Context, code int, data interface{}, message ...string) {
	msg := errcode.GetMessage(code)
	if len(message) > 0 && message[0] != "" {
		msg = message[0]
	}
	c.JSON(http.StatusOK, Response{
		Code:    code,
		Message: msg,
		Data:    data,
	})
}

// Raw 返回原始响应结构
// 参数：
//   - c: gin 上下文
//   - resp: 自定义响应结构
func Raw(c *gin.Context, resp Response) {
	c.JSON(http.StatusOK, resp)
}
