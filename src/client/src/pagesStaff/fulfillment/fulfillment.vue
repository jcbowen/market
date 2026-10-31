<template>
  <view class="fulfillment-page">
    <!-- 身份提示 -->
    <view v-if="statusLoaded && !isFulfiller" class="empty-wrap">
      <u-empty text="您不是本社区履约人员" mode="permission" />
    </view>

    <template v-else>
      <!-- 模块切换：送货 / 自提核销 -->
      <u-tabs :list="tabs" :current="activeTabIndex" @change="onTabChange" />

      <!-- 状态组切换：进行中 / 已完成 -->
      <view class="group-bar">
        <view
          v-for="g in groupOptions"
          :key="g.value"
          :class="['group-item', { active: statusGroup === g.value }]"
          @click="onGroupChange(g.value)"
        >
          <text>{{ g.label }}</text>
        </view>
      </view>

      <scroll-view class="order-list" scroll-y @scrolltolower="loadMore">
        <view v-for="order in orders" :key="order.id" class="order-card">
          <view class="order-header">
            <text class="order-no">{{ order.order_no }}</text>
            <view class="header-right">
              <!-- 部分退款标识：状态文本看不出售后痕迹，提示交付时留意剩余件数（2026-10-31） -->
              <text v-if="hasPartialRefund(order)" class="refund-tag">部分退款</text>
              <text class="order-status">{{ order.status_text }}</text>
            </view>
          </view>

          <!-- 商品摘要（最多显示 2 行 + 数量；已退件数随行展示） -->
          <view class="goods-line" v-for="item in order.items.slice(0, 2)" :key="`${order.id}-${item.sku_id}`">
            <text class="goods-name">{{ item.product_name }}{{ item.spec_name ? `（${item.spec_name}）` : '' }}</text>
            <text class="goods-qty">
              x{{ item.quantity }}
              <text v-if="(item.refunded_quantity ?? 0) > 0" class="goods-refunded">已退{{ item.refunded_quantity }}</text>
            </text>
          </view>
          <view v-if="order.items.length > 2" class="goods-more">
            <text>…共 {{ order.items.length }} 种商品</text>
          </view>

          <!-- 收货信息 -->
          <view class="contact-block">
            <text class="contact-name">{{ order.contact_name || '未填写' }} {{ order.contact_phone || '' }}</text>
            <text v-if="order.delivery_type === 2" class="contact-addr">{{ order.contact_address || '未填写地址' }}</text>
          </view>

          <view class="order-footer">
            <view class="pay-wrap">
              <text class="pay-amount">实付 {{ formatPrice(order.pay_amount) }}</text>
              <!-- 部分退款痕迹：原实付保留，附加已退金额（订单级 money 口径含运费） -->
              <text v-if="(order.refunded_amount ?? 0) > 0" class="refunded-amount">
                已退 {{ formatPrice(order.refunded_amount ?? 0) }}，应交付剩余 {{ remainingQty(order) }} 件
              </text>
            </view>
            <!-- 送货：开始配送 / 送达拍照 -->
            <u-button
              v-if="isDeliveryTab && (order.status === 2 || order.status === 3)"
              type="primary"
              size="small"
              text="开始配送"
              @click="onStartDelivery(order)"
            />
            <u-button
              v-else-if="isDeliveryTab && order.status === 4"
              type="success"
              size="small"
              text="送达拍照"
              @click="onOpenProof(order)"
            />
            <!-- 自提：核销 -->
            <u-button
              v-else-if="!isDeliveryTab && (order.status === 2 || order.status === 3)"
              type="success"
              size="small"
              text="核销取货码"
              @click="onOpenVerify(order)"
            />
          </view>
        </view>
        <u-empty v-if="statusLoaded && orders.length === 0" :text="emptyText" mode="list" />
        <u-loadmore :status="loadStatus" />
        <view style="height: 40rpx" />
      </scroll-view>
    </template>

    <!-- 送达拍照弹层（决策点②：强制签收照片） -->
    <u-popup :show="proofVisible" mode="bottom" round="16" :close-on-click-overlay="false" @close="proofVisible = false">
      <view class="proof-panel">
        <view class="panel-title"><text>送达签收拍照</text></view>
        <text class="panel-tip">订单 {{ proofOrder?.order_no }}：请拍摄放置在收货位置的包裹/商品后提交（必填）</text>
        <view class="proof-images">
          <view v-for="(img, idx) in proofImages" :key="idx" class="proof-img-wrap">
            <image :src="img" mode="aspectFill" class="proof-img" />
            <view class="proof-del" @click="removeProof(idx)">
              <u-icon name="close" size="10" color="#fff" />
            </view>
          </view>
          <view v-if="proofImages.length < 3" class="proof-add" @click="chooseProof">
            <u-icon name="camera" size="26" color="#999" />
            <text>拍照/选图</text>
          </view>
        </view>
        <view class="panel-actions">
          <u-button text="取消" plain @click="proofVisible = false" />
          <u-button
            type="success"
            :text="uploading ? '上传中…' : '确认送达'"
            :disabled="proofImages.length === 0 || uploading"
            :loading="uploading"
            @click="onSubmitProof"
          />
        </view>
      </view>
    </u-popup>

    <!-- 核销取货码弹层 -->
    <u-popup :show="verifyVisible" mode="bottom" round="16" :close-on-click-overlay="false" @close="verifyVisible = false">
      <view class="proof-panel">
        <view class="panel-title"><text>核销自提订单</text></view>
        <text class="panel-tip">请输入顾客出示的取货码（订单详情页展示的 8 位数字码）</text>
        <!-- 部分退款单核销提示：显著提醒剩余应交付件数，不强制阻断（口径 2026-10-31） -->
        <text v-if="verifyOrder && hasPartialRefund(verifyOrder)" class="panel-refund-tip">
          本单已部分退款：已退 {{ formatPrice(verifyOrder.refunded_amount ?? 0) }}（{{ refundedQty(verifyOrder) }} 件），请仅核销剩余 {{ remainingQty(verifyOrder) }} 件商品
        </text>
        <view class="verify-order-no"><text>订单 {{ verifyOrder?.order_no }}</text></view>
        <view class="verify-input-wrap">
          <input
            v-model="verifyCode"
            class="verify-input"
            type="number"
            maxlength="8"
            placeholder="8 位取货码"
            :disabled="verifying"
          />
        </view>
        <view class="panel-actions">
          <u-button text="取消" plain @click="verifyVisible = false" />
          <u-button type="success" text="确认核销" :loading="verifying" @click="onSubmitVerify" />
        </view>
      </view>
    </u-popup>
  </view>
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'FulfillmentWorkbench' })
import { computed, ref } from 'vue'
import { onShow } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { formatPrice } from '@/utils/format'
import { goLogin } from '@/utils/navigate'
import {
  getFulfillmentStatus,
  getFulfillmentOrders,
  startDelivery,
  completeDelivery,
  verifyPickupByStaff,
  uploadImage,
} from '@/api/fulfillment'
import type { FulfillmentOrder } from '@/api/fulfillment'

