<template>
  <n-card title="渠道管理">
    <n-spin :show="loading">
      <n-alert type="info" style="margin-bottom: 16px;">
        客户端渠道是各端的访问凭证（AppID/AppSecret）与启用开关：小程序登录、公众号 OAuth、按端支付下单均取用此处配置。
        每个渠道可绑定启用的支付方式（含货到付款），绑定顺序即该端收银台的展示顺序；
        凭证与绑定保存后即时生效，无需重启服务。
      </n-alert>

      <div class="channel-cards">
        <div
          v-for="ch in channels"
          :key="ch.code"
          class="channel-card"
          :class="{ inactive: !ch.enabled, disabled: isDisabledChannel(ch.code) }"
          @click="openConfig(ch)"
        >
          <div class="card-main">
            <div class="card-top">
              <span class="card-name">{{ ch.name }}</span>
              <n-tag size="small" :bordered="false" :type="codeTagType(ch.code)">{{ codeLabel(ch.code) }}</n-tag>
            </div>
            <div class="card-status">
              <span v-if="isDisabledChannel(ch.code)" class="status-text muted">暂不支持</span>
              <span v-else-if="(ch.code === 'h5' || ch.code === 'pos') && !bindsWechatPay(ch.pay_methods)" class="status-text ok">无需配置凭证</span>
              <span v-else class="status-text" :class="ch.configured ? 'ok' : 'warn'">
                {{ ch.configured ? '已配置' : '未配置' }}
              </span>
            </div>
            <div class="card-desc">{{ channelDesc(ch.code) }}</div>
            <div class="card-paymethods">
              <n-tag
                v-for="m in ch.pay_methods"
                :key="m"
                size="tiny"
                :bordered="false"
                :disabled="!ch.enabled"
                class="pm-tag"
              >
                {{ payMethodLabel(m) }}
              </n-tag>
              <span v-if="!ch.pay_methods?.length" class="pm-empty">未绑定支付方式</span>
            </div>
            <n-text v-if="!ch.configured && bindsWechatPay(ch.pay_methods)" depth="3" class="form-hint">
              绑定微信支付需填写本渠道 AppID，且该 AppID 须已在支付配置对应的商户号下完成关联
            </n-text>
            <div class="card-ops">
              <span class="switch-holder" @click.stop>
                <n-switch
                  size="small"
                  :value="ch.enabled"
                  :disabled="isDisabledChannel(ch.code) || (!ch.configured && !ch.enabled)"
                  @update:value="(on: boolean) => onToggle(ch, on)"
                />
              </span>
              <n-button quaternary size="tiny" :disabled="isDisabledChannel(ch.code)" @click.stop="openConfig(ch)">配置</n-button>
            </div>
          </div>

          <!-- 右侧扫码访问区：所有渠道统一占位，H5 出二维码，其余展示差异化说明避免突兀 -->
          <div class="scan-zone" @click.stop>
            <template v-if="scanMode(ch.code) === 'qr'">
              <img
                :src="h5QrDataUrl"
                class="scan-qr"
                alt="H5 站点二维码"
                title="点击放大"
                @click="qrModalVisible = true"
              />
              <span class="scan-hint">扫码访问</span>
            </template>
            <template v-else>
              <span class="scan-text">{{ scanHint(ch.code) }}</span>
              <n-button v-if="scanMode(ch.code) === 'guide'" text size="tiny" type="primary" @click="goSiteSetting">
                去配置
              </n-button>
            </template>
          </div>
        </div>
      </div>
      <n-text depth="3" class="form-hint">
        H5 扫码入口基于站点设置生效域名（{{ h5BaseUrl || '未配置' }}）生成，小程序码与公众号二维码需在对应开放平台获取。
        支付方式与支付配置在「支付配置」页维护，此处仅绑定引用。
      </n-text>

      <!-- H5 二维码放大弹窗 -->
      <n-modal v-model:show="qrModalVisible" preset="card" title="H5 站点访问入口" style="max-width: 420px;">
        <n-space vertical align="center" :size="12">
          <img v-if="h5QrDataUrl" :src="h5QrDataUrl" alt="H5 站点二维码" class="qr-modal-img" />
          <n-input :value="h5ShareUrl" readonly size="small" />
          <n-space :size="8">
            <n-button size="small" type="primary" @click="copyH5Link">复制链接</n-button>
            <n-button size="small" tag="a" :href="h5ShareUrl" target="_blank">打开页面</n-button>
          </n-space>
          <n-text depth="3" style="font-size: 12px;">扫码进入 C 端 H5 首页，可长按或右键保存二维码</n-text>
        </n-space>
      </n-modal>

      <n-modal v-model:show="showConfigModal" preset="card" :title="`配置 - ${modalChannel?.name ?? ''}`" style="width: 600px; max-width: 92vw;">
        <template v-if="modalChannel">
          <n-alert v-if="modalChannel.code === 'h5'" type="success" style="margin-bottom: 16px;">
            浏览器 H5 渠道无登录凭证；绑定微信支付时需填写本渠道的微信 AppID（下单渠道身份），其余只需控制启用状态与支付方式绑定。
          </n-alert>
          <n-alert v-else-if="modalChannel.code === 'alimp'" type="success" style="margin-bottom: 16px;">
            支付宝小程序仅需配置 AppID：登录为手机号表单登录，支付的服务端签名凭证复用支付配置，无需在此录入密钥。
          </n-alert>

          <n-form label-placement="left" label-width="110">
            <n-form-item label="渠道名称">
              <n-input v-model:value="modalChannel.name" placeholder="显示名称" />
            </n-form-item>
            <template v-if="needsAppID(modalChannel.code)">
              <n-form-item label="AppID">
                <n-input v-model:value="modalChannel.app_id" :placeholder="appIDPlaceholder(modalChannel.code)" />
                <n-text v-if="modalChannel.code === 'h5' || modalChannel.code === 'pos'" depth="3" class="form-hint">
                  微信 AppID 按渠道填写（一个商户号可关联多个 AppID）：绑定微信支付时必填，
                  且该 AppID 须已在商户号下完成关联，否则下单会被微信拒绝。
                </n-text>
              </n-form-item>
            </template>
            <template v-if="needsAppSecret(modalChannel.code)">
              <n-form-item label="AppSecret">
                <n-input
                  v-model:value="modalChannel.app_secret"
                  type="password"
                  show-password-on="click"
                  :placeholder="secretPlaceholder"
                />
              </n-form-item>
            </template>
            <n-form-item label="启用渠道">
              <n-switch
                v-model:value="modalChannel.enabled"
                :disabled="!hasCredential && !modalChannel.enabled"
              />
            </n-form-item>

            <!-- 支付方式绑定：勾选 + 排序，顺序即该端收银台展示顺序 -->
            <n-form-item label="支付方式">
              <div class="paymethod-box">
                <div v-if="modalChannel.pay_methods.length === 0" class="pm-none">
                  未绑定任何支付方式，该端收银台将为空。请从下方「添加支付方式」中选择。
                </div>
                <div v-for="(m, idx) in modalChannel.pay_methods" :key="m" class="pm-row">
                  <n-tag size="small" :bordered="false" :type="m === 'cash' ? 'default' : pmPlatform(m) === 'wechat' ? 'success' : 'info'">
                    {{ payMethodLabel(m) }}
                  </n-tag>
                  <n-text v-if="m !== 'cash' && !payConfigReady(m)" depth="3" class="pm-warn">配置未就绪，收银台将隐藏</n-text>
                  <span class="pm-spacer" />
                  <n-button quaternary size="tiny" :disabled="idx === 0" @click="movePayMethod(idx, -1)">↑</n-button>
                  <n-button quaternary size="tiny" :disabled="idx === modalChannel.pay_methods.length - 1" @click="movePayMethod(idx, 1)">↓</n-button>
                  <n-button quaternary size="tiny" type="error" @click="removePayMethod(idx)">移除</n-button>
                </div>
                <n-select
                  :value="null"
                  placeholder="＋添加支付方式"
                  clearable
                  filterable
                  :options="addablePayMethods"
                  :disabled="addablePayMethods.length === 0"
                  @update:value="(v: string | null) => v && addPayMethod(v)"
                />
                <n-text depth="3" class="pm-hint">
                  仅列出与该渠道平台兼容、已启用且就绪的支付配置；绑定顺序即收银台展示顺序，
                  保存后写入渠道并在该端支付链路即时生效。
                </n-text>
              </div>
            </n-form-item>
          </n-form>
        </template>

        <template #footer>
          <n-space justify="end">
            <n-button @click="showConfigModal = false">取消</n-button>
            <n-button
              type="primary"
              :loading="saving"
              :disabled="!authStore.isSuperAdmin && !authStore.hasPermission('settings:update')"
              @click="saveChannel"
            >
              保存
            </n-button>
          </n-space>
        </template>
      </n-modal>
    </n-spin>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'ChannelSetting' })
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { refreshOnActivated } from '@/utils/activateRefresh'
import { useMessage } from 'naive-ui'
import QRCode from 'qrcode'
import { getClientChannels, getSiteSetting, getPaymentConfigs, updateClientChannel } from '@/api/admin'
import type { ClientChannel, PayConfigRow } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'

