<template>
  <n-card title="支付配置">
    <n-spin :show="paymentLoading">
      <!-- 渠道卡片：启用的在前按展示顺序排列，移动后卡片顺序即时重排 -->
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
            <n-tag v-if="ch.value === 'alipay' && alipayInSandbox" size="small" type="warning" :bordered="false">沙箱</n-tag>
          </div>
          <div class="card-status">
            <n-tag v-if="!ch.available" size="small" :bordered="false">暂未开放</n-tag>
            <span v-else-if="ch.value === 'cash'" class="status-text ok">无需配置</span>
            <span v-else-if="ch.value === 'wechat'" class="status-text" :class="paymentForm.wechat_payment_ready ? 'ok' : 'warn'">
              {{ paymentForm.wechat_payment_ready ? '已配置' : '未配置' }}
            </span>
            <span v-else-if="ch.value === 'alipay'" class="status-text" :class="paymentForm.alipay_ready ? 'ok' : 'warn'">
              {{ paymentForm.alipay_ready ? '已配置' : '未配置' }}
            </span>
            <span v-else class="status-text">—</span>
          </div>
          <div class="card-ops">
            <span class="switch-holder" @click.stop>
              <n-switch
                size="small"
                :value="ch.available && enabledOrder.includes(ch.value)"
                :disabled="switchDisabled(ch) || !canUpdate"
                @update:value="(on: boolean) => onChannelToggle(ch.value, on)"
              />
            </span>
            <template v-if="ch.available && enabledOrder.includes(ch.value)">
              <n-button
                quaternary
                size="tiny"
                :disabled="channelOrder(ch.value) === 0 || !canUpdate"
                @click.stop="moveChannel(ch.value, -1)"
              >上移</n-button>
              <n-button
                quaternary
                size="tiny"
                :disabled="channelOrder(ch.value) === enabledOrder.length - 1 || !canUpdate"
                @click.stop="moveChannel(ch.value, 1)"
              >下移</n-button>
            </template>
          </div>
        </div>
      </div>
      <n-text depth="3" class="form-hint">卡片从左到右的排列顺序即 C 端收银台的展示顺序，通过上移/下移调整；开关与顺序调整后即时保存生效。未配置的渠道需先点击卡片完成配置后再启用。</n-text>
      <n-alert v-if="!canUpdate" type="default" class="readonly-alert">
        当前账号无支付配置修改权限，页面为只读：可查看各渠道配置状态，但开关、顺序与配置保存均已禁用。
      </n-alert>

      <!-- 配置弹窗：微信/支付宝为完整表单；货到付款仅提示 -->
      <n-modal :show="showConfigModal" preset="card" :title="modalTitle" style="width: 680px; max-width: 92vw" @update:show="onModalShowChange">
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
            <n-grid-item :span="2">
              <n-form-item label="微信 AppID">
                <div class="appid-readonly-row">
                  <n-text v-if="paymentForm.wechat.app_id" code>{{ paymentForm.wechat.app_id }}</n-text>
                  <n-text v-else depth="3">未配置，请先在「渠道管理」完成小程序凭证配置</n-text>
                  <n-text depth="3" class="appid-hint">
                    AppID/AppSecret 统一在「渠道管理」维护，此处自动同步，无需重复填写
                  </n-text>
                </div>
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="商户号 MchID">
                <n-input v-model:value="paymentForm.wechat.mch_id" :disabled="!canUpdate" placeholder="微信支付商户号" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="API V3 密钥">
                <n-input v-model:value="paymentForm.wechat.api_v3_key" type="password" :disabled="!canUpdate" placeholder="微信支付 V3 API 密钥" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="证书序列号">
                <n-input v-model:value="paymentForm.wechat.cert_serial_no" :disabled="!canUpdate" placeholder="微信支付商户证书序列号" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="通知 URL">
                <n-input v-model:value="paymentForm.wechat.notify_url" :disabled="!canUpdate" placeholder="建议使用上方生成的回调地址" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-form-item label="商户私钥证书（apiclient_key.pem）">
                <div class="cert-upload-row">
                  <n-upload
                    :show-file-list="false"
                    accept=".pem,.key,.txt,.crt"
                    :disabled="!canUpdate"
                    :custom-request="(options: UploadCustomRequestOptions) => handleCertUpload('wechat', options)"
                  >
                    <n-button type="primary" secondary :disabled="!canUpdate" :loading="wechatCertUploading">上传证书文件</n-button>
                  </n-upload>
                  <n-tag v-if="paymentForm.wechat_cert_uploaded" type="success" size="small">已上传商户私钥</n-tag>
                  <n-tag v-else type="warning" size="small">未上传</n-tag>
                </div>
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-form-item label="适用客户端渠道">
                <div class="client-channel-box">
                  <n-checkbox-group v-model:value="paymentForm.wechat_client_channels" :disabled="!canUpdate">
                    <n-checkbox
                      v-for="opt in wechatChannelOptions"
                      :key="opt.code"
                      :value="opt.code"
                      :disabled="opt.disabled"
                      style="margin-right: 16px;"
                    >
                      {{ opt.label }}
                    </n-checkbox>
                  </n-checkbox-group>
                  <n-text depth="3" class="client-channel-hint">
                    勾选微信支付在哪些客户端渠道展示与可用；全部不勾选 = 所有已启用客户端渠道均可用（兼容口径）。
                    公众号需 OAuth 授权、App 需支付 SDK，这两类渠道服务端已判定不可用，故勾选框禁用
                    （已勾选的存量项仍可取消，便于纠正历史配置）。
                  </n-text>
                </div>
              </n-form-item>
            </n-grid-item>
          </n-grid>
        </template>

        <template v-else-if="modalChannel === 'alipay'">
          <n-alert v-if="alipayStatus.level === 'ready'" type="success" style="margin-bottom: 16px;">
            支付宝配置完整且密钥校验通过，C 端可正常展示
          </n-alert>
          <n-alert v-else-if="alipayStatus.level === 'invalid'" type="error" style="margin-bottom: 16px;">
            必填项均已填写，但配置校验未通过：C 端不会展示支付宝，用户下单也会失败。请按以下原因修正后重新保存——
            {{ alipayStatus.reason }}
          </n-alert>
          <n-alert v-else type="warning" style="margin-bottom: 16px;">
            当前支付宝支付配置不完整，C 端不会展示支付宝支付选项
          </n-alert>

          <n-alert type="info" style="margin-bottom: 16px;">
            请将下方异步通知地址配置到支付宝开放平台（应用详情 → 开发设置 → 接口加签与消息校验），
            加签方式选择「公钥模式」，将应用私钥与支付宝公钥填入下方对应项。配置后支付结果才能通知到本系统。
          </n-alert>
          <div class="url-guide-row">
            <n-text code>{{ paymentForm.suggested_urls.alipay_notify_url }}</n-text>
            <n-button size="small" type="primary" secondary @click="copyURL(paymentForm.suggested_urls.alipay_notify_url)">复制</n-button>
          </div>

          <n-grid cols="1 m:2" :x-gap="16" responsive="screen" style="margin-top: 8px;">
            <n-grid-item>
              <n-form-item label="支付宝 AppID">
                <n-input v-model:value="paymentForm.alipay.app_id" :disabled="!canUpdate" placeholder="开放平台应用 AppID" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="异步通知 URL">
                <n-input v-model:value="paymentForm.alipay.notify_url" :disabled="!canUpdate" placeholder="建议使用上方生成的回调地址" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-form-item label="网关地址（留空 = 生产环境）">
                <div class="gateway-row">
                  <n-input
                    v-model:value="paymentForm.alipay.gateway_url"
                    :disabled="!canUpdate"
                    placeholder="https://openapi.alipay.com，留空即生产环境"
                  />
                  <n-button size="small" secondary :disabled="!canUpdate" @click="fillAlipayGateway('')">切生产</n-button>
                  <n-button size="small" secondary :disabled="!canUpdate" @click="fillAlipayGateway(ALIPAY_SANDBOX_GATEWAY)">切沙箱</n-button>
                </div>
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-text depth="3" class="form-hint">
                当前指向：{{ alipayInSandbox ? '沙箱环境（非真实资金）' : '生产环境' }}。
                沙箱与生产是两套独立的 AppID、应用私钥与支付宝公钥，切换后必须重新填写并上传，混用会直接导致签名或验签失败；
                沙箱的异步通知同样由支付宝服务器发起，回调地址仍需公网可达。
                网关地址只需填到域名（含端口），带 /gateway.do 后缀时保存会自动去掉。
              </n-text>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-form-item label="支付完成回跳 URL">
                <n-input
                  v-model:value="paymentForm.alipay.return_url"
                  :disabled="!canUpdate"
                  placeholder="支付完成后浏览器回跳的 C 端 H5 地址，建议指向订单详情页，可留空"
                />
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-text depth="3" class="form-hint">
                回跳地址必须是浏览器可访问的绝对地址（http/https）。留空时用户在支付宝收银台完成支付后不会自动回到本站，
                需要手动切回浏览器查看订单，因此 C 端 H5 对外可访问时建议填写，例如
                {{ alipayReturnUrlSuggestion }}。回跳仅作体验用途，支付结果一律以异步通知与订单状态为准。
              </n-text>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-form-item label="支付宝公钥">
                <n-input
                  v-model:value="paymentForm.alipay.alipay_public_key"
                  type="textarea"
                  :rows="3"
                  :disabled="!canUpdate"
                  placeholder="开放平台「接口加签方式」中查看的支付宝公钥内容（注意不是本应用的公钥），用于回调验签"
                />
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-form-item label="应用私钥（接口加签私钥）">
                <div class="cert-upload-row">
                  <n-upload
                    :show-file-list="false"
                    accept=".pem,.key,.txt,.crt"
                    :disabled="!canUpdate"
                    :custom-request="(options: UploadCustomRequestOptions) => handleCertUpload('alipay', options)"
                  >
                    <n-button type="primary" secondary :disabled="!canUpdate" :loading="alipayCertUploading">上传私钥文件</n-button>
                  </n-upload>
                  <n-tag v-if="paymentForm.alipay_cert_uploaded" type="success" size="small">已上传应用私钥</n-tag>
                  <n-tag v-else type="warning" size="small">未上传</n-tag>
                </div>
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-form-item label="适用客户端渠道">
                <div class="client-channel-box">
                  <n-checkbox-group v-model:value="paymentForm.alipay_client_channels" :disabled="!canUpdate">
                    <n-checkbox
                      v-for="opt in alipayChannelOptions"
                      :key="opt.code"
                      :value="opt.code"
                      :disabled="opt.disabled"
                      style="margin-right: 16px;"
                    >
                      {{ opt.label }}
                    </n-checkbox>
                  </n-checkbox-group>
                  <n-text depth="3" class="client-channel-hint">
                    勾选支付宝支付在哪些客户端渠道展示与可用；全部不勾选 = 所有已启用客户端渠道均可用（兼容口径）。
                    微信小程序/公众号处于微信生态内无法唤起支付宝、App 支付 SDK 未接入，这三类渠道服务端已判定不可用，
                    故勾选框禁用（已勾选的存量项仍可取消，便于纠正历史配置）。
                  </n-text>
                </div>
              </n-form-item>
            </n-grid-item>
          </n-grid>
        </template>

        <n-alert v-else-if="modalChannel === 'cash'" type="success">
          货到付款无需配置参数，开通后 C 端按渠道顺序展示。
        </n-alert>

        <template #footer>
          <n-space justify="end">
            <n-button @click="requestCloseModal">关闭</n-button>
            <n-button
              v-if="modalChannel === 'wechat'"
              type="primary"
              :loading="paymentSaving"
              :disabled="!canUpdate"
              @click="saveWechatConfig"
            >
              保存配置
            </n-button>
            <n-button
              v-else-if="modalChannel === 'alipay'"
              type="primary"
              :loading="paymentSaving"
              :disabled="!canUpdate"
              @click="saveAlipayConfig"
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
import { useDialog, useMessage } from 'naive-ui'
import type { UploadCustomRequestOptions } from 'naive-ui'
import { getPaymentSetting, updatePaymentSetting, uploadPaymentCert, getClientChannels } from '@/api/admin'
import type { PaymentSetting, PaymentSuggestedURLs, ClientChannel } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'

