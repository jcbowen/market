package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/timeutil"
	"github.com/jcbowen/market/src/backend/services/payment/model"
	wegopayment "github.com/jcbowen/wego/payment"

	"google.golang.org/grpc/codes"

	alipaysdk "github.com/jcbowen/alipay-sdk-go-all"
)

// payCertMaxSize 证书文件大小上限（64KB，PEM 文件通常不超过 10KB）
const payCertMaxSize = 64 * 1024

// payCertAllowedExts 证书文件允许的扩展名
var payCertAllowedExts = map[string]bool{".pem": true, ".key": true, ".txt": true, ".crt": true}

// checkNotifyURL 校验异步通知地址格式：允许空（未配置，就绪判定自然为未就绪），
// 非空时必须是带域名的 http(s) 绝对地址。
// 通知地址写错（漏协议、粘贴成相对路径、带空格换行）时，支付宝/微信会持续重试回调但永远打不到网关，
// 表现为「用户已付款但订单未支付」，属于只能在保存时拦住的配置错误，故在服务端兜底校验而不只依赖前端表单
//
// 参数：
//   - label: 字段中文名，用于错误提示
//   - raw: 通知地址
//
// 返回值：
//   - error: 格式非法时返回业务错误（InvalidArgument），合法返回 nil
func checkNotifyURL(label, raw string) error {
	u := strings.TrimSpace(raw)
	if u == "" {
		return nil
	}
	// 粘贴带来的空格/换行会让支付宝回调打到错误路径，url.Parse 不报错但请求必然 404，须在此拦住
	if strings.ContainsAny(u, " \t\r\n") {
		return bizerr.New(codes.InvalidArgument, fmt.Sprintf("%s不能包含空格或换行", label))
	}
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return bizerr.New(codes.InvalidArgument, fmt.Sprintf("%s必须是 http(s) 开头的完整地址", label))
	}
	return nil
}

// missingDeployBaseReason 凭证齐全、仅缺异步通知地址时的未就绪原因
// 通知地址是只读的部署域名派生字段（回调路径固定在网关路由上，不接受人工填写），
// 未配置显式部署域名时必然为空；若仍回「必填配置不完整」，管理员会对着一个不可编辑的字段无从下手，
// 故单独指向真正该改的地方
const missingDeployBaseReason = "缺少异步通知地址：该地址由部署域名派生，请在站点设置填写域名，或部署时设置 SITE_BASE_URL 环境变量"

// wechatCredentialsReady 微信支付凭证齐全度（不含由部署域名派生的异步通知地址）
//
// 参数：
//   - w: 微信支付配置载体
//
// 返回值：
//   - bool: 除通知地址外全部必填项齐全返回 true
func wechatCredentialsReady(w config.WechatConfig) bool {
	return w.MchID != "" && w.APIV3Key != "" && w.CertSerialNo != "" && w.PrivateKey != ""
}

// alipayCredentialsReady 支付宝凭证齐全度（按加签方式取对应必填集，不含异步通知地址）
//
// 参数：
//   - a: 支付宝配置载体
//
// 返回值：
//   - bool: 除通知地址外全部必填项齐全返回 true
func alipayCredentialsReady(a config.AlipayConfig) bool {
	if a.AppID == "" || a.PrivateKey == "" {
		return false
	}
	if a.IsCertMode() {
		return a.AppCert != "" && a.AlipayPublicCert != "" && a.AlipayRootCert != ""
	}
	return a.AlipayPublicKey != ""
}

// payClientRegistry 支付配置内存注册表
// 缓存 pay_config 全量行，并仅对「启用 + 凭证完整」的行构建渠道客户端；
// version 由全部行的 updated_at 拼接而成，任一行增删改都会失配并触发整表重建
// （配置行总数是个位数级，整表重建成本可忽略，换取一致性简单可靠）
type payClientRegistry struct {
	version string
	rows    map[int64]*model.PayConfig
	wechat  map[int64]*wegopayment.Client
	alipay  map[int64]*alipaysdk.GenericExecuteApi
}

// registryVersion 计算注册表版本号：行数 + 各行 updated_at 拼接
//
// 参数：
//   - rows: 全量配置行（按 id 升序）
//
// 返回值：
//   - string: 版本串
func registryVersion(rows []*model.PayConfig) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(len(rows)))
	for _, row := range rows {
		b.WriteByte('|')
		b.WriteString(row.UpdatedAt)
	}
	return b.String()
}

// nextPayConfigVersion 生成严格大于当前值的乐观锁版本号（「Y-m-d H:i:s」形态）
// updated_at 列只有秒精度，同一秒内的连续保存会得到相同版本号，两处后果都不能接受：
//   - 乐观锁失效：旧版本请求与库中版本相同，覆盖不会被拒（双击开关、上传证书后紧接保存都落在同一秒）
//   - 注册表不重建：registryVersion 按各行 updated_at 拼串比对，版本没变即视为配置未变，
//     所有实例继续沿用旧渠道客户端（如刚上传的新私钥不生效、启停开关静默无效）
//
// 故新版本取 max(当前时间, 旧版本 + 1 秒)，不依赖「墙钟必然前进」这一假设
//
// 参数：
//   - prev: 当前版本号（规范化形态，新增行为空串）
//
// 返回值：
//   - string: 新版本号
func nextPayConfigVersion(prev string) string {
	// 先截断到秒再比较：now 的亚秒部分会让「同一秒」被判成 After，
	// 格式化后又退回同一版本号
	now := time.Now().Truncate(time.Second)
	if prevTime, err := time.ParseInLocation("2006-01-02 15:04:05", prev, time.Local); err == nil && !now.After(prevTime) {
		now = prevTime.Add(time.Second)
	}
	return now.Format("2006-01-02 15:04:05")
}

