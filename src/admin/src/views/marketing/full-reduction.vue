<template>
  <n-card title="满减活动">
    <n-space vertical>
      <n-space>
        <n-button type="primary" @click="router.push('/marketing/full-reduction/create')">创建满减</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" />
    </n-space>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'FullReductionList' })
import { ref, onActivated, h } from 'vue'
import { useRouter } from 'vue-router'
import { NButton, NSpace, NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getFullReductionList, deleteFullReduction } from '@/api/marketing'
import type { FullReduction } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'

/**
 * 满减活动管理页面
 */
const router = useRouter()
const list = ref<FullReduction[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const columns: DataTableColumns<FullReduction> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '活动名称', key: 'name' },
  { title: '满减门槛', key: 'threshold' },
  { title: '减免金额', key: 'reduce_amount' },
  { title: '开始时间', key: 'start_time', render: (row) => formatDateTime(row.start_time) },
  { title: '结束时间', key: 'end_time', render: (row) => formatDateTime(row.end_time) },
  {
    title: '状态',
    key: 'status',
    render(row) {
      return h(NTag, { type: row.status === 1 ? 'success' : 'default' }, { default: () => (row.status === 1 ? '启用' : '禁用') })
    },
  },
  {
    title: '操作',
    key: 'actions',
    render(row) {
      return h(NSpace, null, {
        default: () => [
          h(NButton, { size: 'small', type: 'error', onClick: () => remove(row) }, { default: () => '删除' }),
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
    const res = await getFullReductionList({ page: pagination.value.page, page_size: pagination.value.pageSize })
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

async function remove(row: FullReduction) {
  const res = await deleteFullReduction(row.id)
  if (res.data.code === 0) loadData()
}

// keep-alive 缓存下从内页（新建）返回时 onMounted 不会触发，
// onActivated 重新拉取，保证满减活动新增后列表展示最新数据
onActivated(loadData)
</script>
