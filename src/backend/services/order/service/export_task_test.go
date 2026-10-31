package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jcbowen/market/src/backend/common/eventbus"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * 导出任务纯函数用例（无外部依赖，CI 可直接跑）
 *
 * 覆盖：
 *   - escapeCSVCell      引号转义与 CSV 公式注入防护（=+-@ 开头加制表符前缀）
 *   - exportCSVRow       单行字段口径（商品明细摘要/金额格式/状态文案）与整行引号包裹
 *   - appendDownloaderRecord 下载留痕追加与 50 条上限截断（防长文本无限膨胀）
 *   - payTypesText/orderStatusTextOf/deliveryTypeTextOf/orderSourceTextOf 文案映射
 *   - shortTaskNo        任务号取前 8 位作文件名随机段
 *   - toListOrdersReq    查询快照还原为 ListOrdersReq 的字段对齐（导出「所见即所导」的基础）
 */

// TestEscapeCSVCell 引号转义与公式注入防护
func TestEscapeCSVCell(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"普通文本不变", "正常商品", "正常商品"},
		{"空串不变", "", ""},
		{"引号成对转义", `含"引号"内容`, `含""引号""内容`},
		{"等号开头加制表符", "=SUM(A1)", "\t=SUM(A1)"},
		{"加号开头加制表符", "+8613800000000", "\t+8613800000000"},
		{"减号开头加制表符", "-1-1", "\t-1-1"},
		{"艾特开头加制表符", "@import", "\t@import"},
		{"引号与公式前缀叠加", `="cmd"`, "\t=\"\"cmd\"\""},
	}
	for _, c := range cases {
		if got := escapeCSVCell(c.in); got != c.want {
			t.Errorf("%s: escapeCSVCell(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestExportCSVRow 单行 CSV 字段口径
func TestExportCSVRow(t *testing.T) {
	o := &model.Order{
		OrderNo:      "ORD-TEST-1",
		DeliveryType: 1, // 自提
		Source:       1, // C端
		Status:       2,
		TotalAmount:  12.5,
		PayAmount:    10.00,
		CreatedAt:    "2026-10-24 10:00:00",
	}
	t.Run("含规格的商品明细", func(t *testing.T) {
		items := []model.OrderItem{
			{ProductName: "苹果", SpecName: "500g", Quantity: 2},
			{ProductName: "香蕉", SpecName: "", Quantity: 1},
		}
		line, err := exportCSVRow(o, items, "张三", "微信支付")
		if err != nil {
			t.Fatalf("exportCSVRow 异常: %v", err)
		}
		s := string(line)
		for _, want := range []string{
			`"ORD-TEST-1"`, `"张三"`, `"自提"`, `"C端"`,
			`"苹果（500g）x2；香蕉 x1"`, `"微信支付"`, `"12.50"`, `"10.00"`, `"0.00"`, `"待自提"`, `"2026-10-24 10:00:00"`,
		} {
			if !strings.Contains(s, want) {
				t.Errorf("CSV 行缺少 %s: %s", want, s)
			}
		}
		if !strings.HasSuffix(s, "\n") {
			t.Error("CSV 行应以换行结尾")
		}
		if strings.Count(s, ",") != 11 {
			t.Errorf("12 列应产生 11 个分隔逗号, got %d: %s", strings.Count(s, ","), s)
		}
	})

	t.Run("已退金额按订单快照输出", func(t *testing.T) {
		refunded := *o
		refunded.RefundedAmount = 3.5
		line, err := exportCSVRow(&refunded, nil, "", payTypesText(0))
		if err != nil {
			t.Fatalf("exportCSVRow 异常: %v", err)
		}
		if !strings.Contains(string(line), `"3.50"`) {
			t.Errorf("已退金额列应输出 3.50: %s", string(line))
		}
	})

	t.Run("恶意内容被防护", func(t *testing.T) {
		items := []model.OrderItem{{ProductName: "=HYPERLINK(\"http://x\")", Quantity: 1}}
		line, err := exportCSVRow(o, items, "@evil", "")
		if err != nil {
			t.Fatalf("exportCSVRow 异常: %v", err)
		}
		s := string(line)
		if strings.Contains(s, `"`+"=HYPERLINK") {
			t.Errorf("公式开头内容未加制表符前缀: %s", s)
		}
		if !strings.Contains(s, "\t=HYPERLINK") || !strings.Contains(s, "\t@evil") {
			t.Errorf("公式注入防护缺失: %s", s)
		}
	})

	t.Run("无明细与未支付留空", func(t *testing.T) {
		line, err := exportCSVRow(o, nil, "", payTypesText(0))
		if err != nil {
			t.Fatalf("exportCSVRow 异常: %v", err)
		}
		s := string(line)
		if !strings.Contains(s, `""`) {
			t.Errorf("空单元格应为空引号对: %s", s)
		}
	})
}

// TestAppendDownloaderRecord 下载留痕追加与上限截断
func TestAppendDownloaderRecord(t *testing.T) {
	t.Run("从空串追加首条", func(t *testing.T) {
		raw := appendDownloaderRecord("", 7, "超管")
		var records []map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &records); err != nil {
			t.Fatalf("留痕 JSON 非法: %v", err)
		}
		if len(records) != 1 || records[0]["admin_name"] != "超管" {
			t.Fatalf("首条留痕不符: %s", raw)
		}
		if records[0]["admin_id"].(float64) != 7 {
			t.Errorf("admin_id = %v, want 7", records[0]["admin_id"])
		}
		if at, _ := records[0]["at"].(string); at == "" {
			t.Error("留痕时间应落 at 字段")
		}
	})

	t.Run("非法原 JSON 不报错可追加", func(t *testing.T) {
		raw := appendDownloaderRecord("not-json", 1, "甲")
		var records []map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &records); err != nil {
			t.Fatalf("非法原 JSON 应回落为重新建数组: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("应只含新追加的一条: %s", raw)
		}
	})

	t.Run("超过 50 条仅保留最近 50 条", func(t *testing.T) {
		// 预置 50 条旧记录，再追加 1 条后总数应为 50 且最旧的一条被挤掉
		olds := make([]map[string]interface{}, 50)
		for i := range olds {
			olds[i] = map[string]interface{}{"admin_id": i, "admin_name": "旧", "at": "2026-01-01 00:00:00"}
		}
		b, _ := json.Marshal(olds)
		raw := appendDownloaderRecord(string(b), 999, "新")
		var records []map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &records); err != nil {
			t.Fatalf("截断后 JSON 非法: %v", err)
		}
		if len(records) != 50 {
			t.Fatalf("应保留 50 条, got %d", len(records))
		}
		if records[0]["admin_id"].(float64) != 1 {
			t.Errorf("最旧的 0 号应被挤掉, 首条 = %v", records[0]["admin_id"])
		}
		last := records[len(records)-1]
		if last["admin_id"].(float64) != 999 || last["admin_name"] != "新" {
			t.Errorf("最新一条应在末尾: %v", last)
		}
	})
}

// TestExportTextMappings 文案映射口径（与 gateway/前端口径一致，防导出列文案漂移）
func TestExportTextMappings(t *testing.T) {
	payCases := map[int32]string{1: "微信支付", 2: "支付宝支付", 3: "货到付款", 4: "现金", 5: "微信付款码", 6: "支付宝付款码", 0: "", 99: ""}
	for code, want := range payCases {
		if got := payTypesText(code); got != want {
			t.Errorf("payTypesText(%d) = %q, want %q", code, got, want)
		}
	}
	statusCases := []struct {
		status, delivery int32
		want             string
	}{
		{1, 0, "待支付"},
		{2, 1, "待自提"},
		{2, 2, "待发货"},
		{3, 2, "备货中"},
		{4, 1, "待自提"},
		{4, 2, "已发货"},
		{5, 0, "已完成"},
		{6, 0, "已取消"},
		{7, 0, "退款中"},
		{8, 0, "已退款"},
		{99, 0, "未知"},
	}
	for _, c := range statusCases {
		if got := orderStatusTextOf(c.status, c.delivery); got != c.want {
			t.Errorf("orderStatusTextOf(%d,%d) = %q, want %q", c.status, c.delivery, got, c.want)
		}
	}
	deliveryCases := map[int32]string{1: "自提", 2: "配送", 3: "到店", 0: "未知"}
	for code, want := range deliveryCases {
		if got := deliveryTypeTextOf(code); got != want {
			t.Errorf("deliveryTypeTextOf(%d) = %q, want %q", code, got, want)
		}
	}
	sourceCases := map[int32]string{1: "C端", 2: "收银台", 0: "C端"}
	for code, want := range sourceCases {
		if got := orderSourceTextOf(code); got != want {
			t.Errorf("orderSourceTextOf(%d) = %q, want %q", code, got, want)
		}
	}
}

// TestShortTaskNo 任务号取前 8 位
func TestShortTaskNo(t *testing.T) {
	long := "0123456789abcdef0123456789abcdef"
	if got := shortTaskNo(long); got != "01234567" {
		t.Errorf("shortTaskNo(32位) = %q, want 前 8 位", got)
	}
	if got := shortTaskNo("abc"); got != "abc" {
		t.Errorf("shortTaskNo(短串) = %q, want 原样", got)
	}
}

// TestExportQueryToListOrdersReq 查询快照还原字段对齐
// 导出复用列表过滤构建（applyOrderListFilters），字段漏映射会导致「所见非所导」
func TestExportQueryToListOrdersReq(t *testing.T) {
	q := exportQuery{
		CommunityID:    3,
		UserID:         99100001,
		Source:         2,
		Status:         2,
		ShiftID:        8,
		ProductID:      66,
		OrderNo:        "ORD-1",
		StartTime:      "2026-10-01",
		EndTime:        "2026-10-24",
		ReceiverName:   "张三",
		ReceiverPhone:  "13800000000",
		PickupCode:     "8866",
		HasPayNo:       true,
		PayOrderNos:    []string{"PAY-1", "PAY-2"},
		IncludeDeleted: true,
	}
	r := q.toListOrdersReq()
	if r.CommunityId != q.CommunityID || r.UserId != q.UserID || r.Source != q.Source ||
		r.Status != q.Status || r.ShiftId != q.ShiftID || r.ProductId != q.ProductID ||
		r.OrderNo != q.OrderNo || r.StartTime != q.StartTime || r.EndTime != q.EndTime ||
		r.ReceiverName != q.ReceiverName || r.ReceiverPhone != q.ReceiverPhone ||
		r.PickupCode != q.PickupCode || !r.HasPayNo || r.IncludeUserDeleted != q.IncludeDeleted {
		t.Fatalf("toListOrdersReq 字段未对齐: %+v", r)
	}
	if len(r.PayOrderNos) != 2 || r.PayOrderNos[0] != "PAY-1" {
		t.Fatalf("PayOrderNos 未对齐: %v", r.PayOrderNos)
	}
}

// TestExportCSVHeader 表头与数据行列数一致（错位会让 Excel 打开串列）
func TestExportCSVHeader(t *testing.T) {
	cols := len(strings.Split(strings.TrimSuffix(exportCSVHeader, "\n"), ","))
	if cols != 12 {
		t.Fatalf("表头应为 12 列, got %d", cols)
	}
}

// TestSanitizeExportMsg 导出错误文案下发前剥离落盘路径（WS 载荷不带文件路径红线）
func TestSanitizeExportMsg(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"空串原样", "", ""},
		{"截断提示等常规文案原样", "已达单次导出上限 5000 行，仅导出前 1200 行，请缩小筛选范围",
			"已达单次导出上限 5000 行，仅导出前 1200 行，请缩小筛选范围"},
		{"不带路径的失败原因原样", "查询条件快照解析失败", "查询条件快照解析失败"},
		{"含冒号但无路径的文案原样", "写入导出文件失败: disk quota exceeded", "写入导出文件失败: disk quota exceeded"},
		{"中文逗号与顿号不应误伤", "订单号、金额，含 / 号的路径不会单独成段", "订单号、金额，含 / 号的路径不会单独成段"},
		{"Linux 绝对路径从路径段截断", `创建导出目录失败: mkdir /app/uploads/exports: permission denied`, "创建导出目录失败: mkdir"},
		{"Windows 盘符路径连同盘符截断", `创建导出目录失败: mkdir C:\app\uploads\exports: Access is denied.`, "创建导出目录失败: mkdir"},
		{"路径在句中时保留前因", "准备落盘目录 /data/uploads/exports 失败", "准备落盘目录"},
		{"相对路径不改写（不带部署根目录，无泄露面）", "打开文件失败: open exports/2026/a.csv: no such file",
			"打开文件失败: open exports/2026/a.csv: no such file"},
	}
	for _, c := range cases {
		if got := sanitizeExportMsg(c.in); got != c.want {
			t.Errorf("%s: got=%q want=%q", c.name, got, c.want)
		}
	}
}

