<template>
  <view class="confirm-page">
    <!-- 收货地址 -->
    <view class="address-card" @click="goAddressList">
      <view v-if="selectedAddress" class="address-info">
        <view class="address-top">
          <text class="name">{{ selectedAddress.contact_name }}</text>
          <text class="phone">{{ selectedAddress.contact_phone }}</text>
        </view>
        <text class="address-text">
          {{ selectedAddress.province }}{{ selectedAddress.city }}{{ selectedAddress.district }}{{ selectedAddress.detail }}
        </text>
      </view>
      <view v-else class="address-empty">
        <text>请选择收货地址</text>
      </view>
      <u-icon name="arrow-right" size="14" color="#999999" />
    </view>

    <!-- 商品清单 -->
    <view class="goods-card">
      <view
        v-for="item in orderItems"
        :key="`${item.product_id}-${item.sku_id}`"
        class="goods-item"
      >
        <image class="goods-image" :src="item.product_image || '/static/logo.png'" mode="aspectFill" />
        <view class="goods-info">
          <text class="goods-name">{{ item.product_name }}</text>
          <text class="goods-sku">{{ item.spec_name }}</text>
          <view class="goods-bottom">
            <text class="goods-price">{{ formatPrice(item.price) }}</text>
            <text class="goods-quantity">x{{ item.quantity }}</text>
          </view>
        </view>
      </view>
    </view>

    <!-- 优惠券、支付方式与备注 -->
    <view class="extra-card">
      <view class="extra-item">
        <text>满减优惠</text>
        <text class="extra-value" :class="{ 'discount-value': fullReductionDiscount > 0 }">{{ fullReductionText }}</text>
      </view>
      <view class="extra-item" @click="onSelectCoupon">
        <text>优惠券</text>
        <text class="extra-value">{{ couponText }}</text>
        <u-icon name="arrow-right" size="14" color="#999999" />
      </view>
      <view class="extra-item" @click="onSelectChannel">
        <text>支付方式</text>
        <text class="extra-value">{{ selectedChannelName }}</text>
        <u-icon name="arrow-right" size="14" color="#999999" />
      </view>
      <view class="extra-item">
        <text>备注</text>
        <u-input
          v-model="remark"
          placeholder="请输入备注"
          border="none"
          input-align="right"
        />
      </view>
    </view>

    <!-- 底部结算 -->
    <view class="footer-bar">
      <view class="total-wrap">
        <text class="total-label">合计:</text>
        <text class="total-price">{{ formatPrice(payAmount) }}</text>
        <text v-if="couponDiscount > 0" class="discount-tag">已优惠 {{ formatPrice(couponDiscount) }}</text>
      </view>
      <view class="submit-btn">
        <u-button type="success" text="提交订单" :loading="submitting" @click="onSubmit" />
      </view>
    </view>
  </view>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { onLoad, onShow } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { getAddressList } from '@/api/user'
import { getCart } from '@/api/cart'
import { getFullReductions, getProductDetail, getSkuById } from '@/api/product'
import { createOrder } from '@/api/order'
import { getPaymentChannels } from '@/api/payment'
import { getUserCoupons } from '@/api/marketing'
import { formatPrice } from '@/utils/format'
import { notifyPayOutcome, payOrder } from '@/utils/pay'
import { goAddressList, goOrderDetail, goLogin } from '@/utils/navigate'
import type { Address, FullReduction, PaymentChannel, UserCoupon } from '@/api/types'

/** 结算页商品展示结构（购物车/直购中间态，无订单侧 id/total_price 字段） */
interface SettleItem {
  product_id: number
  product_name: string
  sku_id: number
  spec_name: string
  price: number
  quantity: number
  product_image: string
}

const userStore = useUserStore()
const addresses = ref<Address[]>([])
const orderItems = ref<SettleItem[]>([])
const remark = ref('')
const submitting = ref(false)
const cartItemIds = ref<number[]>([])
const productId = ref(0)
const skuId = ref(0)
const quantity = ref(1)

