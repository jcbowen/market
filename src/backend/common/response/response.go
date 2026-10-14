package response

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"reflect"

	"github.com/gin-gonic/gin"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
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
		Data:    normalizeData(data),
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
			List:     normalizeData(normalizeList(list)),
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
		Data:    normalizeData(normalizeList(list)),
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

// normalizeData 统一规范化响应数据：
// 将 proto message（含切片/映射中嵌套的）转换为全字段 map，
// 解决 proto 生成代码 `json:"...,omitempty"` 把零值字段（如 status=0 停业、
// sort=0、delivery_fee=0）从 JSON 输出中省略，导致前端读到 undefined 的问题。
//
// 转换语义与现有 encoding/json 输出保持兼容：
//   - 字段名使用 proto 字段名（snake_case），与生成代码 json tag 第一段一致
//   - int64/uint64 保持 Go 数值类型（序列化为 JSON 数字），不用 protojson
//     （protojson 会把 64 位整数输出为字符串，前端按数字比较 id 会全量 breaking）
//   - enum 输出数值，bytes 输出 base64（同 encoding/json 默认行为）
func normalizeData(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	if m, ok := v.(proto.Message); ok {
		// nil / typed nil 由 protoToMap 统一防御（它还服务 protoValueToInterface 路径），
		// 对 typed nil 返回 nil map，JSON 序列化同样输出 null
		return protoToMap(m)
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return v
		}
		out := make([]interface{}, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = normalizeData(rv.Index(i).Interface())
		}
		return out
	case reflect.Map:
		out := make(map[string]interface{}, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[fmt.Sprintf("%v", iter.Key().Interface())] = normalizeData(iter.Value().Interface())
		}
		return out
	default:
		return v
	}
}

// protoToMap 将 proto message 转换为 map[string]interface{}，输出全部字段（含零值）。
// 未 set 的 oneof 成员输出 null（与原 oneof 接口字段为 nil 时的输出一致）。
// 入参为 nil 或 typed nil（含未设置嵌套 message 字段的 reflect 零值）时返回 nil。
func protoToMap(m proto.Message) map[string]interface{} {
	if m == nil {
		return nil
	}
	if rv := reflect.ValueOf(m); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return nil
	}
	pr := m.ProtoReflect()
	fields := pr.Descriptor().Fields()
	out := make(map[string]interface{}, fields.Len())
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		name := string(fd.Name())
		switch {
		case fd.IsList():
			list := pr.Get(fd).List()
			arr := make([]interface{}, 0, list.Len())
			for j := 0; j < list.Len(); j++ {
				arr = append(arr, protoValueToInterface(list.Get(j), fd))
			}
			out[name] = arr
		case fd.IsMap():
			mp := pr.Get(fd).Map()
			mm := make(map[string]interface{}, mp.Len())
			mp.Range(func(k protoreflect.MapKey, val protoreflect.Value) bool {
				mm[fmt.Sprintf("%v", k.Interface())] = protoValueToInterface(val, fd.MapValue())
				return true
			})
			out[name] = mm
		case fd.ContainingOneof() != nil && !pr.Has(fd):
			out[name] = nil
		default:
			out[name] = protoValueToInterface(pr.Get(fd), fd)
		}
	}
	return out
}

// protoValueToInterface 将单个 proto 字段值转换为 JSON 兼容的 Go 值
func protoValueToInterface(v protoreflect.Value, fd protoreflect.FieldDescriptor) interface{} {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		if !v.IsValid() {
			return nil
		}
		return protoToMap(v.Message().Interface())
	case protoreflect.BytesKind:
		return base64.StdEncoding.EncodeToString(v.Bytes())
	case protoreflect.EnumKind:
		return int32(v.Enum())
	default:
		return v.Interface()
	}
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
		Data:    normalizeData(data),
	})
}

// Raw 返回原始响应结构
// 参数：
//   - c: gin 上下文
//   - resp: 自定义响应结构
func Raw(c *gin.Context, resp Response) {
	c.JSON(http.StatusOK, resp)
}
