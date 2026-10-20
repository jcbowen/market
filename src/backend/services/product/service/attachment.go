package service

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // 注册 GIF 解码器
	_ "image/jpeg" // 注册 JPEG 解码器
	_ "image/png"  // 注册 PNG 解码器

	_ "golang.org/x/image/bmp"  // 注册 BMP 解码器
	_ "golang.org/x/image/webp" // 注册 WebP 解码器
	"log"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/attachment"
	"github.com/jcbowen/jcbaseGo/component/attachment/remote"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// 附件默认限制与常量
const (
	// attachmentConfigID 附件配置表固定单行主键
	attachmentConfigID = 1

	// defaultImageMaxSize 图片默认大小上限（5MB，与 jcbaseGo attachment image 类型一致）
	defaultImageMaxSize = 5 * 1024 * 1024

	// attachmentSelectorPageSize 附件选择器默认每页条数
	attachmentSelectorPageSize = 12

	// presignExpires 预签名 URL 有效期
	presignExpires = 10 * time.Minute

	// maskedSecret AccessKeySecret 脱敏回显值
	// 提交时命中该值或为空串均表示保留原值（真实密钥不可能为纯星号）
	maskedSecret = "******"

	// md5HexLength MD5 十六进制字符串长度
	md5HexLength = 32

	// attachmentConfigCacheTTL 附件配置进程内缓存有效期
	// 命中期内不再查库：附件配置只有单行，却经 ToMediaURL 被逐行调用（列表页每行一次查询）
	// 取值权衡：越短配置变更生效越快，越长查库越少；
	// 本实例保存配置时立即失效，滞后只发生在多实例部署的其他进程，60s 足够
	attachmentConfigCacheTTL = 60 * time.Second
)

// imageAllowedExts 允许上传的图片扩展名（与 jcbaseGo attachment image 类型白名单对齐）
var imageAllowedExts = map[string]bool{
	".gif": true, ".jpg": true, ".jpeg": true, ".bmp": true,
	".png": true, ".ico": true, ".webp": true, ".heic": true,
}

// AttachmentService 附件业务服务
// 覆盖附件配置管理、本地上传、OSS 预签名直传（presign/confirm）与 MD5 秒传
type AttachmentService struct {
	db *mysql.Instance
	// 附件配置进程内缓存：mu 保护 cachedCfg 与 cachedAt 的读写
	mu        sync.RWMutex
	cachedCfg *model.AttachmentConfig
	cachedAt  time.Time
}

// NewAttachmentService 创建附件业务服务实例
//
// 参数:
//   - db: MySQL 实例
//
// 返回值:
//   - *AttachmentService: 附件业务服务实例
func NewAttachmentService(db *mysql.Instance) *AttachmentService {
	return &AttachmentService{db: db}
}

