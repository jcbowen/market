package response

import (
	"encoding/json"
	"testing"

	"github.com/jcbowen/market/src/backend/common/proto/user"
)

// 零值字段必须出现在 JSON 输出中（omitempty 曾导致停业社区 status=0 被省略，前端读到 undefined）
func TestNormalizeData_ProtoZeroValuesEmitted(t *testing.T) {
	m := normalizeData(&user.CommunityInfo{
		Id:     5,
		Name:   "测试社区",
		Status: 0,
	})
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)

	// 零值字段必须输出
	for _, want := range []string{`"status":0`, `"sort":0`, `"delivery_fee":0`, `"min_order_amount":0`, `"allow_outside_delivery":false`, `"longitude":0`} {
		if !contains(got, want) {
			t.Errorf("输出缺少零值字段 %s，实际: %s", want, got)
		}
	}
	// 字段名保持 snake_case，与生成代码 json tag 行为一致
	if !contains(got, `"min_order_amount"`) || contains(got, `"minOrderAmount"`) {
		t.Errorf("字段名应为 snake_case，实际: %s", got)
	}
	// int64 保持数字，而非 protojson 的字符串
	if !contains(got, `"id":5`) || contains(got, `"id":"5"`) {
		t.Errorf("int64 应输出为数字，实际: %s", got)
	}
	// 非零字段正常输出
	if !contains(got, `"name":"测试社区"`) {
		t.Errorf("非零字段输出异常，实际: %s", got)
	}
}

// proto 指针为 nil 时不崩溃，JSON 序列化输出 null
func TestNormalizeData_TypedNilProto(t *testing.T) {
	var p *user.CommunityInfo
	b, err := json.Marshal(normalizeData(p))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != "null" {
		t.Errorf("typed nil proto 应输出 null，实际: %s", b)
	}
}

// 切片与 gin.H 中嵌套的 proto 均应被转换
func TestNormalizeData_NestedInSliceAndMap(t *testing.T) {
	list := []*user.CommunityInfo{
		{Id: 1, Name: "A", Status: 1},
		{Id: 2, Name: "B", Status: 0},
	}
	b, err := json.Marshal(normalizeData(ginHForTest(list)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if !contains(got, `"status":1`) || !contains(got, `"status":0`) {
		t.Errorf("列表中嵌套 proto 的零值 status 应输出，实际: %s", got)
	}
}

func ginHForTest(list []*user.CommunityInfo) map[string]interface{} {
	return map[string]interface{}{"list": list, "total": 2}
}

// SuccessWithPage 对 proto 列表转换后，status=0 仍存在于 list 元素中
func TestProtoToMap_StatusZeroPresent(t *testing.T) {
	m := protoToMap(&user.CommunityInfo{Id: 9, Status: 0})
	if _, ok := m["status"]; !ok {
		t.Errorf("protoToMap 输出缺少 status 字段: %v", m)
	}
	if v, _ := m["status"].(int32); v != 0 {
		t.Errorf("status 应为 0，实际: %v", m["status"])
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
