<template>
  <div class="login-page">
    <!-- 品牌展示区：≥900px 显示，纯 CSS 装饰不承载信息 -->
    <section class="brand-pane">
      <div class="brand-inner">
        <div class="brand-logo">
          <img v-if="branding.logoURL" :src="branding.logoURL" alt="站点 Logo" class="logo-img" />
          <div v-else class="logo-badge" aria-hidden="true">
            <n-icon :component="CartOutline" :size="24" />
          </div>
          <span class="brand-name">{{ branding.siteName }}系统</span>
        </div>
        <div class="brand-hero">
          <h1 class="brand-slogan">让社区生意，变得更简单</h1>
          <p class="brand-sub">商品 · 库存 · 订单 · 营销 · 收银，一站式经营</p>
        </div>
        <ul class="brand-feats">
          <li v-for="feat in feats" :key="feat" class="feat">
            <span class="feat-ic" aria-hidden="true">
              <n-icon :component="CheckmarkOutline" :size="13" />
            </span>
            {{ feat }}
          </li>
        </ul>
      </div>
      <span class="deco-ring ring-a" aria-hidden="true"></span>
      <span class="deco-ring ring-b" aria-hidden="true"></span>
      <span class="deco-dot dot-a" aria-hidden="true"></span>
      <span class="deco-dot dot-b" aria-hidden="true"></span>
    </section>

    <!-- 登录表单区 -->
    <section class="form-pane">
      <div class="form-box">
        <div class="form-head">
          <!-- 移动端顶部渐变头部中的徽标，桌面端由品牌区承载品牌信息 -->
          <div class="logo-badge logo-badge--light" aria-hidden="true">
            <n-icon :component="CartOutline" :size="24" />
          </div>
          <h2 class="welcome">欢迎回来 👋</h2>
          <p class="welcome-sub">请使用管理员账号登录后台</p>
        </div>
        <n-form
          ref="formRef"
          :model="formData"
          :rules="rules"
          label-placement="top"
          size="large"
        >
          <n-form-item path="username">
            <n-input
              v-model:value="formData.username"
              placeholder="请输入管理员账号"
              :input-props="{ autocomplete: 'username' }"
            >
              <template #prefix>
                <n-icon :component="PersonOutline" />
              </template>
            </n-input>
          </n-form-item>
          <n-form-item path="password">
            <n-input
              v-model:value="formData.password"
              type="password"
              placeholder="请输入密码"
              show-password-on="mousedown"
              :input-props="{ autocomplete: 'current-password' }"
              @keydown.enter="onLogin"
            >
              <template #prefix>
                <n-icon :component="LockClosedOutline" />
              </template>
            </n-input>
          </n-form-item>
          <n-button
            type="primary"
            block
            size="large"
            class="submit-btn"
            :loading="loading"
            @click="onLogin"
          >登 录</n-button>
        </n-form>
        <p class="form-foot" v-html="footHTML"></p>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import type { FormInst, FormRules } from 'naive-ui'
import { CartOutline, CheckmarkOutline, LockClosedOutline, PersonOutline } from '@vicons/ionicons5'
import { useAuthStore } from '@/stores/auth'
import { useBrandingStore } from '@/stores/branding'
import { resolveHomePath } from '@/router'

const route = useRoute()
const router = useRouter()
const message = useMessage()
const authStore = useAuthStore()
const branding = useBrandingStore()

/** 表单引用 */
const formRef = ref<FormInst | null>(null)
/** 登录加载状态 */
const loading = ref(false)
/** 版权年份 */
const year = new Date().getFullYear()
/** 页脚内容：优先展示站点设置里的页脚富文本（服务端已消毒），未配置时回落默认署名 */
const footHTML = computed(() => branding.footerHTML || `© ${year} ${branding.siteName}管理平台`)

onMounted(() => branding.load())

/** 品牌区产品特性文案 */
const feats = [
  '实时库存与预警，缺货早知道',
  '优惠券、秒杀、满减灵活配置',
  '收银台与后台数据实时联动',
]

/** 登录表单数据 */
const formData = ref({
  username: '',
  password: '',
})