// TestSanitizedMsgPassesEventContract 净化结果必须能通过事件契约的路径红线
// 把「发布侧脱敏」与「契约校验」串起来，任一侧口径变化都会在这里暴露
//
// 参数：
//   - t: 测试句柄
func TestSanitizedMsgPassesEventContract(t *testing.T) {
	raw, err := json.Marshal(eventbus.ExportTaskEvent{
		TaskID:   11,
		State:    eventbus.ExportTaskStateFailed,
		ErrorMsg: sanitizeExportMsg(`创建导出目录失败: mkdir /app/uploads/exports: permission denied`),
	})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("反解为 map 失败: %v", err)
	}
	for _, v := range pathBearingValues(payload) {
		t.Errorf("脱敏后的载荷仍带路径: %s", v)
	}
}

// pathBearingValues 检查载荷字符串取值里是否夹带落盘路径（与 eventbus 契约同口径）
// 参数：
//   - payload: 已反解为 map 的载荷
//
// 返回值：
//   - []string: 违规描述，合规时为空
func pathBearingValues(payload map[string]any) []string {
	var bad []string
	for key, val := range payload {
		s, ok := val.(string)
		if !ok {
			continue
		}
		if strings.Contains(s, "/uploads/") || strings.Contains(s, `\uploads\`) {
			bad = append(bad, key+"="+s)
		}
	}
	return bad
}

// TestExportFailureMsgsAreReadable 失败文案不得带落盘路径
// 落库文本与 WS 下发共用同一条链路，源头不写路径才是根治
func TestExportFailureMsgsAreReadable(t *testing.T) {
	if strings.Contains(exportDirFailureMsg, "/") || strings.Contains(exportDirFailureMsg, "\\") {
		t.Errorf("exportDirFailureMsg 不应含路径分隔符: %q", exportDirFailureMsg)
	}
}
