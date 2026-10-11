<template>
  <n-input
    :value="value"
    :placeholder="placeholder"
    clearable
    @update:value="onInput"
  >
    <template #suffix>
      <n-image
        v-if="value"
        :src="value"
        class="preview"
        fit="cover"
        :img-props="{ style: { borderRadius: '3px' } }"
        lazy
      />
      <n-upload
        :show-file-list="false"
        accept="image/*"
        :custom-request="handleUpload"
      >
        <n-button type="primary" secondary size="tiny" :loading="uploading">
          上传图片
        </n-button>
      </n-upload>
    </template>
  </n-input>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useMessage, type UploadCustomRequestOptions } from 'naive-ui'
import { uploadImage } from '@/api/attachment'

/**
 * 图片上传输入框通用组件
 * 上传按钮与缩略图预览收在输入框 suffix 插槽内，宽度行为与普通 n-input 一致（默认 100%，支持外部设宽）
 * 上传成功后回填接口返回的完整 URL 回显；入库值由后端统一剥离域名只存相对路径
 * 上传通道由后台附件配置自动分流：local 走 multipart 直传后端，oss 走预签名直传 OSS
 */

const props = withDefaults(
  defineProps<{
    /** 图片 URL（v-model:value） */
    value?: string | null
    /** 占位提示文案 */
    placeholder?: string
  }>(),
  {
    value: '',
    placeholder: '图片 URL 或点击右侧按钮上传',
  }
)

const emit = defineEmits<{
  (e: 'update:value', url: string): void
  (e: 'success', url: string): void
}>()

const message = useMessage()
const uploading = ref(false)

/**
 * 手动输入 URL 时的同步处理
 * @param url 输入的 URL
 */
function onInput(url: string) {
  emit('update:value', url)
}

/**
 * 自定义上传请求
 * 读取图片宽高后调用 uploadImage 按存储模式分流上传，成功后回填 URL
 * @param options naive-ui 自定义上传请求参数
 */
async function handleUpload(options: UploadCustomRequestOptions) {
  const file = options.file.file
  if (!file) {
    return
  }

  uploading.value = true
  try {
    const [width, height] = await readImageSize(file)
    const url = await uploadImage(file, width, height)
    if (!url) {
      message.error('上传成功但未返回图片地址')
      options.onError()
      return
    }
    emit('update:value', url)
    emit('success', url)
    message.success('图片上传成功')
    options.onFinish()
  } catch (err) {
    // 业务错误提示已由 axios 拦截器统一弹出，这里只标记失败状态
    options.onError()
    if (err instanceof Error && !isHandledRequestError(err)) {
      message.error(err.message || '图片上传失败')
    }
  } finally {
    uploading.value = false
  }
}

/**
 * 读取图片宽高
 * @param file 图片文件
 * @returns [宽, 高]，解析失败返回 [0, 0]
 */
function readImageSize(file: File): Promise<[number, number]> {
  return new Promise((resolve) => {
    const img = new Image()
    const objectURL = URL.createObjectURL(file)
    img.onload = () => {
      URL.revokeObjectURL(objectURL)
      resolve([img.naturalWidth, img.naturalHeight])
    }
    img.onerror = () => {
      URL.revokeObjectURL(objectURL)
      resolve([0, 0])
    }
    img.src = objectURL
  })
}

/**
 * 判断是否为已被 axios 拦截器提示过的请求错误
 * @param err 错误对象
 * @returns 已提示返回 true
 */
function isHandledRequestError(err: Error): boolean {
  return 'isAxiosError' in err
}
</script>

<style lang="scss" scoped>
/* 上传按钮与缩略图收在 input 的 suffix 插槽内，
   组件整体宽度行为与普通 n-input 一致（默认 100%，可由外部 style/class 设宽） */
:deep(.preview) {
  width: 22px;
  height: 22px;
  border-radius: 3px;
  border: 1px solid #eee;
  cursor: pointer;
  flex-shrink: 0;
}
</style>