const selectedAddress = computed(() => {
  return addresses.value.find((item) => item.is_default) || addresses.value[0]
})

const totalAmount = computed(() => {
  return orderItems.value.reduce((sum, item) => sum + item.price * item.quantity, 0)
})

const channels = ref<PaymentChannel[]>([])
const selectedChannel = ref<PaymentChannel | null>(null)
const selectedCoupon = ref<UserCoupon | null>(null)
const fullReductions = ref<FullReduction[]>([])

const couponText = computed(() => {
  if (!selectedCoupon.value) return '暂不使用'
  return `满${formatPrice(selectedCoupon.value.threshold)}减${formatPrice(selectedCoupon.value.value)}`
})

/**
 * 计算当前订单可享的满减优惠
 * 规则与后端一致：在生效且满足门槛的满减活动中取减免金额最大的一项，不超过商品总额
 */
const fullReductionDiscount = computed(() => {
  const communityId = userStore.communityInfo?.id
  if (!communityId || !fullReductions.value.length) return 0
  const now = new Date()
  let best = 0
  for (const fr of fullReductions.value) {
    if (fr.status !== 1) continue
    const start = new Date(fr.start_time.replace(' ', 'T'))
    const end = new Date(fr.end_time.replace(' ', 'T'))
    if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) continue
    if (now < start || now > end) continue
    if (totalAmount.value >= fr.threshold && fr.reduce_amount > best) {
      best = fr.reduce_amount
    }
  }
  return Math.min(best, totalAmount.value)
})

const fullReductionText = computed(() => {
  if (fullReductionDiscount.value <= 0) return '暂无可用满减'
  return `- ${formatPrice(fullReductionDiscount.value)}`
})

const couponDiscount = computed(() => {
  if (!selectedCoupon.value) return 0
  if (totalAmount.value < selectedCoupon.value.threshold) return 0
  return Math.min(totalAmount.value, selectedCoupon.value.value)
})

const payAmount = computed(() => {
  return Math.max(0, totalAmount.value - fullReductionDiscount.value - couponDiscount.value)
})

const selectedChannelName = computed(() => {
  return selectedChannel.value?.name || '请选择支付方式'
})

onLoad((query) => {
  productId.value = Number(query?.productId) || 0
  skuId.value = Number(query?.skuId) || 0
  quantity.value = Number(query?.quantity) || 1
  const ids = query?.cartItemIds
  if (ids) {
    cartItemIds.value = String(ids).split(',').map(Number)
  }
})

onShow(() => {
  if (!userStore.isLogin) {
    goLogin()
    return
  }
  loadAddresses()
  loadPaymentChannels()
  loadFullReductions()
  loadItems()
})

/**
 * 加载收货地址列表
 */
async function loadAddresses() {
  try {
    const res = await getAddressList()
    addresses.value = res.data || []
  } catch {
    addresses.value = []
  }
}

/**
 * 加载订单商品
 * 优先按 URL 参数加载；无参数时默认加载购物车中已选中的商品（从购物车/本地购物车结算场景）
 */
async function loadItems() {
  if (cartItemIds.value.length) {
    await loadCartItems()
  } else if (skuId.value) {
    await loadDirectItems()
  } else {
    await loadSelectedCartItems()
  }
}

/**
 * 从购物车加载所有选中商品
 * 用于未携带 cartItemIds 的结算入口（如本地购物车登录同步后结算）
 */
async function loadSelectedCartItems() {
  try {
    const res = await getCart(userStore.communityInfo?.id)
    const cartItems = res.data?.items || []
    const selected = cartItems.filter((item) => item.selected)
    orderItems.value = selected.map((item) => ({
      product_id: item.product_id,
      product_name: item.product_name,
      sku_id: item.sku_id,
      spec_name: item.spec_name,
      price: item.price,
      quantity: item.quantity,
      product_image: item.product_image,
    }))
  } catch {
    orderItems.value = []
  }
}

