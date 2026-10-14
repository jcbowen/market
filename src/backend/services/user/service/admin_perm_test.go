package service

import "testing"

// TestIntersectPermissions 社区权限上限交集逻辑
// 覆盖：无上限、上限含通配、有效权限含通配、常规交集、去重保序、空上限兼容
func TestIntersectPermissions(t *testing.T) {
	cases := []struct {
		name      string
		effective []string
		ceiling   []string
		want      []string
	}{
		{
			name:      "空上限视为未配置，不限制",
			effective: []string{"a", "b"},
			ceiling:   nil,
			want:      []string{"a", "b"},
		},
		{
			name:      "上限含通配，不限制",
			effective: []string{"a", "b"},
			ceiling:   []string{"*"},
			want:      []string{"a", "b"},
		},
		{
			name:      "有效权限含通配，收敛为上限全部权限点",
			effective: []string{"*"},
			ceiling:   []string{"a", "b"},
			want:      []string{"a", "b"},
		},
		{
			name:      "常规交集，保序去重",
			effective: []string{"c", "a", "b", "a", "d"},
			ceiling:   []string{"b", "a", "e"},
			want:      []string{"a", "b"},
		},
		{
			name:      "交集为空返回空切片",
			effective: []string{"x"},
			ceiling:   []string{"a", "b"},
			want:      []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := intersectPermissions(tc.effective, tc.ceiling)
			if len(got) != len(tc.want) {
				t.Fatalf("长度不符: got=%v want=%v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("顺序/内容不符: got=%v want=%v", got, tc.want)
				}
			}
		})
	}
}

// TestDefaultCommunityAdminPermissions 默认权限集不含系统级权限点
func TestDefaultCommunityAdminPermissions(t *testing.T) {
	banned := map[string]bool{
		"community:view": true, "community:create": true, "community:update": true, "community:delete": true,
		"region:view": true, "region:update": true,
	}
	seen := make(map[string]bool, len(DefaultCommunityAdminPermissions))
	for _, p := range DefaultCommunityAdminPermissions {
		if banned[p] {
			t.Errorf("默认权限集不应包含系统级权限点 %q", p)
		}
		if seen[p] {
			t.Errorf("默认权限集存在重复项 %q", p)
		}
		seen[p] = true
	}
	if !seen["admin:view"] || !seen["role:view"] || !seen["order:view"] {
		t.Error("默认权限集应覆盖员工/角色/订单等社区运营权限点")
	}
}
