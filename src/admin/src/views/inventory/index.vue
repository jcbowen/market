<template>
  <div class="inventory-page">
    <n-tabs v-model:value="activeTab" type="line" animated>
      <n-tab-pane name="entry" tab="商品入库">
        <n-card title="SKU 入库">
          <n-space vertical>
            <n-form :model="stockInForm" inline style="flex-wrap: wrap">
              <n-form-item label="商品 ID">
                <n-input-number v-model:value="stockInForm.product_id" placeholder="商品 ID" @update:value="onProductChange" />
              </n-form-item>
              <n-form-item label="选择 SKU">
                <n-select
                  v-model:value="stockInForm.sku_id"
                  :options="skuOptions"
                  placeholder="请先输入商品 ID"
                  style="width: 220px;"
                  :disabled="!skuOptions.length"
                />
              </n-form-item>
              <n-form-item label="入库数量">
                <n-input-number v-model:value="stockInForm.quantity" placeholder="入库数量" :min="1" />
              </n-form-item>
              <n-form-item label="批次号">
                <n-input v-model:value="stockInForm.batch_no" placeholder="批次号" />
              </n-form-item>
              <n-form-item label="生产日期">
                <n-date-picker v-model:formatted-value="stockInForm.production_date" value-format="yyyy-MM-dd" type="date" placeholder="生产日期" />
              </n-form-item>
              <n-form-item label="过期日期">
                <n-date-picker v-model:formatted-value="stockInForm.expire_date" value-format="yyyy-MM-dd" type="date" placeholder="过期日期" />
              </n-form-item>
              <n-form-item label="备注">
                <n-input v-model:value="stockInForm.remark" placeholder="备注" />
              </n-form-item>
              <n-form-item>
                <n-button type="primary" :loading="stockInLoading" @click="handleStockIn">入库</n-button>
              </n-form-item>
            </n-form>
          </n-space>
        </n-card>
      </n-tab-pane>

      <n-tab-pane name="logs" tab="库存流水">
        <n-card title="库存流水">
          <n-space vertical>
            <n-form :model="logSearchForm" inline style="flex-wrap: wrap">
              <n-form-item label="SKU ID">
                <n-input-number v-model:value="logSearchForm.sku_id" placeholder="SKU ID" />
              </n-form-item>
              <n-form-item label="类型">
                <n-select v-model:value="logSearchForm.type" :options="stockTypeOptions" placeholder="全部类型" style="width: 160px;" clearable />
              </n-form-item>
              <n-form-item>
                <n-button type="primary" @click="loadLogs">查询</n-button>
              </n-form-item>
            </n-form>
            <n-data-table :columns="logColumns" :data="logs" :loading="logLoading" scroll-x="720" />
            <n-pagination v-model:page="logPage" :page-count="logPageCount" @update:page="loadLogs" />
          </n-space>
        </n-card>
      </n-tab-pane>

      <n-tab-pane name="warnings" tab="库存预警">
        <n-card title="库存预警">
          <n-space vertical>
            <n-button type="primary" @click="loadWarnings">刷新</n-button>
            <n-data-table :columns="warningColumns" :data="warnings" :loading="warningLoading" scroll-x="600" />
          </n-space>
        </n-card>
      </n-tab-pane>

      <n-tab-pane name="checks" tab="盘点列表">
        <n-card title="盘点列表">
          <n-space vertical>
            <n-space>
              <n-button type="primary" @click="openCheckModal">新增盘点</n-button>
            </n-space>
            <n-data-table :columns="checkColumns" :data="checks" :loading="checkLoading" scroll-x="540" />
            <n-pagination v-model:page="checkPage" :page-count="checkPageCount" @update:page="loadChecks" />
          </n-space>
        </n-card>
      </n-tab-pane>
    </n-tabs>

    <!-- 盘点弹窗 -->
    <n-modal v-model:show="showCheckModal" title="新增盘点" preset="card" style="width: 520px; max-width: 90vw" scrollable>
      <n-form :model="checkForm" label-placement="top">
        <n-form-item label="备注">
          <n-input v-model:value="checkForm.remark" placeholder="备注" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showCheckModal = false">取消</n-button>
          <n-button type="primary" :loading="checkModalLoading" @click="saveCheck">创建盘点单</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { h, onMounted, reactive, ref } from 'vue'
import { useMessage } from 'naive-ui'
import { useRouter } from 'vue-router'
import type { DataTableColumns } from 'naive-ui'
import { stockIn, getInventoryLogList, getInventoryCheckList, createInventoryCheck, getStockWarnings, getSkuList } from '@/api/inventory'
import type { InventoryLog, InventoryCheck, Sku } from '@/api/types'
import { formatDateTime } from '@/utils/datetime'

const message = useMessage()
const router = useRouter()
const pageSize = 10

const activeTab = ref('entry')

/** 库存流水表格列 */
const logColumns: DataTableColumns<InventoryLog> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: 'SKU ID', key: 'sku_id', width: 80 },
  { title: '类型', key: 'type_text', width: 100 },
  { title: '变动数量', key: 'quantity', width: 100 },
  { title: '变动前库存', key: 'before_stock', width: 110 },
  { title: '变动后库存', key: 'after_stock', width: 110 },
  { title: '批次号', key: 'batch_no', width: 120, ellipsis: { tooltip: true } },
  { title: '备注', key: 'remark', width: 140, ellipsis: { tooltip: true } },
  { title: '时间', key: 'created_at', width: 160, render: (row) => formatDateTime(row.created_at) },
]

