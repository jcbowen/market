<template>
  <n-card title="免配送费活动">
    <n-space vertical>
      <n-space>
        <n-button type="primary" @click="router.push('/marketing/free-shipping/create')">创建活动</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'FreeShippingList' })
import { ref, onActivated, h } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NSpace, NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getFreeShippingList, updateFreeShipping } from '@/api/marketing'
import type { FreeShipping } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'

/**
 * 免配送费活动管理页面
 * 规则：配送订单中参与商品额合计 >= 门槛则免收配送费；排除范围（分类/商品）不计入门槛
 */
const router = useRouter()
const list = ref<FreeShipping[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

/** 排除范围列渲染：排除分类/商品名拼接 */
function renderScopes(row: FreeShipping) {
  const scopes = row.scopes || []
  if (!scopes.length) return '无（全部商品参与）'
  const parts = scopes.map((s) =>
    s.scope_type === 4 ? s.category_name || `分类#${s.category_id}` : s.product_name || `商品#${s.product_id}`
  )
  return `排除：${parts.join('、')}`
}

const columns: DataTableColumns<FreeShipping> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '活动名称', key: 'name' },
  { title: '免配送费门槛（元）', key: 'threshold', width: 160 },
  { title: '排除范围', key: 'scopes', render: renderScopes },
  { title: '开始时间', key: 'start_time', width: 160, render: (row) => formatDateTime(row.start_time) },
  { title: '结束时间', key: 'end_time', width: 160, render: (row) => formatDateTime(row.end_time) },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render(row) {
      return h(NTag, { type: row.status === 1 ? 'success' : 'default' }, { default: () => (row.status === 1 ? '启用' : '禁用') })
    },
  },
  {
    title: '操作',
    key: 'actions',
    width: 200,
    render(row) {
      return h(NSpace, null, {
        default: () => [
          h(
            NButton,
            { size: 'small', onClick: () => router.push(`/marketing/free-shipping/${row.id}/edit`) },
            { default: () => '编辑' }
          ),
          h(
            NButton,
            {
              size: 'small',
              type: row.status === 1 ? 'warning' : 'success',
              onClick: () => toggleStatus(row),
            },
            { default: () => (row.status === 1 ? '禁用' : '启用') }
          ),
        ],
      })
    },
  },
]

// 请求序号守卫：快速翻页时旧响应可能后到并覆盖新结果（与 UserPicker 同范式）
let loadSeq = 0

async function loadData() {
  const seq = ++loadSeq
  loading.value = true
  try {
    const res = await getFreeShippingList({ page: pagination.value.page, page_size: pagination.value.pageSize })
    if (seq !== loadSeq) return
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
    }
  } finally {
    // 请求异常时也必须关闭 loading，避免页面永久转圈；过期批次不得掐掉新一轮的 loading
    if (seq === loadSeq) loading.value = false
  }
}

/** 启停切换（只传 status 与 id，不触碰其余字段与范围） */
async function toggleStatus(row: FreeShipping) {
  const res = await updateFreeShipping(row.id, { status: row.status === 1 ? 0 : 1 })
  if (res.data.code === 0) loadData()
}

// keep-alive 缓存下从内页（新建/编辑）返回时 onMounted 不会触发，
// onActivated 重新拉取，保证免配送费活动新增/编辑后列表展示最新数据
onActivated(loadData)
</script>