// EnsureDefaultAttachmentConfig 确保附件配置表存在默认配置行
// 表为空时写入一份默认记录（本地存储，其余字段待后台配置）
//
// 返回值:
//   - error: 初始化失败时返回错误
func (s *AttachmentService) EnsureDefaultAttachmentConfig() error {
	var count int64
	if err := s.db.GetDb().Model(&model.AttachmentConfig{}).Count(&count).Error; err != nil {
		return fmt.Errorf("查询附件配置表失败: %w", err)
	}
	if count > 0 {
		return nil
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	row := &model.AttachmentConfig{
		ID:          attachmentConfigID,
		StorageType: "local",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.db.GetDb().Create(row).Error; err != nil {
		return fmt.Errorf("初始化默认附件配置失败: %w", err)
	}
	log.Printf("已初始化默认附件配置（attachment_config id=%d）", attachmentConfigID)
	return nil
}

// configCacheFresh 判断进程内附件配置缓存是否在有效期内（调用方需已持有读锁）
//
// 参数:
//   - now: 判定基准时间
//
// 返回值:
//   - bool: 缓存非空且未过期时为 true
func (s *AttachmentService) configCacheFresh(now time.Time) bool {
	return s.cachedCfg != nil && now.Sub(s.cachedAt) < attachmentConfigCacheTTL
}

// getConfigCached 获取附件配置（进程内缓存，有效期内不查库）
// 缓存未过期直接复用，过期后查库刷新；查库失败时降级返回旧缓存，
// 避免 DB 抖动阻断上传与图片 URL 拼接链路（配置行恒在，读失败只可能是瞬时故障）
//
// 参数:
//   - ctx: 上下文
//
// 返回值:
//   - *model.AttachmentConfig: 附件配置（缓存或最新），均不可用时返回 nil
//
// 异常:
//   - 返回的是缓存内共享指针，调用方只读；需改写请自行复制（见 SaveConfig）
func (s *AttachmentService) getConfigCached(ctx context.Context) *model.AttachmentConfig {
	s.mu.RLock()
	cached, fresh := s.cachedCfg, s.configCacheFresh(time.Now())
	s.mu.RUnlock()
	if fresh {
		return cached
	}

	var row model.AttachmentConfig
	if err := s.db.GetDb().WithContext(ctx).First(&row, attachmentConfigID).Error; err != nil {
		log.Printf("读取附件配置失败（使用缓存兜底）: %v", err)
		return cached
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cachedCfg = &row
	s.cachedAt = time.Now()
	return s.cachedCfg
}

// GetConfigMasked 读取附件配置并脱敏 AccessKeySecret
// 返回值供后台展示，真实密钥不回传明文
//
// 参数:
//   - ctx: 上下文
//
// 返回值:
//   - *product.AttachmentConfig: 脱敏后的配置
//   - error: 查询失败时返回错误
func (s *AttachmentService) GetConfigMasked(ctx context.Context) (*product.AttachmentConfig, error) {
	row := s.getConfigCached(ctx)
	if row == nil {
		return nil, errors.New("附件配置不存在")
	}
	return toProtoConfigMasked(row), nil
}

// SaveConfig 保存附件配置
// AccessKeySecret 为空串或脱敏掩码时保留原值；保存后更新 updated_at 并立即刷新本实例缓存，
// 其余实例在进程内缓存过期（attachmentConfigCacheTTL）后自动读到新配置并重建 OSS 客户端
//
// 参数:
//   - ctx: 上下文
//   - cfg: proto 附件配置
//
// 返回值:
//   - error: 保存失败时返回错误
func (s *AttachmentService) SaveConfig(ctx context.Context, cfg *product.AttachmentConfig) error {
	cached := s.getConfigCached(ctx)
	if cached == nil {
		return errors.New("附件配置不存在")
	}
	// 副本改写：缓存指针在多协程间共享，原地改字段会与并发读取构成数据竞争
	row := *cached

	if err := validateStorageType(cfg.StorageType); err != nil {
		return err
	}
	if cfg.StorageType == "oss" {
		// 切换/保存 OSS 模式时校验必填字段（Secret 允许传掩码表示沿用原值）
		if cfg.OssAccessKeyId == "" || cfg.OssEndpoint == "" || cfg.OssBucketName == "" {
			return errors.New("OSS 配置不完整：AccessKeyId、Endpoint、BucketName 为必填项")
		}
		if cfg.OssAccessKeySecret == "" || cfg.OssAccessKeySecret == maskedSecret {
			if row.OssAccessKeySecret == "" {
				return errors.New("OSS AccessKeySecret 未配置")
			}
		}
	}

	row.StorageType = cfg.StorageType
	row.VisitDomain = ensureTrailingSlash(cfg.VisitDomain)
	row.OssAccessKeyId = cfg.OssAccessKeyId
	// Secret 为空或掩码时保留原值，避免脱敏回显被意外写回
	if cfg.OssAccessKeySecret != "" && cfg.OssAccessKeySecret != maskedSecret {
		row.OssAccessKeySecret = cfg.OssAccessKeySecret
	}
	row.OssEndpoint = cfg.OssEndpoint
	row.OssBucketName = cfg.OssBucketName
	row.OssCustomVisitDomain = ensureTrailingSlash(cfg.OssCustomVisitDomain)
	row.MaxSize = cfg.MaxSize
	row.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")

	if err := s.db.GetDb().WithContext(ctx).Save(&row).Error; err != nil {
		return fmt.Errorf("保存附件配置失败: %w", err)
	}

	// 本实例立即刷新缓存（存入独立副本，不把已被调用方持有的行指针交给缓存）
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := row
	s.cachedCfg = &updated
	s.cachedAt = time.Now()
	return nil
}

// UploadImageLocal 本地模式图片上传
// 校验扩展名与大小后先按内容 MD5 秒传查重（命中直接复用已有附件记录），
// 未命中则落盘至附件目录（可执行文件同级 attachment/）并写入附件记录
//
// 参数:
//   - ctx: 上下文
//   - req: 上传请求（含 admin_id、文件名与内容）
//
// 返回值:
//   - *product.AttachmentInfo: 附件信息（url 为空，由 gateway 按请求域名拼接）
//   - error: 校验或保存失败时返回错误
func (s *AttachmentService) UploadImageLocal(ctx context.Context, req *product.UploadImageLocalReq) (*product.AttachmentInfo, error) {
	cfg := s.getConfigCached(ctx)
	if cfg == nil {
		return nil, errors.New("附件配置不存在")
	}
	if cfg.StorageType != "local" {
		return nil, errors.New("当前存储模式不是本地附件，请使用预签名直传")
	}

	ext := strings.ToLower(filepath.Ext(req.Filename))
	if ext == "" || !imageAllowedExts[ext] {
		return nil, errors.New("不支持的图片格式")
	}

	maxSize := cfg.MaxSize
	if maxSize <= 0 {
		maxSize = defaultImageMaxSize
	}
	if len(req.Content) == 0 {
		return nil, errors.New("文件内容为空")
	}
	if int64(len(req.Content)) > maxSize {
		return nil, fmt.Errorf("文件大小超出限制（最大 %dMB）", maxSize/1024/1024)
	}

	width, height := decodeImageSize(req.Content)
	mimeType := resolveImageMime(req.ContentType, ext)
	contentMD5 := fileMD5(req.Content)

	// 秒传：内容 MD5 命中已有附件时直接复用，不重复落盘
	// local 模式记录写入时 md5 立即非空且文件已落盘，天然满足「md5 非空 ⇒ 文件已上传」不变量
	var hit model.Attachment
	err := s.db.GetDb().WithContext(ctx).
		Where("md5 = ? AND path <> ''", contentMD5).
		Order("id DESC").First(&hit).Error
	if err == nil {
		return &product.AttachmentInfo{
			AttachId:   fmt.Sprintf("%d", hit.ID),
			RemotePath: hit.Path,
			Size:       hit.Size,
			Width:      int32(hit.Width),
			Height:     int32(hit.Height),
		}, nil
	}

	// 对象相对路径：images/{Ymd}/{dd}/mkt{HHmmss}{6位随机}.{ext}
	relPath, err := buildObjectPath(ext, time.Now())
	if err != nil {
		return nil, err
	}

	// 落盘目录与 gateway 静态路由约定一致：<可执行文件目录>/attachment/<relPath>
	root, err := attachmentRootDir()
	if err != nil {
		return nil, err
	}
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err = os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return nil, fmt.Errorf("创建附件目录失败: %w", err)
	}
	if err = os.WriteFile(fullPath, req.Content, 0o644); err != nil {
		return nil, fmt.Errorf("写入附件文件失败: %w", err)
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	rec := &model.Attachment{
		AdminID:   req.AdminId,
		Type:      mimeType,
		Path:      relPath,
		Name:      filepath.Base(req.Filename),
		Size:      int64(len(req.Content)),
		MD5:       contentMD5,
		Width:     width,
		Height:    height,
		IsDisplay: boolToInt8(req.IsDisplay), // 后台上传 true 进选择器；客户端用户上传默认 false 不进入
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err = s.db.GetDb().WithContext(ctx).Create(rec).Error; err != nil {
		return nil, fmt.Errorf("写入附件记录失败: %w", err)
	}

	return &product.AttachmentInfo{
		AttachId:   fmt.Sprintf("%d", rec.ID),
		RemotePath: rec.Path,
		Size:       rec.Size,
		Width:      int32(rec.Width),
		Height:     int32(rec.Height),
	}, nil
}

// AttachmentPresign 获取预签名上传 URL（OSS 直传模式）
// 链路：配置校验 → MD5 规范化 → MIME 推导 → 秒传查询 → 探测/签发 → 写记录
// md5 延迟写入：presign 阶段置空串，confirm 成功后补写，
// 保证「md5 非空 ⇒ 对象已上传」的不变量，客户端放弃上传留下的记录天然从秒传查询中隔离
//
// 参数:
//   - ctx: 上下文
//   - req: 预签名请求
//
// 返回值:
//   - *product.AttachmentPresignResp: 预签名结果
//   - error: 校验失败或业务异常时返回错误
func (s *AttachmentService) AttachmentPresign(ctx context.Context, req *product.AttachmentPresignReq) (*product.AttachmentPresignResp, error) {
	cfg := s.getConfigCached(ctx)
	if cfg == nil {
		return nil, errors.New("附件配置不存在")
	}
	if cfg.StorageType != "oss" {
		return nil, errors.New("当前存储模式不是远程附件，请使用本地上传接口")
	}
	ossCfg, err := buildOSSConfig(cfg)
	if err != nil {
		return nil, err
	}

	if req.Filename == "" {
		return nil, errors.New("文件名不能为空")
	}
	ext := strings.ToLower(filepath.Ext(req.Filename))
	if ext == "" || !imageAllowedExts[ext] {
		return nil, errors.New("不支持的图片格式")
	}

	maxSize := cfg.MaxSize
	if maxSize <= 0 {
		maxSize = defaultImageMaxSize
	}
	if req.Size > maxSize {
		return nil, fmt.Errorf("文件大小超出限制（最大 %dMB）", maxSize/1024/1024)
	}

	fileMD5Value, err := normalizeMD5(req.Md5)
	if err != nil {
		return nil, err
	}
	mimeType, err := checkImageMime(req.ContentType, ext)
	if err != nil {
		return nil, err
	}

	// 秒传查询：md5 非空的记录才代表对象已上传
	var hit model.Attachment
	err = s.db.GetDb().WithContext(ctx).
		Where("md5 = ? AND path <> ''", fileMD5Value).
		Order("id DESC").First(&hit).Error
	if err == nil {
		return s.handlePresignHit(ctx, ossCfg, cfg, &hit)
	}
	// 其他查询错误直接返回；未找到记录（ErrRecordNotFound）继续走新建流程
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("查询秒传记录失败: %w", err)
	}

	return s.createPresign(ctx, ossCfg, cfg, req, fileMD5Value, mimeType, ext)
}

// handlePresignHit 处理秒传命中场景
// 对象存在时直接返回 final_url；对象已被清理时复用记录重新签发上传 URL
//
// 参数:
//   - ctx: 上下文
//   - ossCfg: OSS 配置
//   - cfg: 附件配置行
//   - rec: 命中的附件记录
//
// 返回值:
//   - *product.AttachmentPresignResp: 预签名结果
//   - error: 探测或签发失败时返回错误
func (s *AttachmentService) handlePresignHit(ctx context.Context, ossCfg *remote.OSSConfig, cfg *model.AttachmentConfig, rec *model.Attachment) (*product.AttachmentPresignResp, error) {
	finalURL := buildVisitURL(cfg, rec.Path)

	client, err := remote.NewOSSClient(*ossCfg)
	if err != nil {
		return nil, fmt.Errorf("初始化 OSS 客户端失败: %w", err)
	}

	// 探测失败按不存在处理，让客户端重传
	exists, _ := client.Exists(ctx, rec.Path)
	if exists {
		_ = s.db.GetDb().WithContext(ctx).Model(&model.Attachment{}).
			Where("id = ?", rec.ID).
			Update("updated_at", time.Now().Format("2006-01-02 15:04:05")).Error
		return &product.AttachmentPresignResp{
			AttachId:   fmt.Sprintf("%d", rec.ID),
			Hit:        true,
			FinalUrl:   finalURL,
			RemotePath: rec.Path,
		}, nil
	}

	// 命中但对象不存在：复用记录重新签发 URL
	presignURL, headers, err := presignUpload(ctx, client, rec.Path, rec.Type)
	if err != nil {
		return nil, err
	}
	_ = s.db.GetDb().WithContext(ctx).Model(&model.Attachment{}).
		Where("id = ?", rec.ID).
		Update("updated_at", time.Now().Format("2006-01-02 15:04:05")).Error

	return &product.AttachmentPresignResp{
		AttachId:   fmt.Sprintf("%d", rec.ID),
		Hit:        false,
		Url:        presignURL,
		Method:     "PUT",
		Headers:    headers,
		FinalUrl:   finalURL,
		RemotePath: rec.Path,
	}, nil
}

// createPresign 创建新的附件记录并签发预签名 URL
//
// 参数:
//   - ctx: 上下文
//   - ossCfg: OSS 配置
//   - cfg: 附件配置行
//   - req: 预签名请求
//   - fileMD5Value: 规范化后的文件 MD5（记录中延迟写入，此处仅参与秒传查询）
//   - mimeType: 文件 MIME 类型
//   - ext: 文件扩展名（含点，小写）
//
// 返回值:
//   - *product.AttachmentPresignResp: 预签名结果
//   - error: 写库或签发失败时返回错误
func (s *AttachmentService) createPresign(ctx context.Context, ossCfg *remote.OSSConfig, cfg *model.AttachmentConfig, req *product.AttachmentPresignReq, fileMD5Value, mimeType, ext string) (*product.AttachmentPresignResp, error) {
	relPath, err := buildObjectPath(ext, time.Now())
	if err != nil {
		return nil, err
	}
	finalURL := buildVisitURL(cfg, relPath)

	now := time.Now().Format("2006-01-02 15:04:05")
	rec := &model.Attachment{
		AdminID:   req.AdminId,
		Type:      mimeType,
		Path:      relPath,
		Name:      filepath.Base(req.Filename),
		Size:      req.Size,
		MD5:       "", // 延迟写入：confirm 成功后补写
		Width:     int(req.Width),
		Height:    int(req.Height),
		IsDisplay: boolToInt8(req.IsDisplay), // 后台上传 true 进选择器；客户端用户上传默认 false 不进入
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err = s.db.GetDb().WithContext(ctx).Create(rec).Error; err != nil {
		return nil, fmt.Errorf("写入附件记录失败: %w", err)
	}

	client, err := remote.NewOSSClient(*ossCfg)
	if err != nil {
		return nil, fmt.Errorf("初始化 OSS 客户端失败: %w", err)
	}
	presignURL, headers, err := presignUpload(ctx, client, relPath, mimeType)
	if err != nil {
		return nil, err
	}

	return &product.AttachmentPresignResp{
		AttachId:   fmt.Sprintf("%d", rec.ID),
		Hit:        false,
		Url:        presignURL,
		Method:     "PUT",
		Headers:    headers,
		FinalUrl:   finalURL,
		RemotePath: relPath,
	}, nil
}

// AttachmentConfirm 确认上传完成
// md5 必传且校验格式；归属校验要求 admin_id 与记录一致（attach_id 为数字可枚举，不能仅凭 ID 放行），
// confirm 成功后才补写记录 md5，保证「md5 非空 ⇒ 对象已上传」的不变量
//
// 参数:
//   - ctx: 上下文
//   - req: 确认请求
//
// 返回值:
//   - *product.AttachmentInfo: 附件信息
//   - error: 记录不存在、归属不符或更新失败时返回错误
func (s *AttachmentService) AttachmentConfirm(ctx context.Context, req *product.AttachmentConfirmReq) (*product.AttachmentInfo, error) {
	fileMD5Value, err := normalizeMD5(req.Md5)
	if err != nil {
		return nil, err
	}

	var rec model.Attachment
	if err = s.db.GetDb().WithContext(ctx).First(&rec, "id = ?", req.AttachId).Error; err != nil {
		return nil, errors.New("附件不存在或无权操作")
	}
	if rec.AdminID != req.AdminId {
		return nil, errors.New("附件不存在或无权操作")
	}

	// 未上报的字段回退到库里已有的值，避免 0 覆盖真实数据
	updates := map[string]interface{}{
		"md5":        fileMD5Value,
		"updated_at": time.Now().Format("2006-01-02 15:04:05"),
	}
	if req.Size > 0 {
		updates["size"] = req.Size
	}
	if req.Width > 0 {
		updates["width"] = req.Width
	}
	if req.Height > 0 {
		updates["height"] = req.Height
	}
	if err = s.db.GetDb().WithContext(ctx).Model(&model.Attachment{}).Where("id = ?", rec.ID).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("更新附件记录失败: %w", err)
	}

	size := rec.Size
	if req.Size > 0 {
		size = req.Size
	}
	width := rec.Width
	if req.Width > 0 {
		width = int(req.Width)
	}
	height := rec.Height
	if req.Height > 0 {
		height = int(req.Height)
	}

	// url 不落库，响应时按附件配置动态拼接（域名变更无需迁移记录）
	finalURL := ""
	if cfg := s.getConfigCached(ctx); cfg != nil {
		finalURL = buildVisitURL(cfg, rec.Path)
	}

	return &product.AttachmentInfo{
		AttachId:   req.AttachId,
		Url:        finalURL,
		RemotePath: rec.Path,
		Size:       size,
		Width:      int32(width),
		Height:     int32(height),
	}, nil
}

// ListAttachments 附件选择器分页列表
// 可见性规则：仅返回当前管理员上传（admin_id）且 is_display=1 的图片附件，
// 软删除记录由 gorm.DeletedAt 天然排除；keyword 模糊匹配原始文件名或路径，
// 时间范围对 created_at（datetime 字符串）比较；按上传时间倒序，url 经 ToMediaURL 统一拼接
//
// 参数:
//   - ctx: 上下文
//   - req: 查询请求（admin_id/keyword/时间范围/分页）
//
// 返回值:
//   - *product.ListAttachmentsResp: 附件列表与分页信息
//   - error: 查询失败时返回错误
func (s *AttachmentService) ListAttachments(ctx context.Context, req *product.ListAttachmentsReq) (*product.ListAttachmentsResp, error) {
	page := int(req.Page)
	if page <= 0 {
		page = 1
	}
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = attachmentSelectorPageSize
	}
	keyword := escapeLike(strings.TrimSpace(req.Keyword))

	buildQuery := func() *gorm.DB {
		q := s.db.GetDb().WithContext(ctx).Model(&model.Attachment{}).
			Where("admin_id = ? AND is_display = 1 AND type LIKE 'image/%'", req.AdminId)
		if keyword != "" {
			like := "%" + keyword + "%"
			q = q.Where("name LIKE ? OR path LIKE ?", like, like)
		}
		if req.StartTime != "" {
			q = q.Where("created_at >= ?", req.StartTime)
		}
		if req.EndTime != "" {
			q = q.Where("created_at <= ?", req.EndTime)
		}
		return q
	}

	var total int64
	if err := buildQuery().Count(&total).Error; err != nil {
		return nil, fmt.Errorf("统计附件数量失败: %w", err)
	}

	var rows []*model.Attachment
	if err := buildQuery().
		Order("created_at DESC, id DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询附件列表失败: %w", err)
	}

	list := make([]*product.AttachmentInfo, 0, len(rows))
	for _, row := range rows {
		list = append(list, &product.AttachmentInfo{
			AttachId:   fmt.Sprintf("%d", row.ID),
			Url:        s.ToMediaURL(ctx, row.Path),
			RemotePath: row.Path,
			Size:       row.Size,
			Width:      int32(row.Width),
			Height:     int32(row.Height),
			Name:       row.Name,
			Type:       row.Type,
			CreatedAt:  row.CreatedAt,
		})
	}
	return &product.ListAttachmentsResp{
		List:     list,
		Total:    total,
		Page:     int32(page),
		PageSize: int32(pageSize),
	}, nil
}

// toProtoConfigMasked 将附件配置行转换为脱敏的 proto 配置
//
// 参数:
//   - row: 附件配置行
//
// 返回值:
//   - *product.AttachmentConfig: 脱敏后的配置（Secret 有值时回显掩码）
func toProtoConfigMasked(row *model.AttachmentConfig) *product.AttachmentConfig {
	secret := ""
	if row.OssAccessKeySecret != "" {
		secret = maskedSecret
	}
	return &product.AttachmentConfig{
		StorageType:          row.StorageType,
		VisitDomain:          row.VisitDomain,
		OssAccessKeyId:       row.OssAccessKeyId,
		OssAccessKeySecret:   secret,
		OssEndpoint:          row.OssEndpoint,
		OssBucketName:        row.OssBucketName,
		OssCustomVisitDomain: row.OssCustomVisitDomain,
		MaxSize:              row.MaxSize,
		UpdatedAt:            row.UpdatedAt,
	}
}

// validateStorageType 校验存储类型取值
//
// 参数:
//   - storageType: 存储类型
//
// 返回值:
//   - error: 取值不受支持时返回错误
func validateStorageType(storageType string) error {
	switch storageType {
	case "local", "oss":
		return nil
	default:
		return fmt.Errorf("不支持的存储类型: %s", storageType)
	}
}

// buildOSSConfig 将附件配置行转换为 jcbaseGo OSS 客户端配置
//
// 参数:
//   - cfg: 附件配置行
//
// 返回值:
//   - *remote.OSSConfig: OSS 客户端配置
//   - error: 配置不完整时返回错误
func buildOSSConfig(cfg *model.AttachmentConfig) (*remote.OSSConfig, error) {
	if cfg.OssAccessKeyId == "" || cfg.OssAccessKeySecret == "" || cfg.OssEndpoint == "" || cfg.OssBucketName == "" {
		return nil, errors.New("OSS 配置不完整，请先在后台完善附件配置")
	}
	return &remote.OSSConfig{
		AccessKeyId:     cfg.OssAccessKeyId,
		AccessKeySecret: cfg.OssAccessKeySecret,
		Endpoint:        cfg.OssEndpoint,
		BucketName:      cfg.OssBucketName,
	}, nil
}

// buildVisitURL 按配置拼接最终访问 URL
// OSS 模式优先自定义访问域名，否则使用 bucket 域名；本地模式配置了域名同样拼接
//
// 参数:
//   - cfg: 附件配置行
//   - relPath: 对象相对路径
//
// 返回值:
//   - string: 访问 URL（本地模式未配置域名时返回空串，由 gateway 按请求域名拼接）
func buildVisitURL(cfg *model.AttachmentConfig, relPath string) string {
	if cfg.StorageType == "oss" {
		if cfg.OssCustomVisitDomain != "" {
			return cfg.OssCustomVisitDomain + relPath
		}
		return fmt.Sprintf("https://%s.%s/", cfg.OssBucketName, cfg.OssEndpoint) + relPath
	}
	if cfg.VisitDomain != "" {
		return cfg.VisitDomain + relPath
	}
	return ""
}

// presignUpload 签发预签名上传 URL
//
// 参数:
//   - ctx: 上下文
//   - client: OSS 客户端
//   - objectPath: 对象相对路径
//   - contentType: 文件 MIME 类型
//
// 返回值:
//   - string: 预签名 URL
//   - map[string]string: 上传时必须携带的请求头
//   - error: 签发失败时返回错误
func presignUpload(ctx context.Context, client *remote.OSSClient, objectPath, contentType string) (string, map[string]string, error) {
	opts := &remote.PresignOptions{
		Expires:     presignExpires,
		ContentType: contentType,
	}
	return client.PresignUpload(ctx, objectPath, opts)
}

// buildObjectPath 生成附件对象相对路径
// 规则: images/{Ymd}/{dd}/mkt{HHmmss}{6位随机数字}.{ext}
// 示例: images/20261011/11/mkt080102123456.jpg
//
// 参数:
//   - ext: 文件扩展名（含点，小写）
//   - now: 当前时间
//
// 返回值:
//   - string: 对象相对路径（统一使用 / 分隔）
//   - error: 随机数生成失败时返回错误
func buildObjectPath(ext string, now time.Time) (string, error) {
	randomNum, err := randomNumeric(6)
	if err != nil {
		return "", fmt.Errorf("生成随机文件名失败: %w", err)
	}
	// 目录层级显式写在路径模板里，不要把「/」拼进 time.Format 布局串
	return fmt.Sprintf("images/%s/%s/mkt%s%s%s",
		now.Format("20060102"), now.Format("02"), now.Format("150405"), randomNum, ext), nil
}

// attachmentRootDir 获取本地附件根目录
// 固定为可执行文件同级 attachment/ 目录，与 gateway 静态路由 /uploads 的映射保持一致
//
// 返回值:
//   - string: 附件根目录绝对路径
//   - error: 定位可执行文件失败时返回错误
func attachmentRootDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位可执行文件失败: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), "attachment"), nil
}

