package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/response"
	"github.com/jcbowen/market/src/backend/common/version"
)

// 更新检测的双镜像源与缓存参数
// 项目同时发布到 Docker Hub 与 GHCR（GitHub Container Registry），两个源并行查询、
// 取各自最新稳定版的较大者（任一源有新版即提示，单源失败不影响整体结果）。
// 仓库地址有内置默认值（jcbowen/market），环境变量可覆盖，设为 off 显式关闭该源：
//   - DOCKERHUB_REPO：Docker Hub 仓库（如 jcbowen/market）
//   - GHCR_REPO：GHCR 仓库（如 jcbowen/market，对应 ghcr.io/jcbowen/market）
//   - DOCKERHUB_API_BASE：Docker Hub API 基地址覆盖（网络受限环境指向兼容代理），
//     或直接设 HTTPS_PROXY 走代理（Go 默认 Transport 自动尊重该环境变量）
const (
	defaultRepo       = "jcbowen/market"
	dockerHubAPIBase  = "https://hub.docker.com/v2/repositories/"
	ghcrAPIBase       = "https://ghcr.io"
	updateHTTPTimeout = 5 * time.Second
	updateCacheTTL    = 6 * time.Hour    // 检测成功结果缓存时长
	updateFailTTL     = 10 * time.Minute // 检测失败结果缓存时长（避免每次进页都打外网）
)

// updateCache 更新检测结果进程内缓存（同一进程内共享，重启失效）
// updateFetch 为在途拉取单飞：外网请求移出缓存锁，慢源只阻塞真正等待结果的请求，
// 不让「刷新缓存」期间的读缓存请求排队，也避免并发同时打双源
var (
	updateCacheMu sync.Mutex
	updateCache   *updateCheckResult
	updateCacheAt time.Time

	updateFlightMu sync.Mutex
	updateFlight   *updateFetch
)

// updateFetch 一次在途的外网检测；结果落缓存后经 done 广播给等待方
type updateFetch struct {
	done   chan struct{}
	result *updateCheckResult
}

// updateCheckResult 更新检测结果
type updateCheckResult struct {
	Enabled       bool   // 是否启用检测（双源均显式关闭时为 false）
	HasUpdate     bool   // 是否存在更新（双源最新稳定版的较大者 > 当前编译版本）
	LatestVersion string // 双源中较新的稳定版本号（如 0.1.5）
	Error         string // 检测失败原因（至少一个源成功时为空）
}

// LatestVersion 返回更新检测结果（供关于页版本号旁提示）
// 双源并行查询 + 进程内缓存（成功 6 小时 / 失败 10 分钟），缓存过期后同一轮刷新只发一次外网请求；
// 外网不可达不影响本接口成功返回
// 使用示例：GET /api/v1/admin/about/updates（登录即可访问）
// 参数：
//   - c: gin 上下文
func (h *AboutHandler) LatestVersion(c *gin.Context) {
	result := checkLatestVersion()
	response.Success(c, gin.H{
		"enabled":        result.Enabled,
		"has_update":     result.HasUpdate,
		"latest_version": result.LatestVersion,
		"error":          result.Error,
	})
}

// checkLatestVersion 获取更新检测结果（带缓存 + 拉取单飞）
// 缓存命中直接返回；过期后只放一个请求去打外网，其余请求等同一次结果。
// 外网调用不在缓存锁内：慢源只拖住本轮刷新，不阻塞同时进来的读缓存请求，
// 也避免「关于」页并发请求各自发起双源查询（旧实现是全局锁串行 + 每请求都打外网）
// 返回：
//   - *updateCheckResult: 检测结果（永不返回 nil）
func checkLatestVersion() *updateCheckResult {
	if cached, ok := cachedUpdateResult(); ok {
		return cached
	}

	updateFlightMu.Lock()
	if f := updateFlight; f != nil {
		updateFlightMu.Unlock()
		<-f.done
		return f.result
	}
	flight := &updateFetch{done: make(chan struct{})}
	updateFlight = flight
	updateFlightMu.Unlock()

	flight.result = fetchLatestVersion()
	updateCacheMu.Lock()
	updateCache, updateCacheAt = flight.result, time.Now()
	updateCacheMu.Unlock()
	updateFlightMu.Lock()
	updateFlight = nil
	updateFlightMu.Unlock()
	close(flight.done)
	return flight.result
}

