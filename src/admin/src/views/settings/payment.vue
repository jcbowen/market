<template>
  <n-card title="支付配置">
    <n-spin :show="paymentLoading">
      <n-alert type="info" style="margin-bottom: 16px;">
        支付配置按「行」维护：同一平台可创建多条配置（如「支付宝-H5」与「支付宝-小程序」），每条独立启停。
        各客户端渠道（渠道管理）按 <n-text code>平台:配置ID</n-text> 绑定使用哪一条，绑定顺序即该端收银台的展示顺序。
        只有「启用且就绪」的配置才会出现在渠道绑定候选与 C 端收银台。
      </n-alert>

      <div class="list-toolbar">
        <n-button type="primary" :disabled="!canUpdate" @click="openCreate">＋新增支付配置</n-button>
      </div>
      <n-alert v-if="!canUpdate" type="default" class="readonly-alert">
        当前账号无支付配置修改权限，页面为只读：可查看各配置状态，但新增、编辑、启停、删除与证书上传均已禁用。
      </n-alert>

      <!-- 按平台分组：微信支付 / 支付宝 -->
      <div v-for="group in groups" :key="group.platform" class="platform-group">
        <div class="group-title">
          <span class="group-name">{{ group.label }}</span>
          <n-tag size="small" :bordered="false" :type="group.platform === 'wechat' ? 'success' : 'info'">
            {{ group.rows.length }} 条配置
          </n-tag>
        </div>

        <div v-if="group.rows.length === 0" class="group-empty">
          暂无{{ group.label }}配置，点击右上角「＋新增支付配置」创建。
        </div>

        <div v-else class="config-cards">
          <div v-for="row in group.rows" :key="row.id" class="config-card" :class="{ disabled: !row.enabled }">
            <div class="card-main">
              <div class="card-top">
                <span class="card-name">{{ row.name }}</span>
                <n-tag size="small" :bordered="false" :type="group.platform === 'wechat' ? 'success' : 'info'">
                  {{ group.label }}
                </n-tag>
                <n-tag v-if="row.platform === 'alipay' && row.alipay_gateway_url.includes('openapi-sandbox.')" size="small" type="warning" :bordered="false">沙箱</n-tag>
              </div>
              <div class="card-status">
                <span v-if="!row.enabled" class="status-text muted">已停用</span>
                <span v-else-if="row.ready" class="status-text ok">已就绪</span>
                <n-tooltip v-else trigger="hover" placement="top">
                  <template #trigger>
                    <span class="status-text warn">未就绪</span>
                  </template>
                  {{ row.ready_message || '必填配置不完整，C 端不会展示该支付方式' }}
                </n-tooltip>
              </div>
              <div class="card-desc">{{ rowDesc(row) }}</div>
            </div>
            <div class="card-ops">
              <span class="switch-holder" @click.stop>
                <n-switch
                  size="small"
                  :value="row.enabled"
                  :disabled="!canUpdate"
                  @update:value="(on: boolean) => onToggle(row, on)"
                />
              </span>
              <n-button quaternary size="tiny" :disabled="!canUpdate" @click.stop="openEdit(row)">编辑</n-button>
              <n-button quaternary size="tiny" type="error" :disabled="!canUpdate" @click.stop="onDelete(row)">删除</n-button>
            </div>
          </div>
        </div>
      </div>

      <!-- 配置编辑弹窗：新增 / 编辑共用 -->
      <n-modal
        :show="showModal"
        preset="card"
        :title="modalTitle"
        style="width: 720px; max-width: 94vw"
        @update:show="onModalShowChange"
      >
        <n-form label-placement="left" label-width="150">
          <n-form-item label="配置名称" required>
            <n-input v-model:value="form.name" :disabled="!canUpdate" placeholder="如「支付宝-H5」「微信支付-主力」" />
          </n-form-item>
          <n-form-item label="平台类型" required>
            <n-radio-group :value="form.platform" :disabled="!canUpdate || !isNewRow" @update:value="(p: 'wechat' | 'alipay') => (form.platform = p)">
              <n-radio value="wechat" style="margin-right: 16px;">微信支付</n-radio>
              <n-radio value="alipay">支付宝</n-radio>
            </n-radio-group>
            <n-text v-if="!isNewRow" depth="3" class="form-hint">平台类型创建后不可修改；同平台可建多条配置</n-text>
          </n-form-item>
          <n-form-item label="启用">
            <n-switch v-model:value="form.enabled" :disabled="!canUpdate" />
            <n-text depth="3" class="form-hint">停用后渠道绑定仍保留，但该配置不会出现在收银台</n-text>
          </n-form-item>

          <!-- 微信支付凭证 -->
          <template v-if="form.platform === 'wechat'">
            <n-divider title-placement="left" style="margin: 8px 0 16px;">微信支付凭证</n-divider>
            <n-form-item label="商户号 MchID">
              <n-input v-model:value="form.wechat_mch_id" :disabled="!canUpdate" placeholder="微信支付商户号" />
              <n-text depth="3" class="form-hint">一个商户号一条配置即可；AppID 请到渠道管理按渠道填写，并在商户号下完成 AppID 关联</n-text>
            </n-form-item>
            <n-form-item label="API V3 密钥">
              <n-input
                v-model:value="form.wechat_api_v3_key"
                type="password"
                :disabled="!canUpdate"
                placeholder="微信支付 V3 API 密钥（留空保留已配置值）"
              />
              <n-tag v-if="form.wechat_api_v3_configured" type="success" size="small">已配置</n-tag>
              <n-tag v-else type="warning" size="small">未配置</n-tag>
            </n-form-item>
            <n-form-item label="证书序列号">
              <n-input v-model:value="form.wechat_cert_serial_no" :disabled="!canUpdate" placeholder="微信支付商户证书序列号" />
            </n-form-item>
            <n-form-item label="商户私钥证书（apiclient_key.pem）">
              <div class="cert-field">
                <div class="cert-upload-row">
                  <n-upload
                    :show-file-list="false"
                    accept=".pem,.key,.txt,.crt"
                    :disabled="!canUpdate || form.id === 0"
                    :custom-request="(options: UploadCustomRequestOptions) => handleCertUpload('private_key', options)"
                  >
                    <n-button type="primary" secondary :disabled="!canUpdate || form.id === 0" :loading="certUploading['private_key']">上传证书文件</n-button>
                  </n-upload>
                  <n-tag v-if="form.wechat_cert_uploaded" type="success" size="small">已上传商户私钥</n-tag>
                  <n-tag v-else type="warning" size="small">未上传</n-tag>
                  <n-text v-if="form.id === 0" depth="3" class="form-hint">新增配置请先保存，再上传证书</n-text>
                </div>
                <div class="cert-paste-row">
                  <n-input
                    v-model:value="certDraft['private_key']"
                    type="textarea"
                    :rows="2"
                    :disabled="!canUpdate || form.id === 0"
                    placeholder="或粘贴证书内容（含 BEGIN/END 行），留空表示不修改"
                  />
                  <n-button
                    size="small"
                    secondary
                    :disabled="!canUpdate || form.id === 0 || !certDraft['private_key'].trim()"
                    :loading="certUploading['private_key']"
                    @click="saveCertDraft('private_key', '商户私钥证书')"
                  >写入粘贴内容</n-button>
                </div>
              </div>
            </n-form-item>
            <n-form-item label="异步通知地址">
              <div class="url-guide-row">
                <n-text code>{{ form.wechat_notify_url || suggestedURLs.wechat_notify_url }}</n-text>
                <n-button size="small" type="primary" secondary @click="copyURL(form.wechat_notify_url || suggestedURLs.wechat_notify_url)">复制</n-button>
              </div>
              <n-text depth="3" class="form-hint">
                由「站点设置」的部署域名自动派生并在保存时写入，请将其配置到微信商户平台（API 安全 → 支付通知 URL）
              </n-text>
            </n-form-item>
          </template>

          <!-- 支付宝凭证 -->
          <template v-else>
            <n-divider title-placement="left" style="margin: 8px 0 16px;">支付宝凭证</n-divider>
            <n-form-item label="加签方式">
              <div class="sign-mode-row">
                <n-radio-group :value="form.alipay_mode || 'public'" :disabled="!canUpdate" @update:value="switchAlipaySignMode">
                  <n-radio v-for="m in ALIPAY_SIGN_MODES" :key="m.value" :value="m.value" style="margin-right: 16px;">
                    {{ m.label }}
                  </n-radio>
                </n-radio-group>
                <n-text depth="3" class="form-hint">
                  括号内为支付宝开放平台控制台的叫法，须与其「接口加签方式」所选一致；方式不匹配会导致签名或验签失败
                </n-text>
              </div>
            </n-form-item>
            <n-form-item label="支付宝 AppID">
              <n-input v-model:value="form.alipay_app_id" :disabled="!canUpdate" placeholder="开放平台应用 AppID" />
            </n-form-item>
            <n-form-item label="网关地址（留空 = 生产环境）">
              <div class="gateway-row">
                <n-input v-model:value="form.alipay_gateway_url" :disabled="!canUpdate" placeholder="https://openapi.alipay.com，留空即生产环境" />
                <n-button size="small" secondary :disabled="!canUpdate" @click="fillAlipayGateway('')">切生产</n-button>
                <n-button size="small" secondary :disabled="!canUpdate" @click="fillAlipayGateway(ALIPAY_SANDBOX_GATEWAY)">切沙箱</n-button>
              </div>
              <n-text depth="3" class="form-hint">
                当前指向：{{ form.alipay_gateway_url.includes('openapi-sandbox.') ? '沙箱环境（非真实资金）' : '生产环境' }}。
                沙箱与生产是两套独立的凭证，切换后必须重新填写并上传；地址只需填到域名（含端口）。
              </n-text>
            </n-form-item>
            <n-form-item v-if="!isAlipayCertMode" label="支付宝公钥">
              <n-input
                v-model:value="form.alipay_public_key"
                type="textarea"
                :rows="3"
                :disabled="!canUpdate"
                placeholder="开放平台「接口加签方式」中查看的支付宝公钥内容（注意不是本应用的公钥），用于回调验签"
              />
            </n-form-item>
            <template v-if="isAlipayCertMode">
              <n-form-item v-for="cert in ALIPAY_CERT_ITEMS" :key="cert.type" :label="cert.label">
                <div class="cert-field">
                  <div class="cert-upload-row">
                    <n-upload
                      :show-file-list="false"
                      accept=".pem,.crt,.key,.txt"
                      :disabled="!canUpdate || form.id === 0"
                      :custom-request="(options: UploadCustomRequestOptions) => handleCertUpload(cert.type, options)"
                    >
                      <n-button type="primary" secondary :disabled="!canUpdate || form.id === 0" :loading="certUploading[cert.type]">
                        上传{{ cert.label }}
                      </n-button>
                    </n-upload>
                    <n-tag v-if="certUploaded[cert.key]" type="success" size="small">已上传</n-tag>
                    <n-tag v-else type="warning" size="small">未上传</n-tag>
                    <n-text depth="3" class="form-hint">{{ cert.hint }}</n-text>
                  </div>
                  <div class="cert-paste-row">
                    <n-input
                      v-model:value="certDraft[cert.type]"
                      type="textarea"
                      :rows="2"
                      :disabled="!canUpdate || form.id === 0"
                      placeholder="或粘贴证书内容（含 BEGIN/END 行），留空表示不修改"
                    />
                    <n-button
                      size="small"
                      secondary
                      :disabled="!canUpdate || form.id === 0 || !certDraft[cert.type].trim()"
                      :loading="certUploading[cert.type]"
                      @click="saveCertDraft(cert.type, cert.label)"
                    >写入粘贴内容</n-button>
                  </div>
                </div>
              </n-form-item>
            </template>
            <n-form-item :label="isAlipayCertMode ? '应用私钥（与证书同批生成）' : '应用私钥（接口加签私钥）'">
              <div class="cert-field">
                <div class="cert-upload-row">
                  <n-upload
                    :show-file-list="false"
                    accept=".pem,.key,.txt,.crt"
                    :disabled="!canUpdate || form.id === 0"
                    :custom-request="(options: UploadCustomRequestOptions) => handleCertUpload('private_key', options)"
                  >
                    <n-button type="primary" secondary :disabled="!canUpdate || form.id === 0" :loading="certUploading['private_key']">上传私钥文件</n-button>
                  </n-upload>
                  <n-tag v-if="form.alipay_cert_uploaded" type="success" size="small">已上传应用私钥</n-tag>
                  <n-tag v-else type="warning" size="small">未上传</n-tag>
                  <n-text v-if="form.id === 0" depth="3" class="form-hint">新增配置请先保存，再上传私钥</n-text>
                </div>
                <div class="cert-paste-row">
                  <n-input
                    v-model:value="certDraft['private_key']"
                    type="textarea"
                    :rows="2"
                    :disabled="!canUpdate || form.id === 0"
                    placeholder="或粘贴私钥内容（含 BEGIN/END 行），留空表示不修改"
                  />
                  <n-button
                    size="small"
                    secondary
                    :disabled="!canUpdate || form.id === 0 || !certDraft['private_key'].trim()"
                    :loading="certUploading['private_key']"
                    @click="saveCertDraft('private_key', '应用私钥')"
                  >写入粘贴内容</n-button>
                </div>
              </div>
            </n-form-item>
            <n-form-item label="异步通知地址">
              <div class="url-guide-row">
                <n-text code>{{ form.alipay_notify_url || suggestedURLs.alipay_notify_url }}</n-text>
                <n-button size="small" type="primary" secondary @click="copyURL(form.alipay_notify_url || suggestedURLs.alipay_notify_url)">复制</n-button>
              </div>
              <n-text depth="3" class="form-hint">
                由「站点设置」的部署域名派生并随下单逐笔携带，无需在开放平台配置；改域名请到站点设置页
              </n-text>
            </n-form-item>
          </template>
        </n-form>

        <template #footer>
          <n-space justify="end">
            <n-button @click="requestCloseModal">关闭</n-button>
            <n-button type="primary" :loading="paymentSaving" :disabled="!canUpdate" @click="saveRow">保存配置</n-button>
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
import {
  getPaymentConfigs,
  createPayConfig,
  updatePayConfig,
  deletePayConfig,
  uploadPaymentCert,
} from '@/api/admin'
import type { PayConfigRow, PaymentSuggestedURLs } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'

