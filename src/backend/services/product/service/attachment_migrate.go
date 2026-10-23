package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/jcbowen/jcbaseGo/component/attachment/remote"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// AttachmentMigrateService 承接附件迁移相关方法（挂载在 AttachmentService 上，共用配置缓存与 DB）

// 可迁移条目的批量上限（防御性限制，正常后台附件量级远低于此值）
const migrateScanLimit = 20000

// ScanAttachmentMigrate 扫描本地附件目录并与 attachment 表匹配，生成/刷新迁移任务清单
// 匹配成功的文件按 path upsert 为迁移条目（已有条目保留状态，孤儿文件以 orphan 状态入表），
// 附件表有记录但本地文件缺失的仅在统计中体现；清单中既不在本次扫描也非终态的条目将被清理
//
// 参数:
//   - ctx: 上下文
//
// 返回值:
//   - *product.AttachmentMigrateOverview: 任务概览统计（含 orphan 与 missing）
//   - error: OSS 配置不完整或扫描失败时返回错误
func (s *AttachmentService) ScanAttachmentMigrate(ctx context.Context) (*product.AttachmentMigrateOverview, error) {
	// 扫描不要求当前为 OSS 模式，但要求 OSS 凭证已配置完整（迁移目标可用）
	cfg := s.getConfigCached(ctx)
	if cfg == nil {
		return nil, errors.New("附件配置不存在")
	}
	if _, err := buildOSSConfig(cfg); err != nil {
		return nil, err
	}

	root, err := attachmentRootDir()
	if err != nil {
		return nil, err
	}
	localFiles, err := scanLocalAttachmentFiles(root)
	if err != nil {
		return nil, err
	}

	// 附件表记录：path → 记录（软删除记录自动排除）
	var attachments []*model.Attachment
	if err = s.db.GetDb().WithContext(ctx).
		Select("id", "path", "name", "type", "size", "md5", "width", "height").
		Limit(migrateScanLimit).
		Find(&attachments).Error; err != nil {
		return nil, fmt.Errorf("查询附件记录失败: %w", err)
	}
	attMap := make(map[string]*model.Attachment, len(attachments))
	for _, rec := range attachments {
		if rec.Path != "" {
			attMap[rec.Path] = rec
		}
	}

	// 匹配：本地文件命中附件记录 → 待迁移条目；未命中 → 孤儿文件
	type itemDiff struct {
		item  *model.AttachmentMigrateItem
		isNew bool
	}
	var diffs []itemDiff
	missing, matchedCount := 0, 0
	for path := range attMap {
		if _, ok := localFiles[path]; !ok {
			missing++
		}
	}
	for path, size := range localFiles {
		rec, matched := attMap[path]
		status := model.MigrateStatusOrphan
		item := &model.AttachmentMigrateItem{
			Path:  path,
			Size:  size,
			Name:  filepath.Base(path),
			Type:  "",
			MD5:   "",
			Width: 0, Height: 0,
		}
		if matched {
			matchedCount++
			status = model.MigrateStatusPending
			item.Name, item.Type, item.Size = rec.Name, rec.Type, rec.Size
			item.MD5, item.Width, item.Height = rec.MD5, rec.Width, rec.Height
		}

		existing, ok := s.loadMigrateItem(ctx, path)
		if ok {
			// 已有条目保留状态，仅刷新文件信息；孤儿/匹配互转时纠正状态
			if existing.Status == model.MigrateStatusOrphan && matched {
				existing.Status = model.MigrateStatusPending
			}
			if !matched && existing.Status != model.MigrateStatusOrphan && existing.Status != model.MigrateStatusLocalDeleted {
				existing.Status = model.MigrateStatusOrphan
			}
			existing.Name, existing.Type, existing.Size = item.Name, item.Type, item.Size
			existing.MD5, existing.Width, existing.Height = item.MD5, item.Width, item.Height
			existing.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
			diffs = append(diffs, itemDiff{item: existing})
			continue
		}
		item.Status = status
		now := time.Now().Format("2006-01-02 15:04:05")
		item.CreatedAt, item.UpdatedAt = now, now
		diffs = append(diffs, itemDiff{item: item, isNew: true})
	}

	for i := range diffs {
		d := &diffs[i]
		if d.isNew {
			if err = s.db.GetDb().WithContext(ctx).Create(d.item).Error; err != nil {
				return nil, fmt.Errorf("写入迁移条目失败: %w", err)
			}
			continue
		}
		if err = s.db.GetDb().WithContext(ctx).Model(&model.AttachmentMigrateItem{}).
			Where("id = ?", d.item.ID).
			Updates(map[string]interface{}{
				"name": d.item.Name, "type": d.item.Type, "size": d.item.Size,
				"md5": d.item.MD5, "width": d.item.Width, "height": d.item.Height,
				"status": d.item.Status, "updated_at": d.item.UpdatedAt,
			}).Error; err != nil {
			return nil, fmt.Errorf("更新迁移条目失败: %w", err)
		}
	}

	// 清理失效条目：本地文件已消失且非终态（local_deleted 保留作为操作痕迹）
	if err = s.db.GetDb().WithContext(ctx).
		Where("status <> ?", model.MigrateStatusLocalDeleted).
		Where("path NOT IN ?", pathKeys(localFiles)).
		Delete(&model.AttachmentMigrateItem{}).Error; err != nil {
		return nil, fmt.Errorf("清理失效迁移条目失败: %w", err)
	}

	log.Printf("附件迁移扫描完成: 本地文件 %d，附件记录 %d，孤儿 %d，缺失 %d",
		len(localFiles), len(attMap), len(localFiles)-matchedCount, missing)
	return s.countMigrateOverview(ctx, int32(missing))
}

