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
            地址由「站点设置」的部署域名自动派生并在保存时写入配置，本页无需填写；
            若通过反向代理部署，请确认该地址对外可访问。
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
                <n-input v-model:value="paymentForm.wechat.api_v3_key" type="password" :disabled="!canUpdate" placeholder="微信支付 V3 API 密钥（留空保留已配置值）" />
                <n-tag v-if="paymentForm.wechat_api_v3_configured" type="success" size="small">已配置</n-tag>
                <n-tag v-else type="warning" size="small">未配置</n-tag>
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="证书序列号">
                <n-input v-model:value="paymentForm.wechat.cert_serial_no" :disabled="!canUpdate" placeholder="微信支付商户证书序列号" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-form-item label="商户私钥证书（apiclient_key.pem）">
                <div class="cert-field">
                  <div class="cert-upload-row">
                    <n-upload
                      :show-file-list="false"
                      accept=".pem,.key,.txt,.crt"
                      :disabled="!canUpdate"
                      :custom-request="(options: UploadCustomRequestOptions) => handleCertUpload('wechat', 'private_key', options)"
                    >
                      <n-button type="primary" secondary :disabled="!canUpdate" :loading="certUploading['wechat:private_key']">上传证书文件</n-button>
                    </n-upload>
                    <n-tag v-if="paymentForm.wechat_cert_uploaded" type="success" size="small">已上传商户私钥</n-tag>
                    <n-tag v-else type="warning" size="small">未上传</n-tag>
                  </div>
                  <div class="cert-paste-row">
                    <n-input
                      v-model:value="certDraft['wechat:private_key']"
                      type="textarea"
                      :rows="2"
                      :disabled="!canUpdate"
                      placeholder="或粘贴证书内容（含 BEGIN/END 行），留空表示不修改"
                    />
                    <n-button
                      size="small"
                      secondary
                      :disabled="!canUpdate || !certDraft['wechat:private_key'].trim()"
                      :loading="certUploading['wechat:private_key']"
                      @click="saveCertDraft('wechat', 'private_key', '商户私钥证书')"
                    >写入粘贴内容</n-button>
                  </div>
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
            开放平台（应用详情 → 开发设置 → 接口加签方式）的加签方式只有「密钥」和「证书」两种，
            下方选项已按官方文档全称与平台叫法一一对应，所选方式必须与开放平台保持一致：
            <strong>公钥模式</strong>（平台选「密钥」）需填写应用私钥与支付宝公钥；
            <strong>公钥证书模式</strong>（平台选「证书」）需录入应用公钥证书、支付宝公钥证书与支付宝根证书（应用私钥仍单独录入）。
            方式不匹配会导致签名或验签失败。
            证书与私钥可上传文件，也可直接粘贴内容。
            异步通知地址无需在开放平台配置：本系统每次下单都随请求携带 notify_url，
            该地址由「站点设置」的部署域名派生，只需保证部署域名公网可达。
          </n-alert>

          <n-grid cols="1 m:2" :x-gap="16" responsive="screen" style="margin-top: 8px;">
            <n-grid-item :span="2">
              <n-form-item label="加签方式">
                <div class="sign-mode-row">
                  <n-radio-group
                    :value="paymentForm.alipay.mode || 'public'"
                    :disabled="!canUpdate"
                    @update:value="switchAlipaySignMode"
                  >
                    <n-radio v-for="m in ALIPAY_SIGN_MODES" :key="m.value" :value="m.value" style="margin-right: 16px;">
                      {{ m.label }}
                    </n-radio>
                  </n-radio-group>
                  <n-text depth="3" class="form-hint">
                    括号内为支付宝开放平台控制台的叫法，须与其「接口加签方式」所选一致。两种方式的必填项不同，切换后需补齐对应配置并保存；
                    必填项未齐全时 C 端不会展示支付宝支付。
                  </n-text>
                </div>
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-form-item label="支付宝 AppID">
                <n-input v-model:value="paymentForm.alipay.app_id" :disabled="!canUpdate" placeholder="开放平台应用 AppID" />
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
              <n-text depth="3" class="form-hint">
                通知与回跳地址一律由「站点设置」的部署域名派生，并随下单请求逐笔带给支付宝，无需在任何平台手工配置，故不再展示；
                改域名请到站点设置页。回跳仅作体验用途，支付结果一律以异步通知与订单状态为准。
              </n-text>
            </n-grid-item>
            <n-grid-item v-if="!isAlipayCertMode" :span="2">
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
            <n-grid-item v-if="!isAlipayCertMode" :span="2">
              <n-text depth="3" class="form-hint">
                公钥模式只用到这一项公钥；若开放平台选的是「证书」，请把上方加签方式改为「公钥证书模式」并录入三份证书，
                把「支付宝公钥证书」内容粘到此处不会生效。
              </n-text>
            </n-grid-item>
            <template v-if="isAlipayCertMode">
              <n-grid-item v-for="cert in ALIPAY_CERT_ITEMS" :key="cert.type" :span="2">
                <n-form-item :label="cert.label">
                  <div class="cert-field">
                    <div class="cert-upload-row">
                      <n-upload
                        :show-file-list="false"
                        accept=".pem,.crt,.key,.txt"
                        :disabled="!canUpdate"
                        :custom-request="(options: UploadCustomRequestOptions) => handleCertUpload('alipay', cert.type, options)"
                      >
                        <n-button type="primary" secondary :disabled="!canUpdate" :loading="certUploading['alipay:' + cert.type]">
                          上传{{ cert.label }}
                        </n-button>
                      </n-upload>
                      <n-tag v-if="certUploaded[cert.key]" type="success" size="small">已上传</n-tag>
                      <n-tag v-else type="warning" size="small">未上传</n-tag>
                      <n-text depth="3" class="form-hint">{{ cert.hint }}</n-text>
                    </div>
                    <div class="cert-paste-row">
                      <n-input
                        v-model:value="certDraft['alipay:' + cert.type]"
                        type="textarea"
                        :rows="2"
                        :disabled="!canUpdate"
                        placeholder="或粘贴证书内容（含 BEGIN/END 行），留空表示不修改"
                      />
                      <n-button
                        size="small"
                        secondary
                        :disabled="!canUpdate || !certDraft['alipay:' + cert.type].trim()"
                        :loading="certUploading['alipay:' + cert.type]"
                        @click="saveCertDraft('alipay', cert.type, cert.label)"
                      >写入粘贴内容</n-button>
                    </div>
                  </div>
                </n-form-item>
              </n-grid-item>
            </template>
            <n-grid-item :span="2">
              <n-form-item :label="isAlipayCertMode ? '应用私钥（与证书同批生成）' : '应用私钥（接口加签私钥）'">
                <div class="cert-field">
                  <div class="cert-upload-row">
                    <n-upload
                      :show-file-list="false"
                      accept=".pem,.key,.txt,.crt"
                      :disabled="!canUpdate"
                      :custom-request="(options: UploadCustomRequestOptions) => handleCertUpload('alipay', 'private_key', options)"
                    >
                      <n-button type="primary" secondary :disabled="!canUpdate" :loading="certUploading['alipay:private_key']">上传私钥文件</n-button>
                    </n-upload>
                    <n-tag v-if="paymentForm.alipay_cert_uploaded" type="success" size="small">已上传应用私钥</n-tag>
                    <n-tag v-else type="warning" size="small">未上传</n-tag>
                  </div>
                  <div class="cert-paste-row">
                    <n-input
                      v-model:value="certDraft['alipay:private_key']"
                      type="textarea"
                      :rows="2"
                      :disabled="!canUpdate"
                      placeholder="或粘贴私钥内容（含 BEGIN/END 行），留空表示不修改"
                    />
                    <n-button
                      size="small"
                      secondary
                      :disabled="!canUpdate || !certDraft['alipay:private_key'].trim()"
                      :loading="certUploading['alipay:private_key']"
                      @click="saveCertDraft('alipay', 'private_key', '应用私钥')"
                    >写入粘贴内容</n-button>
                  </div>
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
import { refreshOnActivated } from '@/utils/activateRefresh'
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
/** 证书上传中状态，键为「渠道:证书类型」，使各上传按钮独立展示 loading */
const certUploading = reactive<Record<string, boolean>>({})

