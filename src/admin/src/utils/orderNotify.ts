import { onRealtime, isRealtimeConnected } from './realtime'
import { runAsAnnouncer, setupNotifyLeader } from './notifyLeader'
import type { OrderPaidInfo, OrderRefundInfo } from './realtime'

/**
 * 后台订单事件播报层（实时通道 utils/realtime.ts 的订阅者之一）
 *
 * 职责边界：只负责把事件变成人能感知到的提醒——提示音（WebAudio 合成）、
 * 语音播报（Web Speech）、浏览器系统通知（失焦兜底）。
 * 连接管理、断线重连、事件分发、权限快照刷新都在 realtime 层，本文件不持有 socket。
 *
 * 不做权限判定：服务端在推送时刻按握手权限快照过滤数据类事件
 * （gateway/ws/hub.go 的 msgRequiredPerm），无 order:view 的连接收不到 order.paid /
 * order.refund；前端判定挡不住越权，服务端过滤是唯一安全边界。
 *
 * 多标签页选主：每个 tab 各持一条 WS 连接，同一事件会送达所有 tab；
 * 发声（提示音/语音/系统通知）经 notifyLeader 选主，只有 leader tab 出声
 * （Web Locks 长期持锁，降级 BroadcastChannel 认领，详见 notifyLeader.ts）。
 * 数据同步不经过本层，各 tab 照常刷自己的数据。
 *
 * 浏览器自动播放限制：首次用户交互时解锁 AudioContext 与语音合成。
 */

/** WebAudio 上下文（懒创建，用于合成提示音） */
let audioCtx: AudioContext | null = null
/** 语音合成是否已入队过手势内语句（Safari 放行后续异步 speak 的前提） */
let speechUnlocked = false
/** 持有当前播报语句的强引用：Chrome 有 utterance 被 GC 导致播到一半静音的已知问题 */
let lastUtterance: SpeechSynthesisUtterance | null = null
/** 订阅注册标记：setupOrderNotify 幂等，重复调用不会叠加一份播报 */
let installed = false

/**
 * 取（或懒创建）WebAudio 上下文
 * @returns 可用的 AudioContext，创建失败（极老浏览器）时返回 null
 */
function getAudioContext(): AudioContext | null {
  if (audioCtx) return audioCtx
  const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
  if (!Ctx) return null
  try {
    audioCtx = new Ctx()
  } catch {
    return null
  }
  return audioCtx
}

/**
 * 首次用户交互解锁音频与浏览器通知
 * 监听常驻，内部各步骤幂等：
 * - AudioContext.resume：唤醒被自动播放策略挂起的音频上下文
 * - 语音合成：必须在用户手势调用栈内真实入队一条语句（Safari 才会放行后续
 *   异步回调里的 speak，Chrome/Safari 均会忽略空串语句，故用零音量空格），
 *   并顺带触发音色列表加载（Chrome 异步 getVoices）
 * - 浏览器通知授权：与语音解锁独立推进——若首次交互发生在连接建立前（WS 尚未
 *   连上，申请不到权限框），后续每次交互都会补申请，避免授权时机死锁；
 *   用户拒绝后 permission 不再是 default，自然不再打扰
 */
function unlockAudio() {
  getAudioContext()?.resume().catch(() => {})
  if (!speechUnlocked) {
    try {
      speechSynthesis.getVoices()
      const warmup = new SpeechSynthesisUtterance(' ')
      warmup.volume = 0
      speechSynthesis.speak(warmup)
      speechUnlocked = true
    } catch {
      // 语音合成不可用不影响提示音
    }
  }
  if (isRealtimeConnected() && typeof Notification !== 'undefined' && Notification.permission === 'default') {
    void Notification.requestPermission()
  }
}

/**
 * 播放单个提示音音符（正弦波 + 指数衰减包络）
 * @param ctx 音频上下文
 * @param freq 频率（Hz）
 * @param start 起始时间（ctx.currentTime 基准）
 * @param dur 时长（秒）
 */
function playTone(ctx: AudioContext, freq: number, start: number, dur: number) {
  const osc = ctx.createOscillator()
  const gain = ctx.createGain()
  osc.type = 'sine'
  osc.frequency.value = freq
  gain.gain.setValueAtTime(0.0001, start)
  gain.gain.exponentialRampToValueAtTime(0.35, start + 0.02)
  gain.gain.exponentialRampToValueAtTime(0.0001, start + dur)
  osc.connect(gain).connect(ctx.destination)
  osc.start(start)
  osc.stop(start + dur + 0.05)
}

/**
 * 播放"叮咚"提示音（两连音），WebAudio 实时合成、无需静态音频文件
 * 上下文被自动播放策略挂起时（页面刷新后未交互过、后台标签页恢复等场景）
 * 自动尝试 resume 兜底：页面发生过任意交互后 Chrome 即允许无手势 resume；
 * 彻底失败时输出控制台日志，不再静默吞掉
 */