/**
 * 渠道管理页面
 * 卡片维护渠道凭证、启用状态与支付方式绑定；右侧统一「扫码访问」区；
 * 凭证与绑定保存后即时生效（小程序登录与支付下单按渠道现取现用）
 */
const message = useMessage()
const authStore = useAuthStore()
const router = useRouter()

const loading = ref(false)
const saving = ref(false)
const channels = ref<ClientChannel[]>([])
/** 支付配置行列表（支付方式绑定的候选数据源） */
const payConfigs = ref<PayConfigRow[]>([])

const showConfigModal = ref(false)
const modalChannel = ref<ClientChannel | null>(null)

/** H5 扫码入口：站点生效域名、对应二维码 dataURL 与放大弹窗开关 */
const h5BaseUrl = ref('')
const h5QrDataUrl = ref('')
const qrModalVisible = ref(false)

/** H5 访问链接：C 端为 hash 路由且 base=/s/，直达首页 */
const h5ShareUrl = computed(() => (h5BaseUrl.value ? `${h5BaseUrl.value}/s/` : ''))

/** Secret 占位提示：服务端不回传明文，以 configured 标志判断存量凭证 */
const secretPlaceholder = computed(() =>
  modalChannel.value?.configured ? '已配置，留空则保留原值' : '微信 AppSecret',
)

