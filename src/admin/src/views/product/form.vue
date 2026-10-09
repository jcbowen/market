<template>
  <n-card :title="isEdit ? '编辑商品' : '新增商品'">
    <n-form :model="form" label-placement="left" label-width="100px">
      <n-form-item label="商品名称" required>
        <n-input v-model:value="form.name" placeholder="请输入商品名称" />
      </n-form-item>
      <n-form-item label="商品分类" required>
        <n-select v-model:value="form.category_id" :options="categoryOptions" placeholder="请选择分类" />
      </n-form-item>
      <n-form-item label="商品单位">
        <n-input v-model:value="form.unit" placeholder="如：件 / 斤 / 箱" />
      </n-form-item>
      <n-form-item label="封面图">
        <n-input v-model:value="form.cover_image" placeholder="图片 URL" />
      </n-form-item>
      <n-form-item label="是否多规格">
        <n-switch v-model:value="form.is_multi_spec" />
      </n-form-item>
      <n-form-item label="起售数量">
        <n-input-number v-model:value="form.min_buy" :min="1" />
      </n-form-item>
      <n-form-item label="排序">
        <n-input-number v-model:value="form.sort" />
      </n-form-item>

      <!-- 单规格：直接填写价格库存 -->
      <template v-if="!form.is_multi_spec">
        <n-form-item label="售价（元）" required>
          <n-input-number v-model:value="singleSku.price" :min="0.01" :precision="2" />
        </n-form-item>
        <n-form-item label="成本价（元）">
          <n-input-number v-model:value="singleSku.cost_price" :min="0" :precision="2" />
        </n-form-item>
        <n-form-item label="库存" required>
          <n-input-number v-model:value="singleSku.stock" :min="0" />
        </n-form-item>
        <n-form-item label="安全库存">
          <n-input-number v-model:value="singleSku.safety_stock" :min="0" />
        </n-form-item>
      </template>

      <!-- 多规格：规格编辑器 + SKU 笛卡尔积表格 -->
      <template v-else>
        <n-form-item
          v-for="(spec, si) in specs"
          :key="spec.tempId"
          :label="si === 0 ? '规格' : ' '"
        >
          <n-space vertical class="spec-block">
            <n-space align="center">
              <n-input
                v-model:value="spec.name"
                placeholder="规格名，如：颜色"
                style="width: 160px"
              />
              <n-button quaternary type="error" @click="removeSpec(si)">删除规格</n-button>
            </n-space>
            <n-dynamic-tags
              :value="specValueNames(spec)"
              @update:value="(names: string[]) => updateSpecValues(spec, names)"
            />
          </n-space>
        </n-form-item>
        <n-form-item :label="specs.length ? ' ' : '规格'">
          <n-button dashed style="width: 220px" @click="addSpec">+ 添加规格</n-button>
        </n-form-item>

        <n-form-item v-if="skuRows.length > 0" label="SKU 列表">
          <n-table :bordered="true" size="small">
            <thead>
              <tr>
                <th v-for="spec in specs" :key="spec.tempId">{{ spec.name || '规格' }}</th>
                <th>售价（元）</th>
                <th>成本价（元）</th>
                <th>库存</th>
                <th>安全库存</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in skuRows" :key="row.key">
                <td v-for="v in row.values" :key="v.tempId" class="spec-value-cell">
                  {{ v.name }}
                </td>
                <td>
                  <n-input-number
                    v-model:value="row.price"
                    :min="0.01"
                    :precision="2"
                    size="small"
                    placeholder="售价"
                  />
                </td>
                <td>
                  <n-input-number
                    v-model:value="row.cost_price"
                    :min="0"
                    :precision="2"
                    size="small"
                    placeholder="成本价"
                  />
                </td>
                <td>
                  <n-input-number
                    v-model:value="row.stock"
                    :min="0"
                    size="small"
                    placeholder="库存"
                  />
                </td>
                <td>
                  <n-input-number
                    v-model:value="row.safety_stock"
                    :min="0"
                    size="small"
                    placeholder="安全库存"
                  />
                </td>
              </tr>
            </tbody>
          </n-table>
        </n-form-item>
      </template>

      <n-form-item>
        <n-space>
          <n-button type="primary" :loading="saving" @click="submit">保存</n-button>
          <n-button @click="router.back()">返回</n-button>
        </n-space>
      </n-form-item>
    </n-form>
  </n-card>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMessage } from 'naive-ui'
