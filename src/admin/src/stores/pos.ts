import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getCurrentPosShift } from '@/api/order'
import type { PosShift } from '@/api/types'

/**
 * 待引用到收银台的订单商品项（订单查询「引用到收银台」写入，商品下单 Tab 挂载时消费）
 * 附商品名与档案价快照：引用后购物车可直接展示，成交金额仍以后端实时结算为准
 */
export interface PosPendingImportItem {
  sku_id: number
  quantity: number
  /** 商品名（含规格）快照 */
  product_name: string
  /** 规格名快照 */
  spec_name?: string
  /** 档案价快照（改价单取 original_price，改价不带入） */
  price: number
}

/**
 * 收银台共享状态（布局壳/收银台/开工引导共享）
 * 班次 + 跨 Tab 传递的待引用商品清单（Tab 组件 v-if 切换即销毁，跨页数据走 store）
 */
export const usePosStore = defineStore('pos', () => {
  /** 当前进行中的班次；null 表示无进行中班次（未开工） */
  const currentShift = ref<PosShift | null>(null)
  /** 是否已完成首次加载（避免引导页闪烁） */
  const loaded = ref(false)
  /** 待引用到收银台的商品清单（空=无待引用） */
  const pendingImport = ref<PosPendingImportItem[]>([])

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
   * 写入待引用商品清单（订单查询「引用到收银台」时调用）
   * @param items 订单商品明细（sku_id + 数量）
   */
  function setPendingImport(items: PosPendingImportItem[]) {
    pendingImport.value = items
  }

  /**
   * 取走待引用清单（商品下单 Tab 挂载时消费，取后即清）
   * @returns 待引用商品清单（可能为空数组）
   */
  function consumePendingImport(): PosPendingImportItem[] {
    const items = pendingImport.value
    pendingImport.value = []
    return items
  }

  /**
   * 清空本地班次状态（退出登录时调用）
   */
  function clearShift() {
    currentShift.value = null
    loaded.value = false
    pendingImport.value = []
  }

  return {
    currentShift,
    loaded,
    pendingImport,
    loadCurrentShift,
    setPendingImport,
    consumePendingImport,
    clearShift,
  }
})
