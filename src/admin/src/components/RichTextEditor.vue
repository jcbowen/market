<template>
  <div class="rich-editor">
    <!-- 自定义工具栏 DOM：Quill 按类名识别按钮（空内容即图标按钮）；
         图片按钮用自定义类名（非 ql-image）避免 Quill 默认处理器插入 base64，
         改走 uploadImageFile 上传后插入 URL -->
    <div ref="toolbarRef">
      <button class="ql-bold" type="button" title="加粗"></button>
      <button class="ql-italic" type="button" title="斜体"></button>
      <button class="ql-underline" type="button" title="下划线"></button>
      <button class="ql-link" type="button" title="链接"></button>
      <button v-if="enableImage" type="button" title="插入图片" class="rich-editor__img-btn" @click="pickImage">
        <svg viewBox="0 0 18 18" width="16" height="16">
          <rect x="2" y="3" width="14" height="12" rx="1.5" fill="none" stroke="currentColor" stroke-width="1.4" />
          <circle cx="6.4" cy="7.4" r="1.4" fill="currentColor" />
          <path d="M2.5 13.5 L7 9 L10 12 L12 10.2 L15.5 13.5 Z" fill="currentColor" />
        </svg>
      </button>
      <button class="ql-clean" type="button" title="清除格式"></button>
    </div>
    <div ref="editorRef"></div>
    <input ref="fileInputRef" type="file" accept="image/*" style="display: none" @change="onFilePicked" />
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Quill from 'quill'
import 'quill/dist/quill.snow.css'
import { isHandledRequestError, uploadImageFile } from '@/utils/image'

/**
 * 富文本编辑器通用组件（Quill 2 封装）
 * 基础排版：加粗/斜体/下划线/链接/清除格式，产出 HTML 由服务端白名单消毒后落库；
 * enableImage 开启后支持插入图片（走后台上传链路得到 URL，而非 base64 内联）；
 * 编辑中（text-change）主动外发，外部回灌（value 变化）时与当前内容比对去重，避免回环
 *
 * 使用示例：
 *   <RichTextEditor v-model:value="form.footer_html" :readonly="!isSuperAdmin" />
 *   <RichTextEditor v-model:value="form.content" enable-image />
 */
const props = withDefaults(
  defineProps<{
    /** HTML 内容（v-model:value），空串表示未配置 */
    value: string
    /** 只读（禁用编辑） */
    readonly?: boolean
    /** 是否启用图片插入（上传后以 <img src="URL"> 嵌入） */
    enableImage?: boolean
  }>(),
  { readonly: false, enableImage: false },
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
    placeholder: props.enableImage
      ? '支持加粗、斜体、下划线、链接与插入图片'
      : '支持加粗、斜体、下划线与链接；例：© 2026 社区商超 · 京ICP备00000000号',
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

// --- 图片插入（enableImage 时可用） ---

const fileInputRef = ref<HTMLInputElement>()
let uploadingImage = false

/** 打开文件选择框（readonly 时不可用） */
function pickImage() {
  if (props.readonly || uploadingImage || !quill) return
  fileInputRef.value?.click()
}

/**
 * 上传所选图片并在光标处插入 URL embed
 * 走统一上传链路（本地/OSS 自适应），避免 Quill 默认处理器内联 base64 撑爆正文
 */
async function onFilePicked(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || !quill) return
  uploadingImage = true
  try {
    const url = await uploadImageFile(file)
    const range = quill.getSelection(true)
    quill.insertEmbed(range?.index ?? quill.getLength(), 'image', url, 'user')
    quill.setSelection((range?.index ?? quill.getLength()) + 1, 0)
  } catch (err) {
    if (!isHandledRequestError(err)) {
      console.error('[RichTextEditor] 图片上传失败：', err)
    }
  } finally {
    uploadingImage = false
  }
}

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

/* 自定义图片按钮：对齐 Quill 工具栏按钮的视觉尺寸 */
.rich-editor :deep(.rich-editor__img-btn) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 24px;
  border: none;
  background: none;
  cursor: pointer;
  color: #444;
  padding: 0 2px;
}
.rich-editor :deep(.rich-editor__img-btn:hover) {
  color: #06c;
}
</style>
