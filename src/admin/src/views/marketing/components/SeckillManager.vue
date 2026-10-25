<template>
  <div class="seckill-manager">
    <n-space vertical>
      <n-space wrap>
        <n-input v-model:value="searchProductId" placeholder="商品 ID" clearable />
        <n-button type="primary" @click="loadSeckills">查询</n-button>
        <n-button type="primary" @click="openModal()">新增秒杀</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="seckills" :loading="loading" scroll-x="720" />
      <n-pagination v-model:page="page" :page-count="pageCount" @update:page="loadSeckills" />
    </n-space>

    <n-modal v-model:show="showModal" title="秒杀活动" preset="card" style="width: 520px; max-width: 90vw" scrollable>
      <n-form :model="form" label-placement="top">
        <n-form-item label="商品 ID">
          <n-input-number v-model:value="form.product_id" placeholder="商品 ID" style="width: 100%;" />
        </n-form-item>
        <n-form-item label="秒杀价">
          <n-input-number v-model:value="form.seckill_price" placeholder="秒杀价" style="width: 100%;" />
        </n-form-item>
        <n-form-item label="秒杀库存">
          <n-input-number v-model:value="form.stock" placeholder="秒杀库存" style="width: 100%;" />
        </n-form-item>
        <n-form-item label="活动时间">
          <n-date-picker v-model:value="timeRange" type="datetimerange" clearable style="width: 100%;" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showModal = false">取消</n-button>
          <n-button type="primary" :loading="modalLoading" @click="saveSeckill">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { h, onMounted, reactive, ref } from 'vue'
import { formatAmount } from '@/utils/format'
import { useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getSeckillList, createSeckill, updateSeckill } from '@/api/marketing'
import type { Seckill } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'

const message = useMessage()
const pageSize = 10

const columns: DataTableColumns<Seckill> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '商品', key: 'product_name', width: 140, ellipsis: { tooltip: true } },
  { title: '秒杀价', key: 'seckill_price', width: 100, render(row) { return `¥${formatAmount(row.seckill_price)}` } },
  { title: '库存', key: 'stock', width: 80 },
  { title: '开始时间', key: 'start_time', width: 160, render: (row) => formatDateTime(row.start_time) },
  { title: '结束时间', key: 'end_time', width: 160, render: (row) => formatDateTime(row.end_time) },
  {
    title: '操作',
    key: 'actions',
    width: 80,
    render(row) {
      return h('a', { onClick: () => openModal(row), style: 'color: #07c160; cursor: pointer;' }, '编辑')
    },
  },
]

const seckills = ref<Seckill[]>([])
const loading = ref(false)
const page = ref(1)
const pageCount = ref(1)
const searchProductId = ref('')

const showModal = ref(false)
const modalLoading = ref(false)
const timeRange = ref<[number, number] | null>(null)
const form = reactive<Partial<Seckill>>({
  id: undefined,
  product_id: undefined,
  seckill_price: undefined,
  stock: undefined,
})

/**
 * 加载秒杀列表
 */
async function loadSeckills() {
  loading.value = true
  try {
    const res = await getSeckillList({ page: page.value, page_size: pageSize, product_id: searchProductId.value ? Number(searchProductId.value) : undefined })
    seckills.value = res.data.data.list
    pageCount.value = Math.ceil(res.data.data.total / pageSize) || 1
  } finally {
    loading.value = false
  }
}

/**
 * 打开秒杀编辑弹窗
 * @param row 秒杀数据
 */
function openModal(row?: Seckill) {
  form.id = row?.id
  form.product_id = row?.product_id
  form.seckill_price = row?.seckill_price
  form.stock = row?.stock
  if (row?.start_time && row?.end_time) {
    timeRange.value = [new Date(row.start_time).getTime(), new Date(row.end_time).getTime()]
  } else {
    timeRange.value = null
  }
  showModal.value = true
}

/**
 * 保存秒杀活动
 */
async function saveSeckill() {
  if (!form.product_id || form.seckill_price === undefined || form.stock === undefined) {
    message.warning('请填写完整信息')
    return
  }
  modalLoading.value = true
  try {
    const data = { ...form }
    if (timeRange.value) {
      data.start_time = new Date(timeRange.value[0]).toISOString()
      data.end_time = new Date(timeRange.value[1]).toISOString()
    }
    if (form.id) {
      await updateSeckill(form.id, data)
    } else {
      await createSeckill(data)
    }
    message.success('保存成功')
    showModal.value = false
    loadSeckills()
  } finally {
    modalLoading.value = false
  }
}

onMounted(() => {
  loadSeckills()
})
</script>
