<template>
  <n-card title="支付配置">
    <n-spin :show="paymentLoading">
      <!-- 渠道卡片：启用的在前按展示顺序排列，移动后卡片顺序即时重排；支付宝暂未开放，显示禁用态 -->
      <div class="channel-cards">
        <div
          v-for="ch in sortedCards"
          :key="ch.value"
          class="channel-card"
          :class="{ available: ch.available, inactive: ch.available && !enabledOrder.includes(ch.value) }"
          @click="onCardClick(ch)"
        >
          <div class="card-top">
            <span class="card-name">{{ ch.label }}</span>
          </div>
          <div class="card-status">
            <n-tag v-if="!ch.available" size="small" :bordered="false">暂未开放</n-tag>
            <span v-else-if="ch.value === 'cash'" class="status-text ok">无需配置</span>
            <span
              v-else-if="ch.value === 'wechat'"
              class="status-text"
              :class="paymentForm.wechat_payment_ready ? 'ok' : 'warn'"
            >
              {{ paymentForm.wechat_payment_ready ? '已配置' : '未配置' }}
            </span>
            <span v-else class="status-text">—</span>
          </div>
          <div class="card-ops">
            <span class="switch-holder" @click.stop>
              <n-switch
                size="small"
                :value="ch.available && enabledOrder.includes(ch.value)"
                :disabled="switchDisabled(ch)"
                @update:value="(on: boolean) => onChannelToggle(ch.value, on)"
              />
            </span>
            <template v-if="ch.available && enabledOrder.includes(ch.value)">
              <n-button quaternary size="tiny" :disabled="channelOrder(ch.value) === 0" @click.stop="moveChannel(ch.value, -1)">上移</n-button>
              <n-button quaternary size="tiny" :disabled="channelOrder(ch.value) === enabledOrder.length - 1" @click.stop="moveChannel(ch.value, 1)">下移</n-button>
            </template>
          </div>
        </div>
      </div>
      <n-text depth="3" class="form-hint">卡片从左到右的排列顺序即 C 端收银台的展示顺序，通过上移/下移调整；开关与顺序调整后即时保存生效。未配置的渠道需先点击卡片完成配置后再启用。</n-text>

      <!-- 配置弹窗：微信为完整表单；支付宝/货到付款仅提示 -->
      <n-modal v-model:show="showConfigModal" preset="card" :title="modalTitle" style="width: 680px; max-width: 92vw">
        <template v-if="modalChannel === 'wechat'">
          <n-alert v-if="!paymentForm.wechat_payment_ready" type="warning" style="margin-bottom: 16px;">
            当前微信支付配置不完整，C 端不会展示微信支付选项
          </n-alert>
          <n-alert v-else type="success" style="margin-bottom: 16px;">
            微信支付配置完整，C 端可正常展示
          </n-alert>

          <n-alert type="info" style="margin-bottom: 16px;">
            请将下方回调地址配置到微信商户平台（API 安全 → 支付通知 URL），配置后支付结果才能通知到本系统。
            地址由当前后台访问域名自动生成；若通过反向代理部署，请确认地址对外可访问。
          </n-alert>
          <div class="url-guide-row">
            <n-text code>{{ paymentForm.suggested_urls.wechat_notify_url }}</n-text>
            <n-button size="small" type="primary" secondary @click="copyURL(paymentForm.suggested_urls.wechat_notify_url)">复制</n-button>
          </div>

          <n-grid cols="1 m:2" :x-gap="16" responsive="screen" style="margin-top: 8px;">
            <n-grid-item>
              <n-form-item label="AppID">
                <n-input v-model:value="paymentForm.wechat.app_id" placeholder="小程序/公众号 AppID" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="AppSecret">
                <n-input v-model:value="paymentForm.wechat.app_secret" type="password" placeholder="小程序/公众号 AppSecret" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="商户号 MchID">
                <n-input v-model:value="paymentForm.wechat.mch_id" placeholder="微信支付商户号" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="API V3 密钥">
                <n-input v-model:value="paymentForm.wechat.api_v3_key" type="password" placeholder="微信支付 V3 API 密钥" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="证书序列号">
                <n-input v-model:value="paymentForm.wechat.cert_serial_no" placeholder="微信支付商户证书序列号" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="通知 URL">
                <n-input v-model:value="paymentForm.wechat.notify_url" placeholder="建议使用上方生成的回调地址" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-form-item label="商户私钥证书（apiclient_key.pem）">
                <div class="cert-upload-row">
                  <n-upload
                    :show-file-list="false"
                    accept=".pem,.key,.txt,.crt"
                    :custom-request="(options: UploadCustomRequestOptions) => handleCertUpload('wechat', options)"
                  >
                    <n-button type="primary" secondary :loading="wechatCertUploading">上传证书文件</n-button>
                  </n-upload>
                  <n-tag v-if="paymentForm.wechat_cert_uploaded" type="success" size="small">已上传商户私钥</n-tag>
                  <n-tag v-else type="warning" size="small">未上传</n-tag>
                </div>
              </n-form-item>
            </n-grid-item>
          </n-grid>
        </template>

        <n-alert v-else-if="modalChannel === 'alipay'" type="info">
          支付宝支付暂未开放，开放后将在此提供配置入口。
        </n-alert>

        <n-alert v-else-if="modalChannel === 'cash'" type="success">
          货到付款无需配置参数，开通后 C 端按渠道顺序展示。
        </n-alert>

        <template #footer>
          <n-space justify="end">
            <n-button @click="showConfigModal = false">关闭</n-button>
            <n-button
              v-if="modalChannel === 'wechat'"
              type="primary"
              :loading="paymentSaving"
              :disabled="!authStore.hasPermission('system:update')"
              @click="saveWechatConfig"
            >
              保存配置
            </n-button>
          </n-space>
        </template>
      </n-modal>
    </n-spin>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'PaymentSetting' })
