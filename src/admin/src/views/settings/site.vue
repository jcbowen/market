<template>
  <n-card title="站点设置">
    <n-spin :show="loading">
      <div style="max-width: 640px">
        <n-alert v-if="!isSuperAdmin" type="warning" :show-icon="true" class="mb-16">
          仅超级管理员可修改站点设置，当前为只读查看。
        </n-alert>

        <n-form label-placement="left" label-width="110" :disabled="!isSuperAdmin">
          <n-divider title-placement="left">部署域名</n-divider>
          <n-form-item label="站点域名">
            <n-space vertical size="small" style="width: 100%">
              <n-input
                v-model:value="form.base_url"
                placeholder="留空则按访问请求自动推导，如 https://shop.example.com"
                clearable
              />
              <n-space align="center" justify="space-between" :wrap="false" style="width: 100%">
                <n-text depth="3" style="font-size: 12px; word-break: break-all">
                  当前生效：{{ setting.effective_base_url || '未配置' }}（来源：{{ sourceLabel }}{{ sourceHint }}）
                </n-text>
                <n-button
                  size="tiny"
                  type="primary"
                  text
                  :disabled="!isSuperAdmin || !setting.effective_base_url"
                  @click="fillEffective"
                >
                  一键填入
                </n-button>
              </n-space>
            </n-space>
          </n-form-item>
          <n-alert v-if="isPrivateHost" type="warning" :show-icon="true" class="mb-16">
            当前生效地址指向本机或内网，支付宝与微信无法回调，正式环境请改为公网域名。
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
          <n-form-item label="页脚文案">
            <RichTextEditor v-model:value="form.footer_html" :readonly="!isSuperAdmin" />
          </n-form-item>
          <n-alert type="default" :show-icon="false" class="mb-16" style="font-size: 12px">
            版权与 ICP 备案号在此合并编辑；备案号建议做成链接指向 https://beian.miit.gov.cn。
            仅支持加粗、斜体、下划线、链接与换行，其它格式保存时会被过滤。
          </n-alert>

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

          <!-- 外部引导锚点：渠道管理 gzh/mp 卡片「域名校验文件」链接跳转至此滚动定位 -->
          <div id="verify-files" style="scroll-margin-top: 80px"></div>
          <n-divider title-placement="left">平台验证文件</n-divider>
          <n-alert type="default" :show-icon="false" class="mb-16" style="font-size: 12px">
            微信公众号配置「JS接口安全域名 / 网页授权域名」与小程序配置「业务域名」时，需把平台下发的
            校验文件放到站点域名根目录。将下载的 .txt 文件用文本编辑器打开，把文件名与内容分别填入下方保存，
            即可通过 https://域名/文件名.txt 访问（内容原样下发）。支付宝小程序无需验证文件；
            部署容器重建后文件会丢失，重新保存一次即可。
          </n-alert>
          <n-form-item label="文件名">
            <n-input
              v-model:value="verifyForm.name"
              placeholder="如 MP_verify_xxx，不带后缀"
              :disabled="!isSuperAdmin"
              clearable
            >
              <template #suffix>
                <span style="color: var(--n-text-color-disabled, #999); font-size: 13px">.txt</span>
              </template>
            </n-input>
          </n-form-item>
          <n-form-item label="文件内容">
            <n-input
              v-model:value="verifyForm.content"
              type="textarea"
              :autosize="{ minRows: 3, maxRows: 6 }"
              maxlength="65536"
              :disabled="!isSuperAdmin"
              placeholder="粘贴校验文件的全部内容，原样复制不要改动"
            />
          </n-form-item>
          <n-space justify="end" class="mb-16">
            <n-button :disabled="!isSuperAdmin" @click="resetVerifyForm">重置</n-button>
            <n-button type="primary" :loading="verifySaving" :disabled="!isSuperAdmin" @click="saveVerify">
              保存验证文件
            </n-button>
          </n-space>
          <n-table v-if="verifyFiles.length" size="small" :single-line="false">
            <thead>
              <tr>
                <th>文件名（点击查看）</th>
                <th style="width: 90px">大小</th>
                <th style="width: 170px">更新时间</th>
                <th style="width: 150px">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="f in verifyFiles" :key="f.name">
                <td>
                  <n-a :href="verifyFileURL(f.path)" target="_blank" rel="noopener" style="word-break: break-all">
                    {{ f.name }}
                  </n-a>
                </td>
                <td>{{ f.size }} B</td>
                <td>{{ f.mod_time }}</td>
                <td>
                  <n-space size="small">
                    <n-button text size="tiny" type="primary" @click="fillVerifyName(f)">填入表单</n-button>
                    <n-button text size="tiny" type="error" :disabled="!isSuperAdmin" @click="removeVerify(f)">
                      删除
                    </n-button>
                  </n-space>
                </td>
              </tr>
            </tbody>
          </n-table>
        </n-form>
      </div>
    </n-spin>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'SiteSetting' })
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { refreshOnActivated } from '@/utils/activateRefresh'
import { useRoute } from 'vue-router'
import { useDialog, useMessage } from 'naive-ui'
import { getSiteSetting, updateSiteSetting, listVerifyFiles, saveVerifyFile, deleteVerifyFile } from '@/api/admin'
import type { SiteSetting, VerifyFileInfo } from '@/api/admin'
import { CODE_DATA_CONFLICT } from '@/api/request'
import type { BizError } from '@/api/request'
import ImageUploadInput from '@/components/ImageUploadInput.vue'
import RichTextEditor from '@/components/RichTextEditor.vue'
import { useAuthStore } from '@/stores/auth'