// fileMD5 计算文件内容的 MD5（32 位小写十六进制）
//
// 参数:
//   - content: 文件内容
//
// 返回值:
//   - string: 小写 MD5
func fileMD5(content []byte) string {
	sum := md5.Sum(content)
	return hex.EncodeToString(sum[:])
}

// normalizeMD5 校验并规范化文件内容 MD5
// 算法为标准 MD5，输入是文件完整原始二进制内容，输出 32 位小写十六进制；
// 统一转小写与 PHP md5_file() 口径对齐，避免大小写不一致导致秒传失效
//
// 参数:
//   - value: 原始 MD5 字符串
//
// 返回值:
//   - string: 规范化后的小写 MD5
//   - error: 长度不为 32 位或含非十六进制字符时返回错误
func normalizeMD5(value string) (string, error) {
	if len(value) != md5HexLength {
		return "", fmt.Errorf("MD5长度必须为%d位", md5HexLength)
	}
	normalized := strings.ToLower(value)
	for i := 0; i < len(normalized); i++ {
		c := normalized[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", errors.New("MD5必须为十六进制字符串")
		}
	}
	return normalized, nil
}

// resolveImageMime 解析图片 MIME 类型
// content_type 非空时优先使用并校验前缀；为空时由扩展名推导
//
// 参数:
//   - contentType: 请求传入的 MIME 类型
//   - ext: 文件扩展名（含点，小写）
//
// 返回值:
//   - string: MIME 类型字符串（推导失败时回退 application/octet-stream）
func resolveImageMime(contentType, ext string) string {
	mime, err := checkImageMime(contentType, ext)
	if err != nil || mime == "" {
		return "application/octet-stream"
	}
	return mime
}

