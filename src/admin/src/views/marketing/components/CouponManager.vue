<template>
  <div class="coupon-manager">
    <n-space vertical>
      <n-space wrap>
        <n-input v-model:value="searchName" placeholder="优惠券名称" clearable />
        <n-button type="primary" @click="loadCoupons">查询</n-button>
        <n-button type="primary" @click="openModal()">新增优惠券</n-button>
      </n-space>
      <n-data-table :columns="columns" :data="coupons" :loading="loading" scroll-x="720" />
      <n-pagination v-model:page="page" :page-count="pageCount" @update:page="loadCoupons" />
    </n-space>

    <n-modal v-model:show="showModal" title="优惠券" preset="card" style="width: 520px; max-width: 90vw" scrollable>
      <n-form :model="form" label-placement="top">
        <n-form-item label="优惠券名称">
          <n-input v-model:value="form.name" placeholder="请输入优惠券名称" />
        </n-form-item>
        <n-form-item label="优惠类型">
          <n-select v-model:value="form.type" :options="typeOptions" placeholder="请选择类型" />
        </n-form-item>
        <n-form-item label="优惠值">
          <n-input-number v-model:value="form.discount_value" placeholder="优惠值" style="width: 100%;" />
        </n-form-item>
        <n-form-item label="最低消费">
          <n-input-number v-model:value="form.threshold" placeholder="最低消费" style="width: 100%;" />
        </n-form-item>
        <n-form-item label="发放总量">
          <n-input-number v-model:value="form.total_count" placeholder="发放总量" style="width: 100%;" />
        </n-form-item>
        <n-form-item label="有效期">
          <n-date-picker v-model:value="timeRange" type="datetimerange" clearable style="width: 100%;" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showModal = false">取消</n-button>
          <n-button type="primary" :loading="modalLoading" @click="saveCoupon">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { h, onMounted, reactive, ref } from 'vue'
import { useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getCouponList, createCoupon, updateCoupon } from '@/api/marketing'
import type { Coupon } from '@/api/types'

const message = useMessage()
const pageSize = 10

const typeOptions = [
  { label: '满减券', value: 1 },
  { label: '折扣券', value: 2 },
]

const typeMap: Record<number, string> = {
  1: '满减券',
  2: '折扣券',
}

const columns: DataTableColumns<Coupon> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '名称', key: 'name', width: 140, ellipsis: { tooltip: true } },
  { title: '类型', key: 'type', width: 90, render(row) { return typeMap[row.type] || '未知' } },
  { title: '优惠值', key: 'discount_value', width: 90 },
  { title: '最低消费', key: 'threshold', width: 100 },
  { title: '发放/领取', key: 'count', width: 100, render(row) { return `${row.total_count}/${row.received_count}` } },
  {
    title: '操作',
    key: 'actions',
    width: 80,
    render(row) {
      return h('a', { onClick: () => openModal(row), style: 'color: #07c160; cursor: pointer;' }, '编辑')
    },
  },
]

const coupons = ref<Coupon[]>([])
const loading = ref(false)
const page = ref(1)
const pageCount = ref(1)
const searchName = ref('')

const showModal = ref(false)
const modalLoading = ref(false)
const timeRange = ref<[number, number] | null>(null)
const form = reactive<Partial<Coupon>>({
  id: undefined,
  name: '',
  type: 1,
  discount_value: undefined,
  threshold: undefined,
  total_count: undefined,
})

/**
 * 加载优惠券列表
 */
async function loadCoupons() {
  loading.value = true
  try {
    const res = await getCouponList({ page: page.value, page_size: pageSize, name: searchName.value })
    coupons.value = res.data.data.list
    pageCount.value = Math.ceil(res.data.data.total / pageSize) || 1
  } finally {
    loading.value = false
  }
}

/**
 * 打开优惠券编辑弹窗
 * @param row 优惠券数据
 */
function openModal(row?: Coupon) {
  form.id = row?.id
  form.name = row?.name || ''
  form.type = row?.type || 1
  form.discount_value = row?.discount_value
  form.threshold = row?.threshold
  form.total_count = row?.total_count
  if (row?.start_time && row?.end_time) {
    timeRange.value = [new Date(row.start_time).getTime(), new Date(row.end_time).getTime()]
  } else {
    timeRange.value = null
  }
  showModal.value = true
}

/**
 * 保存优惠券
 */
async function saveCoupon() {
  if (!form.name || form.discount_value === undefined) {
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
      await updateCoupon(form.id, data)
    } else {
      await createCoupon(data)
    }
    message.success('保存成功')
    showModal.value = false
    loadCoupons()
  } finally {
    modalLoading.value = false
  }
}

onMounted(() => {
  loadCoupons()
})
</script>