/** 暂不支持渠道（服务端 fixedOrder 之外的禁用态展示）：不可配置、不可启用 */
function isDisabledChannel(code: string): boolean {
  return code === 'android' || code === 'ios'
}

/** 渠道是否需要展示 AppID 输入（仅 android/ios 禁用态不可配置；h5/pos 无登录凭证，绑定微信支付时才必填） */
function needsAppID(code: string): boolean {
  return !['android', 'ios'].includes(code)
}

/** 渠道是否需要配置 AppSecret（仅微信系渠道；alimp 签名私钥复用支付配置） */
function needsAppSecret(code: string): boolean {
  return code === 'mp' || code === 'gzh'
}

/**
 * 弹窗内凭证是否完整（控制启用开关的可用性）
 * Secret 不回传明文：以 configured 标志判断存量凭证，或本次弹窗内新输入了完整凭证
 */
const hasCredential = computed(() => {
  const ch = modalChannel.value
  if (!ch) return false
  // h5/pos 无登录凭证，只有绑定微信支付时才需要 AppID（与服务端 IsConfigured 口径一致）
  if (ch.code === 'h5' || ch.code === 'pos') return !bindsWechatPay(ch.pay_methods) || Boolean(ch.app_id)
  if (ch.code === 'alimp') return ch.configured || Boolean(ch.app_id)
  return ch.configured || Boolean(ch.app_id && ch.app_secret)
})

/**
 * 渠道 code 展示标签
 * @param code 渠道标识
 */
function codeLabel(code: string): string {
  const map: Record<string, string> = {
    h5: '浏览器',
    gzh: '公众号',
    mp: '小程序',
    alimp: '支付宝小程序',
    android: '安卓App',
    ios: 'iOS App',
    pos: '收银台',
  }
  return map[code] ?? code
}

