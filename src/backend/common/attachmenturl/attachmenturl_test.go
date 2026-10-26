package attachmenturl

import (
	"context"
	"testing"
	"time"
)

/**
 * 附件地址统一拼装的离线单元测试（不连库，纯函数与转换器口径）
 *
 * 运行方式：go test ./common/attachmenturl/ -v
 *
 * 覆盖：
 *   - RelPath：外链/未知绝对路径透传；/uploads/ 与 uploads/ 前缀归一；相对路径不变
 *   - FallbackURL：相对路径兜底 /uploads/ 前缀，外链原样
 *   - NewConverter + ToMedia：local 拼域名+uploads 前缀；oss 优先自定义域名回落 bucket 域名；
 *     配置缺失/local 未配域名返回 nil（补写未完成窗口由调用方走 FallbackURL）
 *   - SourcePath：任意本站域名 /uploads/ URL 剥离（与域名无关）；OSS 候选域名剥离；外链原样
 *   - RestoreURL 出库还原口径需 DB（Loader），不在此覆盖，由接入方 e2e 验证
 */

func TestRelPath(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		rel         string
		passthrough bool
	}{
		{"空值", "", "", true},
		{"外链完整URL", "https://other.example.com/x.jpg", "https://other.example.com/x.jpg", true},
		{"协议相对URL", "//cdn.example.com/x.jpg", "//cdn.example.com/x.jpg", true},
		{"相对路径", "images/a.jpg", "images/a.jpg", false},
		{"uploads前缀归一", "uploads/images/a.jpg", "images/a.jpg", false},
		{"站内路径归一", "/uploads/seed/products/pickle.png", "seed/products/pickle.png", false},
		{"未知绝对路径透传", "/static/logo.png", "/static/logo.png", true},
	}
	for _, c := range cases {
		rel, passthrough := RelPath(c.in)
		if rel != c.rel || passthrough != c.passthrough {
			t.Errorf("%s: got (%q, %v) want (%q, %v)", c.name, rel, passthrough, c.rel, c.passthrough)
		}
	}
}

func TestFallbackURL(t *testing.T) {
	if got := FallbackURL("images/a.jpg"); got != "/uploads/images/a.jpg" {
		t.Errorf("相对路径应补 /uploads/ 前缀: %q", got)
	}
	if got := FallbackURL("uploads/images/a.jpg"); got != "/uploads/images/a.jpg" {
		t.Errorf("uploads/ 前缀应归一: %q", got)
	}
	if got := FallbackURL("https://other.example.com/x.jpg"); got != "https://other.example.com/x.jpg" {
		t.Errorf("外链应原样返回: %q", got)
	}
	if got := FallbackURL("/uploads/seed/c.png"); got != "/uploads/seed/c.png" {
		t.Errorf("/uploads/ 前缀应归一为 /uploads/: %q", got)
	}
	if got := FallbackURL(""); got != "" {
		t.Errorf("空值应返回空串: %q", got)
	}
}

func TestNewConverter(t *testing.T) {
	if conv := NewConverter(nil); conv != nil {
		t.Error("nil 配置应返回 nil 转换器")
	}
	if conv := NewConverter(&Config{StorageType: "local"}); conv != nil {
		t.Error("local 未配访问域名应返回 nil（补写未完成窗口）")
	}

	local := NewConverter(&Config{StorageType: "local", VisitDomain: "https://m.example.com"})
	if got := local.ToMedia("images/2026-10-11/a.jpg"); got != "https://m.example.com/uploads/images/2026-10-11/a.jpg" {
		t.Errorf("local 拼接不符: %q", got)
	}
	if got := local.ToMedia("seed/products/pickle.png"); got != "https://m.example.com/uploads/seed/products/pickle.png" {
		t.Errorf("seed 素材拼接不符: %q", got)
	}

	cdn := NewConverter(&Config{StorageType: "oss", OssCustomVisitDomain: "https://cdn.example.com/", OssBucketName: "bucket", OssEndpoint: "oss-cn-hangzhou.aliyuncs.com"})
	if got := cdn.ToMedia("images/a.jpg"); got != "https://cdn.example.com/images/a.jpg" {
		t.Errorf("OSS 自定义域名拼接不符: %q", got)
	}
	bucket := NewConverter(&Config{StorageType: "oss", OssBucketName: "bucket", OssEndpoint: "oss-cn-hangzhou.aliyuncs.com"})
	if got := bucket.ToMedia("images/a.jpg"); got != "https://bucket.oss-cn-hangzhou.aliyuncs.com/images/a.jpg" {
		t.Errorf("OSS bucket 域名拼接不符: %q", got)
	}
}

