package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jcbowen/market/src/backend/common/attachmenturl"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

/**
 * 附件 URL 拼接与配置缓存的离线单元测试（不连库）
 *
 * 运行方式：go test ./services/product/service/ -run TestAttachment -v
 *
 * 覆盖（对应 E4「附件配置真内存缓存」后的行为契约）：
 *   - configCacheFresh 有效期内命中缓存、过期后失效（列表逐行取 URL 不再每行查库的前提）
 *   - ToMediaURL local 模式拼 <访问域名>/uploads/<相对路径>（含 /uploads/seed 演示素材归一重拼）
 *   - ToMediaURL oss 模式优先自定义 CDN 域名，缺省回落 bucket.endpoint 域名
 *   - ToMediaURL 对空值、外链完整 URL、未知 / 开头绝对路径原样返回
 *   - local 未配置访问域名（补写未完成的异常窗口）时兜底拼 /uploads/<相对路径>，不原样返回裸相对路径
 *   - 存量 uploads/ 目录前缀形态统一归一：配置域名时单次拼接、兜底时归一为 /uploads/
 *   - ToMediaURLs 整批共用一次配置读取，降级时逐项兜底
 *
 * 用例会预置一份未过期的进程内缓存，因此不会触达 s.db（保持 nil 即证明走的是缓存）。
 */

// newCachedAttachmentService 构造只带进程内缓存、不带数据库的附件服务
// cachedAt 置为当前时间使缓存处于有效期内：getConfigCached 直接命中，不会执行任何查询
//
// 参数：
//   - cfg: 附件配置行
//
// 返回值：
//   - *AttachmentService: 可直接调用 URL 转换方法的附件服务
func newCachedAttachmentService(cfg *model.AttachmentConfig) *AttachmentService {
	return &AttachmentService{cachedCfg: cfg, cachedAt: time.Now()}
}

// TestAttachmentConfigCacheFresh 验证缓存有效期判定：有效期内命中、过期后需回源
//
// 参数：
//   - t: 测试对象
func TestAttachmentConfigCacheFresh(t *testing.T) {
	svc := &AttachmentService{}
	now := time.Now()

	svc.mu.RLock()
	if svc.configCacheFresh(now) {
		svc.mu.RUnlock()
		t.Fatal("缓存为空时应判定为未命中")
	}
	svc.mu.RUnlock()

	svc.cachedCfg = &model.AttachmentConfig{StorageType: "local"}
	svc.cachedAt = now
	if !svc.isConfigFreshAt(now) {
		t.Error("缓存刚写入时应命中")
	}
	if !svc.isConfigFreshAt(now.Add(attachmentConfigCacheTTL - time.Second)) {
		t.Error("有效期边界内 1 秒仍应命中")
	}
	if svc.isConfigFreshAt(now.Add(attachmentConfigCacheTTL)) {
		t.Error("达到有效期应立即失效，避免多实例配置长期滞后")
	}
	if svc.isConfigFreshAt(now.Add(attachmentConfigCacheTTL + time.Minute)) {
		t.Error("超过有效期必须回源")
	}
}

// isConfigFreshAt 加读锁判定缓存在指定时刻是否有效，供测试与生产复用同一规则
//
// 参数：
//   - now: 判定基准时间
//
// 返回值：
//   - bool: 缓存非空且未过期时为 true
func (s *AttachmentService) isConfigFreshAt(now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configCacheFresh(now)
}

