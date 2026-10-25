<template>
  <div class="orders-tab">
    <!-- 筛选区：主条件行 + 低频条件折叠区（口径与后台订单列表一致，后端 ListOrders 全量支持） -->
    <div class="toolbar">
      <n-input
        v-model:value="query.order_no"
        placeholder="订单号前缀匹配"
        clearable
        size="small"
        class="order-no-input"
        @keydown.enter="onSearch"
        @clear="onSearch"
      />
      <n-select
        v-model:value="query.status"
        :options="statusOptions"
        size="small"
        class="status-select"
        clearable
        placeholder="订单状态"
        @update:value="onSearch"
      />
      <n-select
        v-model:value="query.source"
        :options="orderSourceFilterOptions"
        size="small"
        class="source-select"
        clearable
        placeholder="订单来源"
        @update:value="onSearch"
      />
      <n-button type="primary" size="small" :loading="loading" @click="onSearch">查询</n-button>
      <n-button size="small" @click="toggleMoreFilters">
        {{ showMoreFilters ? '收起筛选' : '更多筛选' }}
        <template v-if="moreFilterCount > 0 && !showMoreFilters">（{{ moreFilterCount }}）</template>
      </n-button>
    </div>

    <!-- 已设筛选条件回显（折叠态可见，标签 × 清除对应条件） -->
    <div v-if="!showMoreFilters && activeFilterTags.length" class="filter-tags">
      <n-tag
        v-for="t in activeFilterTags"
        :key="t.key"
        size="small"
        type="info"
        closable
        @close="clearFilter(t.key)"
      >
        {{ t.label }}
      </n-tag>
    </div>

    <!-- 折叠更多筛选区：账单号/会员/商品/取货码/收货人/电话/时间+快捷档/班次 -->
    <div v-else-if="showMoreFilters" class="more-filters">
      <div class="filter-item">
        <span class="f-label">账单号</span>
        <n-input
          v-model:value="query.pay_no"
          placeholder="支付单号 / 渠道交易号"
          clearable
          size="small"
          class="w-input"
          @keyup.enter="onSearch"
          @clear="onSearch"
        />
      </div>
      <div class="filter-item">
        <span class="f-label">会员</span>
        <n-button v-if="!query.user_id" dashed size="small" @click="userPickerVisible = true">+ 选择会员</n-button>
        <n-tag v-else size="small" type="primary" closable @close="clearFilter('user_id')">{{ query.user_name }}</n-tag>
      </div>
      <div class="filter-item">
        <span class="f-label">商品</span>
        <n-button v-if="!query.product_id" dashed size="small" @click="productPickerVisible = true">+ 选择商品</n-button>
        <n-tag v-else size="small" type="primary" closable @close="clearFilter('product_id')">{{ query.product_name }}</n-tag>
      </div>
      <div class="filter-item">
        <span class="f-label">取货码</span>
        <n-input
          v-model:value="query.pickup_code"
          placeholder="8 位取货码"
          clearable
          size="small"
          maxlength="8"
          class="w-input"
          :input-props="{ inputmode: 'numeric' }"
          @keyup.enter="onSearch"
          @clear="onSearch"
        />
      </div>
      <div class="filter-item">
        <span class="f-label">收货人</span>
        <n-input
          v-model:value="query.receiver_name"
          placeholder="姓名模糊"
          clearable
          size="small"
          class="w-input"
          @keyup.enter="onSearch"
          @clear="onSearch"
        />
      </div>
      <div class="filter-item">
        <span class="f-label">电话</span>
        <n-input
          v-model:value="query.receiver_phone"
          placeholder="手机号"
          clearable
          size="small"
          class="w-input"
          @keyup.enter="onSearch"
          @clear="onSearch"
        />
      </div>
      <div class="filter-item">
        <span class="f-label">下单时间</span>
        <div class="f-inline">
          <n-date-picker v-model:value="dateRange" type="daterange" clearable size="small" class="date-picker" @update:value="onDateRangeChange" />
          <div class="quick-chips">
            <n-button
              v-for="q in dateQuickOptions"
              :key="q.key"
              size="tiny"
              :secondary="dateQuick !== q.key"
              :type="dateQuick === q.key ? 'primary' : 'default'"
              @click="applyDateQuick(q.key)"
            >
              {{ q.label }}
            </n-button>
          </div>
        </div>
      </div>
      <div class="filter-item">
        <span class="f-label">班次</span>
        <div class="f-inline">
          <n-input-number
            v-model:value="query.shift_id"
            :min="0"
            :update-value-on-input="false"
            size="small"
            clearable
            placeholder="班次 ID"
            class="w-shift"
            @update:value="onShiftChange"
          />
          <n-button size="tiny" :disabled="!posStore.currentShift" @click="applyCurrentShift">本班</n-button>
        </div>
      </div>
      <div class="filter-item actions">
        <n-button size="small" @click="resetFilters">重置</n-button>
      </div>
    </div>

    <!-- 订单列表 -->
    <n-data-table
      :columns="columns"
      :data="orders"
      :loading="loading"
      :row-key="(row: Order) => row.id"
      size="small"
      class="table"
    />
    <div class="pagination">
      <n-pagination
        :page="page"
        :page-size="pageSize"
        :item-count="total"
        :page-sizes="[10, 20, 50]"
        show-size-picker
        @update:page="onPageChange"
        @update:page-size="onPageSizeChange"
      />
    </div>

    <!-- 订单详情 + 处理弹窗 -->
    <n-modal
      v-model:show="showDetail"
      preset="card"
      title="订单详情"
      style="width: 720px; max-width: 94vw"
    >
      <n-spin :show="detailLoading">
        <template v-if="detail">
          <div class="detail-grid">
            <div class="detail-item"><span class="label">订单号</span><span>{{ detail.order_no }}</span></div>
            <div class="detail-item"><span class="label">状态</span><n-tag size="small" :type="orderStatusTagType(detail.status)">{{ detail.status_text }}</n-tag></div>
            <div class="detail-item"><span class="label">来源</span><span>{{ orderSourceText(detail.source ?? 1) }}</span></div>
            <div class="detail-item">
              <span class="label">会员</span>
              <!-- 关联会员可点击查看其历史订单/常买商品 -->
              <n-button
                v-if="detail.user_id > 0"
                size="tiny"
                text
                type="primary"
                @click="openUserOrders(detail)"
              >
                {{ detail.user_name || `用户${detail.user_id}` }}
              </n-button>
              <template v-else>
                <span>{{ detail.user_name || '散客' }}</span>
                <!-- 散客单补绑会员：仅已收款单开放（待支付走继续收款时选会员），绑后不可换绑 -->
                <n-button
                  v-if="canBindUser(detail)"
                  size="tiny"
                  text
                  type="warning"
                  :disabled="bindingUser"
                  @click="openBindUser(detail)"
                >
                  绑定会员
                </n-button>
              </template>
            </div>
            <div class="detail-item"><span class="label">配送方式</span><span>{{ deliveryTypeText(detail.delivery_type) }}</span></div>
            <div class="detail-item">
              <span class="label">支付方式</span>
              <pay-type-label :pay-type="detail.pay_type" :text="detail.pay_type_text" />
            </div>
            <div class="detail-item"><span class="label">下单时间</span><span>{{ formatDateTime(detail.created_at) }}</span></div>
            <div class="detail-item"><span class="label">收银员</span><span>{{ detail.operator_name || '-' }}</span></div>
            <div class="detail-item"><span class="label">班次</span><span>{{ detail.shift_id ? `#${detail.shift_id}` : '-' }}</span></div>
            <div v-if="detail.pickup_code" class="detail-item"><span class="label">取货码</span><span class="pickup-code">{{ detail.pickup_code }}</span></div>
            <div v-if="detail.remark" class="detail-item span2"><span class="label">备注</span><span>{{ detail.remark }}</span></div>
          </div>

          <!-- 商品明细（含改价标记）；直接收款单无商品，给出空状态避免空白区块 -->
          <div class="section-title">商品明细</div>
          <n-empty v-if="!detail.items?.length" description="该订单无商品明细（直接收款）" size="small" class="items-empty" />
          <div v-else class="items-table">
            <div v-for="item in detail.items" :key="item.id" class="item-row">
              <span class="item-name">
                {{ item.product_name }}
                <template v-if="item.sku_name">（{{ item.sku_name }}）</template>
                <n-tag v-if="item.price_modified" size="tiny" type="warning" :bordered="false" class="mod-tag">已改价</n-tag>
              </span>
              <span class="item-price">
                ￥{{ formatAmount(item.price) }}
                <span v-if="item.price_modified" class="origin">￥{{ formatAmount(item.original_price ?? item.price) }}</span>
              </span>
              <span class="item-qty">x{{ item.quantity }}</span>
              <span class="item-subtotal">￥{{ formatAmount(item.total_amount) }}</span>
            </div>
          </div>

          <!-- 金额构成 -->
          <div class="amount-block">
            <div class="amount-row"><span>商品总额</span><span>￥{{ formatAmount(detail.total_amount) }}</span></div>
            <div v-if="detail.discount_amount > 0" class="amount-row"><span>系统优惠</span><span>-￥{{ formatAmount(detail.discount_amount) }}</span></div>
            <div v-if="(detail.manual_discount ?? 0) > 0" class="amount-row"><span>收银优惠（改价/抹零）</span><span>-￥{{ formatAmount(detail.manual_discount) }}</span></div>
            <div v-if="detail.delivery_fee > 0" class="amount-row"><span>配送费</span><span>+￥{{ formatAmount(detail.delivery_fee) }}</span></div>
            <div class="amount-row strong"><span>实付</span><span>￥{{ formatAmount(detail.pay_amount) }}</span></div>
          </div>

          <!-- 售后记录（每次退款单：类型/金额/状态/时间） -->
          <template v-if="detail.refunds && detail.refunds.length">
            <div class="section-title">售后记录</div>
            <div class="refund-list">
              <div v-for="refund in detail.refunds" :key="refund.id" class="refund-row">
                <span class="refund-no">{{ refund.refund_no }}</span>
                <n-tag size="tiny" :type="refundTypeTagType(refund.type)" :bordered="false">{{ refundTypeText(refund.type) }}</n-tag>
                <span class="refund-amount">￥{{ formatAmount(refund.amount) }}</span>
                <n-tag size="tiny" :type="refundStatusTagType(refund.status)" :bordered="false">{{ refundStatusText(refund.status) }}</n-tag>
                <span class="refund-time">{{ formatDateTime(refund.refund_time || refund.created_at) }}</span>
              </div>
            </div>
          </template>

          <!-- 处理操作（按状态与权限显示，全部弹窗内完成） -->
          <n-space justify="end" class="detail-actions">
            <n-button
              v-if="canRepay"
              size="small"
              type="warning"
              @click="openRepay(detail)"
            >
              继续收款
            </n-button>
            <n-button
              v-if="canCancel"
              size="small"
              type="error"
              secondary
              @click="showCancelModal = true"
            >
              取消订单
            </n-button>
            <n-button
              v-if="canRefund"
              size="small"
              type="warning"
              secondary
              @click="showRefundModal = true"
            >
              代客退款
            </n-button>
            <n-button
              v-if="canVerify"
              size="small"
              type="primary"
              @click="onVerify"
              :loading="verifying"
            >
              自提核销
            </n-button>
          </n-space>
        </template>
      </n-spin>
    </n-modal>

    <!-- 继续收款弹窗（待支付单：付款码 / 现金两路） -->
    <n-modal
      v-model:show="showRepay"
      preset="card"
      title="继续收款"
      style="width: 380px; max-width: 92vw"
    >
      <div v-if="repayOrder" class="repay-wrap">
        <div class="repay-amount">￥{{ formatAmount(repayOrder.pay_amount) }}</div>
        <div class="repay-order muted">{{ repayOrder.order_no }}</div>
        <n-space vertical :size="10">
          <n-button type="primary" block size="large" :loading="repayPreparing" @click="onRepayByCode">
            扫顾客付款码收款
          </n-button>
          <n-button block size="large" type="warning" secondary @click="onRepayByCash">
            收现金（顾客改用现金）
          </n-button>
        </n-space>
        <n-text depth="3" style="font-size: 12px">付款码收款失败可在弹窗内改用现金收款</n-text>
      </div>
    </n-modal>

    <!-- 付款码扫码弹窗（待支付单继续收款，与商品下单共用） -->
    <code-pay-dialog
      v-model:show="showRepayCodePay"
      :order-id="repayOrder?.id ?? 0"
      :order-no="repayOrder?.order_no ?? ''"
      :amount="repayOrder?.pay_amount ?? 0"
      :payment-no="repayPaymentNo"
      @paid="onRepayPaid"
      @close-abandoned="onRepayAbandoned"
    />

    <!-- 会员选择器（筛选用，单选；复用公共 UserPicker） -->
    <user-picker v-model:show="userPickerVisible" title="按会员筛选订单" @select="onUserPicked" />
    <!-- 会员选择器（散客单补绑会员用，详情弹窗内触发；与筛选用互不干扰） -->
    <user-picker v-model:show="bindPickerVisible" title="补绑会员" @select="onBindUserSelect" />

    <!-- 商品选择器（筛选用，单选；与后台订单列表同款：仅上架商品 + 关键字防抖 + 分页） -->
    <n-modal
      v-model:show="productPickerVisible"
      preset="card"
      title="按商品筛选订单"
      style="width: 520px; max-width: 92vw"
    >
      <n-input
        v-model:value="productKeyword"
        placeholder="按商品名搜索"
        clearable
        style="margin-bottom: 12px"
      />
      <n-spin :show="productLoading">
        <n-empty v-if="!productOptions.length && !productLoading" description="暂无商品" style="padding: 40px 0" />
        <div v-else class="product-list">
          <div
            v-for="p in productOptions"
            :key="p.id"
            class="product-row"
            @click="onProductPicked(p)"
          >
            <span class="name">{{ p.name }}</span>
            <span class="meta">{{ p.min_price !== undefined ? `¥${p.min_price} 起` : '' }}</span>
          </div>
        </div>
      </n-spin>
      <n-pagination
        v-model:page="productPage"
        :item-count="productTotal"
        :page-size="10"
        @update:page="loadProductOptions"
      />
    </n-modal>

    <!-- 取消订单弹窗 -->
    <n-modal
      v-model:show="showCancelModal"
      preset="dialog"
      type="warning"
      title="取消订单"
      positive-text="确认取消"
      negative-text="关闭"
      :loading="cancelling"
      @positive-click="onCancel"
    >
      <n-input v-model:value="cancelReason" type="textarea" placeholder="取消原因（必填）" :rows="2" />
    </n-modal>

    <!-- 代客退款弹窗（与后台订单页共用：整单/部分商品/仅金额退，金额可调低） -->
    <refund-modal v-model:show="showRefundModal" :order-id="detail?.id ?? null" @success="onRefundSuccess" />

    <!-- 会员订单弹窗：点击会员查看其历史订单与常买商品（订单号点击经 open-order 转发到详情弹窗） -->
    <user-orders-modal v-model:show="showUserOrders" :user="userOrdersUser" @open-order="openOrderDetail" />

    <!-- 订单详情弹窗（会员订单内订单号点击打开；用户点击经 view-user 回到会员订单弹窗） -->
    <order-detail-modal v-model:show="showOrderDetail" :order-id="orderDetailId" @view-user="openUserOrdersOf" />
  </div>