// ListAttachmentMigrateItems 迁移条目分页列表，附概览统计
//
// 参数:
//   - ctx: 上下文
//   - req: 列表请求（status 过滤，空为全部；pagination 分页）
//
// 返回值:
//   - *product.AttachmentMigrateListResp: 条目列表 + 分页 + 概览
//   - error: 查询失败时返回错误
func (s *AttachmentService) ListAttachmentMigrateItems(ctx context.Context, req *product.AttachmentMigrateListReq) (*product.AttachmentMigrateListResp, error) {
	page, pageSize := 1, 20
	if req.Pagination != nil {
		page = int(req.Pagination.Page)
		pageSize = int(req.Pagination.PageSize)
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	db := s.db.GetDb().WithContext(ctx).Model(&model.AttachmentMigrateItem{})
	if req.Status != "" {
		db = db.Where("status = ?", req.Status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("统计迁移条目失败: %w", err)
	}
	var items []*model.AttachmentMigrateItem
	if err := db.Order("id desc").Limit(pageSize).Offset((page - 1) * pageSize).Find(&items).Error; err != nil {
		return nil, fmt.Errorf("查询迁移条目失败: %w", err)
	}

	list := make([]*product.AttachmentMigrateItemInfo, 0, len(items))
	for _, item := range items {
		list = append(list, toProtoMigrateItem(item))
	}

	overview, err := s.countMigrateOverview(ctx, 0)
	if err != nil {
		return nil, err
	}
	return &product.AttachmentMigrateListResp{
		List: list,
		Pagination: &common.PaginationResponse{
			Total:    int32(total),
			Page:     int32(page),
			PageSize: int32(pageSize),
		},
		Overview: overview,
	}, nil
}

// MigrateAttachmentItem 迁移单个条目：读取本地文件上传 OSS（相同 path），成功后自动验证
// 幂等可重试（OSS 同名覆盖）；local_deleted（本地文件已删）与 orphan（无附件记录）不允许迁移
//
// 参数:
//   - ctx: 上下文
//   - id: 条目 ID
//
// 返回值:
//   - *product.AttachmentMigrateItemInfo: 操作后的最新条目
//   - error: 条目不可迁移或迁移失败时返回错误（失败同时落库到条目 error_msg）
func (s *AttachmentService) MigrateAttachmentItem(ctx context.Context, id int64) (*product.AttachmentMigrateItemInfo, error) {
	item, err := s.getMigrateItem(ctx, id)
	if err != nil {
		return nil, err
	}
	if !model.CanMigrateAttachmentStatus(item.Status) {
		return nil, bizerr.New(codes.FailedPrecondition, fmt.Sprintf("当前状态（%s）不允许迁移", migrateStatusLabel(item.Status)))
	}

	client, _, err := s.migrateOSSClient(ctx)
	if err != nil {
		return nil, err
	}

	root, err := attachmentRootDir()
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(item.Path)))
	if err != nil {
		return nil, s.failMigrateItem(ctx, item, fmt.Errorf("读取本地文件失败: %w", err))
	}

	// 置迁移中，失败时回写 failed
	s.updateMigrateItemStatus(ctx, item.ID, model.MigrateStatusMigrating, "")
	if err = client.Upload(ctx, item.Path, content); err != nil {
		return nil, s.failMigrateItem(ctx, item, fmt.Errorf("上传 OSS 失败: %w", err))
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	updates := map[string]interface{}{
		"status":      model.MigrateStatusMigrated,
		"migrated_at": now,
		"error_msg":   "",
		"updated_at":  now,
	}
	// 迁移成功后立即验证：对象存在且大小一致才算迁移完成
	if verr := verifyMigrateObject(ctx, client, item); verr != nil {
		updates["status"] = model.MigrateStatusVerifyFailed
		updates["error_msg"] = truncateErrMsg(verr)
	} else {
		updates["status"] = model.MigrateStatusVerified
		updates["verified_at"] = now
	}
	if err = s.db.GetDb().WithContext(ctx).Model(&model.AttachmentMigrateItem{}).
		Where("id = ?", item.ID).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("更新迁移条目失败: %w", err)
	}

	return s.getMigrateItemInfo(ctx, id)
}