// normalizePayConfigRow 把配置行的 datetime 列归一化为「Y-m-d H:i:s」形态
// parseTime DSN 下字符串字段读回的是 RFC3339 字面量（如 2026-10-31T00:44:51+08:00），
// 而写入侧统一是「Y-m-d H:i:s」；updated_at 兼作乐观锁版本号，读写两侧形态不一致时，
// 刚保存成功紧接着再保存会被误判为版本冲突（上传证书后随后的保存同样会被拒），故读取即归一化
//
// 参数：
//   - row: 配置行（原地修改）
//
// 返回值：
//   - *model.PayConfig: 同一行对象，便于链式返回
func normalizePayConfigRow(row *model.PayConfig) *model.PayConfig {
	row.CreatedAt = timeutil.NormalizeRFC3339(row.CreatedAt)
	row.UpdatedAt = timeutil.NormalizeRFC3339(row.UpdatedAt)
	return row
}

// GetPayConfigs 查询全部支付配置行（按 id 升序）
//
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - []*model.PayConfig: 配置行列表
//   - error: 查询失败时返回错误
func (s *PaymentService) GetPayConfigs(ctx context.Context) ([]*model.PayConfig, error) {
	// db 未初始化（离线单测直接构造服务实例并注入 registry 缓存的场景）视为查询失败，
	// 调用方 getRegistryCached 会回落缓存，不 panic
	if s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	var rows []*model.PayConfig
	if err := s.db.GetDb().WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取支付配置失败: %w", err)
	}
	for _, row := range rows {
		normalizePayConfigRow(row)
	}
	return rows, nil
}

// GetPayConfigRow 按 ID 查询单条支付配置
//
// 参数：
//   - ctx: 上下文
//   - id: 配置行 ID
//
// 返回值：
//   - *model.PayConfig: 配置行，不存在时为 nil
//   - error: 查询失败时返回错误
func (s *PaymentService) GetPayConfigRow(ctx context.Context, id int64) (*model.PayConfig, error) {
	var row model.PayConfig
	if err := s.db.GetDb().WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, fmt.Errorf("读取支付配置失败: %w", err)
	}
	return normalizePayConfigRow(&row), nil
}

