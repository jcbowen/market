package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/timeutil"
	"github.com/jcbowen/market/src/backend/common/channel"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

// ToClientChannelInfo 将渠道模型转换为 proto 渠道信息
//
// 参数：
//   - c: 渠道模型，可为 nil
//
// 返回值：
//   - *user.ClientChannelInfo: proto 渠道信息，入参为 nil 时返回 nil
func ToClientChannelInfo(c *model.ClientChannel) *user.ClientChannelInfo {
	if c == nil {
		return nil
	}
	return &user.ClientChannelInfo{
		Id:         c.ID,
		Code:       c.Code,
		Name:       c.Name,
		AppId:      c.AppID,
		AppSecret:  c.AppSecret,
		Enabled:    c.Enabled,
		PayMethods: c.PayMethodsList(),
		Configured: c.IsConfigured(),
		UpdatedAt:  c.UpdatedAt,
	}
}

// isPhoneOpenid 判断是否手机号登录的占位 openid（phone_<手机号>）
// 该值仅为满足 users.openid 唯一索引生成，不是微信真实 openid
//
// 参数：
//   - openid: openid 字符串
//
// 返回值：
//   - bool: 是否占位 openid
func isPhoneOpenid(openid string) bool {
	return strings.HasPrefix(openid, "phone_")
}

// ChannelService 客户端渠道业务服务
// 渠道主数据（client_channels）与用户渠道身份（user_channel_identities）同域管理：
// 登录换取的 openid 按渠道落身份表，支付/渲染链路按渠道读取
type ChannelService struct {
	db *mysql.Instance
}

// NewChannelService 创建客户端渠道业务服务
//
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *ChannelService: 渠道业务服务实例
func NewChannelService(db *mysql.Instance) *ChannelService {
	return &ChannelService{db: db}
}

// channelSeeds 内置渠道种子（幂等补种基准，顺序即固定展示顺序）
// h5/pos 无需凭证默认启用；其余渠道默认停用待配置；
// 默认支付方式绑定均为 ["cash"]（货到付款内置项），其余支付方式由管理员在渠道配置中绑定
var channelSeeds = []struct {
	Code       string
	Name       string
	Enabled    bool
	PayMethods string
}{
	{Code: channel.H5, Name: "浏览器 H5", Enabled: true, PayMethods: `["cash"]`},
	{Code: channel.GZH, Name: "微信公众号", Enabled: false, PayMethods: `[]`},
	{Code: channel.MP, Name: "微信小程序", Enabled: false, PayMethods: `[]`},
	{Code: channel.ALIMP, Name: "支付宝小程序", Enabled: false, PayMethods: `[]`},
	{Code: channel.ANDROID, Name: "安卓 App", Enabled: false, PayMethods: `[]`},
	{Code: channel.IOS, Name: "iOS App", Enabled: false, PayMethods: `[]`},
	{Code: channel.POS, Name: "门店收银台", Enabled: true, PayMethods: `["cash"]`},
}

