<template>
  <n-card :title="isEdit ? '编辑优惠券' : '创建优惠券'">
    <n-alert v-if="isEdit && !isDraft" type="info" :show-icon="true" style="margin-bottom: 16px">
      该券{{ publishText }}：仅可「补充发放数量、停止/恢复发放、修改规则文案」；面额/门槛/时间/商品范围已锁定。
    </n-alert>

    <n-form :model="form" label-placement="left" label-width="120px" style="max-width: 720px">
      <n-form-item label="名称" required>
        <n-input v-model:value="form.name" :disabled="!isDraft" />
      </n-form-item>
      <n-form-item label="类型" required>
        <n-select v-model:value="form.type" :options="typeOptions" :disabled="!isDraft" />
      </n-form-item>
      <n-form-item label="使用门槛" required>
        <n-input-number v-model:value="form.threshold" :min="0" :precision="2" :disabled="!isDraft">
          <template #suffix>元</template>
        </n-input-number>
      </n-form-item>
      <n-form-item :label="form.type === 2 ? '折扣率(如0.85)' : '减免金额'" required>
        <n-input-number v-model:value="form.discount_value" :min="0" :precision="form.type === 2 ? 2 : 2" :disabled="!isDraft" />
      </n-form-item>
      <n-form-item label="发放总量" required>
        <n-input-number v-model:value="form.total_count" :min="editMinTotal" :precision="0" />
        <span v-if="isEdit && !isDraft" class="field-tip">已领 {{ current?.received_count ?? 0 }} 张，仅可增加</span>
      </n-form-item>
      <n-form-item label="每人限领">
        <n-input-number v-model:value="form.per_user_limit" :min="1" :precision="0" :disabled="!isDraft" />
      </n-form-item>

      <n-divider title-placement="left">时间</n-divider>
      <n-form-item label="领取时间窗口">
        <n-date-picker v-model:value="grantDateRange" type="datetimerange" clearable :disabled="!isDraft" />
      </n-form-item>
      <n-form-item label="到期方式">
        <n-radio-group v-model:value="form.expire_type" :disabled="!isDraft">
          <n-radio :value="1">领取后 N 天有效</n-radio>
          <n-radio :value="2">固定可用范围</n-radio>
        </n-radio-group>
      </n-form-item>
      <n-form-item v-if="form.expire_type === 1" label="领取后有效天数">
        <n-input-number v-model:value="form.valid_days" :min="0" :precision="0" :disabled="!isDraft">
          <template #suffix>天</template>
        </n-input-number>
        <span class="field-tip">0 = 跟随领取窗口结束时间</span>
      </n-form-item>
      <n-form-item v-else label="可用范围">
        <n-date-picker v-model:value="useDateRange" type="datetimerange" clearable :disabled="!isDraft" />
      </n-form-item>

      <n-divider title-placement="left">适用范围与展示</n-divider>
      <n-form-item label="商品范围">
        <n-space vertical style="width: 100%">
          <n-button dashed :disabled="!isDraft" @click="showScopePicker = true">+ 选择适用/排除的分类与商品</n-button>
          <div v-if="pickedScopes.length" class="scope-tags">
            <n-tag
              v-for="(s, i) in pickedScopes"
              :key="`${s.type}-${s.id}`"
              size="small"
              closable
              :disabled="!isDraft"
              :type="s.kind === 'include' ? 'primary' : 'warning'"
              @close="removeScope(i)"
            >
              {{ scopeTagText(s) }}
            </n-tag>
          </div>
          <span v-else class="field-tip">全场通用</span>
        </n-space>
      </n-form-item>
      <n-form-item label="进入领券中心">
        <n-switch v-model:value="centerVisible" :disabled="!isDraft" />
        <span class="field-tip">关闭后券仅可通过手动发放（二期）触达用户</span>
      </n-form-item>
      <n-form-item label="规则文案">
        <n-input
          v-model:value="form.rule_text"
          type="textarea"
          :rows="3"
          maxlength="1000"
          show-count
          placeholder="展示给用户的规则说明，可手写；一键生成功能随后续版本提供"
        />
      </n-form-item>

      <n-form-item label="保存方式" v-if="!isEdit">
        <n-radio-group v-model:value="publishMode">
          <n-radio value="draft">存为草稿</n-radio>
          <n-radio value="publish">直接发布</n-radio>
        </n-radio-group>
      </n-form-item>
      <n-form-item label=" ">
        <n-space>
          <n-button type="primary" :loading="saving" @click="submit">保存</n-button>
          <n-button @click="router.back()">返回</n-button>
        </n-space>
      </n-form-item>
    </n-form>
  </n-card>

  <!-- 商品/分类范围选择弹窗（公共组件） -->
  <ProductScopePicker
    v-model:show="showScopePicker"
    title="选择商品范围"
    :selected="pickedScopes.map((s) => ({ type: s.type, id: s.id, name: s.name }))"
    @confirm="onScopeConfirm"
  />