// SavePayConfigRow 新增（ID=0）或更新一条支付配置
// 更新走 updated_at 乐观锁（调用方携带读取时版本，冲突即拒绝整行覆写）；
// 保存前校验「行身份键」在同平台内不重复（见 checkPayConfigIdentity）；
// 保存后本实例立即热重建，其余实例在下次支付调用时按版本号自动刷新
//
// 参数：
//   - ctx: 上下文
//   - in: proto 配置行（凭证空值口径：Secret/密钥/私钥为空时保留原值）
//
// 返回值：
//   - *model.PayConfig: 保存后的配置行
//   - error: 校验失败或保存冲突时返回错误
func (s *PaymentService) SavePayConfigRow(ctx context.Context, in *payment.PayConfigRow) (*model.PayConfig, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, bizerr.New(codes.InvalidArgument, "配置名称不能为空")
	}
	if in.Platform != model.PayPlatformWechat && in.Platform != model.PayPlatformAlipay {
		return nil, bizerr.New(codes.InvalidArgument, "平台类型不合法，仅支持 wechat/alipay")
	}

	var row model.PayConfig
	// prevVersion 记录读取时库中版本：下方比较校验是先读后写存在窗口
	// （两请求读到同一版本后都会通过比较），原子性最终由保存时的
	// WHERE updated_at=prevVersion 条件更新收口，空版本调用方（未传 UpdatedAt）同样兼容
	prevVersion := ""
	if in.Id > 0 {
		existing, err := s.GetPayConfigRow(ctx, in.Id)
		if err != nil {
			return nil, err
		}
		row = *existing
		prevVersion = row.UpdatedAt
		// 乐观锁：调用方携带读取时的版本号，与库中不一致说明期间已被他人改写，
		// 拒绝整行覆写（全量覆盖式保存，后提交者会静默丢掉先提交者的改动）
		if in.UpdatedAt != "" && in.UpdatedAt != row.UpdatedAt {
			log.Printf("支付配置保存冲突：调用方版本 %s，当前版本 %s", in.UpdatedAt, row.UpdatedAt)
			return nil, bizerr.New(codes.Aborted, "支付配置已被其他操作更新，请刷新页面获取最新配置后重新提交")
		}
	}

	row.Name = strings.TrimSpace(in.Name)
	row.Platform = in.Platform
	row.Enabled = in.Enabled

	if in.Platform == model.PayPlatformWechat {
		// Secret/密钥/私钥网关查询已脱敏不回传明文：空值保留原值，避免前端回传空串时误清空
		if in.WechatAppSecret != "" {
			row.WechatAppSecret = in.WechatAppSecret
		}
		row.WechatMchID = strings.TrimSpace(in.WechatMchId)
		if in.WechatApiV3Key != "" {
			row.WechatAPIV3Key = in.WechatApiV3Key
		}
		row.WechatCertSerialNo = in.WechatCertSerialNo
		if in.WechatPrivateKey != "" {
			row.WechatPrivateKey = in.WechatPrivateKey
		}
		row.WechatNotifyURL = in.WechatNotifyUrl
		if err := checkNotifyURL("微信异步通知地址", row.WechatNotifyURL); err != nil {
			return nil, err
		}
	} else {
		row.AlipayAppID = strings.TrimSpace(in.AlipayAppId)
		if in.AlipayPrivateKey != "" {
			row.AlipayPrivateKey = in.AlipayPrivateKey
		}
		row.AlipayPublicKey = in.AlipayPublicKey
		row.AlipayNotifyURL = in.AlipayNotifyUrl
		row.AlipayReturnURL = in.AlipayReturnUrl
		row.AlipayGatewayURL = normalizeAlipayGateway(in.AlipayGatewayUrl)
		// 加签方式：空串按公钥模式落库（存量口径），cert 模式下三证书仅由上传接口维护、此处不触碰
		if in.AlipayMode == config.AlipayModeCert {
			row.AlipayMode = config.AlipayModeCert
		} else {
			row.AlipayMode = config.AlipayModePublic
		}
		if err := checkNotifyURL("支付宝异步通知地址", row.AlipayNotifyURL); err != nil {
			return nil, err
		}
	}

	if err := s.checkPayConfigIdentity(ctx, &row); err != nil {
		return nil, err
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	row.UpdatedAt = nextPayConfigVersion(prevVersion)
	if row.CreatedAt == "" {
		row.CreatedAt = now
	}

	if row.ID > 0 {
		// 条件更新代替整行 Save：WHERE 带读取时版本，与比较校验共同构成完整乐观锁，
		// 0 行说明期间已被并发改写，后提交者不再静默覆盖先提交者的改动
		res := s.db.GetDb().WithContext(ctx).Model(&model.PayConfig{}).
			Where("id = ? AND updated_at = ?", row.ID, prevVersion).
			Select("*").Updates(&row)
		if res.Error != nil {
			return nil, fmt.Errorf("保存支付配置失败: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			log.Printf("支付配置保存冲突：期望版本 %s 已被并发更新", prevVersion)
			return nil, bizerr.New(codes.Aborted, "支付配置已被其他操作更新，请刷新页面获取最新配置后重新提交")
		}
	} else {
		if err := s.db.GetDb().WithContext(ctx).Create(&row).Error; err != nil {
			return nil, fmt.Errorf("保存支付配置失败: %w", err)
		}
	}

	// 本实例立即刷新，其余实例在下次支付调用时按版本号自动刷新
	s.getRegistryCached(ctx)
	return &row, nil
}

// checkPayConfigIdentity 校验配置行的「身份键」在同平台内唯一
// 微信按商户号唯一、支付宝按 AppID 唯一，两个渠道口径不对称是有意为之（见 model.PayConfig 注释）：
//   - 微信的 AppID 是渠道身份（同一商户号可关联小程序、H5 等多个 AppID），商户号才是配置行的身份；
//     允许同商户号建多行时，各行共用 APIv3 密钥与平台证书，回调验签会在多行间随机命中错配行，
//     已付款的订单因归属校验被拒而永不入账——本校验从模型层消除这一歧义来源
//   - 支付宝的 AppID 是签名身份（一个 AppID 一套密钥对），重复建行的两份密钥必有一份是错的
//
// 只做应用层校验、不建库级唯一索引：唯一性条件依赖 platform（同列在两平台间语义不同），
// MySQL 无部分索引可表达，且凭证未填齐的行（占位行）该列为空串，会被唯一索引误伤
//
// 参数：
//   - ctx: 上下文
//   - row: 待保存的配置行（ID=0 表示新增）
//
// 返回值：
//   - error: 身份键与同平台其他行重复时返回 AlreadyExists，查询失败返回内部错误
func (s *PaymentService) checkPayConfigIdentity(ctx context.Context, row *model.PayConfig) error {
	var column, identity string
	switch row.Platform {
	case model.PayPlatformWechat:
		column, identity = "wechat_mch_id", row.WechatMchID
	case model.PayPlatformAlipay:
		column, identity = "alipay_app_id", row.AlipayAppID
	}
	if identity == "" {
		// 身份键尚未填写（凭证未配齐、不参与下单），无需校验
		return nil
	}
	var conflict int64
	if err := s.db.GetDb().WithContext(ctx).Model(&model.PayConfig{}).
		Where("platform = ? AND "+column+" = ? AND id <> ?", row.Platform, identity, row.ID).
		Count(&conflict).Error; err != nil {
		return fmt.Errorf("校验支付配置唯一性失败: %w", err)
	}
	if conflict == 0 {
		return nil
	}
	if row.Platform == model.PayPlatformWechat {
		return bizerr.New(codes.AlreadyExists,
			fmt.Sprintf("商户号 %s 已有支付配置：一个商户号只建一条微信配置，小程序/H5 等各渠道的 AppID 请到渠道管理分别配置", identity))
	}
	return bizerr.New(codes.AlreadyExists,
		fmt.Sprintf("支付宝 AppID %s 已有支付配置：一个 AppID 对应一套密钥，请勿重复创建", identity))
}

// DeletePayConfigRow 删除一条支付配置
// 渠道绑定引用（client_channels.pay_methods）由网关在删除前校验，服务层只删行本身；
// 删除后热重建注册表，该配置的渠道客户端随之失效
//
// 参数：
//   - ctx: 上下文
//   - id: 配置行 ID
//
// 返回值：
//   - error: 配置不存在或删除失败时返回错误
func (s *PaymentService) DeletePayConfigRow(ctx context.Context, id int64) error {
	res := s.db.GetDb().WithContext(ctx).Delete(&model.PayConfig{}, id)
	if res.Error != nil {
		return fmt.Errorf("删除支付配置失败: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return bizerr.New(codes.NotFound, "支付配置不存在")
	}
	s.getRegistryCached(ctx)
	return nil
}

// UploadPayCert 上传支付证书文件到指定配置行
// 校验扩展名与大小后，将文件内容写入对应平台的证书字段
// 成功后回传最新配置行：上传推进了乐观锁版本（updated_at），调用方若不同步新版本，
// 编辑会话随后的保存会被「已被其他操作更新」拒绝——返回行让前端直接渲染，乐观锁机制原样保留
//
// 写入用「仅证书列 + WHERE id AND updated_at=读取时版本」的条件更新，不用整行 Save：
// 上传请求只带证书内容，整行 Save 会把读取时快照的其他列（回调地址/名称/启停状态等）
// 原样写回，覆盖这期间并发保存的改动；且版本号也来自同一快照，
// 会把别人刚推进的版本写回旧值，使注册表按「版本未变」复用缓存而漏重建渠道客户端
//
// 参数：
//   - ctx: 上下文
//   - req: 上传请求（config_id 定位配置行，cert_type 决定写入字段）
//
// 返回值：
//   - *model.PayConfig: 上传后的最新配置行
//   - error: 校验失败、版本冲突或保存失败时返回错误
func (s *PaymentService) UploadPayCert(ctx context.Context, req *payment.UploadPayCertReq) (*model.PayConfig, error) {
	if req.ConfigId <= 0 {
		return nil, fmt.Errorf("缺少支付配置 ID")
	}
	if len(req.Content) == 0 {
		return nil, fmt.Errorf("证书文件内容为空")
	}
	if len(req.Content) > payCertMaxSize {
		return nil, fmt.Errorf("证书文件超过大小限制（最大 %dKB）", payCertMaxSize/1024)
	}
	ext := strings.ToLower(req.Filename)
	if idx := strings.LastIndex(ext, "."); idx >= 0 {
		ext = ext[idx:]
	} else {
		ext = ""
	}
	if !payCertAllowedExts[ext] {
		return nil, fmt.Errorf("仅支持上传 %v 格式的证书文件", allowedExtList())
	}

	row, err := s.GetPayConfigRow(ctx, req.ConfigId)
	if err != nil {
		return nil, err
	}

	content := string(req.Content)
	var column string
	switch {
	case row.Platform == model.PayPlatformWechat && req.CertType == "private_key":
		column = "wechat_private_key"
	case row.Platform == model.PayPlatformAlipay && req.CertType == "private_key":
		// 支付宝证书内容可解析性校验：FailedPrecondition 使网关原样透传具体原因，便于定位传错的文件
		if msg := checkAlipayUploadContent(req.CertType, content); msg != "" {
			return nil, bizerr.New(codes.FailedPrecondition, msg)
		}
		column = "alipay_private_key"
	// 证书模式三证书：内容与私钥同类处理，仅此处写入，保存请求不触碰
	case row.Platform == model.PayPlatformAlipay && req.CertType == "app_cert":
		if msg := checkAlipayUploadContent(req.CertType, content); msg != "" {
			return nil, bizerr.New(codes.FailedPrecondition, msg)
		}
		column = "alipay_app_cert"
	case row.Platform == model.PayPlatformAlipay && req.CertType == "alipay_public_cert":
		if msg := checkAlipayUploadContent(req.CertType, content); msg != "" {
			return nil, bizerr.New(codes.FailedPrecondition, msg)
		}
		column = "alipay_public_cert"
	case row.Platform == model.PayPlatformAlipay && req.CertType == "root_cert":
		if msg := checkAlipayUploadContent(req.CertType, content); msg != "" {
			return nil, bizerr.New(codes.FailedPrecondition, msg)
		}
		column = "alipay_root_cert"
	default:
		return nil, fmt.Errorf("该配置平台不支持上传 %s 类型证书", req.CertType)
	}

	prevVersion := row.UpdatedAt
	res := s.db.GetDb().WithContext(ctx).Model(&model.PayConfig{}).
		Where("id = ? AND updated_at = ?", row.ID, prevVersion).
		Updates(map[string]any{
			column:       content,
			"updated_at": nextPayConfigVersion(prevVersion),
		})
	if res.Error != nil {
		return nil, fmt.Errorf("保存证书失败: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		log.Printf("证书上传冲突：期望版本 %s 已被并发更新 config_id=%d", prevVersion, row.ID)
		return nil, bizerr.New(codes.Aborted, "支付配置已被其他操作更新，请刷新页面获取最新配置后重新上传")
	}

	// 本实例立即刷新，其余实例在下次支付调用时按版本号自动刷新
	s.getRegistryCached(ctx)
	return s.reloadPayConfigRow(ctx, row.ID)
}

// SetPayConfigEnabled 单独启停一条支付配置：仅更新 enabled 字段，不做整行覆写
// 列表开关走本接口而非全量保存：开关场景只持有 enabled + 版本号，
// 全量保存会因名称等必填字段缺失被拒（「配置名称不能为空」），
// 即便补齐也会把编辑会话中尚未同步的改动一并提交
// updated_at 乐观锁：与库中版本不一致说明期间已被改写，拒绝静默覆写；
// 启停同样推进版本号，成功后回传最新行供前端渲染
//
// 参数：
//   - ctx: 上下文
//   - req: 启停入参（id 定位配置行，enabled 为目标状态，updated_at 为读取时版本）
//
// 返回值：
//   - *model.PayConfig: 启停后的最新配置行
//   - error: 配置不存在、版本冲突或保存失败时返回业务错误
func (s *PaymentService) SetPayConfigEnabled(ctx context.Context, req *payment.SetPayConfigEnabledReq) (*model.PayConfig, error) {
	if req.Id <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "缺少支付配置 ID")
	}

	var row model.PayConfig
	if err := s.db.GetDb().WithContext(ctx).First(&row, req.Id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, bizerr.New(codes.NotFound, "支付配置不存在")
		}
		return nil, fmt.Errorf("读取支付配置失败: %w", err)
	}
	normalizePayConfigRow(&row)
	if req.UpdatedAt != "" && req.UpdatedAt != row.UpdatedAt {
		log.Printf("支付配置启停冲突：调用方版本 %s，当前版本 %s", req.UpdatedAt, row.UpdatedAt)
		return nil, bizerr.New(codes.Aborted, "支付配置已被其他操作更新，请刷新页面获取最新配置后重新提交")
	}

	// 条件更新代替整行 Save：只动 enabled 与乐观锁版本，
	// 上传/保存等并发操作推进的其余字段不会被本接口回写覆盖；
	// WHERE 同时带读取时版本收口先读后写窗口，0 行说明期间已被并发改写
	res := s.db.GetDb().WithContext(ctx).Model(&model.PayConfig{}).
		Where("id = ? AND updated_at = ?", req.Id, row.UpdatedAt).
		Updates(map[string]any{
			"enabled":    req.Enabled,
			"updated_at": nextPayConfigVersion(row.UpdatedAt),
		})
	if res.Error != nil {
		return nil, fmt.Errorf("更新支付配置状态失败: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		log.Printf("支付配置启停冲突：期望版本 %s 已被并发更新", row.UpdatedAt)
		return nil, bizerr.New(codes.Aborted, "支付配置已被其他操作更新，请刷新页面获取最新配置后重新提交")
	}

	s.getRegistryCached(ctx)
	return s.reloadPayConfigRow(ctx, req.Id)
}

// reloadPayConfigRow 按 ID 重新读取配置行（启停条件更新后回传最新版本用）
//
// 参数：
//   - ctx: 上下文
//   - id: 配置行 ID
//
// 返回值：
//   - *model.PayConfig: 最新配置行
//   - error: 读取失败时返回错误
func (s *PaymentService) reloadPayConfigRow(ctx context.Context, id int64) (*model.PayConfig, error) {
	var row model.PayConfig
	if err := s.db.GetDb().WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, fmt.Errorf("读取支付配置失败: %w", err)
	}
	return normalizePayConfigRow(&row), nil
}

