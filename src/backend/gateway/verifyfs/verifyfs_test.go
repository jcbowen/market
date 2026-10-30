package verifyfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/**
 * verifyfs 平台验证文件磁盘管理测试（离线，纯文件操作）
 *
 * 运行方式：
 *   go test ./gateway/verifyfs/ -v
 *
 * 覆盖：
 *   - 文件名主体校验：白名单字符集、后缀剥离、空值与非法形态拒绝
 *   - Save/List/Delete 往返与同主体名覆盖更新
 *   - 内容与数量上限
 *   - Lookup/Delete 的穿越与非法文件名拒绝
 */

func TestValidateBaseName(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"常规名", "MP_verify_AbC123", "MP_verify_AbC123", false},
		{"用户顺手带后缀则剥离", "pI0foeRR2F.txt", "pI0foeRR2F", false},
		{"允许点横下划线", "a.b_c-d", "a.b_c-d", false},
		{"空白", "   ", "", true},
		{"空串", "", "", true},
		{"含空格", "a b", "", true},
		{"含斜杠", "a/b", "", true},
		{"含反斜杠", `a\b`, "", true},
		{"含中文", "验证文件", "", true},
		{"超长65", strings.Repeat("a", 65), "", true},
		{"剥离后仅剩点号", ".txt", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateBaseName(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际通过: %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望通过，实际报错: %v", err)
			}
			if got != tc.want {
				t.Fatalf("期望 %q，实际 %q", tc.want, got)
			}
		})
	}
}

func TestSaveListDeleteRoundTrip(t *testing.T) {
	dir := t.TempDir()

	if err := Save(dir, "MP_verify_AAA", []byte("token-111")); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if err := Save(dir, "pI0foeRR2F", []byte(`{"file_name":"pI0foeRR2F.txt","file_content":"x"}`)); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	// 同主体名覆盖更新
	if err := Save(dir, "MP_verify_AAA", []byte("token-222")); err != nil {
		t.Fatalf("覆盖保存失败: %v", err)
	}

	list, err := List(dir)
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("期望 2 个文件，实际 %d: %+v", len(list), list)
	}
	if list[0].Name != "MP_verify_AAA.txt" || list[0].Size != int64(len("token-222")) {
		t.Fatalf("首个条目不符合预期: %+v", list[0])
	}
	if list[0].BaseName != "MP_verify_AAA" || list[0].ModTime == "" {
		t.Fatalf("条目字段不符合预期: %+v", list[0])
	}

	// 命中读取
	fp, ok := Lookup(dir, "MP_verify_AAA.txt")
	if !ok {
		t.Fatal("期望命中 MP_verify_AAA.txt")
	}
	data, err := os.ReadFile(fp)
	if err != nil || string(data) != "token-222" {
		t.Fatalf("文件内容不符合预期: %s, err=%v", data, err)
	}

	// 删除
	if err := Delete(dir, "MP_verify_AAA.txt"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, ok := Lookup(dir, "MP_verify_AAA.txt"); ok {
		t.Fatal("删除后不应再命中")
	}
	if err := Delete(dir, "MP_verify_AAA.txt"); err != ErrNotFound {
		t.Fatalf("重复删除期望 ErrNotFound，实际 %v", err)
	}
}

func TestSaveLimits(t *testing.T) {
	dir := t.TempDir()

	if err := Save(dir, "empty", nil); err == nil {
		t.Fatal("空内容期望报错")
	}
	if err := Save(dir, "toolarge", make([]byte, MaxFileSize+1)); err == nil {
		t.Fatal("超限内容期望报错")
	}

	// 数量上限
	for i := 0; i < MaxFiles; i++ {
		if err := Save(dir, "file"+string(rune('a'+i)), []byte("x")); err != nil {
			t.Fatalf("第 %d 个保存失败: %v", i+1, err)
		}
	}
	if err := Save(dir, "overflow", []byte("x")); err == nil {
		t.Fatal("超数量期望报错")
	}
	// 已存在的文件覆盖更新不受数量上限影响
	if err := Save(dir, "filea", []byte("y")); err != nil {
		t.Fatalf("覆盖已有文件失败: %v", err)
	}
}

func TestLookupAndDeleteRejectInvalidNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("夹具写入失败: %v", err)
	}
	// 目录外敏感文件：任何穿越形态都不得读到
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.txt"), []byte("must-not-leak"), 0o644); err != nil {
		t.Fatalf("敏感夹具写入失败: %v", err)
	}

	for _, name := range []string{
		"../secret.txt", // 相对穿越
		"a/b.txt",       // 子路径
		`..\secret.txt`, // Windows 分隔符
		"..",            // 目录本身
		"ok",            // 缺后缀
		"ok.bak",        // 非法后缀
		"",              // 空
	} {
		if fp, ok := Lookup(dir, name); ok {
			t.Fatalf("非法文件名 %q 不应命中，命中路径 %s", name, fp)
		}
		if err := Delete(dir, name); err == nil {
			t.Fatalf("非法文件名 %q 删除应被拒绝", name)
		}
	}
}

func TestListEmptyDir(t *testing.T) {
	// 目录不存在视为空列表而非报错（首次使用尚未生成任何验证文件）
	list, err := List(filepath.Join(t.TempDir(), "not-exist"))
	if err != nil || len(list) != 0 {
		t.Fatalf("不存在的目录期望空列表，实际 %v, %v", list, err)
	}
}
