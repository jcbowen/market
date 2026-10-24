<template>
  <div class="rich-editor">
    <!-- 自定义工具栏 DOM：Quill 按类名识别按钮（空内容即图标按钮） -->
    <div ref="toolbarRef">
      <button class="ql-bold" type="button" title="加粗"></button>
      <button class="ql-italic" type="button" title="斜体"></button>
      <button class="ql-underline" type="button" title="下划线"></button>
      <button class="ql-link" type="button" title="链接"></button>
      <button class="ql-clean" type="button" title="清除格式"></button>
    </div>
    <div ref="editorRef"></div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Quill from 'quill'
import 'quill/dist/quill.snow.css'

/**
 * 富文本编辑器通用组件（Quill 2 封装）
 * 仅开放基础排版：加粗/斜体/下划线/链接/清除格式，产出 HTML 由服务端白名单消毒后落库；
 * 编辑中（text-change）主动外发，外部回灌（value 变化）时与当前内容比对去重，避免回环
 *
 * 使用示例：
 *   <RichTextEditor v-model:value="form.footer_html" :readonly="!isSuperAdmin" />
 */
const props = withDefaults(
  defineProps<{
    /** HTML 内容（v-model:value），空串表示未配置 */
    value: string
    /** 只读（禁用编辑） */
    readonly?: boolean
  }>(),
  { readonly: false },
)

const emit = defineEmits<{
  (e: 'update:value', v: string): void
}>()

const toolbarRef = ref<HTMLDivElement>()
const editorRef = ref<HTMLDivElement>()

let quill: Quill | null = null
/** 外部回灌标志：回灌触发的 text-change 不再外发，防 v-model 回环 */
let applying = false

/**
 * 读取编辑器当前 HTML，空文档（innerHTML 为 <p><br></p>）规范化为空串
 *
 * 返回值：
 *   - string: 当前 HTML 内容
 */
function currentHTML(): string {
  if (!quill) return ''
  return quill.getText().trim() === '' ? '' : quill.root.innerHTML
}

onMounted(() => {
  quill = new Quill(editorRef.value!, {
    theme: 'snow',
    placeholder: '支持加粗、斜体、下划线与链接；例：© 2026 社区商超 · 京ICP备00000000号',
    readOnly: props.readonly,
    modules: { toolbar: toolbarRef.value! },
  })
  if (props.value) {
    applying = true
    quill.clipboard.dangerouslyPasteHTML(props.value)
    applying = false
  }
  quill.on('text-change', () => {
    if (applying) return
    emit('update:value', currentHTML())
  })
})

watch(
  () => props.value,
  (v) => {
    if (!quill || applying) return
    if ((currentHTML() || '') === (v || '')) return
    applying = true
    quill.clipboard.dangerouslyPasteHTML(v || '')
    applying = false
  },
)

watch(
  () => props.readonly,
  (v) => {
    quill?.enable(!v)
  },
)

onBeforeUnmount(() => {
  quill = null
})
</script>

<style scoped>
.rich-editor {
  width: 100%;
}

/* 编辑态链接去下划线：与展示端视觉一致（展示端链接样式由服务端注入内联 style 统一）；
   Quill 动态生成的 DOM 无 scoped 属性，须 :deep 穿透，且选择器特异性需压过 quill.snow.css 的下划线规则 */
.rich-editor :deep(.ql-snow .ql-editor a) {
  text-decoration: none;
}
</style>
