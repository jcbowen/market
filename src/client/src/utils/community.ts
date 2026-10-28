/**
 * 社区上下文工具
 *
 * C 端商品/价格类接口一律按社区归属校验（跨社区读取被服务端拒绝），
 * 因此这些页面在发请求前必须先确定「当前浏览社区」。
 */
import { getCommunityList } from '@/api/user'
import { useUserStore } from '@/stores/user'

/**
 * 确保当前社区信息存在
 * 本地未选中社区时取营业社区列表首项兜底；已登录时同步服务端绑定，同步失败降级为本地选中
 * @param store 用户 store，缺省取当前实例
 * @returns 就绪后的社区 ID
 * @throws 无可选社区时抛出 Error('no community')，由调用方终止加载
 *
 * 使用示例：
 *   const communityId = await ensureCommunity()
 *   const res = await getProductDetail(id, communityId)
 */
export async function ensureCommunity(store?: ReturnType<typeof useUserStore>): Promise<number> {
  const userStore = store ?? useUserStore()
  const current = userStore.communityInfo?.id
  if (current) return current

  const res = await getCommunityList({ page: 1, page_size: 1 })
  const first = res.data?.list?.[0]
  if (!first) {
    uni.showToast({ title: '暂无可选社区', icon: 'none' })
    throw new Error('no community')
  }
  try {
    await userStore.switchCommunity(first)
  } catch {
    // 同步服务端失败时降级为本地选中，保证页面仍可浏览
    userStore.setCommunity(first)
  }
  return first.id
}
