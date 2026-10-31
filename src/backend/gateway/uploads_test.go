package main

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/attachmenturl"
)

/**
 * 网关 /<local_dir> 静态路由分流测试（离线，不连数据库）
 *
 * 运行方式：
 *   go test ./gateway/ -run TestRegisterUploads -v
 *
 * 覆盖：
 *   - /uploads/seed/** 取自镜像内置素材目录，/uploads/** 取自运行期附件目录
 *   - 两个根目录互不串用（种子目录取不到附件，反之亦然）
 *   - exports 私有段直链与穿越、大小写变体均 404（导出仅走鉴权下载接口）
 *   - 含 .. 的请求被拒绝，无法逃出所属根目录
 *   - 归一后仍指向合法附件的路径正常 200（拦截不误伤正常访问）
 *   - 素材缺失时返回 404 而非 500
 *   - 目录路径（含内置 index.html 的目录）一律 404，不渲染文件清单
 */

// newUploadsTestRouter 用两个临时目录构造只注册了 /uploads 的 gin 引擎
// localDir 取默认值 uploads（与常规部署一致）
// 参数：
//   - t: 测试上下文
//
// 返回值：
//   - *gin.Engine: 已完成 registerUploads 注册的路由
//   - string: 种子素材根目录
//   - string: 附件根目录
//
// 异常：目录或夹具文件创建失败时直接 t.Fatal
func newUploadsTestRouter(t *testing.T) (*gin.Engine, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	// 两个根目录同父级，敏感夹具放在父级：目录穿越若成功会读到它
	base := t.TempDir()
	seedRoot := filepath.Join(base, "seed")
	attachmentRoot := filepath.Join(base, attachmenturl.DefaultLocalDir)

	// 种子素材：/uploads/seed/products/latiao.png
	if err := os.MkdirAll(filepath.Join(seedRoot, "products"), 0o755); err != nil {
		t.Fatalf("创建种子目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seedRoot, "products", "latiao.png"), []byte("png-seed"), 0o644); err != nil {
		t.Fatalf("写入种子素材失败: %v", err)
	}
	// 附件：/uploads/images/20260101/01/order.jpg
	if err := os.MkdirAll(filepath.Join(attachmentRoot, "images", "20260101", "01"), 0o755); err != nil {
		t.Fatalf("创建附件目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attachmentRoot, "images", "20260101", "01", "order.jpg"), []byte("jpg-upload"), 0o644); err != nil {
		t.Fatalf("写入附件失败: %v", err)
	}
	// 导出私有文件：<bin>/uploads/exports/task.csv（静态路由必须整体 404）
	if err := os.MkdirAll(filepath.Join(attachmentRoot, exportPrivateDir), 0o755); err != nil {
		t.Fatalf("创建导出目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attachmentRoot, exportPrivateDir, "task.csv"), []byte("export-must-not-leak"), 0o644); err != nil {
		t.Fatalf("写入导出夹具失败: %v", err)
	}
	// 两个根目录之外的敏感文件：目录穿越用例不得读到
	if err := os.WriteFile(filepath.Join(base, "secret.txt"), []byte("must-not-leak"), 0o644); err != nil {
		t.Fatalf("写入敏感夹具失败: %v", err)
	}

	r := gin.New()
	registerUploads(r, seedRoot, attachmentRoot, attachmenturl.DefaultLocalDir)
	return r, seedRoot, attachmentRoot
}

// doGet 发起一次请求并返回状态码与响应体
// 参数：
//   - r: gin 引擎
//   - target: 请求路径（可含 .. 等异常段）
//
// 返回值：
//   - int: HTTP 状态码
//   - string: 响应体
//
// 异常：不触发 panic
func doGet(r *gin.Engine, target string) (int, string) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// TestRegisterUploadsServesBothRoots 验证两个根目录各自命中且互不串用
// 参数：
//   - t: 测试上下文
//
// 异常：断言失败即 t.Fatalf
func TestRegisterUploadsServesBothRoots(t *testing.T) {
	r, _, _ := newUploadsTestRouter(t)

	cases := []struct {
		name string
		path string
		body string
	}{
		{"种子素材走 seed 目录", "/uploads/seed/products/latiao.png", "png-seed"},
		{"运行期附件走 attachment 目录", "/uploads/images/20260101/01/order.jpg", "jpg-upload"},
	}
	for _, c := range cases {
		code, body := doGet(r, c.path)
		if code != http.StatusOK {
			t.Fatalf("%s: 期望 200，实际 %d", c.name, code)
		}
		if body != c.body {
			t.Fatalf("%s: 内容不符，期望 %q 实际 %q", c.name, c.body, body)
		}
	}

	// 种子前缀不去附件目录找：/uploads/seed/secret.txt 必须 404，而不是命中 attachment/secret.txt
	if code, _ := doGet(r, "/uploads/seed/secret.txt"); code != http.StatusNotFound {
		t.Fatalf("种子路径不应回落到附件目录，期望 404，实际 %d", code)
	}
}

// TestRegisterUploadsBlocksExports 验证附件目录下的 exports 私有段在静态路由整体 404
// 导出文件（含用户数据）只能经订单服务的鉴权下载接口获取，直链、穿越与大小写变体都不放行
// 参数：
//   - t: 测试上下文
//
// 异常：断言失败即 t.Fatalf
func TestRegisterUploadsBlocksExports(t *testing.T) {
	r, _, _ := newUploadsTestRouter(t)

	for _, p := range []string{
		"/uploads/exports/task.csv",
		"/uploads/exports",
		"/uploads/exports/",
		"/uploads/images/../exports/task.csv",
		"/uploads/./exports/task.csv",
		"/uploads/images/20260101/../exports/task.csv",
		"/uploads/seed/../exports/task.csv",
		// Windows/macOS 文件系统大小写不敏感，直链与穿越变体都要拦
		"/uploads/Exports/task.csv",
		"/uploads/images/../EXPORTS/task.csv",
	} {
		code, body := doGet(r, p)
		if code != http.StatusNotFound {
			t.Fatalf("%s: 导出段期望 404，实际 %d（body=%q）", p, code, body)
		}
		if strings.Contains(body, "export-must-not-leak") {
			t.Fatalf("%s: 导出文件内容泄露", p)
		}
	}
}

// TestRegisterUploadsNormalizesBeforeDispatch 验证归一后的合法路径仍按所属根目录正常下发
// 目的：拦截 exports 用的是归一路径，正常附件（含可归一为合法附件路径的 . / .. 形态）不得被误伤
// 参数：
//   - t: 测试上下文
//
// 异常：断言失败即 t.Fatalf
func TestRegisterUploadsNormalizesBeforeDispatch(t *testing.T) {
	r, _, _ := newUploadsTestRouter(t)

	for _, p := range []string{
		"/uploads/images/20260101/01/order.jpg",
		"/uploads/images/20260101/../20260101/01/order.jpg",
		"/uploads/images/./20260101/01/order.jpg",
		"/uploads/images/20260101/01/../01/order.jpg",
	} {
		code, body := doGet(r, p)
		if code != http.StatusOK {
			t.Fatalf("%s: 归一后应命中附件，期望 200，实际 %d", p, code)
		}
		if body != "jpg-upload" {
			t.Fatalf("%s: 内容不符，期望 %q 实际 %q", p, "jpg-upload", body)
		}
	}
}

// TestRegisterUploadsRejectsTraversal 验证含 .. 的请求无法逃出根目录
// 参数：
//   - t: 测试上下文
//
// 异常：断言失败即 t.Fatalf
func TestRegisterUploadsRejectsTraversal(t *testing.T) {
	r, _, _ := newUploadsTestRouter(t)

	for _, p := range []string{
		"/uploads/seed/../secret.txt",
		"/uploads/seed/products/../../secret.txt",
		"/uploads/../secret.txt",
	} {
		code, body := doGet(r, p)
		if code == http.StatusOK {
			t.Fatalf("%s: 目录穿越不应成功，实际 200（body=%q）", p, body)
		}
		if body == "must-not-leak" {
			t.Fatalf("%s: 目录穿越泄露了根目录外文件", p)
		}
	}
}

// TestRegisterUploadsMissingFile 验证素材不存在时返回 404
// 参数：
//   - t: 测试上下文
//
// 异常：断言失败即 t.Fatalf
func TestRegisterUploadsMissingFile(t *testing.T) {
	r, _, _ := newUploadsTestRouter(t)

	if code, _ := doGet(r, "/uploads/seed/products/not-exist.png"); code != http.StatusNotFound {
		t.Fatalf("缺失素材期望 404，实际 %d", code)
	}
	if code, _ := doGet(r, "/uploads/images/not-exist.jpg"); code != http.StatusNotFound {
		t.Fatalf("缺失附件期望 404，实际 %d", code)
	}
}

// TestRegisterUploadsDisablesDirectoryListing 验证目录路径不渲染清单（noListingFS）
// http.FileServer 对目录请求有两种渲染：文件清单、或该目录下的 index.html，
// 两者都等于向任何打得到静态路由的人公开「这个目录里有什么」。附件目录按日期分桶、
// 文件名带订单等业务语义，清单本身就是信息泄露。
// 目录一律 404；index.html 直链与裸 /uploads 只保留各自的 301 规范化，落点仍是 404
// 参数：
//   - t: 测试上下文
//
// 异常：断言失败即 t.Fatalf
func TestRegisterUploadsDisablesDirectoryListing(t *testing.T) {
	r, seedRoot, attachmentRoot := newUploadsTestRouter(t)

	// index.html 形态：FileServer 默认把目录请求回落到 index.html，同样属于枚举
	if err := os.WriteFile(filepath.Join(attachmentRoot, "images", "20260101", "index.html"), []byte("index-listing-leak"), 0o644); err != nil {
		t.Fatalf("写入目录索引夹具失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seedRoot, "index.html"), []byte("seed-index-leak"), 0o644); err != nil {
		t.Fatalf("写入种子索引夹具失败: %v", err)
	}

	for _, p := range []string{
		"/uploads/",                    // 附件根
		"/uploads/images/",             // 含 index.html 的目录
		"/uploads/images",              // 含 index.html 的目录（无尾斜杠）
		"/uploads/images/20260101/01/", // 叶子目录
		"/uploads/seed/",               // 种子根
		"/uploads/seed/products",       // 种子子目录
		"/uploads/images/../../",       // 穿越到根目录也不清单
		"/uploads/seed/products/../",   // 种子目录列表
	} {
		code, body := doGet(r, p)
		if code != http.StatusNotFound {
			t.Fatalf("%s: 目录路径期望 404，实际 %d（body=%q）", p, code, body)
		}
		for _, leak := range []string{"index-listing-leak", "seed-index-leak", "order.jpg", "latiao.png", "<tr>"} {
			if strings.Contains(body, leak) {
				t.Fatalf("%s: 目录清单泄露（命中 %q）", p, leak)
			}
		}
	}

	// 直接点名索引页：FileServer 先 301 到所属目录，跟进后是 404，索引内容永不下发
	if code, body := doGet(r, "/uploads/images/20260101/index.html"); code != http.StatusMovedPermanently || body != "" {
		t.Fatalf("索引页直链期望 301 空体（重定向目标是 404 的目录），实际 %d（body=%q）", code, body)
	}

	// 裸 /uploads（无尾斜杠）由 gin 路由规范化为 301 → /uploads/，跟进后仍是 404，不产生清单
	if code, body := doGet(r, "/uploads"); code == http.StatusOK || strings.Contains(body, "order.jpg") {
		t.Fatalf("裸 /uploads 不应下发内容，实际 %d（body=%q）", code, body)
	}

	// 关闭清单不得误伤单文件下发
	if code, body := doGet(r, "/uploads/images/20260101/01/order.jpg"); code != http.StatusOK || body != "jpg-upload" {
		t.Fatalf("单文件下发受影响：期望 200 jpg-upload，实际 %d %q", code, body)
	}
	if code, body := doGet(r, "/uploads/seed/products/latiao.png"); code != http.StatusOK || body != "png-seed" {
		t.Fatalf("种子单文件下发受影响：期望 200 png-seed，实际 %d %q", code, body)
	}
}

// TestNoListingFSOpenSemantics 直接验证 noListingFS 的打开语义与句柄回收
// 经 http.FileServer 的端到端用例看不到句柄层面：目录命中时句柄必须先关闭再返回错误，
// 否则每次枚举请求都泄漏一个文件描述符
// 参数：
//   - t: 测试上下文
//
// 异常：断言失败即 t.Fatalf
func TestNoListingFSOpenSemantics(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}
	fsys := noListingFS{next: http.Dir(root)}

	// 目录（含根）一律 fs.ErrNotExist
	for _, name := range []string{"/", "/sub", "/sub/"} {
		file, err := fsys.Open(name)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Open(%q) 期望 fs.ErrNotExist，实际 %v", name, err)
		}
		if file != nil {
			t.Fatalf("Open(%q) 判定为目录时不得返回句柄", name)
		}
	}

	// 不存在的文件透传底层错误（仍是 fs.ErrNotExist）
	if _, err := fsys.Open("/sub/none.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("缺失文件期望 fs.ErrNotExist，实际 %v", err)
	}

	// 普通文件照常返回可读句柄
	file, err := fsys.Open("/sub/a.txt")
	if err != nil {
		t.Fatalf("Open 普通文件失败: %v", err)
	}
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		t.Fatalf("普通文件 Stat 异常: info=%+v err=%v", info, err)
	}
	buf := make([]byte, 1)
	if n, _ := file.Read(buf); n != 1 || buf[0] != 'a' {
		t.Fatalf("普通文件内容读取错误: n=%d buf=%q", n, buf)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("关闭句柄失败: %v", err)
	}
}
