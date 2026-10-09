<template>
  <div class="settings-page">
    <n-card title="系统管理">
      <n-tabs v-model:value="activeTab" type="line" animated>
        <n-tab-pane name="info" tab="系统信息">
          <n-descriptions label-placement="top" bordered :columns="2">
            <n-descriptions-item label="系统名称">社区商超后台管理系统</n-descriptions-item>
            <n-descriptions-item label="版本号">v1.0.0</n-descriptions-item>
            <n-descriptions-item label="当前管理员">{{ authStore.admin?.nickname || '-' }}</n-descriptions-item>
            <n-descriptions-item label="角色">{{ authStore.admin?.role_name || '-' }}</n-descriptions-item>
          </n-descriptions>
          <n-space style="margin-top: 16px;">
            <n-button type="primary" @click="openPasswordModal">修改密码</n-button>
          </n-space>
        </n-tab-pane>

        <n-tab-pane name="roles" tab="角色权限">
          <n-space vertical>
            <n-space>
              <n-button type="primary" :disabled="!authStore.hasPermission('role:create')" @click="openRoleModal()">新增角色</n-button>
            </n-space>
            <n-data-table :columns="roleColumns" :data="roles" :loading="roleLoading" scroll-x="600" />
          </n-space>
        </n-tab-pane>

        <n-tab-pane name="payment" tab="支付配置">
          <n-spin :show="paymentLoading">
            <n-form :model="paymentForm" label-placement="top">
              <n-form-item label="支付渠道开关与顺序">
                <n-dynamic-input
                  v-model:value="paymentForm.enabled_channels"
                  :min="1"
                  :max="3"
                  :on-create="createChannelItem"
                >
                  <template #default="{ value, index }">
                    <div style="display: flex; align-items: center; gap: 12px; width: 100%;">
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
              <n-grid :cols="2" :x-gap="16">
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
              <n-grid :cols="2" :x-gap="16">
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
        </n-tab-pane>
      </n-tabs>
    </n-card>

    <!-- 角色编辑弹窗 -->
    <n-modal v-model:show="showRoleModal" title="角色信息" preset="card" style="width: 560px; max-width: 90vw" scrollable>
      <n-form :model="roleForm" label-placement="top">
        <n-form-item label="角色名称">
          <n-input v-model:value="roleForm.name" placeholder="请输入角色名称" />
        </n-form-item>
        <n-form-item label="角色编码">
          <n-input v-model:value="roleForm.code" placeholder="请输入角色编码，如 admin" />
        </n-form-item>
        <n-form-item label="权限">
          <n-checkbox-group v-model:value="roleForm.permissions">
            <n-space vertical>
              <n-checkbox v-for="perm in permissionOptions" :key="perm.value" :value="perm.value" :label="perm.label" />
            </n-space>
          </n-checkbox-group>
        </n-form-item>
        <n-form-item label="描述">
          <n-input v-model:value="roleForm.description" type="textarea" placeholder="角色描述" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showRoleModal = false">取消</n-button>
          <n-button type="primary" :loading="roleModalLoading" @click="saveRole">保存</n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 修改密码弹窗 -->
    <n-modal v-model:show="showPasswordModal" title="修改密码" preset="card" style="width: 420px; max-width: 90vw" scrollable>
      <n-form :model="passwordForm" label-placement="top">
        <n-form-item label="旧密码">
          <n-input v-model:value="passwordForm.old_password" type="password" placeholder="请输入旧密码" />
        </n-form-item>
        <n-form-item label="新密码">
          <n-input v-model:value="passwordForm.new_password" type="password" placeholder="请输入新密码，至少 6 位" />
        </n-form-item>
        <n-form-item label="确认新密码">
          <n-input v-model:value="passwordForm.confirm_password" type="password" placeholder="请再次输入新密码" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showPasswordModal = false">取消</n-button>
          <n-button type="primary" :loading="passwordLoading" @click="savePassword">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { h, onMounted, reactive, ref } from 'vue'
import { useMessage } from 'naive-ui'
import type { DataTableColumns, UploadCustomRequestOptions } from 'naive-ui'
import { getRoleList, createRole, updateRole, deleteRole, changeAdminPassword, getPaymentSetting, updatePaymentSetting, uploadPaymentCert } from '@/api/admin'
import type { PaymentSetting, PaymentSuggestedURLs } from '@/api/admin'
import type { Role } from '@/api/types'
import { useAuthStore } from '@/stores/auth'

const message = useMessage()
const authStore = useAuthStore()

const activeTab = ref('info')
const roleLoading = ref(false)
const roles = ref<Role[]>([])

const showRoleModal = ref(false)
const roleModalLoading = ref(false)
const roleForm = reactive<Partial<Role>>({
  id: undefined,
  name: '',
  code: '',
  permissions: [],
  description: '',
})

const showPasswordModal = ref(false)
const passwordLoading = ref(false)
const passwordForm = reactive({
  old_password: '',
  new_password: '',
  confirm_password: '',
})

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

