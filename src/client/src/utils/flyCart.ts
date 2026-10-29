/**
 * 加购飞球动画的坐标工具
 * 全程使用视口坐标（px）：飞球组件为 fixed 定位，与触点/节点查询的 client 坐标系自洽
 */

/** 视口坐标点（px），飞球动画的起点/落点 */
export interface FlyPoint {
  x: number
  y: number
}

/** 飞球组件对外暴露的实例方法集 */
export type FlyToCartInstance = {
  /** 播放一次从 from 到 to 的抛物线飞球动画，动画结束后 resolve */
  fly: (from: FlyPoint, to: FlyPoint) => Promise<void>
}

/**
 * 从 uni 点击事件对象中提取触点视口坐标
 * 优先取触点 clientX/Y（H5/小程序端语义一致），退回 tap 事件的 detail.x/y
 * @param e 模板中透传的 $event 事件对象
 * @returns 触点坐标；事件结构无法识别时返回 null（由调用方走默认起点）
 */
export function pointFromEvent(e: unknown): FlyPoint | null {
  const ev = e as {
    touches?: Array<{ clientX?: number; clientY?: number }>
    changedTouches?: Array<{ clientX?: number; clientY?: number }>
    detail?: { x?: number; y?: number }
  } | undefined
  const touch = ev?.changedTouches?.[0] || ev?.touches?.[0]
  if (typeof touch?.clientX === 'number' && typeof touch?.clientY === 'number') {
    return { x: touch.clientX, y: touch.clientY }
  }
  if (typeof ev?.detail?.x === 'number' && typeof ev?.detail?.y === 'number') {
    return { x: ev.detail.x, y: ev.detail.y }
  }
  return null
}

/**
 * 查询页面内落点节点的中心视口坐标
 * 页面级查询（不带 in()），selector 须为页面模板中的节点
 * @param selector 落点节点选择器
 * @returns 节点中心坐标；节点不存在或查询失败时兜底为屏幕底部中央
 */
export function getFlyTarget(selector: string): Promise<FlyPoint> {
  return new Promise((resolve) => {
    uni
      .createSelectorQuery()
      .select(selector)
      .boundingClientRect((rect) => {
        // select() 运行时返回单个节点，类型联合了 selectAll 的数组形态，先收窄
        const node = Array.isArray(rect) ? rect[0] : rect
        if (node && typeof node.left === 'number' && typeof node.width === 'number') {
          resolve({
            x: node.left + node.width / 2,
            y: (node.top || 0) + (node.height || 0) / 2,
          })
          return
        }
        resolve(getDefaultFlyPoint())
      })
      .exec()
  })
}

/**
 * 默认起/落点：屏幕底部中央（购物车栏/弹窗方向）
 * 用于无点击坐标（如弹窗确认）或节点查询失败的场景
 * @returns 屏幕底部中央坐标（px）
 */
export function getDefaultFlyPoint(): FlyPoint {
  const { windowWidth = 375, windowHeight = 667 } = uni.getSystemInfoSync()
  return { x: windowWidth / 2, y: windowHeight - 60 }
}
