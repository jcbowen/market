<template>
  <div
    class="image-upload-input"
    :class="{ 'drag-over': dragOver }"
    @dragenter.prevent="onDragEnter"
    @dragover.prevent
    @dragleave.prevent="onDragLeave"
    @drop.prevent="onDrop"
  >
    <n-input
      :value="value"
      :placeholder="placeholder"
      clearable
      @update:value="onInput"
    >
      <template #suffix>
        <n-button size="tiny" secondary :loading="uploading" @click="showPicker = true">
          {{ buttonText }}
        </n-button>
      </template>
    </n-input>
    <p v-if="hint" class="hint">{{ hint }}</p>

    <!-- 预览独立成行，点击可放大查看 -->
    <div v-if="value" class="preview-row">
      <img :src="value" alt="图片预览" class="preview" @click="showPreview = true" />
      <n-button quaternary type="error" size="small" @click="onRemove">
        移除
      </n-button>
    </div>

    <AttachmentPicker v-model:show="showPicker" @select="onSelect" />

    <n-modal
      v-model:show="showPreview"
      preset="card"
      title="图片预览"
      style="width: auto; max-width: 90vw"
    >
      <img v-if="value" :src="value" alt="图片预览" class="preview-large" />
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useMessage } from 'naive-ui'
import { isHandledRequestError, uploadImageFile } from '@/utils/image'
import AttachmentPicker from '@/components/AttachmentPicker.vue'

/**
 * 图片上传输入框通用组件（单图模式）
 * URL 输入框独立一行（保留手动输入能力），图片预览独立成行（点击可放大），
 * 点击「上传图片」按钮打开附件库弹窗，上传与选择均在弹窗内完成（选中即回填）；
 * dragUpload 开启后支持把图片文件拖入组件区域直接上传（拖入高亮），
 * 入库值由后端统一剥离域名只存相对路径；上传通道由后台附件配置自动分流
 */
const props = withDefaults(
  defineProps<{
    /** 图片 URL（v-model:value） */
    value?: string | null
    /** 占位提示文案 */
    placeholder?: string
    /** 上传按钮文案 */
    buttonText?: string
    /** 按钮下方的补充提示文字（如建议尺寸） */
    hint?: string
    /** 是否支持拖拽图片文件到组件区域直接上传 */
    dragUpload?: boolean
  }>(),
  {
    value: '',
    placeholder: '图片 URL 或点击右侧按钮上传',
    buttonText: '上传图片',
    hint: '',
    dragUpload: false,
  }
)

const emit = defineEmits<{
  (e: 'update:value', url: string): void
}>()

const message = useMessage()
const uploading = ref(false)
const showPicker = ref(false)
const showPreview = ref(false)
const dragOver = ref(false)

/** 拖拽进入层级计数（子元素间移动会反复触发 enter/leave，用计数避免闪烁） */
let dragDepth = 0

/**
 * 手动输入 URL 时的同步处理
 * @param url 输入的 URL
 */
function onInput(url: string) {
  emit('update:value', url)
}

/**
 * 附件库选中回填
 * @param url 选中的图片 URL
 */
function onSelect(url: string) {
  emit('update:value', url)
}

/**
 * 移除当前图片（清空输入值）
 */
function onRemove() {
  emit('update:value', '')
}

/**
 * 判断拖拽事件是否携带文件
 * @param event 拖拽事件
 * @returns 携带文件返回 true
 */
function hasFiles(event: DragEvent): boolean {
  return Array.from(event.dataTransfer?.types ?? []).includes('Files')
}

/**
 * 拖拽进入组件区域：dragUpload 开启且携带文件时显示高亮
 * @param event 拖拽事件
 */
function onDragEnter(event: DragEvent) {
  if (!props.dragUpload || !hasFiles(event)) return
  dragDepth += 1
  dragOver.value = true
}

/**
 * 拖拽离开组件区域：层级计数归零后取消高亮
 */
function onDragLeave() {
  if (!props.dragUpload) return
  dragDepth -= 1
  if (dragDepth <= 0) {
    dragDepth = 0
    dragOver.value = false
  }
}

/**
 * 放置文件：取第一张图片上传并回填（dragUpload 关闭时仅清理高亮）
 * @param event 放置事件
 */
async function onDrop(event: DragEvent) {
  dragDepth = 0
  dragOver.value = false
  if (!props.dragUpload) return

  const file = event.dataTransfer?.files?.[0]
  if (!file) return
  if (!file.type.startsWith('image/')) {
    message.error('仅支持上传图片文件')
    return
  }

  uploading.value = true
  try {
    const url = await uploadImageFile(file)
    emit('update:value', url)
    message.success('图片上传成功')
  } catch (err) {
    // 业务错误提示已由 axios 拦截器统一弹出，这里只兜底本地错误
    if (!isHandledRequestError(err)) {
      message.error(err instanceof Error ? err.message : '图片上传失败')
    }
  } finally {
    uploading.value = false
  }
}
</script>

<style lang="scss" scoped>
/* 组件整体宽度行为与普通 n-input 一致（默认 100%，可由外部 style/class 设宽） */
.image-upload-input {
  width: 100%;

  &.drag-over {
    outline: 2px dashed #18a058;
    outline-offset: 3px;
    border-radius: 4px;
  }
}

.hint {
  margin-top: 4px;
  font-size: 12px;
  color: #999;
}

/* 预览独立成行：约 96px 高，点击放大；移除按钮同行右侧 */
.preview-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin-top: 8px;
}

.preview {
  height: 96px;
  max-width: 160px;
  object-fit: cover;
  border-radius: 6px;
  border: 1px solid #e5e7eb;
  cursor: zoom-in;
}

.preview-large {
  display: block;
  max-width: 80vw;
  max-height: 68vh;
  object-fit: contain;
}
</style>