// checkImageMime 校验并返回图片 MIME 类型
// content_type 非空时必须以 image/ 开头；为空时由扩展名推导，均失败时返回默认 image/jpeg
//
// 参数:
//   - contentType: 请求传入的 MIME 类型
//   - ext: 文件扩展名（含点，小写）
//
// 返回值:
//   - string: MIME 类型字符串
//   - error: content_type 与图片类型不匹配时返回错误
func checkImageMime(contentType, ext string) (string, error) {
	if contentType != "" {
		if !strings.HasPrefix(contentType, "image/") {
			return "", errors.New("content_type 与上传类型 image 不匹配")
		}
		return contentType, nil
	}

	// 常见扩展名到 MIME 的映射（标准库 mime.TypeByExtension 对 webp/heic 覆盖不全）
	extMime := map[string]string{
		".gif": "image/gif", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".bmp": "image/bmp", ".png": "image/png", ".ico": "image/x-icon",
		".webp": "image/webp", ".heic": "image/heic",
	}
	if m, ok := extMime[ext]; ok {
		return m, nil
	}
	return "image/jpeg", nil
}

// decodeImageSize 探测图片宽高（像素），本地模式上传时由后端从文件内容计算
// png/jpeg/gif 走标准库，bmp/webp 由 golang.org/x/image 注册解码器，
// ico 走手写头解析（x/image 未提供）；heic 暂无纯 Go 轻量解码器，探测失败返回 0
//
// 参数:
//   - content: 图片文件完整内容
//
// 返回值:
//   - width: 图片宽度（像素），探测失败为 0
//   - height: 图片高度（像素），探测失败为 0
func decodeImageSize(content []byte) (width, height int) {
	if width, height, ok := decodeIcoSize(content); ok {
		return width, height
	}
	img, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return 0, 0
	}
	bounds := img.Bounds()
	return bounds.Dx(), bounds.Dy()
}