/**
 * 渠道 code 标签配色
 * @param code 渠道标识
 */
function codeTagType(code: string): 'default' | 'info' | 'success' | 'warning' {
  switch (code) {
    case 'mp':
    case 'gzh':
      return 'info'
    case 'alimp':
      return 'success'
    case 'pos':
      return 'warning'
    default:
      return 'default'
  }
}

/**
 * 渠道一句话用途说明（卡片内展示）
 * @param code 渠道标识
 */
function channelDesc(code: string): string {
  const map: Record<string, string> = {
    h5: '微信外浏览器访问，绑定微信支付需本端 AppID',
    gzh: '网页授权 + 微信内 JSAPI 支付（授权链路暂未开放）',
    mp: 'Code2Session 登录 + 小程序内 JSAPI 支付',
    alimp: '手机号登录 + 小程序内支付宝支付，仅需 AppID',
    android: '开放平台安卓应用，暂不支持',
    ios: '开放平台 iOS 应用，暂不支持',
    pos: '门店收银台（独立应用），付款码/现金收款',
  }
  return map[code] ?? ''
}

/**
 * AppID 输入框占位提示
 * @param code 渠道标识
 */
function appIDPlaceholder(code: string): string {
  if (code === 'alimp') return '支付宝开放平台小程序 AppID'
  if (code === 'h5' || code === 'pos') return '微信 AppID（绑定微信支付时必填）'
  return '微信 AppID'
}

/**
 * 渠道卡片扫码区展示模式
 * @param code 渠道标识
 * @returns qr 站点二维码 / guide 引导去配置域名 / hint 纯说明文案
 */
type ScanMode = 'qr' | 'guide' | 'hint'
function scanMode(code: string): ScanMode {
  if (code !== 'h5') return 'hint'
  return h5BaseUrl.value ? 'qr' : 'guide'
}

/**
 * 扫码区说明文案（各渠道给出获取指引，App 明确无需扫码）
 * @param code 渠道标识
 */
function scanHint(code: string): string {
  const map: Record<string, string> = {
    gzh: '公众号二维码请在公众平台「账号详情」页获取',
    mp: '小程序码请在微信公众平台下载后分享使用',
    alimp: '支付宝小程序码请在开放平台获取',
    android: 'App 通过应用商店分发安装，无需扫码',
    ios: 'App 通过应用商店分发安装，无需扫码',
    pos: '收银台为独立部署的后台应用，无需扫码',
  }
  return map[code] ?? ''
}

/**
 * 绑定条目展示名
 * @param entry "cash" 或 "<platform>:<config_id>"
 */
function payMethodLabel(entry: string): string {
  if (entry === 'cash') return '货到付款'
  const [platform, id] = entry.split(':')
  const row = payConfigs.value.find((r) => String(r.id) === id && r.platform === platform)
  return row ? row.name : entry
}

/** 绑定条目的平台标识（cash 返回空串） */
function pmPlatform(entry: string): string {
  return entry === 'cash' ? '' : entry.split(':')[0]
}

/**
 * 绑定列表是否含微信支付
 * @param methods 渠道绑定的支付方式列表（cash 或 platform:config_id）
 */
function bindsWechatPay(methods: string[] | undefined): boolean {
  return (methods ?? []).some((m) => pmPlatform(m) === 'wechat')
}

/** 绑定条目引用的配置是否就绪（cash 恒就绪；配置不存在按未就绪处理） */
function payConfigReady(entry: string): boolean {
  if (entry === 'cash') return true
  const [platform, id] = entry.split(':')
  const row = payConfigs.value.find((r) => String(r.id) === id && r.platform === platform)
  return !!row?.ready
}

/**
 * 渠道与支付平台的兼容矩阵（与服务端 validatePayMethods 一致）
 */
const PAY_COMPAT: Record<string, string[]> = {
  wechat: ['h5', 'gzh', 'mp', 'pos'],
  alipay: ['h5', 'alimp', 'pos'],
}

/**
 * 「＋添加支付方式」候选：内置现金（未绑定时）+ 平台兼容且启用就绪的支付配置（未绑定时）
 */