/**
 * 从购物车加载选中商品
 */
async function loadCartItems() {
  try {
    const res = await getCart(userStore.communityInfo?.id)
    const cartItems = res.data?.items || []
    const selected = cartItems.filter((item) => cartItemIds.value.includes(item.id))
    orderItems.value = selected.map((item) => ({
      product_id: item.product_id,
      product_name: item.product_name,
      sku_id: item.sku_id,
      spec_name: item.spec_name,
      price: item.price,
      quantity: item.quantity,
      product_image: item.product_image,
    }))
  } catch {
    orderItems.value = []
  }
}

/**
 * 从商品详情加载直接购买商品
 * 若 URL 未携带 productId 但携带了 skuId，则先查询 SKU 获取 productId
 */
async function loadDirectItems() {
  try {
    let pid = productId.value
    if (!pid && skuId.value) {
      const skuRes = await getSkuById(skuId.value)
      if (skuRes.data?.product_id) {
        pid = skuRes.data.product_id
      }
    }
    if (!pid) {
      orderItems.value = []
      return
    }
    const res = await getProductDetail(pid)
    if (res.data) {
      const sku = res.data.skus.find((s) => s.id === skuId.value) || res.data.skus[0]
      if (sku) {
        orderItems.value = [{
          product_id: res.data.id,
          product_name: res.data.name,
          sku_id: sku.id,
          spec_name: sku.name,
          price: sku.price,
          quantity: quantity.value,
          product_image: sku.image || res.data.main_image,
        }]
      }
    }
  } catch {
    orderItems.value = []
  }
}

/**
 * 加载可用支付渠道
 */
async function loadPaymentChannels() {
  try {
    const res = await getPaymentChannels()
    const list = res.data?.channels || []
    channels.value = list
    if (list.length && !selectedChannel.value) {
      selectedChannel.value = list[0]
    }
  } catch {
    channels.value = []
  }
}

/**
 * 加载当前社区生效的满减活动
 */
async function loadFullReductions() {
  try {
    const res = await getFullReductions({ community_id: userStore.communityInfo?.id })
    fullReductions.value = res.data || []
  } catch {
    fullReductions.value = []
  }
}

/**
 * 选择优惠券
 * 加载当前用户未使用且满足门槛的优惠券列表，使用 ActionSheet 选择
 */
async function onSelectCoupon() {
  try {
    const res = await getUserCoupons({ status: 1 })
    const list = res.data?.list || []
    const validList = list.filter((c) => totalAmount.value >= c.threshold)
    if (validList.length === 0) {
      uni.showToast({ title: '暂无可用优惠券', icon: 'none' })
      return
    }
    const itemList = validList.map((c) => `满${formatPrice(c.threshold)}减${formatPrice(c.value)}`)
    itemList.unshift('暂不使用')
    const coupons = [null, ...validList]
    uni.showActionSheet({
      itemList,
      success: (res) => {
        selectedCoupon.value = coupons[res.tapIndex] || null
      },
    })
  } catch {
    uni.showToast({ title: '加载优惠券失败', icon: 'none' })
  }
}

/**
 * 选择支付方式
 * 当前仅开启货到付款时自动选中，多渠道时弹出 ActionSheet
 */
function onSelectChannel() {
  if (!channels.value.length) {
    uni.showToast({ title: '暂无可用支付方式', icon: 'none' })
    return
  }
  uni.showActionSheet({
    itemList: channels.value.map((item) => item.name),
    success: (res) => {
      selectedChannel.value = channels.value[res.tapIndex]
    },
  })
}

/**
 * 提交订单并按所选渠道拉起支付
 * 仅支付真实完成才提示支付成功；用户取消或渠道未开通时不提示成功，
 * 统一跳转订单详情，便于用户在待支付状态下重新支付
 */