import { getProductDetail, createProduct, updateProduct, getCategoryList } from '@/api/product'
import type {
  Category,
  ProductSavePayload,
  SkuSaveItem,
  SpecSaveItem,
  SpecValueSaveItem,
} from '@/api/types'

/**
 * 商品新增/编辑页面
 * 单规格：直接填写价格库存，保存时生成唯一默认 SKU；
 * 多规格：动态编辑规格名/规格值，前端按笛卡尔积生成 SKU 表格批量填写，
 * 提交时携带规格树与 SKU 全量数据，由后端完成临时 ID 映射与 diff 软删除。
 */

/** 编辑态规格行 */
interface SpecEditor {
  id: number
  tempId: string
  name: string
  values: { id: number; tempId: string; name: string }[]
}

/** SKU 表格行 */
interface SkuRow {
  key: string
  id: number
  values: { id: number; tempId: string; name: string }[]
  price: number | null
  cost_price: number | null
  stock: number | null
  safety_stock: number | null
}

const route = useRoute()
const router = useRouter()
const message = useMessage()

const isEdit = ref(false)
const saving = ref(false)
const categoryOptions = ref<Array<{ label: string; value: number }>>([])

const form = reactive({
  name: '',
  category_id: null as number | null,
  unit: '',
  cover_image: '',
  is_multi_spec: false,
  min_buy: 1,
  sort: 0,
})

/** 单规格默认 SKU（编辑时回填 id 以更新而非新建） */
const singleSku = reactive({
  id: 0,
  price: null as number | null,
  cost_price: null as number | null,
  stock: null as number | null,
  safety_stock: 0,
})

const specs = ref<SpecEditor[]>([])
const skuRows = ref<SkuRow[]>([])

/** 临时 ID 自增序号（页面内唯一即可） */
let tempSeq = 0

/** 生成前端临时 ID */
function nextTempId(prefix: string): string {
  tempSeq += 1
  return `new_${prefix}_${tempSeq}`
}

/** SKU 表格是否有可填行（任一规格存在规格值时才生成笛卡尔积） */
const hasSpecValues = computed(() => specs.value.some((s) => s.values.length > 0))

// 规格变化时重建 SKU 行（保留已填数据）
watch(() => specs.value.map((s) => s.values.length), rebuildSkuRows, { deep: true })
watch(() => specs.value.map((s) => s.values.map((v) => v.tempId)), rebuildSkuRows, { deep: true })

/**
 * 新增一个规格
 */
function addSpec(): void {
  specs.value.push({ id: 0, tempId: nextTempId('spec'), name: '', values: [] })
}

/**
 * 删除指定规格，并重建 SKU 表格
 * @param index 规格下标
 */
function removeSpec(index: number): void {
  specs.value.splice(index, 1)
  rebuildSkuRows()
}

/**
 * 获取规格值的名称列表（供 n-dynamic-tags 展示）
 * @param spec 规格行
 * @returns 规格值名称数组
 */
function specValueNames(spec: SpecEditor): string[] {
  return spec.values.map((v) => v.name)
}

/**
 * 更新规格值列表，保留同名已有值对象以维持真实 ID
 * @param spec 规格行
 * @param names 最新的规格值名称数组
 */
function updateSpecValues(spec: SpecEditor, names: string[]): void {
  spec.values = names.map((name) => {
    const found = spec.values.find((v) => v.name === name)
    return found ?? { id: 0, tempId: nextTempId('value'), name }
  })
}

