<template>
  <div class="about-page">
    <!-- 产品信息头：logo + 产品名 + 版本徽章 -->
    <n-card :bordered="false" class="hero-card">
      <div class="hero">
        <div class="hero-logo">
          <n-icon size="32" color="#fff"><storefront-outline /></n-icon>
        </div>
        <div class="hero-info">
          <div class="hero-name">社区商超</div>
          <div class="hero-desc">社区团购与门店收银一体化系统</div>
        </div>
        <div class="hero-version-wrap">
          <n-tag type="success" size="large" round :bordered="false" class="hero-version">
            {{ about?.version || '—' }}
          </n-tag>
          <!-- 更新检测：仅在启用且发现新版本时提示最新版号 -->
          <n-tag v-if="update?.enabled && update.has_update" size="small" round :bordered="false" class="hero-update">
            发现新版本 {{ update.latest_version }}
          </n-tag>
        </div>
      </div>
    </n-card>

    <!-- 运行环境：后端接口返回的运行时真实版本 -->
    <n-card title="运行环境" :bordered="false">
      <n-descriptions label-placement="left" :column="1" bordered size="small">
        <n-descriptions-item label="运行模式">
          <n-spin v-if="loading" :size="14" />
          <!-- 生产=主题绿实心（正式/醒目），开发=中性灰（弱化），色彩收敛到主题色系 -->
          <n-tag v-else-if="about?.app_env === 'production'" type="success" size="small" :bordered="false">
            生产环境
          </n-tag>
          <n-tag v-else size="small" :bordered="false">开发环境</n-tag>
        </n-descriptions-item>
        <n-descriptions-item label="MySQL 版本">
          <n-spin v-if="loading" :size="14" />
          <span v-else>{{ about?.mysql_version || '-' }}</span>
        </n-descriptions-item>
        <n-descriptions-item label="Redis 版本">
          <n-spin v-if="loading" :size="14" />
          <span v-else>{{ about?.redis_version || '-' }}</span>
        </n-descriptions-item>
      </n-descriptions>
    </n-card>

    <!-- 部署运行方式：运行时自动检测结果（all-in-one / compose / binary 三态） -->
    <n-card title="部署运行方式" :bordered="false">
      <div class="deploy-item">
        <div class="deploy-title">
          <n-icon size="16" color="#07c160">
            <logo-docker v-if="about?.deploy_mode === 'all-in-one'" />
            <git-network-outline v-else-if="about?.deploy_mode === 'compose'" />
            <server-outline v-else />
          </n-icon>
          <n-spin v-if="loading" :size="14" />
          <template v-else-if="about?.deploy_mode === 'all-in-one'">Docker 一体化容器运行</template>
          <template v-else-if="about?.deploy_mode === 'compose'">Docker 多容器编排运行</template>
          <template v-else>二进制直接运行</template>
        </div>
        <div class="deploy-desc">
          {{
            about?.deploy_mode === 'all-in-one'
              ? '当前服务运行于 Docker 一体化容器内（单容器承载 MySQL、Redis、全部后端服务与前端静态资源）'
              : about?.deploy_mode === 'compose'
                ? '当前服务以 Docker Compose 多容器方式运行（MySQL、Redis 与各服务独立容器，经容器网络互通）'
                : '当前服务以二进制方式直接运行于宿主机（MySQL / Redis 由部署环境提供）'
          }}
        </div>
      </div>
    </n-card>

    <!-- 技术支持 -->
    <n-card title="技术支持" :bordered="false">
      <div class="support-row">
        <n-icon size="18" color="#07c160"><mail-outline /></n-icon>
        <a class="support-mail" :href="`mailto:${SUPPORT_EMAIL}`">{{ SUPPORT_EMAIL }}</a>
        <n-button size="tiny" secondary @click="copyEmail">复制</n-button>
      </div>
      <div class="support-tip">使用问题、功能建议或缺陷反馈，欢迎通过邮件联系</div>
    </n-card>

    <div class="about-footer">社区商超 · 让社区生意更简单</div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useMessage } from 'naive-ui'
import { GitNetworkOutline, LogoDocker, MailOutline, ServerOutline, StorefrontOutline } from '@vicons/ionicons5'
import { getAbout, getUpdateCheck, type SystemAbout, type UpdateCheck } from '@/api/about'

/**
 * 关于页面
 * 展示项目版本号（编译时注入 git tag）、运行时 MySQL/Redis 真实版本、
 * 部署运行方式与技术支持邮箱；版本数据来自 /api/v1/admin/about，
 * 更新提示来自 /api/v1/admin/about/updates（Docker Hub 最新版本比对）
 */
const message = useMessage()

/** 技术支持邮箱（页面静态常量） */
const SUPPORT_EMAIL = 'jiuqi@jiuchet.com'

const about = ref<SystemAbout | null>(null)
const update = ref<UpdateCheck | null>(null)
const loading = ref(true)

/**
 * 复制技术支持邮箱到剪贴板
 * 优先 Clipboard API；非安全上下文（http 部署）下降级为隐藏 textarea + execCommand
 */
async function copyEmail() {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(SUPPORT_EMAIL)
    } else {
      const textarea = document.createElement('textarea')
      textarea.value = SUPPORT_EMAIL
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      document.body.removeChild(textarea)
    }
    message.success('已复制邮箱')
  } catch {
    message.error('复制失败，请手动复制')
  }
}

/**
 * 加载系统信息（版本号 + MySQL/Redis 版本）与更新检测结果
 * 更新检测独立异步加载（外网请求较慢），失败静默不打扰主信息展示
 */
async function loadAbout() {
  try {
    const res = await getAbout()
    about.value = res.data.data
  } finally {
    loading.value = false
  }
  try {
    const res = await getUpdateCheck()
    update.value = res.data.data
  } catch {
    // 检测失败静默：不提示更新即可
  }
}

onMounted(loadAbout)
</script>

<style lang="scss" scoped>
.about-page {
  max-width: 860px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.hero-card {
  background: linear-gradient(135deg, #07c160 0%, #05a04f 100%);
  .hero {
    display: flex;
    align-items: center;
    gap: 16px;
  }
  .hero-logo {
    width: 64px;
    height: 64px;
    flex-shrink: 0;
    border-radius: 16px;
    background: rgba(255, 255, 255, 0.2);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .hero-info {
    flex: 1;
    min-width: 0;
  }
  .hero-name {
    font-size: 22px;
    font-weight: 600;
    color: #fff;
  }
  .hero-desc {
    margin-top: 4px;
    font-size: 13px;
    color: rgba(255, 255, 255, 0.85);
  }
  .hero-version-wrap {
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 6px;
  }
  .hero-version {
    font-weight: 600;
    background: rgba(255, 255, 255, 0.92);
    color: #05a04f;
  }
  .hero-update {
    background: rgba(255, 255, 255, 0.25);
    color: #fff;
  }
}
.deploy-item {
  &:not(:last-child) {
    margin-bottom: 16px;
  }
}
.deploy-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 14px;
  font-weight: 600;
  color: #333;
}
.deploy-desc {
  margin-top: 6px;
  padding-left: 22px;
  font-size: 13px;
  line-height: 1.7;
  color: #666;
}
.support-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.support-mail {
  color: #07c160;
  font-size: 15px;
  font-weight: 500;
  text-decoration: none;
  &:hover {
    text-decoration: underline;
  }
}
.support-tip {
  margin-top: 8px;
  font-size: 13px;
  color: #999;
}
.about-footer {
  text-align: center;
  font-size: 12px;
  color: #bbb;
  padding: 8px 0 4px;
}
</style>