</template>

<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { formatAmount } from '@/utils/format'
import { NButton, NEmpty, NTag, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { bindOrderUser, cancelOrder, ensurePosCodePaySession, getOrderDetail, getOrderList, payPosOrderCash, verifyPickup } from '@/api/order'
import { getProductList } from '@/api/product'
import type { Order, OrderDetail, Product, User } from '@/api/types'
import { ORDER_STATUS, QUERY_STATUS, canAdminRefund, deliveryTypeText, orderSourceFilterOptions, orderSourceText, orderStatusTagType, refundStatusText, refundStatusTagType, refundTypeText, refundTypeTagType } from '@/constants/order'
import { useAuthStore } from '@/stores/auth'
import { usePosStore } from '@/stores/pos'
import { formatDateTime } from '@/utils/datetime'
import UserPicker from '@/components/UserPicker.vue'
import PayTypeLabel from '@/components/PayTypeLabel.vue'
import RefundModal from '@/views/order/components/RefundModal.vue'
import UserOrdersModal from '@/views/user/components/UserOrdersModal.vue'
import OrderDetailModal from '@/views/order/detail.vue'
import CodePayDialog from './CodePayDialog.vue'
import { reopenModal } from '@/utils/modal'

/**
 * 订单查询 Tab（收银台内）
 * 筛选口径与后台订单列表一致（后端 ListOrders 全量支持）：主条件行（订单号前缀匹配/状态/来源）
 * + 折叠更多筛选（账单号/会员/商品/取货码/收货人/电话/下单时间+快捷档/班次），低频项折叠控制密度；
 * 筛选变更统一回到第一页刷新，已设条件以可关闭标签回显；详情弹窗内完成继续收款/取消/代客退款/自提核销，
 * 处理按钮按状态与权限显示（取消/核销=order:update，退款=refund:update），不打断收银流程跳后台；
 * 待支付单支持「继续收款」（付款码/现金两路，扣款失败可换现金）；
 * 「引用到收银台」把订单商品写入 posStore 待引用清单并切回商品下单 Tab（仅限有商品明细的订单）；
 * 代客退款与后台共用 RefundModal（整单/部分商品/仅金额退，金额可调低），详情弹窗内展示每次售后记录
 */

const message = useMessage()
const dialog = useDialog()
const authStore = useAuthStore()
const posStore = usePosStore()

/** 引用到收银台事件（父组件切回商品下单 Tab） */
const emit = defineEmits<{ (e: 'import-to-cashier'): void }>()

// --- 列表查询（筛选口径与后台订单列表一致，后端 ListOrders 全量支持）---
interface OrderQuery {
  order_no: string
  pay_no: string
  status: number | null
  source: number | null
  user_id: number
  user_name: string
  product_id: number
  product_name: string
  pickup_code: string
  receiver_name: string
  receiver_phone: string
  shift_id: number
}
const query = ref<OrderQuery>({
  order_no: '',
  pay_no: '',
  status: null,
  source: null,
  user_id: 0,
  user_name: '',
  product_id: 0,
  product_name: '',
  pickup_code: '',
  receiver_name: '',
  receiver_phone: '',
  shift_id: 0,
})
const orders = ref<Order[]>([])
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)
const loading = ref(false)