/**
 * 支付配置页面（pay_config 多行制）
 * 按平台分组展示配置行（名称/就绪状态/启用开关），新增与编辑共用弹窗；
 * 渠道绑定哪一行在「渠道管理」维护，本页只负责凭证与启停
 */
const message = useMessage()
const dialog = useDialog()
const authStore = useAuthStore()

/** 支付宝沙箱网关地址（仅到域名，SDK 自行拼接 /gateway.do 与 V3 路径） */
const ALIPAY_SANDBOX_GATEWAY = 'https://openapi-sandbox.dl.alipaydev.com'

/** 支付宝加签方式选项，值与服务端 config.AlipayMode* 常量一致 */
const ALIPAY_SIGN_MODES = [
  { value: 'public', label: '公钥模式（密钥）' },
  { value: 'cert', label: '公钥证书模式（证书）' },
]

/** 支付宝证书模式需上传的三份证书定义 */
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

const paymentLoading = ref(false)
const paymentSaving = ref(false)
/** 配置行列表（服务端按固定顺序返回，此处按平台分组渲染） */
const rows = ref<PayConfigRow[]>([])
/** 按部署域名派生的回调地址建议 */
const suggestedURLs = ref<PaymentSuggestedURLs>({ base_url: '', wechat_notify_url: '', alipay_notify_url: '' })