// getRegistryCached 获取支付配置注册表（进程内缓存 + 每次调用按版本核对）
// 缓存版本与数据库行版本一致时复用缓存并保留已构建的渠道客户端，不一致时整表重建；
// 数据库查询失败时降级返回当前缓存，避免 DB 抖动阻断支付链路
// 这里刻意不做 TTL 免查库：调用点全在下单/回调/退款链路（非列表逐行，成本是个位数行查询），
// 而支付密钥、回调地址变更后必须立即生效——若缓存期内仍用旧配置，
// 多实例部署下会出现新密钥签名的单被旧客户端验签失败、或下单用已废弃渠道等资金链路故障
//
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - *payClientRegistry: 注册表（缓存或最新），均不可用时返回 nil
func (s *PaymentService) getRegistryCached(ctx context.Context) *payClientRegistry {
	rows, err := s.GetPayConfigs(ctx)
	if err != nil {
		log.Printf("读取支付配置失败（使用缓存兜底）: %v", err)
		s.payMu.RLock()
		defer s.payMu.RUnlock()
		return s.registry
	}

	version := registryVersion(rows)
	s.payMu.RLock()
	unchanged := s.registry != nil && s.registry.version == version
	cached := s.registry
	s.payMu.RUnlock()
	if unchanged {
		return cached
	}

	s.payMu.Lock()
	defer s.payMu.Unlock()
	// 双重检查：等待锁期间其他协程可能已完成刷新
	if s.registry == nil || s.registry.version != version {
		s.applyPayConfig(rows)
	}
	return s.registry
}