/**
 * 支付配置页面
 * 卡片式渠道管理：卡片展示支付方式与配置状态，点击弹窗修改配置；
 * 开关/顺序调整即时保存
 */
const message = useMessage()
const dialog = useDialog()
const authStore = useAuthStore()

/** 支付宝沙箱网关地址（仅到域名，SDK 自行拼接 /gateway.do 与 V3 路径） */
const ALIPAY_SANDBOX_GATEWAY = 'https://openapi-sandbox.dl.alipaydev.com'

/**
 * 各支付渠道在客户端渠道上的能力不可用原因
 * 与服务端 gateway/handler/payment.go 的 wechatPayReadyOnClient / alipayPayReadyOnClient
 * 判定保持一致：服务端为最终门禁，前端禁用无效勾选项只是为了避免「勾了不生效」的误导
 */
const PAY_CHANNEL_UNSUPPORTED: Record<string, Record<string, string>> = {
  wechat: { gzh: '公众号 OAuth 链路未开放', app: 'App 支付 SDK 未接入' },
  alipay: { mp: '微信小程序内无法唤起支付宝', gzh: '公众号网页在微信生态内无法唤起支付宝', app: 'App 支付 SDK 未接入' },
}

const paymentLoading = ref(false)
const paymentSaving = ref(false)
const wechatCertUploading = ref(false)
const alipayCertUploading = ref(false)

