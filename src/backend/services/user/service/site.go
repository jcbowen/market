package service

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/html"

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
		FooterHtml:     c.FooterHTML,
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
	if len([]rune(req.SeoDescription)) > 200 || len([]rune(req.SeoKeywords)) > 200 {
		return nil, bizerr.New(codes.InvalidArgument, "站点设置文本字段超长，请精简后重试")
	}
	footerHTML := sanitizeFooterHTML(req.FooterHtml)
	if len([]rune(footerHTML)) > 2000 {
		return nil, bizerr.New(codes.InvalidArgument, "页脚富文本内容过长，请精简后重试")
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
	row.FooterHTML = footerHTML
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

// footerTagAttrs 页脚富文本标签白名单（值为允许保留的属性列表）
// 覆盖基础排版所需：加粗/斜体/下划线/段落/换行/行内容器与安全链接
var footerTagAttrs = map[string][]string{
	"a":     {"href"},
	"b":     {},
	"strong": {},
	"i":     {},
	"em":    {},
	"u":     {},
	"p":     {},
	"br":    {},
	"span":  {},
}

// footerVoidTags 无需闭合标签的自闭合元素
var footerVoidTags = map[string]bool{"br": true}

// footerDropTags 连同子内容整体丢弃的危险标签（其文本内容也是攻击载荷，不可解包保留）
var footerDropTags = map[string]bool{
	"script": true, "style": true, "iframe": true, "object": true,
	"embed": true, "noscript": true, "template": true, "svg": true, "math": true,
}

// sanitizeFooterHTML 按白名单清洗页脚富文本
// 白名单标签重建并仅保留安全属性；其余标签解包（保留子内容）；
// 危险标签连子树整体丢弃；文本一律 HTML 转义，防存储型 XSS
//
// 参数：
//   - raw: 管理端富文本编辑器产出的原始 HTML
//
// 返回值：
//   - string: 消毒后的 HTML，输入为空时返回空串
//
// 使用示例：
//
//	clean := sanitizeFooterHTML(`<p onclick="x()">© 2026 <script>evil()</script></p>`)
//	// => `<p>© 2026 </p>`
func sanitizeFooterHTML(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return ""
	}
	var buf bytes.Buffer
	writeFooterNodes(&buf, doc)
	return strings.TrimSpace(buf.String())
}

// writeFooterNodes 递归输出经白名单过滤的节点树
//
// 参数：
//   - buf: 输出缓冲
//   - n: 当前节点（从 document 根开始遍历）
func writeFooterNodes(buf *bytes.Buffer, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			buf.WriteString(html.EscapeString(c.Data))
		case html.ElementNode:
			if footerDropTags[c.Data] {
				continue
			}
			attrs, ok := footerSafeAttrs(c)
			if !ok {
				// 非白名单标签：解包，仅保留子内容
				writeFooterNodes(buf, c)
				continue
			}
			buf.WriteString("<" + c.Data)
			for _, a := range attrs {
				buf.WriteString(" " + a.Key + `="` + html.EscapeString(a.Val) + `"`)
			}
			buf.WriteString(">")
			writeFooterNodes(buf, c)
			if !footerVoidTags[c.Data] {
				buf.WriteString("</" + c.Data + ">")
			}
		default:
			writeFooterNodes(buf, c)
		}
	}
}

// footerSafeAttrs 提取节点白名单内的安全属性
// 链接统一追加 target="_blank"、rel="noopener noreferrer"（备案号等外链新窗口打开，
// 且不带走当前商城会话）与固定内联样式（颜色继承页脚、去下划线，覆盖 <a> 的
// 浏览器默认蓝色样式，C 端 rich-text 与管理端 v-html 展示一致）；
// href 不合规时静默剔除该属性
//
// 参数：
//   - n: 元素节点
//
// 返回值：
//   - []html.Attribute: 保留的属性列表
//   - bool: 标签是否在白名单（false 表示解包处理）
func footerSafeAttrs(n *html.Node) ([]html.Attribute, bool) {
	allowed, ok := footerTagAttrs[n.Data]
	if !ok {
		return nil, false
	}
	var out []html.Attribute
	for _, a := range n.Attr {
		if !slices.Contains(allowed, a.Key) {
			continue
		}
		if a.Key == "href" {
			a.Val = safeFooterHref(a.Val)
			if a.Val == "" {
				continue
			}
		}
		out = append(out, a)
	}
	if n.Data == "a" {
		out = append(out,
			html.Attribute{Key: "target", Val: "_blank"},
			html.Attribute{Key: "rel", Val: "noopener noreferrer"},
			html.Attribute{Key: "style", Val: "color:inherit;text-decoration:none"},
		)
	}
	return out, true
}

// safeFooterHref 校验链接目标
// 仅放行站内绝对路径（排除 // 开头的协议相对地址）与 http(s) 外链
//
// 参数：
//   - raw: 原始 href 值
//
// 返回值：
//   - string: 合法时原样返回，否则返回空串
func safeFooterHref(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return v
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return v
}

// MigrateFooterHTML 把版权/备案号两列存量数据迁移为页脚富文本 footer_html
// 启动期一次性执行：仅当 footer_html 为空且旧列仍有值时按「版权 · 备案号」转义拼接回填
// 旧列此时已不在模型中，读取走显式 SQL；迁移成功后由 main.go 负责清理旧列
//
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: 读写异常时返回错误
//
// 使用示例：
//
//	if err := service.MigrateFooterHTML(db); err != nil { log.Printf(...) }
func MigrateFooterHTML(db *mysql.Instance) error {
	var legacy struct {
		CopyrightText string `gorm:"column:copyright_text"`
		ICPNumber     string `gorm:"column:icp_number"`
		FooterHTML    string `gorm:"column:footer_html"`
	}
	err := db.GetDb().Table("site_configs").
		Select("copyright_text", "icp_number", "footer_html").
		Where("id = ?", model.SiteConfigID).
		Scan(&legacy).Error
	if err != nil {
		return fmt.Errorf("读取页脚旧字段失败: %w", err)
	}
	// 已有富文本（迁移过或已重新编辑过）时不覆盖
	if strings.TrimSpace(legacy.FooterHTML) != "" {
		return nil
	}
	parts := make([]string, 0, 2)
	for _, v := range []string{strings.TrimSpace(legacy.CopyrightText), strings.TrimSpace(legacy.ICPNumber)} {
		if v != "" {
			parts = append(parts, html.EscapeString(v))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return db.GetDb().Table("site_configs").
		Where("id = ?", model.SiteConfigID).
		Update("footer_html", strings.Join(parts, " · ")).Error
}