// applyPayConfig 按全量配置行重建注册表：更新缓存行并逐行构建渠道客户端
// 仅「启用 + 凭证完整」的行进入客户端映射；微信与支付宝各自独立构建互不影响；
// 调用方必须已持有 s.payMu 写锁
//
// 参数：
//   - rows: 全量配置行
func (s *PaymentService) applyPayConfig(rows []*model.PayConfig) {
	reg := &payClientRegistry{
		version: registryVersion(rows),
		rows:    make(map[int64]*model.PayConfig, len(rows)),
		wechat:  make(map[int64]*wegopayment.Client),
		alipay:  make(map[int64]*alipaysdk.GenericExecuteApi),
	}
	for i := range rows {
		row := rows[i]
		reg.rows[row.ID] = row
		if !row.Enabled {
			continue
		}
		switch row.Platform {
		case model.PayPlatformWechat:
			wechat := wechatConfigOf(row)
			if !wechat.IsPaymentConfigured() {
				continue
			}
			// NewClientE 在配置异常时返回错误，失败时跳过该行，C 端渲染会相应隐藏微信支付。
			// 不传 AppID：微信 AppID 属渠道身份，下单时按请求级入参传入（见 buildWechatPayParams）
			client, err := wegopayment.NewClientE(&wegopayment.Config{
				MchID:        wechat.MchID,
				APIV3Key:     wechat.APIV3Key,
				CertSerialNo: wechat.CertSerialNo,
				PrivateKey:   wechat.PrivateKey,
				NotifyURL:    wechat.NotifyURL,
			})
			if err != nil {
				log.Printf("重建微信支付客户端失败 config_id=%d: %v", row.ID, err)
				continue
			}
			reg.wechat[row.ID] = client
		case model.PayPlatformAlipay:
			// 支付宝执行器：公钥模式（AppID + 应用私钥 + 支付宝公钥），
			// 配置不完整或构建失败时跳过该行，C 端渲染会相应隐藏支付宝支付
			if executor := buildAlipayExecutor(alipayConfigOf(row)); executor != nil {
				reg.alipay[row.ID] = executor
			}
		}
	}
	s.registry = reg
}