interface ChannelCardItem {
  value: string
  label: string
  /** 是否可开通 */
  available: boolean
}

/** 渠道卡片定义 */
const channelCards: ChannelCardItem[] = [
  { value: 'wechat', label: '微信支付', available: true },
  { value: 'alipay', label: '支付宝', available: true },
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
    gateway_url: '',
  },
  wechat_payment_ready: false,
  alipay_ready: false,
  alipay_ready_message: '',
  wechat_cert_uploaded: false,
  alipay_cert_uploaded: false,
  updated_at: '',
  wechat_client_channels: [] as string[],
  alipay_client_channels: [] as string[],
  suggested_urls: {
    base_url: '',
    wechat_notify_url: '',
    alipay_notify_url: '',
  } as PaymentSuggestedURLs,
})

/** 客户端渠道勾选项（渠道管理已配置的数据源） */
const clientChannels = ref<ClientChannel[]>([])

/**
 * 生成某支付渠道的客户端渠道勾选项
 * 不可用渠道（能力矩阵内）禁用勾选并标注原因；已勾选的存量项保持可取消，
 * 避免禁用后无法纠正历史配置
 * @param payChannel 支付渠道：wechat / alipay
 */
function channelOptionsFor(payChannel: string) {
  const selected = payChannel === 'wechat' ? paymentForm.wechat_client_channels : paymentForm.alipay_client_channels
  return clientChannels.value.map((ch) => {
    const unsupported = PAY_CHANNEL_UNSUPPORTED[payChannel]?.[ch.code]
    const reasons = [
      ...(unsupported ? [unsupported] : []),
      ...(ch.enabled ? [] : ['未启用']),
    ]
    return {
      code: ch.code,
      label: reasons.length ? `${ch.name}（${reasons.join('，')}）` : ch.name,
      disabled: !ch.enabled || (!!unsupported && !selected.includes(ch.code)),
    }
  })
}

