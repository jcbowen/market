/**
 * IP 归属地定位：基于本地 GeoLite2-City mmdb 数据库的离线查询
 *
 * 方案约定：
 *   - 数据库来源 https://geo.wp-statistics.com/#databases（GeoLite2-City，每周二/五更新，
 *     jsDelivr CDN 直链下载，无需鉴权），由根目录 download-geoip.mjs 定期下载更新；
 *   - 数据库文件不入 git 仓库（见 .gitignore 的 *.mmdb 规则）；
 *   - 文件被替换后按 mtime 热重载，无需重启网关；
 *   - 数据库缺失或查询未命中时返回 nil，调用方自行降级，不影响其他功能。
 */
package geoip

import (
	"fmt"
	"net/netip"
	"os"
	"sync"
	"time"

	geoip2 "github.com/oschwald/geoip2-golang/v2"
)

// mtimeCheckInterval 文件 mtime 检查的最小间隔，避免每次查询都 stat 磁盘
const mtimeCheckInterval = time.Minute

// Location IP 归属地定位结果
type Location struct {
	Latitude    float64 // 纬度
	Longitude   float64 // 经度
	Country     string  // ISO 3166-1 alpha-2 国家码，如 CN
	CountryName string  // 国家名称（中文优先，英文兜底）
	City        string  // 城市名称（中文优先，英文兜底）
}

// manager 数据库读取器单例：reader 可并发使用，重载由互斥锁保护
type manager struct {
	mu        sync.Mutex
	path      string
	reader    *geoip2.Reader
	modTime   time.Time
	size      int64
	lastCheck time.Time
}

var mgr manager

// Lookup 按本地 GeoLite2-City 数据库查询 IP 归属地
// 内网/回环/未指定地址、数据库文件缺失或查询未命中时返回 nil
// 参数：
//   - dbPath: mmdb 数据库路径（建议传绝对路径）
//   - ip:     待查询的 IP
// 返回：
//   - *Location: 定位结果，未命中返回 nil
func Lookup(dbPath string, ip netip.Addr) *Location {
	if dbPath == "" || !ip.IsValid() || ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
		return nil
	}

	reader := acquireReader(dbPath)
	if reader == nil {
		return nil
	}

	record, err := reader.City(ip)
	if err != nil || record == nil || !record.Location.HasCoordinates() {
		return nil
	}

	return &Location{
		Latitude:    derefFloat(record.Location.Latitude),
		Longitude:   derefFloat(record.Location.Longitude),
		Country:     record.Country.ISOCode,
		CountryName: pickName(record.Country.Names),
		City:        pickName(record.City.Names),
	}
}

// acquireReader 获取可用的数据库读取器，必要时惰性加载或按 mtime 热重载
// 数据库文件尚未下载（首次部署）时静默返回 nil，并按 mtimeCheckInterval 间隔重试
// 参数：
//   - dbPath: mmdb 数据库路径
// 返回：
//   - *geoip2.Reader: 读取器，不可用时返回 nil
func acquireReader(dbPath string) *geoip2.Reader {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	// 路径变更（如配置热更新）或未加载时直接重建
	if mgr.reader != nil && mgr.path == dbPath && time.Since(mgr.lastCheck) < mtimeCheckInterval {
		return mgr.reader
	}
	mgr.lastCheck = time.Now()

	if mgr.reader != nil && mgr.path != dbPath {
		_ = mgr.reader.Close()
		mgr.reader = nil
	}

	info, err := os.Stat(dbPath)
	if err != nil {
		// 文件缺失：丢弃旧读取器，等待文件就绪后重建
		if mgr.reader != nil {
			_ = mgr.reader.Close()
			mgr.reader = nil
		}
		return nil
	}

	// 文件未变化：继续复用现有读取器
	if mgr.reader != nil && mgr.modTime.Equal(info.ModTime()) && mgr.size == info.Size() {
		return mgr.reader
	}

	// 首次加载或文件已被更新脚本替换：重新打开
	newReader, err := geoip2.Open(dbPath)
	if err != nil {
		if mgr.reader != nil {
			_ = mgr.reader.Close()
			mgr.reader = nil
		}
		fmt.Printf("[geoip] 加载数据库失败 %s: %v\n", dbPath, err)
		return nil
	}

	if mgr.reader != nil {
		_ = mgr.reader.Close()
	}
	mgr.reader = newReader
	mgr.path = dbPath
	mgr.modTime = info.ModTime()
	mgr.size = info.Size()
	return mgr.reader
}

// pickName 取本地化名称：中文优先，英文兜底
// 参数：
//   - names: 数据库记录的多语言名称集合
// 返回：
//   - string: 名称，无数据时为空字符串
func pickName(names geoip2.Names) string {
	if names.SimplifiedChinese != "" {
		return names.SimplifiedChinese
	}
	return names.English
}

// derefFloat 解引用 float64 指针，nil 返回 0
// 参数：
//   - p: 指针
// 返回：
//   - float64: 解引用结果
func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