// EnsureDefaultClientChannels 确保内置客户端渠道存在（幂等补种）
// 逐渠道补种缺失项（含历史 app 渠道拆分为 android/ios 后的缺口），
// 并清理已下线的渠道行（如 app）；已存在的渠道不做任何覆写
//
// 返回值：
//   - error: 初始化失败时返回错误
func (s *ChannelService) EnsureDefaultClientChannels() error {
	now := time.Now().Format("2006-01-02 15:04:05")
	for _, seed := range channelSeeds {
		var count int64
		if err := s.db.GetDb().Model(&model.ClientChannel{}).
			Where("code = ?", seed.Code).Count(&count).Error; err != nil {
			return fmt.Errorf("查询客户端渠道失败: %w", err)
		}
		if count > 0 {
			continue
		}
		row := &model.ClientChannel{
			Code:       seed.Code,
			Name:       seed.Name,
			Enabled:    seed.Enabled,
			PayMethods: seed.PayMethods,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := s.db.GetDb().Create(row).Error; err != nil {
			return fmt.Errorf("初始化内置客户端渠道 %s 失败: %w", seed.Code, err)
		}
	}
	// 清理已下线的渠道（历史单 App 渠道已拆分为 android/ios，直接重建不留迁移）
	if err := s.db.GetDb().
		Where("code NOT IN ?", channel.All()).
		Delete(&model.ClientChannel{}).Error; err != nil {
		return fmt.Errorf("清理下线客户端渠道失败: %w", err)
	}
	return nil
}

// ListClientChannels 查询客户端渠道列表（按固定常用程度顺序，与 channel.OrderIndex 一致）
//
// 参数：
//   - ctx: 上下文
//   - onlyEnabled: true 仅返回启用渠道
//
// 返回值：
//   - []*model.ClientChannel: 渠道列表
//   - error: 查询失败时返回错误
func (s *ChannelService) ListClientChannels(ctx context.Context, onlyEnabled bool) ([]*model.ClientChannel, error) {
	db := s.db.GetDb().WithContext(ctx).Model(&model.ClientChannel{})
	if onlyEnabled {
		db = db.Where("enabled = ?", true)
	}
	var list []*model.ClientChannel
	if err := db.Find(&list).Error; err != nil {
		return nil, fmt.Errorf("查询客户端渠道列表失败: %w", err)
	}
	// 排序功能已移除：按内置固定顺序（常用程度）返回，未知渠道排最后
	order := make(map[string]int, len(list))
	for _, row := range list {
		order[row.Code] = channel.OrderIndex(row.Code)
	}
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && order[list[j].Code] < order[list[j-1].Code]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	return list, nil
}

// GetClientChannel 按渠道 code 查询渠道
//
// 参数：
//   - ctx: 上下文
//   - code: 渠道标识
//
// 返回值：
//   - *model.ClientChannel: 渠道行，不存在时为 nil
//   - error: 查询异常（非记录不存在）时返回错误
func (s *ChannelService) GetClientChannel(ctx context.Context, code string) (*model.ClientChannel, error) {
	if !channel.IsValid(code) {
		return nil, bizerr.New(codes.InvalidArgument, fmt.Sprintf("未知客户端渠道: %s", code))
	}
	var row model.ClientChannel
	err := s.db.GetDb().WithContext(ctx).Where("code = ?", code).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("查询客户端渠道失败: %w", err)
	}
	return &row, nil
}

// payMethodCompatible 支付方式与渠道的平台兼容矩阵
// wechat 配置可绑定 h5/gzh/mp/pos（pos 走 B 扫 C 付款码）；
// alipay 配置可绑定 h5/alimp/pos；gzh 微信支付链路未开放，绑定允许但渲染时被能力判定过滤
var payMethodCompatible = map[string]map[string]bool{
	"wechat": {channel.H5: true, channel.GZH: true, channel.MP: true, channel.POS: true},
	"alipay": {channel.H5: true, channel.ALIMP: true, channel.POS: true},
}

// validatePayMethods 校验并规整渠道绑定的支付方式列表
// 每项为 "cash" 或 "<platform>:<config_id>"；cash 至多一条、同平台至多一条
// （C 端支付方式按平台选择、POS 付款码按前缀自动识别渠道，同平台多条会产生歧义）；
// 平台须与渠道兼容（payMethodCompatible）；返回规整后的 JSON 串
//
// 参数：
//   - code: 渠道标识
//   - methods: 待校验的支付方式列表
//
// 返回值：
//   - string: 规整后的 JSON 数组串
//   - error: 语法、平台或重复项不合法时返回业务错误
func validatePayMethods(code string, methods []string) (string, error) {
	seenPlatform := make(map[string]bool)
	normalized := make([]string, 0, len(methods))
	for _, raw := range methods {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if entry == "cash" {
			if seenPlatform["cash"] {
				return "", bizerr.New(codes.InvalidArgument, "货到付款至多绑定一条")
			}
			seenPlatform["cash"] = true
			normalized = append(normalized, entry)
			continue
		}
		platform, idStr, found := strings.Cut(entry, ":")
		platform = strings.TrimSpace(platform)
		id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
		if !found || err != nil || id <= 0 || (platform != "wechat" && platform != "alipay") {
			return "", bizerr.New(codes.InvalidArgument, fmt.Sprintf("支付方式 %q 不合法，应为 cash 或 平台:配置ID", entry))
		}
		if !payMethodCompatible[platform][code] {
			return "", bizerr.New(codes.InvalidArgument, fmt.Sprintf("%s 平台支付配置不可绑定到渠道 %s", platform, code))
		}
		if seenPlatform[platform] {
			return "", bizerr.New(codes.InvalidArgument, fmt.Sprintf("%s 平台至多绑定一条支付配置", platform))
		}
		seenPlatform[platform] = true
		normalized = append(normalized, fmt.Sprintf("%s:%d", platform, id))
	}
	out, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("序列化支付方式列表失败: %w", err)
	}
	return string(out), nil
}

