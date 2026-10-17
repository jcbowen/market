package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// 民政部官方源（国家地名信息库 dmfw.mca.gov.cn）
// 仅作兜底修正使用：日常同步仍走 fetchRemoteRegions 的镜像多源方案。
// 官方接口响应较慢且偶发失败，所有请求统一带重试（见 officialRequestWithRetry）
const (
	regionOfficialBaseURL = "https://dmfw.mca.gov.cn"

	// getList 省级入口：code=0 从根查起，maxLevel=3 一次返回完整三级树
	// （实测 34 省 / 333 市 / 2847 区县，约 245KB，单请求即可完成全量拉取）
	regionOfficialListPath = "/xzqh/getList"
)

// 官方源拉取超时与重试参数（单请求重试 3 次，指数退避：2s → 4s）
const (
	regionOfficialHTTPTimeout    = 60 * time.Second
	regionOfficialRetryTimes     = 3
	regionOfficialRetryBaseDelay = 2 * time.Second
)

// officialListNode getList 响应节点（根节点 code="00"、level=0，实际区划从 children 开始）
type officialListNode struct {
	Code     string              `json:"code"`
	Name     string              `json:"name"`
	Level    int32               `json:"level"`
	Type     string              `json:"type"`
	Children []*officialListNode `json:"children"`
}

// fetchOfficialRegions 拉取民政部官方完整三级区划树
// 使用 getList 单请求拉全量（code=0 & maxLevel=3），失败自动重试
// 返回：
//   - []*remoteRegionNode: 区划树（结构与镜像源一致，可直接进入 diffAndStore 比对）
//   - string: 实际请求的数据源 URL
//   - error: 重试耗尽后的错误信息
func fetchOfficialRegions() ([]*remoteRegionNode, string, error) {
	listURL := fmt.Sprintf("%s%s?code=0&trimCode=true&maxLevel=3&_=%d",
		regionOfficialBaseURL, regionOfficialListPath, time.Now().UnixMilli())

	var resp struct {
		Data *officialListNode `json:"data"`
	}
	if err := officialRequestWithRetry(http.MethodGet, listURL, "", &resp); err != nil {
		return nil, "", err
	}
	if resp.Data == nil || len(resp.Data.Children) == 0 {
		return nil, "", fmt.Errorf("官方源数据为空")
	}

	nodes := officialListToRemote(resp.Data.Children)
	// 数据完整性兜底：省份数量明显异常时视为拉取失败，避免残缺数据进入比对
	if len(nodes) < 20 {
		return nil, "", fmt.Errorf("官方源数据异常：仅 %d 个省级节点", len(nodes))
	}
	return nodes, listURL, nil
}

// officialListToRemote 将官方 getList 树转换为通用远端节点树
// 参数：
//   - nodes: 官方树节点（省级起，code 已为 2/4/6 位有效码）
//
// 返回：
//   - []*remoteRegionNode: 与镜像源 pca-code.json 同构的节点树
func officialListToRemote(nodes []*officialListNode) []*remoteRegionNode {
	if len(nodes) == 0 {
		return nil
	}
	result := make([]*remoteRegionNode, 0, len(nodes))
	for _, n := range nodes {
		children := officialListToRemote(n.Children)
		result = append(result, &remoteRegionNode{
			Code:     n.Code,
			Name:     n.Name,
			Children: children,
		})
	}
	return result
}

// officialRequestWithRetry 官方接口请求重试封装
// 共尝试 regionOfficialRetryTimes 次，失败按指数退避（2s → 4s）后重试；
// 官方站有 WAF，请求头需模拟浏览器，否则可能被拦截（错误信息中已提示）
// 参数：
//   - method: HTTP 方法
//   - reqURL: 请求地址
//   - body: 请求体（GET 为空串；POST 为 url-encoded 表单）
//   - out: 响应 JSON 解析目标
//
// 返回：
//   - error: 全部重试失败后的错误信息
func officialRequestWithRetry(method, reqURL, body string, out interface{}) error {
	client := &http.Client{Timeout: regionOfficialHTTPTimeout}
	var lastErr error
	for attempt := 0; attempt < regionOfficialRetryTimes; attempt++ {
		if attempt > 0 {
			time.Sleep(regionOfficialRetryBaseDelay << (attempt - 1))
		}
		lastErr = officialDoRequest(client, method, reqURL, body, out)
		if lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("官方源请求失败（已重试 %d 次）: %w", regionOfficialRetryTimes, lastErr)
}

// officialDoRequest 执行单次官方接口请求并解析响应
func officialDoRequest(client *http.Client, method, reqURL, body string, out interface{}) error {
	var req *http.Request
	var err error
	if body != "" {
		req, err = http.NewRequest(method, reqURL, bytes.NewBufferString(body))
	} else {
		req, err = http.NewRequest(method, reqURL, nil)
	}
	if err != nil {
		return err
	}

	// 官方站校验请求头（缺失易触发 WAF 拦截），按浏览器 XHR 特征补齐
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/155.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", regionOfficialBaseURL+"/XzqhVersionPublish.html")
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d（若为 403/405 可能被 WAF 拦截，请检查请求头）", resp.StatusCode)
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("解析 JSON 失败: %w", err)
	}
	return nil
}
