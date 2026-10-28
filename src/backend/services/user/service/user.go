package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/attachmenturl"
	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/likeutil"
	"github.com/jcbowen/market/src/backend/common/pageparam"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

// UserService 用户业务服务
type UserService struct {
	db *mysql.Instance
	// attachmentLoader 附件配置读取器（共享 market 库直读，60s 缓存）：
	// 头像等附件字段入库归一（SourcePath）与出库拼接（MediaURL）共用
	attachmentLoader *attachmenturl.Loader
}

// NewUserService 创建用户业务服务
// 参数：
//   - db: MySQL 实例
//
// 返回：
//   - *UserService: 用户业务服务实例
func NewUserService(db *mysql.Instance) *UserService {
	return &UserService{db: db, attachmentLoader: attachmenturl.NewLoader(db.GetDb())}
}

// MediaURL 附件地址出库拼接完整 URL（外链原样返回）
//
// 参数:
//   - ctx: 上下文
//   - src: 相对路径或完整 URL
//
// 返回值:
//   - string: 完整访问 URL
func (s *UserService) MediaURL(ctx context.Context, src string) string {
	return s.attachmentLoader.MediaURL(ctx, src)
}

// GetUserById 根据 ID 查询用户（无社区语境，服务间/自身调用使用）
// 参数：
//   - ctx: 上下文
//   - id: 用户 ID
//
// 返回：
//   - *model.User: 用户模型
//   - error: 错误信息
func (s *UserService) GetUserById(ctx context.Context, id int64) (*model.User, error) {
	var u model.User
	if err := s.db.GetDb().WithContext(ctx).First(&u, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("用户不存在")
		}
		return nil, err
	}
	return &u, nil
}

// GetUserByIdWithScope 按 ID 查询会员（带社区语境校验，后台查询使用）
// communityID > 0 时校验会员与本社区关联，无关联按不存在处理，防止跨社区越权读取（超管传 0 放行）
// 参数：
//   - ctx: 上下文
//   - id: 用户 ID
//   - communityID: 操作者社区语境（0 放行）
//
// 返回：
//   - *model.User: 用户模型
//   - error: 错误信息
func (s *UserService) GetUserByIdWithScope(ctx context.Context, id, communityID int64) (*model.User, error) {
	if communityID > 0 {
		var bound int64
		if err := s.db.GetDb().WithContext(ctx).Model(&model.UserCommunity{}).
			Where("user_id = ? AND community_id = ?", id, communityID).
			Count(&bound).Error; err != nil {
			return nil, err
		}
		if bound == 0 {
			return nil, fmt.Errorf("用户不存在")
		}
	}
	return s.GetUserById(ctx, id)
}

// GetUserByPhone 根据手机号查询用户
// 参数：
//   - ctx: 上下文
//   - phone: 手机号
//
// 返回：
//   - *model.User: 用户模型
//   - error: 错误信息
func (s *UserService) GetUserByPhone(ctx context.Context, phone string) (*model.User, error) {
	if phone == "" {
		return nil, fmt.Errorf("手机号为空")
	}
	var u model.User
	if err := s.db.GetDb().WithContext(ctx).Where("phone = ?", phone).First(&u).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("用户不存在")
		}
		return nil, err
	}
	return &u, nil
}

// GetUserByOpenid 根据微信 openid 查询用户
// 参数：
//   - ctx: 上下文
//   - openid: 微信 openid
//
// 返回：
//   - *model.User: 用户模型
//   - error: 错误信息
func (s *UserService) GetUserByOpenid(ctx context.Context, openid string) (*model.User, error) {
	if openid == "" {
		return nil, fmt.Errorf("openid 为空")
	}
	var u model.User
	if err := s.db.GetDb().WithContext(ctx).Where("openid = ?", openid).First(&u).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("用户不存在")
		}
		return nil, err
	}
	return &u, nil
}

