/**
 * GeoLite2-City IP 定位数据库下载/更新工具
 *
 * 数据来源：https://geo.wp-statistics.com/#databases（每周二/五更新，jsDelivr CDN 直链，无需鉴权）
 * 数据库文件不入 git 仓库（.gitignore 已忽略 *.mmdb*），由本工具按需下载到 bin/data/geoip/。
 * 网关按文件 mtime 热重载数据库，替换后无需重启。
 *
 * 使用场景（默认 dest 相对当前工作目录解析，指向「网关二进制所在 bin/ 目录」
 * 的 data/geoip，与发布包及本地构建布局一致；容器内布局不同，调用方须显式传 --dest）：
 *   - 开发/构建机：deps-tools.mjs 的 ensureGeoipDatabase 在 start-all / build-select / deploy
 *     时检测 bin/data/geoip 缺失后以 `go run ./cmd/geoip-download` 调用（工作目录 =
 *     src/backend，go.mod 所在；默认 dest 相对 cwd 会指错位置，故显式传项目根下绝对路径）；
 *   - allinone 单容器：entrypoint.sh 检测 /app/data/geoip 无 mmdb 后执行本二进制，
 *     显式传 --dest /app/data/geoip/GeoLite2-City.mmdb；
 *   - 服务器 crontab 定期更新：直接执行发布包内的二进制（cwd = 发布包根，默认 dest
 *     bin/data/geoip/ 与网关二进制所在 bin/ 目录相对约定一致），无需任何运行时依赖。
 *
 * 用法（默认 dest 相对当前工作目录解析）：
 *   geoip-download                  # 数据库 mtime 距今 3 天内则跳过（配合 crontab 定期执行）
 *   geoip-download --force          # 强制重新下载
 *   geoip-download --url <...>      # 覆盖下载地址（默认 GeoLite2-City，可换 dbip-city-lite）
 *   geoip-download --dest <路径>    # 覆盖输出路径（默认 bin/data/geoip/GeoLite2-City.mmdb）
 *
 * 定期更新（服务器 crontab，每天凌晨 4 点执行）：
 *   0 4 * * * cd /opt/market && ./bin/linux-amd64/geoip-download >> logs/geoip-download.log 2>&1
 */
package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// 默认下载地址与新鲜期，与历史 download-geoip.mjs 语义保持一致
const (
	defaultURL = "https://cdn.jsdelivr.net/npm/geolite2-city/GeoLite2-City.mmdb.gz"
	// 数据库每周二/五发布；默认 3 天内视为新鲜，避免无意义的每日重复下载
	freshDays = 3
	// httpTimeout 单次下载超时：数据库约 60MB（gz），国内经 jsDelivr 常规带宽下留足余量
	httpTimeout = 10 * time.Minute
	// maxDecompressedSize 解压体积上限：--url 可控，恶意/错误地址的 gzip 炸弹会无限膨胀。
	// 下载为流式落盘（io.Copy，内存占用仅 32KB 拷贝缓冲 + gzip 窗口，约几 MB），
	// 超限检测靠「LimitReader 多放 1 字节、实际写入字节数超上限即拒绝」，
	// GeoLite2-City 解压后约 600MB，上限放宽到 1GB
	maxDecompressedSize = 1 << 30
	// minMMDBSize mmdb 合法文件大小下限（字节）：低于此值连数据段+元数据都放不下
	minMMDBSize = 1024
	// mmdbTailCheckSize 校验时回读的文件尾部大小：元数据段标识固定位于末尾 128KB 内
	mmdbTailCheckSize = 131072
)

// metadataMarker mmdb 元数据段固定以 \xab\xcd\xefMaxMind.com 标识结尾，位于文件末尾
var metadataMarker = []byte{0xab, 0xcd, 0xef, 'M', 'a', 'x', 'M', 'i', 'n', 'd', '.', 'c', 'o', 'm'}

func main() {
	force := flag.Bool("force", false, "忽略新鲜期强制重新下载")
	url := flag.String("url", defaultURL, "覆盖下载地址")
	dest := flag.String("dest", filepath.Join("bin", "data", "geoip", "GeoLite2-City.mmdb"), "覆盖输出路径（相对 cwd 解析）")
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("[geoip] ")

	if err := run(*url, *dest, *force); err != nil {
		log.Fatalf("%v", err)
	}
}