// VerifyAttachmentItem 验证单个条目：OSS 对象存在且大小与记录一致则通过
//
// 参数:
//   - ctx: 上下文
//   - id: 条目 ID
//
// 返回值:
//   - *product.AttachmentMigrateItemInfo: 操作后的最新条目
//   - error: 条目不可验证时返回错误（验证不通过不算错误，落库为 verify_failed）
func (s *AttachmentService) VerifyAttachmentItem(ctx context.Context, id int64) (*product.AttachmentMigrateItemInfo, error) {
	item, err := s.getMigrateItem(ctx, id)
	if err != nil {
		return nil, err
	}
	if !model.CanVerifyAttachmentStatus(item.Status) {
		return nil, bizerr.New(codes.FailedPrecondition, fmt.Sprintf("当前状态（%s）不允许验证", migrateStatusLabel(item.Status)))
	}

	client, _, err := s.migrateOSSClient(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	updates := map[string]interface{}{"updated_at": now}
	if verr := verifyMigrateObject(ctx, client, item); verr != nil {
		updates["status"] = model.MigrateStatusVerifyFailed
		updates["error_msg"] = truncateErrMsg(verr)
	} else {
		updates["status"] = model.MigrateStatusVerified
		updates["verified_at"] = now
		updates["error_msg"] = ""
	}
	if err = s.db.GetDb().WithContext(ctx).Model(&model.AttachmentMigrateItem{}).
		Where("id = ?", item.ID).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("更新迁移条目失败: %w", err)
	}
	return s.getMigrateItemInfo(ctx, id)
}

