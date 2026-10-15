package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/response"
)

/**
 * 收银台交班体系 + 应用偏好（收银台独立应用配套）
 * - 交班：开工（StartShift）/ 当前班次（GetCurrentShift）/ 交班（EndShift）/ 记录（ListShifts）
 *   交班记录默认仅看自己的（operator_id=当前管理员）；有 order:view 权限可传 operator_id=0 查本店全部
 * - 应用偏好：应用页「常用应用」，社区级单行覆盖式保存（登录即可读写，应用页对所有管理员可见）
 */

// StartShift 开工：创建进行中班次
//
// 接口: POST /api/v1/admin/pos/shifts/start
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) PosStartShift(c *gin.Context) {
	var req struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&req)
	uc := appcontext.GetUserContext(c)

	resp, err := h.orderClient.StartShift(c.Request.Context(), &order.StartShiftReq{
		CommunityId:  h.getAdminCommunityID(c),
		OperatorId:   uc.UserID,
		OperatorName: c.GetString("admin_username"),
		Note:         req.Note,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// PosGetCurrentShift 当前进行中班次（含实时统计；id=0 表示无进行中班次）
//
// 接口: GET /api/v1/admin/pos/shifts/current
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) PosGetCurrentShift(c *gin.Context) {
	uc := appcontext.GetUserContext(c)
	resp, err := h.orderClient.GetCurrentShift(c.Request.Context(), &order.GetCurrentShiftReq{
		CommunityId: h.getAdminCommunityID(c),
		OperatorId:  uc.UserID,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// PosEndShift 交班：生成统计快照 → 置已结束 → 返回快照
//
// 接口: POST /api/v1/admin/pos/shifts/end
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) PosEndShift(c *gin.Context) {
	var req struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&req)
	uc := appcontext.GetUserContext(c)

	resp, err := h.orderClient.EndShift(c.Request.Context(), &order.EndShiftReq{
		CommunityId: h.getAdminCommunityID(c),
		OperatorId:  uc.UserID,
		Note:        req.Note,
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, resp)
}

// PosListShiftsMine 交班记录分页——仅看自己的班次（order:create 即可，收银员默认入口）
//
// 接口: GET /api/v1/admin/pos/shifts/mine
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) PosListShiftsMine(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	operatorID := appcontext.GetUserContext(c).UserID
	resp, err := h.listShifts(c, operatorID, page, pageSize)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, int(resp.Pagination.Total), page, pageSize)
}

// PosListShifts 交班记录分页——本店全部班次（order:view 权限，可切「全部」视角）
//
// 接口: GET /api/v1/admin/pos/shifts?operator_id=0
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) PosListShifts(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	// 默认本店全部；显式传 operator_id>0 时可过滤指定操作员
	operatorID, _ := strconv.ParseInt(c.DefaultQuery("operator_id", "0"), 10, 64)
	resp, err := h.listShifts(c, operatorID, page, pageSize)
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.SuccessWithPage(c, resp.List, int(resp.Pagination.Total), page, pageSize)
}

// listShifts 交班记录分页查询（归属范围由 operatorID 决定：0=本店全部，>0=指定操作员）
// 参数：
//   - c: gin 上下文
//   - operatorID: 操作员过滤（0=全部）
//   - page: 页码
//   - pageSize: 每页条数
//
// 返回值：
//   - *order.ListShiftsResp: 分页响应
//   - error: 错误信息
func (h *AdminHandler) listShifts(c *gin.Context, operatorID int64, page, pageSize int) (*order.ListShiftsResp, error) {
	return h.orderClient.ListShifts(c.Request.Context(), &order.ListShiftsReq{
		CommunityId: h.getAdminCommunityID(c),
		OperatorId:  operatorID,
		Pagination: &common.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
		// 开班时间下限（近7天/近30天筛选），空=不过滤
		StartTime: c.Query("start_time"),
	})
}

// parsePageParams 解析分页参数（page/page_size，缺省 1/10）
// 参数：
//   - c: gin 上下文
//
// 返回值：
//   - int: 页码
//   - int: 每页条数
func parsePageParams(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 10
	}
	return page, pageSize
}

// GetAppPreferences 获取社区常用应用（应用页「常用」，社区级共享）
//
// 接口: GET /api/v1/admin/app-preferences
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetAppPreferences(c *gin.Context) {
	resp, err := h.orderClient.GetAppPreferences(c.Request.Context(), &order.GetAppPreferencesReq{
		CommunityId: h.getAdminCommunityID(c),
	})
	if err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"app_keys": resp.AppKeys})
}

// SaveAppPreferences 保存社区常用应用（覆盖式）
//
// 接口: PUT /api/v1/admin/app-preferences
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) SaveAppPreferences(c *gin.Context) {
	var req struct {
		AppKeys []string `json:"app_keys"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, errcode.ParamError)
		return
	}
	if _, err := h.orderClient.SaveAppPreferences(c.Request.Context(), &order.SaveAppPreferencesReq{
		CommunityId: h.getAdminCommunityID(c),
		AppKeys:     req.AppKeys,
	}); err != nil {
		response.GRPCError(c, err)
		return
	}
	response.Success(c, gin.H{"app_keys": req.AppKeys})
}
