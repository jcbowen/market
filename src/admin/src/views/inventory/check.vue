<template>
  <n-card title="盘点管理">
    <n-space vertical>
      <n-space>
        <n-button v-if="canCreate" type="primary" @click="createVisible = true">新建盘点</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" :pagination="pagination" remote @update:page="onPageChange" />
    </n-space>

    <!-- 新建盘点弹窗：创建后直接进入详情录入实盘数据 -->
    <n-modal v-model:show="createVisible" preset="card" title="新建盘点单" style="width: 440px">
      <n-space vertical>
        <n-input v-model:value="remark" placeholder="备注（选填）" type="textarea" :rows="2" maxlength="200" />
      </n-space>
      <template #footer>
        <n-space justify="end">
          <n-button @click="createVisible = false">取消</n-button>
          <n-button type="primary" :loading="creating" @click="create">创建</n-button>
        </n-space>
      </template>
    </n-modal>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StockCheck' })
import { ref, onMounted, h, computed } from 'vue'
import { refreshOnActivated } from '@/utils/activateRefresh'
import { useRouter } from 'vue-router'
import { NButton, NTag } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { createInventoryCheck, getInventoryCheckList } from '@/api/inventory'
import type { InventoryCheck } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'
import { useAuthStore } from '@/stores/auth'

/**
 * 盘点管理页面
 * 展示盘点单列表；新建改为弹窗（原独立页面只有一个备注框，多一次无意义跳转）
 */
const router = useRouter()
// 按钮级权限：页面只要求 inventory:view，新建盘点按 inventory:create 收窄（与网关同码）
const authStore = useAuthStore()
const canCreate = computed(() => authStore.hasPermission('inventory:create'))
const list = ref<InventoryCheck[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const createVisible = ref(false)
const creating = ref(false)
const remark = ref('')

const columns: DataTableColumns<InventoryCheck> = [
  { title: '盘点单号', key: 'check_no' },
  { title: '品项数', key: 'total_items' },
  { title: '差异数', key: 'diff_items' },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render: (row) => h(NTag, { type: row.status === 1 ? 'warning' : 'success', size: 'small' }, { default: () => (row.status === 1 ? '待提交' : '已提交') }),
  },
  { title: '备注', key: 'remark' },
  { title: '创建时间', key: 'created_at', render: (row) => formatDateTime(row.created_at) },
  {
    title: '操作',
    key: 'actions',
    render(row) {
      return h(NButton, { size: 'small', onClick: () => router.push(`/stock/check/${row.id}`) }, { default: () => '详情' })
    },
  },
]

// 请求序号守卫：快速翻页时旧响应可能后到并覆盖新结果（与 UserPicker 同范式）
let loadSeq = 0

async function loadData() {
  const seq = ++loadSeq
  loading.value = true
  try {
    const res = await getInventoryCheckList({ page: pagination.value.page, page_size: pagination.value.pageSize })
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

async function create() {
  creating.value = true
  try {
    const res = await createInventoryCheck({ remark: remark.value } as any)
    if (res.data.code === 0) {
      createVisible.value = false
      remark.value = ''
      router.push(`/stock/check/${res.data.data.id}`)
    }
  } finally {
    creating.value = false
  }
}

/**
 * 翻页：数据由服务端按页返回，此处接线页码事件并重新拉取
 * （此前未接 @update:page，点第 2 页仍显示第 1 页内容）
 */
function onPageChange(page: number) {
  pagination.value.page = page
  loadData()
}

onMounted(loadData)
// keep-alive 缓存下切回本页不重新挂载，需在激活时刷新，避免展示离开前的旧盘点单
refreshOnActivated(loadData)
</script>