// DeleteAttachmentLocal 删除单个条目的本地文件（仅验证成功条目可删）
// OSS 对象与附件记录不受影响；删除后条目进入 local_deleted 终态
//
// 参数:
//   - ctx: 上下文
//   - id: 条目 ID
//
// 返回值:
//   - *product.AttachmentMigrateItemInfo: 操作后的最新条目
//   - error: 条目状态不满足或删除失败时返回错误
func (s *AttachmentService) DeleteAttachmentLocal(ctx context.Context, id int64) (*product.AttachmentMigrateItemInfo, error) {
	item, err := s.getMigrateItem(ctx, id)
	if err != nil {
		return nil, err
	}
	if item.Status != model.MigrateStatusVerified {
		return nil, bizerr.New(codes.FailedPrecondition, fmt.Sprintf("仅验证成功的条目可删除本地文件（当前状态：%s）", migrateStatusLabel(item.Status)))
	}
	if err = removeLocalAttachmentFile(item.Path); err != nil {
		return nil, err
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	if err = s.db.GetDb().WithContext(ctx).Model(&model.AttachmentMigrateItem{}).
		Where("id = ?", item.ID).
		Updates(map[string]interface{}{
			"status":           model.MigrateStatusLocalDeleted,
			"local_deleted_at": now,
			"updated_at":       now,
		}).Error; err != nil {
		return nil, fmt.Errorf("更新迁移条目失败: %w", err)
	}
	return s.getMigrateItemInfo(ctx, id)
}

// DeleteAttachmentLocalVerified 一键删除全部已验证条目的本地文件
// 逐个删除本地文件后批量更新状态；文件删除失败的单条跳过并记录日志
//
// 参数:
//   - ctx: 上下文
//
// 返回值:
//   - int32: 实际删除的文件数
//   - error: 查询或状态更新失败时返回错误
func (s *AttachmentService) DeleteAttachmentLocalVerified(ctx context.Context) (int32, error) {
	var items []*model.AttachmentMigrateItem
	if err := s.db.GetDb().WithContext(ctx).
		Where("status = ?", model.MigrateStatusVerified).
		Limit(migrateScanLimit).
		Find(&items).Error; err != nil {
		return 0, fmt.Errorf("查询已验证条目失败: %w", err)
	}

	deleted := int32(0)
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		if err := removeLocalAttachmentFile(item.Path); err != nil {
			log.Printf("删除本地文件失败（跳过）path=%s: %v", item.Path, err)
			continue
		}
		ids = append(ids, item.ID)
		deleted++
	}
	if len(ids) > 0 {
		now := time.Now().Format("2006-01-02 15:04:05")
		if err := s.db.GetDb().WithContext(ctx).Model(&model.AttachmentMigrateItem{}).
			Where("id IN ?", ids).
			Updates(map[string]interface{}{
				"status":           model.MigrateStatusLocalDeleted,
				"local_deleted_at": now,
				"updated_at":       now,
			}).Error; err != nil {
			return deleted, fmt.Errorf("更新迁移条目失败: %w", err)
		}
	}
	return deleted, nil
}

// --- 内部辅助 ---

// getMigrateItem 按主键查询单个迁移条目
//
// 参数:
//   - ctx: 上下文
//   - id: 条目 ID
//
// 返回值:
//   - *model.AttachmentMigrateItem: 条目
//   - error: 不存在时返回错误
func (s *AttachmentService) getMigrateItem(ctx context.Context, id int64) (*model.AttachmentMigrateItem, error) {
	var item model.AttachmentMigrateItem
	if err := s.db.GetDb().WithContext(ctx).First(&item, "id = ?", id).Error; err != nil {
		return nil, errors.New("迁移条目不存在")
	}
	return &item, nil
}

// getMigrateItemInfo 查询单个迁移条目并转换为 proto 信息（操作类接口统一返回）
//
// 参数:
//   - ctx: 上下文
//   - id: 条目 ID
//
// 返回值:
//   - *product.AttachmentMigrateItemInfo: 条目信息
//   - error: 不存在时返回错误
func (s *AttachmentService) getMigrateItemInfo(ctx context.Context, id int64) (*product.AttachmentMigrateItemInfo, error) {
	item, err := s.getMigrateItem(ctx, id)
	if err != nil {
		return nil, err
	}
	return toProtoMigrateItem(item), nil
}

// loadMigrateItem 按 path 查询迁移条目（扫描 upsert 用），不存在不算错误
//
// 参数:
//   - ctx: 上下文
//   - path: 对象相对路径
//
// 返回值:
//   - *model.AttachmentMigrateItem: 条目（未命中为 nil）
//   - bool: 是否命中
func (s *AttachmentService) loadMigrateItem(ctx context.Context, path string) (*model.AttachmentMigrateItem, bool) {
	var item model.AttachmentMigrateItem
	err := s.db.GetDb().WithContext(ctx).First(&item, "path = ?", path).Error
	if err != nil {
		return nil, false
	}
	return &item, true
}