/**
 * 站点设置页面
 * 维护平台级部署域名与品牌信息；域名是全平台回调地址与附件外链的唯一基址来源
 */
const authStore = useAuthStore()
const isSuperAdmin = computed(() => authStore.isSuperAdmin)
const route = useRoute()

const loading = ref(false)
const saving = ref(false)
const message = useMessage()

const emptySetting = (): SiteSetting => ({
  base_url: '',
  site_name: '',
  logo_url: '',
  favicon_url: '',
  footer_html: '',
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
  footer_html: '',
  seo_description: '',
  seo_keywords: '',
})

const sourceLabels: Record<string, string> = {
  env: '环境变量 SITE_BASE_URL（默认值）',
  db: '本页站点设置',
  request: '按当前访问请求推导（未配置）',
}
const sourceLabel = computed(() => sourceLabels[setting.value.base_url_source] || '未知')
const sourceHint = computed(() =>
  setting.value.base_url_source === 'env'
    ? '；生效值已回填到输入框，保存后将以本页填写为准（环境变量仅在本页未配置时兑底）'
    : setting.value.base_url_source === 'request'
      ? '；建议填写正式域名，避免访问端口变化导致回调地址漂移'
      : '',
)

/**
 * 一键把「当前生效」地址填入输入框
 * 新站点此处常为空且按请求推导，手动复制麻烦；支付回调地址等派生值随保存自动刷新
 */
function fillEffective() {
  if (!setting.value.effective_base_url) return
  form.base_url = setting.value.effective_base_url
  message.success('已填入当前生效地址，保存后生效')
}

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
        footer_html: s.footer_html,
        seo_description: s.seo_description,
        seo_keywords: s.seo_keywords,
      })
      // 部署侧 SITE_BASE_URL 兑底且本页未配置时，把生效值回填进输入框：
      // 管理员可直接在其上修改，保存后 DB 有值即接管生效，环境变量退为兑底
      if (!s.base_url && s.base_url_source === 'env') {
        form.base_url = s.effective_base_url
      }
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
    // version 是载入时的配置版本号，随表单一起提交做乐观并发校验：
    // 他人已保存过则服务端返回 10007 拒绝，避免旧表单整份覆盖新配置
    const res = await updateSiteSetting({ ...form, version: setting.value.version })
    message.success('站点设置已保存，立即生效')
    // 附件访问域名此前未配置时已自动沿用本次保存的部署域名，提示用户去附件配置页确认
    if (res.data?.data?.attachment_domain_synced) {
      message.info('附件「自定义访问域名」此前未配置，已自动沿用本次保存的部署域名，可到附件配置页调整', { duration: 6000 })
    }
  } catch (err) {
    // 其余错误提示由请求拦截器统一弹出，表单保留用户输入；版本冲突须立即重拉配置刷新版本号，
    // 否则按同一份旧表单重试会一直撞同一个冲突
    if ((err as BizError).code !== CODE_DATA_CONFLICT) {
      return
    }
    // 重拉会把表单换成服务端最新值，未保存的编辑随之丢弃：必须说明原因，不做静默替换
    message.warning('站点配置已被他人更新，已刷新为最新配置，请重新填写后再保存')
  } finally {
    saving.value = false
  }
  // 保存成功后刷新版本号（派生结果同步）、冲突后丢弃旧表单；
  // 重拉失败不影响本次结论（都已提示），不能让 Promise 落成未处理拒绝
  await load().catch(() => undefined)
}