// ListActivePayMethods 列出启用且就绪的支付配置摘要（C 端渲染过滤与下单解析用）
// 就绪口径：启用 + 凭证齐全 + 客户端构建成功
//
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - []*payment.ActivePayMethod: 活跃支付配置列表
func (s *PaymentService) ListActivePayMethods(ctx context.Context) []*payment.ActivePayMethod {
	reg := s.getRegistryCached(ctx)
	if reg == nil {
		return nil
	}
	s.payMu.RLock()
	defer s.payMu.RUnlock()
	methods := make([]*payment.ActivePayMethod, 0, len(reg.rows))
	for id, row := range reg.rows {
		if !row.Enabled {
			continue
		}
		switch row.Platform {
		case model.PayPlatformWechat:
			if _, ok := reg.wechat[id]; !ok {
				continue
			}
		case model.PayPlatformAlipay:
			if _, ok := reg.alipay[id]; !ok {
				continue
			}
		}
		methods = append(methods, &payment.ActivePayMethod{
			ConfigId: id,
			Platform: row.Platform,
		})
	}
	return methods
}

// resolveActivePayConfig 校验配置 ID 对应平台且启用就绪，返回配置行
// 下单（CreatePayment pay_type 1/2/5/6）与付款码扣款的统一入口校验：
// 配置不存在/停用/平台不匹配/客户端未构建（凭证不齐）均拒绝
//
// 参数：
//   - ctx: 上下文
//   - configID: 配置行 ID
//   - platform: 期望平台（wechat/alipay）
//
// 返回值：
//   - *model.PayConfig: 通过校验的配置行
//   - error: 校验失败时返回业务错误
func (s *PaymentService) resolveActivePayConfig(ctx context.Context, configID int64, platform string) (*model.PayConfig, error) {
	if configID <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "缺少支付配置，请先在渠道管理中绑定支付方式")
	}
	reg := s.getRegistryCached(ctx)
	if reg == nil {
		return nil, bizerr.New(codes.FailedPrecondition, "支付配置不可用，请稍后重试")
	}
	s.payMu.RLock()
	row := reg.rows[configID]
	s.payMu.RUnlock()
	if row == nil || row.Platform != platform {
		return nil, bizerr.New(codes.FailedPrecondition, "支付配置不存在或与支付方式不匹配")
	}
	if !row.Enabled {
		return nil, bizerr.New(codes.FailedPrecondition, "该支付方式已停用，请更换支付方式")
	}
	if platform == model.PayPlatformWechat {
		if _, ok := reg.wechat[configID]; !ok {
			return nil, bizerr.New(codes.FailedPrecondition, "微信支付未配置完整，无法支付")
		}
	} else {
		if _, ok := reg.alipay[configID]; !ok {
			return nil, bizerr.New(codes.FailedPrecondition, "支付宝支付未配置完整，无法支付")
		}
	}
	return row, nil
}