/** 微信支付适用客户端渠道勾选项 */
const wechatChannelOptions = computed(() => channelOptionsFor('wechat'))

/** 支付宝支付适用客户端渠道勾选项 */
const alipayChannelOptions = computed(() => channelOptionsFor('alipay'))

/** 已启用渠道的顺序（数组顺序即 C 端收银台展示顺序，保存时写入 enabled_channels） */
const enabledOrder = ref<string[]>([])

/** 配置弹窗 */
const showConfigModal = ref(false)
const modalChannel = ref('')
const modalTitle = computed(() => channelCards.find((c) => c.value === modalChannel.value)?.label ?? '')
/** 打开弹窗时的表单快照，用于关闭前判断是否有未保存改动 */
const formSnapshot = ref('')

/** 是否具备支付配置修改权限（无权限时整页只读，与服务端 settings:update 一致） */
const canUpdate = computed(() => authStore.isSuperAdmin || authStore.hasPermission('settings:update'))

/** 支付宝当前是否指向沙箱网关 */
const alipayInSandbox = computed(() => paymentForm.alipay.gateway_url.includes('openapi-sandbox.'))

/**
 * 支付宝配置状态三态判定
 * - ready：服务端判定就绪（字段齐全且密钥自检通过）
 * - invalid：必填项都已填写但自检未通过（如私钥粘错、把应用公钥当成支付宝公钥填）
 * - incomplete：仍有必填项未填写
 */
