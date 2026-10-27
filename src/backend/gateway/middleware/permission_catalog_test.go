package middleware

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 权限点目录一致性测试（P-3c）
// 扫描 gateway 路由注册（main.go）与 handler 内的权限判断（HasPermission），
// 与 KnownPermissionCodes（镜像前端 constants/permissions.ts）做双向比对：
//   - 实际使用了目录外的权限码 → 新权限点漏登记，前端将无法分配/渲染
//   - 目录内的权限码无任何实际使用 → 残留死权限点，前端可分配但不生效
//
// 修复方式：新增权限点时同步更新本目录与 src/admin/src/constants/permissions.ts

var (
	requirePermRe = regexp.MustCompile(`RequirePermission\(([^)]*)\)`)
	hasPermRe     = regexp.MustCompile(`HasPermission\([^,]+,\s*"([a-z:_]+)"`)
	quotedCodeRe  = regexp.MustCompile(`"([a-z]+:[a-z:_]+)"`)
)

// scanUsedPermissionCodes 提取 main.go 与 handler/*.go 中实际使用的权限码
func scanUsedPermissionCodes(t *testing.T) map[string]struct{} {
	t.Helper()

	files := []string{filepath.Join("..", "main.go")}
	handlerFiles, err := filepath.Glob(filepath.Join("..", "handler", "*.go"))
	if err != nil {
		t.Fatalf("枚举 handler 目录失败: %v", err)
	}
	files = append(files, handlerFiles...)

	used := make(map[string]struct{})
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读取源码失败 %s: %v", f, err)
		}
		src := string(content)
		for _, m := range requirePermRe.FindAllStringSubmatch(src, -1) {
			for _, c := range quotedCodeRe.FindAllStringSubmatch(m[1], -1) {
				used[c[1]] = struct{}{}
			}
		}
		for _, m := range hasPermRe.FindAllStringSubmatch(src, -1) {
			used[m[1]] = struct{}{}
		}
	}
	return used
}

func catalogSet() map[string]struct{} {
	set := make(map[string]struct{}, len(KnownPermissionCodes))
	for _, c := range KnownPermissionCodes {
		set[c] = struct{}{}
	}
	return set
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestPermissionCatalogCoversUsage 实际使用的权限码必须全部登记在目录内
func TestPermissionCatalogCoversUsage(t *testing.T) {
	used := scanUsedPermissionCodes(t)
	catalog := catalogSet()

	var missing []string
	for _, c := range sortedKeys(used) {
		if _, ok := catalog[c]; !ok {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("路由/handler 使用了目录外权限码，请在 KnownPermissionCodes 与 "+
			"src/admin/src/constants/permissions.ts 同步登记: %s", strings.Join(missing, ", "))
	}
}

// TestPermissionCatalogHasNoStaleEntry 目录内的权限码必须至少有一处实际使用
func TestPermissionCatalogHasNoStaleEntry(t *testing.T) {
	used := scanUsedPermissionCodes(t)
	catalog := catalogSet()

	var stale []string
	for _, c := range sortedKeys(catalog) {
		if _, ok := used[c]; !ok {
			stale = append(stale, c)
		}
	}
	if len(stale) > 0 {
		t.Fatalf("目录内权限码无实际使用（残留死权限点），请从 KnownPermissionCodes 与 "+
			"src/admin/src/constants/permissions.ts 一并移除: %s", strings.Join(stale, ", "))
	}
}