import { computed, onMounted, reactive, ref } from 'vue'
import { useMessage } from 'naive-ui'
import type { UploadCustomRequestOptions } from 'naive-ui'
import { getPaymentSetting, updatePaymentSetting, uploadPaymentCert } from '@/api/admin'
import type { PaymentSetting, PaymentSuggestedURLs } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'

/**
 * 支付配置页面
 * 卡片式渠道管理：卡片展示支付方式与配置状态，点击弹窗修改配置；
 * 开关/顺序调整即时保存；支付宝暂未开放，卡片显示禁用态（配置区块保留便于后续开放）
 */
const message = useMessage()
const authStore = useAuthStore()

const paymentLoading = ref(false)
const paymentSaving = ref(false)
const wechatCertUploading = ref(false)

interface ChannelCardItem {
  value: string
  label: string
  /** 是否可开通（支付宝暂未开放） */
  available: boolean
}

/** 渠道卡片定义（支付宝禁用展示；后续开放时将 available 置 true 即恢复完整交互） */
const channelCards: ChannelCardItem[] = [
  { value: 'wechat', label: '微信支付', available: true },
  { value: 'alipay', label: '支付宝', available: false },
  { value: 'cash', label: '货到付款', available: true },
]

/** 卡片渲染顺序：启用的渠道按展示顺序排前，未启用/不可用渠道排在后面 */
const sortedCards = computed<ChannelCardItem[]>(() => {
  const enabled = enabledOrder.value
    .map((v) => channelCards.find((c) => c.value === v))
    .filter((c): c is ChannelCardItem => !!c)
  const rest = channelCards.filter((c) => !enabledOrder.value.includes(c.value))
  return [...enabled, ...rest]
})

