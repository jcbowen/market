// gen-tabbar-icons 生成 C 端 tabBar 图标 PNG。
//
// 使用 Go 标准库绘制（零第三方依赖），81×81px、4x 超采样后 box 降采样抗锯齿、透明背景。
// 4 个图标（home/grid/cart/user）× 2 个状态（普通/选中）= 8 张 PNG，输出到指定目录。
// 图标与配色可长期复用：改色/改尺寸只需修改常量后重跑。
//
// 用法：go run main.go [输出目录]（默认 /tmp/ico-out）
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const (
	logicalSize = 81 // 图标逻辑尺寸（px，uniapp tabBar 官方规范 81×81）
	superScale  = 4  // 超采样倍数（按 324×324 密度采样后 box 降采样抗锯齿）
)

// 配色规范，与 src/client/src/pages.json 的 tabBar color/selectedColor 一致
var (
	colorNormal = color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 0xff} // 普通态 #999999
	colorActive = color.RGBA{R: 0x07, G: 0xc1, B: 0x60, A: 0xff} // 选中态 #07c160
)

// point 二维点（81px 逻辑画布坐标）
type point struct{ x, y float64 }

// shapeFunc 判断逻辑坐标 (x, y) 是否落在形状内部，返回 true 表示该处被填充
type shapeFunc func(x, y float64) bool

// inCircle 判断点是否在圆内（含边界）。
// 参数 x, y 为待测点坐标，cx, cy 为圆心，r 为半径；返回 true 表示点在圆内。
func inCircle(x, y, cx, cy, r float64) bool {
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

// inRoundRect 判断点是否在圆角矩形内（含边界）。
// 参数 rx, ry 为左上角坐标，w/h 为宽高，r 为圆角半径；返回 true 表示点在矩形内。
// 实现：先将点钳制到内部矩形，再按圆角距离判定。
func inRoundRect(x, y, rx, ry, w, h, r float64) bool {
	if x < rx || x > rx+w || y < ry || y > ry+h {
		return false
	}
	cx := math.Min(math.Max(x, rx+r), rx+w-r)
	cy := math.Min(math.Max(y, ry+r), ry+h-r)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

// inPolygon 判断点是否在多边形内（偶奇规则射线法）。
// 参数 pts 为按顺序排列的顶点；返回 true 表示点在多边形内部。
func inPolygon(x, y float64, pts []point) bool {
	inside := false
	j := len(pts) - 1
	for i := range len(pts) {
		pi, pj := pts[i], pts[j]
		if (pi.y > y) != (pj.y > y) {
			xCross := (pj.x-pi.x)*(y-pi.y)/(pj.y-pi.y) + pi.x
			if x < xCross {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

// iconDefs 图标定义：名称 + 形状集合（坐标均为 81px 逻辑画布坐标，填充风格）
var iconDefs = []struct {
	name   string
	shapes []shapeFunc
}{
	{
		name: "home",
		shapes: []shapeFunc{
			// 三角屋顶
			func(x, y float64) bool {
				return inPolygon(x, y, []point{{40.5, 10}, {8, 38}, {73, 38}})
			},
			// 圆角房体
			func(x, y float64) bool { return inRoundRect(x, y, 16, 36, 49, 33, 6) },
		},
	},
	{
		name: "grid",
		shapes: []shapeFunc{
			// 2×2 圆角方块
			func(x, y float64) bool { return inRoundRect(x, y, 12, 12, 24, 24, 6) },
			func(x, y float64) bool { return inRoundRect(x, y, 45, 12, 24, 24, 6) },
			func(x, y float64) bool { return inRoundRect(x, y, 12, 45, 24, 24, 6) },
			func(x, y float64) bool { return inRoundRect(x, y, 45, 45, 24, 24, 6) },
		},
	},
	{
		name: "cart",
		shapes: []shapeFunc{
			// 车斗
			func(x, y float64) bool { return inRoundRect(x, y, 14, 22, 45, 30, 5) },
			// 推杆横条
			func(x, y float64) bool { return inRoundRect(x, y, 6, 16, 12, 7, 3.5) },
			// 推杆与车斗之间的斜连接四边形
			func(x, y float64) bool {
				return inPolygon(x, y, []point{{16, 18}, {24, 22}, {22, 28}, {14, 24}})
			},
			// 两只轮子
			func(x, y float64) bool { return inCircle(x, y, 26, 62, 7) },
			func(x, y float64) bool { return inCircle(x, y, 52, 62, 7) },
		},
	},
	{
		name: "user",
		shapes: []shapeFunc{
			// 头部
			func(x, y float64) bool { return inCircle(x, y, 40.5, 26, 14) },
			// 肩部（圆下缘超出画布，被自然裁剪为拱形）
			func(x, y float64) bool { return inCircle(x, y, 40.5, 68, 24) },
		},
	},
}

// renderIcon 将单个图标按指定颜色渲染为 81×81 透明底图像。
// 参数 shapes 为形状集合，c 为填充色（含透明度）；
// 返回值为 NRGBA 图像，边缘像素按覆盖率带 alpha 过渡（抗锯齿）。
func renderIcon(shapes []shapeFunc, c color.RGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, logicalSize, logicalSize))
	total := superScale * superScale
	for py := range logicalSize {
		for px := range logicalSize {
			// 统计 4x4 子采样网格的覆盖数作为像素覆盖率（等效 box 降采样）
			cover := 0
			for sy := range superScale {
				for sx := range superScale {
					x := (float64(px*superScale+sx) + 0.5) / superScale
					y := (float64(py*superScale+sy) + 0.5) / superScale
					for _, s := range shapes {
						if s(x, y) {
							cover++
							break
						}
					}
				}
			}
			i := img.PixOffset(px, py)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = c.R, c.G, c.B
			img.Pix[i+3] = uint8(uint32(c.A) * uint32(cover) / uint32(total))
		}
	}
	return img
}

// main 程序入口：解析输出目录参数并启动生成流程。
// 参数 os.Args[1] 可选，为输出目录（默认 /tmp/ico-out）；
// 失败时向 stderr 输出错误并以退出码 1 结束。
func main() {
	outDir := "/tmp/ico-out"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	if err := run(outDir); err != nil {
		fmt.Fprintln(os.Stderr, "生成失败:", err)
		os.Exit(1)
	}
}

// run 执行图标生成主流程：创建输出目录，逐张渲染 4 图标 × 2 状态共 8 张 PNG。
// 参数 outDir 为输出目录；返回目录创建或文件写入过程中的错误，成功返回 nil。
func run(outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录 %s: %w", outDir, err)
	}
	variants := []struct {
		suffix string
		color  color.RGBA
	}{
		{suffix: "", color: colorNormal},
		{suffix: "-active", color: colorActive},
	}
	for _, def := range iconDefs {
		for _, v := range variants {
			name := def.name + v.suffix + ".png"
			if err := writePNG(filepath.Join(outDir, name), renderIcon(def.shapes, v.color)); err != nil {
				return err
			}
			fmt.Println("已生成", filepath.Join(outDir, name))
		}
	}
	return nil
}

// writePNG 将图像编码写入指定路径的 PNG 文件。
// 参数 path 为目标文件路径，img 为待编码图像；
// 返回创建文件或编码过程中的错误，成功返回 nil。
func writePNG(path string, img *image.NRGBA) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建文件 %s: %w", path, err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return fmt.Errorf("编码 PNG %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭文件 %s: %w", path, err)
	}
	return nil
}
