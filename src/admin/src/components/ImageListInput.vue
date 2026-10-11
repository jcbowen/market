<template>
  <div
    class="image-list-input"
    :class="{ 'drag-over': dragOver }"
    @dragenter.prevent="onDragEnter"
    @dragover.prevent
    @dragleave.prevent="onDragLeave"
    @drop.prevent="onDrop"
  >
    <div class="actions">
      <n-button secondary :loading="uploadingCount > 0" @click="showPicker = true">
        <template #icon>
          <n-icon><ImagesOutline /></n-icon>
        </template>
        {{ buttonText }}
      </n-button>
      <span v-if="hint" class="hint">{{ hint }}</span>
      <span class="hint">共 {{ list.length }} 张{{ sortable ? '，可拖拽图片调整顺序' : '' }}</span>
    </div>

    <TransitionGroup v-if="list.length" name="sort" tag="div" class="grid">
      <div
        v-for="(url, index) in list"
        :key="url"
        class="cell"
        :class="{ dragging: dragIndex === index, 'drop-target': overIndex === index && dragIndex !== index }"
        :draggable="sortable"
        @dragstart="onDragStart($event, index)"
        @dragover.prevent="onDragOver(index)"
        @dragleave="overIndex = -1"
        @drop="onSortDrop($event, index)"
        @dragend="onDragEnd"
      >
        <img :src="url" alt="相册图片" draggable="false" />
        <n-button class="remove" size="tiny" circle type="error" @click.stop="removeAt(index)">
          <template #icon>
            <n-icon :size="12"><CloseOutline /></n-icon>
          </template>
        </n-button>
        <span v-if="index === 0" class="first-flag">首图</span>
      </div>
    </TransitionGroup>
    <p v-else class="empty">暂无图片，可上传或从附件库选择</p>

    <AttachmentPicker
      v-model:show="showPicker"
      multiple
      :selected="list"
      @confirm="onPicked"
    />
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useMessage } from 'naive-ui'
import { CloseOutline, ImagesOutline } from '@vicons/ionicons5'
import { isHandledRequestError, uploadImageFile } from '@/utils/image'
import AttachmentPicker from '@/components/AttachmentPicker.vue'

/**
 * 多图上传输入组件（图片列表模式）
 * 网格展示图片列表，点击「上传图片」打开附件库弹窗（多选批量回填，弹窗内可直接上传），
 * 支持原生 HTML5 拖拽排序（不依赖第三方拖拽库，sortable 可关闭）、逐张删除，
 * dragUpload 开启后支持把图片文件拖入组件区域批量上传（与排序拖拽互不干扰），
 * 新增图片自动去重；首图标记列表第 1 张（展示顺序即保存顺序）
 */
const props = withDefaults(
  defineProps<{
    /** 图片 URL 列表（v-model:list） */
    list?: string[]
    /** 上传按钮文案 */
    buttonText?: string
    /** 按钮旁的补充提示文字（如建议尺寸） */
    hint?: string
    /** 是否支持拖拽图片文件到组件区域批量上传 */
    dragUpload?: boolean
    /** 是否支持拖拽排序 */
    sortable?: boolean
  }>(),
  {
    list: () => [],
    buttonText: '上传图片',
    hint: '',
    dragUpload: false,
    sortable: true,
  }
)

const emit = defineEmits<{
  (e: 'update:list', urls: string[]): void
}>()

const message = useMessage()
const uploadingCount = ref(0)
const showPicker = ref(false)
const dragIndex = ref(-1)
const overIndex = ref(-1)
const dragOver = ref(false)

/** 拖拽进入层级计数（子元素间移动会反复触发 enter/leave，用计数避免闪烁） */
let dragDepth = 0

/**
 * 附件库批量回填：合并选中 URL 并按已有顺序去重
 * @param urls 弹窗确认的 URL 列表
 */
function onPicked(urls: string[]) {
  emit('update:list', mergeUnique(props.list, urls))
}

/**
 * 合并 URL 列表并按左侧顺序去重
 * @param base 已有列表
 * @param incoming 追加列表
 * @returns 去重后的新列表
 */
function mergeUnique(base: string[], incoming: string[]): string[] {
  const merged = [...base]
  for (const url of incoming) {
    if (!merged.includes(url)) {
      merged.push(url)
    }
  }
  return merged
}