</template>

<script setup lang="ts">
defineOptions({ name: 'CouponForm' })
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import ProductScopePicker from '@/components/ProductScopePicker.vue'
import { getCouponList, createCoupon, updateCoupon } from '@/api/marketing'
import type { Coupon, CouponScope } from '@/api/types'

/**
 * 创建/编辑优惠券页面（M 第①期重构）
 * - 状态机：创建可存草稿或直接发布；列表页可发布/下架/停发恢复
 * - 到期类型：领取后 N 天 / 固定可用范围（use_start_time/use_end_time），与领取窗口分离
 * - 商品范围：复用 ProductScopePicker，含「适用」与「排除」两类标签
 * - 编辑边界（决策点⑧）：草稿全字段；已发布/已下架仅 补充数量/停发恢复/规则文案（后端同步裁剪）
 */

/** 表单内范围项：type 粒度 + kind 适用/排除 */
interface PickedScope {
  type: 'category' | 'product'
  id: number
  name: string
  kind: 'include' | 'exclude'
}

const route = useRoute()
const router = useRouter()
const message = useMessage()

const isEdit = computed(() => !!route.params.id)
const saving = ref(false)
const current = ref<Coupon | null>(null)
const publishMode = ref<'draft' | 'publish'>('draft')
const showScopePicker = ref(false)
const pickedScopes = ref<PickedScope[]>([])
const centerVisible = ref(true)

const grantDateRange = ref<[number, number] | null>(null)
const useDateRange = ref<[number, number] | null>(null)

const form = ref<Partial<Coupon>>({
  name: '',
  type: 1,
  threshold: 0,
  discount_value: 0,
  total_count: 100,
  per_user_limit: 1,
  valid_days: 0,
  expire_type: 1,
  rule_text: '',
})

const typeOptions = [
  { label: '满减券', value: 1 },
  { label: '折扣券', value: 2 },
]

const isDraft = computed(() => !isEdit.value || current.value?.publish_status === 0)

const publishText = computed(() => {
  if (current.value?.publish_status === 1) return '已发布'
  if (current.value?.publish_status === 2) return '已下架'
  return ''
})

/** 已发布态补充数量不可低于当前已领取量 */
const editMinTotal = computed(() => (isEdit.value && !isDraft.value ? current.value?.received_count ?? 0 : 0))

function scopeTagText(s: PickedScope): string {
  const target = s.type === 'category' ? `${s.name}（全部商品）` : s.name
  return s.kind === 'include' ? `适用:${target}` : `排除:${target}`
}

onMounted(async () => {
  if (!isEdit.value) return
  const id = Number(route.params.id)
  const res = await getCouponList({ page: 1, page_size: 200 })
  if (res.data.code !== 0) return
  const row = (res.data.data.list || []).find((x) => x.id === id)
  if (!row) {
    message.error('优惠券不存在或已删除')
    router.back()
    return
  }
  current.value = row
  form.value = { ...row }
  centerVisible.value = row.is_center_visible === 1
  pickedScopes.value = (row.scopes || []).map((s: CouponScope) => {
    const kind: 'include' | 'exclude' = s.scope_type === 4 || s.scope_type === 5 ? 'exclude' : 'include'
    if (s.scope_type === 2 || s.scope_type === 4) {
      return { type: 'category' as const, id: s.category_id, name: s.category_name || `分类#${s.category_id}`, kind }
    }
    return { type: 'product' as const, id: s.product_id, name: s.product_name || `商品#${s.product_id}`, kind }
  })
  if (row.start_time && row.end_time) {
    grantDateRange.value = [toTs(row.start_time), toTs(row.end_time)]
  }
  if (row.use_start_time && row.use_end_time) {
    useDateRange.value = [toTs(row.use_start_time), toTs(row.use_end_time)]
  }
})