/**
 * 证书/私钥「粘贴内容」草稿，键为「渠道:证书类型」，与 certUploading 同键
 * 证书内容读取接口不回传明文，草稿框始终为空，仅作写入入口
 */
const certDraft = reactive<Record<string, string>>({
  'wechat:private_key': '',
  'alipay:private_key': '',
  'alipay:app_cert': '',
  'alipay:alipay_public_cert': '',
  'alipay:root_cert': '',
})

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
    mode: 'public' as string,
    alipay_public_key: '',
    notify_url: '',
    return_url: '',
    gateway_url: '',
  },
  wechat_payment_ready: false,
  alipay_ready: false,
  alipay_ready_message: '',
  wechat_api_v3_configured: false,
  wechat_cert_uploaded: false,
  alipay_cert_uploaded: false,
  alipay_app_cert_uploaded: false,
  alipay_public_cert_uploaded: false,
  alipay_root_cert_uploaded: false,
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
 * 支付宝加签方式选项，值与服务端 config.AlipayMode* 常量一致
 * 主名取自支付宝官方文档（公钥模式 / 公钥证书模式），括号内为开放平台控制台的实际选项名，
 * 避免管理员在两处对照时选错方式
 */
const ALIPAY_SIGN_MODES = [
  { value: 'public', label: '公钥模式（密钥）' },
  { value: 'cert', label: '公钥证书模式（证书）' },
]

