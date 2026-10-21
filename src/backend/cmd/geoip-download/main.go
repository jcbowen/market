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

// run 执行一次下载/更新流程
// 参数：
//   - url:   mmdb.gz 下载地址
//   - dest:  输出路径（相对 cwd 解析）
//   - force: true 时跳过新鲜期检查
//
// 返回：
//   - error: 失败原因；成功或跳过返回 nil
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

	mmdb, err := downloadAndValidate(url)
	if err != nil {
		return err
	}

	// 原子替换：先写临时文件再 rename，中断不会破坏现有数据库（网关仍可用旧文件）
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, mmdb, 0o644); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换数据库失败: %w", err)
	}

	log.Printf("更新完成：%s（%.1f MB）", dest, float64(len(mmdb))/1048576)
	return nil
}

// downloadAndValidate 下载 .gz 并解压校验
// 参数：
//   - url: mmdb.gz 下载地址
//
// 返回：
//   - []byte: 校验通过的 mmdb 原始内容
//   - error:  下载/解压/校验失败原因
func downloadAndValidate(url string) ([]byte, error) {
	client := &http.Client{Timeout: httpTimeout}
	log.Printf("下载 %s", url)

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载失败：HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("解压失败：响应不是合法的 gzip 流: %w", err)
	}
	defer gz.Close()

	mmdb, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("解压失败: %w", err)
	}

	if !validMMDB(mmdb) {
		return nil, fmt.Errorf("解压后的文件不是合法的 mmdb 格式，放弃更新")
	}
	return mmdb, nil
}

// validMMDB 校验解压后的内容是否为合法 mmdb：
// 长度下限 + 元数据段标识（固定位于文件末尾 128KB 内）
// 参数：
//   - b: 解压后的文件内容
//
// 返回：
//   - bool: true 表示内容合法
func validMMDB(b []byte) bool {
	if len(b) < 1024 {
		return false
	}
	tailStart := len(b) - 131072
	if tailStart < 0 {
		tailStart = 0
	}
	tail := b[tailStart:]
	return bytes.Index(tail, metadataMarker) >= 0
}
