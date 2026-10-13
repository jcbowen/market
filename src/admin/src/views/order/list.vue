<template>
  <n-card title="订单列表">
    <n-space vertical :size="16">
      <!-- 筛选区：窄屏允许换行，搜索框自适应宽度，避免手机上横向溢出 -->
      <n-space align="center" :size="12" class="filter-bar">
        <n-radio-group v-model:value="statusFilter" @update:value="onStatusChange">
          <n-radio-button :value="0">全部订单</n-radio-button>
          <n-radio-button v-for="s in statusOptions" :key="s.value" :value="s.value">{{ s.label }}</n-radio-button>
        </n-radio-group>
        <n-input
          v-model:value="orderNo"
          placeholder="订单号搜索"
          clearable
          class="search-input"
          @keyup.enter="onSearch"
          @clear="onSearch"
        />
        <n-button type="primary" @click="onSearch">查询</n-button>
      </n-space>
      <n-data-table
        :columns="columns"
        :data="list"
        :loading="loading"
        remote
        :scroll-x="1040"
        :pagination="pagination"
        @update:page="onPageChange"
      />
    </n-space>
  </n-card>

  <!-- 订单详情弹窗 -->
  <order-detail-modal v-model:show="detailVisible" :order-id="detailId" />

  <!-- 发货表单弹窗 -->
  <n-modal v-model:show="shipVisible" preset="card" title="订单发货" style="width: min(460px, 94vw)">
    <n-form label-placement="left" label-width="80">
      <n-form-item label="物流公司">
        <n-input v-model:value="shipForm.express_company" placeholder="请输入物流公司" />
      </n-form-item>
      <n-form-item label="物流单号">
        <n-input v-model:value="shipForm.express_no" placeholder="请输入物流单号" />
      </n-form-item>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button @click="shipVisible = false">取消</n-button>
        <n-button type="primary" :loading="shipSubmitting" @click="submitShip">确认发货</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'OrderList' })
import { ref, reactive, onMounted, h } from 'vue'
import { NButton, NSpace, NTag, useDialog } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getOrderList, shipOrder, completeOrder, updateOrderStatus } from '@/api/order'
import OrderDetailModal from './detail.vue'
import type { Order } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'

/**
 * 订单列表页面
 * 状态按钮组筛选、彩色状态标签、操作列（详情/发货/完成/取消）
 */
const dialog = useDialog()
const orderNo = ref('')
const statusFilter = ref(0)
const list = ref<Order[]>([])
const loading = ref(false)
const pagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const detailVisible = ref(false)
const detailId = ref<number | null>(null)

const shipVisible = ref(false)
const shipSubmitting = ref(false)
const shipOrderId = ref(0)
const shipForm = reactive({ express_company: '', express_no: '' })

const statusOptions = [
  { label: '待支付', value: 1 },
  { label: '待发货', value: 2 },
  { label: '已发货', value: 4 },
  { label: '已完成', value: 5 },
  { label: '已取消', value: 6 },
]

const columns: DataTableColumns<Order> = [
  { title: '订单号', key: 'order_no', width: 200 },
  { title: '用户', key: 'user_name', width: 120 },
  {
    title: '订单金额',
    key: 'total_amount',
    width: 110,
    render: (row) => `￥${(row.total_amount ?? 0).toFixed(2)}`,
  },
  {
    title: '实付金额',
    key: 'pay_amount',
    width: 110,
    render: (row) => `￥${(row.pay_amount ?? 0).toFixed(2)}`,
  },
  {
    title: '状态',
    key: 'status',
    width: 100,
    render: (row) => h(NTag, { type: statusTagType(row.status), size: 'small', bordered: false }, { default: () => row.status_text }),
  },
  { title: '创建时间', key: 'created_at', width: 180, render: (row) => formatDateTime(row.created_at) },
  {
    title: '操作',
    key: 'actions',
    width: 220,
    render(row) {
      const buttons = [
        h(
          NButton,
          { size: 'small', onClick: () => openDetail(row.id) },
          { default: () => '详情' },
        ),
      ]
      if (row.status === 2) {
        buttons.push(
          h(
            NButton,
            { size: 'small', type: 'primary', onClick: () => openShip(row) },
            { default: () => '发货' },
          ),
        )
      }
      if (row.status === 4) {
        buttons.push(
          h(
            NButton,
            { size: 'small', type: 'success', onClick: () => confirmComplete(row) },
            { default: () => '完成' },
          ),
        )
      }
      if (row.status === 1) {
        buttons.push(
          h(
            NButton,
            { size: 'small', type: 'error', onClick: () => confirmCancel(row) },
            { default: () => '取消订单' },
          ),
        )
      }
      return h(NSpace, { size: 6, wrap: false }, { default: () => buttons })
    },
  },
]

