import { useAuthStore } from '@/stores/auth'

/**
 * 后台 WebSocket 实时通道（连接层 + 事件分发层）
 *
 * 职责边界：
 * - 与 gateway 建/断长连接、断线重连（指数退避）、重连前的 token 续期探测、
 *   换社区自动断旧重连（握手时声明的 community_id 决定推送范围）
 * - 解析服务端消息并按 type 分发给订阅者（`onRealtime`），供播报、工作台、
 *   下载中心、收银台、列表页等各消费方订阅，避免每个页面各连一条 WS
 * - 维护登录态与会话本身：`admin.permission_changed` 到达后防抖刷新权限快照、
 *   页面切回前台的兜底刷新（属会话维护，不是业务消费，故留在本层）
 *
 * 不设权限码门槛（2026-10-27 定案）：登录且已选定社区即建连。权限被收窄的管理员
 * 恰恰需要收到权限变更提醒以刷新本地快照；数据类事件由服务端在推送时刻按握手权限
 * 快照过滤（gateway/ws/hub.go 的 msgRequiredPerm），前端判定不构成安全边界。
 *
 * 与播报层的关系：`orderNotify.ts` 是本层的一个订阅者。本层不感知音频/语音/浏览器通知。
 *
 * 服务端消息协议（docs/需求文档/06-接口设计/03-后台管理接口.md）：
 * - {type:'hello'} 连接建立欢迎
 * - {type:'order.paid', data: OrderPaidInfo}
 * - {type:'order.refund', data: OrderRefundInfo}
 * - {type:'export.task_changed', data: ExportTaskChangedInfo}
 * - {type:'payment.anomaly_detected', data: PaymentAnomalyInfo}
 * - {type:'admin.permission_changed', data: {admin_ids?,community_ids?,global?,reason?}}
 * 另有本层自造的本地类型 {type:'connection', data:{connected}}（连上/断开时发出），
 * 服务端不会下发该类型。
 * 未知 type 静默忽略（前后端独立演进的前向兼容）
 */

/** 重连基础间隔（毫秒） */
const RECONNECT_BASE_DELAY = 2000
/** 重连最大间隔（毫秒） */
const RECONNECT_MAX_DELAY = 60000
/** 两次 token 续期探测的最小间隔（毫秒），重连风暴期间节流 */
const REFRESH_PROBE_INTERVAL = 30000
/** 权限快照刷新防抖等待（毫秒）：合并窗口内多条事件只拉取一次 */
const PERMISSION_REFRESH_DEBOUNCE = 1500
/** 页面切回前台时的权限兜底刷新最小间隔（毫秒），覆盖 WS 丢消息场景 */
const VISIBILITY_REFRESH_INTERVAL = 10000

/**
 * 订单支付成功事件载荷（与后端 common/eventbus.OrderPaidEvent 对齐）
 */