const addablePayMethods = computed(() => {
  const ch = modalChannel.value
  if (!ch) return []
  const options: { label: string; value: string }[] = []
  if (!ch.pay_methods.includes('cash')) {
    options.push({ label: '货到付款（内置）', value: 'cash' })
  }
  for (const row of payConfigs.value) {
    if (!row.enabled || !row.ready) continue
    if (!(PAY_COMPAT[row.platform] ?? []).includes(ch.code)) continue
    const entry = `${row.platform}:${row.id}`
    if (ch.pay_methods.includes(entry)) continue
    options.push({ label: row.name, value: entry })
  }
  return options
})

/**
 * 加载渠道列表与支付配置列表（绑定候选数据源）
 */
async function loadChannels() {
  loading.value = true
  try {
    const res = await getClientChannels()
    channels.value = res.data.data.channels
    const cfgRes = await getPaymentConfigs()
    payConfigs.value = cfgRes.data.data.list
  } finally {
    loading.value = false
  }
}

/**
 * 加载站点生效域名并生成 H5 入口二维码
 * 域名取 effective_base_url（环境变量优先）兜底 base_url；
 * 获取或生成失败时静默降级为引导文案，不阻塞渠道配置功能
 */
async function loadSiteBase() {
  try {
    const res = await getSiteSetting()
    if (res.data.code === 0) {
      h5BaseUrl.value = (res.data.data.effective_base_url || res.data.data.base_url || '').replace(/\/+$/, '')
    }
    h5QrDataUrl.value = h5BaseUrl.value
      ? await QRCode.toDataURL(`${h5BaseUrl.value}/s/`, { width: 220, margin: 2 })
      : ''
  } catch {
    h5BaseUrl.value = ''
    h5QrDataUrl.value = ''
  }
}

/**
 * 跳转站点设置页（H5 未配置域名时的引导入口）
 */
function goSiteSetting() {
  router.push('/system/site')
}

/**
 * 复制 H5 访问链接（clipboard API，降级 execCommand）
 */
async function copyH5Link() {
  if (!h5ShareUrl.value) return
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(h5ShareUrl.value)
    } else {
      // 非安全上下文（http）降级：隐藏 textarea + execCommand
      const ta = document.createElement('textarea')
      ta.value = h5ShareUrl.value
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

/**
 * 打开配置弹窗（深拷贝，取消不污染列表）
 * @param ch 渠道项
 */
function openConfig(ch: ClientChannel) {
  if (isDisabledChannel(ch.code)) return
  modalChannel.value = { ...ch, pay_methods: [...(ch.pay_methods ?? [])] }
  showConfigModal.value = true
}

/**
 * 绑定列表上移/下移（调整收银台展示顺序，保存后生效）
 * @param idx 条目下标
 * @param direction -1 上移，1 下移
 */
function movePayMethod(idx: number, direction: -1 | 1) {
  const ch = modalChannel.value
  if (!ch) return
  const next = idx + direction
  if (next < 0 || next >= ch.pay_methods.length) return
  const list = [...ch.pay_methods]
  ;[list[idx], list[next]] = [list[next], list[idx]]
  ch.pay_methods = list
}

/**
 * 移除绑定条目
 * @param idx 条目下标
 */
function removePayMethod(idx: number) {
  const ch = modalChannel.value
  if (!ch) return
  ch.pay_methods = ch.pay_methods.filter((_, i) => i !== idx)
}

/**
 * 追加绑定条目（选择器值变化触发）
 * @param entry "cash" 或 "<platform>:<config_id>"
 */
function addPayMethod(entry: string) {
  const ch = modalChannel.value
  if (!ch || ch.pay_methods.includes(entry)) return
  ch.pay_methods = [...ch.pay_methods, entry]
}

/**
 * 卡片开关：直接保存启用状态（未配置凭证的渠道禁止启用，已启用的异常存量允许关闭）
 * @param ch 渠道项
 * @param on 是否启用
 */
async function onToggle(ch: ClientChannel, on: boolean) {
  ch.enabled = on
  saving.value = true
  try {
    await updateClientChannel(ch.id, {
      code: ch.code,
      name: ch.name,
      app_id: ch.app_id,
      app_secret: '',
      enabled: ch.enabled,
      pay_methods: ch.pay_methods,
    })
    message.success('渠道状态已保存')
    await loadChannels()
  } catch {
    // 保存失败：request 拦截器已弹出后端文案，回滚到服务端状态即可
    await loadChannels()
  } finally {
    saving.value = false
  }
}

/**
 * 弹窗内保存渠道配置（凭证 + 支付方式绑定全量覆盖）
 */
async function saveChannel() {
  if (!modalChannel.value) {
    return
  }
  const ch = modalChannel.value
  if (!ch.name.trim()) {
    message.warning('渠道名称不能为空')
    return
  }
  saving.value = true
  try {
    await updateClientChannel(ch.id, {
      code: ch.code,
      name: ch.name,
      app_id: ch.app_id,
      // Secret 留空时服务端保留原值，避免未重新输入时清空
      app_secret: ch.app_secret,
      enabled: ch.enabled,
      pay_methods: ch.pay_methods,
    })
    message.success('渠道配置已保存')
    await loadChannels()
    showConfigModal.value = false
  } finally {
    saving.value = false
  }
}

/**
 * 刷新页面数据：渠道列表 + 支付配置 + 站点生效域名（keep-alive 切回时可能已在别处更新）
 */
function refreshAll() {
  loadChannels()
  loadSiteBase()
}

// keep-alive 缓存下切回本页不重新挂载，需重新拉取：
// 带着旧表单值整份 PUT 会覆盖他人刚改的配置，未保存的本地编辑以最新值为准丢弃
onMounted(refreshAll)
refreshOnActivated(refreshAll)
</script>

<style lang="scss" scoped>
.channel-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(460px, 1fr));
  gap: 14px;
  margin-bottom: 8px;
}

