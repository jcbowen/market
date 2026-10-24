package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// SiteConfigID 站点设置固定行 ID
// 全平台单行配置：读写一律按该主键定位，天然避免并发创建出多行
const SiteConfigID int64 = 1

// SiteConfig 站点设置模型（平台级品牌与部署域名）
// base_url 是全平台部署域名的唯一来源，网关据此派生支付回调地址与附件外链；
// 品牌字段（站名/logo/favicon/版权/备案/SEO）由网关注入前端 HTML 入口，供浏览器与爬虫共同读取
//
//nolint:lll
type SiteConfig struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	// 部署域名基址：scheme://host，不含路径与尾斜杠；为空时网关回落按请求现推
	BaseURL string `gorm:"column:base_url;type:varchar(255);comment:部署域名基址，scheme://host，不含路径与尾斜杠" json:"base_url"`
	// 平台品牌名，用于后台/H5 标题与品牌位
	SiteName string `gorm:"column:site_name;type:varchar(64);comment:平台品牌名" json:"site_name"`
	// 站点 logo 与 favicon 均存附件外链 URL，为空时前端使用内置默认图
	LogoURL    string `gorm:"column:logo_url;type:varchar(512);comment:站点 logo 外链 URL" json:"logo_url"`
	FaviconURL string `gorm:"column:favicon_url;type:varchar(512);comment:favicon 外链 URL" json:"favicon_url"`
	// 页脚富文本：版权与 ICP 备案号合并编辑（管理端富文本编辑器产出，服务端白名单消毒后落库）
	FooterHTML string `gorm:"column:footer_html;type:text;comment:页脚富文本，版权与备案号（仅 a/b/i/u/p/br 等白名单标签）" json:"footer_html"`
	// SEO 元信息，由网关写入 H5 HTML 入口
	SeoDescription string `gorm:"column:seo_description;type:varchar(255);comment:SEO 描述" json:"seo_description"`
	SeoKeywords    string `gorm:"column:seo_keywords;type:varchar(255);comment:SEO 关键词，逗号分隔" json:"seo_keywords"`
	// 配置版本号，每次保存递增，网关内存缓存据此失效
	Version   int64  `gorm:"column:version;type:bigint;not null;default:0;comment:配置版本号，每次保存递增" json:"version"`
	CreatedAt string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回站点设置表名
//
// 返回值：
//   - string: 表名
func (SiteConfig) TableName() string {
	return "site_configs"
}