// TestAttachmentToMediaURL 验证附件配置缓存下的图片 URL 拼接口径
//
// 参数：
//   - t: 测试对象
func TestAttachmentToMediaURL(t *testing.T) {
	ctx := context.Background()

	t.Run("local模式拼接uploads前缀", func(t *testing.T) {
		svc := newCachedAttachmentService(&model.AttachmentConfig{
			StorageType: "local",
			VisitDomain: "https://m.example.com",
		})
		if got := svc.ToMediaURL(ctx, "images/2026-10-11/a.jpg"); got != "https://m.example.com/uploads/images/2026-10-11/a.jpg" {
			t.Errorf("local 拼接不符: %q", got)
		}
		// 存量 uploads/ 目录前缀归一为相对路径后再拼接，避免双 uploads/ 前缀（线上 /uploads/uploads/ 事故口径）
		if got := svc.ToMediaURL(ctx, "uploads/images/a.jpg"); got != "https://m.example.com/uploads/images/a.jpg" {
			t.Errorf("uploads/ 目录前缀应归一后单次拼接: %q", got)
		}
		// /uploads/ 前缀（含 /uploads/seed 演示素材）归一为相对路径后按配置拼接为完整地址
		if got := svc.ToMediaURL(ctx, "/uploads/seed/products/pickle.png"); got != "https://m.example.com/uploads/seed/products/pickle.png" {
			t.Errorf("/uploads/ 前缀应归一后拼接完整地址: %q", got)
		}
		if got := svc.ToMediaURL(ctx, ""); got != "" {
			t.Errorf("空路径应返回空串: %q", got)
		}
		if got := svc.ToMediaURL(ctx, "https://other.example.com/x.jpg"); got != "https://other.example.com/x.jpg" {
			t.Errorf("存量完整 URL 应原样返回: %q", got)
		}
	})

	t.Run("local未配置访问域名兜底站内绝对路径", func(t *testing.T) {
		svc := newCachedAttachmentService(&model.AttachmentConfig{StorageType: "local"})
		if got := svc.ToMediaURL(ctx, "images/a.jpg"); got != "/uploads/images/a.jpg" {
			t.Errorf("缺访问域名时应兜底 /uploads/ 前缀: %q", got)
		}
		if got := svc.ToMediaURL(ctx, "uploads/images/a.jpg"); got != "/uploads/images/a.jpg" {
			t.Errorf("uploads/ 目录前缀应归一为 /uploads/: %q", got)
		}
		// 存量完整 URL 与站内绝对路径（含 seed 素材）原样返回，不重复加前缀
		if got := svc.ToMediaURL(ctx, "https://other.example.com/x.jpg"); got != "https://other.example.com/x.jpg" {
			t.Errorf("存量完整 URL 应原样返回: %q", got)
		}
		if got := svc.ToMediaURL(ctx, "/uploads/seed/products/a.png"); got != "/uploads/seed/products/a.png" {
			t.Errorf("/ 开头站内绝对路径应原样返回: %q", got)
		}
	})

	t.Run("降级纯函数口径", func(t *testing.T) {
		// FallbackURL 为纯函数（common/attachmenturl），配置缺失/补写未完成等任何走不到转换器的场景共用此口径
		if got := attachmenturl.FallbackURL("images/a.jpg"); got != "/uploads/images/a.jpg" {
			t.Errorf("相对路径应补 /uploads/ 前缀: %q", got)
		}
		if got := attachmenturl.FallbackURL("uploads/images/a.jpg"); got != "/uploads/images/a.jpg" {
			t.Errorf("uploads/ 目录前缀应归一为 /uploads/: %q", got)
		}
		if got := attachmenturl.FallbackURL("https://other.example.com/x.jpg"); got != "https://other.example.com/x.jpg" {
			t.Errorf("完整 URL 应原样返回: %q", got)
		}
		if got := attachmenturl.FallbackURL("//cdn.example.com/x.jpg"); got != "//cdn.example.com/x.jpg" {
			t.Errorf("协议相对 URL 应原样返回: %q", got)
		}
		if got := attachmenturl.FallbackURL("/uploads/seed/products/a.png"); got != "/uploads/seed/products/a.png" {
			t.Errorf("/uploads/ 前缀应归一为 /uploads/: %q", got)
		}
		if got := attachmenturl.FallbackURL(""); got != "" {
			t.Errorf("空值应返回空串: %q", got)
		}
	})

	t.Run("oss模式优先自定义CDN域名", func(t *testing.T) {
		svc := newCachedAttachmentService(&model.AttachmentConfig{
			StorageType:          "oss",
			OssCustomVisitDomain: "https://cdn.example.com/",
			OssBucketName:        "bucket",
			OssEndpoint:          "oss-cn-hangzhou.aliyuncs.com",
		})
		if got := svc.ToMediaURL(ctx, "images/a.jpg"); got != "https://cdn.example.com/images/a.jpg" {
			t.Errorf("自定义域名拼接不符: %q", got)
		}
	})

	t.Run("oss模式缺省回落bucket域名", func(t *testing.T) {
		svc := newCachedAttachmentService(&model.AttachmentConfig{
			StorageType:   "oss",
			OssBucketName: "bucket",
			OssEndpoint:   "oss-cn-hangzhou.aliyuncs.com",
		})
		got := svc.ToMediaURL(ctx, "images/a.jpg")
		if !strings.HasPrefix(got, "https://bucket.oss-cn-hangzhou.aliyuncs.com/") {
			t.Errorf("bucket 域名拼接不符: %q", got)
		}
		if !strings.HasSuffix(got, "images/a.jpg") {
			t.Errorf("对象 Key 不应被改写: %q", got)
		}
	})
}

// TestAttachmentToMediaURLs 验证图集批量转换共用一次配置读取
//
// 参数：
//   - t: 测试对象
func TestAttachmentToMediaURLs(t *testing.T) {
	ctx := context.Background()
	svc := newCachedAttachmentService(&model.AttachmentConfig{
		StorageType: "local",
		VisitDomain: "https://m.example.com/",
	})
	got := svc.ToMediaURLs(ctx, []string{"images/a.jpg", "", "https://other.example.com/b.jpg"})
	want := []string{"https://m.example.com/uploads/images/a.jpg", "", "https://other.example.com/b.jpg"}
	if len(got) != len(want) {
		t.Fatalf("批量结果长度不符: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 项拼接不符: got %q want %q", i, got[i], want[i])
		}
	}

	if out := svc.ToMediaURLs(ctx, nil); out != nil {
		t.Errorf("空列表应原样返回: %v", out)
	}

	// 降级批量：逐项走 fallbackMediaURL，完整 URL 与站内绝对路径原样保留
	fallbackSvc := newCachedAttachmentService(&model.AttachmentConfig{StorageType: "local"})
	fb := fallbackSvc.ToMediaURLs(ctx, []string{"images/a.jpg", "", "https://other.example.com/b.jpg", "/uploads/seed/c.png"})
	wantFB := []string{"/uploads/images/a.jpg", "", "https://other.example.com/b.jpg", "/uploads/seed/c.png"}
	for i := range wantFB {
		if fb[i] != wantFB[i] {
			t.Errorf("批量降级第 %d 项不符: got %q want %q", i, fb[i], wantFB[i])
		}
	}
}