const alipayStatus = computed<{ level: 'ready' | 'invalid' | 'incomplete'; reason: string }>(() => {
  if (paymentForm.alipay_ready) return { level: 'ready', reason: '' }
  const filled =
    !!paymentForm.alipay.app_id &&
    !!paymentForm.alipay.alipay_public_key &&
    !!paymentForm.alipay.notify_url &&
    paymentForm.alipay_cert_uploaded
  if (!filled) return { level: 'incomplete', reason: paymentForm.alipay_ready_message }
  return { level: 'invalid', reason: paymentForm.alipay_ready_message }
})

/** 回跳地址建议值：C 端 H5 以 hash 路由部署在 /s/ 下，给出订单详情页形态供管理员照抄 */
const alipayReturnUrlSuggestion = computed(() => {
  const base = paymentForm.suggested_urls.base_url
  return base ? `${base}/s/#/pages/order/detail?id=订单ID` : 'https://你的C端域名/s/#/pages/order/detail?id=订单ID'
})

/**
 * 渠道在启用序列中的序号（0 起）
 * @param value 渠道标识
 */
function channelOrder(value: string): number {
  return enabledOrder.value.indexOf(value)
}

/**
 * 渠道是否满足启用条件：微信/支付宝须配置就绪，其余可用渠道默认可启用
 * @param value 渠道标识
 */
function channelReady(value: string): boolean {
  if (value === 'wechat') return paymentForm.wechat_payment_ready
  if (value === 'alipay') return paymentForm.alipay_ready
  return true
}

/**
 * 开关禁用条件：不可用渠道禁用；未配置的渠道禁止启用（已启用的异常存量仍允许关闭）
 * @param ch 渠道卡片项
 */
function switchDisabled(ch: ChannelCardItem): boolean {
  if (!ch.available) return true
  const notReady =
    (ch.value === 'wechat' && !paymentForm.wechat_payment_ready) ||
    (ch.value === 'alipay' && !paymentForm.alipay_ready)
  if (notReady && !enabledOrder.value.includes(ch.value)) {
    return true
  }
  return false
}

/**
 * 采集当前弹窗渠道的表单快照（含适用客户端渠道勾选），用于脏表单判断
 * @param channel 渠道标识
 */