// updateMigrateItemStatus 更新条目状态与错误信息（轻量封装）
//
// 参数:
//   - ctx: 上下文
//   - id: 条目 ID
//   - status: 目标状态
//   - errMsg: 错误信息（成功时传空）
func (s *AttachmentService) updateMigrateItemStatus(ctx context.Context, id int64, status, errMsg string) {
	err := s.db.GetDb().WithContext(ctx).Model(&model.AttachmentMigrateItem{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     status,
			"error_msg":  errMsg,
			"updated_at": time.Now().Format("2006-01-02 15:04:05"),
		}).Error
	if err != nil {
		log.Printf("更新迁移条目状态失败 id=%d status=%s: %v", id, status, err)
	}
}

// failMigrateItem 迁移失败兜底：落库 failed 状态与错误信息后，将业务错误返回给调用方
//
// 参数:
//   - ctx: 上下文
//   - item: 条目
//   - bizErr: 业务错误
//
// 返回值:
//   - error: 原样返回 bizErr
func (s *AttachmentService) failMigrateItem(ctx context.Context, item *model.AttachmentMigrateItem, bizErr error) error {
	s.updateMigrateItemStatus(ctx, item.ID, model.MigrateStatusFailed, truncateErrMsg(bizErr))
	return bizErr
}

// countMigrateOverview 按状态分组统计迁移条目，生成概览
//
// 参数:
//   - ctx: 上下文
//   - missing: 附件表有记录但本地文件缺失的数量（仅扫描响应填充，其余场景传 0）
//
// 返回值:
//   - *product.AttachmentMigrateOverview: 概览统计
//   - error: 查询失败时返回错误
func (s *AttachmentService) countMigrateOverview(ctx context.Context, missing int32) (*product.AttachmentMigrateOverview, error) {
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	if err := s.db.GetDb().WithContext(ctx).Model(&model.AttachmentMigrateItem{}).
		Select("status, COUNT(*) AS count").
		Group("status").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("统计迁移概览失败: %w", err)
	}

	overview := &product.AttachmentMigrateOverview{Missing: missing}
	for _, r := range rows {
		overview.Total += int32(r.Count)
		switch r.Status {
		case model.MigrateStatusPending:
			overview.Pending = int32(r.Count)
		case model.MigrateStatusMigrating:
			overview.Migrating = int32(r.Count)
		case model.MigrateStatusMigrated:
			overview.Migrated = int32(r.Count)
		case model.MigrateStatusVerified:
			overview.Verified = int32(r.Count)
		case model.MigrateStatusFailed:
			overview.Failed = int32(r.Count)
		case model.MigrateStatusVerifyFailed:
			overview.VerifyFailed = int32(r.Count)
		case model.MigrateStatusLocalDeleted:
			overview.LocalDeleted = int32(r.Count)
		case model.MigrateStatusOrphan:
			overview.Orphan = int32(r.Count)
		}
	}
	return overview, nil
}

// migrateOSSClient 校验 OSS 配置并构建迁移用客户端
//
// 参数:
//   - ctx: 上下文
//
// 返回值:
//   - *remote.OSSClient: OSS 客户端
//   - *model.AttachmentConfig: 当前附件配置
//   - error: 配置缺失或客户端构建失败时返回错误
func (s *AttachmentService) migrateOSSClient(ctx context.Context) (*remote.OSSClient, *model.AttachmentConfig, error) {
	cfg := s.getConfigCached(ctx)
	if cfg == nil {
		return nil, nil, errors.New("附件配置不存在")
	}
	ossCfg, err := buildOSSConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	client, err := remote.NewOSSClient(*ossCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("初始化 OSS 客户端失败: %w", err)
	}
	return client, cfg, nil
}

// verifyMigrateObject 校验 OSS 对象与条目一致：对象存在且大小与记录一致
// 使用 List(prefix=path) 拿对象元信息（path 为完整 Key 时按字典序排最前），
// 同时校验返回的 Key 与 path 精确相等，避免同前缀对象误判
//
// 参数:
//   - ctx: 上下文
//   - client: OSS 客户端
//   - item: 迁移条目
//
// 返回值:
//   - error: 对象缺失或大小不一致时返回描述性错误
func verifyMigrateObject(ctx context.Context, client *remote.OSSClient, item *model.AttachmentMigrateItem) error {
	res, err := client.List(ctx, remote.ListOptions{Prefix: item.Path, MaxKeys: 10})
	if err != nil {
		return fmt.Errorf("查询 OSS 对象失败: %w", err)
	}
	for _, f := range res.Files {
		if f.Name != item.Path {
			continue
		}
		if f.Size != item.Size {
			return bizerr.New(codes.FailedPrecondition, fmt.Sprintf("OSS 对象大小不一致：本地 %d 字节，OSS %d 字节", item.Size, f.Size))
		}
		return nil
	}
	return errors.New("OSS 对象不存在")
}

