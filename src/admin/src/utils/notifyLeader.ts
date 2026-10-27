/**
 * 多标签页播报选主（utils/orderNotify.ts 的去重基础设施）
 *
 * 问题：每个后台 tab 各持一条独立 WS 连接（realtime 层模块级单例 per-tab），
 * 同一事件到达时每个 tab 各自执行提示音/语音——3 个 tab 扫一笔单响 3 声。
 * 数据同步不去重（每个 tab 都要刷自己的待办，见 realtime 层），只有「发声」跨 tab 选主。
 *
 * 三级降级链（依次尝试，取第一个可用的）：
 * 1. Web Locks（navigator.locks，需安全上下文）：长期持锁者为 leader，
 *    持锁 Promise 永不 resolve，tab 关闭/崩溃自动释放，其余 tab 立即竞争接管；
 * 2. BroadcastChannel 认领：事件到达后带 key 抖动认领，先到先得，已有人认领则静默；
 *    （无常驻 leader，靠 per-event 竞速去重）
 * 3. 都不可用（非安全上下文的极老浏览器）：维持现状不去重。
 *
 * 边界：数据同步（待办/权限快照/列表刷新）不经过本模块，各 tab 照常执行。
 */

/** Web Locks 名称（同级 tab 互斥） */
const LEADER_LOCK_NAME = 'market-admin-notify-leader'
/** BroadcastChannel 频道名（认领模式） */
const CLAIM_CHANNEL_NAME = 'market-admin-notify-claim'
/** 认领竞速的随机抖动上限（ms）：让「同时收到事件」的各 tab 错开认领时机 */
const CLAIM_JITTER_MAX_MS = 120

/**
 * 去重键集合的容量上限（FIFO 淘汰）
 * 收银页可 7×24 常开，两个 Set 若只增不减会随订单量线性增长。
 * 键只需在「同一事实被重复投递」的窗口内有效（播报去重），留最近 500 条远超需要
 */
const MAX_TRACKED_KEYS = 500

/** 当前生效的选主方式 */
let mode: 'locks' | 'channel' | 'none' = 'none'
/** locks 模式下本 tab 是否为 leader（持锁者） */
let isLeaderTab = false
/** 认领模式的频道实例 */
let claimChannel: BroadcastChannel | null = null
/** 本 tab 已执行过播报的 key（防本 tab 重复播报同一事实） */
const announcedKeys = new Set<string>()
/** 已收到他人认领的 key（认领模式下放弃播报） */
const claimedKeys = new Set<string>()

/**
 * 记录去重键并按 FIFO 淘汰超出的部分
 * @param bag 目标集合（本 tab 已播报 / 已被他人认领）
 * @param key 播报事实的唯一键
 */
function rememberKey(bag: Set<string>, key: string): void {
  bag.add(key)
  while (bag.size > MAX_TRACKED_KEYS) {
    const oldest = bag.values().next().value
    if (oldest === undefined) break
    bag.delete(oldest)
  }
}

/**
 * 尝试用 Web Locks 选主
 * 持锁回调返回永不 resolve 的 Promise：锁随 tab 生命周期持有，
 * tab 关闭浏览器自动释放，排队的其余 tab 中下一个 request 回调即接管
 * @returns locks 可用返回 true（即使尚未持到锁，排队等待中）
 */
function setupLeaderByLocks(): boolean {
  if (typeof navigator === 'undefined' || !navigator.locks) return false
  // request 排队等待期间回调不执行；获得锁后成为 leader
  navigator.locks
    .request(LEADER_LOCK_NAME, () => {
      isLeaderTab = true
      return new Promise<void>(() => {})
    })
    .catch(() => {
      // 锁获取异常（如上下文销毁）：保持非 leader 静默，不做二次选举
    })
  return true
}

/**
 * 用 BroadcastChannel 认领模式去重
 * @returns BroadcastChannel 可用返回 true
 */
function setupClaimByChannel(): boolean {
  if (typeof BroadcastChannel === 'undefined') return false
  try {
    claimChannel = new BroadcastChannel(CLAIM_CHANNEL_NAME)
  } catch {
    return false
  }
  claimChannel.onmessage = (event: MessageEvent) => {
    const key = (event.data as { key?: string } | null)?.key
    if (key) rememberKey(claimedKeys, key)
  }
  return true
}

/**
 * 初始化播报选主（幂等；由 setupOrderNotify 调用，也可独立调用）
 * 三级降级链取第一个可用项
 */
export function setupNotifyLeader(): void {
  if (mode !== 'none') return
  if (setupLeaderByLocks()) {
    mode = 'locks'
    return
  }
  if (setupClaimByChannel()) {
    mode = 'channel'
  }
  // 都不可用：mode 保持 none，播报行为与拆分前一致
}

/**
 * 当前选主方式（测试与诊断用）
 * @returns 'locks' | 'channel' | 'none'
 */
export function notifyLeaderMode(): 'locks' | 'channel' | 'none' {
  return mode
}

/**
 * 以选主身份执行一次播报
 * - locks 模式：仅 leader 执行，其余 tab 静默（leader 关闭后接管生效）
 * - channel 模式：带随机抖动竞速认领，先到先得，已有人认领或本 tab 已播报则静默
 * - none 模式：直接执行（维持历史行为）
 * @param key 播报事实的唯一键（如 `paid-${order_no}`），认领与去重均按此键
 * @param fire 实际播报动作（提示音/语音/浏览器通知）
 * @使用示例 runAsAnnouncer(`paid-${info.order_no}`, () => doAnnounce(info))
 */
export function runAsAnnouncer(key: string, fire: () => void): void {
  switch (mode) {
    case 'locks':
      // 非 leader 不发声也无需记账：接管者只该播「接管之后」的新事实，
      // 重放接管前漏播的旧单反而是错的
      if (!isLeaderTab) return
      if (announcedKeys.has(key)) return
      rememberKey(announcedKeys, key)
      fire()
      return
    case 'channel': {
      if (announcedKeys.has(key)) return
      rememberKey(announcedKeys, key)
      // 抖动后竞速：窗口内收到他人认领即放弃（浏览器跨 tab 消息异步送达，
      // 同步执行必然双方都抢不到先手，抖动是让先到者胜出的最小手段）
      setTimeout(() => {
        if (claimedKeys.has(key)) return
        claimChannel?.postMessage({ key })
        fire()
      }, Math.random() * CLAIM_JITTER_MAX_MS)
      return
    }
    default:
      // 维持历史行为：零去重直通
      fire()
  }
}

/** 重置选主状态（仅测试用：模块级状态跨用例污染时复位） */
export function __resetNotifyLeaderForTest(): void {
  mode = 'none'
  isLeaderTab = false
  claimChannel?.close()
  claimChannel = null
  announcedKeys.clear()
  claimedKeys.clear()
}