// keep-alive 缓存下切回本页不重新挂载，需重新拉取：
// 带着旧表单值整份 PUT 会覆盖他人刚改的配置，未保存的本地编辑以最新值为准丢弃
onMounted(() => {
  loadPage()
  scrollToAnchor()
})
refreshOnActivated(() => {
  loadPage()
  scrollToAnchor()
})

/**
 * 页面进入/切回时的全量刷新：站点设置与平台验证文件列表
 */
function loadPage() {
  load()
  loadVerifyFiles()
}

/**
 * 处理外部引导锚点（渠道管理 gzh/mp 卡片「域名校验文件」链接带 anchor=verify-files
 * 跳转过来时），平滑滚动到「平台验证文件」分区
 */
function scrollToAnchor() {
  if (route.query.anchor !== 'verify-files') return
  nextTick(() => {
    document.getElementById('verify-files')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  })
}

// ---------------- 平台验证文件 ----------------
// 微信域名归属校验文件（公众号 MP_verify_xxx.txt 纯文本 / 小程序业务域名 JSON 校验文件），
// 保存后落盘网关 verify/ 目录，按站点根路径伪静态下发，零查库
const dialog = useDialog()

const verifyFiles = ref<VerifyFileInfo[]>([])
const verifySaving = ref(false)
const verifyForm = reactive({ name: '', content: '' })

/** 站点根目录下的完整访问地址（后台与 C 端同域名部署） */
function verifyFileURL(path: string) {
  return `${window.location.origin}${path}`
}

/**
 * 加载验证文件列表
 */
async function loadVerifyFiles() {
  try {
    const res = await listVerifyFiles()
    if (res.data.code === 0) {
      verifyFiles.value = res.data.data?.list ?? []
    }
  } catch {
    // 错误提示由请求拦截器统一弹出
  }
}

/**
 * 重置验证文件表单
 */
function resetVerifyForm() {
  verifyForm.name = ''
  verifyForm.content = ''
}

/**
 * 保存验证文件（同主体名覆盖更新，后端强制补 .txt 后缀）
 */
async function saveVerify() {
  if (!verifyForm.name.trim() || !verifyForm.content) {
    message.warning('请填写文件名与文件内容')
    return
  }
  verifySaving.value = true
  try {
    const res = await saveVerifyFile({ file_name: verifyForm.name, content: verifyForm.content })
    message.success(`验证文件 ${res.data.data?.name} 已保存，可通过站点根路径访问`)
    resetVerifyForm()
    await loadVerifyFiles()
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    verifySaving.value = false
  }
}

/**
 * 把已生成文件的文件名主体填回表单（内容不从服务端回读，需从平台后台重新复制）
 */
function fillVerifyName(f: VerifyFileInfo) {
  verifyForm.name = f.base_name
  verifyForm.content = ''
  message.info('已填入文件名，内容请从平台后台重新复制后保存')
}

/**
 * 删除验证文件：删除后对应域名校验立即失效，需确认
 */
function removeVerify(f: VerifyFileInfo) {
  dialog.warning({
    title: '删除验证文件',
    content: `确定删除 ${f.name} 吗？删除后该文件对应的域名校验将立即失效。`,
    positiveText: '删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await deleteVerifyFile(f.name)
        message.success('验证文件已删除')
        await loadVerifyFiles()
      } catch {
        // 错误提示由请求拦截器统一弹出
      }
    },
  })
}
</script>

<style scoped>
.mb-16 {
  margin-bottom: 16px;
}
</style>