const userStore = useUserStore()

// tab 的 value 就是后端 order_type，它直接比对 orders.delivery_type（1 自提 2 配送 3 到店），
// 与展示顺序无关——本项曾因「按展示顺序猜值」把两个 tab 的数据整体对调（送货 tab 查出的是自提单），
// 2026-10-28 纠正并在此锁定口径：送货=2、自提核销=1
const tabs = [
  { name: '送货', value: 2 },
  { name: '自提核销', value: 1 },
]
// u-tabs 的 :current 是「索引」而非 value，两者必须分开持有（混用会让高亮错位一格）
const activeTabIndex = ref(0)
/** 当前 tab 对应的后端 order_type */
const currentOrderType = computed(() => tabs[activeTabIndex.value].value)
/** 是否送货 tab（决定按钮组与空态文案） */
const isDeliveryTab = computed(() => currentOrderType.value === 2)
/** 状态组：0=进行中（待处理+配送中/待核销） 5=已完成/已核销 */
const groupOptions = [
  { label: '进行中', value: 0 },
  { label: '已完成', value: 5 },
]
const statusGroup = ref(0)

const statusLoaded = ref(false)
const isFulfiller = ref(false)

const orders = ref<FulfillmentOrder[]>([])
const page = ref(1)
const pageSize = 10
const loadStatus = ref<'loadmore' | 'loading' | 'nomore'>('loadmore')