/** 库存预警表格列 */
const warningColumns: DataTableColumns<{
  sku_id: number
  sku_code: string
  spec_name: string
  product_name: string
  stock: number
  safety_stock: number
  warning_type: number
  expire_date: string
}> = [
  { title: 'SKU ID', key: 'sku_id', width: 80 },
  { title: '商品名称', key: 'product_name', ellipsis: { tooltip: true } },
  { title: '规格', key: 'spec_name', width: 120, ellipsis: { tooltip: true } },
  { title: '当前库存', key: 'stock', width: 100 },
  { title: '安全库存', key: 'safety_stock', width: 100 },
  { title: 'SKU 编码', key: 'sku_code', width: 120, ellipsis: { tooltip: true } },
]

/** 盘点表格列 */
const checkColumns: DataTableColumns<InventoryCheck> = [
  { title: 'ID', key: 'id', width: 60 },
  { title: '盘点单号', key: 'check_no', width: 160, ellipsis: { tooltip: true } },
  { title: 'SKU 总数', key: 'total_items', width: 90 },
  { title: '差异数', key: 'diff_items', width: 90 },
  { title: '状态', key: 'status', width: 90 },
  { title: '备注', key: 'remark', ellipsis: { tooltip: true } },
  { title: '时间', key: 'created_at', width: 160, render: (row) => formatDateTime(row.created_at) },
]

const stockTypeOptions = [
  { label: '入库', value: 1 },
  { label: '出库', value: 2 },
  { label: '盘点', value: 3 },
  { label: '释放', value: 4 },
]

const stockInForm = reactive({
  product_id: undefined as number | undefined,
  sku_id: undefined as number | undefined,
  quantity: undefined as number | undefined,
  batch_no: '',
  production_date: null as string | null,
  expire_date: null as string | null,
  remark: '',
})
const stockInLoading = ref(false)
const skuOptions = ref<{ label: string; value: number }[]>([])

const logs = ref<InventoryLog[]>([])
const logLoading = ref(false)
const logPage = ref(1)
const logPageCount = ref(1)
const logSearchForm = reactive({
  sku_id: undefined as number | undefined,
  type: null as number | null,
})

const warnings = ref<Array<{
  sku_id: number
  sku_code: string
  spec_name: string
  product_name: string
  stock: number
  safety_stock: number
  warning_type: number
  expire_date: string
}>>([])
const warningLoading = ref(false)

const checks = ref<InventoryCheck[]>([])
const checkLoading = ref(false)
const checkPage = ref(1)
const checkPageCount = ref(1)

const showCheckModal = ref(false)
const checkModalLoading = ref(false)
const checkForm = reactive({
  remark: '',
})

/**
 * 商品 ID 变化时加载 SKU 列表
 * @param productId 商品 ID
 */
async function onProductChange(productId: number | null) {
  stockInForm.sku_id = undefined
  skuOptions.value = []
  if (!productId) return

  try {
    const res = await getSkuList(productId)
    const list: Sku[] = res.data.data || []
    skuOptions.value = list.map((sku) => ({
      label: `${sku.sku_code || sku.id} ${sku.spec_name || ''}`.trim(),
      value: sku.id,
    }))
  } catch {
    message.error('加载 SKU 失败')
  }
}

/**
 * SKU 入库
 */
async function handleStockIn() {
  if (!stockInForm.sku_id || !stockInForm.quantity) {
    message.warning('请选择 SKU 并填写入库数量')
    return
  }
  stockInLoading.value = true
  try {
    await stockIn({
      sku_id: stockInForm.sku_id,
      quantity: stockInForm.quantity,
      remark: stockInForm.remark,
      batch_no: stockInForm.batch_no,
      production_date: stockInForm.production_date || undefined,
      expire_date: stockInForm.expire_date || undefined,
    })
    message.success('入库成功')
    stockInForm.sku_id = undefined
    stockInForm.quantity = undefined
    stockInForm.batch_no = ''
    stockInForm.production_date = null
    stockInForm.expire_date = null
    stockInForm.remark = ''
    activeTab.value = 'logs'
    loadLogs()
  } finally {
    stockInLoading.value = false
  }
}

/**
 * 加载库存流水
 */
async function loadLogs() {
  logLoading.value = true
  try {
    const res = await getInventoryLogList({
      page: logPage.value,
      page_size: pageSize,
      sku_id: logSearchForm.sku_id ?? undefined,
      type: logSearchForm.type ?? undefined,
    })
    logs.value = res.data.data.list
    logPageCount.value = Math.ceil(res.data.data.total / pageSize) || 1
  } finally {
    logLoading.value = false
  }
}

/**
 * 加载库存预警
 */
async function loadWarnings() {
  warningLoading.value = true
  try {
    const res = await getStockWarnings()
    warnings.value = res.data.data
  } finally {
    warningLoading.value = false
  }
}

/**
 * 加载盘点列表
 */
async function loadChecks() {
  checkLoading.value = true
  try {
    const res = await getInventoryCheckList({ page: checkPage.value, page_size: pageSize })
    checks.value = res.data.data.list
    checkPageCount.value = Math.ceil(res.data.data.total / pageSize) || 1
  } finally {
    checkLoading.value = false
  }
}

/**
 * 打开新增盘点弹窗
 */
function openCheckModal() {
  checkForm.remark = ''
  showCheckModal.value = true
}

/**
 * 创建盘点单并跳转到详情页录入盘点项
 */
async function saveCheck() {
  checkModalLoading.value = true
  try {
    const res = await createInventoryCheck({ remark: checkForm.remark })
    if (res.data.code === 0 && res.data.data.id) {
      showCheckModal.value = false
      router.push(`/inventory/check/${res.data.data.id}`)
    }
  } finally {
    checkModalLoading.value = false
  }
}

onMounted(() => {
  loadLogs()
  loadWarnings()
  loadChecks()
})
</script>

<style lang="scss" scoped>
.inventory-page {
  .section-card {
    margin-top: 16px;
  }
}
</style>
