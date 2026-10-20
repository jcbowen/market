package service

import (
	"context"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// 管理端按 ID 操作的 RPC 统一采用「操作者社区语境」约定：
// 网关对普通管理员下发其当前社区 ID，对超管下发 0（表示不设语境、放行跨社区操作），
// 内部服务间调用（下单扣库存等）同样传 0。约定一旦遗漏即构成跨社区越权，
// 因此所有写接口与按 ID 读接口都必须经本文件的方法收口。

// EnsureScope 校验操作者社区语境是否覆盖资源归属社区
// scope<=0 放行；资源归属为 0（全平台共享数据，如全局 banner）时仅超管可操作
// 文案口径与订单退款族、券/满减/运费族一致，均返回 PermissionDenied
//
// 参数：
//   - scope: 操作者社区语境（0=超管或内部调用）
//   - owner: 资源归属社区 ID
//   - name: 资源名称，用于错误文案（如「商品」「分类」）
//
// 返回值：
//   - error: 语境与归属不一致时返回 PermissionDenied
//
// 使用示例：
//
//	if err := EnsureScope(req.CommunityId, p.CommunityID, "商品"); err != nil {
//		return nil, err
//	}
func EnsureScope(scope, owner int64, name string) error {
	if scope <= 0 || owner == scope {
		return nil
	}
	return bizerr.New(codes.PermissionDenied, "无权操作其他社区的"+name)
}

// ensureCategoryInCommunity 校验分类存在且归属指定社区
// 商品挂到分类是本社区内的引用关系，跨社区引用会让 A 社区商品出现在 B 社区分类下（数据串户）
//
// 参数：
//   - ctx: 上下文
//   - db: 已绑定上下文的 GORM 实例
//   - categoryID: 待引用的分类 ID（<=0 表示未指定，跳过）
//   - communityID: 商品归属社区 ID
//
// 返回值：
//   - error: 分类不存在或不属于该社区时返回 NotFound
func ensureCategoryInCommunity(ctx context.Context, db *gorm.DB, categoryID, communityID int64) error {
	if categoryID <= 0 {
		return nil
	}
	var count int64
	if err := db.WithContext(ctx).Model(&model.Category{}).
		Where("id = ? AND community_id = ?", categoryID, communityID).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return bizerr.New(codes.NotFound, "分类不存在或不属于当前社区")
	}
	return nil
}
