<template>
  <div class="full-reduction-manager">
    <n-space vertical>
      <n-space wrap>
        <n-input v-model:value="searchName" placeholder="活动名称" clearable />
        <n-button type="primary" @click="loadList">查询</n-button>
        <n-button type="primary" @click="openModal()">新增满减</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="list" :loading="loading" scroll-x="720" />
      <n-pagination v-model:page="page" :page-count="pageCount" @update:page="loadList" />
    </n-space>

    <n-modal v-model:show="showModal" title="满减活动" preset="card" style="width: 520px; max-width: 90vw" scrollable>
      <n-form :model="form" label-placement="top">
        <n-form-item label="活动名称">
          <n-input v-model:value="form.name" placeholder="请输入活动名称" />
        </n-form-item>
        <n-form-item label="满减门槛">
          <n-input-number v-model:value="form.threshold" placeholder="满减门槛" style="width: 100%;" />
        </n-form-item>
        <n-form-item label="减免金额">
          <n-input-number v-model:value="form.reduce_amount" placeholder="减免金额" style="width: 100%;" />
        </n-form-item>
        <n-form-item label="活动时间">
          <n-date-picker v-model:value="timeRange" type="datetimerange" clearable style="width: 100%;" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showModal = false">取消</n-button>
          <n-button type="primary" :loading="modalLoading" @click="saveItem">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { h, onMounted, reactive, ref } from 'vue'
import { useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getFullReductionList, createFullReduction, updateFullReduction } from '@/api/marketing'
import type { FullReduction } from '@/api/types'

const message = useMessage()
const pageSize = 10

const columns: DataTableColumns<FullReduction> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '活动名称', key: 'name', width: 140, ellipsis: { tooltip: true } },
  { title: '满减门槛', key: 'threshold', width: 110, render(row) { return `¥${row.threshold.toFixed(2)}` } },
  { title: '减免金额', key: 'reduce_amount', width: 110, render(row) { return `¥${row.reduce_amount.toFixed(2)}` } },
  { title: '开始时间', key: 'start_time', width: 160 },
  { title: '结束时间', key: 'end_time', width: 160 },
  {
    title: '操作',
    key: 'actions',
    width: 80,
    render(row) {
      return h('a', { onClick: () => openModal(row), style: 'color: #07c160; cursor: pointer;' }, '编辑')
    },
  },
]

const list = ref<FullReduction[]>([])
const loading = ref(false)
const page = ref(1)
const pageCount = ref(1)
const searchName = ref('')

const showModal = ref(false)
const modalLoading = ref(false)
const timeRange = ref<[number, number] | null>(null)
const form = reactive<Partial<FullReduction>>({
  id: undefined,
  name: '',
  threshold: undefined,
  reduce_amount: undefined,
})

/**
 * 加载满减活动列表
 */
async function loadList() {
  loading.value = true
  try {
    const res = await getFullReductionList({ page: page.value, page_size: pageSize, name: searchName.value })
    list.value = res.data.data.list
    pageCount.value = Math.ceil(res.data.data.total / pageSize) || 1
  } finally {
    loading.value = false
  }
}

/**
 * 打开满减编辑弹窗
 * @param row 满减数据
 */
function openModal(row?: FullReduction) {
  form.id = row?.id
  form.name = row?.name || ''
  form.threshold = row?.threshold
  form.reduce_amount = row?.reduce_amount
  if (row?.start_time && row?.end_time) {
    timeRange.value = [new Date(row.start_time).getTime(), new Date(row.end_time).getTime()]
  } else {
    timeRange.value = null
  }
  showModal.value = true
}

/**
 * 保存满减活动
 */
async function saveItem() {
  if (!form.name || form.threshold === undefined || form.reduce_amount === undefined) {
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
      await updateFullReduction(form.id, data)
    } else {
      await createFullReduction(data)
    }
    message.success('保存成功')
    showModal.value = false
    loadList()
  } finally {
    modalLoading.value = false
  }
}

onMounted(() => {
  loadList()
})
</script>