// SaveClientChannel 保存渠道配置（名称/凭证/启用/支付方式绑定）
// AppSecret 为空时保留原值，避免未重新输入时清空已保存内容；code 不可变更；
// pay_methods 做语法、平台兼容与去重校验（validatePayMethods），
// 绑定微信支付的渠道另要求 AppID（微信 AppID 属渠道身份，见 model.ClientChannel.IsConfigured）
//
// 参数：
//   - ctx: 上下文
//   - req: 保存请求
//
// 返回值：
//   - *model.ClientChannel: 保存后的渠道行
//   - error: 校验或保存失败时返回错误
func (s *ChannelService) SaveClientChannel(ctx context.Context, req *user.SaveClientChannelReq) (*model.ClientChannel, error) {
	if req.Id <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "参数错误")
	}
	if !channel.IsValid(req.Code) {
		return nil, bizerr.New(codes.InvalidArgument, fmt.Sprintf("未知客户端渠道: %s", req.Code))
	}
	if req.Name == "" {
		return nil, bizerr.New(codes.InvalidArgument, "渠道名称不能为空")
	}

	var row model.ClientChannel
	if err := s.db.GetDb().WithContext(ctx).First(&row, req.Id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, bizerr.New(codes.NotFound, "客户端渠道不存在")
		}
		return nil, fmt.Errorf("查询客户端渠道失败: %w", err)
	}
	if row.Code != req.Code {
		return nil, bizerr.New(codes.InvalidArgument, "渠道标识不可变更")
	}

	row.Name = req.Name
	row.AppID = strings.TrimSpace(req.AppId)
	if req.AppSecret != "" {
		row.AppSecret = req.AppSecret
	}
	row.Enabled = req.Enabled
	row.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")

	// 支付方式绑定校验与落库（渠道侧决定启用哪些支付配置与收银台顺序）
	// 先于启用校验：微信 AppID 属渠道身份，是否必填取决于本次绑定的支付方式
	methods, err := validatePayMethods(row.Code, req.PayMethods)
	if err != nil {
		return nil, err
	}
	row.PayMethods = methods

	// 绑定微信支付即要求渠道 AppID（不以启用为前提）：
	// 支付配置行不再持有微信 AppID，缺失时下单只能带着空 appid 被渠道拒绝，
	// 在保存时就拦下比让用户在收银台看到一句无法理解的失败更可修
	if row.BindsWechatPay() && row.AppID == "" {
		return nil, bizerr.New(codes.FailedPrecondition, "绑定微信支付需先填写渠道 AppID（微信支付商户号下须已完成该 AppID 关联）")
	}
	// 启用状态与凭证完整性一致性校验：防止凭证被清空/不全的渠道被置为启用
	// （登录与支付链路按渠道现取凭证，启用但凭证不完整会直接报"未配置"）
	// 注意：业务校验错误必须 bizerr（纯 fmt.Errorf 会被 ToStatus 归为 Internal，网关吞成 10002）
	if row.Enabled && !row.IsConfigured() {
		return nil, bizerr.New(codes.FailedPrecondition, "渠道凭证不完整，无法启用")
	}
	// 暂不支持渠道（android/ios）恒为禁用态：即使存量数据为启用也强制关闭
	if row.Enabled && channel.IsDisabled(row.Code) {
		return nil, bizerr.New(codes.FailedPrecondition, "该渠道暂不支持，无法启用")
	}

	row.CreatedAt = timeutil.NormalizeRFC3339(row.CreatedAt)
	row.UpdatedAt = timeutil.NormalizeRFC3339(row.UpdatedAt)
	if err := s.db.GetDb().WithContext(ctx).Save(&row).Error; err != nil {
		return nil, fmt.Errorf("保存客户端渠道失败: %w", err)
	}
	return &row, nil
}