export interface OrderPaidInfo {
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
export interface OrderRefundInfo {
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

/** 权限变更事件载荷（与后端 common/eventbus.AdminPermissionChangedEvent 对齐） */
export interface PermissionChangedInfo {
  /** 直接受影响的管理员 ID（非空时服务端按此定向下发） */
  admin_ids?: number[]
  /** 受影响的社区 ID */
  community_ids?: number[]
  /** 全局角色变更：全量下发 */
  global?: boolean
  /** 变更原因（服务端日志用，前端不展示） */
  reason?: string
}

/** 导出任务终态事件载荷（与后端 common/eventbus.ExportTaskEvent 对齐）
 * 只带 ID 与计数，不含文件路径：下载仍走带归属校验的接口 */
export interface ExportTaskChangedInfo {
  /** 任务 ID */
  task_id: number
  /** 任务创建人管理员 ID（服务端按此定向下发） */
  admin_id: number
  /** 任务归属社区 ID（0=超管跨社区） */
  community_id: number
  /** 导出类型，当前仅 orders */
  export_type: string
  /** 事件状态 done=文件已生成 failed=生成失败 */
  state: 'done' | 'failed' | string
  /** 下载文件名（完成态） */
  file_name?: string
  /** 导出数据行数（完成态） */
  row_count: number
  /** 失败原因；行数上限截断时为「部分导出」提示语 */
  error_msg?: string
  /** 事件时间 */
  event_time: string
}

/**
 * 支付异常/挂账人工介入告警事件载荷（与后端 common/eventbus.PaymentAnomalyEvent 对齐）
 * 只带定位凭据与金额，排查走带权限的异常队列接口；kind: 3 系统检出挂账 8 人工核实挂账 6 放弃对账
 */
export interface PaymentAnomalyInfo {
  /** 流水类别（后端 model.NotifyKind*） */
  kind: number
  /** 订单号快照（解析不出订单时可能为空） */
  order_no: string
  /** 支付单号 */
  payment_no: string
  /** 归属社区 ID（0=未知，仅超管全量队列可见） */
  community_id: number
  /** 支付单金额（元），支付单查不到时为 0 */
  amount: number
  /** 事件时间 */
  event_time: string
}

/** 事件处理函数 */
type EventHandler = (data: any) => void

/** 当前连接实例（单例） */
let socket: WebSocket | null = null
/** 当前连接握手时声明的社区 ID（0=未连接）：换社区必须重连，推送范围在握手时刻定格 */
let connectedCommunityId = 0
/** 重连定时器句柄 */
let reconnectTimer: ReturnType<typeof setTimeout> | null = null
/** 下次重连等待时长（指数退避） */
let retryDelay = RECONNECT_BASE_DELAY
/** 主动关闭标记：登出/社区切换重连时不触发自动重连 */
let intentionallyClosed = false
/** 订阅表：事件 type → 处理函数集合 */
const handlers = new Map<string, Set<EventHandler>>()
/** 权限快照刷新定时器句柄 */
let permissionRefreshTimer: ReturnType<typeof setTimeout> | null = null
/** 上次 token 续期探测时间戳 */
let lastRefreshProbeAt = 0
/** 上次切回前台刷新时间戳 */
let lastVisibilityRefreshAt = 0

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
 * 订阅指定类型的实时事件
 * 订阅表与 socket 实例解耦：服务端因权限变更主动踢线（KickAdmins）后本层会自动重连，
 * 重连期间与重连之后的订阅关系都保持不变，页面无需重新订阅
 * @param type 事件类型（如 'order.paid'），未知 type 由服务端保证不下发业务数据；
 *        额外的本地类型 'connection'（载荷 {connected:boolean}）由本层在连接建立/断开时发出，
 *        供消费方把自适应轮询之类的策略做成响应式状态（服务端不会下发该类型）
 * @param handler 处理函数，接收事件载荷 data；抛出的异常被本层捕获，不影响其他订阅者
 * @returns 退订函数（页面 onUnmounted 时调用，避免 keep-alive 缓存下处理器堆积）
 * @使用示例 onRealtime('order.paid', (data) => reloadTodos())
 */
export function onRealtime(type: string, handler: EventHandler): () => void {
  let set = handlers.get(type)
  if (!set) {
    set = new Set()
    handlers.set(type, set)
  }
  set.add(handler)
  return () => {
    set?.delete(handler)
  }
}

/**
 * 当前实时通道是否已连通
 * 供消费方决定轮询频率（WS 在时降频、断开时回到高频兜底）
 * @returns 已连通返回 true
 */
export function isRealtimeConnected(): boolean {
  return socket !== null && socket.readyState === WebSocket.OPEN
}

/**
 * 向订阅者分发事件；单个处理函数异常只记日志，不中断其余分发
 * @param type 事件类型
 * @param data 事件载荷
 */
function emit(type: string, data: unknown) {
  const set = handlers.get(type)
  if (!set || set.size === 0) return
  for (const handler of [...set]) {
    try {
      handler(data)
    } catch (err) {
      console.warn(`[realtime] 事件处理函数异常 type=${type}：`, err)
    }
  }
}

/**
 * 防抖调度权限快照刷新：拉取 /admin/info 按当前社区语境实时重算，
 * 菜单与路由守卫基于 store computed，刷新即生效，无需 reload
 * 真防抖（每次事件都重置窗口）：批量调整会连发多条事件，若只认首个定时器，
 * 窗口内的后续事件会被吞掉且不再触发，恰好在「连续改多个角色」时漏刷
 */
function schedulePermissionRefresh() {
  if (permissionRefreshTimer) clearTimeout(permissionRefreshTimer)
  permissionRefreshTimer = setTimeout(() => {
    permissionRefreshTimer = null
    const auth = useAuthStore()
    if (!auth.isAuthenticated) return
    void auth.fetchAdminInfo().catch(() => {})
  }, PERMISSION_REFRESH_DEBOUNCE)
}

/**
 * 处理服务端下发的 WS 消息：连接类消息在本层消化，业务事件转交订阅者
 * @param event 消息事件
 */
function onMessage(event: MessageEvent) {
  let msg: { type?: string; data?: unknown }
  try {
    msg = JSON.parse(String(event.data))
  } catch {
    return
  }
  switch (msg.type) {
    case 'hello':
      // 连接确认：复位退避间隔
      retryDelay = RECONNECT_BASE_DELAY
      return
    case 'admin.permission_changed':
      // 权限变更属会话维护，在本层直接消化（服务端送达后会踢线，重连即按新快照握手）
      schedulePermissionRefresh()
      emit(msg.type, msg.data)
      return
    default:
      // 未知 type 静默忽略：前向兼容，无订阅者时不做任何处理
      if (msg.type) emit(msg.type, msg.data)
  }
}

/**
 * 调度下一次重连（指数退避）
 */
function scheduleReconnect() {
  reconnectTimer = setTimeout(() => connectRealtime(), retryDelay)
  retryDelay = Math.min(retryDelay * 2, RECONNECT_MAX_DELAY)
}

/**
 * WS 断连后的 token 续期探测（尽力而为）
 * WS 握手凭 cookie 里的 access_token 鉴权，过期后若仅靠裸重连会持续失败，
 * 直到其他 HTTP 请求恰好触发刷新——空闲后台页正是纯 WS 流量，会一直收不到通知。
 * 此处在重连前主动调 refresh 接口续期；30s 节流避免重连风暴期反复请求。
 * 续期失败（refresh 也过期/账号被停用/网关不可达）返回 false，调用方应停止重连，
 * 登录态收口交给 HTTP 请求拦截器（用户下次操作时统一跳转登录页）
 * @returns 是否可继续重连
 */
async function probeTokenRefresh(): Promise<boolean> {
  const now = Date.now()
  if (now - lastRefreshProbeAt < REFRESH_PROBE_INTERVAL) return true
  lastRefreshProbeAt = now
  const ok = await useAuthStore().refreshAccessToken()
  if (!ok) console.warn('[realtime] 登录态续期失败，停止 WS 重连（等待页面重新登录）')
  return ok
}

/**
 * 建立实时通道连接（幂等，并自行处理换社区）
 * 未登录 / 未选定社区时直接跳过（不设权限门槛，登录即可接入）；
 * 同一社区的存活连接不重复创建（重连调度器也走本函数，重复调用无害）；
 * 社区变了则先断旧连接再按新社区重连——推送范围由握手时的 community_id 决定，
 * 只换 HTTP 请求头不换连接会让页面收到旧社区的推送（例如从角色页「到该社区管理」切换语境）
 */
export function connectRealtime() {
  const auth = useAuthStore()
  if (!auth.isAuthenticated || auth.currentCommunityId <= 0) {
    return
  }
  const communityId = auth.currentCommunityId
  if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) {
    if (connectedCommunityId === communityId) return
    // 换社区：直接换新实例即可——旧连接的 onclose 会因 socket 已被替换而静默退出，
    // 既不会按旧社区重连，也不会向订阅方误报 connection:false
    socket.close()
    socket = null
  }
  intentionallyClosed = false
  if (reconnectTimer) {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
  const sock = new WebSocket(buildWsUrl(communityId))
  connectedCommunityId = communityId
  socket = sock
  sock.onopen = () => {
    retryDelay = RECONNECT_BASE_DELAY
    emit('connection', { connected: true })
  }
  sock.onmessage = onMessage
  // onerror 后浏览器必然触发 onclose，重连统一收口在 onclose
  sock.onerror = () => sock.close()
  sock.onclose = () => {
    // 已被新连接替换（如社区切换）时静默退出，避免旧连接触发多余重连
    if (socket !== sock) return
    socket = null
    emit('connection', { connected: false })
    if (intentionallyClosed) return
    // 重连前先探测续期：握手凭 cookie 里的 access_token，过期后裸重连只会持续失败；
    // 探测失败（refresh 已失效）时不再排重连，登录态由 HTTP 拦截器统一收口
    void probeTokenRefresh().then((ok) => {
      if (ok && !intentionallyClosed) scheduleReconnect()
    })
  }
}

/**
 * 断开实时通道连接（登出、切换社区前的显式断开）
 * 社区切换的自动换连由 connectRealtime 内部完成，本函数供显式收口场景使用
 */
export function disconnectRealtime() {
  intentionallyClosed = true
  if (reconnectTimer) {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
  if (socket) {
    socket.close()
    socket = null
  }
  connectedCommunityId = 0
  // 手动断开不会触发本连接的 onclose（已被 socket !== sock 静默收口），
  // 这里补发一次状态，否则按 WS 状态自适应的消费方（如下载中心轮询间隔）会停留在「在线」
  emit('connection', { connected: false })
}

// 页面从后台切回前台时限流兜底刷新一次权限快照：
// WS 通知是尽力而为（Redis 断连/发送缓冲满会丢消息且不补偿），
// 空闲后台页恰好是纯 WS 流量，兜底刷新保证滞后最终收敛
if (typeof document !== 'undefined') {
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState !== 'visible') return
    const auth = useAuthStore()
    if (!auth.isAuthenticated) return
    const now = Date.now()
    if (now - lastVisibilityRefreshAt < VISIBILITY_REFRESH_INTERVAL) return
    lastVisibilityRefreshAt = now
    void auth.fetchAdminInfo().catch(() => {})
  })
}