const paymentForm = reactive({
  wechat: {
    app_id: '',
    app_secret: '',
    mch_id: '',
    api_v3_key: '',
    cert_serial_no: '',
    private_key: '',
    notify_url: '',
  },
  alipay: {
    app_id: '',
    private_key: '',
    alipay_public_key: '',
    notify_url: '',
    return_url: '',
  },
  wechat_payment_ready: false,
  alipay_ready: false,
  wechat_cert_uploaded: false,
  alipay_cert_uploaded: false,
  updated_at: '',
  suggested_urls: {
    base_url: '',
    wechat_notify_url: '',
    alipay_notify_url: '',
  } as PaymentSuggestedURLs,
})

/** 已启用渠道的顺序（数组顺序即 C 端收银台展示顺序，保存时写入 enabled_channels） */
const enabledOrder = ref<string[]>([])

/** 配置弹窗 */
const showConfigModal = ref(false)
const modalChannel = ref('')
const modalTitle = computed(() => channelCards.find((c) => c.value === modalChannel.value)?.label ?? '')

/**
 * 渠道在启用序列中的序号（0 起）
 * @param value 渠道标识
 */
function channelOrder(value: string): number {
  return enabledOrder.value.indexOf(value)
}

/**
 * 渠道是否满足启用条件：微信须配置就绪，其余可用渠道默认可启用
 * @param value 渠道标识
 */
function channelReady(value: string): boolean {
  if (value === 'wechat') return paymentForm.wechat_payment_ready
  return true
}

/**
 * 开关禁用条件：不可用渠道禁用；未配置的渠道禁止启用（已启用的异常存量仍允许关闭）
 * @param ch 渠道卡片项
 */
function switchDisabled(ch: ChannelCardItem): boolean {
  if (!ch.available) return true
  if (ch.value === 'wechat' && !paymentForm.wechat_payment_ready && !enabledOrder.value.includes(ch.value)) {
    return true
  }
  return false
}

/**
 * 点击卡片：可配置渠道打开配置弹窗，其余轻提示
 * @param ch 渠道卡片项
 */
function onCardClick(ch: ChannelCardItem) {
  if (!ch.available) {
    message.info('支付宝支付暂未开放，敬请期待')
    return
  }
  if (ch.value === 'cash') {
    message.info('货到付款无需配置参数')
    return
  }
  modalChannel.value = ch.value
  showConfigModal.value = true
}

/**
 * 开关渠道：启用追加到展示序列末尾，关闭则移除，并即时保存
 * @param value 渠道标识
 * @param on 是否启用
 */
function onChannelToggle(value: string, on: boolean) {
  if (on) {
    if (!channelReady(value)) {
      message.warning('该渠道尚未完成配置，请先点击卡片完成配置')
      return
    }
    enabledOrder.value = [...enabledOrder.value, value]
  } else {
    enabledOrder.value = enabledOrder.value.filter((v) => v !== value)
  }
  persistChannels()
}

/**
 * 调整渠道展示顺序，并即时保存
 * @param value 渠道标识
 * @param direction -1 上移，1 下移
 */
function moveChannel(value: string, direction: -1 | 1) {
  const idx = enabledOrder.value.indexOf(value)
  const next = idx + direction
  if (idx < 0 || next < 0 || next >= enabledOrder.value.length) return
  const order = [...enabledOrder.value]
  ;[order[idx], order[next]] = [order[next], order[idx]]
  enabledOrder.value = order
  persistChannels()
}

/**
 * 组装保存载荷（当前表单数据 + 渠道顺序）
 */
function buildPayload(): PaymentSetting {
  return {
    enabled_channels: [...enabledOrder.value],
    wechat: paymentForm.wechat,
    alipay: paymentForm.alipay,
    wechat_payment_ready: paymentForm.wechat_payment_ready,
    alipay_ready: paymentForm.alipay_ready,
    wechat_cert_uploaded: paymentForm.wechat_cert_uploaded,
    alipay_cert_uploaded: paymentForm.alipay_cert_uploaded,
    updated_at: paymentForm.updated_at,
    suggested_urls: paymentForm.suggested_urls,
  }
}

/**
 * 保存渠道开关/顺序（即时生效）
 */
