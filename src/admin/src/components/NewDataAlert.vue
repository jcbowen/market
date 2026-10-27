<template>
  <div
    v-if="count > 0"
    class="new-data-alert"
    role="button"
    tabindex="0"
    @click="onConfirm"
    @keydown.enter="onConfirm"
  >
    有 {{ count }} 条新数据，点击刷新
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onRealtime } from '@/utils/realtime'

/**
 * 「有新数据」提示条
 * 只累计计数、不自动重载列表——订单/售后列表可筛选、翻页、行内操作，
 * 自动重排会打断正在处理的行；由管理员点击提示条才触发刷新。
 * 归零时机由宿主数据的「最近加载时刻」（loadedAt 变化）决定，而不是页面激活时机：
 * keep-alive 切回时宿主未必重新拉数（如列表页仅在筛选条件变化时重载），
 * 按激活归零会把「屏幕上确实缺这几条」的真实提示抹掉。
 *
 * @param types 订阅的实时事件类型，任一命中即计数 +1（如 ['order.paid', 'order.refund']）
 * @param loadedAt 宿主列表最近一次加载完成的时间戳；变化即清零计数（隐藏提示条）
 * @event refresh 点击提示条后派发，宿主页面在此调用自己的 loadData
 * @示例 <NewDataAlert :types="['order.paid']" :loaded-at="lastLoadedAt" @refresh="loadData" />
 */
const props = defineProps<{ types: string[]; loadedAt: number }>()
const emit = defineEmits<{ (e: 'refresh'): void }>()

/** 自上次列表加载以来累计到的新数据条数，归零即隐藏 */
const count = ref(0)
/** 事件订阅的退订集合 */
let disposeRealtime: Array<() => void> = []

watch(
  () => props.loadedAt,
  () => {
    count.value = 0
  },
)

/** 收到一个事件信号：计数 +1（不发请求，失活期间照常累计） */
function onSignal() {
  count.value += 1
}

/** 点击提示条：先归零（立即隐藏）再通知宿主刷新 */
function onConfirm() {
  count.value = 0
  emit('refresh')
}

onMounted(() => {
  disposeRealtime = props.types.map((type) => onRealtime(type, onSignal))
})

onBeforeUnmount(() => {
  disposeRealtime.forEach((dispose) => dispose())
  disposeRealtime = []
})
</script>

<style lang="scss" scoped>
.new-data-alert {
  padding: 8px 12px;
  border-radius: 6px;
  background: rgba(7, 193, 96, 0.08);
  border: 1px solid rgba(7, 193, 96, 0.3);
  color: #07c160;
  font-size: 13px;
  text-align: center;
  cursor: pointer;
  user-select: none;

  &:hover {
    background: rgba(7, 193, 96, 0.14);
  }

  &:focus-visible {
    outline: 2px solid rgba(7, 193, 96, 0.5);
    outline-offset: 1px;
  }
}
</style>