/** 请求序号：筛选/翻页交错时只采用最后一次请求的响应，防慢响应后到覆盖新结果 */
let loadSeq = 0

/** 状态筛选选项（含查询聚合虚拟状态） */
const statusOptions = [
  { label: '待支付', value: ORDER_STATUS.PENDING_PAY },
  { label: '待发货/待自提', value: QUERY_STATUS.SHIPPING_AGGREGATE },
  { label: '备货中', value: ORDER_STATUS.STOCKING },
  { label: '配送中', value: ORDER_STATUS.SHIPPING },
  { label: '已完成', value: ORDER_STATUS.COMPLETED },
  { label: '已取消', value: ORDER_STATUS.CANCELLED },
  { label: '售后中', value: QUERY_STATUS.REFUND_AGGREGATE },
]

/** 可清除的筛选项标识（回显标签 × 与折叠区内标签 × 共用，clearFilter 按 key 定位） */
type FilterKey = 'pay_no' | 'user_id' | 'product_id' | 'pickup_code' | 'receiver_name' | 'receiver_phone' | 'shift_id' | 'date'

/** 时间快捷档：today/yesterday/7d/30d；null 表示当前是手选日期（与后台订单列表同口径） */
type DateQuickKey = 'today' | 'yesterday' | '7d' | '30d'
const dateQuick = ref<DateQuickKey | null>(null)
/** 时间快捷选项（与手选日期范围互斥联动） */
const dateQuickOptions: Array<{ key: DateQuickKey; label: string }> = [
  { key: 'today', label: '今天' },
  { key: 'yesterday', label: '昨天' },
  { key: '7d', label: '近7天' },
  { key: '30d', label: '近30天' },
]
const dateRange = ref<[number, number] | null>(null)