function snapshotOf(channel: string): string {
  if (channel === 'wechat') {
    return JSON.stringify({ config: paymentForm.wechat, channels: paymentForm.wechat_client_channels })
  }
  return JSON.stringify({ config: paymentForm.alipay, channels: paymentForm.alipay_client_channels })
}

/**
 * 点击卡片：可配置渠道打开配置弹窗并记录快照，其余轻提示
 * @param ch 渠道卡片项
 */
function onCardClick(ch: ChannelCardItem) {
  if (ch.value === 'cash') {
    message.info('货到付款无需配置参数')
    return
  }
  modalChannel.value = ch.value
  formSnapshot.value = snapshotOf(ch.value)
  showConfigModal.value = true
}

/**
 * 弹窗显隐变化（遮罩关闭 / ESC / 右上角关闭）：有未保存改动时二次确认
 * @param next 期望的显示状态
 */
function onModalShowChange(next: boolean) {
  if (next) {
    showConfigModal.value = true
    return
  }
  requestCloseModal()
}

/**
 * 请求关闭配置弹窗：表单与打开时快照不一致则二次确认，避免误丢改动
 */
function requestCloseModal() {
  if (snapshotOf(modalChannel.value) !== formSnapshot.value) {
    dialog.warning({
      title: '配置尚未保存',
      content: '当前渠道配置有未保存的修改，关闭弹窗后修改将丢失。',
      positiveText: '放弃修改并关闭',
      negativeText: '继续编辑',
      onPositiveClick: () => {
        showConfigModal.value = false
      },
    })
    return
  }
  showConfigModal.value = false
}

/**
 * 填入支付宝网关地址
 * 支付宝已在 C 端启用时切换环境属高危操作（沙箱与生产是两套凭证，切换立即对现网生效），
 * 需二次确认；未启用则直接填入
 * @param gateway 网关地址，空串表示生产环境
 */
function fillAlipayGateway(gateway: string) {
  if (gateway === paymentForm.alipay.gateway_url) return

  const apply = () => {
    paymentForm.alipay.gateway_url = gateway
    message.info('网关地址已填入，需点击「保存配置」后生效')
  }
  if (!enabledOrder.value.includes('alipay')) {
    apply()
    return
  }
  dialog.warning({
    title: '切换支付宝接入环境',
    content: '支付宝已在 C 端启用。沙箱与生产是两套独立的 AppID、应用私钥与支付宝公钥，'
      + '切换后需重新填写并上传，否则现网支付会直接失败。确认切换？',
    positiveText: '确认切换',
    negativeText: '取消',
    onPositiveClick: apply,
  })
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
 * 组装保存载荷（当前表单数据 + 渠道顺序 + 读取时的版本号）
 * updated_at 为服务端乐观锁版本：提交被拒绝或成功后都会重新拉取，保证下一次提交携带最新版本
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
    wechat_client_channels: [...paymentForm.wechat_client_channels],
    alipay_client_channels: [...paymentForm.alipay_client_channels],
    suggested_urls: paymentForm.suggested_urls,
  }
}

/**
 * 提交支付配置并同步服务端状态
 * 无论成功失败都重新拉取配置：成功是为了刷新 updated_at 版本号（服务端每次保存都会推进），
 * 失败是回滚本地改动到服务端真实状态（错误提示由请求层统一弹出）
 * @returns 是否保存成功
 */
async function submitPaymentSetting(): Promise<boolean> {
  paymentSaving.value = true
  try {
    await updatePaymentSetting(buildPayload())
    await fetchPaymentSetting()
    return true
  } catch {
    await fetchPaymentSetting()
    return false
  } finally {
    paymentSaving.value = false
  }
}

/**
 * 保存渠道开关/顺序（即时生效）
 */
async function persistChannels() {
  if (await submitPaymentSetting()) {
    message.success('支付配置已保存，保存后即时生效，无需重启服务')
  }
}

/**
 * 校验支付宝配置项格式
 * @returns 未通过的提示文案，全部通过返回空串
 */