// getWechatPayClientFor 获取指定配置的微信支付客户端快照
// 读取时持有读锁；客户端指针一经发布即不再原地修改，返回后可安全在锁外调用
//
// 参数：
//   - ctx: 上下文
//   - configID: 配置行 ID
//
// 返回值：
//   - *wegopayment.Client: 微信支付客户端，未配置或配置不完整时为 nil
func (s *PaymentService) getWechatPayClientFor(ctx context.Context, configID int64) *wegopayment.Client {
	reg := s.getRegistryCached(ctx)
	if reg == nil {
		return nil
	}
	s.payMu.RLock()
	defer s.payMu.RUnlock()
	return reg.wechat[configID]
}

// getAlipayExecutorFor 获取指定配置的支付宝执行器快照
// 读取时持有读锁；执行器指针一经发布即不再原地修改，返回后可安全在锁外调用
//
// 参数：
//   - ctx: 上下文
//   - configID: 配置行 ID
//
// 返回值：
//   - *alipaysdk.GenericExecuteApi: 支付宝执行器，未配置或配置不完整时为 nil
func (s *PaymentService) getAlipayExecutorFor(ctx context.Context, configID int64) *alipaysdk.GenericExecuteApi {
	reg := s.getRegistryCached(ctx)
	if reg == nil {
		return nil
	}
	s.payMu.RLock()
	defer s.payMu.RUnlock()
	return reg.alipay[configID]
}

// getWechatConfigFor 获取指定配置的微信支付配置载体（H5 主页地址推导、回调商户号比对用）
//
// 参数：
//   - ctx: 上下文
//   - configID: 配置行 ID
//
// 返回值：
//   - config.WechatConfig: 微信支付配置，配置不存在时返回零值
func (s *PaymentService) getWechatConfigFor(ctx context.Context, configID int64) config.WechatConfig {
	reg := s.getRegistryCached(ctx)
	if reg == nil {
		return config.WechatConfig{}
	}
	s.payMu.RLock()
	defer s.payMu.RUnlock()
	row := reg.rows[configID]
	if row == nil {
		return config.WechatConfig{}
	}
	return wechatConfigOf(row)
}

// getAlipayConfigFor 获取指定配置的支付宝配置载体（回调验签公钥取用）
//
// 参数：
//   - ctx: 上下文
//   - configID: 配置行 ID
//
// 返回值：
//   - config.AlipayConfig: 支付宝配置，配置不存在时返回零值
func (s *PaymentService) getAlipayConfigFor(ctx context.Context, configID int64) config.AlipayConfig {
	reg := s.getRegistryCached(ctx)
	if reg == nil {
		return config.AlipayConfig{}
	}
	s.payMu.RLock()
	defer s.payMu.RUnlock()
	row := reg.rows[configID]
	if row == nil {
		return config.AlipayConfig{}
	}
	return alipayConfigOf(row)
}

// allWechatClients 返回全部就绪微信客户端快照（回调验签遍历用）
//
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - map[int64]*wegopayment.Client: 配置 ID → 客户端
func (s *PaymentService) allWechatClients(ctx context.Context) map[int64]*wegopayment.Client {
	reg := s.getRegistryCached(ctx)
	if reg == nil {
		return nil
	}
	s.payMu.RLock()
	defer s.payMu.RUnlock()
	out := make(map[int64]*wegopayment.Client, len(reg.wechat))
	for id, client := range reg.wechat {
		out[id] = client
	}
	return out
}

// alipayConfigByAppID 按支付宝 AppID 定位配置（回调验签路由用）
// 同 AppID 命中多行时取 ID 最小的一行：map 遍历序随机，不加这层确定性就会让
// 「用哪行的公钥验签」成为不确定事件（保存时已按 AppID 唯一拦截，此处是历史脏行与并发的兜底）
//
// 参数：
//   - ctx: 上下文
//   - appID: 支付宝 AppID（回调参数 app_id 字段）
//
// 返回值：
//   - config.AlipayConfig: 命中的配置，未命中返回零值
//   - bool: 是否命中
func (s *PaymentService) alipayConfigByAppID(ctx context.Context, appID string) (config.AlipayConfig, bool) {
	if appID == "" {
		return config.AlipayConfig{}, false
	}
	reg := s.getRegistryCached(ctx)
	if reg == nil {
		return config.AlipayConfig{}, false
	}
	s.payMu.RLock()
	defer s.payMu.RUnlock()
	var best *model.PayConfig
	for _, row := range reg.rows {
		if row.Platform != model.PayPlatformAlipay || row.AlipayAppID != appID {
			continue
		}
		if best == nil || row.ID < best.ID {
			best = row
		}
	}
	if best == nil {
		return config.AlipayConfig{}, false
	}
	return alipayConfigOf(best), true
}

