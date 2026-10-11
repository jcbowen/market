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
        <n-alert type="info" :show-icon="true" style="margin-top: 4px">
          仅整单退款涉及运费；部分商品退款不退还运费。保存后立即生效。
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
import { ref, onMounted } from 'vue'
import { useMessage } from 'naive-ui'
import { getRefundSetting, updateRefundSetting } from '@/api/order'

/**
 * 售后配置页面
 * 配置整单退款时的运费退还策略（始终退/不退/用户选择）
 */
const loading = ref(false)
const saving = ref(false)
const policy = ref('always')
const message = useMessage()

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
  saving.value = true
  try {
    await updateRefundSetting(policy.value)
    message.success('售后配置已保存，保存后即时生效，无需重启服务')
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