// run 执行一次下载/更新流程（流式落盘 + 校验后原子替换，内存占用与库体积无关）
// 参数：
//   - url:   mmdb.gz 下载地址
//   - dest:  输出路径（相对 cwd 解析）
//   - force: true 时跳过新鲜期检查
//
// 返回：
//   - error: 失败原因；成功或跳过返回 nil。失败路径只删除临时文件，
//     绝不触碰既有数据库（网关可继续用旧文件）
func run(url, dest string, force bool) error {
	if !force {
		if info, err := os.Stat(dest); err == nil {
			ageDays := time.Since(info.ModTime()).Hours() / 24
			if ageDays < freshDays {
				log.Printf("数据库为最新（%.1f 天前更新），跳过下载：%s", ageDays, dest)
				return nil
			}
			log.Printf("数据库已 %.1f 天未更新，开始下载...", ageDays)
		}
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	// 原子替换：先流式写同目录临时文件并校验，成功后 rename——中断/失败不破坏现有数据库
	tmp := dest + ".tmp"
	size, err := downloadToTempFile(url, tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换数据库失败: %w", err)
	}

	log.Printf("更新完成：%s（%.1f MB）", dest, float64(size)/1048576)
	return nil
}

// downloadToTempFile 流式下载 .gz 并解压写入临时文件，写完后回读尾部校验
// 内存占用恒定为小量（io.Copy 32KB 缓冲 + gzip.Reader 窗口约 2MB），
// 替代旧「io.ReadAll 整读 600MB 进内存」——一体化镜像内与 mysqld/redis 及
// 7 个业务进程同容器并存，小内存 VPS 上整读有 OOM 打死常驻进程的现实风险。
// 超限防护：LimitReader 只放行 maxDecompressedSize+1 字节，实际写入字节数
// 超过上限即判定 gzip 炸弹/非正常库，拒绝且不留临时文件内容可用。
//
// 参数：
//   - url: mmdb.gz 下载地址
//   - tmp: 临时文件路径（与 dest 同目录，调用方负责失败清理与成功 rename）
//
// 返回：
//   - int64: 校验通过的解压后文件体积（字节）
//   - error: 下载/解压/写入/超限/校验失败原因
//
// 使用示例：
//
//	size, err := downloadToTempFile(defaultURL, dest+".tmp")
func downloadToTempFile(url, tmp string) (int64, error) {
	client := &http.Client{Timeout: httpTimeout}
	log.Printf("下载 %s", url)

	resp, err := client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("下载失败：HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("解压失败：响应不是合法的 gzip 流: %w", err)
	}
	defer gz.Close()

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("创建临时文件失败: %w", err)
	}
	// 显式 Close 拿写入错误（rename 前必须关闭）；defer 兜底异常路径，重复 Close 的报错无害
	defer func() { _ = f.Close() }()

	// LimitReader 防解压炸弹：超限后读出长度恰好等于上限+1，copy 计数超限即走拒绝路径，
	// 且响应体剩余部分不再被消费（快速失败，不拖满 10 分钟超时）
	written, err := io.Copy(f, io.LimitReader(gz, maxDecompressedSize+1))
	if err != nil {
		return 0, fmt.Errorf("解压失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("写入临时文件失败: %w", err)
	}
	if written > maxDecompressedSize {
		return 0, fmt.Errorf("解压后体积超过上限 %dMB，疑似非正常数据库，放弃更新", maxDecompressedSize>>20)
	}

	if !validMMDBFile(tmp, written) {
		return 0, fmt.Errorf("解压后的文件不是合法的 mmdb 格式，放弃更新")
	}
	return written, nil
}

// validMMDBFile 校验已落盘文件是否为合法 mmdb：体积下限 + 元数据段标识
// （固定位于文件末尾 128KB 内，见 MaxMind DB 格式规范）。
// 判定口径与原内存版 validMMDB 完全一致——mmdb 文件头部是数据段（树节点区），
// 无固定魔数可校验，实际依赖的结构特征只有「尾部 128KB 内的 metadataMarker」与
// 体积下限，故只需回读尾部 128KB，无须把 600MB 全量读进内存。
//
// 参数：
//   - path: 待校验文件路径（刚写完的临时文件）
//   - size: 调用方已知的文件体积（字节，io.Copy 的写入计数）
//
// 返回：
//   - bool: true 表示内容合法；打开/读取失败一律按不合法处理（不 panic）
func validMMDBFile(path string, size int64) bool {
	if size < minMMDBSize {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	tailStart := size - mmdbTailCheckSize
	if tailStart < 0 {
		tailStart = 0
	}
	buf := make([]byte, size-tailStart)
	if _, err := f.ReadAt(buf, tailStart); err != nil && err != io.EOF {
		return false
	}
	return bytes.Contains(buf, metadataMarker)
}