// wechatConfigOf 将支付配置行转换为微信支付配置载体
// AppID 有意不填：微信 AppID 属渠道身份（client_channels.app_id），配置行不再持有，
// 下单由请求级入参带入、回调按支付单快照校验，载体里的 AppID 对本渠道恒为空
//
// 参数：
//   - cfg: 支付配置行，可为 nil
//
// 返回值：
//   - config.WechatConfig: 微信支付配置，cfg 为 nil 时返回零值
func wechatConfigOf(cfg *model.PayConfig) config.WechatConfig {
	if cfg == nil {
		return config.WechatConfig{}
	}
	return config.WechatConfig{
		AppSecret:    cfg.WechatAppSecret,
		MchID:        cfg.WechatMchID,
		APIV3Key:     cfg.WechatAPIV3Key,
		CertSerialNo: cfg.WechatCertSerialNo,
		PrivateKey:   cfg.WechatPrivateKey,
		NotifyURL:    cfg.WechatNotifyURL,
	}
}

// WechatConfigStatus 计算支付配置行的微信支付就绪状态
// 就绪口径为「微信支付必填字段齐全」，与下单可用性判断保持一致
//
// 参数：
//   - cfg: 支付配置行，可为 nil
//
// 返回值：
//   - bool: 是否就绪（可安全对 C 端展示并承接下单）
//   - string: 未就绪原因，就绪时为空串
func WechatConfigStatus(cfg *model.PayConfig) (bool, string) {
	wechat := wechatConfigOf(cfg)
	if !wechat.IsPaymentConfigured() {
		if wechatCredentialsReady(wechat) {
			return false, missingDeployBaseReason
		}
		return false, "微信支付必填配置不完整（商户号、APIv3 密钥、证书序列号、商户私钥、异步通知地址均为必填；AppID 属渠道身份，请到渠道管理配置）"
	}
	return true, ""
}

// alipayConfigOf 将支付配置行转换为支付宝配置载体
//
// 参数：
//   - cfg: 支付配置行，可为 nil
//
// 返回值：
//   - config.AlipayConfig: 支付宝配置，cfg 为 nil 时返回零值
func alipayConfigOf(cfg *model.PayConfig) config.AlipayConfig {
	if cfg == nil {
		return config.AlipayConfig{}
	}
	return config.AlipayConfig{
		AppID:            cfg.AlipayAppID,
		PrivateKey:       cfg.AlipayPrivateKey,
		Mode:             cfg.AlipayMode,
		AlipayPublicKey:  cfg.AlipayPublicKey,
		NotifyURL:        cfg.AlipayNotifyURL,
		ReturnURL:        cfg.AlipayReturnURL,
		GatewayURL:       cfg.AlipayGatewayURL,
		AppCert:          cfg.AlipayAppCert,
		AlipayPublicCert: cfg.AlipayPublicCert,
		AlipayRootCert:   cfg.AlipayRootCert,
	}
}

// checkAlipayUploadContent 校验上传到支付宝证书字段的文件内容是否可解析
// 按 cert_type 使用与真实链路一致的解析路径（私钥签名自检 / 证书序列号计算 / 证书公钥提取），
// 上传即拦截「把根证书传到应用证书位置」「私钥缺字符」这类只有等到下单或回调才暴露的错误，
// 且校验失败时不写库，原有配置保持不变
//
// 参数：
//   - certType: 证书类型（private_key、app_cert、alipay_public_cert、root_cert）
//   - content: 文件内容
//
// 返回值：
//   - string: 校验失败原因，通过时为空串
func checkAlipayUploadContent(certType, content string) string {
	switch certType {
	case "private_key":
		if _, err := alipaysdk.AloneRsaSign("market_alipay_config_check", content, false); err != nil {
			return "应用私钥不可用：" + err.Error()
		}
	case "app_cert":
		if _, err := alipaysdk.CertSN(content); err != nil {
			return "应用公钥证书不可解析：" + err.Error()
		}
	case "alipay_public_cert":
		if _, err := alipaysdk.CertPublicKey(content); err != nil {
			return "支付宝公钥证书不可解析：" + err.Error()
		}
	case "root_cert":
		if _, err := alipaysdk.RootCertSN(content); err != nil {
			return "支付宝根证书不可解析：" + err.Error()
		}
	default:
		// 未知类型必须拒绝而非放行：返回空串会被调用方当作校验通过，
		// 与 UploadPayCert 写库 switch 的 default 拒绝口径保持一致
		return "不支持的证书类型: " + certType
	}
	return ""
}

// allowedExtList 生成允许的证书扩展名提示列表
//
// 返回值：
//   - []string: 扩展名列表
func allowedExtList() []string {
	exts := make([]string, 0, len(payCertAllowedExts))
	for ext := range payCertAllowedExts {
		exts = append(exts, ext)
	}
	return exts
}
