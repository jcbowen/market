<template>
  <n-card :title="isEdit ? '编辑免配送费活动' : '创建免配送费活动'">
    <n-form :model="form" label-placement="left" label-width="120px" style="max-width: 720px">
      <n-form-item label="活动名称" required>
        <n-input v-model:value="form.name" placeholder="如：满39元免配送费" />
      </n-form-item>
      <n-form-item label="免配送费门槛" required>
        <n-input-number v-model:value="form.threshold" :min="0" :precision="2">
          <template #suffix>元</template>
        </n-input-number>
      </n-form-item>
      <n-form-item label="活动时间">
        <n-date-picker v-model:value="dateRange" type="datetimerange" clearable />
      </n-form-item>
      <n-form-item label="排除范围">
        <n-space vertical style="width: 100%">
          <n-button dashed @click="showScopePicker = true">+ 选择排除的分类 / 商品</n-button>
          <div v-if="pickedScopes.length" class="scope-tags">
            <n-tag
              v-for="(s, i) in pickedScopes"
              :key="`${s.type}-${s.id}`"
              size="small"
              closable
              :type="s.type === 'category' ? 'info' : 'primary'"
              @close="removeScope(i)"
            >
              {{ s.type === 'category' ? `${s.name}（全部商品）` : s.name }}
            </n-tag>
          </div>
          <span v-else class="scope-none">未配置排除范围（全部商品参与免配送费）</span>
        </n-space>
      </n-form-item>
      <n-form-item label="状态">
        <n-radio-group v-model:value="form.status">
          <n-radio :value="1">启用</n-radio>
          <n-radio :value="0">禁用</n-radio>
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
    title="选择排除范围"
    :selected="pickedScopes"
    @confirm="onScopeConfirm"
  />
</template>

<script setup lang="ts">
defineOptions({ name: 'FreeShippingForm' })
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import ProductScopePicker, { type ScopeItem } from '@/components/ProductScopePicker.vue'
import { getFreeShippingList, createFreeShipping, updateFreeShipping } from '@/api/marketing'
import type { FreeShipping, FreeShippingScope } from '@/api/types'

/**
 * 创建/编辑免配送费活动页面
 * - 创建：POST /admin/free-shippings（status 默认启用）
 * - 编辑：按路由 id 从列表接口取详情回填；scopes 全量提交替换
 * - 排除范围复用公共组件 ProductScopePicker（分类=该分类全部商品 / 具体商品）
 */
const route = useRoute()
const router = useRouter()
const message = useMessage()

const isEdit = computed(() => !!route.params.id)
const saving = ref(false)
const dateRange = ref<[number, number] | null>(null)
const showScopePicker = ref(false)
const pickedScopes = ref<ScopeItem[]>([])

const form = ref<Partial<FreeShipping>>({
  name: '',
  threshold: 0,
  status: 1,
})

onMounted(async () => {
  if (!isEdit.value) return
  const id = Number(route.params.id)
  const res = await getFreeShippingList({ page: 1, page_size: 100 })
  if (res.data.code !== 0) return
  const row = (res.data.data.list || []).find((x) => x.id === id)
  if (!row) {
    message.error('活动不存在或已删除')
    router.back()
    return
  }
  form.value = { ...row }
  pickedScopes.value = (row.scopes || []).map((s: FreeShippingScope) =>
    s.scope_type === 4
      ? { type: 'category' as const, id: s.category_id, name: s.category_name || `分类#${s.category_id}` }
      : { type: 'product' as const, id: s.product_id, name: s.product_name || `商品#${s.product_id}` }
  )
  if (row.start_time && row.end_time) {
    dateRange.value = [new Date(row.start_time.replace(' ', 'T')).getTime(), new Date(row.end_time.replace(' ', 'T')).getTime()]
  }
})

/** 范围弹窗确认：整体替换当前已选 */
function onScopeConfirm(items: ScopeItem[]) {
  pickedScopes.value = items
  showScopePicker.value = false
}

function removeScope(index: number) {
  pickedScopes.value.splice(index, 1)
}

function formatTime(ts: number) {
  const d = new Date(ts)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:00`
}

async function submit() {
  if (!form.value.name?.trim()) {
    message.error('请填写活动名称')
    return
  }
  if (!form.value.threshold || form.value.threshold <= 0) {
    message.error('请填写大于 0 的免配送费门槛')
    return
  }
  saving.value = true
  try {
    const scopes = pickedScopes.value.map((s) =>
      s.type === 'category'
        ? { scope_type: 4, category_id: s.id, product_id: 0 }
        : { scope_type: 5, category_id: 0, product_id: s.id }
    )
    const data: Partial<FreeShipping> = {
      name: form.value.name.trim(),
      threshold: form.value.threshold,
      status: form.value.status ?? 1,
      // 始终携带 scopes（可能为空数组=清空），语义明确
      scopes,
    }
    if (dateRange.value) {
      data.start_time = formatTime(dateRange.value[0])
      data.end_time = formatTime(dateRange.value[1])
    } else {
      data.start_time = ''
      data.end_time = ''
    }
    const res = isEdit.value
      ? await updateFreeShipping(Number(route.params.id), data)
      : await createFreeShipping(data)
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

.scope-none {
  font-size: 13px;
  color: #999;
}
</style>
