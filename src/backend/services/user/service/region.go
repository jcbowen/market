package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

// 行政区划上游数据源：FHZDCJ/administrative-division-code-of-China
// （LionixQ/administrative-division-code-of-China 的 GitHub 镜像，另有 Gitee 镜像）
// 数据来自民政部国家地名信息库，每年 1 月更新至上一年度末，省/市/区县三级
// 注意：该源直辖市下直接挂区县（如 11 → 110101），无「市辖区」中间层，
// 且不含港澳台（其区划代码尚未公布）
const regionDataSource = "https://cdn.jsdelivr.net/gh/FHZDCJ/administrative-division-code-of-China@main/Dist/pca-code.json"

var regionDataSourceFallbacks = []string{
	"https://fastly.jsdelivr.net/gh/FHZDCJ/administrative-division-code-of-China@main/Dist/pca-code.json",
	"https://gitee.com/FHZDCJ/administrative-division-code-of-china/raw/main/Dist/pca-code.json",
	"https://raw.githubusercontent.com/FHZDCJ/administrative-division-code-of-China/main/Dist/pca-code.json",
}

// regionHTTPTimeout 拉取上游数据的超时时间
const regionHTTPTimeout = 30 * time.Second

// RegionDiffItem 更新任务差异条目（JSON 存储于任务表）
type RegionDiffItem struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	NewName    string `json:"new_name,omitempty"`
	ParentCode string `json:"parent_code"`
	Level      int32  `json:"level"`
}

// remoteRegionNode 上游数据节点（pca-code.json 结构）
type remoteRegionNode struct {
	Code     string              `json:"code"`
	Name     string              `json:"name"`
	Children []*remoteRegionNode `json:"children"`
}

// RegionService 行政区划业务服务
type RegionService struct {
	db *mysql.Instance
}

// NewRegionService 创建行政区划业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回：
//   - *RegionService: 行政区划服务实例
func NewRegionService(db *mysql.Instance) *RegionService {
	return &RegionService{db: db}
}

// ListChildren 按父级代码查询下级区划（仅正常状态，供级联选择器逐级懒加载）
// 参数：
//   - ctx: 上下文
//   - parentCode: 父级区划代码，"0" 表示省级
//
// 返回：
//   - []*model.Region: 下级区划列表
//   - error: 错误信息
func (s *RegionService) ListChildren(ctx context.Context, parentCode string) ([]*model.Region, error) {
	if parentCode == "" {
		parentCode = "0"
	}
	var list []*model.Region
	err := s.db.GetDb().WithContext(ctx).
		Where("parent_code = ? AND status = ?", parentCode, model.RegionStatusActive).
		Order("code asc").
		Find(&list).Error
	return list, err
}

