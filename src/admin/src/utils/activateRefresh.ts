import { onActivated } from 'vue'

/**
 * keep-alive 缓存页「切回即刷新」
 *
 * 页面在 onMounted 里拉首屏数据后调用本函数补上再次进入的刷新：
 * 标签页缓存命中时组件不会重新挂载，onMounted 永不触发，用户看到的是离开前的旧快照
 * （新增/编辑后返回列表、顶栏切社区后仍显示上一社区的数据）；
 * 缓存命中时 Vue 在 mounted 之后紧跟一次 activated，那次必须跳过，否则每次进页都双请求；
 * 之后每次从别的标签切回只触发 activated，此时重新拉数。
 *
 * 未进缓存的页面（keep-alive include 未命中）不会触发 activated，首屏仍由调用方的 onMounted 保证。
 *
 * @param load 数据加载函数，与 onMounted 传入的是同一个（或同一组加载的包装）
 * @example
 *   onMounted(loadData)
 *   // keep-alive 缓存下切回本页时重新拉取，避免展示离开前的旧数据
 *   refreshOnActivated(loadData)
 */
export function refreshOnActivated(load: () => unknown): void {
  let skipFirstActivate = true
  onActivated(() => {
    if (skipFirstActivate) {
      skipFirstActivate = false
      return
    }
    load()
  })
}
