<template>
  <n-card title="附件配置">
    <n-spin :show="loading">
      <n-form :model="form" label-placement="left" label-width="160px" class="attachment-form">
        <n-form-item label="存储模式">
          <n-radio-group v-model:value="form.storage_type">
            <n-radio-button value="local">本地附件</n-radio-button>
            <n-radio-button value="oss" :disabled="!authStore.isSuperAdmin && !authStore.hasPermission('settings:update')">
              阿里云 OSS
            </n-radio-button>
            <n-radio-button value="cos" disabled>腾讯云 COS（即将支持）</n-radio-button>
          </n-radio-group>
        </n-form-item>

        <n-form-item label="自定义访问域名">
          <n-input v-model:value="form.visit_domain" placeholder="选填，如 https://market.example.com，用于拼接图片完整访问地址" />
        </n-form-item>

        <n-alert v-if="form.storage_type === 'local'" type="info" class="tip">
          本地模式文件保存在服务部署目录的 attachment/ 下，通过 /uploads/ 路径对外访问；商品、Banner 等图片地址在返回给页面时按上方「自定义访问域名」拼接为完整 URL（数据库仅存相对路径）。访问域名未配置时：保存站点设置会自动沿用其部署域名（此后以本页配置为准，站点域名修改不再联动）；仍为空时图片按 /uploads/ 站内路径访问。切换为 OSS 后，新上传的图片将直传 OSS；历史本地图片的访问地址会随存储模式变化，请先通过下方「本地附件迁移」将历史文件同步到 OSS，再切换存储模式，避免图片无法访问。
        </n-alert>

        <template v-if="form.storage_type === 'oss'">
          <n-divider title-placement="left">阿里云 OSS 配置</n-divider>
          <n-grid cols="1 m:2" :x-gap="16" responsive="screen">
            <n-grid-item>
              <n-form-item label="AccessKey ID" required>
                <n-input v-model:value="form.oss_access_key_id" placeholder="阿里云 RAM 访问密钥 ID" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="AccessKey Secret" required>
                <n-input
                  v-model:value="form.oss_access_key_secret"
                  type="password"
                  show-password-on="click"
                  placeholder="已配置时显示掩码，留空或保持掩码则保留原值"
                />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="Endpoint" required>
                <n-input v-model:value="form.oss_endpoint" placeholder="如 oss-cn-shanghai.aliyuncs.com" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="Bucket 名称" required>
                <n-input v-model:value="form.oss_bucket_name" placeholder="OSS 存储桶名称" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-form-item label="OSS 自定义访问域名">
                <n-input v-model:value="form.oss_custom_visit_domain" placeholder="选填，绑定 CDN 域名时填写，如 https://img.example.com/" />
              </n-form-item>
            </n-grid-item>
          </n-grid>

          <n-alert type="warning" class="tip" title="Bucket 跨域配置">
            直传 OSS 需要在 Bucket 的「跨域设置（CORS）」中允许来自后台访问域名的 PUT 请求，并允许携带 Content-Type 请求头。
          </n-alert>
        </template>

        <n-form-item label="图片大小上限（MB）">
          <n-input-number v-model:value="maxSizeMb" :min="1" :max="50" />
        </n-form-item>

        <n-form-item label="配置版本">
          <n-text depth="3">{{ formatDateTime(form.updated_at) }}</n-text>
        </n-form-item>

        <n-form-item>
          <n-space>
            <n-button type="primary" :loading="saving" :disabled="!authStore.isSuperAdmin && !authStore.hasPermission('settings:update')" @click="save">
              保存配置
            </n-button>
            <n-button :disabled="!authStore.isSuperAdmin && !authStore.hasPermission('settings:update')" @click="goMigrate">
              本地附件迁移
            </n-button>
          </n-space>
        </n-form-item>
      </n-form>
    </n-spin>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'AttachmentSetting' })
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import { getAttachmentSetting, updateAttachmentSetting } from '@/api/attachment'
import type { AttachmentSetting } from '@/api/attachment'
import { useAuthStore } from '@/stores/auth'
import { formatDateTime } from '@/utils/datetime'

/**
 * 附件配置页面
 * 维护附件存储模式（本地 / 阿里云 OSS，预留 COS）与对应访问凭证
 * Secret 脱敏回显：留空或保持掩码提交时服务端保留原值
 */
const message = useMessage()
const authStore = useAuthStore()
const router = useRouter()

const loading = ref(false)
const saving = ref(false)

const form = reactive<AttachmentSetting>({
  storage_type: 'local',
  visit_domain: '',
  oss_access_key_id: '',
  oss_access_key_secret: '',
  oss_endpoint: '',
  oss_bucket_name: '',
  oss_custom_visit_domain: '',
  max_size: 0,
  updated_at: '',
})

/** 图片大小上限（MB 视图值，0 表示使用服务端默认 5MB） */
const maxSizeMb = computed({
  get: () => (form.max_size > 0 ? Math.round(form.max_size / 1024 / 1024) : 5),
  set: (v: number | null) => {
    form.max_size = v && v > 0 ? v * 1024 * 1024 : 0
  },
})

/**
 * 加载附件配置
 */
async function load() {
  loading.value = true
  try {
    const res = await getAttachmentSetting()
    Object.assign(form, res.data.data)
  } finally {
    loading.value = false
  }
}

/**
 * 保存附件配置
 */
async function save() {
  if (form.storage_type === 'oss') {
    if (!form.oss_access_key_id || !form.oss_endpoint || !form.oss_bucket_name) {
      message.warning('请完整填写 OSS 的 AccessKey ID、Endpoint 与 Bucket 名称')
      return
    }
  }

  saving.value = true
  try {
    await updateAttachmentSetting({ ...form })
    message.success('附件配置已保存，保存后即时生效，无需重启服务')
    await load()
  } finally {
    saving.value = false
  }
}

onMounted(load)

/**
 * 跳转本地附件迁移面板（携带 scan=1 自动扫描）
 */
function goMigrate() {
  router.push({ name: 'AttachmentMigrate', query: { scan: '1' } })
}
</script>

<style lang="scss" scoped>
.attachment-form {
  max-width: 720px;
}

.tip {
  margin-bottom: 16px;
}
</style>