function validateAlipayForm(): string {
  const urlPattern = /^https?:\/\/\S+$/
  const { return_url: returnUrl, gateway_url: gateway } = paymentForm.alipay
  if (returnUrl && !urlPattern.test(returnUrl)) {
    return '支付完成回跳 URL 必须是 http/https 开头的绝对地址'
  }
  if (gateway && !urlPattern.test(gateway)) {
    return '网关地址必须是 http/https 开头的绝对地址'
  }
  return ''
}

/**
 * 弹窗内保存微信支付配置，成功后刷新就绪状态并关闭弹窗
 */
async function saveWechatConfig() {
  if (await submitPaymentSetting()) {
    message.success('微信支付配置已保存')
    formSnapshot.value = snapshotOf('wechat')
    if (paymentForm.wechat_payment_ready) {
      showConfigModal.value = false
      return
    }
    message.warning('已保存，但微信支付配置仍不完整，C 端不会展示微信支付')
  }
}

/**
 * 弹窗内保存支付宝支付配置
 * 保存后若自检仍未通过，保持弹窗打开并展示服务端返回的失败原因，避免管理员以为已配好
 */
async function saveAlipayConfig() {
  const invalid = validateAlipayForm()
  if (invalid) {
    message.error(invalid)
    return
  }
  if (await submitPaymentSetting()) {
    formSnapshot.value = snapshotOf('alipay')
    if (paymentForm.alipay_ready) {
      message.success('支付宝支付配置已保存，校验通过')
      showConfigModal.value = false
      return
    }
    message.warning('已保存，但配置校验未通过，C 端不会展示支付宝：' + paymentForm.alipay_ready_message)
  }
}

/**
 * 加载支付配置（带页面 loading）
 */
async function loadPaymentSetting() {
  paymentLoading.value = true
  try {
    await fetchPaymentSetting()
  } finally {
    paymentLoading.value = false
  }
}

/**
 * 拉取支付配置写入表单（不触发页面 loading，供保存后静默刷新版本号与就绪状态）
 */
async function fetchPaymentSetting() {
  const res = await getPaymentSetting()
  const data = res.data.data
  enabledOrder.value = data.enabled_channels
  paymentForm.wechat = data.wechat
  paymentForm.alipay = data.alipay
  paymentForm.wechat_payment_ready = data.wechat_payment_ready
  paymentForm.alipay_ready = data.alipay_ready
  paymentForm.alipay_ready_message = data.alipay_ready_message ?? ''
  paymentForm.wechat_cert_uploaded = data.wechat_cert_uploaded
  paymentForm.alipay_cert_uploaded = data.alipay_cert_uploaded
  paymentForm.updated_at = data.updated_at
  paymentForm.wechat_client_channels = data.wechat_client_channels ?? []
  paymentForm.alipay_client_channels = data.alipay_client_channels ?? []
  paymentForm.suggested_urls = data.suggested_urls
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

  const uploading = channel === 'wechat' ? wechatCertUploading : alipayCertUploading
  uploading.value = true
  try {
    const res = await uploadPaymentCert(channel, 'private_key', file)
    message.success(res.data.message || '证书上传成功')
    options.onFinish()
    await loadPaymentSetting()
  } catch (err) {
    options.onError()
    throw err
  } finally {
    uploading.value = false
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
  // 勾选项数据源：渠道管理维护的客户端渠道；加载失败不阻断支付配置页
  getClientChannels()
    .then((res) => {
      clientChannels.value = res.data.data.channels
    })
    .catch(() => {
      clientChannels.value = []
    })
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

.readonly-alert {
  margin-bottom: 12px;
}

.gateway-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;

  .n-input {
    flex: 1;
  }
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

.appid-readonly-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 100%;
}

.appid-hint {
  font-size: 12px;
}

.cert-upload-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}

.client-channel-box {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
}

.client-channel-hint {
  font-size: 12px;
  line-height: 1.6;
}
</style>