// cachedUpdateResult 读取未过期的缓存结果（锁内只做比较，不含任何外网调用）
// 失败结果按 updateFailTTL、成功结果按 updateCacheTTL 判定是否过期
// 返回：
//   - *updateCheckResult: 缓存命中时的结果
//   - bool: 是否命中
func cachedUpdateResult() (*updateCheckResult, bool) {
	updateCacheMu.Lock()
	defer updateCacheMu.Unlock()
	if updateCache == nil {
		return nil, false
	}
	ttl := updateCacheTTL
	if updateCache.Error != "" {
		ttl = updateFailTTL
	}
	if time.Since(updateCacheAt) >= ttl {
		return nil, false
	}
	return updateCache, true
}

// fetchLatestVersion 并行查询 Docker Hub 与 GHCR 并合并结果
// 合并规则：取所有成功源中语义化版本最大者与当前编译版本比较；
// 全部源失败时返回合并后的错误信息（不 panic、不阻塞页面主信息）
// 返回：
//   - *updateCheckResult: 检测结果
func fetchLatestVersion() *updateCheckResult {
	type source struct {
		name  string
		fetch func() (string, error)
	}
	var sources []source
	if repo := repoFromEnv("DOCKERHUB_REPO"); repo != "" {
		sources = append(sources, source{"Docker Hub", func() (string, error) {
			return queryDockerHubLatest(repo)
		}})
	}
	if repo := repoFromEnv("GHCR_REPO"); repo != "" {
		sources = append(sources, source{"GHCR", func() (string, error) {
			return queryGHCRLatest(repo)
		}})
	}
	if len(sources) == 0 {
		// 双源均被显式关闭（环境变量设为 off）
		return &updateCheckResult{}
	}

	// 并行查询，总耗时为最慢一源（互不阻塞）
	type srcOut struct {
		latest string
		err    error
	}
	outs := make([]srcOut, len(sources))
	var wg sync.WaitGroup
	for i, s := range sources {
		wg.Add(1)
		go func(i int, s source) {
			defer wg.Done()
			outs[i].latest, outs[i].err = s.fetch()
		}(i, s)
	}
	wg.Wait()

	// 合并：成功源中取语义化版本最大者
	best := ""
	var bestSem [3]int
	var errs []string
	for i, out := range outs {
		if out.err != nil {
			errs = append(errs, sources[i].name+": "+out.err.Error())
			continue
		}
		sem, ok := parseSemver(out.latest)
		if !ok {
			continue
		}
		if best == "" || semverGreater(sem, bestSem) {
			best, bestSem = out.latest, sem
		}
	}
	if best == "" {
		if len(errs) == 0 {
			errs = append(errs, "未发现可用版本号")
		}
		return &updateCheckResult{Enabled: true, Error: strings.Join(errs, "; ")}
	}

	result := &updateCheckResult{Enabled: true, LatestVersion: best}
	if current, ok := parseSemver(version.AppVersion); ok {
		if lv, ok := parseSemver(best); ok && semverGreater(lv, current) {
			result.HasUpdate = true
		}
	}
	return result
}

// repoFromEnv 读取仓库地址环境变量
// 未设置时使用内置默认仓库；显式设为 off/none/disable（不区分大小写）时返回空串
// 表示关闭该源；否则返回去除首尾空白与斜杠后的仓库路径
// 参数：
//   - key: 环境变量名（DOCKERHUB_REPO / GHCR_REPO）
//
// 返回：
//   - string: 仓库路径（空串表示该源关闭）
func repoFromEnv(key string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultRepo
	}
	switch strings.ToLower(v) {
	case "off", "none", "disable":
		return ""
	}
	return strings.Trim(v, "/ ")
}

// queryDockerHubLatest 请求 Docker Hub Registry API 获取仓库最新稳定版本
// API 基地址默认官方（DOCKERHUB_API_BASE 可覆盖），取第一页 tags
// （page_size=100，按最近更新排序，发布频率低时足够覆盖全部版本）
// 参数：
//   - repo: Docker Hub 仓库路径（如 jcbowen/market）
//
// 返回：
//   - string: 最新稳定版本号（如 0.1.5），无可用版本时为空串
//   - error: 网络请求/解析失败原因
func queryDockerHubLatest(repo string) (string, error) {
	client := &http.Client{Timeout: updateHTTPTimeout}
	base := strings.TrimRight(os.Getenv("DOCKERHUB_API_BASE"), "/ ")
	if base == "" {
		base = dockerHubAPIBase
	}
	url := base + "/" + repo + "/tags?page_size=100"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "market-version-check")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("返回状态码 %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	var tagsResp struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &tagsResp); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	latest := ""
	var latestSem [3]int
	for _, tag := range tagsResp.Results {
		sem, ok := parseSemver(tag.Name)
		if !ok {
			continue
		}
		if latest == "" || semverGreater(sem, latestSem) {
			latest, latestSem = tag.Name, sem
		}
	}
	return latest, nil
}

