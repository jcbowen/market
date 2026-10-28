<template>
  <n-card title="售后配置">
    <n-spin :show="loading" style="max-width: 560px">
      <n-form label-placement="left" label-width="110">
        <n-form-item label="运费退还策略">
          <n-radio-group v-model:value="policy">
            <n-space vertical>
              <n-radio value="always">始终退运费（整单退款时随退款金额一并退还运费）</n-radio>
              <n-radio value="never">不退运费（整单退款时仅退商品金额）</n-radio>
              <n-radio value="user_decide">由用户选择（用户发起整单退款时自行勾选是否退运费）</n-radio>
            </n-space>
          </n-radio-group>
        </n-form-item>
        <n-form-item label="售后窗口（小时）">
          <n-input-number v-model:value="completedRefundHours" :min="0" :max="8760" :step="24" style="width: 200px" />
          <span class="window-hint">{{ windowHint }}</span>
        </n-form-item>
        <n-alert type="info" :show-icon="true" style="margin-top: 4px">
          仅整单退款涉及运费；部分商品退款不退还运费。售后窗口仅约束 C 端自助申请（以订单完成时间起算），
          后台代客退款不受窗口限制；填 0 表示已完成订单不允许在线申请售后。保存后立即生效。
        </n-alert>
        <n-space justify="end" style="margin-top: 16px">
          <n-button type="primary" :loading="saving" @click="save">保存配置</n-button>
        </n-space>
      </n-form>
    </n-spin>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'RefundSetting' })
import { ref, computed, onMounted } from 'vue'
import { refreshOnActivated } from '@/utils/activateRefresh'
import { useMessage } from 'naive-ui'
import { getRefundSetting, updateRefundSetting } from '@/api/order'

/**
 * 售后配置页面
 * 配置整单退款时的运费退还策略（始终退/不退/用户选择）
 * 与已完成单售后窗口（完成后 N 小时内 C 端可自助申请退款，0=关闭）
 */
const loading = ref(false)
const saving = ref(false)
const policy = ref('always')
const completedRefundHours = ref<number | null>(168)
const message = useMessage()

/** 窗口提示文案：整 24 小时按「天」表述，0 提示已关闭 */
const windowHint = computed(() => {
  const h = completedRefundHours.value ?? 0
  if (h === 0) return '已完成订单不允许在线申请售后'
  if (h > 0 && h % 24 === 0) return `即完成后 ${h / 24} 天内可申请`
  return `即完成后 ${h} 小时内可申请`
})

/**
 * 加载当前退款配置
 */
async function load() {
  loading.value = true
  try {
    const res = await getRefundSetting()
    if (res.data.code === 0 && res.data.data.refund_freight_policy) {
      policy.value = res.data.data.refund_freight_policy
    }
    if (res.data.code === 0 && typeof res.data.data.completed_refund_hours === 'number') {
      completedRefundHours.value = res.data.data.completed_refund_hours
    }
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    loading.value = false
  }
}

/**
 * 保存退款配置
 */
async function save() {
  const hours = completedRefundHours.value
  if (hours === null || hours < 0) {
    message.error('售后窗口不能为空或负数')
    return
  }
  saving.value = true
  try {
    await updateRefundSetting(policy.value, hours)
    message.success('售后配置已保存，保存后即时生效，无需重启服务')
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    saving.value = false
  }
}

// keep-alive 缓存下切回本页不重新挂载，需重新拉取：
// 带着旧表单值整份 PUT 会覆盖他人刚改的配置，未保存的本地编辑以最新值为准丢弃
onMounted(load)
refreshOnActivated(load)
</script>

<style scoped>
.window-hint {
  margin-left: 8px;
  color: var(--n-text-color-disabled, #999);
  font-size: 12px;
}
</style>