/**
 * 按当前规格树重建 SKU 笛卡尔积表格，已填写的数据按规格值组合匹配保留
 */
function rebuildSkuRows(): void {
  if (!form.is_multi_spec) return

  // 任一规格无规格值时，无法生成完整组合
  if (!hasSpecValues.value) {
    skuRows.value = []
    return
  }

  const prev = new Map(skuRows.value.map((r) => [r.key, r]))
  const valueLists = specs.value.map((s) => s.values)
  const combos = cartesian(valueLists)

  skuRows.value = combos.map((combo) => {
    const key = combo.map((v) => v.tempId).join('|')
    const old = prev.get(key)
    return {
      key,
      id: old?.id ?? 0,
      values: combo,
      price: old?.price ?? null,
      cost_price: old?.cost_price ?? null,
      stock: old?.stock ?? null,
      safety_stock: old?.safety_stock ?? null,
    }
  })
}

/**
 * 计算多个数组的笛卡尔积
 * @param arrays 二维数组（每个元素为一个规格的规格值列表）
 * @returns 所有组合的二维数组
 */
function cartesian<T>(arrays: T[][]): T[][] {
  return arrays.reduce<T[][]>(
    (acc, list) => acc.flatMap((row) => list.map((item) => [...row, item])),
    [[]],
  )
}

/**
 * 加载分类选项
 */
async function loadCategories(): Promise<void> {
  const res = await getCategoryList()
  if (res.data.code === 0) {
    categoryOptions.value = (res.data.data || []).map((item: Category) => ({
      label: item.name,
      value: item.id,
    }))
  }
}

/**
 * 加载商品详情并回填表单（编辑态）
 */
async function loadDetail(): Promise<void> {
  const id = Number(route.params.id)
  if (!id) return
  isEdit.value = true

  const res = await getProductDetail(id)
  if (res.data.code !== 0 || !res.data.data) return

  const detail = res.data.data
  const p = detail.product
  Object.assign(form, {
    name: p.name,
    category_id: p.category_id,
    unit: p.unit ?? '',
    cover_image: p.cover_image ?? '',
    is_multi_spec: !!p.is_multi_spec,
    min_buy: p.min_buy ?? 1,
    sort: p.sort ?? 0,
  })

  if (form.is_multi_spec) {
    // 回填规格树：已有节点携带真实 id，tempId 仅用于页面内 key
    specs.value = (detail.specs || []).map((sp) => ({
      id: sp.id,
      tempId: `spec_${sp.id}`,
      name: sp.name,
      values: (sp.values || []).map((v) => ({
        id: v.id,
        tempId: `value_${v.id}`,
        name: v.name,
      })),
    }))
    // 回填 SKU 行：规格值真实 ID 转换为页面内的 tempId（value_${id}）
    const valueTempMap = new Map<number, string>()
    specs.value.forEach((s) => s.values.forEach((v) => valueTempMap.set(v.id, v.tempId)))
    skuRows.value = (detail.skus || []).map((sku) => {
      const valueIds = (sku.spec_value_ids || '')
        .split(',')
        .filter(Boolean)
        .map((raw) => Number(raw))
      const combo = valueIds
        .map((vid) => {
          const tempId = valueTempMap.get(vid)
          if (!tempId) return null
          const spec = specs.value.find((s) => s.values.some((v) => v.tempId === tempId))
          const value = spec?.values.find((v) => v.tempId === tempId)
          return value ? { id: vid, tempId, name: value.name } : null
        })
        .filter((v): v is { id: number; tempId: string; name: string } => v !== null)
      return {
        key: combo.map((v) => v.tempId).join('|'),
        id: sku.id,
        values: combo,
        price: sku.price,
        cost_price: sku.cost_price,
        stock: sku.stock,
        safety_stock: sku.safety_stock,
      }
    })
  } else {
    // 单规格：回填默认 SKU（spec_value_ids 为空的那条）
    const sku = (detail.skus || []).find((s) => !s.spec_value_ids)
    if (sku) {
      singleSku.id = sku.id
      singleSku.price = sku.price
      singleSku.cost_price = sku.cost_price
      singleSku.stock = sku.stock
      singleSku.safety_stock = sku.safety_stock
    }
  }
}