// decodeIcoSize 解析 ICO 文件头首个图标条目的宽高
// 结构：6 字节 ICONDIR（reserved/type/count 各 2 字节小端）+ 16 字节 ICONDIRENTRY，
// 条目首两字节为宽高，值为 0 表示 256
//
// 参数:
//   - content: 文件内容
//
// 返回值:
//   - width: 宽度（像素）
//   - height: 高度（像素）
//   - ok: 是否为合法 ICO 头
func decodeIcoSize(content []byte) (width, height int, ok bool) {
	if len(content) < 22 {
		return 0, 0, false
	}
	// reserved=0、type=1(图标)、count>=1，小端
	if content[0] != 0 || content[1] != 0 || content[2] != 1 || content[3] != 0 ||
		content[4] == 0 || content[5] != 0 {
		return 0, 0, false
	}
	width, height = int(content[6]), int(content[7])
	if width == 0 {
		width = 256
	}
	if height == 0 {
		height = 256
	}
	return width, height, true
}

// randomNumeric 生成指定长度的随机数字字符串
//
// 参数:
//   - length: 长度
//
// 返回值:
//   - string: 随机数字字符串
//   - error: 随机数生成失败时返回错误
func randomNumeric(length int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(length)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", length, n), nil
}

// escapeLike 转义 LIKE 查询中的通配符，避免用户输入的 % 与 _ 扰动匹配范围
//
// 参数:
//   - value: 原始字符串
//
// 返回值:
//   - string: 转义后的字符串（\%、\_、\\）
func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