function playDingDong() {
  const ctx = getAudioContext()
  if (!ctx) {
    console.warn('[orderNotify] WebAudio 不可用，跳过提示音')
    return
  }
  if (ctx.state === 'running') {
    playDingDongTones(ctx)
    return
  }
  ctx.resume()
    .then(() => {
      if (ctx.state === 'running') playDingDongTones(ctx)
      else console.warn('[orderNotify] 提示音被浏览器拦截：AudioContext 状态仍为', ctx.state, '（请在页面上点击一次后重试）')
    })
    .catch((err: unknown) => console.warn('[orderNotify] AudioContext.resume 失败：', err))
}

/** 叮（988Hz）+ 咚（659Hz）两连音 */
function playDingDongTones(ctx: AudioContext) {
  const now = ctx.currentTime
  playTone(ctx, 988, now, 0.18) // 叮
  playTone(ctx, 659, now + 0.18, 0.28) // 咚
}

/**
 * 浏览器系统通知（失焦兜底）
 * 仅在页面失焦（切到其他窗口/标签，语音听得到但页面变化看不到）且用户已授权时弹出，
 * 前台有语音与页面内提醒，不重复打扰；API 不存在或未授权时静默跳过
 * @param title 通知标题
 * @param body 通知正文
 * @param tag 去重标签（同 tag 迟到重推不叠弹窗）
 */
function showBrowserNotification(title: string, body: string, tag: string) {
  if (!document.hidden) return
  if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return
  const notification = new Notification(`${title}`, { body, tag })
  notification.onclick = () => {
    window.focus()
    notification.close()
  }
}

/**
 * 播报新订单语音通知（经多 tab 选主，只有 leader tab 出声）
 * 到店单按"到店收款"话术，其余按"新订单"话术，均含金额；
 * 页面失焦时追加浏览器系统通知兜底视觉通道
 * @param data 事件载荷
 */
function notifyOrderPaid(data: OrderPaidInfo) {
  runAsAnnouncer(`paid-${data.order_no}`, () => {
    playDingDong()
    const amount = Number((data.pay_amount ?? 0).toFixed(2))
    const text = data.delivery_type === 3
      ? `您有一笔到店收款，金额${amount}元`
      : `您有新订单，金额${amount}元，请及时处理`
    showBrowserNotification(data.delivery_type === 3 ? '到店收款' : '新订单',
      `订单 ${data.order_no} 已支付，请及时处理`, `order-paid-${data.order_no}`)
    speak(text)
  })
}

/**
 * 播报退款事件语音通知（经多 tab 选主，只有 leader tab 出声）
 * C 端申请（待审核）按"退款申请"话术提醒后台处理；退款成功按"订单退款"话术；
 * 页面失焦时追加浏览器系统通知兜底视觉通道
 * @param data 事件载荷
 */
function notifyOrderRefund(data: OrderRefundInfo) {
  runAsAnnouncer(`refund-${data.refund_no}`, () => {
    playDingDong()
    const amount = Number((data.amount ?? 0).toFixed(2))
    const isApply = data.action === 'apply'
    const text = isApply
      ? `您有一笔退款申请，金额${amount}元，请及时处理`
      : `订单已退款，金额${amount}元`
    showBrowserNotification(isApply ? '退款申请' : '订单退款',
      `订单 ${data.order_no} ${isApply ? '申请退款，请及时处理' : `已退款${amount}元`}`,
      `order-refund-${data.refund_no}`)
    speak(text)
  })
}

/**
 * 语音播报（Web Speech，zh-CN）
 * 先清队列：上一条语句异常未结束时新语句会被无限排队（表现为永远不出声）
 * @param text 播报文本
 */
function speak(text: string) {
  try {
    speechSynthesis.cancel()
    const utter = new SpeechSynthesisUtterance(text)
    utter.lang = 'zh-CN'
    utter.onerror = (e) => console.warn('[orderNotify] 语音播报失败：', e.error ?? e)
    lastUtterance = utter // 防 GC
    speechSynthesis.speak(utter)
  } catch (err) {
    console.warn('[orderNotify] 语音合成不可用：', err)
  }
}

/**
 * 注册订单播报订阅（幂等，由 App.vue 在建连前调用一次）
 * 订阅表挂在 realtime 层、与 socket 实例无关：服务端因权限变更踢线后自动重连，
 * 本层无需重新订阅
 * 使用示例：setupOrderNotify()
 */
export function setupOrderNotify() {
  if (installed) return
  installed = true
  // 发声跨 tab 选主（locks → channel 认领 → 不去重，三级降级）
  setupNotifyLeader()
  onRealtime('order.paid', (data: unknown) => {
    if (!data) return
    const info = data as OrderPaidInfo
    console.info('[orderNotify] 收到新订单事件：', info)
    notifyOrderPaid(info)
  })
  onRealtime('order.refund', (data: unknown) => {
    if (!data) return
    const info = data as OrderRefundInfo
    console.info('[orderNotify] 收到退款事件：', info)
    notifyOrderRefund(info)
  })
}

// 首次交互解锁音频（浏览器自动播放策略），常驻监听、内部幂等
if (typeof document !== 'undefined') {
  document.addEventListener('pointerdown', unlockAudio)
  document.addEventListener('keydown', unlockAudio)
}
