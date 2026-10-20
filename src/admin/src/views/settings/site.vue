<template>
  <n-card title="站点设置">
    <n-spin :show="loading">
      <div style="max-width: 640px">
        <n-alert v-if="!isSuperAdmin" type="warning" :show-icon="true" class="mb-16">
          仅超级管理员可修改站点设置，当前为只读查看。
        </n-alert>

        <n-form label-placement="left" label-width="110" :disabled="!isSuperAdmin">
          <n-divider title-placement="left">部署域名</n-divider>
          <n-form-item label="域名基址">
            <n-input
              v-model:value="form.base_url"
              placeholder="留空则按访问请求自动推导，如 https://shop.example.com"
              clearable
            />
          </n-form-item>
          <n-form-item label="当前生效">
            <n-space vertical size="small" style="width: 100%">
              <n-text strong>{{ setting.effective_base_url || '未配置' }}</n-text>
              <n-text depth="3" style="font-size: 12px">
                来源：{{ sourceLabel }}{{ sourceHint }}
              </n-text>
            </n-space>
          </n-form-item>
          <n-alert v-if="isPrivateHost" type="warning" :show-icon="true" class="mb-16">
            当前生效地址指向本机或内网，支付宝与微信无法回调，正式环境请改为公网域名。
          </n-alert>

          <n-divider title-placement="left">派生地址</n-divider>
          <n-form-item label="微信通知">
            <n-text depth="3" style="font-size: 12px; word-break: break-all">
              {{ setting.wechat_notify_url }}
            </n-text>
          </n-form-item>
          <n-form-item label="支付宝通知">
            <n-text depth="3" style="font-size: 12px; word-break: break-all">
              {{ setting.alipay_notify_url }}
            </n-text>
          </n-form-item>
          <n-form-item label="支付宝回跳">
            <n-text depth="3" style="font-size: 12px; word-break: break-all">
              {{ setting.alipay_return_url }}
            </n-text>
          </n-form-item>
          <n-alert type="info" :show-icon="true" class="mb-16">
            回调地址由部署域名自动派生，无需在支付配置中填写；把上述地址配置到支付平台后台即可。
          </n-alert>

          <n-divider title-placement="left">品牌信息</n-divider>
          <n-form-item label="站点名称">
            <n-input v-model:value="form.site_name" placeholder="用于浏览器标题与页面品牌位" clearable />
          </n-form-item>
          <n-form-item label="站点 Logo">
            <ImageUploadInput v-model:value="form.logo_url" hint="建议 200×200 以上的方形透明图" />
          </n-form-item>
          <n-form-item label="图标 favicon">
            <ImageUploadInput v-model:value="form.favicon_url" hint="建议 64×64 的 .png 或 .ico" />
          </n-form-item>

          <n-divider title-placement="left">页脚与备案</n-divider>
          <n-form-item label="版权文字">
            <n-input v-model:value="form.copyright_text" placeholder="例：© 2026 社区商超" clearable />
          </n-form-item>
          <n-form-item label="ICP 备案号">
            <n-input v-model:value="form.icp_number" placeholder="例：京ICP备00000000号" clearable />
          </n-form-item>

          <n-divider title-placement="left">搜索优化</n-divider>
          <n-form-item label="页面描述">
            <n-input
              v-model:value="form.seo_description"
              type="textarea"
              :autosize="{ minRows: 2, maxRows: 4 }"
              maxlength="200"
              show-count
              placeholder="展示在搜索结果摘要与社交分享卡片中"
            />
          </n-form-item>
          <n-form-item label="关键词">
            <n-input v-model:value="form.seo_keywords" placeholder="逗号分隔" clearable />
          </n-form-item>

          <n-alert type="default" :show-icon="false" class="mb-16" style="font-size: 12px">
            品牌信息会写入服务端返回的页面，搜索引擎爬虫与浏览器首屏读到同一份内容；
            保存后立即生效，无需重启服务。
          </n-alert>

          <n-space justify="end">
            <n-button type="primary" :loading="saving" :disabled="!isSuperAdmin" @click="save">
              保存设置
            </n-button>
          </n-space>
        </n-form>
      </div>
    </n-spin>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'SiteSetting' })
import { computed, onMounted, reactive, ref } from 'vue'
import { useMessage } from 'naive-ui'
import { getSiteSetting, updateSiteSetting } from '@/api/admin'
import type { SiteSetting } from '@/api/admin'
import ImageUploadInput from '@/components/ImageUploadInput.vue'
import { useAuthStore } from '@/stores/auth'

/**
 * 站点设置页面
 * 维护平台级部署域名与品牌信息；域名是全平台回调地址与附件外链的唯一基址来源
 */
const authStore = useAuthStore()
const isSuperAdmin = computed(() => authStore.isSuperAdmin)

const loading = ref(false)
const saving = ref(false)
const message = useMessage()

const emptySetting = (): SiteSetting => ({
  base_url: '',
  site_name: '',
  logo_url: '',
  favicon_url: '',
  copyright_text: '',
  icp_number: '',
  seo_description: '',
  seo_keywords: '',
  updated_at: '',
  version: 0,
  effective_base_url: '',
  base_url_source: 'request',
  wechat_notify_url: '',
  alipay_notify_url: '',
  alipay_return_url: '',
})

const setting = ref<SiteSetting>(emptySetting())
const form = reactive({
  base_url: '',
  site_name: '',
  logo_url: '',
  favicon_url: '',
  copyright_text: '',
  icp_number: '',
  seo_description: '',
  seo_keywords: '',
})

const sourceLabels: Record<string, string> = {
  env: '环境变量 SITE_BASE_URL',
  db: '本页站点设置',
  request: '按当前访问请求推导（未配置）',
}
const sourceLabel = computed(() => sourceLabels[setting.value.base_url_source] || '未知')
const sourceHint = computed(() =>
  setting.value.base_url_source === 'env'
    ? '；环境变量优先级最高，本页填写的域名不会生效'
    : setting.value.base_url_source === 'request'
      ? '；建议填写正式域名，避免访问端口变化导致回调地址漂移'
      : '',
)

// 本机与内网地址无法被支付平台回调，需给出醒目提示
const isPrivateHost = computed(() => {
  const host = (setting.value.effective_base_url || '').replace(/^https?:\/\//, '').split(':')[0]
  if (!host) return true
  return host === 'localhost' || host === '127.0.0.1' || /^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(host)
})

/**
 * 加载站点设置
 */
async function load() {
  loading.value = true
  try {
    const res = await getSiteSetting()
    if (res.data.code === 0 && res.data.data) {
      setting.value = res.data.data
      const s = res.data.data
      Object.assign(form, {
        base_url: s.base_url,
        site_name: s.site_name,
        logo_url: s.logo_url,
        favicon_url: s.favicon_url,
        copyright_text: s.copyright_text,
        icp_number: s.icp_number,
        seo_description: s.seo_description,
        seo_keywords: s.seo_keywords,
      })
    }
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    loading.value = false
  }
}

/**
 * 保存站点设置并重新拉取派生结果
 */
async function save() {
  saving.value = true
  try {
    await updateSiteSetting({ ...form })
    message.success('站点设置已保存，立即生效')
    await load()
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.mb-16 {
  margin-bottom: 16px;
}
</style>