/**
 * 删除指定位置的图片
 * @param index 图片下标
 */
function removeAt(index: number) {
  const next = [...props.list]
  next.splice(index, 1)
  emit('update:list', next)
}

/**
 * 拖拽开始：记录拖拽源下标
 * @param event 拖拽事件
 * @param index 拖拽源下标
 */
function onDragStart(event: DragEvent, index: number) {
  if (!props.sortable) return
  dragIndex.value = index
  event.dataTransfer!.effectAllowed = 'move'
  // Firefox 要求必须 setData 才会启动拖拽
  event.dataTransfer!.setData('text/plain', String(index))
}

/**
 * 拖拽经过目标格子：记录目标下标用于视觉提示
 * @param index 目标下标
 */
function onDragOver(index: number) {
  if (!props.sortable) return
  overIndex.value = index
}

/**
 * 格子内放置：文件拖入时交由根上的上传处理，否则执行排序移动
 * @param event 放置事件
 * @param index 放置目标下标
 */
function onSortDrop(event: DragEvent, index: number) {
  event.preventDefault()
  if (event.dataTransfer?.files?.length) return
  const from = dragIndex.value
  if (props.sortable && from >= 0 && from !== index) {
    const next = [...props.list]
    const [moved] = next.splice(from, 1)
    next.splice(index, 0, moved)
    emit('update:list', next)
  }
  onDragEnd()
}

/**
 * 拖拽结束（含放置与取消）：清理拖拽状态
 */
function onDragEnd() {
  dragIndex.value = -1
  overIndex.value = -1
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
 * 放置文件：批量上传拖入的图片（dragUpload 关闭时仅清理高亮）
 * @param event 放置事件
 */
function onDrop(event: DragEvent) {
  dragDepth = 0
  dragOver.value = false
  if (!props.dragUpload) return
  const files = Array.from(event.dataTransfer?.files ?? [])
  if (files.length) {
    void uploadFiles(files)
  }
}

/**
 * 批量上传图片文件并追加到列表尾部（自动去重）
 * 单张失败不阻塞其他图片，结束后统一提示成功与失败数量
 * @param files 图片文件列表
 */
async function uploadFiles(files: File[]) {
  const images = files.filter((f) => f.type.startsWith('image/'))
  if (!images.length) {
    message.error('仅支持上传图片文件')
    return
  }

  uploadingCount.value += images.length
  try {
    const results = await Promise.allSettled(images.map((file) => uploadImageFile(file)))
    const okUrls = results
      .filter((r): r is PromiseFulfilledResult<string> => r.status === 'fulfilled')
      .map((r) => r.value)
    const failCount = images.length - okUrls.length
    if (failCount > 0) {
      message.error(`${failCount} 张图片上传失败`)
    }
    if (okUrls.length) {
      emit('update:list', mergeUnique(props.list, okUrls))
      message.success(`已上传 ${okUrls.length} 张图片`)
    }
  } finally {
    uploadingCount.value -= images.length
  }
}
</script>

<style lang="scss" scoped>
.image-list-input {
  &.drag-over {
    outline: 2px dashed #18a058;
    outline-offset: 3px;
    border-radius: 4px;
  }
}

.actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;

  .hint {
    font-size: 12px;
    color: #999;
  }
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(104px, 1fr));
  gap: 10px;
  margin-top: 10px;
}

.cell {
  position: relative;
  border: 1px solid #e5e7eb;
  border-radius: 6px;
  overflow: hidden;
  cursor: grab;
  background: #fafafa;

  &:active {
    cursor: grabbing;
  }

  &.dragging {
    opacity: 0.45;
  }

  &.drop-target {
    border: 2px dashed #18a058;
  }

  img {
    display: block;
    width: 100%;
    aspect-ratio: 1 / 1;
    object-fit: cover;
    pointer-events: none;
  }

  .remove {
    position: absolute;
    top: 4px;
    right: 4px;
  }

  .first-flag {
    position: absolute;
    left: 0;
    bottom: 0;
    padding: 1px 6px;
    font-size: 11px;
    color: #fff;
    background: rgba(24, 160, 88, 0.85);
    border-radius: 0 6px 0 0;
  }
}

.empty {
  margin-top: 10px;
  font-size: 12px;
  color: #bbb;
}

/* 拖拽排序时的位移动画 */
.sort-move {
  transition: transform 0.2s ease;
}
</style>
