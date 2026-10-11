<template>
  <n-card title="支付配置">
    <n-spin :show="paymentLoading">
      <n-form :model="paymentForm" label-placement="top" class="payment-form">
        <n-form-item label="支付渠道开关与顺序">
          <n-dynamic-input
            v-model:value="paymentForm.enabled_channels"
            :min="1"
            :max="3"
            :on-create="createChannelItem"
          >
            <template #default="{ value, index }">
              <div class="channel-row">
                <n-select v-model:value="value.channel" :options="channelOptions" style="flex: 1;" />
                <n-button quaternary type="primary" :disabled="index === 0" @click="moveChannel(index, -1)">上移</n-button>
                <n-button quaternary type="primary" :disabled="index === paymentForm.enabled_channels.length - 1" @click="moveChannel(index, 1)">下移</n-button>
              </div>
            </template>
          </n-dynamic-input>
        </n-form-item>

        <n-divider>回调 URL 配置引导</n-divider>
        <n-alert type="info" style="margin-bottom: 16px;">
          请将下方回调地址配置到对应支付平台，配置后支付结果才能通知到本系统。
          地址由当前后台访问域名自动生成；若通过反向代理部署，请确认地址对外可访问。
        </n-alert>
        <n-descriptions label-placement="top" bordered :columns="1" style="margin-bottom: 24px;">
          <n-descriptions-item label="微信支付回调地址（配置到微信商户平台 → API安全 / 支付通知 URL）">
            <div class="url-guide-row">
              <n-text code>{{ paymentForm.suggested_urls.wechat_notify_url }}</n-text>
              <n-button size="small" type="primary" secondary @click="copyURL(paymentForm.suggested_urls.wechat_notify_url)">复制</n-button>
            </div>
          </n-descriptions-item>
          <n-descriptions-item label="支付宝支付回调地址（配置到支付宝开放平台 → 应用网关 / 异步通知地址）">
            <div class="url-guide-row">
              <n-text code>{{ paymentForm.suggested_urls.alipay_notify_url }}</n-text>
              <n-button size="small" type="primary" secondary @click="copyURL(paymentForm.suggested_urls.alipay_notify_url)">复制</n-button>
            </div>
          </n-descriptions-item>
        </n-descriptions>

        <n-divider>微信支付配置</n-divider>
        <n-alert v-if="!paymentForm.wechat_payment_ready" type="warning" title="微信支付未就绪" style="margin-bottom: 16px;">
          当前微信支付配置不完整，C 端不会展示微信支付选项
        </n-alert>
        <n-alert v-else type="success" title="微信支付已就绪" style="margin-bottom: 16px;">
          微信支付配置完整，C 端可正常展示
        </n-alert>
        <n-grid cols="1 m:2" :x-gap="16" responsive="screen">
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
              <n-input v-model:value="paymentForm.wechat.notify_url" placeholder="微信支付结果通知 URL，建议使用上方生成的回调地址" />
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

        <n-divider>支付宝支付配置（已关闭）</n-divider>
        <n-alert type="info" title="支付宝支付保持关闭" style="margin-bottom: 16px;">
          支付宝支付当前仅保存配置，不会对外启用
        </n-alert>
        <n-grid cols="1 m:2" :x-gap="16" responsive="screen">
          <n-grid-item>
            <n-form-item label="AppID">
              <n-input v-model:value="paymentForm.alipay.app_id" placeholder="支付宝 AppID" disabled />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="通知 URL">
              <n-input v-model:value="paymentForm.alipay.notify_url" placeholder="支付宝支付结果通知 URL，建议使用上方生成的回调地址" disabled />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="支付宝公钥">
              <n-input v-model:value="paymentForm.alipay.alipay_public_key" type="textarea" :rows="4" placeholder="支付宝公钥" disabled />
            </n-form-item>
          </n-grid-item>
        </n-grid>

        <n-space>
          <n-button type="primary" :loading="paymentSaving" :disabled="!authStore.hasPermission('system:update')" @click="savePaymentSetting">保存配置</n-button>
        </n-space>
      </n-form>
    </n-spin>
  </n-card>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useMessage } from 'naive-ui'
import type { UploadCustomRequestOptions } from 'naive-ui'
import { getPaymentSetting, updatePaymentSetting, uploadPaymentCert } from '@/api/admin'
import type { PaymentSetting, PaymentSuggestedURLs } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'

/**
 * 支付配置页面
 * 维护支付渠道开关与顺序、微信/支付宝支付参数及商户证书
 * 配置保存后即时生效，无需重启服务
 */
const message = useMessage()
const authStore = useAuthStore()

const paymentLoading = ref(false)
const paymentSaving = ref(false)
const wechatCertUploading = ref(false)

interface PaymentChannelItem {
  channel: string
}

const paymentForm = reactive({
  enabled_channels: [{ channel: 'cash' }] as PaymentChannelItem[],
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

/** 支付渠道可选项，支付宝保持关闭 */
const channelOptions = [
  { label: '微信支付', value: 'wechat' },
  { label: '货到付款', value: 'cash' },
]

/**
 * 创建新的支付渠道项
 */
function createChannelItem() {
  return { channel: 'cash' }
}

/**
 * 移动支付渠道顺序
 * @param index 当前索引
 * @param direction 移动方向，-1 上移，1 下移
 */
function moveChannel(index: number, direction: number) {
  const newIndex = index + direction
  if (newIndex < 0 || newIndex >= paymentForm.enabled_channels.length) {
    return
  }
  const temp = paymentForm.enabled_channels[index]
  paymentForm.enabled_channels[index] = paymentForm.enabled_channels[newIndex]
  paymentForm.enabled_channels[newIndex] = temp
}

/**
 * 加载支付配置
 */
async function loadPaymentSetting() {
  paymentLoading.value = true
  try {
    const res = await getPaymentSetting()
    const data = res.data.data
    paymentForm.enabled_channels = data.enabled_channels
      .filter((ch: string) => ch !== 'alipay')
      .map((ch: string) => ({ channel: ch }))
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

/**
 * 保存支付配置
 */
async function savePaymentSetting() {
  paymentSaving.value = true
  try {
    const payload: PaymentSetting = {
      enabled_channels: paymentForm.enabled_channels.map((item) => item.channel),
      wechat: paymentForm.wechat,
      alipay: paymentForm.alipay,
      wechat_payment_ready: paymentForm.wechat_payment_ready,
      alipay_ready: paymentForm.alipay_ready,
      wechat_cert_uploaded: paymentForm.wechat_cert_uploaded,
      alipay_cert_uploaded: paymentForm.alipay_cert_uploaded,
      updated_at: paymentForm.updated_at,
      suggested_urls: paymentForm.suggested_urls,
    }
    await updatePaymentSetting(payload)
    message.success('支付配置已保存，保存后即时生效，无需重启服务')
    await loadPaymentSetting()
  } finally {
    paymentSaving.value = false
  }
}

onMounted(() => {
  loadPaymentSetting()
})
</script>

<style lang="scss" scoped>
.payment-form {
  max-width: 860px;
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

.channel-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}

.cert-upload-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}
</style>