const emptyText = computed(() =>
  isDeliveryTab.value
    ? statusGroup.value === 0
      ? '暂无待配送订单'
      : '暂无已完成订单'
    : statusGroup.value === 0
      ? '暂无待核销自提订单'
      : '暂无已核销订单',
)

onShow(() => {
  if (!userStore.isLogin) {
    goLogin()
    return
  }
  checkStatus()
})

/**
 * 查询履约人员身份：非履约人员显示空态，履约人员加载工作台
 */
async function checkStatus() {
  try {
    const res = await getFulfillmentStatus()
    isFulfiller.value = !!res.data?.is_fulfiller
  } catch {
    isFulfiller.value = false
  } finally {
    statusLoaded.value = true
  }
  if (isFulfiller.value) {
    refreshList()
  }
}

/**
 * 切换模块 tab（送货/自提核销）
 * 只记索引，order_type 由 currentOrderType 派生，避免「索引当值用」再次漂移
 */
function onTabChange(index: number | { index?: number; name?: string }) {
  // uview-plus tabs change 事件参数在 H5 为 index，App 为对象，统一兼容
  const idx = typeof index === 'number' ? index : index.index ?? 0
  if (idx === activeTabIndex.value) return
  activeTabIndex.value = idx
  refreshList()
}

/**
 * 切换状态组（进行中/已完成）
 */
function onGroupChange(value: number) {
  statusGroup.value = value
  refreshList()
}

/**
 * 刷新列表（回到第一页）
 */
function refreshList() {
  page.value = 1
  orders.value = []
  loadStatus.value = 'loadmore'
  loadOrders()
}

/**
 * 加载工作台订单（滚动触底翻页）
 */
async function loadOrders() {
  if (loadStatus.value === 'loading' || loadStatus.value === 'nomore') return
  loadStatus.value = 'loading'
  try {
    const res = await getFulfillmentOrders({
      order_type: currentOrderType.value,
      status_group: statusGroup.value,
      page: page.value,
      page_size: pageSize,
    })
    const list = res.data?.list || []
    if (page.value === 1) {
      orders.value = list
    } else {
      orders.value.push(...list)
    }
    if (list.length < pageSize) {
      loadStatus.value = 'nomore'
    } else {
      loadStatus.value = 'loadmore'
      page.value += 1
    }
  } catch {
    loadStatus.value = 'loadmore'
  }
}

/**
 * 滚动触底加载下一页
 */
function loadMore() {
  if (loadStatus.value === 'loadmore') loadOrders()
}

/**
 * 订单行级已退件数合计（部分售后提示用）
 * @param order 履约订单
 * @returns 已退件数
 */
function refundedQty(order: FulfillmentOrder): number {
  return order.items.reduce((s, it) => s + (it.refunded_quantity ?? 0), 0)
}

/**
 * 剩余应交付件数（总件数 - 已退件数，下限 0）
 * @param order 履约订单
 * @returns 剩余件数
 */
function remainingQty(order: FulfillmentOrder): number {
  return Math.max(0, order.items.reduce((s, it) => s + it.quantity, 0) - refundedQty(order))
}

/**
 * 是否存在部分退款痕迹（金额或任一行数量）
 * @param order 履约订单
 * @returns 是否显示「部分退款」标识
 */
function hasPartialRefund(order: FulfillmentOrder): boolean {
  return (order.refunded_amount ?? 0) > 0 || refundedQty(order) > 0
}

/**
 * 开始配送（status 2/3 → 4）；部分退款单在确认文案中提示剩余应交付件数，不强制阻断
 */
function onStartDelivery(order: FulfillmentOrder) {
  const refunded = refundedQty(order)
  const refundTip = refunded > 0 ? `本单已部分退款（已退 ${refunded} 件），请仅配送剩余 ${remainingQty(order)} 件商品。\n` : ''
  uni.showModal({
    title: '开始配送',
    content: `${refundTip}确定开始为订单 ${order.order_no} 配送？`,
    success: async (res) => {
      if (!res.confirm) return
      try {
        await startDelivery(order.id)
        uni.showToast({ title: '已开始配送', icon: 'success' })
        refreshList()
      } catch {
        // 错误提示由 request 统一弹出
      }
    },
  })
}