// queryGHCRLatest 请求 GitHub Container Registry 获取仓库最新稳定版本
// GHCR 兼容 Docker Registry v2 API，公开包支持匿名 token：
// 先请求 /token 换取匿名拉取 token，再携带 Bearer 请求 /v2/<repo>/tags/list，
// 遍历 tag 列表（无排序保证，全量解析）取语义化版本最大者
// 参数：
//   - repo: GHCR 仓库路径（如 jcbowen/market，对应 ghcr.io/jcbowen/market）
//
// 返回：
//   - string: 最新稳定版本号（如 0.1.5），无可用版本时为空串
//   - error: 网络请求/解析失败原因
func queryGHCRLatest(repo string) (string, error) {
	client := &http.Client{Timeout: updateHTTPTimeout}

	// 第一步：匿名拉取 token
	tokenURL := ghcrAPIBase + "/token?service=ghcr.io&scope=repository:" + repo + ":pull"
	tokenReq, err := http.NewRequest(http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("构造 token 请求失败: %w", err)
	}
	tokenReq.Header.Set("User-Agent", "market-version-check")
	tokenResp, err := client.Do(tokenReq)
	if err != nil {
		return "", fmt.Errorf("请求 token 失败: %w", err)
	}
	defer tokenResp.Body.Close()
	if tokenResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token 接口返回状态码 %d", tokenResp.StatusCode)
	}
	var tokenBody struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(tokenResp.Body, 1<<20)).Decode(&tokenBody); err != nil {
		return "", fmt.Errorf("解析 token 失败: %w", err)
	}
	if tokenBody.Token == "" {
		return "", fmt.Errorf("token 为空")
	}

	// 第二步：携带 token 拉取 tags 列表
	tagsURL := ghcrAPIBase + "/v2/" + repo + "/tags/list"
	tagsReq, err := http.NewRequest(http.MethodGet, tagsURL, nil)
	if err != nil {
		return "", fmt.Errorf("构造 tags 请求失败: %w", err)
	}
	tagsReq.Header.Set("User-Agent", "market-version-check")
	tagsReq.Header.Set("Authorization", "Bearer "+tokenBody.Token)
	tagsResp, err := client.Do(tagsReq)
	if err != nil {
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer tagsResp.Body.Close()
	if tagsResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("返回状态码 %d", tagsResp.StatusCode)
	}
	var tagsBody struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(io.LimitReader(tagsResp.Body, 4<<20)).Decode(&tagsBody); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	latest := ""
	var latestSem [3]int
	for _, tag := range tagsBody.Tags {
		sem, ok := parseSemver(tag)
		if !ok {
			continue
		}
		if latest == "" || semverGreater(sem, latestSem) {
			latest, latestSem = tag, sem
		}
	}
	return latest, nil
}

// parseSemver 解析三段语义化版本号
// 容忍 v/V 前缀（v0.1.4 / 0.1.4 均可）；含 - 的预发布版本（如 0.2.0-rc1）
// 或非三段纯数字的 tag（latest、dev、日期戳等）一律视为不可解析
// 参数：
//   - s: 待解析的版本字符串
//
// 返回：
//   - [3]int: major/minor/patch 数组（解析失败时无意义）
//   - bool: 是否解析成功
func parseSemver(s string) ([3]int, bool) {
	var sem [3]int
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if s == "" || strings.Contains(s, "-") || strings.Contains(s, "+") {
		return sem, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return sem, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return sem, false
		}
		sem[i] = n
	}
	return sem, true
}

// semverGreater 比较 a 是否严格大于 b（逐段比较 major/minor/patch）
// 参数：
//   - a: 待比较的版本数组
//   - b: 被比较的版本数组
//
// 返回：
//   - bool: a > b 时为 true（相等返回 false）
func semverGreater(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
