package eventbus

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestParseExportTaskEvent 导出任务事件的序列/反序列化契约
// 两侧（order 服务发布、gateway 订阅）共用本结构，字段名与 JSON 口径在此固化：
// 载荷只带 ID/计数/失败原因，不得出现文件路径（下载走带归属校验的接口）
// 参数：
//   - t: 测试句柄
func TestParseExportTaskEvent(t *testing.T) {
	payload, err := json.Marshal(ExportTaskEvent{
		TaskID:      9,
		AdminID:     3,
		CommunityID: 1,
		ExportType:  "orders",
		State:       ExportTaskStateDone,
		FileName:    "orders-20261027.csv",
		RowCount:    1200,
		ErrorMsg:    "已达单次导出上限 5000 行，仅导出前 1200 行，请缩小筛选范围",
		EventTime:   "2026-10-27 10:00:00",
	})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	evt := ParseExportTaskEvent(payload)
	if evt == nil {
		t.Fatalf("解析失败, payload=%s", payload)
	}
	if evt.TaskID != 9 || evt.AdminID != 3 || evt.CommunityID != 1 {
		t.Errorf("ID 字段解析错误: %+v", evt)
	}
	if evt.State != ExportTaskStateDone || evt.RowCount != 1200 || evt.FileName != "orders-20261027.csv" {
		t.Errorf("状态/行数/文件名解析错误: %+v", evt)
	}
	if evt.ExportType != "orders" {
		t.Errorf("导出类型解析错误: %+v", evt)
	}

	// 契约红线：载荷不出现文件路径字段，也不出现夹带路径的取值
	// （只查键名挡不住 error_msg 里塞进 os.MkdirAll 的完整落盘路径）
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("反解为 map 失败: %v", err)
	}
	for _, v := range assertNoPathBearingValue(raw, "导出事件") {
		t.Error(v)
	}

	if ParseExportTaskEvent([]byte("not-json")) != nil {
		t.Error("非法 JSON 应返回 nil")
	}
}

// TestExportTaskEventRejectsPathBearingMsg 反向用例：未脱敏的落盘路径必须被契约断言拦下
// 用一个真实泄漏形态（error_msg 带 /uploads/exports 目录）证明这条红线不是空转
//
// 参数：
//   - t: 测试句柄
func TestExportTaskEventRejectsPathBearingMsg(t *testing.T) {
	payload, err := json.Marshal(ExportTaskEvent{
		TaskID:   10,
		State:    ExportTaskStateFailed,
		ErrorMsg: `创建导出目录失败: mkdir /app/uploads/exports: permission denied`,
	})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("反解为 map 失败: %v", err)
	}

	if len(assertNoPathBearingValue(raw, "导出事件")) == 0 {
		t.Error("带落盘路径的 error_msg 未被契约断言拦下，说明红线形同虚设")
	}
}

// assertNoPathBearingValue 扫描事件载荷的键与取值，返回违反路径红线的描述（空表示合规）
// 契约红线有两层：字段名不得是路径字段，任何字符串取值也不得夹带落盘路径
// （发布侧的 error_msg 属管理员可读文案，历史或第三方错误可能带完整目录，须由发布方脱敏）。
// 返回违规列表而非直接判红，是为了让反向用例能证明这条断言真的会拦下泄漏
//
// 参数：
//   - payload: 已反解为 map 的载荷
//   - label: 事件名，用于标注违规出处
//
// 返回值：
//   - []string: 违规描述，合规时为空
func assertNoPathBearingValue(payload map[string]any, label string) []string {
	var violations []string
	pathMarkers := []string{"/uploads/", `\uploads\`}
	for key, val := range payload {
		if key == "file_path" || key == "path" {
			violations = append(violations, label+" 携带了路径字段 "+key)
		}
		s, ok := val.(string)
		if !ok {
			continue
		}
		for _, marker := range pathMarkers {
			if strings.Contains(s, marker) {
				violations = append(violations, label+" 字段 "+key+" 的取值夹带了落盘路径: "+s)
			}
		}
	}
	return violations
}

// TestParsePaymentAnomalyEvent 支付异常告警事件的序列/反序列化契约
// 两侧（payment 服务发布、gateway 订阅）共用本结构，字段名与 JSON 口径在此固化：
// 载荷只带定位凭据与金额，不得出现原始报文（raw_body）与对账轨迹
// 参数：
//   - t: 测试句柄
func TestParsePaymentAnomalyEvent(t *testing.T) {
	payload, err := json.Marshal(PaymentAnomalyEvent{
		Kind:        3,
		OrderNo:     "O20261027001",
		PaymentNo:   "PAY20261027001",
		CommunityID: 1,
		Amount:      88.5,
		EventTime:   "2026-10-27 10:00:00",
	})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	evt := ParsePaymentAnomalyEvent(payload)
	if evt == nil {
		t.Fatalf("解析失败, payload=%s", payload)
	}
	if evt.Kind != 3 || evt.OrderNo != "O20261027001" || evt.PaymentNo != "PAY20261027001" {
		t.Errorf("定位凭据解析错误: %+v", evt)
	}
	if evt.CommunityID != 1 || evt.Amount != 88.5 || evt.EventTime != "2026-10-27 10:00:00" {
		t.Errorf("社区/金额/时间解析错误: %+v", evt)
	}

	// 契约红线：载荷不出现原始报文与请求头字段（排查走异常队列接口），取值亦不得带落盘路径
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("反解为 map 失败: %v", err)
	}
	for key := range raw {
		if key == "raw_body" || key == "headers" {
			t.Errorf("支付异常事件携带了报文字段 %q", key)
		}
	}
	for _, v := range assertNoPathBearingValue(raw, "支付异常事件") {
		t.Error(v)
	}

	if ParsePaymentAnomalyEvent([]byte("not-json")) != nil {
		t.Error("非法 JSON 应返回 nil")
	}
}
