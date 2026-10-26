<template>
  <n-modal
    :show="show"
    title="从附件库选择"
    preset="card"
    style="width: 780px; max-width: 92vw"
    @update:show="emit('update:show', $event)"
  >
    <div class="toolbar">
      <n-input
        v-model:value="keyword"
        placeholder="按文件名或路径搜索"
        clearable
        class="keyword"
      >
        <template #prefix>
          <n-icon><SearchOutline /></n-icon>
        </template>
      </n-input>
      <n-date-picker
        v-model:value="dateRange"
        type="daterange"
        clearable
        class="date-range"
        @update:value="onDateChange"
      />
      <n-upload
        :show-file-list="false"
        accept="image/*"
        :multiple="multiple"
        :custom-request="handleUpload"
      >
        <n-button type="primary" secondary :loading="uploadingCount > 0">
          <template #icon>
            <n-icon><CloudUploadOutline /></n-icon>
          </template>
          上传图片
        </n-button>
      </n-upload>
    </div>

    <n-spin :show="loading">
      <n-empty v-if="!list.length && !loading" description="暂无附件，可先上传图片" class="empty" />
      <n-grid v-else cols="3 s:4 m:6" :x-gap="10" :y-gap="10">
        <n-gi v-for="item in list" :key="item.attach_id">
          <div class="card" :class="{ selected: isSelected(item.url) }" @click="onPick(item)">
            <img :src="item.url" :alt="item.name" loading="lazy" draggable="false" />
            <div class="meta">
              <span class="name" :title="item.name">{{ item.name }}</span>
              <span class="time">{{ formatTimeText(item.created_at) }}</span>
            </div>
            <n-button class="eye" size="tiny" circle quaternary @click.stop="previewItem = item">
              <template #icon>
                <n-icon><EyeOutline /></n-icon>
              </template>
            </n-button>
            <div v-if="multiple && isSelected(item.url)" class="badge">
              <n-icon :size="12"><CheckmarkOutline /></n-icon>
            </div>
          </div>
        </n-gi>
      </n-grid>
    </n-spin>

    <div class="footer">
      <span v-if="multiple" class="picked-count">已选 {{ multiSelected.size }} 张</span>
      <n-pagination
        v-model:page="page"
        :item-count="total"
        :page-size="pageSize"
        @update:page="loadList"
      />
      <n-button
        v-if="multiple"
        type="primary"
        :disabled="!multiSelected.size"
        @click="onConfirm"
      >
        确认选择
      </n-button>
    </div>

    <!-- 大图预览（嵌套弹窗） -->
    <n-modal
      :show="!!previewItem"
      preset="card"
      :title="previewItem?.name"
      style="width: auto; max-width: 90vw"
      @update:show="previewItem = null"
    >
      <img
        v-if="previewItem"
        :src="previewItem.url"
        :alt="previewItem.name"
        class="preview-large"
      />
      <p v-if="previewItem" class="preview-info">
        {{ previewItem.width }}×{{ previewItem.height }} · {{ formatSize(previewItem.size) }} ·
        {{ formatTimeText(previewItem.created_at) }}
      </p>
    </n-modal>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, watch, onUnmounted } from 'vue'
import { useMessage, type UploadCustomRequestOptions } from 'naive-ui'
import {
  CheckmarkOutline,
  CloudUploadOutline,
  EyeOutline,
  SearchOutline,
} from '@vicons/ionicons5'
import { listAttachments, type AttachmentItem } from '@/api/attachment'
import { formatDateTime, isHandledRequestError, uploadImageFile } from '@/utils/image'
// 后端 RFC3339 时间字符串展示用,与 image 版(Date 对象入参)区分
import { formatDateTime as formatTimeText } from '@/utils/datetime'

/**
 * 附件选择弹窗组件
 * 分页浏览当前登录管理员上传的图片附件（每页 12 条，响应式 3/4/6 列），
 * 支持关键字（文件名/路径）与上传时间范围筛选、弹窗内直接上传（local/oss 自动分流），
 * 上传成功后新图立即显示在列表中（按上传时间倒序）
 * 选择交互：单图模式点击卡片即回传并关闭（弹窗内上传成功同样直接回填新图并关闭）；
 * 多图模式点选/取消（跨页保留），底部确认批量回传
 */

const props = withDefaults(
  defineProps<{
    /** 弹窗显示（v-model:show） */
    show: boolean
    /** 是否多选模式 */
    multiple?: boolean
    /** 多选模式打开时的已选 URL 列表，用于标记选中态 */
    selected?: string[]
  }>(),
  {
    multiple: false,
    selected: () => [],
  }
)