/** 证书上传中状态，键为证书类型，使各上传按钮独立展示 loading */
const certUploading = reactive<Record<string, boolean>>({})

/** 证书/私钥「粘贴内容」草稿，键为证书类型；证书内容不回传明文，草稿框仅作写入入口 */
const certDraft = reactive<Record<string, string>>({
  private_key: '',
  app_cert: '',
  alipay_public_cert: '',
  root_cert: '',
})

/** 是否具备支付配置修改权限（无权限时整页只读，与服务端 settings:update 一致） */
const canUpdate = computed(() => authStore.isSuperAdmin || authStore.hasPermission('settings:update'))

/** 平台分组定义与渲染数据 */
const groups = computed(() => [
  { platform: 'wechat' as const, label: '微信支付', rows: rows.value.filter((r) => r.platform === 'wechat') },
  { platform: 'alipay' as const, label: '支付宝', rows: rows.value.filter((r) => r.platform === 'alipay') },
])

// —— 编辑弹窗 ——
const showModal = ref(false)
/** 打开弹窗时的表单快照，用于关闭前判断是否有未保存改动 */
const formSnapshot = ref('')
/** 编辑中的配置行（id=0 表示新增） */
const form = reactive<PayConfigRow>(emptyRow())

function emptyRow(): PayConfigRow {
  return {
    id: 0,
    name: '',
    platform: 'wechat',
    enabled: true,
    wechat_mch_id: '',
    wechat_api_v3_key: '',
    wechat_cert_serial_no: '',
    wechat_notify_url: '',
    alipay_app_id: '',
    alipay_mode: 'public',
    alipay_public_key: '',
    alipay_notify_url: '',
    alipay_return_url: '',
    alipay_gateway_url: '',
    wechat_cert_uploaded: false,
    alipay_cert_uploaded: false,
    alipay_app_cert_uploaded: false,
    alipay_public_cert_uploaded: false,
    alipay_root_cert_uploaded: false,
    updated_at: '',
  }
}

