package attachmenturl

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"google.golang.org/grpc/metadata"
	"gorm.io/gorm"
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
 *   - SourcePath（委托 jcbaseGo ToSource）：任意本站域名 /uploads/ URL 剥离（与域名无关）；
 *     OSS 候选域名剥离；外链原样（含查询参数）
 *   - RestoreURL 出库还原口径：用注入缓存的 Loader 离线覆盖，不触库
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

// TestRelPathIn 验证自定义 local_dir 下的前缀归一：配置目录优先、默认 uploads 兜底
func TestRelPathIn(t *testing.T) {
	cases := []struct {
		name        string
		dir         string
		in          string
		rel         string
		passthrough bool
	}{
		{"配置目录前缀剥离", "media", "media/images/a.jpg", "images/a.jpg", false},
		{"配置目录站内路径剥离", "media", "/media/seed/products/p.png", "seed/products/p.png", false},
		{"默认uploads旧前缀仍剥离", "media", "/uploads/images/a.jpg", "images/a.jpg", false},
		{"默认uploads相对前缀仍剥离", "media", "uploads/images/a.jpg", "images/a.jpg", false},
		{"空目录回默认", "", "uploads/images/a.jpg", "images/a.jpg", false},
		{"外链透传", "media", "https://other.example.com/x.jpg", "https://other.example.com/x.jpg", true},
		{"未知绝对路径透传", "media", "/static/logo.png", "/static/logo.png", true},
	}
	for _, c := range cases {
		rel, passthrough := RelPathIn(c.dir, c.in)
		if rel != c.rel || passthrough != c.passthrough {
			t.Errorf("%s: got (%q, %v) want (%q, %v)", c.name, rel, passthrough, c.rel, c.passthrough)
		}
	}
}

// TestFallbackURLIn 验证自定义 local_dir 下的兜底路径段与配置目录一致
func TestFallbackURLIn(t *testing.T) {
	if got := FallbackURLIn("media", "images/a.jpg"); got != "/media/images/a.jpg" {
		t.Errorf("相对路径应补 /media/ 前缀: %q", got)
	}
	if got := FallbackURLIn("media", "/media/images/a.jpg"); got != "/media/images/a.jpg" {
		t.Errorf("/media/ 前缀应归一为 /media/: %q", got)
	}
	if got := FallbackURLIn("media", "/uploads/images/a.jpg"); got != "/media/images/a.jpg" {
		t.Errorf("旧 /uploads/ 前缀应按新目录兜底重拼: %q", got)
	}
	if got := FallbackURLIn("media", "https://other.example.com/x.jpg"); got != "https://other.example.com/x.jpg" {
		t.Errorf("外链应原样返回: %q", got)
	}
}

// TestFallbackURLInCtx 验证带请求上下文的兜底口径：
// metadata 有网关透传的请求基址 → 拼完整 URL；无基址回落站内绝对路径；外链恒原样
func TestFallbackURLInCtx(t *testing.T) {
	base := "http://192.168.1.5:8080"
	incoming := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-request-base", base))

	if got := FallbackURLInCtx(incoming, "uploads", "images/a.jpg"); got != base+"/uploads/images/a.jpg" {
		t.Errorf("有请求基址应拼完整 URL: %q", got)
	}
	if got := FallbackURLInCtx(incoming, "", "seed/products/latiao.png"); got != base+"/uploads/seed/products/latiao.png" {
		t.Errorf("默认目录应回 uploads: %q", got)
	}
	if got := FallbackURLInCtx(incoming, "", "/uploads/seed/products/water.png"); got != base+"/uploads/seed/products/water.png" {
		t.Errorf("站内路径应归一重拼: %q", got)
	}
	if got := FallbackURLInCtx(incoming, "", "https://other.example.com/x.jpg"); got != "https://other.example.com/x.jpg" {
		t.Errorf("外链应原样返回: %q", got)
	}

	// 无基址（后台任务/直调/单测）回落站内绝对路径，与 FallbackURLIn 等价
	if got := FallbackURLInCtx(context.Background(), "", "images/a.jpg"); got != "/uploads/images/a.jpg" {
		t.Errorf("无基址应回落站内绝对路径: %q", got)
	}
	// 纯函数版走 context.TODO（无基址），与 context.Background 回落结果一致
	if got := FallbackURLIn("", "images/a.jpg"); got != "/uploads/images/a.jpg" {
		t.Errorf("纯函数版应回落站内绝对路径: %q", got)
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
	mediaCfg := &Config{StorageType: "local", LocalDir: "media", VisitDomain: "https://m.example.com"}

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
		{"自定义目录前缀剥离", mediaCfg, "/media/images/a.jpg", "images/a.jpg"},
		{"自定义目录URL剥离", mediaCfg, "https://m.example.com/media/images/a.jpg", "images/a.jpg"},
		{"自定义目录非命中域名URL按目录剥离", mediaCfg, "http://localhost:18080/media/images/a.jpg", "images/a.jpg"},
		// 委托 jcbaseGo ToSource 后不再兼容 local_dir 变更前的旧默认前缀存量（环境重装，存量不处理）
		{"自定义目录下旧uploads前缀不再剥离", mediaCfg, "/uploads/images/a.jpg", "uploads/images/a.jpg"},
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

	// 自定义 local_dir：URL 路径段跟随配置目录
	lm := &Loader{ttl: DefaultCacheTTL}
	lm.cached = &Config{StorageType: "local", LocalDir: "media", VisitDomain: "https://new.example.com"}
	lm.cachedAt = time.Now()
	if got := lm.RestoreURL(context.Background(), "images/a.jpg"); got != "https://new.example.com/media/images/a.jpg" {
		t.Errorf("自定义目录拼装不符: %q", got)
	}
}

// TestLoadConfigByPrimaryKey 用 sqlite 真库锁定 LoadConfig 按主键 id 查询的行为。
// 回归背景：configRow 曾缺少 ID 主键字段，GORM 主键推断把首字段 storage_type 当主键，
// First(&row, 1) 被编译成 WHERE storage_type = 1，
// 而 storage_type 取值恒为 'local'/'oss'，该比较永不相等，
// 各服务读配置全部 record not found，出库 URL 降级为站内相对路径（cart 商品图事故根因）
func TestLoadConfigByPrimaryKey(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开 sqlite 失败: %v", err)
	}
	if err := db.Exec(`CREATE TABLE attachment_config (
		id INTEGER PRIMARY KEY,
		storage_type TEXT,
		local_dir TEXT,
		visit_domain TEXT,
		oss_custom_visit_domain TEXT,
		oss_bucket_name TEXT,
		oss_endpoint TEXT
	)`).Error; err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := db.Exec(`INSERT INTO attachment_config (id, storage_type, local_dir, visit_domain)
		VALUES (1, 'local', 'uploads', 'http://localhost:18080/')`).Error; err != nil {
		t.Fatalf("插入配置失败: %v", err)
	}

	cfg, err := LoadConfig(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadConfig 应命中 id=1 行: %v", err)
	}
	if cfg.StorageType != "local" || cfg.LocalDir != "uploads" || cfg.VisitDomain != "http://localhost:18080/" {
		t.Errorf("配置读取不符: %+v", cfg)
	}
}