// CreateOrUpdateUser 创建或更新用户（登录链路共用）
// 查找策略：有手机号按手机号查找（手机号登录/绑定场景）；
// 无手机号按 openid 查找（微信登录场景）——禁止按空手机号兜底匹配，
// 否则第二个微信登录用户会命中首个未绑手机的存量用户并覆盖其 openid，造成账号串号
// 昵称策略：仅在新用户/历史空昵称时按 defaultNickname 填充，登录链路不覆盖已有昵称
// 参数：
//   - ctx: 上下文
//   - req: 创建用户请求
//
// 返回：
//   - *model.User: 用户模型
//   - error: 错误信息
func (s *UserService) CreateOrUpdateUser(ctx context.Context, req *user.CreateUserReq) (*model.User, error) {
	var u *model.User
	if req.Phone != "" {
		if found, err := s.GetUserByPhone(ctx, req.Phone); err == nil {
			u = found
		}
	} else if req.Openid != "" {
		if found, err := s.GetUserByOpenid(ctx, req.Openid); err == nil {
			u = found
		}
	}
	if u == nil {
		u = &model.User{}
	}

	u.Openid = orValue(req.Openid, u.Openid)
	u.AlipayUserID = orPtrValue(req.AlipayUserId, u.AlipayUserID)
	u.Unionid = orPtrValue(req.UnionId, u.Unionid)
	// 手机号只在请求携带时覆盖：按 openid 命中的已绑手机会员走微信登录时 req.Phone 为空，
	// 无条件赋值会把存量手机号清空（Save 全量落库），后续按 phone 查找即丢失该账号
	u.Phone = orPtrValue(req.Phone, u.Phone)
	// 昵称只在为空时写入：登录入参不带真实昵称（微信授权昵称另有 user/profile 更新入口），
	// 避免每次登录把用户自定义昵称刷回默认值
	if u.Nickname == "" {
		u.Nickname = orValue(req.Nickname, defaultNickname(req.Phone))
	}
	// 头像入库归一为附件相对路径（common/attachmenturl 口径）：C 端头像上传提交的
	// 站内完整 URL 剥为相对路径，域名/存储模式变更后不失效；微信头像等外链原样保存
	u.Avatar = orValue(s.attachmentLoader.SourcePath(ctx, req.Avatar), u.Avatar)
	u.Gender = req.Gender
	if req.CommunityId > 0 {
		u.CommunityID = req.CommunityId
	}
	lastLoginAt := time.Now().Format("2006-01-02 15:04:05")
	u.LastLoginAt = &lastLoginAt

	if u.ID > 0 {
		if err := s.db.GetDb().WithContext(ctx).Save(u).Error; err != nil {
			return nil, err
		}
		return u, nil
	}

	if err := s.db.GetDb().WithContext(ctx).Create(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateUserCommunity 切换会员当前社区
// 除改写 users.community_id，同步补写 user_communities 关联：后台的社区语境校验
// （指派配送、拉黑、会员详情）按关联表判定归属，只改主表会让会员在后台「不存在」
// 两写必须在同一事务：主表已切、关联未补的中间态会让会员在新社区的后台语境里查无此人，
// 又已按新社区下单/收货，等于把一个可用的会员从管理视野里抹掉
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - communityID: 社区 ID
//
// 返回：
//   - error: 错误信息
func (s *UserService) UpdateUserCommunity(ctx context.Context, userID, communityID int64) error {
	if userID <= 0 || communityID <= 0 {
		return fmt.Errorf("参数错误")
	}
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).
			Where("id = ?", userID).
			Update("community_id", communityID).Error; err != nil {
			return err
		}
		return markUserCommunity(tx, userID, communityID, "user_select")
	})
}

