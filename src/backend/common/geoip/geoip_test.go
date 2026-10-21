package geoip

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testDBPath 测试用数据库路径：默认 bin/data/geoip/GeoLite2-City.mmdb（相对仓库根）
// 文件不存在时相关用例自动跳过（数据库由 cmd/geoip-download 下载，不入 git）
func testDBPath(t *testing.T) string {
	t.Helper()
	const rel = "../../../../bin/data/geoip/GeoLite2-City.mmdb"
	abs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("解析路径失败: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("数据库文件不存在（执行 go run ./cmd/geoip-download 下载）: %s", abs)
	}
	return abs
}

// TestLookupInvalidInput 内网/回环/无效 IP 应直接返回 nil，不触发数据库加载
func TestLookupInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		ip   string
	}{
		{"回环地址", "127.0.0.1"},
		{"内网地址", "192.168.1.100"},
		{"IPv6 回环", "::1"},
		{"链路本地", "169.254.1.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr := netip.MustParseAddr(c.ip)
			if loc := Lookup("any.mmdb", addr); loc != nil {
				t.Fatalf("期望 nil，实际返回 %+v", loc)
			}
		})
	}

	// 零值 Addr 与空路径
	if loc := Lookup("", netip.MustParseAddr("8.8.8.8")); loc != nil {
		t.Fatalf("空路径应返回 nil")
	}
	var zero netip.Addr
	if loc := Lookup("any.mmdb", zero); loc != nil {
		t.Fatalf("无效 Addr 应返回 nil")
	}
}

// TestLookupMissingDB 数据库文件不存在时应返回 nil 且不 panic
func TestLookupMissingDB(t *testing.T) {
	addr := netip.MustParseAddr("8.8.8.8")
	if loc := Lookup(filepath.Join(t.TempDir(), "not-exist.mmdb"), addr); loc != nil {
		t.Fatalf("数据库缺失应返回 nil，实际返回 %+v", loc)
	}
}

// TestLookupRealDB 真实数据库查询：公网 IP 应返回合法坐标
func TestLookupRealDB(t *testing.T) {
	dbPath := testDBPath(t)

	// 重置单例，隔离其他用例的状态
	mgr.mu.Lock()
	if mgr.reader != nil {
		_ = mgr.reader.Close()
		mgr.reader = nil
	}
	mgr.mu.Unlock()

	// 8.8.8.8 为 Google DNS（美国），GeoLite2-City 必有记录
	loc := Lookup(dbPath, netip.MustParseAddr("8.8.8.8"))
	if loc == nil {
		t.Fatal("查询 8.8.8.8 应命中")
	}
	if loc.Country != "US" {
		t.Errorf("期望国家码 US，实际 %s", loc.Country)
	}
	if loc.Longitude == 0 && loc.Latitude == 0 {
		t.Errorf("坐标不应全为零: %+v", loc)
	}
	t.Logf("8.8.8.8 => %+v", loc)
}

// TestHotReload 数据库文件被替换后（mtime/size 变化）应重新加载
func TestHotReload(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.mmdb")

	// 写入非法内容：首次加载失败返回 nil，但不应 panic
	if err := os.WriteFile(dbPath, []byte("not-a-mmdb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if loc := Lookup(dbPath, netip.MustParseAddr("8.8.8.8")); loc != nil {
		t.Fatalf("非法数据库应返回 nil")
	}

	// 更新 mtime（即使内容仍非法，也不应 panic；mtime 检查间隔为 1 分钟，强制过期）
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(dbPath, future, future); err != nil {
		t.Fatal(err)
	}
	mgr.mu.Lock()
	mgr.lastCheck = time.Time{}
	mgr.mu.Unlock()
	if loc := Lookup(dbPath, netip.MustParseAddr("8.8.8.8")); loc != nil {
		t.Fatalf("非法数据库（mtime 更新后）应返回 nil")
	}
}
