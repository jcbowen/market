<template>
  <n-modal v-model:show="visible" preset="card" title="商品推广" style="max-width: 420px">
    <n-spin :show="loading">
      <n-space vertical align="center" :size="12">
        <n-text depth="2">{{ product?.name }}</n-text>
        <img
          v-if="qrDataUrl"
          :src="qrDataUrl"
          alt="商品推广二维码"
          class="promo-modal__qr"
        />
        <n-text v-else depth="3">二维码生成中…</n-text>

        <n-input :value="shareUrl" readonly size="small" />

        <n-space :size="8">
          <n-button size="small" type="primary" @click="copyLink">复制链接</n-button>
          <n-button size="small" tag="a" :href="shareUrl" target="_blank">打开页面</n-button>
        </n-space>

        <n-text depth="3" style="font-size: 12px">扫码直达 C 端商品详情页，可长按或右键保存二维码</n-text>
      </n-space>
    </n-spin>
  </n-modal>
</template>

<script setup lang="ts">
// 组件名按语义命名（弹窗组件不参与 keep-alive）
defineOptions({ name: 'PromoModal' })
import { ref, computed, watch } from 'vue'
import { NModal, NButton, NInput, NText, NSpace, NSpin, createDiscreteApi } from 'naive-ui'
import QRCode from 'qrcode'
import { getSiteSetting } from '@/api/admin'
import type { Product } from '@/api/types'

/**
 * PromoModal 商品推广弹窗
 *
 * 展示商品 C 端 H5 详情页二维码与链接，支持复制链接。
 * 链接格式：{effective_base_url}/s/#/pagesGoods/product/detail?id={商品ID}
 * （C 端为 hash 路由，base=/s/；域名取站点设置最终生效值）
 * 二维码由 qrcode 包纯前端生成，无后端接口。
 */
const { message } = createDiscreteApi(['message'])

const props = defineProps<{
  /** 弹窗显示（v-model） */
  show: boolean
  /** 推广目标商品（仅需 id 与 name） */
  product: Pick<Product, 'id' | 'name'> | null
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
}>()

const visible = computed({
  get: () => props.show,
  set: (v: boolean) => emit('update:show', v),
})

const loading = ref(false)
const baseUrl = ref('')
const qrDataUrl = ref('')

/** C 端商品详情分享链接 */
const shareUrl = computed(() => {
  if (!props.product || !baseUrl.value) return ''
  return `${baseUrl.value}/s/#/pagesGoods/product/detail?id=${props.product.id}`
})

// 弹窗打开时拉站点域名并生成二维码
watch(visible, (v) => {
  if (v) init()
})

/**
 * 初始化：取站点最终生效域名 → 生成二维码
 */
async function init() {
  qrDataUrl.value = ''
  loading.value = true
  try {
    const res = await getSiteSetting()
    if (res.data.code === 0) {
      baseUrl.value = (res.data.data.effective_base_url || res.data.data.base_url || '').replace(/\/+$/, '')
    }
    if (shareUrl.value) {
      qrDataUrl.value = await QRCode.toDataURL(shareUrl.value, { width: 220, margin: 2 })
    }
  } catch {
    message.error('获取站点域名失败，无法生成推广链接')
  } finally {
    loading.value = false
  }
}

/**
 * 复制推广链接（clipboard API，降级 execCommand）
 */
async function copyLink() {
  if (!shareUrl.value) return
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(shareUrl.value)
    } else {
      // 非安全上下文（http）降级：隐藏 textarea + execCommand
      const ta = document.createElement('textarea')
      ta.value = shareUrl.value
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    message.success('链接已复制')
  } catch {
    message.error('复制失败，请手动选择链接复制')
  }
}
</script>

<style scoped>
.promo-modal__qr {
  width: 220px;
  height: 220px;
  border: 1px solid #efeff5;
  border-radius: 8px;
}
</style>
