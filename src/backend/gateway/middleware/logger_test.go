package middleware

import "testing"

// TestMaskSensitiveQuery 验证访问日志的凭据脱敏：
// 令牌类参数只留名字，业务参数（分页、社区、关键字）必须原样保留，
// 否则排障时日志失去可用性
func TestMaskSensitiveQuery(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "空串原样", raw: "", want: ""},
		{name: "WS 握手令牌", raw: "access_token=eyJhbGciOi.abc", want: "access_token=***"},
		{name: "大小写不敏感", raw: "Access_Token=abc", want: "Access_Token=***"},
		{name: "业务参数保留", raw: "page=2&page_size=20&community_id=1", want: "page=2&page_size=20&community_id=1"},
		{name: "混合参数", raw: "page=1&access_token=abc&refresh_token=def&keyword=%E9%9C%87", want: "page=1&access_token=***&refresh_token=***&keyword=%E9%9C%87"},
		{name: "空值不改写", raw: "code=&token=", want: "code=&token="},
		{name: "无值参数不改写", raw: "signature", want: "signature"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := maskSensitiveQuery(tc.raw); got != tc.want {
				t.Fatalf("脱敏结果不符：got %q want %q", got, tc.want)
			}
		})
	}
}