// UpdateUserProfile 更新用户资料
// 参数：
//   - ctx: 上下文
//   - req: 更新用户资料请求
//
// 返回：
//   - *model.User: 用户模型
//   - error: 错误信息
func (s *UserService) UpdateUserProfile(ctx context.Context, req *user.UpdateUserProfileReq) (*model.User, error) {
	u, err := s.GetUserById(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if req.Nickname != "" {
		u.Nickname = req.Nickname
	}
	if req.Avatar != "" {
		// 入库归一：站内附件剥为相对路径（外链原样），见 Upsert 同字段注释
		u.Avatar = s.attachmentLoader.SourcePath(ctx, req.Avatar)
	}
	// 仅接受 1男/2女；0=未传，其他正值是非法输入，不能覆盖既有资料
	if req.Gender == 1 || req.Gender == 2 {
		u.Gender = req.Gender
	}
	// 生日非空才覆盖（与昵称/头像一致的宽松语义；C 端选择器无法表达「清空」）
	if req.Birthday != "" {
		birthday, err := normalizeBirthday(req.Birthday)
		if err != nil {
			return nil, err
		}
		u.Birthday = &birthday
	}
	if err := s.db.GetDb().WithContext(ctx).Save(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateMember 后台编辑会员基础资料（昵称/性别/生日）
// 昵称非空才覆盖；性别与生日全量覆盖（生日空串=清空）；
// communityId > 0 时校验会员与本社区的关联关系，防止跨社区修改他社区会员（超管传 0 放行）
// 参数：
//   - ctx: 上下文
//   - req: 编辑会员请求
//
// 返回：
//   - *model.User: 更新后的会员模型
//   - error: 错误信息
func (s *UserService) UpdateMember(ctx context.Context, req *user.UpdateMemberReq) (*model.User, error) {
	if req.Id <= 0 {
		return nil, fmt.Errorf("参数错误")
	}
	db := s.db.GetDb().WithContext(ctx)
	if req.CommunityId > 0 {
		var bound int64
		if err := db.Model(&model.UserCommunity{}).
			Where("user_id = ? AND community_id = ?", req.Id, req.CommunityId).
			Count(&bound).Error; err != nil {
			return nil, err
		}
		if bound == 0 {
			return nil, fmt.Errorf("会员不存在或无权操作")
		}
	}
	u, err := s.GetUserById(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if req.Nickname != "" {
		u.Nickname = req.Nickname
	}
	u.Gender = req.Gender
	if req.Birthday == "" {
		u.Birthday = nil
	} else {
		birthday, err := normalizeBirthday(req.Birthday)
		if err != nil {
			return nil, err
		}
		u.Birthday = &birthday
	}
	if err := db.Save(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

// normalizeBirthday 校验并规范生日格式（接受 YYYY-MM-DD，非法格式报参数错误）
// 参数：
//   - value: 待校验的生日字符串
//
// 返回：
//   - string: 规范化后的生日（YYYY-MM-DD）
//   - error: 格式错误时返回错误
func normalizeBirthday(value string) (string, error) {
	t, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return "", fmt.Errorf("生日格式错误，应为 YYYY-MM-DD")
	}
	return t.Format("2006-01-02"), nil
}

// ListUsers 查询会员列表（多社区模型：communityID > 0 时按会员-社区关联表过滤）
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID，>0 只列出与该社区有关联的会员
//   - keyword: 昵称或手机号关键字
//   - status: 旧状态参数，>0 时按状态过滤（兼容保留）
//   - statusFilter: 状态筛选：-1 全部，0 拉黑，1 正常（提供时优先于 status）
//   - page: 页码
//   - pageSize: 每页数量
//
// 返回：
//   - []*model.User: 用户模型列表
//   - int64: 总数
//   - string: 过滤语境社区名称（communityID <= 0 时为空串）
//   - error: 错误信息
func (s *UserService) ListUsers(ctx context.Context, communityID int64, keyword string, status, statusFilter int32, page, pageSize int) ([]*model.User, int64, string, error) {
	db := s.db.GetDb().WithContext(ctx).Model(&model.User{})
	if communityID > 0 {
		db = db.Where("id IN (SELECT user_id FROM user_communities WHERE community_id = ?)", communityID)
	}
	if keyword != "" {
		like := likeutil.Contains(keyword)
		db = db.Where("nickname LIKE ? OR phone LIKE ?", like, like)
	}
	if statusFilter >= 0 {
		db = db.Where("status = ?", statusFilter)
	} else if status > 0 {
		db = db.Where("status = ?", status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, "", err
	}

	// 服务层兜底上限：HTTP 网关已按 maxPageSize 收敛，但 gRPC 可被内部或直连调用传入超大值
	page, pageSize = pageparam.Clamp(page, pageSize)

	var list []*model.User
	offset := (page - 1) * pageSize
	if err := db.Order("id DESC").Limit(pageSize).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, "", err
	}

	communityName := ""
	if communityID > 0 {
		communityName = s.getCommunityName(ctx, communityID)
	}
	return list, total, communityName, nil
}

// batchGetUsersMaxIDs 单次批量查询的会员 ID 上限
// 调用方为后台列表（每页最多 50 行），200 足够又不给构造超大 IN 清单留空间
const batchGetUsersMaxIDs = 200

// BatchGetUsers 按 ID 集合批量查询会员（后台订单列表按行取会员名的收口）
// 逐行 GetUserById 会把一页放大成 N 次 RPC；本方法一次往返取回整页会员。
// 社区语境（communityID>0）按会员-社区关联表过滤：越权 ID 直接不出现在结果里，
// 而不是报错——既防止通过列表接口读其他社区会员的昵称/手机号，
// 也让「会员已解绑该社区」的历史订单仍能正常展示（留空而非整页 500）。
//
// 参数：
//   - ctx: 上下文
//   - ids: 会员 ID 集合（可重复、可含 0，自动忽略）
//   - communityID: 操作者社区语境，0=超管或内部调用（不过滤）
//
// 返回值：
//   - []*model.User: 命中的会员（顺序不保证，调用方按 ID 建索引）
//   - error: ID 集合超限或数据库错误
//
// 使用示例：
//
//	users, err := userService.BatchGetUsers(ctx, []int64{1, 2, 3}, communityID)
func (s *UserService) BatchGetUsers(ctx context.Context, ids []int64, communityID int64) ([]*model.User, error) {
	clean := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			clean = append(clean, id)
		}
	}
	if len(clean) == 0 {
		return nil, nil
	}
	if len(clean) > batchGetUsersMaxIDs {
		return nil, bizerr.New(codes.InvalidArgument, fmt.Sprintf("单次批量查询会员数不能超过 %d", batchGetUsersMaxIDs))
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.User{}).Where("id IN ?", clean)
	if communityID > 0 {
		db = db.Where("id IN (SELECT user_id FROM user_communities WHERE community_id = ?)", communityID)
	}
	var list []*model.User
	if err := db.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// getCommunityName 查询社区名称
// 参数：
//   - ctx: 上下文
//   - id: 社区 ID
//
// 返回：
//   - string: 社区名称（不存在时为空串）
func (s *UserService) getCommunityName(ctx context.Context, id int64) string {
	var c model.Community
	if err := s.db.GetDb().WithContext(ctx).Select("name").First(&c, id).Error; err != nil {
		return ""
	}
	return c.Name
}

// MarkUserCommunity 幂等记录会员与社区的关联
// 同一 (user_id, community_id) 重复记录时保留首次来源，不报错
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - communityID: 社区 ID
//   - source: 关联来源（address/order/admin/user_select/backfill）
//
// 返回：
//   - error: 错误信息
func (s *UserService) MarkUserCommunity(ctx context.Context, userID, communityID int64, source string) error {
	if userID <= 0 || communityID <= 0 {
		return fmt.Errorf("参数错误")
	}
	return markUserCommunity(s.db.GetDb().WithContext(ctx), userID, communityID, source)
}

// markUserCommunity 在给定 GORM 句柄上幂等写入会员-社区关联行
// 独立出来是为了让「主表 + 关联表」必须同事务的调用方（UpdateUserCommunity）复用同一份写入口径
//
// 参数：
//   - tx: GORM 句柄（事务或普通连接均可，已由调用方绑定上下文）
//   - userID: 用户 ID
//   - communityID: 社区 ID
//   - source: 关联来源
//
// 返回：
//   - error: 错误信息（唯一索引冲突视为已存在，返回 nil）
func markUserCommunity(tx *gorm.DB, userID, communityID int64, source string) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	row := &model.UserCommunity{
		UserID:      userID,
		CommunityID: communityID,
		Source:      source,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err := tx.
		Where("user_id = ? AND community_id = ?", userID, communityID).
		FirstOrCreate(row).Error
	if err != nil {
		// 并发下唯一索引冲突等同已存在，视为成功
		if isDuplicateKeyErr(err) {
			return nil
		}
		return err
	}
	return nil
}

// SetUserStatus 设置会员状态（拉黑/恢复）
// 参数：
//   - ctx: 上下文
//   - id: 用户 ID
//   - status: 0 拉黑 1 正常
//   - communityId: 操作者社区语境（>0 时要求会员与本社区有绑定关系，防止跨社区拉黑；0=超管）
//
// 返回：
//   - error: 参数非法/会员不存在/无权操作
func (s *UserService) SetUserStatus(ctx context.Context, id int64, status int32, communityId int64) error {
	if id <= 0 || (status != 0 && status != 1) {
		return fmt.Errorf("参数错误")
	}
	db := s.db.GetDb().WithContext(ctx)
	if communityId > 0 {
		// 状态为用户级全局字段（拉黑即全站拦截登录），故必须确认操作者社区与该会员有关系，
		// 否则 A 社区管理员可冻结仅在 B 社区消费的会员
		var bound int64
		if err := db.Model(&model.UserCommunity{}).
			Where("user_id = ? AND community_id = ?", id, communityId).
			Count(&bound).Error; err != nil {
			return err
		}
		if bound == 0 {
			return fmt.Errorf("会员不存在或无权操作")
		}
	}
	res := db.
		Model(&model.User{}).
		Where("id = ?", id).
		Update("status", status)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("会员不存在")
	}
	return nil
}

// afterRegisterHooks 新会员注册后钩子列表（新用户活动等扩展点，如新人券/首单礼）
// 注册核心只负责建人与关联社区，活动发放通过挂载钩子实现，避免每加活动都改注册逻辑
var afterRegisterHooks []func(ctx context.Context, u *model.User)

// RegisterAfterRegisterHook 挂载新会员注册后钩子（进程启动时注册；钩子异常不影响注册结果）
// 参数：
//   - fn: 钩子函数
func RegisterAfterRegisterHook(fn func(ctx context.Context, u *model.User)) {
	afterRegisterHooks = append(afterRegisterHooks, fn)
}

// runAfterRegisterHooks 触发全部注册后钩子（recover 兜底，钩子失败仅跳过不回滚注册）
func runAfterRegisterHooks(ctx context.Context, u *model.User) {
	for _, fn := range afterRegisterHooks {
		func() {
			defer func() { _ = recover() }()
			fn(ctx, u)
		}()
	}
}

// CreateMember 注册会员（幂等）
// 手机号未注册：新建会员（openid 沿用手机号登录的 phone_<手机号> 占位约定），
// 事务写入会员-社区关联（来源 req.Source，缺省 admin），并触发注册后钩子；
// 已注册（含其他社区老会员）：复用既有会员，幂等补写本社区关联，不报错；
// 返回 is_new 供调用方区分「新注册」与「复用」（新用户活动只对 is_new=true 生效）
// 参数：
//   - ctx: 上下文
//   - req: 注册请求
//
// 返回：
//   - *model.User: 注册（或复用）的会员
//   - bool: 是否本次新注册
//   - error: 错误信息
func (s *UserService) CreateMember(ctx context.Context, req *user.CreateMemberReq) (*model.User, bool, error) {
	if req.Phone == "" || req.CommunityId <= 0 {
		return nil, false, fmt.Errorf("参数错误")
	}
	source := req.Source
	if source == "" {
		source = "admin"
	}

	// 已注册：复用既有会员，幂等补写本社区关联
	if u, err := s.GetUserByPhone(ctx, req.Phone); err == nil {
		if err := s.MarkUserCommunity(ctx, u.ID, req.CommunityId, source); err != nil {
			return nil, false, err
		}
		return u, false, nil
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	u := &model.User{
		Openid:      phoneOpenidPrefix(req.Phone),
		Phone:       model.NewPhone(req.Phone),
		Nickname:    orValue(req.Nickname, defaultNickname(req.Phone)),
		Gender:      req.Gender,
		CommunityID: req.CommunityId,
		Status:      1,
	}
	// 生日选填：注册时携带才写入（YYYY-MM-DD）
	if req.Birthday != "" {
		birthday, err := normalizeBirthday(req.Birthday)
		if err != nil {
			return nil, false, err
		}
		u.Birthday = &birthday
	}
	// 并发竞态兜底：两个同手机号请求同时通过上方查重后撞 phone 唯一索引（1062），
	// 捕获后按 phone 重查复用既有会员，幂等口径与查重命中分支一致（补社区关联、非新注册），
	// 而不是把 1062 冒泡成内部错误（网关吞成 10002）
	created := true
	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(u).Error; err != nil {
			if !isDuplicateKeyErr(err) {
				return err
			}
			var existing model.User
			if err := tx.Where("phone = ?", req.Phone).First(&existing).Error; err != nil {
				return err
			}
			*u = existing
			created = false
			return nil
		}
		row := &model.UserCommunity{
			UserID:      u.ID,
			CommunityID: req.CommunityId,
			Source:      source,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		return tx.Create(row).Error
	}); err != nil {
		return nil, false, err
	}

	if !created {
		if err := s.MarkUserCommunity(ctx, u.ID, req.CommunityId, source); err != nil {
			return nil, false, err
		}
		return u, false, nil
	}

	// 新注册成功，触发扩展钩子（新用户活动预留；钩子内部失败不影响注册结果）
	runAfterRegisterHooks(ctx, u)
	return u, true, nil
}

// ListFulfillers 列出某社区全部履约人员（后台订单指派弹窗数据源）
// 履约人员 = 本社区任职中且绑定了会员的员工（admin_users.user_id，配置入口在员工编辑页）
// 参数：
//   - ctx: 上下文
//   - communityID: 社区 ID
//
// 返回：
//   - []*user.FulfillerInfo: 履约人员列表（UserId 为会员 ID，含昵称/手机号）
//   - error: 错误信息
func (s *UserService) ListFulfillers(ctx context.Context, communityID int64) ([]*user.FulfillerInfo, error) {
	if communityID <= 0 {
		return nil, fmt.Errorf("参数错误")
	}
	// 禁用员工（status != 1）不视为履约人员，避免停用账号仍接单
	var userIDs []int64
	err := s.db.GetDb().WithContext(ctx).
		Model(&model.Admin{}).
		Joins("JOIN admin_communities ac ON ac.admin_id = admin_users.id AND ac.community_id = ?", communityID).
		Where("admin_users.status = 1 AND admin_users.user_id IS NOT NULL").
		Pluck("admin_users.user_id", &userIDs).Error
	if err != nil {
		return nil, err
	}
	if len(userIDs) == 0 {
		return []*user.FulfillerInfo{}, nil
	}
	var users []*model.User
	if err := s.db.GetDb().WithContext(ctx).
		Select("id, nickname, phone").
		Where("id IN ?", userIDs).
		Find(&users).Error; err != nil {
		return nil, err
	}
	list := make([]*user.FulfillerInfo, 0, len(users))
	for _, u := range users {
		list = append(list, &user.FulfillerInfo{UserId: u.ID, Nickname: u.Nickname, Phone: u.PhoneValue()})
	}
	return list, nil
}

// CheckFulfiller 校验用户在某社区是否履约人员（员工绑定会员即履约，禁用员工除外）
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - communityID: 社区 ID
//
// 返回：
//   - bool: 是否履约人员
//   - error: 错误信息
func (s *UserService) CheckFulfiller(ctx context.Context, userID, communityID int64) (bool, error) {
	if userID <= 0 || communityID <= 0 {
		return false, nil
	}
	var count int64
	err := s.db.GetDb().WithContext(ctx).
		Model(&model.Admin{}).
		Joins("JOIN admin_communities ac ON ac.admin_id = admin_users.id AND ac.community_id = ?", communityID).
		Where("admin_users.status = 1 AND admin_users.user_id = ?", userID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// phoneOpenidPrefix 生成手机号会员的 openid 占位标识（与手机号登录约定一致）// 参数：
//   - phone: 手机号
//
// 返回：
//   - string: phone_<手机号>
func phoneOpenidPrefix(phone string) string {
	return "phone_" + phone
}

// isDuplicateKeyErr 判断是否为唯一键冲突错误（MySQL 1062）
// 参数：
//   - err: 数据库错误
//
// 返回：
//   - bool: 是否唯一键冲突
func isDuplicateKeyErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "1062") || strings.Contains(err.Error(), "Duplicate entry")
}

// BackfillUserCommunities 启动时幂等回填存量会员-社区关联
// 将 users.community_id > 0 且尚无关联记录的会员补写一条来源 backfill 的关联；
// 依赖 (user_id, community_id) 唯一索引保证重复执行不产生重复数据
// 参数：
//   - ctx: 上下文
//
// 返回：
//   - error: 错误信息
func (s *UserService) BackfillUserCommunities(ctx context.Context) error {
	db := s.db.GetDb().WithContext(ctx)
	var users []*model.User
	if err := db.
		Where("community_id > 0 AND id NOT IN (SELECT user_id FROM user_communities)").
		Select("id, community_id").
		Find(&users).Error; err != nil {
		return err
	}
	if len(users) == 0 {
		return nil
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	rows := make([]*model.UserCommunity, 0, len(users))
	for _, u := range users {
		rows = append(rows, &model.UserCommunity{
			UserID:      u.ID,
			CommunityID: u.CommunityID,
			Source:      "backfill",
			CreatedAt:   now,
			UpdatedAt:   now,
		})
	}
	return db.CreateInBatches(rows, 500).Error
}

// ToUserInfo 将用户模型转换为 proto 用户信息
// 参数：
//   - u: 用户模型
//
// 返回：
//   - *user.UserInfo: proto 用户信息
func ToUserInfo(u *model.User) *user.UserInfo {
	if u == nil {
		return nil
	}
	return &user.UserInfo{
		Id:           u.ID,
		Openid:       u.Openid,
		AlipayUserId: ptrString(u.AlipayUserID),
		UnionId:      ptrString(u.Unionid),
		Phone:        u.PhoneValue(),
		Nickname:     u.Nickname,
		Avatar:       u.Avatar,
		Gender:       u.Gender,
		CommunityId:  u.CommunityID,
		Status:       u.Status,
		LastLoginAt:  ptrString(u.LastLoginAt),
		Birthday:     birthdayString(u.Birthday),
		CreatedAt:    u.CreatedAt,
	}
}

// orValue 返回非空值，优先使用新值
// 参数：
//   - newVal: 新值
//   - oldVal: 旧值
//
// 返回：
//   - string: 有效值
func orValue(newVal, oldVal string) string {
	if newVal != "" {
		return newVal
	}
	return oldVal
}

// defaultNickname 生成默认昵称（C 端登录静默注册与后台/收银台注册会员共用口径）
// 有手机号时取「会员+手机尾号 4 位」，无手机号（微信等渠道登录）取「微信用户」
//
// 参数：
//   - phone: 手机号，可为空
//
// 返回：
//   - string: 默认昵称
func defaultNickname(phone string) string {
	if len(phone) >= 4 {
		return "会员" + phone[len(phone)-4:]
	}
	return "微信用户"
}

// orPtrValue 返回非空字符串指针，优先使用新值
// 空字符串视为未提供，保留旧指针值
//
// 参数：
//   - newVal: 新值
//   - oldVal: 旧指针值
//
// 返回：
//   - *string: 有效指针
func orPtrValue(newVal string, oldVal *string) *string {
	if newVal != "" {
		return &newVal
	}
	return oldVal
}

// ptrString 将字符串指针安全转换为字符串
//
// 参数：
//   - s: 字符串指针
//
// 返回：
//   - string: 空字符串或指针指向的值
func ptrString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// birthdayString 将生日列扫描值规范化为 YYYY-MM-DD 输出
// DATE 列在 DSN parseTime=true 时被驱动解析为 time.Time，扫入 *string 字段时
// database/sql 按 RFC3339Nano 格式化（如 "1995-06-15T00:00:00+08:00"），须截取前 10 位
// 参数：
//   - v: 生日字段指针（可空）
//
// 返回：
//   - string: YYYY-MM-DD；未设置或值异常短时返回空串
//
// 示例：入参指向 "1995-06-15T00:00:00+08:00" 时返回 "1995-06-15"；入参 nil 返回 ""
func birthdayString(v *string) string {
	if v == nil || len(*v) < 10 {
		return ""
	}
	return (*v)[:10]
}

// _ 占位，避免 errcode 未使用告警
var _ = errcode.Success