// removeLocalAttachmentFile 删除本地附件文件（文件不存在视为已删除，不报错）
//
// 参数:
//   - path: 对象相对路径
//
// 返回值:
//   - error: 删除失败时返回错误
func removeLocalAttachmentFile(path string) error {
	root, err := attachmentRootDir()
	if err != nil {
		return err
	}
	fullPath := filepath.Join(root, filepath.FromSlash(path))
	if err = os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除本地文件失败: %w", err)
	}
	return nil
}

// scanLocalAttachmentFiles 遍历本地附件目录，收集全部文件的相对路径与大小
//
// 参数:
//   - root: 本地附件根目录
//
// 返回值:
//   - map[string]int64: 相对路径（/ 分隔）→ 文件大小（字节）
//   - error: 目录不存在（视为空集合）以外的读取错误
func scanLocalAttachmentFiles(root string) (map[string]int64, error) {
	files := make(map[string]int64)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return files, nil
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		files[filepath.ToSlash(rel)] = info.Size()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("扫描本地附件目录失败: %w", err)
	}
	return files, nil
}

// pathKeys 提取本地文件集合的全部相对路径（清理失效条目用）
//
// 参数:
//   - files: 相对路径 → 大小
//
// 返回值:
//   - []string: 路径列表（空集合时返回含单个空串的切片，保证 SQL NOT IN 合法）
func pathKeys(files map[string]int64) []string {
	if len(files) == 0 {
		return []string{""}
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	return keys
}

// toProtoMigrateItem 将迁移条目模型转换为 proto 信息
//
// 参数:
//   - item: 条目模型
//
// 返回值:
//   - *product.AttachmentMigrateItemInfo: proto 信息
func toProtoMigrateItem(item *model.AttachmentMigrateItem) *product.AttachmentMigrateItemInfo {
	return &product.AttachmentMigrateItemInfo{
		Id:             item.ID,
		Path:           item.Path,
		Name:           item.Name,
		Type:           item.Type,
		Size:           item.Size,
		Width:          int32(item.Width),
		Height:         int32(item.Height),
		Status:         item.Status,
		ErrorMsg:       item.ErrorMsg,
		MigratedAt:     item.MigratedAt,
		VerifiedAt:     item.VerifiedAt,
		LocalDeletedAt: item.LocalDeletedAt,
		CreatedAt:      item.CreatedAt,
	}
}

// truncateErrMsg 截断错误信息到条目字段可容纳的长度
//
// 参数:
//   - err: 错误
//
// 返回值:
//   - string: 截断后的错误描述
func truncateErrMsg(err error) string {
	msg := err.Error()
	const maxLen = 500
	r := []rune(msg)
	if len(r) > maxLen {
		return string(r[:maxLen])
	}
	return msg
}

// migrateStatusLabel 迁移条目状态的中文描述（错误提示用）
//
// 参数:
//   - status: 条目状态
//
// 返回值:
//   - string: 中文状态名
func migrateStatusLabel(status string) string {
	switch status {
	case model.MigrateStatusPending:
		return "待迁移"
	case model.MigrateStatusMigrating:
		return "迁移中"
	case model.MigrateStatusMigrated:
		return "已迁移"
	case model.MigrateStatusVerified:
		return "验证成功"
	case model.MigrateStatusFailed:
		return "迁移失败"
	case model.MigrateStatusVerifyFailed:
		return "验证失败"
	case model.MigrateStatusLocalDeleted:
		return "本地已删除"
	case model.MigrateStatusOrphan:
		return "孤儿文件"
	default:
		return status
	}
}
