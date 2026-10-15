import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getCurrentPosShift } from '@/api/order'
import type { PosShift } from '@/api/types'

/**
 * 收银台班次状态（布局壳/收银台/开工引导共享）
 */
export const usePosStore = defineStore('pos', () => {
  /** 当前进行中的班次；null 表示无进行中班次（未开工） */
  const currentShift = ref<PosShift | null>(null)
  /** 是否已完成首次加载（避免引导页闪烁） */
  const loaded = ref(false)

  /**
   * 拉取当前进行中的班次
   * 后端 id=0 表示无班次，此处归一为 null
   * @param force 强制刷新（交班/开工后调用）
   */
  async function loadCurrentShift(force = false) {
    if (loaded.value && !force) return
    try {
      const res = await getCurrentPosShift()
      const shift = res.data.data
      currentShift.value = shift && shift.id > 0 ? shift : null
    } catch {
      currentShift.value = null
    } finally {
      loaded.value = true
    }
  }

  /**
   * 清空本地班次状态（退出登录时调用）
   */
  function clearShift() {
    currentShift.value = null
    loaded.value = false
  }

  return { currentShift, loaded, loadCurrentShift, clearShift }
})
