import { useAuthStore } from '@/stores/auth'

/**
 * 新订单 WebSocket 语音通知管理器
 *
 * 职责：
 * - 登录就绪且具备 order:view 权限、已选定社区时，与 gateway 建立 WS 长连接
 * - 断线自动重连（指数退避 2s 起步，封顶 60s；收到服务端 hello 后复位）
 * - 收到 order.paid 事件：播放提示音（WebAudio 合成，无需音频文件）+ 语音播报（Web Speech）
 * - 浏览器自动播放限制：首次用户交互时解锁 AudioContext 与语音合成
 *
 * 服务端消息协议（docs/需求文档/06-接口设计）：
 * - {type:'hello'} 连接建立欢迎
 * - {type:'order.paid', data:{order_no,community_id,pay_amount,delivery_type,source,status,pay_time}}
 * - {type:'order.refund', data:{order_no,community_id,refund_no,amount,delivery_type,source,action,operator_type,reason,event_time}}
 * 未知 type 静默忽略（前后端独立演进的前向兼容）
 */

/** 重连基础间隔（毫秒） */
const RECONNECT_BASE_DELAY = 2000
/** 重连最大间隔（毫秒） */
const RECONNECT_MAX_DELAY = 60000

/** 当前连接实例（单例） */
let socket: WebSocket | null = null
/** 重连定时器句柄 */
let reconnectTimer: ReturnType<typeof setTimeout> | null = null
/** 下次重连等待时长（指数退避） */
let retryDelay = RECONNECT_BASE_DELAY
/** 主动关闭标记：登出/社区切换重连时不触发自动重连 */
let intentionallyClosed = false

/** WebAudio 上下文（懒创建，用于合成提示音） */
let audioCtx: AudioContext | null = null
/** 语音合成是否已入队过手势内语句（Safari 放行后续异步 speak 的前提） */
let speechUnlocked = false
/** 持有当前播报语句的强引用：Chrome 有 utterance 被 GC 导致播到一半静音的已知问题 */
let lastUtterance: SpeechSynthesisUtterance | null = null

/**
 * 订单支付成功事件载荷（与后端 common/eventbus.OrderPaidEvent 对齐）
 */
interface OrderPaidInfo {
  /** 订单号 */
  order_no: string
  /** 订单归属社区 ID */
  community_id: number
  /** 实付金额（元） */
  pay_amount: number
  /** 配送方式 1自提 2配送 3到店 */
  delivery_type: number
  /** 订单来源 1C端用户 2收银台代客 */
  source: number
  /** 支付完成后的订单状态 */
  status: number
  /** 支付时间 */
  pay_time: string
}

/**
 * 订单退款事件载荷（与后端 common/eventbus.OrderRefundEvent 对齐）
 */
interface OrderRefundInfo {
  /** 订单号 */
  order_no: string
  /** 订单归属社区 ID */
  community_id: number
  /** 退款单号 */
  refund_no: string
  /** 本笔退款金额（元） */
  amount: number
  /** 配送方式 1自提 2配送 3到店 */
  delivery_type: number
  /** 订单来源 1C端用户 2收银台代客 */
  source: number
  /** 事件动作 apply=申请退款(待审核) completed=退款成功 */
  action: 'apply' | 'completed' | string
  /** 操作者类型 user/admin/system */
  operator_type: string
  /** 退款原因 */
  reason: string
  /** 事件时间 */
  event_time: string
}

/**
 * 拼接 WS 握手 URL
 * 浏览器 WebSocket 握手无法自定义请求头，鉴权凭登录时后端下发的 httpOnly
 * access cookie（随同源握手自动携带，令牌不落 URL 与访问日志），
 * 由网关 WSAuthMiddleware 映射到标准请求头后走既有 admin 鉴权链；
 * community_id 非敏感，仍经查询参数传递（声明推送过滤维度）
 * @param communityId 当前社区 ID
 * @returns 完整 ws/wss URL
 */