// --- 送达拍照（决策点②） ---
const proofVisible = ref(false)
const proofOrder = ref<FulfillmentOrder | null>(null)
const proofImages = ref<string[]>([])
const uploading = ref(false)

/**
 * 打开送达拍照弹层
 */
function onOpenProof(order: FulfillmentOrder) {
  proofOrder.value = order
  proofImages.value = []
  proofVisible.value = true
}

/**
 * 拍照/从相册选择签收照片（最多 3 张）
 */
function chooseProof() {
  uni.chooseImage({
    count: 3 - proofImages.value.length,
    sizeType: ['compressed'],
    success: (res) => {
      proofImages.value.push(...res.tempFilePaths)
    },
  })
}

/**
 * 移除待上传的签收照片
 */
function removeProof(idx: number) {
  proofImages.value.splice(idx, 1)
}

/**
 * 提交送达：逐张上传照片后调用送达接口
 */
async function onSubmitProof() {
  const order = proofOrder.value
  if (!order || proofImages.value.length === 0) return
  uploading.value = true
  try {
    const urls: string[] = []
    for (const filePath of proofImages.value) {
      const res = await uploadImage(filePath)
      if (res.data?.url) urls.push(res.data.url)
    }
    if (urls.length === 0) {
      uni.showToast({ title: '照片上传失败，请重试', icon: 'none' })
      return
    }
    await completeDelivery(order.id, urls)
    uni.showToast({ title: '已送达完成', icon: 'success' })
    proofVisible.value = false
    refreshList()
  } catch {
    // 错误提示由 request 统一弹出
  } finally {
    uploading.value = false
  }
}

// --- 自提核销 ---
const verifyVisible = ref(false)
const verifyOrder = ref<FulfillmentOrder | null>(null)
const verifyCode = ref('')
const verifying = ref(false)

/**
 * 打开核销弹层
 */
function onOpenVerify(order: FulfillmentOrder) {
  verifyOrder.value = order
  verifyCode.value = ''
  verifyVisible.value = true
}

/**
 * 提交核销：输码比对后完成订单
 */
async function onSubmitVerify() {
  const order = verifyOrder.value
  if (!order) return
  if (!/^\d{8}$/.test(verifyCode.value)) {
    uni.showToast({ title: '请输入 8 位取货码', icon: 'none' })
    return
  }
  verifying.value = true
  try {
    await verifyPickupByStaff(order.id, verifyCode.value)
    uni.showToast({ title: '核销成功', icon: 'success' })
    verifyVisible.value = false
    refreshList()
  } catch {
    // 错误提示由 request 统一弹出
  } finally {
    verifying.value = false
  }
}
</script>

<style scoped>
.fulfillment-page {
  display: flex;
  flex-direction: column;
  /* 100vh 含 H5 原生导航栏高度，直接用会把底部裁掉一截（需滚动整个页面才能看全），
     用 --window-top 扣除导航栏：H5 为 44px，小程序端为 0 */
  height: calc(100vh - var(--window-top, 0px));
  background-color: #f5f5f5;
}

/* uview-plus 的 u-popup 根元素自带 flex: 1（组件样式 .u-popup { flex: 1 }），
   在本页 flex column 布局下，关闭态弹窗（内联 height:0 被当作 flex-basis）
   仍会参与剩余高度瓜分，把订单滚动区压缩到约 1/3 屏；
   覆盖为 none 让关闭态回归 0 高，弹出的面板本身是 fixed 定位不受影响 */
.fulfillment-page :deep(.u-popup) {
  flex: none;
}

.empty-wrap {
  display: flex;
  align-items: center;
  justify-content: center;
  padding-top: 240rpx;
}

.group-bar {
  display: flex;
  gap: 16rpx;
  padding: 20rpx 30rpx;
  background-color: #fff;
}

.group-item {
  padding: 10rpx 32rpx;
  font-size: 26rpx;
  color: #666;
  background-color: #f2f3f5;
  border-radius: 28rpx;
}