/** 是否新增（id=0）：新增行需先保存才能上传证书（上传接口按配置行 ID 定位） */
const isNewRow = computed(() => form.id === 0)

const modalTitle = computed(() => (isNewRow.value ? '新增支付配置' : `编辑支付配置 - ${form.name}`))

/** 是否处于证书模式 */
const isAlipayCertMode = computed(() => form.alipay_mode === 'cert')

/** 证书上传状态映射，键与 ALIPAY_CERT_ITEMS 的 key 一致，供「已上传」标签取值 */
const certUploaded = computed<Record<string, boolean>>(() => ({
  alipay_app_cert_uploaded: form.alipay_app_cert_uploaded,
  alipay_public_cert_uploaded: form.alipay_public_cert_uploaded,
  alipay_root_cert_uploaded: form.alipay_root_cert_uploaded,
}))

/**
 * 配置行卡片描述（一行简介）
 * @param row 配置行
 */
function rowDesc(row: PayConfigRow): string {
  if (row.platform === 'wechat') {
    return row.wechat_mch_id ? `商户号 ${row.wechat_mch_id}` : '未填写商户号'
  }
  const sandbox = row.alipay_gateway_url.includes('openapi-sandbox.')
  return [row.alipay_app_id, sandbox ? '沙箱网关' : ''].filter(Boolean).join(' · ') || '未填写商户凭证'
}