// GetUserChannelOpenid 查询用户在指定客户端渠道的微信 openid
// 渠道身份表优先；mp 渠道回落存量 users.openid（排除 phone_ 占位），零迁移兼容旧数据
//
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - channelCode: 渠道标识
//
// 返回值：
//   - string: 微信 openid，不可用时为空串
//   - string: 微信 unionid，可为空串
//   - error: 查询异常时返回错误
func (s *ChannelService) GetUserChannelOpenid(ctx context.Context, userID int64, channelCode string) (string, string, error) {
	if userID <= 0 {
		return "", "", nil
	}

	var row model.UserChannelIdentity
	err := s.db.GetDb().WithContext(ctx).
		Where("user_id = ? AND channel_code = ?", userID, channelCode).
		First(&row).Error
	if err == nil && row.Openid != "" {
		return row.Openid, row.Unionid, nil
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return "", "", fmt.Errorf("查询用户渠道身份失败: %w", err)
	}

	// 身份表无记录：mp 渠道回落存量 users.openid 口径
	if channelCode == channel.MP {
		var u model.User
		if err := s.db.GetDb().WithContext(ctx).Select("openid, union_id").First(&u, userID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return "", "", nil
			}
			return "", "", fmt.Errorf("查询用户信息失败: %w", err)
		}
		// 手机号登录的 phone_<手机号> 占位不是真实 openid，不可用于 JSAPI 支付
		if u.Openid != "" && !isPhoneOpenid(u.Openid) {
			return u.Openid, ptrString(u.Unionid), nil
		}
	}
	return "", "", nil
}

// UpsertUserChannelIdentity 幂等写入/更新用户在指定渠道的微信身份
// 登录链路每次换取最新 openid 后调用，重复登录仅更新 openid/unionid
//
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//   - channelCode: 渠道标识
//   - openid: 该渠道下的微信 openid
//   - unionid: 微信 unionid，可为空
//
// 返回值：
//   - error: 校验或写入失败时返回错误
func (s *ChannelService) UpsertUserChannelIdentity(ctx context.Context, userID int64, channelCode, openid, unionid string) error {
	if userID <= 0 || !channel.IsValid(channelCode) || openid == "" {
		return bizerr.New(codes.InvalidArgument, "参数错误")
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	var row model.UserChannelIdentity
	err := s.db.GetDb().WithContext(ctx).
		Where("user_id = ? AND channel_code = ?", userID, channelCode).
		First(&row).Error
	switch err {
	case nil:
		// 内容一致时跳过写入，减少登录链路的无效更新
		if row.Openid == openid && row.Unionid == unionid {
			return nil
		}
		return s.db.GetDb().WithContext(ctx).Model(&model.UserChannelIdentity{}).
			Where("id = ?", row.ID).
			Updates(map[string]interface{}{
				"openid":     openid,
				"unionid":    unionid,
				"updated_at": now,
			}).Error
	case gorm.ErrRecordNotFound:
		row = model.UserChannelIdentity{
			UserID:      userID,
			ChannelCode: channelCode,
			Openid:      openid,
			Unionid:     unionid,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		// 并发登录唯一索引冲突时视为已存在，下轮登录会走更新分支
		if cerr := s.db.GetDb().WithContext(ctx).Create(&row).Error; cerr != nil && !isDuplicateKeyErr(cerr) {
			return fmt.Errorf("写入用户渠道身份失败: %w", cerr)
		}
		return nil
	default:
		return fmt.Errorf("查询用户渠道身份失败: %w", err)
	}
}
