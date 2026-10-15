<template>
  <div class="pos-layout">
    <!-- 顶部栏：应用标识 + 班次状态 + 操作员操作 -->
    <div class="pos-header">
      <div class="header-left">
        <n-icon :size="22" class="brand-icon"><CashOutline /></n-icon>
        <span class="brand-name">收银台</span>
        <span class="community-name">{{ currentCommunityName }}</span>
      </div>
      <div class="header-right">
        <!-- 班次状态：无班次提示未开工，进行中显示开班时间 -->
        <n-tag v-if="posStore.currentShift" type="success" size="small" :bordered="false">
          班次进行中 · {{ formatTime(posStore.currentShift.start_time) }} 开班
        </n-tag>
        <n-tag v-else type="warning" size="small" :bordered="false">未开工</n-tag>
        <n-dropdown :options="userOptions" @select="onUserAction">
          <n-button quaternary size="small">
            <n-icon size="18" class="user-icon"><PersonCircleOutline /></n-icon>
            <span class="operator-name">{{ operatorName }}</span>
            <n-icon size="14"><ChevronDownOutline /></n-icon>
          </n-button>
        </n-dropdown>
      </div>
    </div>

    <!-- 子页面：收银工作台 / 交班记录 -->
    <div class="pos-body">
      <router-view />
    </div>

    <!-- 交班确认弹窗：左侧班次信息 + 右侧收款/退款统计 + 现金合计 -->
    <n-modal
      v-model:show="showEndShiftModal"
      preset="card"
      title="确认交班"
      style="width: 640px; max-width: 94vw"
      :mask-closable="false"
    >
      <n-spin :show="endingShift">
        <div class="shift-confirm">
          <div class="shift-info">
            <div class="info-title">班次信息</div>
            <div class="info-row"><span class="label">收银员</span><span>{{ operatorName }}</span></div>
            <div class="info-row"><span class="label">上班时间</span><span>{{ formatTime(shiftForConfirm?.start_time) }}</span></div>
            <div class="info-row"><span class="label">交班时间</span><span>{{ nowTimeText }}</span></div>
            <div class="info-row"><span class="label">工作时长</span><span>{{ workDurationText }}</span></div>
          </div>
          <div class="shift-stats">
            <div class="stats-block">
              <div class="info-title">收款</div>
              <!-- 固定展示 现金/微信/支付宝/余额，渠道未接入的支付方式数额为 0 -->
              <div v-for="item in incomeRows" :key="'in' + item.pay_type" class="stat-row" :class="{ muted: item.amount === 0 }">
                <span>{{ payTypeText(item.pay_type) }}</span>
                <span class="amount">￥{{ item.amount.toFixed(2) }}（{{ item.order_count }} 单）</span>
              </div>
            </div>
            <div class="stats-block">
              <div class="info-title">退款</div>
              <div v-for="item in refundRows" :key="'rf' + item.pay_type" class="stat-row" :class="{ muted: item.amount === 0 }">
                <span>{{ payTypeText(item.pay_type) }}</span>
                <span class="amount refund">-￥{{ item.amount.toFixed(2) }}（{{ item.order_count }} 单）</span>
              </div>
            </div>
          </div>
        </div>
        <div class="cash-total">
          <span>现金合计（收款 - 退款，交班实缴参考）</span>
          <span class="amount">￥{{ cashTotalText }}</span>
        </div>
        <n-input v-model:value="endNote" type="textarea" placeholder="交班备注（选填）" :rows="2" />
      </n-spin>
      <template #footer>
        <n-space justify="space-between" align="center">
          <!-- 交班记录入口：收银员查历史班次从此进入，不再单设菜单 -->
          <n-button quaternary size="small" @click="goShiftRecords">历史交班记录</n-button>
          <n-space>
            <n-button :disabled="endingShift" @click="showEndShiftModal = false">取消</n-button>
            <n-button type="primary" :loading="endingShift" @click="confirmEndShift">确认交班</n-button>
          </n-space>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import { CashOutline, PersonCircleOutline, ChevronDownOutline } from '@vicons/ionicons5'