// ListRegions 管理端分页查询区划列表
// 参数：
//   - ctx: 上下文
//   - req: 查询请求（关键词/父级/层级/状态筛选 + 分页）
//
// 返回：
//   - []*model.Region: 区划列表
//   - int: 总条数
//   - error: 错误信息
func (s *RegionService) ListRegions(ctx context.Context, req *user.ListRegionsReq) ([]*model.Region, int, error) {
	page, pageSize := 1, 20
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.Region{})
	if kw := strings.TrimSpace(req.Keyword); kw != "" {
		kw = strings.TrimSuffix(kw, "%")
		like := "%" + kw + "%"
		db = db.Where("code LIKE ? OR name LIKE ?", like, like)
	}
	if req.ParentCode != "" {
		db = db.Where("parent_code = ?", req.ParentCode)
	}
	if req.Level > 0 {
		db = db.Where("level = ?", req.Level)
	}
	if req.Status > 0 {
		db = db.Where("status = ?", req.Status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*model.Region
	err := db.Order("code asc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error
	return list, int(total), err
}

// MatchRegionByName 按名称匹配区划（供 IP 定位反查区划代码）
// 匹配策略：先市后省；依次尝试精确匹配、补"市"/"省"后缀、前缀模糊。
// 命中市时同时返回其上级省份
// 参数：
//   - ctx: 上下文
//   - name: 地理库返回的名称，如 "广州"、"广东"
//
// 返回：
//   - *model.Region: 命中的市级区划（未命中为 nil）
//   - *model.Region: 命中市级时的上级省份（未命中市时为命中的省级区划）
//   - error: 错误信息
func (s *RegionService) MatchRegionByName(ctx context.Context, name string) (*model.Region, *model.Region, error) {
	kw := strings.TrimSpace(name)
	if kw == "" {
		return nil, nil, nil
	}

	candidates := []string{kw, kw + "市"}
	city, err := s.matchRegionByLevel(ctx, model.RegionLevelCity, candidates)
	if err != nil {
		return nil, nil, err
	}
	if city != nil {
		var province *model.Region
		if city.ParentCode != "" && city.ParentCode != "0" {
			province, err = s.getRegionByCode(ctx, city.ParentCode)
			if err != nil {
				return nil, nil, err
			}
		}
		return city, province, nil
	}

	province, err := s.matchRegionByLevel(ctx, model.RegionLevelProvince, []string{kw, kw + "省"})
	if err != nil {
		return nil, nil, err
	}
	return nil, province, nil
}

// matchRegionByLevel 按层级依次尝试候选名称匹配（精确 → 前缀模糊）
func (s *RegionService) matchRegionByLevel(ctx context.Context, level int32, candidates []string) (*model.Region, error) {
	for _, kw := range candidates {
		if kw == "" {
			continue
		}
		var r model.Region
		err := s.db.GetDb().WithContext(ctx).
			Where("level = ? AND status = ? AND name = ?", level, model.RegionStatusActive, kw).
			First(&r).Error
		if err == nil {
			return &r, nil
		}
		if err != gorm.ErrRecordNotFound {
			return nil, err
		}
	}
	// 兜底：前缀模糊（如 GeoLite2 返回 "广州"、库内为 "广州市" 已由补后缀覆盖；
	// 返回 "呼和浩特" 之类无法补后缀的场景走前缀）
	kw := strings.TrimSpace(candidates[0])
	if kw == "" {
		return nil, nil
	}
	var r model.Region
	err := s.db.GetDb().WithContext(ctx).
		Where("level = ? AND status = ? AND name LIKE ?", level, model.RegionStatusActive, kw+"%").
		Order("code asc").
		First(&r).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// getRegionByCode 按代码查询区划
func (s *RegionService) getRegionByCode(ctx context.Context, code string) (*model.Region, error) {
	var r model.Region
	err := s.db.GetDb().WithContext(ctx).Where("code = ?", code).First(&r).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// CreateRegionUpdateTask 创建区划更新任务
// 同一时刻仅允许一个进行中的任务（待执行/拉取中/比对中）；
// 已存在进行中任务时直接返回该任务（幂等），避免重复拉取
// 参数：
//   - ctx: 上下文
//
// 返回：
//   - *model.RegionUpdateTask: 新建或进行中的任务
//   - error: 错误信息
func (s *RegionService) CreateRegionUpdateTask(ctx context.Context) (*model.RegionUpdateTask, error) {
	var existing model.RegionUpdateTask
	err := s.db.GetDb().WithContext(ctx).
		Where("status IN (?)", []int32{model.RegionTaskStatusPending, model.RegionTaskStatusFetching, model.RegionTaskStatusDiffing}).
		Order("id desc").
		First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	task := &model.RegionUpdateTask{
		Status: model.RegionTaskStatusPending,
		Source: regionDataSource,
	}
	if err := s.db.GetDb().WithContext(ctx).Create(task).Error; err != nil {
		return nil, err
	}

	// 异步执行拉取与比对：任务表自身记录状态，前端轮询任务详情
	go s.runUpdateTask(task.ID)
	return task, nil
}

// GetRegionUpdateTask 查询更新任务详情（含差异明细）
// 参数：
//   - ctx: 上下文
//   - id: 任务 ID
//
// 返回：
//   - *model.RegionUpdateTask: 任务
//   - error: 错误信息
func (s *RegionService) GetRegionUpdateTask(ctx context.Context, id int64) (*model.RegionUpdateTask, error) {
	var task model.RegionUpdateTask
	err := s.db.GetDb().WithContext(ctx).First(&task, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("任务不存在")
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// ApplyRegionUpdateTask 将已完成任务的差异覆盖入库
// 事务内执行：新增直接插入；更名更新名称；停用软标记（不做物理删除，
// 避免业务表已引用的区划代码悬空）；全程幂等（applied 标记防重复执行）
// 参数：
//   - ctx: 上下文
//   - id: 任务 ID
//
// 返回：
//   - error: 错误信息
func (s *RegionService) ApplyRegionUpdateTask(ctx context.Context, id int64) error {
	task, err := s.GetRegionUpdateTask(ctx, id)
	if err != nil {
		return err
	}
	if task.Status != model.RegionTaskStatusDone {
		return fmt.Errorf("任务尚未完成，无法应用差异")
	}
	if task.Applied {
		return fmt.Errorf("任务差异已应用过，请重新执行检查更新")
	}

	var added, updated, removed []RegionDiffItem
	if err := json.Unmarshal([]byte(task.AddedJSON), &added); err != nil && task.AddedJSON != "" {
		return fmt.Errorf("解析新增差异数据失败: %w", err)
	}
	if err := json.Unmarshal([]byte(task.UpdatedJSON), &updated); err != nil && task.UpdatedJSON != "" {
		return fmt.Errorf("解析更名差异数据失败: %w", err)
	}
	if err := json.Unmarshal([]byte(task.RemovedJSON), &removed); err != nil && task.RemovedJSON != "" {
		return fmt.Errorf("解析停用差异数据失败: %w", err)
	}

	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(added) > 0 {
			// 批量插入时 base 的 BeforeCreate 钩子不生效（Dest 为 slice），
			// 显式赋值避免 created_at/updated_at 零值空串写入 datetime 报 1292
			now := time.Now().Format("2006-01-02 15:04:05")
			rows := make([]*model.Region, 0, len(added))
			for _, item := range added {
				rows = append(rows, &model.Region{
					Code:       item.Code,
					Name:       item.Name,
					ParentCode: item.ParentCode,
					Level:      item.Level,
					Status:     model.RegionStatusActive,
					CreatedAt:  now,
					UpdatedAt:  now,
				})
			}
			if err := tx.Create(&rows).Error; err != nil {
				return fmt.Errorf("写入新增区划失败: %w", err)
			}
		}
		for _, item := range updated {
			updates := map[string]interface{}{
				"name":        item.NewName,
				"parent_code": item.ParentCode,
				"level":       item.Level,
				"status":      model.RegionStatusActive,
			}
			if err := tx.Model(&model.Region{}).Where("code = ?", item.Code).Updates(updates).Error; err != nil {
				return fmt.Errorf("更新区划 %s 失败: %w", item.Code, err)
			}
		}
		if len(removed) > 0 {
			codes := make([]string, 0, len(removed))
			for _, item := range removed {
				codes = append(codes, item.Code)
			}
			if err := tx.Model(&model.Region{}).Where("code IN ?", codes).
				Update("status", model.RegionStatusDisabled).Error; err != nil {
				return fmt.Errorf("停用区划失败: %w", err)
			}
		}
		return tx.Model(&model.RegionUpdateTask{}).Where("id = ? AND applied = 0", id).
			Update("applied", true).Error
	})
}

// runUpdateTask 执行更新任务：拉取上游数据 → 与库内比对 → 差异写回任务表
// 状态流转：待执行 → 拉取中 → 比对中 → 完成/失败
// 参数：
//   - taskID: 任务 ID
func (s *RegionService) runUpdateTask(taskID int64) {
	fail := func(err error) {
		_ = s.db.GetDb().Model(&model.RegionUpdateTask{}).Where("id = ?", taskID).Updates(map[string]interface{}{
			"status":      model.RegionTaskStatusFailed,
			"error":       truncateRegionError(err.Error()),
			"finished_at": time.Now().Format("2006-01-02 15:04:05"),
		}).Error
	}

	// 比对中：锁定任务为执行状态，防止期间被重复应用
	if err := s.db.GetDb().Model(&model.RegionUpdateTask{}).Where("id = ?", taskID).
		Update("status", model.RegionTaskStatusFetching).Error; err != nil {
		fail(err)
		return
	}

	nodes, source, err := fetchRemoteRegions()
	if err != nil {
		fail(err)
		return
	}
	if err := s.db.GetDb().Model(&model.RegionUpdateTask{}).Where("id = ?", taskID).
		Updates(map[string]interface{}{"status": model.RegionTaskStatusDiffing, "source": source}).Error; err != nil {
		fail(err)
		return
	}

	if err := s.diffAndStore(taskID, nodes); err != nil {
		fail(err)
	}
}

// diffAndStore 与库内全量比对并写回差异结果
// 参数：
//   - taskID: 任务 ID
//   - nodes: 上游区划树
//
// 返回：
//   - error: 错误信息
func (s *RegionService) diffAndStore(taskID int64, nodes []*remoteRegionNode) error {
	// 展平上游数据：code → item
	// 层级不按树深度推断（该源直辖市下直接挂区县），而是按代码位数：
	// 2 位省级 / 4 位地级 / 6 位县级；parent_code 直接取树上的父节点
	remote := make(map[string]RegionDiffItem, 4000)
	var walk func(items []*remoteRegionNode, parentCode string) error
	walk = func(items []*remoteRegionNode, parentCode string) error {
		for _, n := range items {
			code := normalizeRegionCode(n.Code)
			level := regionLevelByCodeLen(code)
			if code == "" || n.Name == "" || level == 0 {
				continue
			}
			remote[code] = RegionDiffItem{
				Code:       code,
				Name:       n.Name,
				ParentCode: parentCode,
				Level:      level,
			}
			if len(n.Children) > 0 {
				if err := walk(n.Children, code); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(nodes, "0"); err != nil {
		return err
	}

	var local []*model.Region
	if err := s.db.GetDb().Find(&local).Error; err != nil {
		return err
	}
	localMap := make(map[string]*model.Region, len(local))
	for _, r := range local {
		localMap[r.Code] = r
	}

	var added, updated, removed []RegionDiffItem
	for code, item := range remote {
		if _, ok := localMap[code]; !ok {
			added = append(added, item)
			continue
		}
		lr := localMap[code]
		if lr.Name != item.Name || lr.ParentCode != item.ParentCode || lr.Level != item.Level {
			updated = append(updated, RegionDiffItem{
				Code:       code,
				Name:       lr.Name,
				NewName:    item.Name,
				ParentCode: item.ParentCode,
				Level:      item.Level,
			})
		}
	}
	for code, lr := range localMap {
		// 停用区划不重复计入停用差异；仅比对正常状态区划是否仍存在于上游
		if _, ok := remote[code]; !ok && lr.Status == model.RegionStatusActive {
			removed = append(removed, RegionDiffItem{
				Code:       code,
				Name:       lr.Name,
				ParentCode: lr.ParentCode,
				Level:      lr.Level,
			})
		}
	}

	addedJSON, _ := json.Marshal(added)
	updatedJSON, _ := json.Marshal(updated)
	removedJSON, _ := json.Marshal(removed)

	return s.db.GetDb().Model(&model.RegionUpdateTask{}).Where("id = ?", taskID).Updates(map[string]interface{}{
		"status":        model.RegionTaskStatusDone,
		"total_remote":  len(remote),
		"total_local":   len(local),
		"added_count":   len(added),
		"updated_count": len(updated),
		"removed_count": len(removed),
		"added_json":    string(addedJSON),
		"updated_json":  string(updatedJSON),
		"removed_json":  string(removedJSON),
		"finished_at":   time.Now().Format("2006-01-02 15:04:05"),
	}).Error
}

// fetchRemoteRegions 依次尝试多源拉取上游区划 JSON
// 返回：
//   - []*remoteRegionNode: 区划树
//   - string: 实际命中的数据源 URL
//   - error: 全部数据源失败时的错误信息
func fetchRemoteRegions() ([]*remoteRegionNode, string, error) {
	urls := append([]string{regionDataSource}, regionDataSourceFallbacks...)
	client := &http.Client{Timeout: regionHTTPTimeout}
	var lastErr error
	for _, u := range urls {
		nodes, err := fetchRegionURL(client, u)
		if err == nil {
			return nodes, u, nil
		}
		lastErr = fmt.Errorf("%s: %w", u, err)
	}
	return nil, "", fmt.Errorf("全部数据源拉取失败: %w", lastErr)
}

// fetchRegionURL 拉取并解析单个数据源
func fetchRegionURL(client *http.Client, url string) ([]*remoteRegionNode, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, err
	}
	var nodes []*remoteRegionNode
	if err := json.Unmarshal(body, &nodes); err != nil {
		return nil, fmt.Errorf("解析 JSON 失败: %w", err)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("数据为空")
	}
	return nodes, nil
}

// normalizeRegionCode 规范化区划代码：去空白、校验纯数字
func normalizeRegionCode(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if _, err := strconv.Atoi(s); err != nil {
		return ""
	}
	return s
}

// regionLevelByCodeLen 按代码位数推断层级（GB/T 2260 三层六位层次码）
// 返回 0 表示非法层级（超出省/市/县范围，如 9 位乡级码）
func regionLevelByCodeLen(code string) int32 {
	switch len(code) {
	case 2:
		return model.RegionLevelProvince
	case 4:
		return model.RegionLevelCity
	case 6:
		return model.RegionLevelDistrict
	default:
		return 0
	}
}

// truncateRegionError 截断错误信息到任务表字段容量内
func truncateRegionError(msg string) string {
	const max = 480
	runes := []rune(msg)
	if len(runes) > max {
		return string(runes[:max])
	}
	return msg
}

// ToRegionInfo 将区划模型转换为 proto 区划信息
func ToRegionInfo(r *model.Region) *user.RegionInfo {
	if r == nil {
		return nil
	}
	return &user.RegionInfo{
		Code:       r.Code,
		Name:       r.Name,
		ParentCode: r.ParentCode,
		Level:      r.Level,
		Status:     r.Status,
	}
}

// ToRegionUpdateTaskInfo 将任务模型转换为 proto 任务信息（含差异明细解码）
func ToRegionUpdateTaskInfo(t *model.RegionUpdateTask) *user.RegionUpdateTaskInfo {
	if t == nil {
		return nil
	}
	info := &user.RegionUpdateTaskInfo{
		Id:           t.ID,
		Status:       t.Status,
		Source:       t.Source,
		TotalRemote:  t.TotalRemote,
		TotalLocal:   t.TotalLocal,
		AddedCount:   t.AddedCount,
		UpdatedCount: t.UpdatedCount,
		RemovedCount: t.RemovedCount,
		Error:        t.Error,
		Applied:      t.Applied,
		CreatedAt:    t.CreatedAt,
		FinishedAt:   "",
	}
	if t.FinishedAt != nil {
		info.FinishedAt = *t.FinishedAt
	}
	decodeDiff := func(raw string) []*user.RegionDiffItem {
		if raw == "" {
			return nil
		}
		var items []RegionDiffItem
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			return nil
		}
		list := make([]*user.RegionDiffItem, 0, len(items))
		for _, item := range items {
			list = append(list, &user.RegionDiffItem{
				Code:       item.Code,
				Name:       item.Name,
				NewName:    item.NewName,
				ParentCode: item.ParentCode,
				Level:      item.Level,
			})
		}
		return list
	}
	info.Added = decodeDiff(t.AddedJSON)
	info.Updated = decodeDiff(t.UpdatedJSON)
	info.Removed = decodeDiff(t.RemovedJSON)
	return info
}

// _ 占位，避免 common 未使用告警
var _ = (*common.Pagination)(nil)
