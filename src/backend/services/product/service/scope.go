package service

import (
	"context"
	"log"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

// 管理端按 ID 操作的 RPC 统一采用「操作者社区语境」约定：
// 网关对普通管理员下发其当前社区 ID，对超管下发 0（表示不设语境、放行跨社区操作）。
// 交易链路（下单/加购/收银）不适用「内部调用传 0」的旧约定：community_id 是成交社区，
// 必须由调用方（订单服务）按下单社区原样传入，否则跨社区取用价格/库存/优惠无从拦截。
// 约定一旦遗漏即构成跨社区越权，因此所有写接口与按 ID 读接口都必须经本文件的方法收口。

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

// EnsureTradeAccess 单行「取用/交易」统一判定：社区归属 + 可选的可售收口
// 归属判定永远执行（scope>0 即逐条比对，与调用方是谁无关）；可售判定只在 requireSellable=true
// 时执行，判定的具体实现仍分别留在 EnsureProductSellable（商品 status=1）与
// EnsureSkuSellable（SKU status=1），本函数只负责「一次调用、口径不散落」。
// 商品与 SKU 任一侧传 nil 表示该行只判另一侧（批量入口各自只加载一侧模型）；
// 下单链路两个批量入口成对调用（BatchGetSkus + BatchGetProducts），
// 因此必须传同一个 community_id，SKU 与商品归属漂移时也会被其中一侧拦下。
//
// scope<=0 在管理端是合法语义（超管跨社区、内部调用不设语境），但在交易侧（requireSellable=true）
// 意味着「成交社区没传进来」——此时归属校验整体失效，本应只在本社区可售的商品可被跨社区取用。
// 现网调用方是否都补齐了 community_id 无从静态证明，硬拒会打死未知的内部调用方，
// 故先落告警观测一轮（C5）：日志按「交易入口 + 无社区语境」这一唯一可疑组合打，不打在管理端路径上。
//
// 参数：
//   - scope: 操作者/交易社区语境（<=0 跳过归属校验，见 EnsureScope）
//   - p: 商品模型（可为 nil）
//   - sku: SKU 模型（可为 nil）
//   - requireSellable: 是否要求可售（交易入口传 true，管理端/展示口径传 false）
//
// 返回值：
//   - error: 越权返回 PermissionDenied；不可售返回 FailedPrecondition
//
// 使用示例：
//
//	if err := EnsureTradeAccess(communityID, p, nil, requireSellable); err != nil {
//		return nil, err
//	}
func EnsureTradeAccess(scope int64, p *model.Product, sku *model.Sku, requireSellable bool) error {
	if requireSellable && scope <= 0 {
		log.Printf("[WARN] 交易入口缺少社区语境，商品归属校验已跳过 product_id=%d sku_id=%d", productIDOf(p), skuIDOf(sku))
	}
	if p != nil {
		if err := EnsureScope(scope, p.CommunityID, "商品"); err != nil {
			return err
		}
	}
	if sku != nil {
		if err := EnsureScope(scope, sku.CommunityID, "SKU"); err != nil {
			return err
		}
	}
	if !requireSellable {
		return nil
	}
	if p != nil {
		if err := EnsureProductSellable(p); err != nil {
			return err
		}
	}
	if sku != nil {
		if err := EnsureSkuSellable(sku); err != nil {
			return err
		}
	}
	return nil
}

// productIDOf 取商品 ID 供告警定位（nil 安全，批量入口可能只加载 SKU 一侧）
//
// 参数：
//   - p: 商品模型，可为 nil
//
// 返回值：
//   - int64: 商品 ID；nil 时为 0
func productIDOf(p *model.Product) int64 {
	if p == nil {
		return 0
	}
	return p.ID
}

// skuIDOf 取 SKU ID 供告警定位（nil 安全，批量入口可能只加载商品一侧）
//
// 参数：
//   - sku: SKU 模型，可为 nil
//
// 返回值：
//   - int64: SKU ID；nil 时为 0
func skuIDOf(sku *model.Sku) int64 {
	if sku == nil {
		return 0
	}
	return sku.ID
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
