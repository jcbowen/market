<template>
  <n-card title="采购管理">
    <n-tabs v-model:value="activeTab" type="line" animated @update:value="onTabChange">
      <!-- 采购单 -->
      <n-tab-pane name="orders" tab="采购单">
        <n-space style="margin-bottom: 12px">
          <n-input
            v-model:value="orderKeyword"
            placeholder="采购单号"
            style="width: 200px"
            clearable
            @keyup.enter="searchOrders"
          />
          <n-select v-model:value="orderStatus" :options="statusOptions" placeholder="状态" clearable style="width: 140px" />
          <n-select
            v-model:value="orderSupplierId"
            :options="supplierFilterOptions"
            filterable
            placeholder="供应商"
            clearable
            style="width: 180px"
          />
          <n-button type="primary" :loading="orderLoading" @click="loadOrders">查询</n-button>
          <n-button type="primary" @click="router.push('/stock/purchase/create')">新建采购单</n-button>
        </n-space>
        <n-data-table
          :columns="orderColumns"
          :data="orderList"
          :loading="orderLoading"
          :pagination="orderPagination"
          remote
          @update:page="onOrderPageChange"
        />
      </n-tab-pane>

      <!-- 供应商 -->
      <n-tab-pane name="suppliers" tab="供应商">
        <n-space style="margin-bottom: 12px">
          <n-input
            v-model:value="supplierKeyword"
            placeholder="供应商名称 / 联系人 / 电话"
            style="width: 240px"
            clearable
            @keyup.enter="onSupplierSearch"
          />
          <n-button type="primary" :loading="supplierLoading" @click="onSupplierSearch">查询</n-button>
          <n-button type="primary" @click="openSupplierForm()">新增供应商</n-button>
        </n-space>
        <n-data-table :columns="supplierColumns" :data="supplierList" :loading="supplierLoading" :pagination="supplierPagination" remote @update:page="onSupplierPageChange" />
      </n-tab-pane>
    </n-tabs>

    <!-- 供应商编辑弹窗 -->
    <n-modal v-model:show="supplierFormVisible" preset="card" :title="supplierForm.id ? '编辑供应商' : '新增供应商'" style="width: 520px">
      <n-form ref="supplierFormRef" :model="supplierForm" :rules="supplierRules" label-placement="left" label-width="80">
        <n-form-item label="名称" path="name">
          <n-input v-model:value="supplierForm.name" placeholder="供应商名称" maxlength="50" />
        </n-form-item>
        <n-form-item label="联系人" path="contact_name">
          <n-input v-model:value="supplierForm.contact_name" placeholder="联系人" maxlength="30" />
        </n-form-item>
        <n-form-item label="电话" path="phone">
          <n-input v-model:value="supplierForm.phone" placeholder="联系电话" maxlength="20" />
        </n-form-item>
        <n-form-item label="地址" path="address">
          <n-input v-model:value="supplierForm.address" type="textarea" placeholder="地址" :rows="2" maxlength="200" />
        </n-form-item>
        <n-form-item label="备注" path="remark">
          <n-input v-model:value="supplierForm.remark" type="textarea" placeholder="备注" :rows="2" maxlength="200" />
        </n-form-item>
        <n-form-item label="状态" path="status">
          <n-switch v-model:value="supplierForm.status" :checked-value="1" :unchecked-value="2">
            <template #checked>启用</template>
            <template #unchecked>停用</template>
          </n-switch>
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="supplierFormVisible = false">取消</n-button>
          <n-button type="primary" :loading="supplierSubmitting" @click="submitSupplier">保存</n-button>
        </n-space>
      </template>
    </n-modal>
  </n-card>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'StockPurchase' })
import { h, ref, onActivated } from 'vue'
import { formatAmount } from '@/utils/format'
import { useRouter, useRoute } from 'vue-router'
import { useDialog, useMessage } from 'naive-ui'
import { NButton, NTag } from 'naive-ui'
import type { DataTableColumns, FormInst, FormRules } from 'naive-ui'
import {
  getPurchaseOrderList,
  submitPurchaseOrder,
  cancelPurchaseOrder,
  getSupplierList,
  createSupplier,
  updateSupplier,
  deleteSupplier,
} from '@/api/inventory'
import type { PurchaseOrder, Supplier } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'

/**
 * 采购管理页面
 * 采购单页签：列表/筛选/提交/取消/详情入口
 * 供应商页签：列表/新增/编辑/删除
 */
const router = useRouter()
const route = useRoute()
const message = useMessage()
const dialog = useDialog()
const activeTab = ref('orders')

// ---------- 采购单 ----------
const orderStatus = ref<number | null>(null)
const orderSupplierId = ref<number | null>(null)
// 单号搜索（入库单「关联采购单」跳转时经 route.query.keyword 预填）
const orderKeyword = ref('')
const orderList = ref<PurchaseOrder[]>([])
const orderLoading = ref(false)
const orderPagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const statusOptions = [
  { label: '草稿', value: 1 },
  { label: '已下单', value: 2 },
  { label: '部分入库', value: 3 },
  { label: '已完结', value: 4 },
  { label: '已取消', value: 5 },
]

