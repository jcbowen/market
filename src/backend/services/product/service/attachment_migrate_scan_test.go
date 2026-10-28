package service

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScanLocalAttachmentFilesSkipsExports 扫描附件根时排除导出目录：
// 订单 CSV 含用户数据、且不登记在 attachment 表，不能被当成孤儿文件进迁移清单
//
// 使用示例: go test ./services/product/service -run TestScanLocalAttachmentFiles
func TestScanLocalAttachmentFilesSkipsExports(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "images", "20261028", "a.png"), 12)
	writeTestFile(t, filepath.Join(root, "exports", "orders-20261028.csv"), 34)
	// 嵌套的 exports 同名目录不在排除范围（只跳顶层），此处断言它仍被扫到
	writeTestFile(t, filepath.Join(root, "images", "exports", "keep.png"), 56)

	files, err := scanLocalAttachmentFiles(root)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if _, ok := files["exports/orders-20261028.csv"]; ok {
		t.Fatalf("导出文件不应进清单，实际清单为 %v", files)
	}
	if got := files["images/20261028/a.png"]; got != 12 {
		t.Fatalf("附件应正常入清单且大小一致，实际 %d", got)
	}
	if got := files["images/exports/keep.png"]; got != 56 {
		t.Fatalf("仅顶层 exports 目录被排除，嵌套同名目录应保留，实际 %d", got)
	}
}

// TestScanLocalAttachmentFilesMissingRoot 附件根不存在时返回空清单而不是报错
func TestScanLocalAttachmentFilesMissingRoot(t *testing.T) {
	files, err := scanLocalAttachmentFiles(filepath.Join(t.TempDir(), "not-created"))
	if err != nil {
		t.Fatalf("目录缺失不应报错: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("期望空清单，实际 %v", files)
	}
}

// writeTestFile 在指定路径写入指定长度的文件（父目录自动创建）
//
// 参数:
//   - t: 测试句柄，失败时直接终止
//   - path: 目标文件路径
//   - size: 文件字节数
func writeTestFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("写入文件失败: %v", err)
	}
}