/** 表单校验规则 */
const rules: FormRules = {
  username: [{ required: true, message: '请输入账号', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

/**
 * 解析登录成功后的回跳地址
 * 优先取守卫携带的 redirect 参数，校验合法性防止开放重定向；
 * 无有效 redirect 时按权限解析兜底首页
 * @returns 回跳目标路径
 */
function resolveRedirectPath(): string {
  const redirect = route.query.redirect
  if (typeof redirect === 'string' && redirect.startsWith('/') && !redirect.startsWith('//') && redirect !== '/login') {
    return redirect
  }
  return resolveHomePath(authStore) ?? '/403'
}

/**
 * 登录提交
 * 校验表单后调用登录接口，成功后优先回跳原访问页面（无 redirect 时按权限跳转兜底首页）
 */
async function onLogin() {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }

  loading.value = true
  try {
    await authStore.login(formData.value)
    message.success('登录成功')
    router.push(resolveRedirectPath())
  } catch (error: unknown) {
    const errMsg = error instanceof Error ? error.message : '登录失败，请重试'
    message.error(errMsg)
  } finally {
    loading.value = false
  }
}
</script>

<style lang="scss" scoped>
.login-page {
  min-height: 100vh;
  display: flex;
  background: #fff;
}

/* ---------- 品牌展示区（≥900px） ---------- */
.brand-pane {
  flex: 1.1;
  position: relative;
  overflow: hidden;
  display: flex;
  align-items: center;
  padding: 48px 56px;
  color: #fff;
  background: linear-gradient(150deg, #047a3e 0%, #07c160 62%, #2ad47a 100%);
}

.brand-inner {
  position: relative;
  z-index: 1;
  display: flex;
  flex-direction: column;
  gap: 44px;
  max-width: 460px;
}

.logo-badge {
  width: 44px;
  height: 44px;
  flex-shrink: 0;
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.16);
  border: 1px solid rgba(255, 255, 255, 0.35);
  display: flex;
  align-items: center;
  justify-content: center;
}

.logo-img {
  width: 44px;
  height: 44px;
  flex-shrink: 0;
  border-radius: 12px;
  object-fit: contain;
  background: rgba(255, 255, 255, 0.16);
}

.brand-logo {
  display: flex;
  align-items: center;
  gap: 12px;
}

.brand-name {
  font-size: 17px;
  font-weight: 600;
  letter-spacing: 0.5px;
}

.brand-slogan {
  margin: 0;
  font-size: 30px;
  line-height: 1.35;
  font-weight: 600;
}

.brand-sub {
  margin: 12px 0 0;
  font-size: 15px;
  color: rgba(255, 255, 255, 0.85);
}

.brand-feats {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.feat {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 14px;
  color: rgba(255, 255, 255, 0.92);
}

.feat-ic {
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.18);
  display: flex;
  align-items: center;
  justify-content: center;
}

/* 装饰圆环与光点 */
.deco-ring {
  position: absolute;
  border-radius: 50%;
  border: 1.5px solid rgba(255, 255, 255, 0.18);
}

.ring-a {
  width: 320px;
  height: 320px;
  right: -100px;
  top: -100px;
}

.ring-b {
  width: 200px;
  height: 200px;
  right: 60px;
  bottom: -90px;
  border-style: dashed;
}

.deco-dot {
  position: absolute;
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.35);
}

.dot-a {
  left: 56px;
  bottom: 80px;
}

.dot-b {
  right: 130px;
  top: 130px;
}

/* ---------- 登录表单区 ---------- */
.form-pane {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 40px 32px;
}

.form-box {
  width: 360px;
  max-width: 100%;
}

.form-head {
  margin-bottom: 28px;
}

.logo-badge--light {
  display: none;
  margin-bottom: 16px;
}

.welcome {
  margin: 0;
  font-size: 24px;
  line-height: 32px;
  font-weight: 600;
  color: #171717;
}

.welcome-sub {
  margin: 6px 0 0;
  font-size: 14px;
  color: #7a7b82;
}

:deep(.n-input) {
  border-radius: 8px;
}

:deep(.n-input__prefix) {
  color: #b8b9bf;
}

.submit-btn {
  margin-top: 8px;
  height: 44px;
  border-radius: 8px;
  font-size: 16px;
  letter-spacing: 4px;
  box-shadow: 0 6px 16px rgba(7, 193, 96, 0.3);
}

.form-foot {
  margin: 24px 0 0;
  text-align: center;
  font-size: 12px;
  color: #b0b0b6;
}

/* ---------- 窄屏（<900px）：单栏布局 ---------- */
@media (max-width: 899px) {
  .brand-pane {
    display: none;
  }

  .form-pane {
    align-items: stretch;
    padding: 0;
  }

  .form-box {
    width: 100%;
    padding: 0 24px 40px;
  }

  .form-head {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    margin: 0 -24px 24px;
    padding: 44px 24px 26px;
    color: #fff;
    border-radius: 0 0 20px 20px;
    background: linear-gradient(150deg, #047a3e 0%, #07c160 100%);
  }

  .logo-badge--light {
    display: flex;
  }

  .welcome {
    color: #fff;
  }

  .welcome-sub {
    color: rgba(255, 255, 255, 0.85);
  }
}
</style>