/** 折叠筛选区展开态 */
const showMoreFilters = ref(false)

/** 折叠区已设置的条件数（「更多筛选」按钮计数角标，不含主条件行三项） */
const moreFilterCount = computed(() => {
  const q = query.value
  return (
    (q.pay_no ? 1 : 0) +
    (q.user_id > 0 ? 1 : 0) +
    (q.product_id > 0 ? 1 : 0) +
    (q.pickup_code ? 1 : 0) +
    (q.receiver_name ? 1 : 0) +
    (q.receiver_phone ? 1 : 0) +
    (dateRange.value ? 1 : 0) +
    (q.shift_id > 0 ? 1 : 0)
  )
})

/** 已设筛选条件回显标签（折叠态展示；key 与 clearFilter 清除目标对应） */
const activeFilterTags = computed<Array<{ key: FilterKey; label: string }>>(() => {
  const q = query.value
  const tags: Array<{ key: FilterKey; label: string }> = []
  if (q.pay_no) tags.push({ key: 'pay_no', label: `账单号 ${q.pay_no}` })
  if (q.user_id > 0) tags.push({ key: 'user_id', label: `会员 ${q.user_name}` })
  if (q.product_id > 0) tags.push({ key: 'product_id', label: `商品 ${q.product_name}` })
  if (q.pickup_code) tags.push({ key: 'pickup_code', label: `取货码 ${q.pickup_code}` })
  if (q.receiver_name) tags.push({ key: 'receiver_name', label: `收货人 ${q.receiver_name}` })
  if (q.receiver_phone) tags.push({ key: 'receiver_phone', label: `电话 ${q.receiver_phone}` })
  if (q.shift_id > 0) tags.push({ key: 'shift_id', label: `班次 #${q.shift_id}` })
  if (dateQuick.value) {
    tags.push({ key: 'date', label: dateQuickOptions.find((o) => o.key === dateQuick.value)?.label ?? '' })
  } else if (dateRange.value) {
    tags.push({ key: 'date', label: '自定义时间段' })
  }
  return tags
})

/** 时间戳 → YYYY-MM-DD（本地时区，与后台订单列表同实现） */
function fmtDate(ts: number): string {
  const d = new Date(ts)
  const m = `${d.getMonth() + 1}`.padStart(2, '0')
  const day = `${d.getDate()}`.padStart(2, '0')
  return `${d.getFullYear()}-${m}-${day}`
}

/**
 * 组装查询条件（与后台订单列表 buildQuery 同字段同口径；
 * start_time/end_time 传 YYYY-MM-DD，后端 end_time 自动补齐当日末尾成闭区间）
 */
function buildQuery() {
  const [start, end] = dateRange.value ?? []
  const q = query.value
  return {
    page: page.value,
    page_size: pageSize.value,
    order_no: q.order_no.trim() || undefined,
    pay_no: q.pay_no.trim() || undefined,
    status: q.status ?? undefined,
    source: q.source ?? undefined,
    user_id: q.user_id > 0 ? q.user_id : undefined,
    product_id: q.product_id > 0 ? q.product_id : undefined,
    pickup_code: q.pickup_code.trim() || undefined,
    receiver_name: q.receiver_name.trim() || undefined,
    receiver_phone: q.receiver_phone.trim() || undefined,
    shift_id: q.shift_id > 0 ? q.shift_id : undefined,
    start_time: start ? fmtDate(start) : undefined,
    end_time: end ? fmtDate(end) : undefined,
  }
}

/**
 * 拉取订单列表（本社区语境，C端+收银台全量；请求序号防竞态）
 */