/** 权限可选项 */
const permissionOptions = [
  { label: '仪表盘查看', value: 'dashboard:view' },
  { label: '管理员查看', value: 'admin:view' },
  { label: '管理员创建', value: 'admin:create' },
  { label: '管理员更新', value: 'admin:update' },
  { label: '管理员删除', value: 'admin:delete' },
  { label: '角色查看', value: 'role:view' },
  { label: '角色创建', value: 'role:create' },
  { label: '角色更新', value: 'role:update' },
  { label: '角色删除', value: 'role:delete' },
  { label: '商品查看', value: 'product:view' },
  { label: '商品创建', value: 'product:create' },
  { label: '商品更新', value: 'product:update' },
  { label: '商品删除', value: 'product:delete' },
  { label: '订单查看', value: 'order:view' },
  { label: '订单更新', value: 'order:update' },
  { label: '库存查看', value: 'inventory:view' },
  { label: '库存创建', value: 'inventory:create' },
  { label: '营销查看', value: 'marketing:view' },
  { label: '营销创建', value: 'marketing:create' },
  { label: '营销更新', value: 'marketing:update' },
  { label: '营销删除', value: 'marketing:delete' },
  { label: '社区查看', value: 'community:view' },
  { label: '用户查看', value: 'user:view' },
  { label: '退款查看', value: 'refund:view' },
  { label: '退款更新', value: 'refund:update' },
  { label: '数据统计', value: 'statistics:view' },
  { label: '系统管理查看', value: 'settings:view' },
  { label: '系统管理修改', value: 'settings:update' },
]

/** 角色表格列 */
const roleColumns: DataTableColumns<Role> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '角色名称', key: 'name', ellipsis: { tooltip: true } },
  { title: '角色编码', key: 'code', width: 120, ellipsis: { tooltip: true } },
  {
    title: '权限',
    key: 'permissions',
    width: 200,
    ellipsis: { tooltip: true },
    render(row) {
      return (row.permissions || []).join(', ')
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 120,
    render(row) {
      return h('div', { style: 'display: flex; gap: 12px; flex-wrap: wrap;' }, [
        authStore.hasPermission('role:update')
          ? h('a', { onClick: () => openRoleModal(row), style: 'color: #07c160; cursor: pointer;' }, '编辑')
          : null,
        authStore.hasPermission('role:delete')
          ? h('a', { onClick: () => onDeleteRole(row.id), style: 'color: #ff4d4f; cursor: pointer;' }, '删除')
          : null,
      ])
    },
  },
]

/**
 * 加载角色列表
 */
async function loadRoles() {
  roleLoading.value = true
  try {
    const res = await getRoleList()
    roles.value = res.data.data
  } finally {
    roleLoading.value = false
  }
}

/**
 * 打开角色编辑弹窗
 * @param row 角色数据
 */
function openRoleModal(row?: Role) {
  roleForm.id = row?.id
  roleForm.name = row?.name || ''
  roleForm.code = row?.code || ''
  roleForm.permissions = row?.permissions || []
  roleForm.description = row?.description || ''
  showRoleModal.value = true
}

/**
 * 保存角色
 */
async function saveRole() {
  if (!roleForm.name) {
    message.warning('请输入角色名称')
    return
  }
  if (!roleForm.code) {
    message.warning('请输入角色编码')
    return
  }

  roleModalLoading.value = true
  try {
    if (roleForm.id) {
      await updateRole(roleForm.id, roleForm)
    } else {
      await createRole(roleForm)
    }
    message.success('保存成功')
    showRoleModal.value = false
    loadRoles()
  } finally {
    roleModalLoading.value = false
  }
}

/**
 * 删除角色
 * @param id 角色 ID
 */
async function onDeleteRole(id: number) {
  try {
    await deleteRole(id)
    message.success('删除成功')
    loadRoles()
  } catch {
    message.error('删除失败')
  }
}

/**
 * 打开修改密码弹窗
 */
function openPasswordModal() {
  passwordForm.old_password = ''
  passwordForm.new_password = ''
  passwordForm.confirm_password = ''
  showPasswordModal.value = true
}

/**
 * 保存新密码
 */
async function savePassword() {
  if (!passwordForm.old_password) {
    message.warning('请输入旧密码')
    return
  }
  if (!passwordForm.new_password || passwordForm.new_password.length < 6) {
    message.warning('新密码至少 6 位')
    return
  }
  if (passwordForm.new_password !== passwordForm.confirm_password) {
    message.warning('两次输入的新密码不一致')
    return
  }

  const adminId = authStore.admin?.id
  if (!adminId) {
    message.error('未获取到管理员信息')
    return
  }

  passwordLoading.value = true
  try {
    await changeAdminPassword(adminId, {
      old_password: passwordForm.old_password,
      new_password: passwordForm.new_password,
    })
    message.success('密码修改成功，请重新登录')
    showPasswordModal.value = false
  } finally {
    passwordLoading.value = false
  }
}

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
  loadRoles()
  loadPaymentSetting()
})
</script>

<style lang="scss" scoped>
.settings-page {
  max-width: 100%;
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
