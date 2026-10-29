<template>
  <view v-if="ball" class="fly-track" :style="trackStyle">
    <view class="fly-ball" :style="ballStyle" />
  </view>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { type FlyPoint } from '@/utils/flyCart'

/**
 * 飞球动画组件：从起点到落点播放一颗抛物线小球（加入购物车反馈）
 *
 * 使用示例（页面模板中放置 <fly-to-cart ref="flyRef" />）：
 *   const [from, to] = await Promise.all([getFlyTarget('.action-btn'), getFlyTarget('.cart-entry')])
 *   await flyRef.value?.fly(from, to)
 *
 * 实现说明：外层 track 负责 X/Y 线性匀速位移（就位起点 → 终点），内层小球负责
 * 「上抛(ease-out) → 下落(ease-in) 回到轨道原点」，两者叠加形成抛物线轨迹，
 * 拱顶位于行程中点上方 UP_HEIGHT 处；终态 track 在落点、小球回到原点，位置精确对齐落点中心。
 * 全程 fixed 视口定位，仅用 px 坐标，H5/微信/支付宝/App 行为一致。
 */

/** 单颗飞行小球的运动状态：ready 就位起点 → up 上升 → down 下落；id 用于连续加购时隔离新旧球的定时器 */
interface FlyBall {
  id: number
  from: FlyPoint
  to: FlyPoint
  phase: 'ready' | 'up' | 'down'
}

/** 上抛高度（px），高于落点形成抛物线拱顶 */
const UP_HEIGHT = 80
/** 水平飞行总时长（ms），与 Y 方向两段（各半）同步结束 */
const FLY_DURATION = 500
const HALF_DURATION = FLY_DURATION / 2

const ball = ref<FlyBall | null>(null)
/** 自增球序号：快速连续加购时新调用覆盖旧球，旧定时器按 id 失效，不误清新球 */
let flySeq = 0

/** 外层轨道样式：X 方向位移，ready 帧无过渡（先就位），起飞后线性匀速 */
const trackStyle = computed(() => {
  if (!ball.value) return {}
  const { from, to, phase } = ball.value
  const moving = phase !== 'ready'
  return {
    transform: `translate(${moving ? to.x : from.x}px, ${moving ? to.y : from.y}px)`,
    transition: moving ? `transform ${FLY_DURATION}ms linear` : 'none',
  }
})

/** 内层小球样式：Y 方向就位/上抛/下落三态；下落段回到轨道原点（Y 终态即落点，不叠加净位移） */
const ballStyle = computed(() => {
  if (!ball.value) return {}
  const { phase } = ball.value
  if (phase === 'ready') {
    return { transform: 'translateY(0px)', transition: 'none' }
  }
  if (phase === 'up') {
    return {
      transform: `translateY(-${UP_HEIGHT}px)`,
      transition: `transform ${HALF_DURATION}ms ease-out`,
    }
  }
  // 下落段：track 已同步线性行进至终点，小球从拱顶 ease-in 落回轨道原点
  return {
    transform: 'translateY(0px)',
    transition: `transform ${HALF_DURATION}ms cubic-bezier(0.55, 0, 1, 0.45)`,
  }
})

/**
 * 播放一次抛物线飞球动画
 * ready 帧先渲染就位（无过渡），随后切 up 触发两段位移；用定时器兜底清理，
 * 不依赖 transitionend（小程序端节点销毁时机不稳定）
 * @param from 起点视口坐标（px）
 * @param to 落点视口坐标（px）
 * @returns 动画播放完成、小球移除后 resolve；组件重复调用时以最后一次为准
 */
function fly(from: FlyPoint, to: FlyPoint): Promise<void> {
  return new Promise((resolve) => {
    const id = ++flySeq
    ball.value = { id, from, to, phase: 'ready' }
    const isCurrent = () => ball.value?.id === id
    nextTick(() => {
      // 等 ready 帧渲染完成（小程序端 setData 异步）后再起飞，避免过渡被跳过
      setTimeout(() => {
        if (!isCurrent()) {
          resolve()
          return
        }
        ball.value!.phase = 'up'
        setTimeout(() => {
          if (isCurrent()) ball.value!.phase = 'down'
        }, HALF_DURATION)
        setTimeout(() => {
          if (isCurrent()) ball.value = null
          resolve()
        }, FLY_DURATION + 20)
      }, 30)
    })
  })
}

defineExpose({ fly })
</script>

<style scoped>
.fly-track {
  position: fixed;
  left: 0;
  top: 0;
  /* 盖过 uview 弹窗（z-index 10075）：规格弹窗内确认加购时飞球不被遮挡 */
  z-index: 10099;
  pointer-events: none;
}

.fly-ball {
  width: 24rpx;
  height: 24rpx;
  /* 负 margin 让 track 的 translate 坐标对准球心（与触点/落点中心一致，而非球的左上角） */
  margin-left: -12rpx;
  margin-top: -12rpx;
  border-radius: 50%;
  background-color: #07c160;
  box-shadow: 0 2rpx 8rpx rgba(7, 193, 96, 0.4);
}
</style>
