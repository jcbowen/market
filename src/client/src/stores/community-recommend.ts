/**
 * 社区推荐弹窗状态
 * App.vue 启动时自动选社区（未登录首选 / 绑定失效兜底 / 社区停业切换）后，
 * 通过 pending 载荷驱动挂载在 tabbar 页面的 community-recommend-dialog 弹窗确认；
 * 弹窗组件消费（确认/去选择页）后 clear。载荷未被消费时由 App.vue 兜底降级为 toast。
 */

import { defineStore } from 'pinia'
import { ref } from 'vue'

export interface CommunityRecommendPayload {
  /** 弹窗标题，如「为您推荐最近社区」/「社区已停业，已为您切换」 */
  title: string
  /** 推荐的社区名称 */
  communityName: string
  /** 副文案：系统定位为「距离您 X」、IP 定位为「根据当前位置为您推荐」、无定位为「已为您选择默认社区」 */
  subtitle: string
}

export const useCommunityRecommendStore = defineStore('communityRecommend', () => {
  /** 待展示的推荐弹窗载荷，null 表示无待展示内容 */
  const pending = ref<CommunityRecommendPayload | null>(null)

  /**
   * 展示推荐弹窗（载荷对象引用用于兜底降级时的同一性判断）
   * @param payload 推荐载荷
   */
  function show(payload: CommunityRecommendPayload) {
    pending.value = payload
  }

  /** 关闭弹窗并清空载荷 */
  function clear() {
    pending.value = null
  }

  return { pending, show, clear }
})
