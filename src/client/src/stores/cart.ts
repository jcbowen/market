/**
 * 购物车状态管理
 * 维护购物车数量角标缓存，提供数量更新与清空能力
 */

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getCart } from '@/api/cart'

export const useCartStore = defineStore('cart', () => {
  /** 购物车商品总数量 */
  const totalCount = ref(0)
  /** 购物车选中商品数量 */
  const selectedCount = ref(0)

  /** tabBar 角标数字，0 时不显示 */
  const badgeCount = computed(() => (totalCount.value > 0 ? totalCount.value : 0))

  /**
   * 设置购物车数量
   * @param total 总数量
   * @param selected 选中数量
   */
  function setCount(total: number, selected: number) {
    totalCount.value = total
    selectedCount.value = selected
    updateTabBarBadge()
  }

  /**
   * 从服务端同步购物车数量
   */
  async function syncCartCount() {
    try {
      const res = await getCart()
      if (res.data) {
        setCount(res.data.total_count, res.data.selected_count)
      }
    } catch {
      // 未登录时忽略错误
    }
  }

  /**
   * 清空本地购物车数量
   */
  function clearCount() {
    totalCount.value = 0
    selectedCount.value = 0
    removeTabBarBadge()
  }

  /**
   * 更新 tabBar 购物车角标
   */
  function updateTabBarBadge() {
    if (badgeCount.value > 0) {
      uni.setTabBarBadge({
        index: 2,
        text: String(badgeCount.value),
      })
    } else {
      removeTabBarBadge()
    }
  }

  /**
   * 移除 tabBar 购物车角标
   */
  function removeTabBarBadge() {
    uni.removeTabBarBadge({ index: 2, fail: () => {} })
  }

  return {
    totalCount,
    selectedCount,
    badgeCount,
    setCount,
    syncCartCount,
    clearCount,
    updateTabBarBadge,
    removeTabBarBadge,
  }
})
