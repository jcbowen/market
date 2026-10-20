package service

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

// ToSiteConfigInfo 将站点设置模型转换为 proto 站点设置
//
// 参数：
//   - c: 站点设置模型，可为 nil
//
// 返回值：
//   - *user.SiteConfigInfo: proto 站点设置，入参为 nil 时返回 nil
func ToSiteConfigInfo(c *model.SiteConfig) *user.SiteConfigInfo {
	if c == nil {
		return nil
	}
	return &user.SiteConfigInfo{
		Id:             c.ID,
		BaseUrl:        c.BaseURL,
		SiteName:       c.SiteName,
		LogoUrl:        c.LogoURL,
		FaviconUrl:     c.FaviconURL,
		CopyrightText:  c.CopyrightText,
		IcpNumber:      c.ICPNumber,
		SeoDescription: c.SeoDescription,
		SeoKeywords:    c.SeoKeywords,
		Version:        c.Version,
		UpdatedAt:      c.UpdatedAt,
	}
}

// normalizeBaseURL 校验并归一化部署域名基址
// 只接受 scheme://host[:port] 形态：带路径/查询会污染派生出来的回调地址与附件外链
//
// 参数：
//   - raw: 原始输入，空串表示清空配置
//
// 返回值：
//   - string: 归一化结果（去尾斜杠），空串表示未配置
//   - error: 校验不通过时返回可透传的业务错误
func normalizeBaseURL(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", nil
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "", bizerr.New(codes.InvalidArgument, "部署域名格式不正确，请填写形如 https://shop.example.com 的地址")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", bizerr.New(codes.InvalidArgument, "部署域名仅支持 http 或 https 协议")
	}
	if strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", bizerr.New(codes.InvalidArgument, "部署域名请勿包含路径或参数，只填到域名（可带端口）即可")
	}
	return strings.ToLower(u.Scheme) + "://" + u.Host, nil
}

// SiteService 站点设置业务服务
// 全平台单行配置（site_configs 固定 id=1），仅超管经网关写入
type SiteService struct {
	db *mysql.Instance
}

// NewSiteService 创建站点设置业务服务
//
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - *SiteService: 站点设置业务服务实例
func NewSiteService(db *mysql.Instance) *SiteService {
	return &SiteService{db: db}
}

// getOrCreateRow 读取站点设置单行，不存在时创建
// 按固定主键定位，并发创建会命中主键冲突后重读，不会产生多行
//
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - *model.SiteConfig: 站点设置行
//   - error: 读写异常时返回错误
func (s *SiteService) getOrCreateRow(ctx context.Context) (*model.SiteConfig, error) {
	var row model.SiteConfig
	err := s.db.GetDb().WithContext(ctx).First(&row, model.SiteConfigID).Error
	if err == nil {
		return &row, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("查询站点设置失败: %w", err)
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	row = model.SiteConfig{ID: model.SiteConfigID, CreatedAt: now, UpdatedAt: now}
	if cerr := s.db.GetDb().WithContext(ctx).Create(&row).Error; cerr != nil {
		// 并发下另一请求已建行：主键冲突视为已存在，重读即可
		if !isDuplicateKeyErr(cerr) {
			return nil, fmt.Errorf("初始化站点设置失败: %w", cerr)
		}
		if rerr := s.db.GetDb().WithContext(ctx).First(&row, model.SiteConfigID).Error; rerr != nil {
			return nil, fmt.Errorf("查询站点设置失败: %w", rerr)
		}
	}
	return &row, nil
}

// GetSiteConfig 读取站点设置（首次读取时建行，保证调用方总能拿到非 nil 结果）
//
// 参数：
//   - ctx: 上下文
//
// 返回值：
//   - *model.SiteConfig: 站点设置行
//   - error: 读写异常时返回错误
func (s *SiteService) GetSiteConfig(ctx context.Context) (*model.SiteConfig, error) {
	return s.getOrCreateRow(ctx)
}

// SaveSiteConfig 保存站点设置（覆盖式写入，版本号递增）
// 版本号递增是网关内存缓存与前端 favicon 缓存的失效依据，必须每次保存都 +1
//
// 参数：
//   - ctx: 上下文
//   - req: 保存请求，字段传空串即清空该配置
//
// 返回值：
//   - *model.SiteConfig: 保存后的站点设置行
//   - error: 校验或写入失败时返回错误
func (s *SiteService) SaveSiteConfig(ctx context.Context, req *user.SaveSiteConfigReq) (*model.SiteConfig, error) {
	baseURL, err := normalizeBaseURL(req.BaseUrl)
	if err != nil {
		return nil, err
	}

	siteName := strings.TrimSpace(req.SiteName)
	if len([]rune(siteName)) > 32 {
		return nil, bizerr.New(codes.InvalidArgument, "站点名称长度不能超过 32 个字符")
	}
	if len([]rune(req.SeoDescription)) > 200 || len([]rune(req.SeoKeywords)) > 200 ||
		len([]rune(req.CopyrightText)) > 200 || len([]rune(req.IcpNumber)) > 64 {
		return nil, bizerr.New(codes.InvalidArgument, "站点设置文本字段超长，请精简后重试")
	}
	logoURL := strings.TrimSpace(req.LogoUrl)
	faviconURL := strings.TrimSpace(req.FaviconUrl)
	if err := validateAssetURL(logoURL); err != nil {
		return nil, err
	}
	if err := validateAssetURL(faviconURL); err != nil {
		return nil, err
	}

	row, err := s.getOrCreateRow(ctx)
	if err != nil {
		return nil, err
	}
	row.BaseURL = baseURL
	row.SiteName = siteName
	row.LogoURL = logoURL
	row.FaviconURL = faviconURL
	row.CopyrightText = strings.TrimSpace(req.CopyrightText)
	row.ICPNumber = strings.TrimSpace(req.IcpNumber)
	row.SeoDescription = strings.TrimSpace(req.SeoDescription)
	row.SeoKeywords = strings.TrimSpace(req.SeoKeywords)
	row.Version++
	row.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")

	if err := s.db.GetDb().WithContext(ctx).Save(row).Error; err != nil {
		return nil, fmt.Errorf("保存站点设置失败: %w", err)
	}
	return row, nil
}

// validateAssetURL 校验品牌资源外链格式
// 允许站内绝对路径（/uploads/...）与 http(s) 外链，拒绝其它形态避免注入
//
// 参数：
//   - raw: 待校验 URL，空串表示未配置
//
// 返回值：
//   - error: 格式不合法时返回可透传的业务错误
func validateAssetURL(raw string) error {
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "/") {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return bizerr.New(codes.InvalidArgument, "logo 与 favicon 地址需为站内路径或 http(s) 外链")
	}
	return nil
}
