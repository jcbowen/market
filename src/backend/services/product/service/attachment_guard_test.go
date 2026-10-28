package service

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"strings"
	"testing"
)

// TestResolveLocalImageMime 本地上传的 MIME 判定：以文件头为准，伪装一律拒
//
// 使用示例: go test ./services/product/service -run TestResolveLocalImageMime
func TestResolveLocalImageMime(t *testing.T) {
	pngBytes := encodePNG(t, 3, 2)
	jpegBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x04, 'J', 'F', 'I', 'F', 0x00}
	scriptBytes := []byte("<?php system($_GET['c']); ?>")
	heicBytes := append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftypheic")...)

	cases := []struct {
		name        string
		content     []byte
		contentType string
		ext         string
		wantMime    string
		wantErrPart string
	}{
		{"真实 png 且扩展名一致", pngBytes, "image/png", ".png", "image/png", ""},
		{"未声明 content_type 由文件头推导", pngBytes, "", ".png", "image/png", ""},
		{"声明非图片直接拒", pngBytes, "text/html", ".png", "", "content_type"},
		{"脚本伪装成 jpg 被拒", scriptBytes, "image/jpeg", ".jpg", "", "不是可识别的图片"},
		{"png 内容配 jpg 扩展名被拒", pngBytes, "image/png", ".jpg", "", "与扩展名不符"},
		{"真实 jpeg 放行", jpegBytes, "", ".jpg", "image/jpeg", ""},
		{"heic 无探测器按扩展名放行", heicBytes, "", ".heic", "image/heic", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveLocalImageMime(tc.content, tc.contentType, tc.ext)
			if tc.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrPart) {
					t.Fatalf("期望错误含 %q，实际 err=%v", tc.wantErrPart, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错，实际 err=%v", err)
			}
			if got != tc.wantMime {
				t.Fatalf("MIME 期望 %q，实际 %q", tc.wantMime, got)
			}
		})
	}
}

// TestDecodeImageSize 宽高探测：heic 记 0 放行，损坏文件报错而不是静默记 0
func TestDecodeImageSize(t *testing.T) {
	if _, _, err := decodeImageSize([]byte("not an image at all"), ".png"); err == nil {
		t.Fatal("损坏的 png 内容应返回错误")
	}
	w, h, err := decodeImageSize(nil, ".heic")
	if err != nil || w != 0 || h != 0 {
		t.Fatalf("heic 应返回 0x0 且不报错，实际 %dx%d err=%v", w, h, err)
	}

	img := encodePNG(t, 40, 17)
	w, h, err = decodeImageSize(img, ".png")
	if err != nil {
		t.Fatalf("合法 png 不应报错: %v", err)
	}
	if w != 40 || h != 17 {
		t.Fatalf("宽高期望 40x17，实际 %dx%d", w, h)
	}
}

// TestDecodeWebpCanvasSize 动图 WebP 的 VP8X 容器 x/image 解不了，需从容器头取画布尺寸
func TestDecodeWebpCanvasSize(t *testing.T) {
	content := buildVP8XWebp(t, 640, 480)
	w, h, ok := decodeWebpCanvasSize(content)
	if !ok || w != 640 || h != 480 {
		t.Fatalf("期望 640x480 ok=true，实际 %dx%d ok=%v", w, h, ok)
	}
	// 解码失败 + VP8X 头有效 ⇒ 宽高仍可入库，不落 0
	if w, h, err := decodeImageSize(content, ".webp"); err != nil || w != 640 || h != 480 {
		t.Fatalf("动图 webp 应回退容器头，实际 %dx%d err=%v", w, h, err)
	}
}

// encodePNG 生成指定尺寸的合法 PNG 字节，供 MIME 嗅探与宽高断言使用
//
// 参数:
//   - t: 测试句柄，失败时直接终止
//   - width: 目标宽度（像素）
//   - height: 目标高度（像素）
//
// 返回值:
//   - []byte: PNG 文件内容
func encodePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatalf("生成 PNG 失败: %v", err)
	}
	return buf.Bytes()
}

// buildVP8XWebp 构造仅含 VP8X chunk 的 WebP 头，用于断言画布尺寸解析
//
// 参数:
//   - t: 测试句柄
//   - width: 画布宽度（写入时按规范减 1）
//   - height: 画布高度（写入时按规范减 1）
//
// 返回值:
//   - []byte: 可被 decodeWebpCanvasSize 识别的字节
func buildVP8XWebp(t *testing.T, width, height int) []byte {
	t.Helper()
	content := []byte("RIFF")
	content = binary.LittleEndian.AppendUint32(content, 0) // 文件大小占位，解析不读取
	content = append(content, []byte("WEBP")...)
	content = append(content, []byte("VP8X")...)
	content = binary.LittleEndian.AppendUint32(content, 10) // VP8X 载荷长度
	content = append(content, 0x02)                         // flags: 仅 ICC，动图会再置 Anim 位
	content = append(content, 0x00, 0x00, 0x00)             // reserved 3 字节
	content = append(content, byte(width-1), byte((width-1)>>8), byte((width-1)>>16))
	content = append(content, byte(height-1), byte((height-1)>>8), byte((height-1)>>16))
	return content
}
