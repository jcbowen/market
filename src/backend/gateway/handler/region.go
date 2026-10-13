package handler

import (
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/geoip"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/common/response"
)

// RegionHandler 行政区划接口处理器
// 公开接口供前端区划选择器逐级懒加载与 IP 定位反查区划；
// 管理接口供后台区划管理页（列表/检查更新/差异确认覆盖）
type RegionHandler struct {
	userClient user.UserServiceClient
	// GeoLite2-City mmdb 数据库路径（相对路径以可执行文件所在目录为基准）
	geoipDBPath string
}

// NewRegionHandler 创建行政区划处理器
// 参数：
//   - userClient: user-service gRPC 客户端
//   - geoipDBPath: GeoLite2-City 数据库路径
//
// 返回：
//   - *RegionHandler: 处理器实例
func NewRegionHandler(userClient user.UserServiceClient, geoipDBPath string) *RegionHandler {
	return &RegionHandler{userClient: userClient, geoipDBPath: geoipDBPath}
}

// regionItem 区划视图对象
type regionItem struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	ParentCode string `json:"parent_code"`
	Level      int32  `json:"level"`
	Status     int32  `json:"status"`
}

// toRegionItem 将 proto 区划信息转换为前端视图对象
func toRegionItem(r *user.RegionInfo) regionItem {
	if r == nil {
		return regionItem{}
	}
	return regionItem{
		Code:       r.Code,
		Name:       r.Name,
		ParentCode: r.ParentCode,
		Level:      r.Level,
		Status:     r.Status,
	}
}

// toRegionItemPtr 区划视图对象指针（定位结果可能为空，需要区分 null）
func toRegionItemPtr(r *user.RegionInfo) *regionItem {
	if r == nil || r.Code == "" {
		return nil
	}
	item := toRegionItem(r)
	return &item
}

// ListChildren 按父级代码查询下级区划（公开接口）
// 使用示例：GET /api/v1/regions/children?parent_code=0（省级）
// 参数：
//   - c: gin 上下文
func (h *RegionHandler) ListChildren(c *gin.Context) {
	resp, err := h.userClient.ListRegionChildren(c.Request.Context(), &user.ListRegionChildrenReq{
		ParentCode: c.DefaultQuery("parent_code", "0"),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	list := make([]regionItem, 0, len(resp.List))
	for _, r := range resp.List {
		list = append(list, toRegionItem(r))
	}
	response.Success(c, list)
}

// Locate 按客户端出口 IP 定位并反查行政区划（公开接口）
// 流程：GeoLite2-City 解析城市/省份名称 → 区划表按名称匹配出区划代码。
// 精度为城市级：命中市级时同时返回上级省份；区县需用户在地址选择器中补选。
// 数据库未就绪或未命中时各字段返回 null，调用方自行降级
// 使用示例：GET /api/v1/regions/locate
// 参数：
//   - c: gin 上下文
func (h *RegionHandler) Locate(c *gin.Context) {
	ipStr := c.ClientIP()
	if ipStr == "" {
		if host, _, err := net.SplitHostPort(c.Request.RemoteAddr); err == nil {
			ipStr = host
		}
	}

	addr, err := netip.ParseAddr(strings.Trim(ipStr, "[]"))
	if err != nil {
		response.Success(c, gin.H{"province": nil, "city": nil})
		return
	}

	loc := geoip.Lookup(h.geoipDBPath, addr)
	if loc == nil {
		response.Success(c, gin.H{"longitude": 0, "latitude": 0, "province": nil, "city": nil})
		return
	}

	// 依次尝试城市名、省份名匹配区划（MatchRegionByName 内部已先市后省）
	var matched *user.RegionMatchInfo
	if loc.City != "" {
		matched, err = h.userClient.MatchRegionByName(c.Request.Context(), &user.MatchRegionByNameReq{Name: loc.City})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
	}
	if matched != nil && matched.City == nil && matched.Province == nil && loc.Province != "" {
		matched, err = h.userClient.MatchRegionByName(c.Request.Context(), &user.MatchRegionByNameReq{Name: loc.Province})
		if err != nil {
			response.GRPCError(c, err)
			return
		}
	}

	if matched == nil || (matched.City == nil && matched.Province == nil) {
		response.Success(c, gin.H{"longitude": loc.Longitude, "latitude": loc.Latitude, "province": nil, "city": nil})
		return
	}

	response.Success(c, gin.H{
		"longitude": loc.Longitude,
		"latitude":  loc.Latitude,
		"province":  toRegionItemPtr(matched.Province),
		"city":      toRegionItemPtr(matched.City),
	})
}

// ListRegions 管理端分页查询区划列表
// 使用示例：GET /api/v1/admin/regions?keyword=&level=&status=&page=&page_size=
// 参数：
//   - c: gin 上下文
func (h *RegionHandler) ListRegions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	level, _ := strconv.Atoi(c.DefaultQuery("level", "0"))
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))

	resp, err := h.userClient.ListRegions(c.Request.Context(), &user.ListRegionsReq{
		Keyword:    c.Query("keyword"),
		ParentCode: c.Query("parent_code"),
		Level:      int32(level),
		Status:     int32(status),
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	list := make([]regionItem, 0, len(resp.List))
	for _, r := range resp.List {
		list = append(list, toRegionItem(r))
	}
	response.SuccessWithPage(c, list, int(resp.Pagination.Total), page, pageSize)
}

// CreateUpdateTask 创建区划更新检查任务（管理端）
// source_type 指定数据源：default 常规多源镜像（默认）/ official 民政部官方源（兜底修正，较慢）
// 返回新建（或已存在的进行中）任务，前端据 id 轮询任务详情展示差异
// 使用示例：POST /api/v1/admin/regions/update/check?source_type=official
// 参数：
//   - c: gin 上下文
func (h *RegionHandler) CreateUpdateTask(c *gin.Context) {
	resp, err := h.userClient.CreateRegionUpdateTask(c.Request.Context(), &user.CreateRegionUpdateTaskReq{
		// 与 service 层 RegionSourceType* 常量对应，网关层不引入 service 依赖故用字面量
		SourceType: c.DefaultQuery("source_type", "default"),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// GetUpdateTask 查询区划更新任务详情（含差异明细，轮询用）
// 使用示例：GET /api/v1/admin/regions/update/tasks/:id
// 参数：
//   - c: gin 上下文
func (h *RegionHandler) GetUpdateTask(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	resp, err := h.userClient.GetRegionUpdateTask(c.Request.Context(), &user.GetRegionUpdateTaskReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// ApplyUpdateTask 将已完成任务的差异覆盖入库（管理端）
// 使用示例：POST /api/v1/admin/regions/update/tasks/:id/apply
// 参数：
//   - c: gin 上下文
func (h *RegionHandler) ApplyUpdateTask(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, errcode.ParamError)
		return
	}
	_, err = h.userClient.ApplyRegionUpdateTask(c.Request.Context(), &user.ApplyRegionUpdateTaskReq{Id: id})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}
