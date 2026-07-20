import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

/**
 * 应用级状态管理 Store
 * 管理菜单折叠状态、当前路由标题等全局 UI 状态
 */
export const useAppStore = defineStore('app', () => {
  /** 侧边栏是否折叠 */
  const collapsed = ref(false)
  /** 当前页面标题 */
  const title = ref('')

  /** 内容区宽度类名 */
  const contentClass = computed(() => (collapsed.value ? 'collapsed' : ''))

  /**
   * 切换侧边栏折叠状态
   */
  function toggleCollapse() {
    collapsed.value = !collapsed.value
  }

  /**
   * 设置侧边栏折叠状态
   * @param value 是否折叠
   */
  function setCollapse(value: boolean) {
    collapsed.value = value
  }

  /**
   * 设置当前页面标题
   * @param value 标题
   */
  function setTitle(value: string) {
    title.value = value
  }

  return {
    collapsed,
    title,
    contentClass,
    toggleCollapse,
    setCollapse,
    setTitle,
  }
})
