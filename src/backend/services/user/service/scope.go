package service

import (
	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
)

// EnsureScope 校验操作者社区语境是否覆盖资源归属社区
// 与 product 服务 service.EnsureScope 同口径：scope<=0 表示超管或内部调用（放行），
// 资源归属为 0 的历史保留数据（community_id=0 的遗留角色、全局 banner）仅超管可操作
//
// 参数：
//   - scope: 操作者社区语境（0=超管或内部调用）
//   - owner: 资源归属社区 ID
//   - name: 资源名称，用于错误文案（如「角色」）
//
// 返回值：
//   - error: 语境与归属不一致时返回 PermissionDenied
//
// 使用示例：
//
//	if err := EnsureScope(req.CommunityId, role.CommunityID, "角色"); err != nil {
//		return err
//	}
func EnsureScope(scope, owner int64, name string) error {
	if scope <= 0 || owner == scope {
		return nil
	}
	return bizerr.New(codes.PermissionDenied, "无权操作其他社区的"+name)
}