/**
 * 支付宝证书模式需上传的三份证书定义
 * type 为上传接口的 cert_type，key 为配置读取接口返回的「已上传」标志字段，
 * 与服务端 payment 服务 UploadPayCert 的 case 一一对应
 */
const ALIPAY_CERT_ITEMS = [
  {
    type: 'app_cert',
    key: 'alipay_app_cert_uploaded',
    label: '应用公钥证书',
    hint: '开放平台「应用公钥证书」页签下载，文件名形如 appCertPublicKey_你的AppID.crt',
  },
  {
    type: 'alipay_public_cert',
    key: 'alipay_public_cert_uploaded',
    label: '支付宝公钥证书',
    hint: '开放平台「支付宝公钥证书」页签下载，文件名形如 alipayCertPublicKey_RSA2.crt，用于回调验签',
  },
  {
    type: 'root_cert',
    key: 'alipay_root_cert_uploaded',
    label: '支付宝根证书',
    hint: '开放平台「根证书」页签下载，文件名形如 alipayRootCert.crt，可含多段证书',
  },
] as const

/** 是否处于证书模式（存量空值按公钥模式处理，与服务端口径一致） */
const isAlipayCertMode = computed(() => paymentForm.alipay.mode === 'cert')

/** 证书上传状态映射，键与 ALIPAY_CERT_ITEMS 的 key（API 响应字段名）一致，供「已上传」标签与必填校验取值 */
const certUploaded = computed<Record<string, boolean>>(() => ({
  alipay_app_cert_uploaded: paymentForm.alipay_app_cert_uploaded,
  alipay_public_cert_uploaded: paymentForm.alipay_public_cert_uploaded,
  alipay_root_cert_uploaded: paymentForm.alipay_root_cert_uploaded,
}))

/**
 * 切换支付宝加签方式
 * 仅改动表单值，需点击「保存配置」后才写入服务端；两种方式必填项不同，
 * 切换后未补齐对应配置时服务端按「未就绪」处理，C 端不展示支付宝
 * @param mode 目标加签方式
 */
function switchAlipaySignMode(mode: string) {
  paymentForm.alipay.mode = mode
  message.info(mode === 'cert'
    ? '已切换为公钥证书模式，请上传或粘贴应用公钥证书、支付宝公钥证书与支付宝根证书后保存'
    : '已切换为公钥模式，请填写支付宝公钥后保存')
}

/**
 * 支付宝配置状态三态判定
 * - ready：服务端判定就绪（当前加签方式必填项齐全且密钥自检通过）
 * - invalid：必填项都已填写但自检未通过（如私钥粘错、把应用公钥当成支付宝公钥填、证书传错位置）
 * - incomplete：仍有必填项未填写
 * notify_url 不在页面展示（由站点域名派生、下单时随请求携带），但服务端仍将其列为必填，
 * 故此处保留判定：未派生时必须归入 incomplete，否则会误报为「必填项均已填写但自检未通过」
 */