.group-item.active {
  color: #07c160;
  background-color: rgba(7, 193, 96, 0.1);
  font-weight: 600;
}

.order-list {
  flex: 1;
  min-height: 0;
  padding: 20rpx 30rpx 0;
  box-sizing: border-box;
}

.order-card {
  margin-bottom: 24rpx;
  padding: 24rpx;
  background-color: #fff;
  border-radius: 16rpx;
}

.order-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16rpx;
}

.header-right {
  display: flex;
  align-items: center;
  gap: 12rpx;
}

/* 部分退款标识：红色小标签，与状态文本并排 */
.refund-tag {
  font-size: 22rpx;
  color: #fa3534;
  background-color: rgba(250, 53, 52, 0.08);
  padding: 2rpx 12rpx;
  border-radius: 8rpx;
}

.order-no {
  font-size: 26rpx;
  color: #333;
  font-weight: 600;
}

.order-status {
  font-size: 26rpx;
  color: #07c160;
}

.goods-line {
  display: flex;
  justify-content: space-between;
  margin-bottom: 8rpx;
}

.goods-name {
  flex: 1;
  font-size: 26rpx;
  color: #333;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.goods-qty {
  font-size: 26rpx;
  color: #999;
}

/* 行级已退件数：随数量展示，红色可辨 */
.goods-refunded {
  color: #fa3534;
  margin-left: 6rpx;
}

.goods-more {
  font-size: 24rpx;
  color: #999;
  margin-bottom: 8rpx;
}

.contact-block {
  padding: 16rpx;
  margin: 12rpx 0;
  font-size: 26rpx;
  color: #666;
  background-color: #f8f8f8;
  border-radius: 12rpx;
  display: flex;
  flex-direction: column;
  gap: 6rpx;
}

.contact-name {
  font-weight: 500;
  color: #333;
}

.order-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 8rpx;
}

.pay-wrap {
  display: flex;
  flex-direction: column;
  gap: 4rpx;
}

/* 已退金额/剩余件数：弱于实付强调级但需醒目 */
.refunded-amount {
  font-size: 24rpx;
  color: #fa3534;
}

.pay-amount {
  font-size: 28rpx;
  color: #fa3534;
  font-weight: 600;
}

/* --- 弹层面板 --- */
.proof-panel {
  padding: 32rpx 32rpx calc(32rpx + env(safe-area-inset-bottom));
}

.panel-title {
  font-size: 32rpx;
  font-weight: 600;
  color: #333;
  margin-bottom: 12rpx;
}

.panel-tip {
  display: block;
  font-size: 26rpx;
  color: #999;
  margin-bottom: 24rpx;
}

/* 核销弹层部分退款提示：警示色，紧随操作提示 */
.panel-refund-tip {
  display: block;
  font-size: 26rpx;
  color: #fa3534;
  margin-bottom: 24rpx;
}

.verify-order-no {
  font-size: 28rpx;
  color: #333;
  margin-bottom: 16rpx;
}

.verify-input-wrap {
  margin-bottom: 24rpx;
}

.verify-input {
  height: 88rpx;
  padding: 0 24rpx;
  font-size: 34rpx;
  letter-spacing: 8rpx;
  text-align: center;
  background-color: #f5f5f5;
  border-radius: 12rpx;
}

.proof-images {
  display: flex;
  flex-wrap: wrap;
  gap: 16rpx;
  margin-bottom: 28rpx;
}

.proof-img-wrap {
  position: relative;
  width: 180rpx;
  height: 180rpx;
}

.proof-img {
  width: 100%;
  height: 100%;
  border-radius: 12rpx;
}

.proof-del {
  position: absolute;
  top: -10rpx;
  right: -10rpx;
  width: 36rpx;
  height: 36rpx;
  display: flex;
  align-items: center;
  justify-content: center;
  background-color: #fa3534;
  border-radius: 50%;
}

.proof-add {
  width: 180rpx;
  height: 180rpx;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8rpx;
  font-size: 22rpx;
  color: #999;
  border: 2rpx dashed #ddd;
  border-radius: 12rpx;
  box-sizing: border-box;
}

.panel-actions {
  display: flex;
  gap: 24rpx;
}
</style>
