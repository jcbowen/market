// Package verifyfs 平台验证文件磁盘管理
// 微信公众号「JS接口安全域名」与小程序「业务域名」配置要求把平台下发的校验文件
// （如 MP_verify_xxx.txt，内容为纯文本 token；小程序校验文件内容为 JSON 串）放到
// 站点域名根目录，且必须能按 https://域名/文件名.txt 直接访问。
// 本包把验证文件落盘为网关运行目录下 verify/ 内的真实文件，网关以伪静态方式
// 原样下发（零查库）；容器重建后文件丢失属预期行为，在后台站点设置页重新生成即可。
package verifyfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DirName 验证文件目录名（位于网关可执行文件同级：<bin>/verify）
const DirName = "verify"

// 常量约束：
//   - MaxFileSize 单文件内容上限 64KB——平台校验文件实际仅数百字节，上限只为防滥用
//   - MaxFiles   目录内文件数量上限——验证文件每个平台/业务至多一两个
const (
	MaxFileSize = 64 << 10
	MaxFiles    = 20
)

// FileExt 强制文件后缀：只允许生成 .txt（校验器仅比对响应 body 内容，.txt 足够）
const FileExt = ".txt"

// baseNamePattern 文件名主体白名单（不含后缀）：字母数字与 ._ -
// 排除 / \ 等路径分隔符与空白控制字符，路径穿越在源头即被拒绝
var baseNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// Info 验证文件条目（列表接口出参）
type Info struct {
	Name     string `json:"name"`      // 完整文件名（含 .txt 后缀），即对外可访问的 URL 路径段
	Size     int64  `json:"size"`      // 字节数
	ModTime  string `json:"mod_time"`  // 最后写入时间
	BaseName string `json:"base_name"` // 文件名主体（不含后缀），供编辑回填
}

// ErrNotFound 文件不存在
var ErrNotFound = errors.New("验证文件不存在")

// DefaultDir 默认验证文件目录：可执行文件同级 verify/；定位失败回退相对路径 verify/
//
// 返回值:
//   - string: 目录绝对路径
func DefaultDir() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return DirName
	}
	return filepath.Join(filepath.Dir(exe), DirName)
}

// ValidateBaseName 校验并规范化文件名主体（用户输入，不含后缀）
// 用户顺手带上 .txt 后缀时剥掉再校验，避免生成 xxx.txt.txt
//
// 参数:
//   - raw: 用户输入的文件名
//
// 返回值:
//   - string: 规范化后的主体名
//   - error: 为空或不符白名单时返回可透传的业务错误
func ValidateBaseName(raw string) (string, error) {
	base := strings.TrimSpace(raw)
	base = strings.TrimSuffix(base, FileExt)
	if base == "" {
		return "", errors.New("请填写文件名")
	}
	if !baseNamePattern.MatchString(base) {
		return "", errors.New("文件名仅支持字母、数字与 . _ -，长度 1-64")
	}
	return base, nil
}

// FileName 由主体名拼完整文件名（强制 .txt 后缀）
//
// 参数:
//   - base: 已通过 ValidateBaseName 的主体名
//
// 返回值:
//   - string: 完整文件名
func FileName(base string) string {
	return base + FileExt
}

// ValidateContent 校验文件内容
//
// 参数:
//   - content: 文件内容字节
//
// 返回值:
//   - error: 为空或超限时返回可透传的业务错误
func ValidateContent(content []byte) error {
	if len(content) == 0 {
		return errors.New("请填写文件内容")
	}
	if len(content) > MaxFileSize {
		return fmt.Errorf("文件内容不能超过 %dKB", MaxFileSize>>10)
	}
	return nil
}

// Save 写入验证文件（同主体名覆盖更新）
// 先写临时文件再 rename，保证校验器任何时刻读到的都是完整文件
//
// 参数:
//   - dir: 验证文件目录
//   - base: 已通过 ValidateBaseName 的主体名
//   - content: 文件内容
//
// 返回值:
//   - error: 数量超限或写盘异常
func Save(dir, base string, content []byte) error {
	if err := ValidateContent(content); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建验证文件目录失败: %w", err)
	}

	name := FileName(base)
	if _, err := os.Stat(filepath.Join(dir, name)); os.IsNotExist(err) {
		files, lerr := List(dir)
		if lerr != nil {
			return lerr
		}
		if len(files) >= MaxFiles {
			return fmt.Errorf("验证文件最多保留 %d 个，请先删除不需要的文件", MaxFiles)
		}
	}

	tmp := filepath.Join(dir, name+".tmp")
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return fmt.Errorf("写入验证文件失败: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("写入验证文件失败: %w", err)
	}
	return nil
}

// Delete 删除验证文件；完整文件名同样受白名单约束（含后缀校验），穿越形态一律拒绝
//
// 参数:
//   - dir: 验证文件目录
//   - name: 完整文件名（含 .txt 后缀）
//
// 返回值:
//   - error: 文件不存在返回 ErrNotFound，其余为写盘异常
func Delete(dir, name string) error {
	if err := validateFileName(name); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("删除验证文件失败: %w", err)
	}
	return nil
}

// List 列出目录内全部验证文件（仅常规 .txt 文件，按名称排序）
//
// 参数:
//   - dir: 验证文件目录
//
// 返回值:
//   - []Info: 文件条目
//   - error: 目录不存在视为空列表，其余为读盘异常
func List(dir string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Info{}, nil
		}
		return nil, fmt.Errorf("读取验证文件目录失败: %w", err)
	}

	list := make([]Info, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), FileExt) {
			continue
		}
		fi, ierr := e.Info()
		if ierr != nil || !fi.Mode().IsRegular() {
			continue
		}
		list = append(list, Info{
			Name:     e.Name(),
			Size:     fi.Size(),
			ModTime:  fi.ModTime().Format(time.DateTime),
			BaseName: strings.TrimSuffix(e.Name(), FileExt),
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// Lookup 伪静态查询：文件名合法且目录内存在同名常规文件时返回其绝对路径
// 供网关 NoRoute 调用，仅做存在性检查，读取与下发交给 http.ServeFile 语义
//
// 参数:
//   - dir: 验证文件目录
//   - name: URL 中的单段文件名（应含 .txt 后缀）
//
// 返回值:
//   - string: 文件绝对路径；不存在时为空串
//   - bool: 是否命中
func Lookup(dir, name string) (string, bool) {
	if err := validateFileName(name); err != nil {
		return "", false
	}
	fp := filepath.Join(dir, name)
	fi, err := os.Stat(fp)
	if err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	return fp, true
}

// validateFileName 完整文件名校验：白名单字符集 + 必须 .txt 结尾
// Lookup/Delete 的入参来自 URL 与外部请求，必须独立过闸，防止路径穿越与任意读删
//
// 参数:
//   - name: 完整文件名
//
// 返回值:
//   - error: 不合法时返回错误
func validateFileName(name string) error {
	base, err := ValidateBaseName(name)
	if err != nil {
		return ErrNotFound // 对外不泄露校验细节，仅按不存在处理
	}
	if base+FileExt != name {
		return ErrNotFound
	}
	return nil
}
