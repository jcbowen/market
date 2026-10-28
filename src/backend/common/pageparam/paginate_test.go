package pageparam

import "testing"

// TestClamp 三档封顶与非法入参退化
//
// 使用示例: go test ./common/pageparam -run TestClamp
func TestClamp(t *testing.T) {
	cases := []struct {
		name     string
		fn       func(page, pageSize int) (int, int)
		page     int
		pageSize int
		wantPage int
		wantSize int
	}{
		{"常规-正常值原样", Clamp, 2, 20, 2, 20},
		{"常规-页码 0 归首页", Clamp, 0, 20, 1, 20},
		{"常规-页码负数归首页", Clamp, -3, 20, 1, 20},
		{"常规-页长 0 用默认", Clamp, 1, 0, 1, DefaultSize},
		{"常规-页长负数用默认", Clamp, 1, -1, 1, DefaultSize},
		{"常规-超上限收敛", Clamp, 1, 100000, 1, MaxSize},
		{"批量-允许更大的内部页长", ClampBatch, 1, 500, 1, 500},
		{"批量-仍受批量上限约束", ClampBatch, 1, BatchMaxSize + 1, 1, BatchMaxSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, size := tc.fn(tc.page, tc.pageSize)
			if page != tc.wantPage || size != tc.wantSize {
				t.Fatalf("期望 %d/%d，实际 %d/%d", tc.wantPage, tc.wantSize, page, size)
			}
		})
	}
}

// TestClampMax 调用方自定义上限：严格出口（履约 50、报表 100）不因统一入口而放宽
func TestClampMax(t *testing.T) {
	if _, size := ClampMax(1, 99, 50); size != 50 {
		t.Fatalf("上限 50 应把 99 收敛为 50，实际 %d", size)
	}
	// 上限倒挂（maxSize<1）不应返回空页，退化为默认页长
	if _, size := ClampMax(1, 30, 0); size != DefaultSize {
		t.Fatalf("非法上限应退化为默认页长，实际 %d", size)
	}
}

// TestClampWith 保留各服务历史默认页长，同时仍受封顶约束
func TestClampWith(t *testing.T) {
	if _, size := ClampWith(1, 0, 20, 200); size != 20 {
		t.Fatalf("默认页长应取 20，实际 %d", size)
	}
	if _, size := ClampWith(1, 5000, 20, 200); size != 200 {
		t.Fatalf("超过上限应收敛为 200，实际 %d", size)
	}
	if _, size := ClampWith(1, 0, 0, 200); size != DefaultSize {
		t.Fatalf("非法默认值应退化为 DefaultSize，实际 %d", size)
	}
}