// TestLocalDirName 验证 local_dir 目录名归一与默认值回退
func TestLocalDirName(t *testing.T) {
	if got := LocalDirName(""); got != DefaultLocalDir {
		t.Errorf("空值应回默认 %q: %q", DefaultLocalDir, got)
	}
	if got := LocalDirName("media"); got != "media" {
		t.Errorf("自定义目录名应原样: %q", got)
	}
	if got := LocalDirName("/media/"); got != "media" {
		t.Errorf("首尾斜杠应剥离: %q", got)
	}

	// local_dir 驱动 URL 路径段：非默认值时 ToMedia 拼 /<local_dir>/ 前缀
	local := NewConverter(&Config{StorageType: "local", LocalDir: "media", VisitDomain: "https://m.example.com"})
	if got := local.ToMedia("images/a.jpg"); got != "https://m.example.com/media/images/a.jpg" {
		t.Errorf("自定义 local_dir 拼接不符: %q", got)
	}
	// local_dir 空值回默认 uploads
	local2 := NewConverter(&Config{StorageType: "local", VisitDomain: "https://m.example.com"})
	if got := local2.ToMedia("images/a.jpg"); got != "https://m.example.com/uploads/images/a.jpg" {
		t.Errorf("local_dir 空值应回默认 uploads: %q", got)
	}
}

func TestSourcePath(t *testing.T) {
	cfg := &Config{StorageType: "local", VisitDomain: "https://m.example.com"}
	cdnCfg := &Config{StorageType: "oss", OssCustomVisitDomain: "https://cdn.example.com/"}

	cases := []struct {
		name string
		cfg  *Config
		in   string
		want string
	}{
		{"空值", cfg, "", ""},
		{"本站任意域名URL剥离", cfg, "http://localhost:18080/uploads/images/2026/10/11/a.jpg", "images/2026/10/11/a.jpg"},
		{"当前配置域名URL剥离", cfg, "https://m.example.com/uploads/images/a.jpg", "images/a.jpg"},
		{"站内路径剥离", cfg, "/uploads/images/a.jpg", "images/a.jpg"},
		{"uploads前缀剥离", cfg, "uploads/images/a.jpg", "images/a.jpg"},
		{"已是相对路径", cfg, "images/a.jpg", "images/a.jpg"},
		{"外链原样保留", cfg, "https://placehold.co/300x300?text=Tomato", "https://placehold.co/300x300?text=Tomato"},
		{"OSS自定义域名剥离为Key", cdnCfg, "https://cdn.example.com/images/a.jpg", "images/a.jpg"},
		{"OSS未命中候选域名原样", cdnCfg, "https://other-bucket.oss-cn-beijing.aliyuncs.com/images/a.jpg", "https://other-bucket.oss-cn-beijing.aliyuncs.com/images/a.jpg"},
	}
	for _, c := range cases {
		if got := SourcePath(c.cfg, c.in); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// TestRestoreURLSemantics 用注入缓存的 Loader 验证出库还原口径（不触库：缓存直接命中）
func TestRestoreURLSemantics(t *testing.T) {
	l := &Loader{ttl: DefaultCacheTTL}
	l.cached = &Config{StorageType: "local", VisitDomain: "https://new.example.com"}
	l.cachedAt = time.Now()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"相对路径直接拼", "images/a.jpg", "https://new.example.com/uploads/images/a.jpg"},
		{"存量旧域名URL归一重拼", "https://old.example.com/uploads/images/a.jpg", "https://new.example.com/uploads/images/a.jpg"},
		{"站内路径重拼", "/uploads/images/a.jpg", "https://new.example.com/uploads/images/a.jpg"},
		{"外链原样", "https://placehold.co/x.png", "https://placehold.co/x.png"},
		{"空值", "", ""},
	}
	for _, c := range cases {
		if got := l.RestoreURL(context.Background(), c.in); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