function toTs(s: string): number {
  return new Date(s.replace(' ', 'T')).getTime()
}

/**
 * 范围弹窗确认：判断新增项是「适用」还是「排除」
 * 弹窗本身不区分语义，以打开前已存在项为准：新项一律归为「适用」，由用户删除重加来调整
 * （范围项数量有限，排除项通常在草稿期精修）
 */
function onScopeConfirm(items: { type: 'category' | 'product'; id: number; name: string }[]) {
  const next: PickedScope[] = []
  for (const it of items) {
    const existing = pickedScopes.value.find((p) => p.type === it.type && p.id === it.id)
    next.push(existing || { ...it, kind: 'include' })
  }
  pickedScopes.value = next
  showScopePicker.value = false
}

function removeScope(index: number) {
  pickedScopes.value.splice(index, 1)
}

function formatTime(ts: number) {
  const d = new Date(ts)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:00`
}

/** 表单范围项 → API scopes */
function buildScopes(): CouponScope[] {
  return pickedScopes.value.map((s) => {
    if (s.kind === 'include') {
      return s.type === 'category'
        ? { scope_type: 2, category_id: s.id, product_id: 0 }
        : { scope_type: 3, category_id: 0, product_id: s.id }
    }
    return s.type === 'category'
      ? { scope_type: 4, category_id: s.id, product_id: 0 }
      : { scope_type: 5, category_id: 0, product_id: s.id }
  })
}

async function submit() {
  if (!form.value.name?.trim()) {
    message.error('请填写优惠券名称')
    return
  }
  if (form.value.type === 2 && (form.value.discount_value ?? 0) <= 0) {
    message.error('折扣率必须大于 0')
    return
  }
  saving.value = true
  try {
    const data: Record<string, unknown> = {
      name: form.value.name?.trim(),
      type: form.value.type,
      threshold: form.value.threshold ?? 0,
      discount_value: form.value.discount_value ?? 0,
      total_count: form.value.total_count ?? 0,
      per_user_limit: form.value.per_user_limit ?? 1,
      valid_days: form.value.expire_type === 1 ? form.value.valid_days ?? 0 : 0,
      expire_type: form.value.expire_type ?? 1,
      is_center_visible: centerVisible.value,
      rule_text: form.value.rule_text ?? '',
    }
    if (grantDateRange.value) {
      data.start_time = formatTime(grantDateRange.value[0])
      data.end_time = formatTime(grantDateRange.value[1])
    } else {
      data.start_time = ''
      data.end_time = ''
    }
    if (form.value.expire_type === 2 && useDateRange.value) {
      data.use_start_time = formatTime(useDateRange.value[0])
      data.use_end_time = formatTime(useDateRange.value[1])
    } else {
      data.use_start_time = ''
      data.use_end_time = ''
    }
    if (isDraft.value) {
      // 草稿态：范围全量提交
      data.scopes = buildScopes()
    }

    if (!isEdit.value) {
      data.publish_status = publishMode.value === 'publish' ? 1 : 0
      const res = await createCoupon(data as Partial<Coupon>)
      if (res.data.code === 0) {
        message.success('保存成功')
        router.back()
      }
      return
    }

    // 编辑：非草稿仅提交允许字段（后端同步裁剪兜底）
    if (!isDraft.value) {
      const patch: Record<string, unknown> = {
        total_count: form.value.total_count,
        grant_status: current.value?.grant_status ?? 1,
        rule_text: form.value.rule_text ?? '',
      }
      const res = await updateCoupon(Number(route.params.id), patch as Partial<Coupon>)
      if (res.data.code === 0) {
        message.success('保存成功')
        router.back()
      }
      return
    }

    const res = await updateCoupon(Number(route.params.id), data as Partial<Coupon>)
    if (res.data.code === 0) {
      message.success('保存成功')
      router.back()
    }
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.scope-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.field-tip {
  margin-left: 10px;
  font-size: 12px;
  color: #999;
}
</style>