// boolToInt8 布尔值转 tinyint 存储（proto3 bool 默认 false，对应客户端上传不进选择器）
//
// 参数:
//   - v: 布尔值
//
// 返回值:
//   - int8: true 为 1，false 为 0
func boolToInt8(v bool) int8 {
	if v {
		return 1
	}
	return 0
}

// ensureTrailingSlash 确保域名以 / 结尾
//
// 参数:
//   - domain: 域名字符串
//
// 返回值:
//   - string: 以 / 结尾的域名（空串原样返回）
func ensureTrailingSlash(domain string) string {
	if domain == "" {
		return ""
	}
	return strings.TrimSuffix(domain, "/") + "/"
}

// ==================== 图片 URL 统一转换 ====================
// 数据库仅存附件相对路径（remote_path 形态，如 images/2026/10/11/xxx.jpg，不含 uploads 目录前缀）：
// 入库前经 ToSourcePath 剥离域名，出库前经 ToMediaURL 拼接完整访问地址。
// local 模式拼接为 <visit_domain>/uploads/<path>（与 gateway /uploads 静态路由一致），
// oss 模式拼接为 <自定义 CDN 域名或 bucket 域名>/<path>

// attachmentConverter 按附件配置构造 jcbaseGo 附件转换器
// 直接以字面量构造 Attachment（不走 New），BaseConfig 字段由本方法保证规范性
//
// 参数:
//   - cfg: 附件配置行，nil 时返回 nil（调用方需兜底原样返回）
//
// 返回值:
//   - *attachment.Attachment: 附件转换器实例
func attachmentConverter(cfg *model.AttachmentConfig) *attachment.Attachment {
	if cfg == nil {
		return nil
	}
	base := &jcbaseGo.AttachmentStruct{
		StorageType:      cfg.StorageType,
		LocalDir:         "uploads",
		LocalVisitDomain: ensureTrailingSlash(cfg.VisitDomain),
	}
	if cfg.StorageType == "oss" {
		domain := cfg.OssCustomVisitDomain
		if domain == "" {
			domain = fmt.Sprintf("https://%s.%s/", cfg.OssBucketName, cfg.OssEndpoint)
		}
		base.VisitDomain = ensureTrailingSlash(domain)
	}
	return &attachment.Attachment{BaseConfig: base}
}