import { useAuthStore } from '@/stores/auth'
import { usePosStore } from '@/stores/pos'
import { resolveHomePath } from '@/router'
import { endPosShift } from '@/api/order'
import { payStatRows, payTypeText } from '@/constants/order'
import { getCommunityList } from '@/api/user'
import type { PosShiftStatistics } from '@/api/types'

const router = useRouter()
const message = useMessage()
const authStore = useAuthStore()
const posStore = usePosStore()

/** 交班弹窗展示状态 */
const showEndShiftModal = ref(false)
/** 交班请求进行中 */
const endingShift = ref(false)
/** 交班备注 */
const endNote = ref('')

/** 操作员显示名 */
const operatorName = computed(() => authStore.admin?.nickname || authStore.admin?.username || '收银员')

/** 交班弹窗展示的班次与统计（进行中班次的实时统计由后端计算） */
const shiftForConfirm = computed(() => posStore.currentShift)
const stats = computed<Partial<PosShiftStatistics>>(() => posStore.currentShift?.statistics ?? {})

/** 收款固定四行（现金/微信/支付宝/余额，缺省补 0） */
const incomeRows = computed(() => payStatRows(stats.value.income))
/** 退款固定四行（现金/微信/支付宝/余额，缺省补 0） */
const refundRows = computed(() => payStatRows(stats.value.refund))

/**
 * 现金合计文本（收款-退款，后端已按支付方式折算）
 */
const cashTotalText = computed(() => (stats.value.cash_total ?? 0).toFixed(2))

/** 交班弹窗打开时的当前时间文本（交班时间展示） */
const nowTimeText = ref('')

/**
 * 工作时长文本（开班时间至当前时间）
 */
const workDurationText = computed(() => {
  if (!shiftForConfirm.value) return '-'
  const start = parseDate(shiftForConfirm.value.start_time).getTime()
  if (!Number.isFinite(start)) return '-'
  const minutes = Math.max(0, Math.floor((Date.now() - start) / 60000))
  const hours = Math.floor(minutes / 60)
  if (hours <= 0) return `${minutes} 分钟`
  return `${hours} 小时 ${minutes % 60} 分钟`
})

/** 操作员下拉选项：交班记录入口在交班弹窗内（不放独立菜单），仅在有可用页面时展示其它项 */
const userOptions = computed(() => {
  const options: Array<{ label: string; key: string }> = []
  if (posStore.currentShift) {
    options.push({ label: '交班', key: 'endShift' })
  }
  if (resolveHomePath(authStore)) {
    options.push({ label: '进入管理系统', key: 'admin' })
  }
  options.push({ label: '退出登录', key: 'logout' })
  return options
})

/** 社区 ID → 名称映射（无 community:view 权限时降级显示"社区 #id"） */
const communityNameMap = ref<Map<number, string>>(new Map())

/** 当前社区显示名 */
const currentCommunityName = computed(() => {
  const id = authStore.currentCommunityId
  if (id <= 0) return '未选择社区'
  return communityNameMap.value.get(id) ?? `社区 #${id}`
})

/**
 * 加载社区名称映射（失败静默降级，与主系统 Layout 行为一致）
 */
async function loadCommunityNames() {
  if (!authStore.hasPermission('community:view')) return
  try {
    const res = await getCommunityList({ page: 1, page_size: 200 })
    const map = new Map<number, string>()
    for (const item of res.data.data.list) {
      map.set(item.id, item.name)
    }
    communityNameMap.value = map
  } catch {
    // 静默降级
  }
}

/**
 * 解析后端时间字符串为 Date
 * 兼容 "YYYY-MM-DD HH:mm:ss"（下单路径返回）与 RFC3339（DB 读出）两种格式
 * @param time 后端时间字符串
 * @returns Date 对象
 */
