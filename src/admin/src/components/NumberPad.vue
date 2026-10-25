<template>
  <div class="number-pad">
    <button
      v-for="key in keys"
      :key="key.value"
      type="button"
      class="pad-key"
      @click="onKey(key.value)"
    >
      <template v-if="key.value === 'backspace'">⌫</template>
      <template v-else-if="key.value === 'clear'">{{ clearText }}</template>
      <template v-else>{{ key.value }}</template>
    </button>
    <button
      v-if="showConfirm"
      type="button"
      class="pad-key pad-key--confirm"
      @click="emit('confirm')"
    >
      {{ confirmText }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

/**
 * 收银台数字小键盘（触屏收银机友好：大按键、无 hover 依赖）
 * 纯展示组件：按键通过事件抛给父组件处理输入逻辑（核销码取 0-9，直接收款金额带小数点），
 * 物理键盘输入由父组件自行监听，本组件只服务触屏点按
 */

/** 按键事件：数字/小数点/退格/清空/确认 */
const emit = defineEmits<{
  (e: 'input', char: string): void
  (e: 'backspace'): void
  (e: 'clear'): void
  (e: 'confirm'): void
}>()

/** 是否显示小数点键（核销码等纯整数场景关闭） */
const props = withDefaults(
  defineProps<{
    /** 是否显示小数点键（金额输入 true，取货码 false） */
    dot?: boolean
    /** 清空键文案 */
    clearText?: string
    /** 是否显示独立确认键 */
    showConfirm?: boolean
    /** 确认键文案 */
    confirmText?: string
  }>(),
  { dot: false, clearText: '清空', showConfirm: false, confirmText: '确认' },
)

/**
 * 键盘布局（3 列网格）：
 * 金额模式（dot）：1-9 / 小数点 0 退格——清空由逐位退格覆盖；
 * 整数模式（核销码）：1-9 / 清空 0 退格
 */
const keys = computed(() => {
  const list = props.dot
    ? ['1', '2', '3', '4', '5', '6', '7', '8', '9', '.', '0', 'backspace']
    : ['1', '2', '3', '4', '5', '6', '7', '8', '9', 'clear', '0', 'backspace']
  return list.map((value) => ({ value }))
})

/**
 * 按键分发：动作键走独立事件，数字/小数点走 input
 * @param value 键值
 */
function onKey(value: string) {
  if (value === 'backspace') {
    emit('backspace')
    return
  }
  if (value === 'clear') {
    emit('clear')
    return
  }
  emit('input', value)
}
</script>

<style lang="scss" scoped>
.number-pad {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 10px;
  width: 100%;
}
.pad-key {
  height: 56px;
  border: 1px solid #e0e0e6;
  border-radius: 10px;
  background: #fff;
  font-size: 22px;
  font-weight: 600;
  color: #333;
  cursor: pointer;
  user-select: none;
  transition: background 0.15s;
  &:active {
    background: #f2f2f5;
  }
  &--confirm {
    grid-column: 1 / -1;
    background: #07c160;
    border-color: #07c160;
    color: #fff;
    &:active {
      background: #06ad56;
    }
  }
}
</style>