const alipayStatus = computed<{ level: 'ready' | 'invalid' | 'incomplete'; reason: string }>(() => {
  if (paymentForm.alipay_ready) return { level: 'ready', reason: '' }
  const common = !!paymentForm.alipay.app_id && !!paymentForm.alipay.notify_url && paymentForm.alipay_cert_uploaded
  const filled = isAlipayCertMode.value
    ? common && ALIPAY_CERT_ITEMS.every((cert) => certUploaded.value[cert.key])
    : common && !!paymentForm.alipay.alipay_public_key
  if (!filled) return { level: 'incomplete', reason: paymentForm.alipay_ready_message }
  return { level: 'invalid', reason: paymentForm.alipay_ready_message }
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
 * 确认后必须按服务端真实值回滚表单：只关弹窗不清改动的话，未保存的编辑（尤其是切换过的加签方式）
 * 会静默留在共享表单里，之后任意一次即时保存（渠道开关、上移/下移）都会把它一并写库
 */
function requestCloseModal() {
  if (snapshotOf(modalChannel.value) !== formSnapshot.value) {
    dialog.warning({
      title: '配置尚未保存',
      content: '当前渠道配置有未保存的修改，关闭弹窗后修改将丢失。',
      positiveText: '放弃修改并关闭',
      negativeText: '继续编辑',
      onPositiveClick: async () => {
        showConfigModal.value = false
        // 回滚用的重读失败时不阻断关闭，失败原因已由请求层统一提示
        await fetchPaymentSetting().catch(() => {})
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
 * 只做格式与必填项的本地预检（服务端另有兜底：通知地址格式非法会拒绝整行保存），
 * 内容是否可解析由服务端自检判定
 * @returns 未通过的提示文案，全部通过返回空串
 */
function validateAlipayForm(): string {
  const urlPattern = /^https?:\/\/\S+$/
  const { gateway_url: gateway } = paymentForm.alipay
  if (gateway && !urlPattern.test(gateway)) {
    return '网关地址必须是 http/https 开头的绝对地址'
  }
  if (isAlipayCertMode.value) {
    const missing = ALIPAY_CERT_ITEMS.filter((cert) => !certUploaded.value[cert.key]).map((cert) => cert.label)
    if (missing.length) {
      return `公钥证书模式还需上传或粘贴：${missing.join('、')}`
    }
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
  // 存量配置加签方式可能为空串，统一按公钥模式展示（与服务端兼容口径一致）
  paymentForm.alipay = { ...data.alipay, mode: data.alipay.mode || 'public' }
  paymentForm.wechat_payment_ready = data.wechat_payment_ready
  paymentForm.alipay_ready = data.alipay_ready
  paymentForm.alipay_ready_message = data.alipay_ready_message ?? ''
  paymentForm.wechat_api_v3_configured = data.wechat_api_v3_configured ?? false
  paymentForm.wechat_cert_uploaded = data.wechat_cert_uploaded
  paymentForm.alipay_cert_uploaded = data.alipay_cert_uploaded
  paymentForm.alipay_app_cert_uploaded = data.alipay_app_cert_uploaded ?? false
  paymentForm.alipay_public_cert_uploaded = data.alipay_public_cert_uploaded ?? false
  paymentForm.alipay_root_cert_uploaded = data.alipay_root_cert_uploaded ?? false
  paymentForm.updated_at = data.updated_at
  paymentForm.wechat_client_channels = data.wechat_client_channels ?? []
  paymentForm.alipay_client_channels = data.alipay_client_channels ?? []
  paymentForm.suggested_urls = data.suggested_urls
}

/**
 * 仅回写服务端的就绪/上传状态与乐观锁版本号
 * 证书写入接口是即时落库的，但表单里往往还有未保存的编辑（刚切换的加签方式、已填未存的 AppID），
 * 整份重读会把这些覆盖回服务端旧值，表现为「证书上传成功但表单跳回公钥模式」，故此处只刷状态位
 */
async function refreshUploadStatus() {
  const data = (await getPaymentSetting()).data.data
  paymentForm.wechat_payment_ready = data.wechat_payment_ready
  paymentForm.alipay_ready = data.alipay_ready
  paymentForm.alipay_ready_message = data.alipay_ready_message ?? ''
  paymentForm.wechat_api_v3_configured = data.wechat_api_v3_configured ?? false
  paymentForm.wechat_cert_uploaded = data.wechat_cert_uploaded
  paymentForm.alipay_cert_uploaded = data.alipay_cert_uploaded
  paymentForm.alipay_app_cert_uploaded = data.alipay_app_cert_uploaded ?? false
  paymentForm.alipay_public_cert_uploaded = data.alipay_public_cert_uploaded ?? false
  paymentForm.alipay_root_cert_uploaded = data.alipay_root_cert_uploaded ?? false
  paymentForm.updated_at = data.updated_at
}

/**
 * 上传支付证书文件
 * 上传成功后刷新证书上传状态；服务端会校验内容可解析性，
 * 校验失败时不写库并返回具体原因（请求层统一提示）
 * @param channel 支付渠道：wechat / alipay
 * @param certType 证书类型：private_key、app_cert、alipay_public_cert、root_cert
 * @param options naive-ui 自定义上传请求参数
 */
async function handleCertUpload(channel: string, certType: string, options: UploadCustomRequestOptions) {
  const file = options.file.file
  if (!file) {
    return
  }

  const loadingKey = `${channel}:${certType}`
  certUploading[loadingKey] = true
  try {
    const res = await uploadPaymentCert(channel, certType, file)
    message.success(res.data.message || '证书上传成功')
    options.onFinish()
    await refreshUploadStatus()
  } catch {
    options.onError()
  } finally {
    certUploading[loadingKey] = false
  }
}

/**
 * 以粘贴内容写入支付证书/私钥
 * 把文本包成 .pem 文件复用上传接口，服务端扩展名白名单、大小上限与内容可解析性校验口径与文件上传完全一致；
 * 校验失败时服务端不写库（原配置保持不变），具体原因由请求层统一提示，此时保留草稿内容便于修正
 * @param channel 支付渠道：wechat / alipay
 * @param certType 证书类型：private_key、app_cert、alipay_public_cert、root_cert
 * @param label 字段名称，用于提示文案
 */
async function saveCertDraft(channel: string, certType: string, label: string) {
  const key = `${channel}:${certType}`
  const content = certDraft[key].trim()
  if (!content) {
    message.warning(`请先粘贴${label}内容`)
    return
  }

  certUploading[key] = true
  try {
    const file = new File([content], `${certType}.pem`, { type: 'application/x-pem-file' })
    const res = await uploadPaymentCert(channel, certType, file)
    message.success(res.data.message || `${label}已保存`)
    certDraft[key] = ''
    await refreshUploadStatus()
  } catch {
    // 失败原因已由请求层提示，此处仅保留草稿内容
  } finally {
    certUploading[key] = false
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

// 首屏与「切回本页」共用同一组加载：支付配置 + 渠道勾选项
// keep-alive 缓存下再次进入只触发 onActivated，不重新拉取会带着旧值整份 PUT 覆盖他人改动
function loadAll() {
  loadPaymentSetting()
  // 勾选项数据源：渠道管理维护的客户端渠道；加载失败不阻断支付配置页
  getClientChannels()
    .then((res) => {
      clientChannels.value = res.data.data.channels
    })
    .catch(() => {
      clientChannels.value = []
    })
}

onMounted(loadAll)
refreshOnActivated(loadAll)
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
  flex-wrap: wrap;
}

/* 证书/私钥录入位：上传行 + 粘贴行上下排列 */
.cert-field {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.cert-paste-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  width: 100%;
  flex-wrap: wrap;

  .n-input {
    flex: 1;
    /* 窄弹窗下按钮换到下一行，避免输入框被挤到不可用 */
    min-width: 220px;
  }

  .n-button {
    margin-top: 4px;
    flex-shrink: 0;
  }
}

.sign-mode-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
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
