package service

import (
	"strconv"
	"strings"
	"testing"
)

// TestNearbyOrderSQLRejectsNonFiniteCoords 附近社区排序片段对非有限坐标退化处理
// 背景：strconv.ParseFloat 认 "NaN"/"Inf" 为合法浮点字面量且不报错，公开列表接口
// ?longitude=NaN 可直达本函数；拼进 SQL 后 MySQL 把 NaN 当列名、把 +Inf 当语法错误，
// 整个社区列表 500。现在退化为按 id 升序，不报错也不拼坐标。
func TestNearbyOrderSQLRejectsNonFiniteCoords(t *testing.T) {
	for _, tc := range []struct {
		name         string
		longitude    string
		latitude     string
		wantDistance bool
	}{
		{"正常坐标", "120.15", "30.25", true},
		{"零值（未定位）", "0", "0", true},
		{"经度 NaN", "NaN", "30.25", false},
		{"纬度 NaN", "120.15", "NaN", false},
		{"经度 +Inf", "+Inf", "30.25", false},
		{"纬度 -Inf", "120.15", "-Inf", false},
	} {
		longitude, _ := strconv.ParseFloat(tc.longitude, 64)
		latitude, _ := strconv.ParseFloat(tc.latitude, 64)
		got := nearbyOrderSQL(longitude, latitude)
		switch {
		case tc.wantDistance && !strings.Contains(got, "POWER(longitude -"):
			t.Errorf("%s: 应保留距离排序，实际片段 %q", tc.name, got)
		case !tc.wantDistance && got != "id asc":
			t.Errorf("%s: 非有限坐标应退化为 id asc，实际片段 %q", tc.name, got)
		}
	}
}
