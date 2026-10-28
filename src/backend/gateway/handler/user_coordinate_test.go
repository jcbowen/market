package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestParseCoordinates 社区列表经纬度入参判定
// 覆盖：正常坐标透传、缺省与非数值按未定位处理、NaN/Inf（ParseFloat 不报错的合法字面量）
// 与越界坐标一律归零——三者都会让距离排序拿到无意义值，其中 NaN/Inf 直接让 SQL 报错
func TestParseCoordinates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name     string
		query    string
		wantLong float64
		wantLat  float64
	}{
		{"正常坐标", "longitude=120.15&latitude=30.25", 120.15, 30.25},
		{"负值（西经南纬）", "longitude=-73.98&latitude=40.71", -73.98, 40.71},
		{"未提供", "", 0, 0},
		{"非数值", "longitude=abc&latitude=30.25", 0, 0},
		{"NaN", "longitude=NaN&latitude=NaN", 0, 0},
		{"Inf", "longitude=+Inf&latitude=30.25", 0, 0},
		{"经度越界", "longitude=181&latitude=30.25", 0, 0},
		{"纬度越界", "longitude=120.15&latitude=91", 0, 0},
		{"纬度非法", "longitude=120.15&latitude=abc", 0, 0},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/communities?"+tc.query, nil)
		longitude, latitude := parseCoordinates(c)
		if longitude != tc.wantLong || latitude != tc.wantLat {
			t.Errorf("%s: 期望 (%v, %v)，实际 (%v, %v)", tc.name, tc.wantLong, tc.wantLat, longitude, latitude)
		}
	}
}
