import { nextTick, type Ref } from 'vue'

/**
 * 打开弹窗并保证其位于顶层
 * Naive UI 弹窗在挂载时自动分配递增的 z-index（后挂载者更高）。
 * 当目标弹窗已打开（如从会员订单弹窗切单到详情弹窗、或反向跳转）时，
 * 直接置 show=true 不会改变层级也不会触发内部 watch 重载数据；
 * 此时先关后开（nextTick 后重挂载）即可置顶并触发数据重新加载。
 * @param show 弹窗显示状态的 ref（配合 v-model:show 使用）
 * @example
 * const detailVisible = ref(false)
 * function openDetail(id: number) {
 *   detailId.value = id
 *   reopenModal(detailVisible)
 * }
 */
export function reopenModal(show: Ref<boolean>) {
  if (show.value) {
    show.value = false
    nextTick(() => {
      show.value = true
    })
  } else {
    show.value = true
  }
}