const emit = defineEmits<{
  (e: 'update:show', v: boolean): void
  /** 单选模式点击卡片回传 */
  (e: 'select', url: string): void
  /** 多选模式确认回传 */
  (e: 'confirm', urls: string[]): void
}>()

const message = useMessage()

/** 每页条数（12 条恰好排满 2×6 / 3×4 / 4×3 响应式网格） */
const PAGE_SIZE = 12
const pageSize = PAGE_SIZE

const list = ref<AttachmentItem[]>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const keyword = ref('')
const dateRange = ref<[number, number] | null>(null)
const uploadingCount = ref(0)
const multiSelected = ref<Set<string>>(new Set())
const previewItem = ref<AttachmentItem | null>(null)

let keywordTimer: ReturnType<typeof setTimeout> | null = null

/**
 * 请求序号：防抖搜索与翻页交错时，只采用最后一次请求的响应，
 * 否则慢响应会后到并覆盖新结果，列表显示与筛选条件不一致
 */
let loadSeq = 0

/**
 * 归一化附件地址为可比对的资源键
 * 上传响应按部署域名拼完整 URL，列表接口按附件配置拼装（未配访问域名时为站内 /uploads/ 路径），
 * 两种形态指向同一文件，统一剥协议与 host、剥离 /uploads/ 前缀后按相对路径比较；
 * OSS 场景 CDN/bucket 域名分叉的同一对象 key 也因此能匹配
 * @param src 附件 URL 或相对路径
 * @returns 归一化资源键
 */
function normalizeMediaKey(src: string): string {
  if (!src) return ''
  // 完整 URL：截取 path 段后按纯路径规则继续归一，无 path 段则原样返回
  if (/^https?:\/\//i.test(src)) {
    const pathIdx = src.indexOf('/', src.indexOf('://') + 3)
    if (pathIdx === -1) return src
    return normalizeMediaKey(src.slice(pathIdx)) || src
  }
  // 纯路径：剥离开头的 / 与 uploads/ 目录前缀
  const trimmed = src.replace(/^\/+/, '')
  return trimmed.startsWith('uploads/') ? trimmed.slice('uploads/'.length) : trimmed
}

onUnmounted(() => {
  if (keywordTimer) clearTimeout(keywordTimer)
  keywordTimer = null
})

watch(
  () => props.show,
  (v) => {
    if (v) init()
  }
)

watch(keyword, () => {
  // 关键字输入 300ms 防抖后自动搜索
  if (keywordTimer) clearTimeout(keywordTimer)
  keywordTimer = setTimeout(() => {
    keywordTimer = null
    reload()
  }, 300)
})

/**
 * 打开弹窗时的初始化：重置筛选与页码，多选模式带入已选标记
 */
function init() {
  page.value = 1
  keyword.value = ''
  dateRange.value = null
  previewItem.value = null
  multiSelected.value = new Set(props.multiple ? props.selected : [])
  void loadList()
}

/**
 * 重置到第一页并加载列表
 */
function reload() {
  page.value = 1
  void loadList()
}

/**
 * 加载附件分页列表
 * 按上传时间倒序，筛选条件含关键字与时间范围
 */
