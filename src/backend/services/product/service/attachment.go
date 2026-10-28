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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/jcbaseGo/component/attachment/remote"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"github.com/jcbowen/market/src/backend/common/attachmenturl"
	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/likeutil"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/pageparam"
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
		return nil, bizerr.New(codes.NotFound, "附件配置不存在")
	}
	return toProtoConfigMasked(row), nil
}

// SaveConfig 保存附件配置
// AccessKeySecret 为空串或脱敏掩码时保留原值；保存后更新 updated_at 并立即刷新本实例缓存，
// 其余实例在进程内缓存过期（attachmentConfigCacheTTL）后自动读到新配置并重建 OSS 客户端
// 调用方携带读取时的 updated_at 时执行乐观并发校验，版本不一致拒绝整行覆写
//
// 参数:
//   - ctx: 上下文
//   - cfg: proto 附件配置
//
// 返回值:
//   - error: 配置缺失、校验失败、版本冲突或写库失败时返回错误
func (s *AttachmentService) SaveConfig(ctx context.Context, cfg *product.AttachmentConfig) error {
	// 覆写基于库中当前行而非进程内缓存：缓存允许陈旧降级（读链路可容忍），
	// 但整份 PUT 的版本比对与被掩码保护沿用原值的 Secret，都必须取当前值
	var current model.AttachmentConfig
	if err := s.db.GetDb().WithContext(ctx).First(&current, attachmentConfigID).Error; err != nil {
		return fmt.Errorf("读取附件配置失败: %w", err)
	}

	// 乐观锁：附件设置为单行整份覆盖式保存，后提交者会静默丢掉先提交者的改动
	// （改写方含后台另一管理员与站点域名联动补写 syncAttachmentVisitDomain）。
	// 版本号为空表示调用方未参与并发写入（旧版客户端），不校验。
	// 用 Aborted：网关据此归入业务错误码 10007（数据冲突）并原样透传提示到管理端界面
	if cfg.UpdatedAt != "" && !sameConfigVersion(cfg.UpdatedAt, current.UpdatedAt) {
		log.Printf("附件配置保存冲突：调用方版本 %s，当前版本 %s", cfg.UpdatedAt, current.UpdatedAt)
		return bizerr.New(codes.Aborted, "附件配置已被其他操作更新，请刷新页面获取最新配置后重新提交")
	}

	// 副本改写：在当前行副本上覆写表单字段，Secret 未重新录入时沿用库中原值
	row := current

	if err := validateStorageType(cfg.StorageType); err != nil {
		return err
	}
	if cfg.StorageType == "oss" {
		// 切换/保存 OSS 模式时校验必填字段（Secret 允许传掩码表示沿用原值）
		if cfg.OssAccessKeyId == "" || cfg.OssEndpoint == "" || cfg.OssBucketName == "" {
			return bizerr.New(codes.InvalidArgument, "OSS 配置不完整：AccessKeyId、Endpoint、BucketName 为必填项")
		}
		if cfg.OssAccessKeySecret == "" || cfg.OssAccessKeySecret == maskedSecret {
			if row.OssAccessKeySecret == "" {
				return bizerr.New(codes.InvalidArgument, "OSS AccessKeySecret 未配置")
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
//   - *product.AttachmentInfo: 附件信息（url 已按附件配置拼为完整地址）
//   - error: 校验或保存失败时返回错误
func (s *AttachmentService) UploadImageLocal(ctx context.Context, req *product.UploadImageLocalReq) (*product.AttachmentInfo, error) {
	cfg := s.getConfigCached(ctx)
	if cfg == nil {
		return nil, bizerr.New(codes.InvalidArgument, "附件配置不存在")
	}
	if cfg.StorageType != "local" {
		return nil, bizerr.New(codes.InvalidArgument, "当前存储模式不是本地附件，请使用预签名直传")
	}

	ext := strings.ToLower(filepath.Ext(req.Filename))
	if ext == "" || !imageAllowedExts[ext] {
		return nil, bizerr.New(codes.InvalidArgument, "不支持的图片格式")
	}

	maxSize := cfg.MaxSize
	if maxSize <= 0 {
		maxSize = defaultImageMaxSize
	}
	if len(req.Content) == 0 {
		return nil, bizerr.New(codes.InvalidArgument, "文件内容为空")
	}
	if int64(len(req.Content)) > maxSize {
		return nil, bizerr.New(codes.InvalidArgument, fmt.Sprintf("文件大小超出限制（最大 %dMB）", maxSize/1024/1024))
	}

	mimeType, err := resolveLocalImageMime(req.Content, req.ContentType, ext)
	if err != nil {
		return nil, err
	}
	width, height, err := decodeImageSize(req.Content, ext)
	if err != nil {
		return nil, err
	}
	contentMD5 := fileMD5(req.Content)

	// 秒传：内容 MD5 命中已有附件时直接复用，不重复落盘
	// local 模式记录写入时 md5 立即非空且文件已落盘，天然满足「md5 非空 ⇒ 文件已上传」不变量
	var hit model.Attachment
	err = s.db.GetDb().WithContext(ctx).
		Where("md5 = ? AND path <> ''", contentMD5).
		Order("id DESC").First(&hit).Error
	if err == nil {
		return &product.AttachmentInfo{
			AttachId:   fmt.Sprintf("%d", hit.ID),
			Url:        s.ToMediaURL(ctx, hit.Path),
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

	// 落盘目录由附件配置 local_dir 驱动：<可执行文件目录>/<local_dir>/<relPath>
	root, err := attachmentRootDir(cfg)
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
		Url:        s.ToMediaURL(ctx, rec.Path),
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
		return nil, bizerr.New(codes.InvalidArgument, "附件配置不存在")
	}
	if cfg.StorageType != "oss" {
		return nil, bizerr.New(codes.InvalidArgument, "当前存储模式不是远程附件，请使用本地上传接口")
	}
	ossCfg, err := buildOSSConfig(cfg)
	if err != nil {
		return nil, err
	}

	if req.Filename == "" {
		return nil, bizerr.New(codes.InvalidArgument, "文件名不能为空")
	}
	ext := strings.ToLower(filepath.Ext(req.Filename))
	if ext == "" || !imageAllowedExts[ext] {
		return nil, bizerr.New(codes.InvalidArgument, "不支持的图片格式")
	}

	maxSize := cfg.MaxSize
	if maxSize <= 0 {
		maxSize = defaultImageMaxSize
	}
	if req.Size > maxSize {
		return nil, bizerr.New(codes.InvalidArgument, fmt.Sprintf("文件大小超出限制（最大 %dMB）", maxSize/1024/1024))
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
		// 只有「记录不存在」才是业务口径；连接/SQL 故障不能伪装成「不存在」，否则运维无从发现
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, bizerr.New(codes.NotFound, "附件不存在或无权操作")
		}
		return nil, fmt.Errorf("查询附件记录失败: %w", err)
	}
	if rec.AdminID != req.AdminId {
		return nil, bizerr.New(codes.NotFound, "附件不存在或无权操作")
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
	page, pageSize := pageparam.ClampWith(int(req.Page), int(req.PageSize), attachmentSelectorPageSize, pageparam.MaxSize)
	keyword := strings.TrimSpace(req.Keyword)

	buildQuery := func() *gorm.DB {
		q := s.db.GetDb().WithContext(ctx).Model(&model.Attachment{}).
			Where("admin_id = ? AND is_display = 1 AND type LIKE 'image/%'", req.AdminId)
		if keyword != "" {
			like := likeutil.Contains(keyword)
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
		LocalDir:             row.LocalDir,
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

// sameConfigVersion 判断两个附件配置版本号是否指向同一时刻
// 同一行版本号存在两种字符串形态：本服务保存时写入并以空格格式进入进程内缓存（随后回显给前端），
// 与驱动把 DATETIME 列读进 string 字段时的 RFC3339 格式；直接字符串比对会把同一版本判成冲突，
// 故先按原始相等判定，再退化为解析成时间后比较（解析口径复用同包 parseTime 的双格式兼容）
// 参数：
//   - a: 调用方携带的版本号
//   - b: 库中当前版本号
//
// 返回值：
//   - bool: 同一版本返回 true；任一侧无法解析且原始值不等时返回 false（按冲突处理，不放行覆盖）
//
// 使用示例：
//   - if cfg.UpdatedAt != "" && !sameConfigVersion(cfg.UpdatedAt, current.UpdatedAt) { /* 拒绝保存 */ }
func sameConfigVersion(a, b string) bool {
	if a == b {
		return true
	}
	ta, tb := parseTime(a), parseTime(b)
	if ta.IsZero() || tb.IsZero() {
		return false
	}
	return ta.Equal(tb)
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
		return bizerr.New(codes.InvalidArgument, fmt.Sprintf("不支持的存储类型: %s", storageType))
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
		return nil, bizerr.New(codes.InvalidArgument, "OSS 配置不完整，请先在后台完善附件配置")
	}
	return &remote.OSSConfig{
		AccessKeyId:     cfg.OssAccessKeyId,
		AccessKeySecret: cfg.OssAccessKeySecret,
		Endpoint:        cfg.OssEndpoint,
		BucketName:      cfg.OssBucketName,
	}, nil
}

// buildVisitURL 按配置拼接最终访问 URL
// OSS 模式优先自定义访问域名，否则使用 bucket 域名；本地模式拼 <visit_domain>/uploads/<path>，
// 与 gateway /uploads 静态路由及 ToMediaURL 转换器口径一致
//
// 参数:
//   - cfg: 附件配置行
//   - relPath: 对象相对路径
//
// 返回值:
//   - string: 访问 URL（local 模式 visit_domain 为空时返回空串；正常部署下 gateway
//     会按部署域名自动补写 visit_domain，见 gateway syncAttachmentVisitDomain）
func buildVisitURL(cfg *model.AttachmentConfig, relPath string) string {
	if cfg.StorageType == "oss" {
		if cfg.OssCustomVisitDomain != "" {
			return cfg.OssCustomVisitDomain + relPath
		}
		return fmt.Sprintf("https://%s.%s/", cfg.OssBucketName, cfg.OssEndpoint) + relPath
	}
	if cfg.VisitDomain != "" {
		// URL 路径段与落盘目录共用 local_dir（jcbaseGo LocalDir 设计）
		return ensureTrailingSlash(cfg.VisitDomain) + attachmenturl.LocalDirName(cfg.LocalDir) + "/" + relPath
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
// 目录名由附件配置 local_dir 驱动（对齐 jcbaseGo LocalDir 设计：落盘目录名 = URL 路径段），
// 与 gateway 静态路由的挂载保持一致；配置不可用时回默认 uploads
//
// 参数:
//   - cfg: 附件配置，可为 nil（回默认目录名）
//
// 返回值:
//   - string: 附件根目录绝对路径
//   - error: 定位可执行文件失败时返回错误
func attachmentRootDir(cfg *model.AttachmentConfig) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位可执行文件失败: %w", err)
	}
	dir := attachmenturl.DefaultLocalDir
	if cfg != nil {
		// 归一逻辑统一走 LocalDirName（含首尾斜杠剥离与 "/" 等纯斜杠值回默认）
		dir = attachmenturl.LocalDirName(cfg.LocalDir)
	}
	return filepath.Join(filepath.Dir(exe), dir), nil
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
		return "", bizerr.New(codes.InvalidArgument, fmt.Sprintf("MD5长度必须为%d位", md5HexLength))
	}
	normalized := strings.ToLower(value)
	for i := 0; i < len(normalized); i++ {
		c := normalized[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", bizerr.New(codes.InvalidArgument, "MD5必须为十六进制字符串")
		}
	}
	return normalized, nil
}

// checkImageMime 校验并返回图片 MIME 类型（仅用于拿不到文件字节的预签名直传路径）
// content_type 非空时必须以 image/ 开头；为空时由扩展名推导，均失败时返回默认 image/jpeg
//
// 本地上传能拿到完整字节，必须改走 resolveLocalImageMime 做文件头嗅探，
// 否则把脚本改名为 .jpg 也能以图片身份入库
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
		if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
			return "", bizerr.New(codes.InvalidArgument, "content_type 与上传类型 image 不匹配")
		}
		return contentType, nil
	}
	if m, ok := imageExtMime[ext]; ok {
		return m, nil
	}
	return "image/jpeg", nil
}

// imageExtMime 常见扩展名到 MIME 的映射（标准库 mime.TypeByExtension 对 webp/heic 覆盖不全）
var imageExtMime = map[string]string{
	".gif": "image/gif", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".bmp": "image/bmp", ".png": "image/png", ".ico": "image/x-icon",
	".webp": "image/webp", ".heic": "image/heic",
}

// sniffImageMime 用文件头 magic number 探测真实图片 MIME
// 走 net/http.DetectContentType（内部只取前 512 字节，无需自行截断）；
// 非图片或标准库无签名规则的类型（heic 属 ISO-BMFF 容器）返回空串
//
// 参数:
//   - content: 文件完整内容
//
// 返回值:
//   - string: 形如 image/png 的 MIME；探测不出返回空串
func sniffImageMime(content []byte) string {
	detected := http.DetectContentType(content)
	if !strings.HasPrefix(detected, "image/") {
		return ""
	}
	if idx := strings.IndexByte(detected, ';'); idx >= 0 {
		detected = detected[:idx]
	}
	return strings.TrimSpace(detected)
}

// resolveLocalImageMime 本地上传的 MIME 判定：以前端声明把住入口，以文件头为准落库
//
// 旧实现只信 content_type 与扩展名，任意脚本改名为 .jpg 就能进附件目录并以 image/jpeg 记录；
// 现在要求字节本身是标准库可识别的图片，且嗅探结果与扩展名一致才放行。
// 以文件头为准（而不是以声明为准）还顺带修掉前端声明成非标准写法（如 image/jpg）时入库类型不可用的问题。
// heic 是唯一例外：纯 Go 侧没有它的文件头规则，也没有解码器，只能按扩展名放行。
//
// 参数:
//   - content: 文件完整内容
//   - contentType: 请求传入的 MIME 类型
//   - ext: 文件扩展名（含点，小写）
//
// 返回值:
//   - string: 与文件内容一致的 MIME 类型
//   - error: 内容不是图片、或与扩展名不符时返回业务错误
func resolveLocalImageMime(content []byte, contentType, ext string) (string, error) {
	if _, err := checkImageMime(contentType, ext); err != nil {
		return "", err
	}
	sniffed := sniffImageMime(content)
	if sniffed == "" {
		if ext == ".heic" {
			return imageExtMime[ext], nil
		}
		return "", bizerr.New(codes.InvalidArgument, "文件内容不是可识别的图片")
	}
	// 静态资源按扩展名给出 Content-Type，扩展名与真实内容不符会让浏览器拿到错误的类型，
	// 因此以扩展名推导的 MIME 作为比对基准，落库也用该值
	expected, ok := imageExtMime[ext]
	if !ok || expected != sniffed {
		return "", bizerr.New(codes.InvalidArgument, fmt.Sprintf("文件内容与扩展名不符（实际为 %s）", sniffed))
	}
	return expected, nil
}

// decodeImageSize 探测图片宽高（像素），本地模式上传时由后端从文件内容计算
// png/jpeg/gif/bmp/webp 走解码器（bmp/webp 由 golang.org/x/image 注册），ico 走手写头解析；
// 动图 webp（VP8X 容器）x/image 解不了，改从容器头直接取画布尺寸，仍能得到真实宽高
//
// 失败口径：除 heic 外，认得出的格式解不出来说明文件损坏或并非该格式，返回错误让上传失败——
// 旧实现把失败记成 width/height=0 照样入库，附件列表只能显示占位，前端按尺寸排版时会塌陷
//
// 参数:
//   - content: 图片文件完整内容
//   - ext: 文件扩展名（含点，小写）
//
// 返回值:
//   - width: 图片宽度（像素），heic 探测不到时为 0
//   - height: 图片高度（像素），同上
//   - error: 内容损坏或无法解析时返回业务错误
func decodeImageSize(content []byte, ext string) (int, int, error) {
	// heic 暂无纯 Go 轻量解码器，按「探测不到」放行而不是拒掉手机照片
	if ext == ".heic" {
		return 0, 0, nil
	}
	if width, height, ok := decodeIcoSize(content); ok {
		return width, height, nil
	}
	img, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		if width, height, ok := decodeWebpCanvasSize(content); ok {
			return width, height, nil
		}
		return 0, 0, bizerr.New(codes.InvalidArgument, "图片内容无法解析，文件可能已损坏")
	}
	bounds := img.Bounds()
	return bounds.Dx(), bounds.Dy(), nil
}

// decodeWebpCanvasSize 从 WebP 容器头读取画布宽高
// 动图与带元数据的 webp 使用 VP8X chunk（flags 1 字节 + 保留 2 字节 + 宽 3 字节 + 高 3 字节，
// 均为小端且存的是「实际值 - 1」），x/image/webp 不支持该容器，故绕过解码器直接取头
//
// 参数:
//   - content: 文件内容
//
// 返回值:
//   - width: 画布宽度（像素）
//   - height: 画布高度（像素）
//   - ok: 是否为带 VP8X 的合法 WebP 容器
func decodeWebpCanvasSize(content []byte) (width, height int, ok bool) {
	// RIFF(4) + size(4) + WEBP(4) + FOURCC(4) + chunkSize(4) + VP8X 载荷 8 字节
	if len(content) < 30 || string(content[0:4]) != "RIFF" || string(content[8:12]) != "WEBP" {
		return 0, 0, false
	}
	if string(content[12:16]) != "VP8X" {
		return 0, 0, false
	}
	readLE24 := func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }
	width = readLE24(content[24:27]) + 1
	height = readLE24(content[27:30]) + 1
	return width, height, true
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
// 口径总纲与实现见 common/attachmenturl 包（全系统公共能力，order 等服务共用）：
// 数据库仅存附件相对路径，入库前经 SourcePath 剥离，出库前经 ToMediaURL 拼完整地址。
// 本服务保留的方法是公共能力的薄委托：配置读取继续走本服务的 getConfigCached
// （与上传/保存共用同一份缓存与失效联动），转换与归一化全部下沉到公共包

// commonAttachmentConfig 将附件配置行转换为公共包配置结构
//
// 参数:
//   - cfg: 附件配置行
//
// 返回值:
//   - *attachmenturl.Config: 公共配置；入参 nil 时返回 nil
func commonAttachmentConfig(cfg *model.AttachmentConfig) *attachmenturl.Config {
	if cfg == nil {
		return nil
	}
	return &attachmenturl.Config{
		StorageType:          cfg.StorageType,
		LocalDir:             cfg.LocalDir,
		VisitDomain:          cfg.VisitDomain,
		OssCustomVisitDomain: cfg.OssCustomVisitDomain,
		OssBucketName:        cfg.OssBucketName,
		OssEndpoint:          cfg.OssEndpoint,
	}
}

// ToMediaURL 将入库相对路径补全为完整访问 URL
// 拼装规则（均无需迁移存量记录）：
//  1. 外链 URL（http/https/协议相对）与未知 / 开头绝对路径原样返回
//  2. 其余形态（相对路径、/<local_dir>/ 与 <local_dir>/ 前缀路径，含 /<local_dir>/seed 演示素材）
//     归一为相对路径后拼接：<域名>/<local_dir>/<path>（local）或 <域名>/<path>（oss）
//  3. local 模式 visit_domain 未就绪（补写尚未完成）→ 临时拼站内相对路径 /<local_dir>/<path> 并告警
//
// 参数:
//   - ctx: 上下文
//   - src: 相对路径（如 images/2026/10/11/xxx.jpg）或存量完整 URL
//
// 返回值:
//   - string: 完整访问 URL
func (s *AttachmentService) ToMediaURL(ctx context.Context, src string) string {
	pub := commonAttachmentConfig(s.getConfigCached(ctx))
	dir := attachmenturl.DirOf(pub)
	rel, passthrough := attachmenturl.RelPathIn(dir, src)
	if passthrough {
		return rel
	}
	conv := attachmenturl.NewConverter(pub)
	if conv == nil {
		return attachmenturl.FallbackURLIn(dir, src)
	}
	return conv.ToMedia(rel)
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
	pub := commonAttachmentConfig(s.getConfigCached(ctx))
	dir := attachmenturl.DirOf(pub)
	conv := attachmenturl.NewConverter(pub)
	out := make([]string, 0, len(list))
	for _, src := range list {
		rel, passthrough := attachmenturl.RelPathIn(dir, src)
		if passthrough {
			out = append(out, rel)
			continue
		}
		if conv == nil {
			out = append(out, attachmenturl.FallbackURLIn(dir, src))
			continue
		}
		out = append(out, conv.ToMedia(rel))
	}
	return out
}

// ToSourcePath 将图片地址归一化为入库相对路径（remote_path 形态，不含 uploads 前缀）
// 实现下沉在 common/attachmenturl.SourcePath（全系统公共口径），本方法为薄委托
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
	return attachmenturl.SourcePath(commonAttachmentConfig(s.getConfigCached(ctx)), src)
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