/**
 * 加载配置列表
 */
async function loadRows(showLoading = true) {
  if (showLoading) paymentLoading.value = true
  try {
    const res = await getPaymentConfigs()
    rows.value = res.data.data.list
    suggestedURLs.value = res.data.data.suggested_urls
  } finally {
    if (showLoading) paymentLoading.value = false
  }
}

/**
 * 打开新增弹窗
 */
function openCreate() {
  Object.assign(form, emptyRow())
  formSnapshot.value = snapshotOf()
  showModal.value = true
}

/**
 * 打开编辑弹窗（深拷贝，取消不污染列表）
 * @param row 配置行
 */
function openEdit(row: PayConfigRow) {
  Object.assign(form, emptyRow(), JSON.parse(JSON.stringify(row)))
  formSnapshot.value = snapshotOf()
  showModal.value = true
}

/**
 * 采集弹窗表单快照（凭证字段组 + 名称与启用状态），用于脏表单判断
 */
function snapshotOf(): string {
  return JSON.stringify({
    name: form.name,
    enabled: form.enabled,
    wechat: [form.wechat_mch_id, form.wechat_api_v3_key, form.wechat_cert_serial_no],
    alipay: [form.alipay_app_id, form.alipay_mode, form.alipay_public_key, form.alipay_gateway_url],
  })
}

/**
 * 弹窗显隐变化：有未保存改动时二次确认
 * @param next 期望的显示状态
 */
function onModalShowChange(next: boolean) {
  if (next) {
    showModal.value = true
    return
  }
  requestCloseModal()
}

/**
 * 请求关闭弹窗：表单与打开时快照不一致则二次确认，避免误丢改动
 */