async function loadList() {
  const seq = ++loadSeq
  loading.value = true
  try {
    const params: Record<string, unknown> = { page: page.value, page_size: pageSize }
    if (keyword.value.trim()) {
      params.keyword = keyword.value.trim()
    }
    if (dateRange.value) {
      const [start, end] = dateRange.value
      params.start_time = dayBoundary(start, false)
      params.end_time = dayBoundary(end, true)
    }
    const res = await listAttachments(params)
    if (seq !== loadSeq) return
    // proto3 omitempty：空列表时 total/list 字段可能缺省，需兜底
    list.value = res.data.data.list || []
    total.value = res.data.data.total || 0
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

/**
 * 日期毫秒时间戳转查询边界时间字符串
 * @param ms 日期时间戳（取其年月日）
 * @param endOfDay 是否取当日末尾（23:59:59），否则为 00:00:00
 * @returns YYYY-MM-DD HH:mm:ss
 */
function dayBoundary(ms: number, endOfDay: boolean): string {
  const d = new Date(ms)
  const time = endOfDay ? '23:59:59' : '00:00:00'
  return `${formatDateTime(d).slice(0, 10)} ${time}`
}

/**
 * 时间范围筛选变化，回到第一页重新加载
 */
function onDateChange() {
  reload()
}

/**
 * 判断 URL 是否处于多选已选集合
 * 按归一化资源键比对，兼容上传响应完整 URL 与列表站内路径两种形态
 * @param url 图片 URL
 * @returns 已选返回 true
 */
function isSelected(url: string): boolean {
  const key = normalizeMediaKey(url)
  if (!key) return false
  for (const sel of multiSelected.value) {
    if (normalizeMediaKey(sel) === key) return true
  }
  return false
}

/**
 * 点击卡片：单选模式回传并关闭；多选模式切换选中态
 * 切换按归一化资源键查找既有项，避免同一文件以两种 URL 形态重复入集合
 * @param item 附件条目
 */
function onPick(item: AttachmentItem) {
  if (!props.multiple) {
    emit('select', item.url)
    emit('update:show', false)
    return
  }
  const key = normalizeMediaKey(item.url)
  const existing = [...multiSelected.value].find((sel) => normalizeMediaKey(sel) === key)
  const next = new Set(multiSelected.value)
  if (existing !== undefined) {
    next.delete(existing)
  } else {
    next.add(item.url)
  }
  multiSelected.value = next
}

/**
 * 多选模式确认：回传全部已选 URL（跨页保留项）并关闭
 */
function onConfirm() {
  emit('confirm', [...multiSelected.value])
  emit('update:show', false)
}

/**
 * 弹窗内上传（多选模式支持多文件并发）
 * 上传成功后回到第一页刷新列表，保证新图立即可见
 * @param options naive-ui 自定义上传请求参数
 */
async function handleUpload(options: UploadCustomRequestOptions) {
  const file = options.file.file
  if (!file) {
    return
  }

  uploadingCount.value += 1
  try {
    // 上传响应即完整访问 URL，直接用于回填/选中，
    // 不依赖列表接口的 URL 拼装结果（附件访问域名未配置时列表会降级为站内路径）
    const url = await uploadImageFile(file)
    options.onFinish()
    message.success('图片上传成功')
    if (!props.multiple) {
      emit('select', url)
      emit('update:show', false)
    } else if (!isSelected(url)) {
      // 已按归一化资源键存在（重复上传/秒传）时不重复入集合
      multiSelected.value = new Set([...multiSelected.value, url])
    }
    reload()
  } catch (err) {
    // 业务错误提示已由 axios 拦截器统一弹出，这里只标记失败状态
    options.onError()
    if (!isHandledRequestError(err)) {
      message.error(err instanceof Error ? err.message : '图片上传失败')
    }
  } finally {
    uploadingCount.value -= 1
  }
}

/**
 * 字节数格式化为可读文本
 * @param size 字节数
 * @returns 形如 128.5 KB 的文本
 */
function formatSize(size: number): string {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / 1024 / 1024).toFixed(2)} MB`
}
</script>

<style lang="scss" scoped>
.toolbar {
  display: flex;
  gap: 10px;
  align-items: center;
  flex-wrap: wrap;
  margin-bottom: 12px;

  .keyword {
    width: 220px;
  }

  .date-range {
    flex: 1;
    min-width: 220px;
  }
}

.empty {
  padding: 40px 0;
}

.card {
  position: relative;
  border: 2px solid #e5e7eb;
  border-radius: 6px;
  overflow: hidden;
  cursor: pointer;
  background: #fafafa;
  transition: border-color 0.15s, box-shadow 0.15s;

  &:hover {
    border-color: #36ad6a;
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
  }

  &.selected {
    border-color: #18a058;
    box-shadow: 0 0 0 2px rgba(24, 160, 88, 0.2);
  }

  img {
    display: block;
    width: 100%;
    aspect-ratio: 1 / 1;
    object-fit: cover;
  }

  .meta {
    padding: 5px 8px 6px;
    line-height: 1.4;

    .name {
      display: block;
      font-size: 12px;
      color: #333;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .time {
      display: block;
      font-size: 11px;
      color: #999;
    }
  }

  .eye {
    position: absolute;
    top: 6px;
    right: 6px;
    background: rgba(255, 255, 255, 0.85);
  }

  .badge {
    position: absolute;
    top: 0;
    left: 0;
    width: 20px;
    height: 20px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: #18a058;
    color: #fff;
    border-radius: 0 0 6px 0;
  }
}

.footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 14px;

  .picked-count {
    margin-right: auto;
    font-size: 13px;
    color: #18a058;
  }
}

.preview-large {
  display: block;
  max-width: 80vw;
  max-height: 68vh;
  object-fit: contain;
}

.preview-info {
  margin-top: 8px;
  font-size: 12px;
  color: #999;
  text-align: center;
}
</style>
