import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

/** 标签页数据结构 */
export interface TabItem {
  /** 路由 fullPath，作为标签唯一标识 */
  path: string
  /** 标签标题，取路由 meta.title */
  title: string
  /** 对应路由 name，用于 keep-alive include 匹配组件名 */
  name?: string
  /** 是否参与页面缓存（hidden 页面不缓存，避免共用组件的表单/详情页串数据） */
  keepAlive: boolean
  /** 是否固定标签（账号兜底首页），固定标签不可关闭 */
  affix: boolean
}

/**
 * 标签页状态管理 Store
 * 维护访问过的标签列表与激活标签，并为 keep-alive 提供 include 组件名列表；
 * 关闭标签时从 include 移除对应组件名，缓存随之释放
 */
export const useTabsStore = defineStore('tabs', () => {
  /** 标签列表，初始为空，Layout 挂载时由 init 按账号兜底首页注入固定标签 */
  const tabs = ref<TabItem[]>([])
  /** 当前激活标签的 fullPath */
  const activePath = ref('')

  /** keep-alive include 列表：当前打开且参与缓存的标签对应组件名 */
  const cachedNames = computed(() =>
    tabs.value.filter((t) => t.keepAlive && t.name).map((t) => t.name as string),
  )

  /**
   * 初始化固定标签（Layout 挂载时按账号兜底首页注入，登录切换账号时天然重置）
   * @param homePath 兜底首页路径
   * @param title 固定标签标题
   * @param name 兜底首页路由 name
   */
  function init(homePath: string, title: string, name?: string) {
    tabs.value = [{ path: homePath, title, name, keepAlive: true, affix: true }]
    activePath.value = homePath
  }

  /**
   * 路由变化时同步标签：已存在则激活，不存在则新增
   * @param fullPath 当前路由 fullPath（作为标签唯一标识）
   * @param title 当前路由 meta.title
   * @param name 当前路由 name（symbol 形态视为未命名，不参与缓存匹配）
   * @param hidden 当前路由 meta.hidden（hidden 页面进标签栏但不缓存）
   */
  function syncWithRoute(fullPath: string, title: string, name: unknown, hidden: boolean) {
    activePath.value = fullPath
    if (tabs.value.some((t) => t.path === fullPath)) return
    tabs.value.push({
      path: fullPath,
      title: title || '未命名',
      name: typeof name === 'string' ? name : undefined,
      keepAlive: !hidden,
      affix: false,
    })
  }

  /**
   * 更新指定标签标题（参数页在业务数据加载后调用，让不同参数的标签可区分）
   * @param path 标签路径
   * @param title 新标题（如"编辑商品 · 商品名"）
   */
  function rename(path: string, title: string) {
    const tab = tabs.value.find((t) => t.path === path)
    if (tab) tab.title = title
  }

  /**
   * 关闭指定标签（固定标签不可关闭）
   * @param path 被关闭标签的路径
   * @returns 关闭的是激活标签时返回应跳转的目标路径，否则返回 null
   */
  function closeTab(path: string): string | null {
    const index = tabs.value.findIndex((t) => t.path === path)
    if (index === -1 || tabs.value[index].affix) return null
    tabs.value.splice(index, 1)
    if (path !== activePath.value) return null
    // 关闭的是当前标签：优先跳右侧标签，其次左侧，最后兜底固定标签
    const next = tabs.value[index] ?? tabs.value[index - 1] ?? tabs.value[0]
    if (!next) return null
    activePath.value = next.path
    return next.path
  }

  /** 关闭除固定标签与当前激活标签外的所有标签（当前标签保留，无需跳转） */
  function closeOthers() {
    tabs.value = tabs.value.filter((t) => t.affix || t.path === activePath.value)
  }

  /**
   * 关闭全部可关闭标签，回到固定标签
   * @returns 固定标签路径；无固定标签时返回空串（调用方据此不跳转）
   */
  function closeAll(): string {
    const home = tabs.value.find((t) => t.affix)
    tabs.value = home ? [home] : []
    activePath.value = home?.path ?? ''
    return activePath.value
  }

  /** 清空全部标签（登出时调用），下次进入 Layout 由 init 重建 */
  function reset() {
    tabs.value = []
    activePath.value = ''
  }

  return { tabs, activePath, cachedNames, init, syncWithRoute, rename, closeTab, closeOthers, closeAll, reset }
})