/**
 * 订单状态彩色标签类型
 * @param status 订单状态码
 * @returns n-tag 类型
 */
function statusTagType(status: number): 'default' | 'warning' | 'info' | 'primary' | 'success' | 'error' {
  switch (status) {
    case 1:
      return 'warning'
    case 2:
    case 3:
      return 'info'
    case 4:
      return 'primary'
    case 5:
      return 'success'
    case 7:
    case 8:
      return 'error'
    default:
      return 'default'
  }
}

/**
 * 切换状态按钮组后回到第 1 页并刷新
 * @param value 目标状态（0 表示全部）
 */
function onStatusChange(value: number) {
  statusFilter.value = value
  pagination.value.page = 1
  loadData()
}

/**
 * 点击查询回到第 1 页并刷新
 */
function onSearch() {
  pagination.value.page = 1
  loadData()
}

/**
 * 分页切换
 * @param page 目标页码
 */
function onPageChange(page: number) {
  pagination.value.page = page
  loadData()
}

/**
 * 打开订单详情弹窗
 * @param id 订单 ID
 */
function openDetail(id: number) {
  detailId.value = id
  detailVisible.value = true
}

/**
 * 打开发货表单弹窗
 * @param row 订单行
 */
function openShip(row: Order) {
  shipOrderId.value = row.id
  shipForm.express_company = ''
  shipForm.express_no = ''
  shipVisible.value = true
}

/**
 * 提交发货（物流信息表单）
 */
async function submitShip() {
  shipSubmitting.value = true
  try {
    await shipOrder(shipOrderId.value, {
      express_company: shipForm.express_company,
      express_no: shipForm.express_no,
      remark: '后台发货',
    })
    shipVisible.value = false
    loadData()
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    shipSubmitting.value = false
  }
}

/**
 * 确认收货/完成订单（二次确认）
 * @param row 订单行
 */
function confirmComplete(row: Order) {
  dialog.warning({
    title: '完成订单',
    content: `确认将订单 ${row.order_no} 标记为已完成？`,
    positiveText: '确认完成',
    negativeText: '取消',
    onPositiveClick: async () => {
      try {
        await completeOrder(row.id)
        loadData()
      } catch {
        // 错误提示由请求拦截器统一弹出
      }
    },
  })
}

/**
 * 取消订单（二次确认）
 * @param row 订单行
 */
function confirmCancel(row: Order) {
  dialog.warning({
    title: '取消订单',
    content: `确认取消订单 ${row.order_no}？取消后将释放占用库存，该操作不可恢复。`,
    positiveText: '确认取消',
    negativeText: '返回',
    onPositiveClick: async () => {
      try {
        await updateOrderStatus(row.id, 6, '后台取消订单')
        loadData()
      } catch {
        // 错误提示由请求拦截器统一弹出
      }
    },
  })
}

/**
 * 加载订单分页数据
 */
async function loadData() {
  loading.value = true
  try {
    const res = await getOrderList({
      page: pagination.value.page,
      page_size: pagination.value.pageSize,
      order_no: orderNo.value,
      status: statusFilter.value > 0 ? statusFilter.value : undefined,
    })
    if (res.data.code === 0) {
      list.value = res.data.data.list
      pagination.value.itemCount = res.data.data.total
    }
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    loading.value = false
  }
}

onMounted(loadData)
</script>

<style scoped>
/* 筛选区允许换行后,窄屏下搜索框撑满剩余宽度,避免固定 200px 挤压 */
.search-input {
  width: 200px;
}

@media (max-width: 768px) {
  .search-input {
    width: 100%;
  }
}
</style>
