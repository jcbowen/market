<template>
  <div class="order-page">
    <n-card title="订单管理">
      <n-space vertical>
        <n-space wrap>
          <n-input v-model:value="searchForm.order_no" placeholder="订单号" clearable />
          <n-select v-model:value="searchForm.status" :options="statusOptions" placeholder="订单状态" clearable style="width: 160px;" />
          <n-button type="primary" @click="loadOrders">查询</n-button>
          <n-button @click="resetSearch">重置</n-button>
        </n-space>
        <n-data-table :columns="columns" :data="orders" :loading="loading" :row-key="(row: Order) => row.id" scroll-x="1200" style="width: 100%;" />
        <n-pagination v-model:page="page" :page-count="pageCount" @update:page="loadOrders" />
      </n-space>
    </n-card>

    <!-- 订单详情弹窗 -->
    <n-modal v-model:show="showDetail" title="订单详情" preset="card" style="width: 720px; max-width: 90vw" scrollable>
      <n-descriptions v-if="detail" bordered :columns="2">
        <n-descriptions-item label="订单号">{{ detail.order_no }}</n-descriptions-item>
        <n-descriptions-item label="下单用户">{{ detail.user_name }}</n-descriptions-item>
        <n-descriptions-item label="订单金额">¥{{ detail.total_amount.toFixed(2) }}</n-descriptions-item>
        <n-descriptions-item label="实付金额">¥{{ detail.pay_amount.toFixed(2) }}</n-descriptions-item>
        <n-descriptions-item label="订单状态">{{ detail.status_text }}</n-descriptions-item>
        <n-descriptions-item label="下单时间">{{ detail.created_at }}</n-descriptions-item>
      </n-descriptions>
      <n-divider />
      <n-h4>商品清单</n-h4>
      <n-table :bordered="false" :single-line="false">
        <thead>
          <tr>
            <th>商品</th>
            <th>规格</th>
            <th>单价</th>
            <th>数量</th>
            <th>小计</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in detail?.items" :key="item.id">
            <td>{{ item.product_name }}</td>
            <td>{{ item.sku_name }}</td>
            <td>¥{{ item.price.toFixed(2) }}</td>
            <td>{{ item.quantity }}</td>
            <td>¥{{ item.total_amount.toFixed(2) }}</td>
          </tr>
        </tbody>
      </n-table>
      <template #footer>
        <n-space justify="end">
          <n-button v-if="detail?.status === 2" type="primary" @click="shipCurrentOrder">发货</n-button>
          <n-button @click="showDetail = false">关闭</n-button>
        </n-space>
      </template>
    </n-modal>

    <!-- 发货弹窗 -->
    <n-modal v-model:show="showShipModal" title="订单发货" preset="card" style="width: 420px; max-width: 90vw" scrollable>
      <n-form :model="shipForm" label-placement="top">
        <n-form-item label="物流公司">
          <n-input v-model:value="shipForm.express_company" placeholder="请输入物流公司" />
        </n-form-item>
        <n-form-item label="物流单号">
          <n-input v-model:value="shipForm.express_no" placeholder="请输入物流单号" />
        </n-form-item>
        <n-form-item label="备注">
          <n-input v-model:value="shipForm.remark" placeholder="备注" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showShipModal = false">取消</n-button>
          <n-button type="primary" :loading="shipLoading" @click="confirmShip">确认发货</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { h, onMounted, reactive, ref } from 'vue'
import { useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { getOrderList, getOrderDetail, updateOrderStatus, shipOrder } from '@/api/order'
import type { Order, OrderDetail } from '@/api/types'

const message = useMessage()
const pageSize = 10

const statusOptions = [
  { label: '待支付', value: 1 },
  { label: '待发货', value: 2 },
  { label: '待收货', value: 4 },
  { label: '已完成', value: 5 },
  { label: '已取消', value: 6 },
  { label: '退款/售后', value: 9 },
]

const searchForm = reactive({
  order_no: '',
  status: null as number | null,
})

const orders = ref<Order[]>([])
const loading = ref(false)
const page = ref(1)
const pageCount = ref(1)

const showDetail = ref(false)
const detail = ref<OrderDetail | null>(null)

const showShipModal = ref(false)
const shipLoading = ref(false)
const shipForm = reactive({
  order_id: undefined as number | undefined,
  express_company: '',
  express_no: '',
  remark: '',
})

/**
 * 订单表格列
 */
const columns: DataTableColumns<Order> = [
  { title: '订单号', key: 'order_no', width: 160, ellipsis: { tooltip: true } },
  { title: '用户', key: 'user_name', width: 120, ellipsis: { tooltip: true } },
  { title: '实付金额', key: 'pay_amount', width: 100, render(row) { return `¥${row.pay_amount.toFixed(2)}` } },
  { title: '状态', key: 'status_text', width: 90 },
  { title: '下单时间', key: 'created_at', width: 160 },
  {
    title: '操作',
    key: 'actions',
    width: 120,
    render(row) {
      return h('div', { style: 'display: flex; gap: 12px; flex-wrap: wrap;' }, [
        h('a', { onClick: () => openDetail(row.id), style: 'color: #07c160; cursor: pointer;' }, '详情'),
        row.status === 2 ? h('a', { onClick: () => openShipModal(row.id), style: 'color: #07c160; cursor: pointer;' }, '发货') : null,
      ])
    },
  },
]

/**
 * 加载订单列表
 */
async function loadOrders() {
  loading.value = true
  try {
    const res = await getOrderList({
      page: page.value,
      page_size: pageSize,
      order_no: searchForm.order_no || undefined,
      status: searchForm.status ?? undefined,
    })
    orders.value = res.data.data?.list || []
    pageCount.value = Math.ceil((res.data.data?.total || 0) / pageSize) || 1
  } finally {
    loading.value = false
  }
}

/**
 * 重置搜索条件
 */
function resetSearch() {
  searchForm.order_no = ''
  searchForm.status = null
  page.value = 1
  loadOrders()
}

/**
 * 打开订单详情
 * @param id 订单 ID
 */
async function openDetail(id: number) {
  const res = await getOrderDetail(id)
  detail.value = res.data.data
  showDetail.value =  true
}

/**
 * 打开发货弹窗
 * @param id 订单 ID
 */
function openShipModal(id: number) {
  shipForm.order_id = id
  shipForm.express_company = ''
  shipForm.express_no = ''
  shipForm.remark = ''
  showShipModal.value = true
}

/**
 * 当前详情订单发货
 */
function shipCurrentOrder() {
  if (!detail.value) return
  openShipModal(detail.value.id)
}

/**
 * 确认发货
 */
async function confirmShip() {
  if (!shipForm.order_id) return
  shipLoading.value = true
  try {
    await shipOrder(shipForm.order_id, {
      express_company: shipForm.express_company,
      express_no: shipForm.express_no,
      remark: shipForm.remark,
    })
    message.success('发货成功')
    showShipModal.value = false
    loadOrders()
    if (showDetail.value && detail.value) {
      const res = await getOrderDetail(detail.value.id)
      detail.value = res.data.data
    }
  } finally {
    shipLoading.value = false
  }
}

onMounted(() => {
  loadOrders()
})
</script>

<style lang="scss" scoped>
.order-page {
  :deep(.n-table) {
    th, td {
      max-width: 200px;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }
}
</style>
