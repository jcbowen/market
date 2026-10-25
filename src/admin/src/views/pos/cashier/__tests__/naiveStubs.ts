import { defineComponent } from 'vue'

/**
 * 收银台组件单测的 naive-ui 替身
 * 项目里 naive-ui 组件由全局注册解析（main.ts），测试环境用轻量替身替代，
 * 只保留被测组件实际用到的行为：v-model、disabled/loading、click/keydown 透传
 */

/** n-modal 替身：show=true 时内联渲染默认插槽（不走 teleport，VTU 可直接查询） */
export const NModal = defineComponent({
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show" class="n-modal-stub"><slot /></div>',
})

/**
 * n-input 替身：原生 input 承载 v-model 与键盘事件透传（回车提交、Enter/Esc 键盘化测试用）
 * keyup 与 keydown 均透传：OrderTab 搜索框回车走 @keyup.enter（扫码枪等价键盘输入+回车），
 * DirectPayTab 快捷键走 @keydown
 * 注意 naive-ui 的 model prop 是 `value`（v-model:value 用法），与原生 Vue 约定不同
 */
export const NInput = defineComponent({
  props: {
    value: { type: String, default: '' },
    disabled: { type: Boolean, default: false },
    placeholder: { type: String, default: '' },
  },
  emits: ['update:value', 'keydown', 'keyup'],
  // 被 ref 引用时（inputRef.value?.focus()）需要暴露 focus
  methods: {
    focus() {},
  },
  template: `<input
    class="n-input-stub"
    :disabled="disabled"
    :placeholder="placeholder"
    :value="value"
    @input="$emit('update:value', $event.target.value)"
    @keydown="$emit('keydown', $event)"
    @keyup="$emit('keyup', $event)"
  />`,
})

/** n-button 替身：loading 期间同步置灰（与真实组件交互口径一致） */
export const NButton = defineComponent({
  props: {
    disabled: { type: Boolean, default: false },
    loading: { type: Boolean, default: false },
  },
  emits: ['click'],
  template: `<button type="button" :disabled="disabled || loading" @click="$emit('click')"><slot /></button>`,
})

/** n-space 替身：纵向布局容器，直接透传插槽 */
export const NSpace = defineComponent({ template: '<div class="n-space-stub"><slot /></div>' })

/** 收银台单测统一挂载配置：替身组件注入 */
export const naiveTestComponents = { NModal, NInput, NButton, NSpace }

/** naive-ui 组合式 API 替身（vi.mock('naive-ui') 工厂用） */
export function mockNaiveApi() {
  const messages: string[] = []
  return {
    useMessage: () => ({
      success: (m: string) => messages.push(m),
      warning: (m: string) => messages.push(m),
      info: (m: string) => messages.push(m),
      error: (m: string) => messages.push(m),
    }),
    useDialog: () => ({ warning: () => ({}), success: () => ({}), error: () => ({}) }),
    __messages: messages,
  }
}