function parseDate(time: string): Date {
  return new Date(time.includes('T') ? time : time.replace(/-/g, '/'))
}

/**
 * 时间文本格式化（HH:mm）
 * @param time 后端时间字符串
 * @returns HH:mm 格式，解析失败返回原文本
 */
function formatTime(time?: string): string {
  if (!time) return '-'
  const date = parseDate(time)
  if (!Number.isFinite(date.getTime())) return time
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(date.getHours())}:${pad(date.getMinutes())}`
}

/**
 * 操作员下拉动作分发
 * @param key 动作标识
 */
function onUserAction(key: string) {
  if (key === 'endShift') {
    openEndShiftModal()
    return
  }
  if (key === 'admin') {
    router.push(resolveHomePath(authStore) ?? '/dashboard')
    return
  }
  if (key === 'logout') {
    // 退出登录统一落收银台独立登录页，不回主系统
    authStore.logout()
    posStore.clearShift()
    router.push('/pos/login')
  }
}

/**
 * 跳转历史交班记录页（交班弹窗内入口）
 */
function goShiftRecords() {
  showEndShiftModal.value = false
  router.push('/pos/shifts')
}

/**
 * 打开交班确认弹窗：刷新班次统计后展示
 */
async function openEndShiftModal() {
  const now = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  nowTimeText.value = `${pad(now.getHours())}:${pad(now.getMinutes())}`
  endNote.value = ''
  await posStore.loadCurrentShift(true)
  if (!posStore.currentShift) {
    message.warning('当前没有进行中的班次')
    return
  }
  showEndShiftModal.value = true
}

/**
 * 确认交班：结束后清空班次状态并强制登出，落到收银台独立登录页
 * （交班即换人收银，必须重新登录下一个操作员账号）
 */
async function confirmEndShift() {
  endingShift.value = true
  try {
    await endPosShift(endNote.value.trim() || undefined)
    message.success('交班成功，辛苦了')
    showEndShiftModal.value = false
    posStore.clearShift()
    // 交班即退出登录：清除 token，下一位收银员从登录页进入
    authStore.logout()
    router.push('/pos/login')
  } catch (error: unknown) {
    message.error(error instanceof Error ? error.message : '交班失败，请重试')
  } finally {
    endingShift.value = false
  }
}

onMounted(() => {
  posStore.loadCurrentShift(true)
  loadCommunityNames()
})
</script>

<style lang="scss" scoped>
.pos-layout {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: #f5f6f8;
}
.pos-header {
  height: 52px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
  background: #fff;
  border-bottom: 1px solid #eee;
}
.header-left,
.header-right {
  display: flex;
  align-items: center;
  gap: 10px;
}
.brand-icon {
  color: #07c160;
}
.brand-name {
  font-size: 16px;
  font-weight: 700;
}
.community-name {
  font-size: 13px;
  color: #666;
  padding: 2px 10px;
  background: #f5f5f5;
  border-radius: 4px;
}
.user-icon {
  color: #07c160;
}
.operator-name {
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pos-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
}
// --- 交班确认弹窗 ---
.shift-confirm {
  display: flex;
  gap: 20px;
}
.shift-info,
.shift-stats {
  flex: 1;
  min-width: 0;
}
.info-title {
  font-size: 13px;
  font-weight: 600;
  color: #333;
  margin-bottom: 10px;
}
.info-row {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  padding: 4px 0;
  .label {
    color: #999;
  }
}
.stats-block {
  margin-bottom: 14px;
}
.stat-row {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  padding: 4px 0;
  // 数额为 0 的支付方式（渠道未接入）弱化展示
  &.muted {
    color: #bbb;
  }
  .amount {
    font-weight: 600;
    &.refund {
      color: #d03050;
    }
  }
}
.cash-total {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin: 16px 0;
  padding: 12px 16px;
  background: #f0faf4;
  border-radius: 8px;
  font-size: 13px;
  .amount {
    font-size: 20px;
    font-weight: 700;
    color: #07c160;
  }
}
</style>
