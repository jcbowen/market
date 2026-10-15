package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm/clause"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * 应用偏好业务（应用页「常用应用」）
 * 常用应用为社区级配置（同社区跨账号/跨设备共享），每社区单行覆盖式保存。
 * 数据量极小（一行 JSON），无分页需求。
 */

// AppPreferenceService 应用偏好业务服务
type AppPreferenceService struct {
	db *mysql.Instance
}

// NewAppPreferenceService 创建应用偏好业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *AppPreferenceService: 应用偏好业务服务实例
func NewAppPreferenceService(db *mysql.Instance) *AppPreferenceService {
	return &AppPreferenceService{db: db}
}

// Get 获取社区常用应用列表（无配置时返回空列表）
// 参数：
//   - ctx: 上下文
//   - req: 查询请求
//
// 返回值：
//   - *order.AppPreferencesInfo: 常用应用 key 列表（有序）
//   - error: 错误信息
func (s *AppPreferenceService) Get(ctx context.Context, req *order.GetAppPreferencesReq) (*order.AppPreferencesInfo, error) {
	if req.CommunityId <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "缺少社区信息")
	}
	var m model.AppPreference
	err := s.db.GetDb().WithContext(ctx).Where("community_id = ?", req.CommunityId).First(&m).Error
	if err != nil {
		// 无配置行：返回空列表（非错误，应用页展示全量清单即可）
		return &order.AppPreferencesInfo{AppKeys: make([]string, 0)}, nil
	}
	keys := make([]string, 0)
	if m.AppKeysJSON != "" {
		if err := json.Unmarshal([]byte(m.AppKeysJSON), &keys); err != nil {
			// 脏数据按空列表兜底，不阻塞应用页
			keys = make([]string, 0)
		}
	}
	return &order.AppPreferencesInfo{AppKeys: keys}, nil
}

// Save 保存社区常用应用（覆盖式，upsert 防并发首写冲突）
// 参数：
//   - ctx: 上下文
//   - req: 保存请求
//
// 返回值：
//   - error: 错误信息
func (s *AppPreferenceService) Save(ctx context.Context, req *order.SaveAppPreferencesReq) error {
	if req.CommunityId <= 0 {
		return bizerr.New(codes.InvalidArgument, "缺少社区信息")
	}
	if req.AppKeys == nil {
		req.AppKeys = make([]string, 0)
	}
	raw, err := json.Marshal(req.AppKeys)
	if err != nil {
		return bizerr.New(codes.InvalidArgument, "应用偏好序列化失败")
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	m := &model.AppPreference{
		CommunityID: req.CommunityId,
		AppKeysJSON: string(raw),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	// community_id 唯一键冲突时更新（社区级单行覆盖式保存）
	return s.db.GetDb().WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "community_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{"app_keys_json": string(raw), "updated_at": now}),
		}).
		Create(m).Error
}