const statusMeta: Record<number, { label: string; type: 'default' | 'info' | 'warning' | 'success' | 'error' }> = {
  1: { label: '草稿', type: 'default' },
  2: { label: '已下单', type: 'info' },
  3: { label: '部分入库', type: 'warning' },
  4: { label: '已完结', type: 'success' },
  5: { label: '已取消', type: 'error' },
}

const supplierFilterOptions = ref<{ label: string; value: number }[]>([])

const orderColumns: DataTableColumns<PurchaseOrder> = [
  { title: '采购单号', key: 'purchase_no', width: 170 },
  { title: '供应商', key: 'supplier_name', width: 160, ellipsis: { tooltip: true } },
  {
    title: '状态',
    key: 'status',
    width: 110,
    render: (row) => {
      const meta = statusMeta[row.status] ?? { label: String(row.status), type: 'default' as const }
      return h(NTag, { type: meta.type, size: 'small' }, { default: () => meta.label })
    },
  },
  {
    title: '采购金额',
    key: 'total_amount',
    width: 120,
    render: (row) => `¥${formatAmount(row.total_amount)}`,
  },
  { title: '备注', key: 'remark', ellipsis: { tooltip: true } },
  { title: '创建时间', key: 'created_at', width: 170, render: (row) => formatDateTime(row.created_at) },
  {
    title: '操作',
    key: 'actions',
    width: 220,
    render(row) {
      const actions = [
        h(NButton, { size: 'small', type: 'primary', quaternary: true, onClick: () => router.push(`/stock/purchase/${row.id}`) }, { default: () => '详情' }),
      ]
      if (row.status === 1) {
        actions.push(
          h(NButton, { size: 'small', type: 'primary', quaternary: true, onClick: () => router.push(`/stock/purchase/edit/${row.id}`) }, { default: () => '编辑' }),
          h(NButton, { size: 'small', type: 'info', quaternary: true, onClick: () => onSubmitOrder(row) }, { default: () => '提交' }),
          h(NButton, { size: 'small', type: 'error', quaternary: true, onClick: () => onCancelOrder(row) }, { default: () => '取消' }),
        )
      } else if (row.status === 2 || row.status === 3) {
        actions.push(
          h(NButton, { size: 'small', type: 'success', quaternary: true, onClick: () => router.push(`/stock/in/create?purchase_id=${row.id}`) }, { default: () => '去入库' }),
          h(NButton, { size: 'small', type: 'error', quaternary: true, onClick: () => onCancelOrder(row) }, { default: () => '取消' }),
        )
      }
      return h('span', {}, { default: () => actions })
    },
  },
]

/** 单号搜索：重置到第一页 */
function searchOrders() {
  orderPagination.value.page = 1
  loadOrders()
}

async function loadOrders() {
  orderLoading.value = true
  try {
    const res = await getPurchaseOrderList({
      page: orderPagination.value.page,
      page_size: orderPagination.value.pageSize,
      status: orderStatus.value ?? undefined,
      supplier_id: orderSupplierId.value ?? undefined,
      keyword: orderKeyword.value || undefined,
    })
    if (res.data.code === 0) {
      orderList.value = res.data.data.list
      orderPagination.value.itemCount = res.data.data.total
    }
  } finally {
    orderLoading.value = false
  }
}

function onOrderPageChange(page: number) {
  orderPagination.value.page = page
  loadOrders()
}

async function onSubmitOrder(row: PurchaseOrder) {
  dialog.warning({
    title: '确认提交',
    content: `提交后采购单将变为「已下单」状态，确认提交「${row.purchase_no}」？`,
    positiveText: '确认提交',
    negativeText: '再想想',
    onPositiveClick: async () => {
      // 失败时保持弹窗打开（错误提示由请求拦截器统一弹出），避免误以为已提交
      try {
        await submitPurchaseOrder(row.id)
        message.success('已提交')
        loadOrders()
      } catch {
        return false
      }
    },
  })
}

async function onCancelOrder(row: PurchaseOrder) {
  dialog.warning({
    title: '确认取消',
    content: `取消后采购单不可恢复，确认取消「${row.purchase_no}」？`,
    positiveText: '确认取消',
    negativeText: '再想想',
    onPositiveClick: async () => {
      // 失败时保持弹窗打开（错误提示由请求拦截器统一弹出），避免误以为已取消
      try {
        await cancelPurchaseOrder(row.id)
        message.success('已取消')
        loadOrders()
      } catch {
        return false
      }
    },
  })
}

// ---------- 供应商 ----------
const supplierKeyword = ref('')
const supplierList = ref<Supplier[]>([])
const supplierLoading = ref(false)
const supplierPagination = ref({ page: 1, pageSize: 20, itemCount: 0 })