function buildWsUrl(communityId: number): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const params = new URLSearchParams({ community_id: String(communityId) })
  return `${proto}//${location.host}/api/v1/admin/ws?${params.toString()}`
}

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
 * - 浏览器通知授权：与语音解锁独立推进——若首次交互发生在登录前（WS 尚未连接，
 *   申请不到权限框），后续每次交互都会补申请，避免授权时机死锁；
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
  if (socket && typeof Notification !== 'undefined' && Notification.permission === 'default') {
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
 * 播报新订单语音通知
 * 到店单按"到店收款"话术，其余按"新订单"话术，均含金额；
 * 页面失焦时追加浏览器系统通知兜底视觉通道
 * @param data 事件载荷
 */
function notifyOrderPaid(data: OrderPaidInfo) {
  playDingDong()
  const amount = Number((data.pay_amount ?? 0).toFixed(2))
  const text = data.delivery_type === 3
    ? `您有一笔到店收款，金额${amount}元`
    : `您有新订单，金额${amount}元，请及时处理`
  showBrowserNotification(data.delivery_type === 3 ? '到店收款' : '新订单',
    `订单 ${data.order_no} 已支付，请及时处理`, `order-paid-${data.order_no}`)
  speak(text)
}

/**
 * 播报退款事件语音通知
 * C 端申请（待审核）按"退款申请"话术提醒后台处理；退款成功按"订单退款"话术；
 * 页面失焦时追加浏览器系统通知兜底视觉通道
 * @param data 事件载荷
 */
function notifyOrderRefund(data: OrderRefundInfo) {
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
 * 处理服务端下发的 WS 消息
 * @param event 消息事件
 */
function onMessage(event: MessageEvent) {
  let msg: { type?: string; data?: OrderPaidInfo & OrderRefundInfo }
  try {
    msg = JSON.parse(String(event.data))
  } catch {
    return
  }
  switch (msg.type) {
    case 'hello':
      // 连接确认：复位退避间隔
      retryDelay = RECONNECT_BASE_DELAY
      break
    case 'order.paid':
      if (msg.data) {
        console.info('[orderNotify] 收到新订单事件：', msg.data)
        notifyOrderPaid(msg.data)
      }
      break
    case 'order.refund':
      if (msg.data) {
        console.info('[orderNotify] 收到退款事件：', msg.data)
        notifyOrderRefund(msg.data)
      }
      break
    default:
      break
  }
}

/**
 * 调度下一次重连（指数退避）
 */
function scheduleReconnect() {
  reconnectTimer = setTimeout(() => connectOrderNotify(), retryDelay)
  retryDelay = Math.min(retryDelay * 2, RECONNECT_MAX_DELAY)
}

/**
 * 建立新订单通知 WS 连接（幂等）
 * 未登录 / 未选定社区 / 无 order:view 权限时直接跳过；
 * 已有存活的连接时不再重复创建
 */
export function connectOrderNotify() {
  if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) {
    return
  }
  const auth = useAuthStore()
  if (!auth.isAuthenticated || auth.currentCommunityId <= 0 || !auth.hasPermission('order:view')) {
    return
  }
  intentionallyClosed = false
  if (reconnectTimer) {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
  const sock = new WebSocket(buildWsUrl(auth.currentCommunityId))
  socket = sock
  sock.onopen = () => {
    retryDelay = RECONNECT_BASE_DELAY
  }
  sock.onmessage = onMessage
  // onerror 后浏览器必然触发 onclose，重连统一收口在 onclose
  sock.onerror = () => sock.close()
  sock.onclose = () => {
    // 已被新连接替换（如社区切换）时静默退出，避免旧连接触发多余重连
    if (socket !== sock) return
    socket = null
    if (!intentionallyClosed) scheduleReconnect()
  }
}

/**
 * 断开新订单通知 WS 连接（登出、权限丧失、社区切换时调用）
 */
export function disconnectOrderNotify() {
  intentionallyClosed = true
  if (reconnectTimer) {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
  if (socket) {
    socket.close()
    socket = null
  }
}

// 首次交互解锁音频（浏览器自动播放策略），常驻监听、内部幂等
if (typeof document !== 'undefined') {
  document.addEventListener('pointerdown', unlockAudio)
  document.addEventListener('keydown', unlockAudio)
}