// mediaConverter 取当前可用的图片 URL 转换器
// 配置缺失，或 local 模式未填访问域名（拼不出可用地址）时返回 nil，调用方原样返回源值
//
// 参数:
//   - ctx: 上下文
//
// 返回值:
//   - *attachment.Attachment: 附件转换器；不可用时返回 nil
func (s *AttachmentService) mediaConverter(ctx context.Context) *attachment.Attachment {
	conv := attachmentConverter(s.getConfigCached(ctx))
	if conv == nil || (conv.BaseConfig.StorageType != "oss" && conv.BaseConfig.LocalVisitDomain == "") {
		return nil
	}
	return conv
}

// ToMediaURL 将入库相对路径补全为完整访问 URL
// 空值与 http(s) 开头的存量完整 URL 原样返回；
// local 模式未配置访问域名时拼不出可用地址，同样原样返回（需先在后台附件配置中填写）
//
// 参数:
//   - ctx: 上下文
//   - src: 相对路径（如 images/2026/10/11/xxx.jpg）或存量完整 URL
//
// 返回值:
//   - string: 完整访问 URL
func (s *AttachmentService) ToMediaURL(ctx context.Context, src string) string {
	if src == "" {
		return ""
	}
	conv := s.mediaConverter(ctx)
	if conv == nil {
		return src
	}
	return conv.ToMedia(src)
}