async function onSubmit() {
  if (!selectedAddress.value) {
    uni.showToast({ title: '请选择收货地址', icon: 'none' })
    return
  }
  if (!orderItems.value.length) {
    uni.showToast({ title: '商品信息异常', icon: 'none' })
    return
  }
  if (!selectedChannel.value) {
    uni.showToast({ title: '请选择支付方式', icon: 'none' })
    return
  }
  submitting.value = true
  try {
    const res = await createOrder({
      address_id: selectedAddress.value.id,
      items: orderItems.value.map((item) => ({
        product_id: item.product_id,
        sku_id: item.sku_id,
        quantity: item.quantity,
      })),
      remark: remark.value,
      coupon_id: selectedCoupon.value?.coupon_id,
    })
    if (!res.data) {
      uni.showToast({ title: '下单失败', icon: 'none' })
      return
    }
    const outcome = await payOrder(res.data.id, selectedChannel.value.channel)
    notifyPayOutcome(outcome)
    goOrderDetail(res.data.id)
  } catch {
    // 下单或支付失败已由 request 统一提示
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.confirm-page {
  min-height: 100vh;
  padding-bottom: 120rpx;
  background-color: #f5f5f5;
}

.address-card {
  display: flex;
  align-items: center;
  padding: 30rpx;
  background-color: #ffffff;
}

.address-info {
  flex: 1;
}

.address-top {
  display: flex;
  align-items: center;
  margin-bottom: 12rpx;
}

.address-top .name {
  font-size: 30rpx;
  font-weight: bold;
  color: #333333;
}

.address-top .phone {
  margin-left: 20rpx;
  font-size: 28rpx;
  color: #666666;
}

.address-text {
  font-size: 26rpx;
  color: #666666;
  line-height: 1.4;
}

.address-empty {
  flex: 1;
  font-size: 28rpx;
  color: #999999;
}

.goods-card {
  margin-top: 20rpx;
  padding: 20rpx 30rpx;
  background-color: #ffffff;
}

.goods-item {
  display: flex;
  padding: 16rpx 0;
}

.goods-image {
  width: 160rpx;
  height: 160rpx;
  border-radius: 12rpx;
}

.goods-info {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  margin-left: 20rpx;
  height: 160rpx;
}

.goods-name {
  font-size: 28rpx;
  color: #333333;
}

.goods-sku {
  font-size: 24rpx;
  color: #999999;
}

.goods-bottom {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.goods-price {
  font-size: 30rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.goods-quantity {
  font-size: 26rpx;
  color: #999999;
}

.extra-card {
  margin-top: 20rpx;
  padding: 0 30rpx;
  background-color: #ffffff;
}

.extra-item {
  display: flex;
  align-items: center;
  padding: 24rpx 0;
  border-bottom: 1rpx solid #f5f5f5;
  font-size: 28rpx;
  color: #333333;
}

.extra-item:last-child {
  border-bottom: none;
}

.extra-value {
  flex: 1;
  text-align: right;
  margin-right: 12rpx;
  font-size: 26rpx;
  color: #999999;
}

.extra-value.discount-value {
  color: #ff4d4f;
}

.footer-bar {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16rpx 30rpx;
  background-color: #ffffff;
  border-top: 1rpx solid #eeeeee;
}

.total-wrap {
  flex: 1;
  min-width: 0;
  overflow: hidden;
}

.total-label {
  font-size: 28rpx;
  color: #666666;
}

.total-price {
  margin-left: 12rpx;
  font-size: 36rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.discount-tag {
  margin-left: 12rpx;
  padding: 2rpx 10rpx;
  border-radius: 8rpx;
  font-size: 22rpx;
  color: #ff4d4f;
  background-color: rgba(255, 77, 79, 0.1);
}

.submit-btn {
  flex-shrink: 0;
  width: 200rpx;
  margin-left: 20rpx;
}

.submit-btn .u-button {
  width: 100%;
}
</style>