function requestCloseModal() {
  if (snapshotOf() !== formSnapshot.value) {
    dialog.warning({
      title: '配置尚未保存',
      content: '当前配置有未保存的修改，关闭弹窗后修改将丢失。',
      positiveText: '放弃修改并关闭',
      negativeText: '继续编辑',
      onPositiveClick: () => {
        showModal.value = false
        loadRows(false)
      },
    })
    return
  }
  showModal.value = false
}

/**
 * 切换支付宝加签方式（仅改表单值，保存后生效）
 * @param mode 目标加签方式
 */
function switchAlipaySignMode(mode: 'public' | 'cert') {
  form.alipay_mode = mode
  message.info(mode === 'cert'
    ? '已切换为公钥证书模式，请上传或粘贴应用公钥证书、支付宝公钥证书与支付宝根证书后保存'
    : '已切换为公钥模式，请填写支付宝公钥后保存')
}

/**
 * 填入支付宝网关地址（已在 C 端启用时切换环境属高危操作，需二次确认）
 * @param gateway 网关地址，空串表示生产环境
 */
function fillAlipayGateway(gateway: string) {
  if (gateway === form.alipay_gateway_url) return
  const apply = () => {
    form.alipay_gateway_url = gateway
    message.info('网关地址已填入，需点击「保存配置」后生效')
  }
  if (!form.enabled) {
    apply()
    return
  }
  dialog.warning({
    title: '切换支付宝接入环境',
    content: '该配置已启用。沙箱与生产是两套独立的 AppID、应用私钥与支付宝公钥，'
      + '切换后需重新填写并上传，否则现网支付会直接失败。确认切换？',
    positiveText: '确认切换',
    negativeText: '取消',
    onPositiveClick: apply,
  })
}

/**
 * 组装保存载荷（剥离仅出参字段）
 */
function buildPayload(): Omit<PayConfigRow, 'id' | 'updated_at' | 'ready' | 'ready_message' | 'wechat_api_v3_configured'> {
  return {
    name: form.name.trim(),
    platform: form.platform,
    enabled: form.enabled,
    wechat_mch_id: form.wechat_mch_id.trim(),
    wechat_api_v3_key: form.wechat_api_v3_key,
    wechat_cert_serial_no: form.wechat_cert_serial_no.trim(),
    wechat_notify_url: '',
    alipay_app_id: form.alipay_app_id.trim(),
    alipay_mode: form.alipay_mode || 'public',
    alipay_public_key: form.alipay_public_key.trim(),
    alipay_notify_url: '',
    alipay_return_url: '',
    alipay_gateway_url: form.alipay_gateway_url.trim(),
    wechat_cert_uploaded: form.wechat_cert_uploaded,
    alipay_cert_uploaded: form.alipay_cert_uploaded,
    alipay_app_cert_uploaded: form.alipay_app_cert_uploaded,
    alipay_public_cert_uploaded: form.alipay_public_cert_uploaded,
    alipay_root_cert_uploaded: form.alipay_root_cert_uploaded,
  }
}

/**
 * 保存配置行（新增/更新），成功后刷新列表并关闭弹窗
 * 私钥/V3 密钥空值提交时服务端保留原值；版本冲突由服务端拒绝后整份重拉
 */
async function saveRow() {
  if (!form.name.trim()) {
    message.warning('配置名称不能为空')
    return
  }
  paymentSaving.value = true
  try {
    if (isNewRow.value) {
      await createPayConfig(buildPayload())
      message.success('支付配置已创建，如需上传证书请重新打开本配置')
    } else {
      await updatePayConfig(form.id, { ...buildPayload(), updated_at: form.updated_at })
      message.success('支付配置已保存')
    }
    showModal.value = false
    await loadRows()
  } finally {
    paymentSaving.value = false
  }
}

/**
 * 卡片启停开关：仅提交启用状态（携带版本号做乐观锁）
 * @param row 配置行
 * @param on 是否启用
 */
async function onToggle(row: PayConfigRow, on: boolean) {
  try {
    await updatePayConfig(row.id, { enabled: on, updated_at: row.updated_at })
    message.success(on ? '配置已启用' : '配置已停用')
  } catch {
    // 失败原因已由请求层提示，回滚到服务端状态
  } finally {
    await loadRows(false)
  }
}

