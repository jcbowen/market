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
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

	// presignExpires 预签名 URL 有效期
	presignExpires = 10 * time.Minute

	// maskedSecret AccessKeySecret 脱敏回显值
	// 提交时命中该值或为空串均表示保留原值（真实密钥不可能为纯星号）
	maskedSecret = "******"

	// md5HexLength MD5 十六进制字符串长度
	md5HexLength = 32
)

// imageAllowedExts 允许上传的图片扩展名（与 jcbaseGo attachment image 类型白名单对齐）
var imageAllowedExts = map[string]bool{
	".gif": true, ".jpg": true, ".jpeg": true, ".bmp": true,
	".png": true, ".ico": true, ".webp": true, ".heic": true,
}

// AttachmentService 附件业务服务
// 覆盖附件配置管理、本地上传、OSS 预签名直传（presign/confirm）与 MD5 秒传
type AttachmentService struct {
	db        *mysql.Instance
	mu        sync.RWMutex
	cachedCfg *model.AttachmentConfig
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

// getConfigCached 获取附件配置（带进程内缓存与版本检测）
// 缓存版本与数据库 updated_at 一致时直接复用，不一致时刷新缓存
// 数据库查询失败时降级返回当前缓存，避免 DB 抖动阻断上传链路
//
// 参数:
//   - ctx: 上下文
//
// 返回值:
//   - *model.AttachmentConfig: 附件配置行（缓存或最新），均不可用时返回 nil
func (s *AttachmentService) getConfigCached(ctx context.Context) *model.AttachmentConfig {
	var row model.AttachmentConfig
	if err := s.db.GetDb().WithContext(ctx).First(&row, attachmentConfigID).Error; err != nil {
		log.Printf("读取附件配置失败（使用缓存兜底）: %v", err)
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.cachedCfg
	}

	s.mu.RLock()
	unchanged := s.cachedCfg != nil && s.cachedCfg.UpdatedAt == row.UpdatedAt
	s.mu.RUnlock()
	if unchanged {
		return s.cachedCfg
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// 双重检查：等待锁期间其他协程可能已完成刷新
	if s.cachedCfg == nil || s.cachedCfg.UpdatedAt != row.UpdatedAt {
		s.cachedCfg = &row
	}
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
// AccessKeySecret 为空串或脱敏掩码时保留原值；保存后更新 updated_at 作为版本号，
// 各实例在下一次上传链路调用时自动检测并重建 OSS 客户端
//
// 参数:
//   - ctx: 上下文
//   - cfg: proto 附件配置
//
// 返回值:
//   - error: 保存失败时返回错误
func (s *AttachmentService) SaveConfig(ctx context.Context, cfg *product.AttachmentConfig) error {
	row := s.getConfigCached(ctx)
	if row == nil {
		return errors.New("附件配置不存在")
	}

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

	if err := s.db.GetDb().WithContext(ctx).Save(row).Error; err != nil {
		return fmt.Errorf("保存附件配置失败: %w", err)
	}

	// 本实例立即刷新缓存，其余实例在下次上传调用时按版本号自动刷新
	s.mu.Lock()
	defer s.mu.Unlock()
	cfgCopy := *row
	s.cachedCfg = &cfgCopy
	return nil
}

// UploadImageLocal 本地模式图片上传
// 校验扩展名与大小后落盘至附件目录（可执行文件同级 attachment/），并写入附件记录
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
		MD5:       fileMD5(req.Content),
		Width:     width,
		Height:    height,
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
		URL:       finalURL,
		Path:      relPath,
		Name:      filepath.Base(req.Filename),
		Size:      req.Size,
		MD5:       "", // 延迟写入：confirm 成功后补写
		Width:     int(req.Width),
		Height:    int(req.Height),
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

	return &product.AttachmentInfo{
		AttachId:   req.AttachId,
		Url:        rec.URL,
		RemotePath: rec.Path,
		Size:       size,
		Width:      int32(width),
		Height:     int32(height),
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

// decodeImageSize 解析图片宽高
// 解码失败（如 webp/heic 标准库不支持）时返回 0 值，不视为错误
//
// 参数:
//   - content: 图片内容
//
// 返回值:
//   - width: 图片宽度（像素），解析失败为 0
//   - height: 图片高度（像素），解析失败为 0
func decodeImageSize(content []byte) (width, height int) {
	img, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return 0, 0
	}
	bounds := img.Bounds()
	return bounds.Dx(), bounds.Dy()
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
