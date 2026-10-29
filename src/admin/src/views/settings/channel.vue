<template>
  <n-card title="渠道管理">
    <n-spin :show="loading">
      <n-alert type="info" style="margin-bottom: 16px;">
        客户端渠道是各端的访问凭证（AppID/AppSecret）与启用开关：小程序登录、公众号 OAuth、按端支付下单均取用此处配置。
        启用前需先点击卡片完成凭证配置；配置保存后即时生效，无需重启服务。
      </n-alert>

      <div class="channel-cards">
        <div
          v-for="ch in channels"
          :key="ch.code"
          class="channel-card"
          :class="{ inactive: !ch.enabled }"
          @click="openConfig(ch)"
        >
          <div class="card-main">
            <div class="card-top">
              <span class="card-name">{{ ch.name }}</span>
              <n-tag size="small" :bordered="false" :type="codeTagType(ch.code)">{{ codeLabel(ch.code) }}</n-tag>
            </div>
            <div class="card-status">
              <span v-if="ch.code === 'h5'" class="status-text ok">无需配置凭证</span>
              <span v-else class="status-text" :class="ch.configured ? 'ok' : 'warn'">
                {{ ch.configured ? '已配置' : '未配置' }}
              </span>
            </div>
            <div class="card-desc">{{ channelDesc(ch.code) }}</div>
            <div class="card-ops">
              <span class="switch-holder" @click.stop>
                <n-switch
                  size="small"
                  :value="ch.enabled"
                  :disabled="ch.code !== 'h5' && !ch.configured && !ch.enabled"
                  @update:value="(on: boolean) => onToggle(ch, on)"
                />
              </span>
              <n-button quaternary size="tiny" @click.stop="openConfig(ch)">配置</n-button>
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
        支付配置中可按渠道勾选各支付方式的适用范围；H5 扫码入口基于站点设置生效域名（{{ h5BaseUrl || '未配置' }}）生成，
        小程序码与公众号二维码需在微信公众平台获取。
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

      <n-modal v-model:show="showConfigModal" preset="card" :title="`配置 - ${modalChannel?.name ?? ''}`" style="width: 560px; max-width: 92vw;">
        <template v-if="modalChannel">
          <n-alert v-if="modalChannel.code === 'h5'" type="success" style="margin-bottom: 16px;">
            浏览器 H5 渠道无需配置凭证，仅需控制启用状态。
          </n-alert>

          <n-form label-placement="left" label-width="110">
            <n-form-item label="渠道名称">
              <n-input v-model:value="modalChannel.name" placeholder="显示名称" />
            </n-form-item>
            <template v-if="modalChannel.code !== 'h5'">
              <n-form-item label="AppID">
                <n-input v-model:value="modalChannel.app_id" placeholder="微信 AppID" />
              </n-form-item>
              <n-form-item label="AppSecret">
                <n-input
                  v-model:value="modalChannel.app_secret"
                  type="password"
                  show-password-on="click"
                  :placeholder="secretPlaceholder"
                />
              </n-form-item>
            </template>
            <n-form-item label="排序">
              <n-input-number v-model:value="modalChannel.sort" :min="0" style="width: 160px;" />
            </n-form-item>
            <n-form-item label="启用渠道">
              <n-switch v-model:value="modalChannel.enabled" :disabled="modalChannel.code !== 'h5' && !hasCredential && !modalChannel.enabled" />
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
import { getClientChannels, getSiteSetting, updateClientChannel } from '@/api/admin'
import type { ClientChannel } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'

/**
 * 渠道管理页面
 * 左右布局卡片：左侧维护渠道凭证与启用状态，右侧统一「扫码访问」区；
 * H5 基于站点设置生效域名纯前端生成站点二维码，小程序/公众号/App 展示差异化说明统一占位；
 * 凭证保存后即时生效（小程序登录与支付下单按渠道现取现用）
 */
const message = useMessage()
const authStore = useAuthStore()
const router = useRouter()

const loading = ref(false)
const saving = ref(false)
const channels = ref<ClientChannel[]>([])

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

/**
 * 弹窗内凭证是否完整（控制启用开关的可用性）
 * Secret 不回传明文：以 configured 标志判断存量凭证，或本次弹窗内新输入了完整凭证
 */
const hasCredential = computed(() => {
  const ch = modalChannel.value
  if (!ch) return false
  if (ch.code === 'h5') return true
  return ch.configured || Boolean(ch.app_id && ch.app_secret)
})

/**
 * 渠道 code 展示标签
 * @param code 渠道标识
 */
function codeLabel(code: string): string {
  const map: Record<string, string> = {
    mp: '小程序',
    gzh: '公众号',
    app: 'App',
    h5: '浏览器',
  }
  return map[code] ?? code
}

/**
 * 渠道 code 标签配色
 * @param code 渠道标识
 */
function codeTagType(code: string): 'default' | 'info' | 'warning' {
  switch (code) {
    case 'mp':
      return 'info'
    case 'gzh':
    case 'app':
      return 'warning'
    default:
      return 'default'
  }
}

/**
 * 渠道一句话用途说明（卡片内展示，替代原底部汇总文案）
 * @param code 渠道标识
 */
function channelDesc(code: string): string {
  const map: Record<string, string> = {
    mp: 'Code2Session 登录 + 小程序内 JSAPI 支付',
    gzh: '网页授权 + 微信内支付（授权链路暂未开放）',
    app: '开放平台 App 支付（暂未开放）',
    h5: '微信外浏览器访问，无需凭证',
  }
  return map[code] ?? ''
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
 * 扫码区说明文案（小程序/公众号给出获取指引，App 明确无需扫码）
 * @param code 渠道标识
 */
function scanHint(code: string): string {
  const map: Record<string, string> = {
    mp: '小程序码请在微信公众平台下载后分享使用',
    gzh: '公众号二维码请在公众平台「账号详情」页获取',
    app: 'App 通过应用商店分发安装，无需扫码',
  }
  return map[code] ?? ''
}

/**
 * 加载渠道列表
 */
async function loadChannels() {
  loading.value = true
  try {
    const res = await getClientChannels()
    channels.value = res.data.data.channels
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
  modalChannel.value = { ...ch }
  showConfigModal.value = true
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
      sort: ch.sort,
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
 * 弹窗内保存渠道配置
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
      sort: ch.sort,
    })
    message.success('渠道配置已保存')
    await loadChannels()
    showConfigModal.value = false
  } finally {
    saving.value = false
  }
}

/**
 * 刷新页面数据：渠道列表 + 站点生效域名（keep-alive 切回时域名可能在站点设置中被更新）
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
}

.card-desc {
  flex: 1;
  font-size: 12px;
  line-height: 1.6;
  color: #999;
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