// ToMediaURLs ToMediaURL 的批量版本
// 整批共用一次配置读取与转换器构造，供列表接口的图集字段使用
//
// 参数:
//   - ctx: 上下文
//   - list: 相对路径列表
//
// 返回值:
//   - []string: 完整访问 URL 列表
func (s *AttachmentService) ToMediaURLs(ctx context.Context, list []string) []string {
	if len(list) == 0 {
		return list
	}
	conv := s.mediaConverter(ctx)
	out := make([]string, 0, len(list))
	for _, src := range list {
		if src == "" || conv == nil {
			out = append(out, src)
			continue
		}
		out = append(out, conv.ToMedia(src))
	}
	return out
}

// ToSourcePath 将图片地址归一化为入库相对路径（remote_path 形态，不含 uploads 前缀）
// 规则：
//   - 本站附件地址（path 以 /uploads/ 开头，或纯路径以 uploads/ 开头）→ 剥离为相对路径
//   - OSS/CDN URL（域名命中附件配置候选）→ 剥离为对象 Key
//   - 其他外链 URL（如 placehold.co 占位图）→ 原样保存，出库时同样原样返回，保证外链图可用
//
// 未复用 jcbaseGo ToSource 全量逻辑的原因：其域名候选依赖附件配置中的固定域名，
// 剥不掉按请求域名动态拼接的存量 URL，本站路径改由 url.Parse 提取，配置候选仅用于 OSS 域名剥离
//
// 参数:
//   - ctx: 上下文
//   - src: 完整 URL、/uploads/... 纯路径或已是相对路径
//
// 返回值:
//   - string: 入库相对路径或原样外链 URL
//
// 使用示例:
//
//	ToSourcePath("http://localhost:18080/uploads/images/2026/10/11/a.jpg") => "images/2026/10/11/a.jpg"
//	ToSourcePath("/uploads/images/a.jpg")                                  => "images/a.jpg"
//	ToSourcePath("https://cdn.example.com/images/a.jpg")（CDN 域名已配置）  => "images/a.jpg"
//	ToSourcePath("https://placehold.co/300x300?text=Tomato")               => 原样返回
func (s *AttachmentService) ToSourcePath(ctx context.Context, src string) string {
	if src == "" {
		return ""
	}
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		// 本站附件：path 以 /uploads/ 开头，直接提取相对路径（query/锚点丢弃）
		if u, err := url.Parse(src); err == nil {
			if rel, ok := strings.CutPrefix(u.Path, "/uploads/"); ok {
				return rel
			}
		}
		// OSS/CDN：域名命中附件配置候选时剥离为对象 Key，未命中的外链原样保存
		// （不用 ToSource：其未命中候选也会剥除查询参数，会破坏外链 URL）
		if conv := attachmentConverter(s.getConfigCached(ctx)); conv != nil {
			for _, domain := range []string{conv.BaseConfig.VisitDomain, conv.BaseConfig.LocalVisitDomain} {
				if domain == "" || domain == "/" || !strings.HasPrefix(src, domain) {
					continue
				}
				return strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(src, domain), "/"), "uploads/")
			}
		}
		return src
	}
	// 纯路径形态：剥离开头的 / 与 uploads/ 目录前缀
	if _, rest, ok := strings.Cut(strings.Trim(src, "/"), "uploads/"); ok {
		return rest
	}
	return src
}

// ToSourcePaths ToSourcePath 的批量版本
//
// 参数:
//   - ctx: 上下文
//   - list: URL 或路径列表
//
// 返回值:
//   - []string: 入库相对路径（或原样外链 URL）列表
func (s *AttachmentService) ToSourcePaths(ctx context.Context, list []string) []string {
	if len(list) == 0 {
		return list
	}
	out := make([]string, 0, len(list))
	for _, src := range list {
		out = append(out, s.ToSourcePath(ctx, src))
	}
	return out
}