/**
 * 删除配置行：二次确认；被渠道绑定引用时服务端拒绝并提示解除绑定
 * @param row 配置行
 */
function onDelete(row: PayConfigRow) {
  dialog.warning({
    title: '删除支付配置',
    content: `确定删除「${row.name}」？删除后引用它的渠道绑定将失效，需在渠道管理中重新配置支付方式。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      await deletePayConfig(row.id)
      message.success('支付配置已删除')
      await loadRows(false)
    },
  })
}

/**
 * 上传支付证书文件（按当前编辑的配置行 ID 定位）
 * @param certType 证书类型：private_key、app_cert、alipay_public_cert、root_cert
 * @param options naive-ui 自定义上传请求参数
 */
async function handleCertUpload(certType: string, options: UploadCustomRequestOptions) {
  const file = options.file.file
  if (!file) {
    return
  }
  const loadingKey = certType
  certUploading[loadingKey] = true
  try {
    const res = await uploadPaymentCert(form.id, certType, file)
    message.success(res.data.message || '证书上传成功')
    options.onFinish()
    // 上传即时落库：只刷新上传状态位，不覆盖弹窗内未保存的编辑
    await loadRows(false)
    const fresh = rows.value.find((r) => r.id === form.id)
    if (fresh) {
      form.wechat_cert_uploaded = fresh.wechat_cert_uploaded
      form.alipay_cert_uploaded = fresh.alipay_cert_uploaded
      form.alipay_app_cert_uploaded = fresh.alipay_app_cert_uploaded
      form.alipay_public_cert_uploaded = fresh.alipay_public_cert_uploaded
      form.alipay_root_cert_uploaded = fresh.alipay_root_cert_uploaded
    }
  } catch {
    options.onError()
  } finally {
    certUploading[loadingKey] = false
  }
}

/**
 * 以粘贴内容写入支付证书/私钥（复用上传接口，服务端校验口径一致）
 * @param certType 证书类型
 * @param label 字段名称，用于提示文案
 */
async function saveCertDraft(certType: string, label: string) {
  const key = certType
  const content = certDraft[key].trim()
  if (!content) {
    message.warning(`请先粘贴${label}内容`)
    return
  }
  certUploading[key] = true
  try {
    const file = new File([content], `${certType}.pem`, { type: 'application/x-pem-file' })
    const res = await uploadPaymentCert(form.id, certType, file)
    message.success(res.data.message || `${label}已保存`)
    certDraft[key] = ''
    await loadRows(false)
    const fresh = rows.value.find((r) => r.id === form.id)
    if (fresh) {
      form.wechat_cert_uploaded = fresh.wechat_cert_uploaded
      form.alipay_cert_uploaded = fresh.alipay_cert_uploaded
      form.alipay_app_cert_uploaded = fresh.alipay_app_cert_uploaded
      form.alipay_public_cert_uploaded = fresh.alipay_public_cert_uploaded
      form.alipay_root_cert_uploaded = fresh.alipay_root_cert_uploaded
    }
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

onMounted(() => loadRows())
refreshOnActivated(() => loadRows(false))
</script>

<style lang="scss" scoped>
.list-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 12px;
}

.readonly-alert {
  margin-bottom: 12px;
}

.platform-group {
  margin-bottom: 20px;
}

.group-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}

.group-name {
  font-size: 14px;
  font-weight: 600;
  color: #333;
}

.group-empty {
  padding: 18px 16px;
  border: 1px dashed #e5e5e5;
  border-radius: 10px;
  font-size: 13px;
  color: #999;
  text-align: center;
}

.config-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(380px, 1fr));
  gap: 12px;
}

.config-card {
  display: flex;
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid #eee;
  border-radius: 10px;
  transition: all 0.2s;

  &:hover {
    border-color: #18a058;
    box-shadow: 0 2px 10px rgba(24, 160, 88, 0.08);
  }

  &.disabled {
    background: #fcfcfc;

    .card-name {
      color: #999;
    }
  }
}

.card-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.card-top {
  display: flex;
  align-items: center;
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

  &.muted {
    color: #999;
  }
}

.card-desc {
  flex: 1;
  font-size: 12px;
  line-height: 1.6;
  color: #999;
  word-break: break-all;
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
  margin-top: 4px;
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
</style>