async function persistChannels() {
  paymentSaving.value = true
  try {
    await updatePaymentSetting(buildPayload())
    message.success('支付配置已保存，保存后即时生效，无需重启服务')
  } catch (err) {
    // 保存失败回滚到服务端状态
    await loadPaymentSetting()
    throw err
  } finally {
    paymentSaving.value = false
  }
}

/**
 * 弹窗内保存微信支付配置，成功后刷新就绪状态并关闭弹窗
 */
async function saveWechatConfig() {
  paymentSaving.value = true
  try {
    await updatePaymentSetting(buildPayload())
    message.success('微信支付配置已保存')
    await loadPaymentSetting()
    showConfigModal.value = false
  } finally {
    paymentSaving.value = false
  }
}

/**
 * 加载支付配置
 */
async function loadPaymentSetting() {
  paymentLoading.value = true
  try {
    const res = await getPaymentSetting()
    const data = res.data.data
    // 支付宝暂未开放：存量配置中的 alipay 不参与启用顺序
    enabledOrder.value = data.enabled_channels.filter((ch: string) => ch !== 'alipay')
    paymentForm.wechat = data.wechat
    paymentForm.alipay = data.alipay
    paymentForm.wechat_payment_ready = data.wechat_payment_ready
    paymentForm.alipay_ready = data.alipay_ready
    paymentForm.wechat_cert_uploaded = data.wechat_cert_uploaded
    paymentForm.alipay_cert_uploaded = data.alipay_cert_uploaded
    paymentForm.updated_at = data.updated_at
    paymentForm.suggested_urls = data.suggested_urls
  } finally {
    paymentLoading.value = false
  }
}

/**
 * 上传支付证书文件
 * 上传成功后刷新配置以更新证书上传状态
 * @param channel 支付渠道：wechat / alipay
 * @param options naive-ui 自定义上传请求参数
 */
async function handleCertUpload(channel: string, options: UploadCustomRequestOptions) {
  const file = options.file.file
  if (!file) {
    return
  }

  wechatCertUploading.value = true
  try {
    const res = await uploadPaymentCert(channel, 'private_key', file)
    message.success(res.data.message || '证书上传成功')
    options.onFinish()
    await loadPaymentSetting()
  } catch (err) {
    options.onError()
    throw err
  } finally {
    wechatCertUploading.value = false
  }
}

/**
 * 复制回调地址到剪贴板
 * @param url 回调地址
 */
async function copyURL(url: string) {
  try {
    await navigator.clipboard.writeText(url)
    message.success('已复制到剪贴板')
  } catch {
    message.error('复制失败，请手动复制')
  }
}

onMounted(() => {
  loadPaymentSetting()
})
</script>

<style lang="scss" scoped>
.channel-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 14px;
  margin-bottom: 8px;
}

.channel-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 16px;
  border: 1px solid #eee;
  border-radius: 10px;
  cursor: pointer;
  transition: all 0.2s;

  &.available:hover {
    border-color: #07c160;
    box-shadow: 0 2px 10px rgba(7, 193, 96, 0.08);
  }

  &:not(.available) {
    background: #fafafa;
    cursor: not-allowed;

    .card-name {
      color: #aaa;
    }
  }

  &.inactive.available {
    background: #fcfcfc;

    .card-name {
      color: #999;
    }
  }
}

.card-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.card-name {
  font-size: 15px;
  font-weight: 600;
  color: #333;
}

.card-status {
  min-height: 22px;
  display: flex;
  align-items: center;
}

.status-text {
  font-size: 13px;

  &.ok {
    color: #18a058;
  }

  &.warn {
    color: #f0a020;
  }
}

.card-ops {
  display: flex;
  align-items: center;
  gap: 4px;

  .switch-holder {
    display: inline-flex;
    margin-right: 6px;
  }

  .n-button {
    padding: 0 6px;
  }
}

.form-hint {
  display: block;
  font-size: 12px;
}

.url-guide-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;

  .n-text {
    flex: 1;
    word-break: break-all;
  }
}

.cert-upload-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}
</style>