async function loadOrders() {
  const seq = ++loadSeq
  loading.value = true
  try {
    const res = await getOrderList(buildQuery())
    if (seq !== loadSeq) return
    orders.value = res.data.data.list || []
    total.value = res.data.data.total
  } catch {
    // 失败提示由请求拦截器统一弹出
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

/**
 * 查询（筛选条件变化统一回到第一页，与后台订单列表 onSearch 口径一致）
 */
function onSearch() {
  page.value = 1
  void loadOrders()
}

/**
 * 翻页
 * @param p 页码
 */
function onPageChange(p: number) {
  page.value = p
  void loadOrders()
}

/**
 * 调整每页条数
 * @param size 每页条数
 */
function onPageSizeChange(size: number) {
  pageSize.value = size
  page.value = 1
  void loadOrders()
}

// --- 筛选条件管理（折叠区 / 回显标签 / 时间快捷档 / 重置）---

/**
 * 展开/收起折叠筛选区
 */
function toggleMoreFilters() {
  showMoreFilters.value = !showMoreFilters.value
}

/**
 * 清除单个筛选条件并刷新（回显标签 × 与折叠区内标签 × 共用）
 * @param key 筛选项标识（date 同时清空手选范围与快捷档标记）
 */
function clearFilter(key: FilterKey) {
  const q = query.value
  switch (key) {
    case 'date':
      dateRange.value = null
      dateQuick.value = null
      break
    case 'user_id':
      q.user_id = 0
      q.user_name = ''
      break
    case 'product_id':
      q.product_id = 0
      q.product_name = ''
      break
    case 'shift_id':
      q.shift_id = 0
      break
    case 'pay_no':
      q.pay_no = ''
      break
    case 'pickup_code':
      q.pickup_code = ''
      break
    case 'receiver_name':
      q.receiver_name = ''
      break
    case 'receiver_phone':
      q.receiver_phone = ''
      break
  }
  onSearch()
}

/**
 * 应用时间快捷档：填充日期范围并刷新（再点同档取消，与后台订单列表同口径）
 * @param key 快捷档 today/yesterday/7d/30d
 */
function applyDateQuick(key: DateQuickKey) {
  if (dateQuick.value === key) {
    // 再点同档：取消快捷并清空日期
    dateQuick.value = null
    dateRange.value = null
  } else {
    dateQuick.value = key
    const now = new Date()
    const start = new Date(now)
    const end = new Date(now)
    if (key === 'yesterday') {
      start.setDate(start.getDate() - 1)
      end.setTime(start.getTime())
    } else if (key === '7d') {
      start.setDate(start.getDate() - 6)
    } else if (key === '30d') {
      start.setDate(start.getDate() - 29)
    }
    dateRange.value = [start.getTime(), end.getTime()]
  }
  onSearch()
}

/**
 * 手选日期变化：清除快捷档标记（互斥联动）并刷新
 * @param value 新日期范围（null=已清空）
 */
function onDateRangeChange(value: [number, number] | null) {
  dateQuick.value = null
  void value
  onSearch()
}

/**
 * 快捷筛选「本班」：填入当前进行中班次 ID 并刷新（无进行中班次时按钮禁用）
 */
function applyCurrentShift() {
  const shiftId = posStore.currentShift?.id ?? 0
  if (shiftId <= 0) return
  query.value.shift_id = shiftId
  onSearch()
}

/**
 * 班次输入变更（含清空为 null）：归一为 0 后刷新
 * 输入框设 update-value-on-input=false：键入过程不提交，回车/失焦/清空才触发本回调，
 * 避免逐字符触发中间态查询
 * @param v 输入值（null=已清空）
 */
function onShiftChange(v: number | null) {
  query.value.shift_id = v && v > 0 ? v : 0
  onSearch()
}

/**
 * 重置全部筛选条件并刷新（回到「全部订单 · 无条件」初始态）
 */
function resetFilters() {
  const q = query.value
  q.order_no = ''
  q.pay_no = ''
  q.status = null
  q.source = null
  q.user_id = 0
  q.user_name = ''
  q.product_id = 0
  q.product_name = ''
  q.pickup_code = ''
  q.receiver_name = ''
  q.receiver_phone = ''
  q.shift_id = 0
  dateRange.value = null
  dateQuick.value = null
  onSearch()
}

// --- 会员/商品选择器（筛选用，单选）---

const userPickerVisible = ref(false)

/**
 * 会员选择器回调：写入筛选条件并刷新（单选）
 * @param user 选中的会员
 */
function onUserPicked(user: User) {
  query.value.user_id = user.id
  query.value.user_name = user.nickname || user.phone || `用户${user.id}`
  onSearch()
}

const productPickerVisible = ref(false)
const productKeyword = ref('')
const productOptions = ref<Product[]>([])
const productTotal = ref(0)
const productPage = ref(1)
const productLoading = ref(false)

/** 商品关键字防抖定时器（卸载时清理） */
let productSearchTimer: ReturnType<typeof setTimeout> | null = null

// 商品关键字 300ms 防抖搜索（与后台订单列表同款；回调守卫防关闭后残留定时器空发请求）
watch(productKeyword, () => {
  if (productSearchTimer) clearTimeout(productSearchTimer)
  productSearchTimer = setTimeout(() => {
    productSearchTimer = null
    if (!productPickerVisible.value) return
    productPage.value = 1
    void loadProductOptions()
  }, 300)
})

/**
 * 打开商品选择器时加载第一页；关闭时清空关键词与残留防抖，
 * 保证下次打开是干净态且不会叠加冗余请求
 */
watch(productPickerVisible, (v) => {
  if (v) {
    if (productSearchTimer) {
      clearTimeout(productSearchTimer)
      productSearchTimer = null
    }
    productPage.value = 1
    void loadProductOptions()
  } else {
    productKeyword.value = ''
  }
})

/**
 * 加载商品分页列表（仅上架商品，供筛选选择）
 */
async function loadProductOptions() {
  productLoading.value = true
  try {
    const res = await getProductList({
      page: productPage.value,
      page_size: 10,
      status: 1,
      keyword: productKeyword.value.trim() || undefined,
    })
    if (res.data.code === 0) {
      productOptions.value = res.data.data.list || []
      productTotal.value = res.data.data.total || 0
    }
  } catch {
    // 失败提示由请求拦截器统一弹出
  } finally {
    productLoading.value = false
  }
}

/**
 * 商品选择器回调：写入筛选条件并刷新（按商品过滤含全部规格）
 * @param p 选中的商品
 */
function onProductPicked(p: Product) {
  query.value.product_id = p.id
  query.value.product_name = p.name
  productPickerVisible.value = false
  onSearch()
}

// --- 详情弹窗 ---
const showDetail = ref(false)
const detailLoading = ref(false)
const detail = ref<OrderDetail | null>(null)
const verifying = ref(false)
const showCancelModal = ref(false)
const cancelling = ref(false)
const cancelReason = ref('')
const showRefundModal = ref(false)

/** 是否持有订单处理权限（取消/核销接口要求 order:update） */
const hasUpdatePermission = computed(() => authStore.isSuperAdmin || authStore.hasPermission('order:update'))
/** 是否持有退款权限（代客退款接口要求 refund:update） */
const hasRefundPermission = computed(() => authStore.isSuperAdmin || authStore.hasPermission('refund:update'))

/** 可取消状态集：仅待支付（已支付订单走代客退款，避免取消/退款按钮并存歧义） */
const CANCELABLE_STATUSES: number[] = [ORDER_STATUS.PENDING_PAY]

/** 可取消：未发货且有权限 */
const canCancel = computed(
  () => !!detail.value && hasUpdatePermission.value && CANCELABLE_STATUSES.includes(detail.value.status),
)
/**
 * 可退款：状态口径统一由 canAdminRefund 判定（已支付/备货中/配送中，
 * 以及到店单的已完成——到店单收款即自动完结，门店售出后仍需能退款），与后端一致
 */
const canRefund = computed(() => !!detail.value && hasRefundPermission.value && canAdminRefund(detail.value))
/** 可核销：自提单待自提（已支付）且有权限 */
const canVerify = computed(
  () => !!detail.value && hasUpdatePermission.value && detail.value.status === ORDER_STATUS.PAID && detail.value.delivery_type === 1,
)
/** 可继续收款：待支付收银台单（与后端 pay-cash/pay-code-session 口径一致） */
const canRepay = computed(
  () => !!detail.value && detail.value.status === ORDER_STATUS.PENDING_PAY && detail.value.source === 2,
)

// --- 继续收款（待支付单：付款码/现金两路） ---
const showRepay = ref(false)
const showRepayCodePay = ref(false)
const repayOrder = ref<Order | OrderDetail | null>(null)
const repayPaymentNo = ref('')
const repayPreparing = ref(false)

/** 确认弹窗聚焦定时器（组件卸载时清理，避免已卸载实例操作 DOM） */
let dialogFocusTimer: number | undefined

/**
 * 弹窗渲染后聚焦确认按钮，回车即确认（不依赖鼠标）
 * @param selector 确认按钮选择器
 */
function focusDialogConfirm(selector = '.n-dialog__action button:last-of-type') {
  if (dialogFocusTimer !== undefined) window.clearTimeout(dialogFocusTimer)
  dialogFocusTimer = window.setTimeout(() => {
    dialogFocusTimer = undefined
    const btn = document.querySelector(selector) as HTMLButtonElement | null
    btn?.focus()
  }, 150)
}

/**
 * 打开继续收款弹窗（详情弹窗或列表行入口）
 * @param order 订单行/详情
 */
function openRepay(order: Order | OrderDetail) {
  repayOrder.value = order
  repayPaymentNo.value = ''
  showRepay.value = true
}

/**
 * 付款码路径：先向后端要收款会话（复用遗留待支付单，out_trade_no 不变），
 * 再打开扫码窗；渠道未开启时后端提示改现金
 */
async function onRepayByCode() {
  if (!repayOrder.value || repayPreparing.value) return
  repayPreparing.value = true
  try {
    const res = await ensurePosCodePaySession(repayOrder.value.id)
    if (res.data.code !== 0) return
    repayPaymentNo.value = res.data.data.payment_no
    showRepayCodePay.value = true
    showRepay.value = false
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    repayPreparing.value = false
  }
}

/**
 * 现金路径：确认后调 pay-cash（后端关遗留待支付单后收现金，订单自动完结）
 */
function onRepayByCash() {
  if (!repayOrder.value) return
  const order = repayOrder.value
  dialog.warning({
    title: '现金收款',
    content: `确认已收现金 ￥${formatAmount(order.pay_amount)}（订单 ${order.order_no}）？（回车确认）`,
    positiveText: '确认收款',
    negativeText: '取消',
    onPositiveClick: async () => {
      const res = await payPosOrderCash(order.id)
      if (res.data.code === 0) {
        message.success(`订单 ${order.order_no} 现金收款成功`)
        showRepay.value = false
        showDetail.value = false
        void loadOrders()
      }
    },
  })
  // 回车确认：弹窗渲染后聚焦确认按钮
  focusDialogConfirm()
}

/**
 * 待支付单付款码收款成功：刷新列表
 */
function onRepayPaid() {
  showRepayCodePay.value = false
  message.success('收款成功')
  void loadOrders()
}

/**
 * 收银员放弃扫码：订单保留待支付
 */
function onRepayAbandoned() {
  message.info(`订单 ${repayOrder.value?.order_no ?? ''} 已保留待支付，可稍后继续收款或取消`)
}

// --- 引用到收银台 ---

/**
 * 把订单商品引用到收银台：拉详情取商品快照写入 posStore 待引用清单，
 * 通知父组件切回商品下单 Tab（OrderTab 挂载时消费，追加到当前小票）
 * @param row 订单行（列表入口）
 */
async function importToCashier(row: Order) {
  try {
    const res = await getOrderDetail(row.id)
    const d = res.data.data
    const items = (d.items || []).map((it) => ({
      sku_id: it.sku_id,
      quantity: it.quantity,
      product_name: it.sku_name ? `${it.product_name}（${it.sku_name}）` : it.product_name,
      spec_name: it.sku_name || '',
      // 改价单取档案价快照，改价不带入（"再来一单"按当前档案价成交）
      price: it.price_modified ? (it.original_price ?? it.price) : it.price,
    }))
    if (!items.length) {
      message.warning('该订单没有商品明细（直接收款单无需引用）')
      return
    }
    posStore.setPendingImport(items)
    message.success(`已引用订单 ${d.order_no} 的 ${items.length} 项商品`)
    emit('import-to-cashier')
  } catch {
    // 错误提示由请求拦截器统一弹出
  }
}

/**
 * 打开详情弹窗并加载完整详情
 * @param row 列表行
 */
async function openDetail(row: Order) {
  showDetail.value = true
  detailLoading.value = true
  detail.value = null
  try {
    const res = await getOrderDetail(row.id)
    detail.value = res.data.data
  } finally {
    detailLoading.value = false
  }
}

/**
 * 自提核销（详情弹窗内完成）
 */
async function onVerify() {
  if (!detail.value) return
  verifying.value = true
  try {
    await verifyPickup(detail.value.id, detail.value.pickup_code || '')
    message.success(`订单 ${detail.value.order_no} 核销成功`)
    showDetail.value = false
    void loadOrders()
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    verifying.value = false
  }
}

/**
 * 取消订单（必填原因）
 */
async function onCancel() {
  if (!detail.value) return false
  const reason = cancelReason.value.trim()
  if (!reason) {
    message.warning('请填写取消原因')
    return false
  }
  cancelling.value = true
  try {
    await cancelOrder(detail.value.id, reason)
    message.success('订单已取消')
    showCancelModal.value = false
    cancelReason.value = ''
    showDetail.value = false
    void loadOrders()
    return true
  } catch {
    // 错误提示由请求拦截器统一弹出
    return false
  } finally {
    cancelling.value = false
  }
}

/**
 * 代客退款成功后的统一刷新（RefundModal 内完成发起，关闭详情弹窗回列表刷新）
 */
function onRefundSuccess() {
  showDetail.value = false
  void loadOrders()
}

// --- 会员订单弹窗（列表/详情中的会员点击打开） ---
const showUserOrders = ref(false)
const userOrdersUser = ref<{ id: number; nickname?: string } | null>(null)

// --- 订单详情弹窗（会员订单弹窗内订单号点击打开） ---
const showOrderDetail = ref(false)
const orderDetailId = ref<number | null>(null)

/**
 * 打开会员订单弹窗（轻量用户对象；订单详情弹窗 view-user 事件也走这里）
 * @param user 会员 ID 与昵称
 */
function openUserOrdersOf(user: { id: number; nickname?: string }) {
  userOrdersUser.value = { id: user.id, nickname: user.nickname }
  reopenModal(showUserOrders)
}

/**
 * 打开订单详情弹窗（含会员订单弹窗 open-order 事件转发；已开时重挂载置顶并重载）
 * @param id 订单 ID
 */
function openOrderDetail(id: number) {
  orderDetailId.value = id
  reopenModal(showOrderDetail)
}

/**
 * 打开会员订单弹窗
 * @param row 订单行（取归属会员；散客不响应）
 */
function openUserOrders(row: Pick<Order, 'user_id' | 'user_name'>) {
  if (row.user_id <= 0) return
  openUserOrdersOf({ id: row.user_id, nickname: row.user_name })
}

// --- 散客单补绑会员（详情弹窗内；仅已收款散客单可绑，绑后不可换绑） ---

/** 补绑会员选择弹窗 */
const bindPickerVisible = ref(false)
/** 补绑请求进行中（防重复提交） */
const bindingUser = ref(false)
/** 待补绑的订单 ID */
const bindTargetId = ref(0)

/**
 * 散客单是否可补绑会员：仍是散客（user_id=0）、已收款（状态 2-5，待支付走继续收款时选会员）、
 * 有订单处理权限（接口要求 order:update）
 * @param row 订单（需含 user_id/status）
 */
function canBindUser(row: Pick<Order, 'user_id' | 'status'>): boolean {
  if (row.user_id > 0) return false
  if (!authStore.isSuperAdmin && !authStore.hasPermission('order:update')) return false
  return row.status >= ORDER_STATUS.PAID && row.status <= ORDER_STATUS.COMPLETED
}

/**
 * 打开补绑会员弹窗（详情弹窗内散客单）
 * @param row 订单（需含 id）
 */
function openBindUser(row: Pick<Order, 'id'>) {
  bindTargetId.value = row.id
  bindPickerVisible.value = true
}

/**
 * 补绑会员：调绑定接口，成功后刷新详情与列表（会员名按 user_id 重新填充）
 * @param u 选中的会员
 */
async function onBindUserSelect(u: User) {
  if (bindingUser.value || bindTargetId.value <= 0) return
  bindingUser.value = true
  try {
    const res = await bindOrderUser(bindTargetId.value, u.id)
    if (res.data.code !== 0) return
    bindPickerVisible.value = false
    message.success(`订单已绑定到会员 ${u.nickname || u.phone || `用户${u.id}`}，不可再更换`)
    await openDetail({ id: bindTargetId.value } as Order)
    void loadOrders()
  } finally {
    bindingUser.value = false
  }
}

/** 表格列定义（行点击打开详情；状态列彩色标签；待支付可继续收款；有商品的单可引用到收银台） */
const columns = computed<DataTableColumns<Order>>(() => [
  { title: '订单号', key: 'order_no', width: 190, ellipsis: { tooltip: true } },
  {
    title: '会员',
    key: 'user_name',
    width: 110,
    ellipsis: { tooltip: true },
    // 关联会员（user_id>0）可点击查看其历史订单/常买商品；散客展示原文
    render: (row) =>
      row.user_id > 0
        ? h(
            NButton,
            { size: 'tiny', type: 'primary', quaternary: true, onClick: () => openUserOrders(row) },
            { default: () => row.user_name || `用户${row.user_id}` },
          )
        : row.user_name || '散客',
  },
  { title: '实付', key: 'pay_amount', width: 90, render: (row) => `￥${formatAmount(row.pay_amount)}` },
  {
    title: '支付方式',
    key: 'pay_type',
    width: 90,
    // 仅图标：悬浮/触屏点按显示文字（2026-10-25 修订）
    render: (row) => h(PayTypeLabel, { payType: row.pay_type, text: row.pay_type_text, iconOnly: true }),
  },
  {
    // 支付/订单状态彩色标签：与详情弹窗同口径（待支付黄/已支付蓝/完成绿/退款红）
    title: '状态',
    key: 'status',
    width: 90,
    render: (row) =>
      h(NTag, { size: 'small', type: orderStatusTagType(row.status), bordered: false }, { default: () => row.status_text }),
  },
  {
    title: '来源',
    key: 'source',
    width: 80,
    render: (row) => orderSourceText(row.source ?? 1),
  },
  { title: '取货码', key: 'pickup_code', width: 90, render: (row) => row.pickup_code || '-' },
  { title: '下单时间', key: 'created_at', width: 165, ellipsis: { tooltip: true }, render: (row) => formatDateTime(row.created_at) },
  {
    title: '操作',
    key: 'actions',
    width: 120,
    render: (row) => {
      const btns = [
        h(
          NButton,
          { size: 'tiny', type: 'primary', quaternary: true, onClick: () => openDetail(row) },
          { default: () => '详情' },
        ),
      ]
      // 引用按商品快照重建小票：直接收款单（item_count=0）无商品可引用，不渲染入口
      if ((row.item_count ?? 0) > 0) {
        btns.push(
          h(
            NButton,
            { size: 'tiny', type: 'info', quaternary: true, onClick: () => void importToCashier(row) },
            { default: () => '引用' },
          ),
        )
      }
      // 待支付收银台单：列表直达继续收款
      if (row.status === ORDER_STATUS.PENDING_PAY && row.source === 2) {
        btns.push(
          h(
            NButton,
            { size: 'tiny', type: 'warning', quaternary: true, onClick: () => openRepay(row) },
            { default: () => '收款' },
          ),
        )
      }
      return btns
    },
  },
])

onMounted(loadOrders)

onBeforeUnmount(() => {
  if (dialogFocusTimer !== undefined) {
    window.clearTimeout(dialogFocusTimer)
    dialogFocusTimer = undefined
  }
  if (productSearchTimer) {
    clearTimeout(productSearchTimer)
    productSearchTimer = null
  }
})
</script>

<style lang="scss" scoped>
.orders-tab {
  height: 100%;
  display: flex;
  flex-direction: column;
  padding: 14px;
  box-sizing: border-box;
  gap: 10px;
}
.toolbar {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  flex-shrink: 0;
  .order-no-input {
    width: 220px;
  }
  .status-select,
  .source-select {
    width: 150px;
  }
}
// 已设筛选条件回显（折叠态）
.filter-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  flex-shrink: 0;
}
// 折叠更多筛选区：浅色底与主条件行区分，行内自动换行
.more-filters {
  flex-shrink: 0;
  display: flex;
  flex-wrap: wrap;
  gap: 10px 18px;
  padding: 10px 12px;
  background: #fafafa;
  border-radius: 8px;
  .filter-item {
    display: flex;
    align-items: center;
    gap: 8px;
    .f-label {
      font-size: 12px;
      color: #999;
      flex-shrink: 0;
    }
    .w-input {
      width: 170px;
    }
    .w-shift {
      width: 110px;
    }
    .date-picker {
      width: 230px;
    }
    &.actions {
      margin-left: auto;
    }
  }
  .f-inline {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .quick-chips {
    display: flex;
    gap: 4px;
  }
}
// 商品选择器（筛选用）
.product-list {
  max-height: 320px;
  overflow-y: auto;
  border: 1px solid #efeff5;
  border-radius: 6px;
}
.product-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  font-size: 13px;
  cursor: pointer;
  border-bottom: 1px solid #f3f3f7;
  &:last-child {
    border-bottom: none;
  }
  &:hover {
    background: #f7f9fa;
  }
  .meta {
    color: #999;
    flex-shrink: 0;
  }
}
.table {
  flex: 1;
  min-height: 0;
}
.pagination {
  flex-shrink: 0;
  display: flex;
  justify-content: flex-end;
}
.detail-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px 20px;
  .span2 {
    grid-column: span 2;
  }
}
.detail-item {
  display: flex;
  gap: 8px;
  font-size: 13px;
  align-items: center;
  .label {
    color: #999;
    flex-shrink: 0;
    width: 62px;
  }
}
.pickup-code {
  font-size: 16px;
  font-weight: 700;
  letter-spacing: 2px;
  color: #07c160;
}
.section-title {
  font-size: 13px;
  font-weight: 600;
  color: #666;
  margin: 16px 0 8px;
}
.items-table {
  border: 1px solid #efeff5;
  border-radius: 6px;
}
.items-empty {
  padding: 12px 0;
}
.item-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 12px;
  font-size: 13px;
  border-bottom: 1px solid #f3f3f7;
  &:last-child {
    border-bottom: none;
  }
  .item-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    .mod-tag {
      margin-left: 4px;
    }
  }
  .item-price {
    font-variant-numeric: tabular-nums;
    .origin {
      margin-left: 3px;
      text-decoration: line-through;
      color: #ccc;
    }
  }
  .item-qty {
    color: #999;
    width: 44px;
    text-align: right;
  }
  .item-subtotal {
    width: 84px;
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
}
.amount-block {
  margin-top: 12px;
  padding: 10px 14px;
  background: #fafafa;
  border-radius: 6px;
}
.amount-row {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  padding: 2px 0;
  color: #666;
  &.strong {
    color: #d03050;
    font-weight: 700;
    font-size: 14px;
    margin-top: 4px;
  }
}
.detail-actions {
  margin-top: 16px;
}
.repay-wrap {
  display: flex;
  flex-direction: column;
  gap: 14px;
  text-align: center;
}
.repay-amount {
  font-size: 32px;
  font-weight: 700;
  color: #d03050;
}
.repay-order {
  font-size: 12px;
}
.muted {
  color: #999;
}
.refund-list {
  border: 1px solid #efeff5;
  border-radius: 6px;
}
.refund-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  font-size: 12px;
  border-bottom: 1px solid #f3f3f7;
  &:last-child {
    border-bottom: none;
  }
  .refund-no {
    color: #666;
    font-family: monospace;
  }
  .refund-amount {
    color: #d03050;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .refund-time {
    margin-left: auto;
    color: #999;
  }
}
</style>