.channel-card {
  display: flex;
  gap: 14px;
  padding: 16px;
  border: 1px solid #eee;
  border-radius: 10px;
  cursor: pointer;
  transition: all 0.2s;

  &:hover {
    border-color: #18a058;
    box-shadow: 0 2px 10px rgba(24, 160, 88, 0.08);
  }

  &.inactive {
    background: #fcfcfc;

    .card-name {
      color: #999;
    }
  }

  // 暂不支持渠道（android/ios）：禁用态，不可点击配置
  &.disabled {
    background: #fafafa;
    cursor: not-allowed;

    .card-name {
      color: #aaa;
    }

    &:hover {
      border-color: #eee;
      box-shadow: none;
    }
  }
}

.card-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
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

  &.muted {
    color: #999;
  }
}

.card-desc {
  font-size: 12px;
  line-height: 1.6;
  color: #999;
}

.card-paymethods {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;

  .pm-tag {
    max-width: 160px;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .pm-empty {
    font-size: 12px;
    color: #bbb;
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

/* 支付方式绑定区块：有序条目列表 + 添加选择器 */
.paymethod-box {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.pm-none {
  padding: 8px 10px;
  border: 1px dashed #e5e5e5;
  border-radius: 6px;
  font-size: 12px;
  color: #999;
  line-height: 1.6;
}

.pm-row {
  display: flex;
  align-items: center;
  gap: 6px;

  .pm-spacer {
    flex: 1;
  }

  .pm-warn {
    font-size: 12px;
  }

  .n-button {
    padding: 0 6px;
  }
}

.pm-hint {
  font-size: 12px;
  line-height: 1.6;
}

// 右侧扫码访问区：固定宽度统一占位，H5 出二维码，其余渠道展示说明文案
.scan-zone {
  width: 120px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 4px 0;
  border-left: 1px dashed #e8e8e8;

  .scan-qr {
    width: 88px;
    height: 88px;
    border: 1px solid #f0f0f0;
    border-radius: 6px;
    cursor: zoom-in;
  }

  .scan-hint,
  .scan-text {
    font-size: 12px;
    line-height: 1.6;
    color: #999;
  }

  .scan-text {
    text-align: center;
    padding: 0 4px;
  }
}

.qr-modal-img {
  width: 220px;
  height: 220px;
  border: 1px solid #efeff5;
  border-radius: 8px;
}

.form-hint {
  display: block;
  font-size: 12px;
}
</style>