/**
 * 校验并组装保存载荷
 * @returns 组装好的保存载荷，校验失败返回 null
 */
function buildPayload(): ProductSavePayload | null {
  if (!form.name.trim()) {
    message.error('请输入商品名称')
    return null
  }
  if (!form.category_id) {
    message.error('请选择商品分类')
    return null
  }

  const payload: ProductSavePayload = {
    name: form.name.trim(),
    category_id: form.category_id,
    description: '',
    cover_image: form.cover_image,
    images: [],
    unit: form.unit,
    is_multi_spec: form.is_multi_spec,
    min_buy: form.min_buy,
    sort: form.sort,
    specs: [],
    sku_items: [],
  }

  if (!form.is_multi_spec) {
    if (!singleSku.price || singleSku.price <= 0) {
      message.error('请填写大于 0 的售价')
      return null
    }
    if (singleSku.stock === null || singleSku.stock < 0) {
      message.error('请填写库存')
      return null
    }
    payload.sku_items = [
      {
        id: singleSku.id,
        spec_value_ids: [],
        sku_code: '',
        price: singleSku.price,
        cost_price: singleSku.cost_price ?? 0,
        stock: singleSku.stock,
        safety_stock: singleSku.safety_stock ?? 0,
        image: '',
      },
    ]
    return payload
  }

  // 多规格校验与组装
  if (specs.value.length === 0) {
    message.error('多规格商品至少需要一个规格')
    return null
  }
  for (const spec of specs.value) {
    if (!spec.name.trim()) {
      message.error('规格名不能为空')
      return null
    }
    if (spec.values.length === 0) {
      message.error(`规格「${spec.name}」至少需要一个规格值`)
      return null
    }
  }
  if (skuRows.value.length === 0) {
    message.error('请为规格添加规格值以生成 SKU')
    return null
  }
  for (const row of skuRows.value) {
    if (!row.price || row.price <= 0) {
      message.error(`SKU「${row.values.map((v) => v.name).join(' / ')}」的售价必须大于 0`)
      return null
    }
    if (row.stock === null || row.stock < 0) {
      message.error(`SKU「${row.values.map((v) => v.name).join(' / ')}」请填写库存`)
      return null
    }
  }

  payload.specs = specs.value.map((spec, si): SpecSaveItem => ({
    id: spec.id,
    temp_id: spec.id ? '' : spec.tempId,
    name: spec.name.trim(),
    sort: si,
    values: spec.values.map((v, vi): SpecValueSaveItem => ({
      id: v.id,
      temp_id: v.id ? '' : v.tempId,
      name: v.name,
      sort: vi,
    })),
  }))

  payload.sku_items = skuRows.value.map((row): SkuSaveItem => ({
    id: row.id,
    spec_value_ids: row.values.map((v) => (v.id ? String(v.id) : v.tempId)),
    sku_code: '',
    price: row.price ?? 0,
    cost_price: row.cost_price ?? 0,
    stock: row.stock ?? 0,
    safety_stock: row.safety_stock ?? 0,
    image: '',
  }))
  return payload
}

/**
 * 提交保存
 */
async function submit(): Promise<void> {
  const payload = buildPayload()
  if (!payload) return

  saving.value = true
  try {
    const res = isEdit.value
      ? await updateProduct(Number(route.params.id), payload)
      : await createProduct(payload)
    if (res.data.code === 0) {
      message.success('保存成功')
      router.back()
    }
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  loadCategories()
  loadDetail()
})
</script>

<style scoped>
.spec-block {
  flex: 1;
}
.spec-value-cell {
  color: var(--n-text-color);
}
</style>