const supplierColumns: DataTableColumns<Supplier> = [
  { title: 'ID', key: 'id', width: 80 },
  { title: '名称', key: 'name', width: 180, ellipsis: { tooltip: true } },
  { title: '联系人', key: 'contact_name', width: 120 },
  { title: '电话', key: 'phone', width: 140 },
  { title: '地址', key: 'address', ellipsis: { tooltip: true } },
  {
    title: '状态',
    key: 'status',
    width: 90,
    render: (row) =>
      h(NTag, { type: row.status === 1 ? 'success' : 'error', size: 'small' }, { default: () => (row.status === 1 ? '启用' : '停用') }),
  },
  { title: '备注', key: 'remark', width: 140, ellipsis: { tooltip: true } },
  {
    title: '操作',
    key: 'actions',
    width: 140,
    render(row) {
      return h('span', {}, {
        default: () => [
          h(NButton, { size: 'small', type: 'primary', quaternary: true, onClick: () => openSupplierForm(row) }, { default: () => '编辑' }),
          h(NButton, { size: 'small', type: 'error', quaternary: true, onClick: () => onDeleteSupplier(row) }, { default: () => '删除' }),
        ],
      })
    },
  },
]

async function loadSuppliers() {
  supplierLoading.value = true
  try {
    const res = await getSupplierList({
      page: supplierPagination.value.page,
      page_size: supplierPagination.value.pageSize,
      keyword: supplierKeyword.value || undefined,
    })
    if (res.data.code === 0) {
      supplierList.value = res.data.data.list
      supplierPagination.value.itemCount = res.data.data.total
      supplierFilterOptions.value = supplierList.value.map((s) => ({ label: s.name, value: s.id }))
    }
  } finally {
    supplierLoading.value = false
  }
}

/**
 * 供应商列表翻页：数据由服务端按页返回，接上页码事件才会真正翻页
 */
function onSupplierPageChange(page: number) {
  supplierPagination.value.page = page
  loadSuppliers()
}

/**
 * 供应商查询：关键词变化后回到第 1 页，避免停在旧页码看到空列表
 */
function onSupplierSearch() {
  supplierPagination.value.page = 1
  loadSuppliers()
}

const supplierFormVisible = ref(false)
const supplierSubmitting = ref(false)
const supplierFormRef = ref<FormInst | null>(null)
const supplierForm = ref({ id: 0, name: '', contact_name: '', phone: '', address: '', remark: '', status: 1 })

const supplierRules: FormRules = {
  name: { required: true, message: '请输入供应商名称', trigger: 'blur' },
  contact_name: { required: true, message: '请输入联系人', trigger: 'blur' },
  phone: { required: true, message: '请输入联系电话', trigger: 'blur' },
}

function openSupplierForm(row?: Supplier) {
  supplierFormRef.value?.restoreValidation()
  if (row) {
    supplierForm.value = {
      id: row.id,
      name: row.name,
      contact_name: row.contact_name,
      phone: row.phone,
      address: row.address,
      remark: row.remark,
      status: row.status,
    }
  } else {
    supplierForm.value = { id: 0, name: '', contact_name: '', phone: '', address: '', remark: '', status: 1 }
  }
  supplierFormVisible.value = true
}

async function submitSupplier() {
  if (!supplierFormRef.value) return
  await supplierFormRef.value.validate()
  supplierSubmitting.value = true
  try {
    const payload = {
      name: supplierForm.value.name,
      contact_name: supplierForm.value.contact_name,
      phone: supplierForm.value.phone,
      address: supplierForm.value.address,
      remark: supplierForm.value.remark,
      status: supplierForm.value.status,
    }
    const res = supplierForm.value.id ? await updateSupplier(supplierForm.value.id, payload) : await createSupplier(payload)
    if (res.data.code === 0) {
      message.success('保存成功')
      supplierFormVisible.value = false
      loadSuppliers()
    }
  } finally {
    supplierSubmitting.value = false
  }
}

function onDeleteSupplier(row: Supplier) {
  dialog.warning({
    title: '确认删除',
    content: `删除后不可恢复。若该供应商已被采购单/入库单引用，将无法删除。确认删除「${row.name}」？`,
    positiveText: '确认删除',
    negativeText: '再想想',
    onPositiveClick: async () => {
      // 失败时保持弹窗打开（错误提示由请求拦截器统一弹出），避免误以为已删除
      try {
        await deleteSupplier(row.id)
        message.success('已删除')
        loadSuppliers()
      } catch {
        return false
      }
    },
  })
}

// ---------- 页签懒加载 ----------
function onTabChange(value: string) {
  if (value === 'suppliers') loadSuppliers()
}

// 外部入口（入库单/入库列表「关联采购单」跳转带 keyword、工作台一键补货带供应商筛选）
// 在 keep-alive 缓存下再次进入不触发 onMounted，重读 query 必须放在 onActivated，
// 否则跳转命中缓存会沿用上一次筛选（与 order/list.vue、order/refunds.vue 同口径）
// 数据加载同样按页签分支：停在供应商页签时不该只拉采购单
onActivated(() => {
  const q = route.query.keyword
  if (typeof q === 'string' && q) orderKeyword.value = q
  if (activeTab.value === 'suppliers') {
    loadSuppliers()
    return
  }
  loadOrders()
})
</script>
