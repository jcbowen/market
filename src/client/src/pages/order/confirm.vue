<template>
  <view class="confirm-page">
    <!-- 配送方式：社区自提 / 送货上门（按社区渠道开关渲染，缺省视为开启） -->
    <view class="delivery-card">
      <view
        v-if="enableSelfPickup"
        :class="['delivery-option', deliveryType === DELIVERY_TYPE_PICKUP ? 'active' : '']"
        @click="deliveryType = DELIVERY_TYPE_PICKUP"
      >
        <!-- 未选中用 CSS 空心圆：uview-plus 图标库无 "circle" 图标名，缺失时会渲染成文字 -->
        <view class="delivery-radio" :class="{ checked: deliveryType === DELIVERY_TYPE_PICKUP }" />
        <text class="delivery-option-text">社区自提</text>
      </view>
      <view
        v-if="enableDelivery"
        :class="['delivery-option', deliveryType === DELIVERY_TYPE_DELIVERY ? 'active' : '']"
        @click="deliveryType = DELIVERY_TYPE_DELIVERY"
      >
        <view class="delivery-radio" :class="{ checked: deliveryType === DELIVERY_TYPE_DELIVERY }" />
        <text class="delivery-option-text">送货上门</text>
      </view>
      <view v-if="!enableSelfPickup && !enableDelivery" class="delivery-option disabled">
        <text class="delivery-option-text">当前社区暂未开启取货渠道，请联系门店</text>
      </view>
    </view>

    <!-- 自提点信息（社区自提时展示，替代收货地址卡；跨社区结算展示结算社区自提点） -->
    <view v-if="deliveryType === DELIVERY_TYPE_PICKUP" class="address-card">
      <view class="address-info">
        <view class="address-top">
          <text class="name">{{ communityCtx?.name || '当前社区' }}</text>
        </view>
        <!-- 自提卡优先展示自提点地址，未配置时回退社区地址 -->
        <text class="address-text">{{ communityCtx?.pickup_address || communityCtx?.address || '社区地址暂未配置' }}</text>
        <text class="address-tip">商品备好后凭取货信息到点自提</text>
      </view>
    </view>

    <!-- 收货地址（送货上门时展示） -->
    <view v-else class="address-card" @click="goAddressList">
      <view v-if="selectedAddress" class="address-info">
        <view class="address-top">
          <text class="name">{{ selectedAddress.contact_name }}</text>
          <text class="phone">{{ selectedAddress.contact_phone }}</text>
        </view>
        <text class="address-text">
          {{ joinRegionAddress(selectedAddress.province, selectedAddress.city, selectedAddress.district, selectedAddress.detail) }}
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
      <!-- 商品总额：让"总额 + 配送费 - 满减 - 券 = 合计"口径可对账 -->
      <view class="extra-item">
        <text>商品总额</text>
        <text class="extra-value amount-value">{{ formatPrice(totalAmount) }}</text>
      </view>
      <view class="extra-item">
        <text>满减优惠</text>
        <text class="extra-value" :class="{ 'discount-value': fullReductionDiscount > 0 }">{{ fullReductionText }}</text>
      </view>
      <view class="extra-item" @click="onSelectCoupon">
        <text>优惠券</text>
        <text class="extra-value">{{ couponText }}</text>
        <u-icon name="arrow-right" size="14" color="#999999" />
      </view>
      <view v-if="deliveryType === DELIVERY_TYPE_DELIVERY" class="extra-item">
        <text>配送费</text>
        <view class="fs-detail">
          <text class="extra-value" :class="{ 'discount-value': fsFree }">{{ deliveryFeeText }}</text>
          <text v-if="fsHint" class="fs-hint">{{ fsHint }}</text>
          <text v-if="excludedText" class="fs-excluded">不参与：{{ excludedText }}</text>
        </view>
      </view>
      <!-- 自提无配送环节，不展示配送费行，避免"免运费"造成误解 -->
      <!-- 支付方式：直接列出渠道单选，默认选中第一个（无需弹层选择） -->
      <view class="extra-item">
        <text>支付方式</text>
        <view v-if="channels.length" class="channel-list">
          <view
            v-for="channel in channels"
            :key="channel.channel"
            class="channel-option"
            @click="selectedChannel = channel"
          >
            <view
              class="channel-radio"
              :class="{ checked: selectedChannel?.channel === channel.channel }"
            />
            <text
              class="channel-option-text"
              :class="{ active: selectedChannel?.channel === channel.channel }"
            >{{ channel.name }}</text>
          </view>
        </view>
        <text v-else class="channel-empty">暂无可用支付方式</text>
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
        <view class="total-line">
          <text class="total-label">合计:</text>
          <text class="total-price">{{ formatPrice(payAmount) }}</text>
          <text v-if="totalDiscount > 0" class="discount-tag">已优惠 {{ formatPrice(totalDiscount) }}</text>
        </view>
        <text v-if="belowMinAmount > 0" class="min-order-tip">还差 {{ formatPrice(belowMinAmount) }} 起送</text>
      </view>
      <view class="submit-btn">
        <u-button
          type="success"
          text="提交订单"
          :loading="submitting"
          :disabled="belowMinAmount > 0"
          @click="onSubmit"
        />
      </view>
    </view>

    <!-- 优惠券选择弹窗（ActionSheet 上限 6 项，券多时不可用，改用弹窗列表） -->
    <u-popup :show="couponPopupVisible" mode="bottom" :round="16" @close="couponPopupVisible = false">
      <view class="coupon-popup">
        <view class="coupon-popup-header">
          <text class="coupon-popup-title">选择优惠券</text>
          <u-icon name="close" size="20" color="#999999" @click="couponPopupVisible = false" />
        </view>
        <scroll-view scroll-y class="coupon-popup-body">
          <view class="coupon-option" @click="onPickCoupon(null)">
            <text class="coupon-option-name">暂不使用</text>
            <u-icon
              v-if="!selectedCoupon"
              name="checkmark"
              size="18"
              color="#07c160"
            />
          </view>
          <!-- 全量展示：不可用券置灰并展示原因（过期/未达门槛） -->
          <view
            v-for="option in couponOptions"
            :key="option.coupon.id"
            :class="['coupon-option', { 'coupon-option-disabled': !option.available }]"
            @click="option.available && onPickCoupon(option.coupon)"
          >
            <view class="coupon-option-main">
              <text class="coupon-option-name">{{ option.coupon.name }}</text>
              <text class="coupon-option-desc">{{ couponTitle(option.coupon) }}，有效期至 {{ formatDate(option.coupon.end_time) }}</text>
              <text v-if="!option.available" class="coupon-option-reason">{{ option.reason }}</text>
            </view>
            <u-icon
              v-if="selectedCoupon?.id === option.coupon.id"
              name="checkmark"
              size="18"
              color="#07c160"
            />
          </view>
          <view v-if="!couponOptions.length" class="coupon-option-empty">
            <text>暂无可用优惠券</text>
          </view>
        </scroll-view>
      </view>
    </u-popup>
  </view>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { onLoad, onShow } from '@dcloudio/uni-app'
import { useUserStore } from '@/stores/user'
import { useCartStore } from '@/stores/cart'
import { getAddressList, getCommunityById } from '@/api/user'
import { getCart, removeCartItem } from '@/api/cart'
import { getFullReductions, getProductDetail, getSkuById } from '@/api/product'
import { createOrder, previewFreeShipping, type FreeShippingPreview } from '@/api/order'
import { getPaymentChannels } from '@/api/payment'
import { getUserCoupons } from '@/api/marketing'
import { formatPrice, formatDate } from '@/utils/format'
import { notifyPayOutcome, payOrder } from '@/utils/pay'
import { goAddressList, goLogin } from '@/utils/navigate'
import { joinRegionAddress } from '@/utils/region'
import { removeLocalCartItem } from '@/utils/localCart'
import { calcCouponDiscount, couponTitle, isCouponExpired } from '@/utils/coupon'
import { DELIVERY_TYPE_PICKUP, DELIVERY_TYPE_DELIVERY } from '@/utils/order'
import type { Address, Community, FullReduction, PaymentChannel, UserCoupon } from '@/api/types'

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
const cartStore = useCartStore()
const addresses = ref<Address[]>([])
const orderItems = ref<SettleItem[]>([])
const remark = ref('')
const submitting = ref(false)
/** 结算来源的购物车项 ID（下单成功后据此清理服务端购物车） */
const cartItemIds = ref<number[]>([])
const productId = ref(0)
const skuId = ref(0)
const quantity = ref(1)
/** 配送方式：1 社区自提 2 送货上门，默认自提（无运费、无起送门槛） */
const deliveryType = ref<1 | 2>(DELIVERY_TYPE_PICKUP)

// --- 结算社区上下文 ---
// 购物车跨社区结算时由 URL 指定结算社区；不传时按用户当前社区
const settleCommunityId = ref(0)
/** 结算社区完整信息（跨社区结算时异步加载，覆盖自提点/运费/满减等展示） */
const settleCommunity = ref<Community | null>(null)

/** 生效社区 ID：显式指定 > 当前社区 */
const effectiveCommunityId = computed(() => {
  return settleCommunityId.value || userStore.communityInfo?.id || 0
})

/**
 * 生效社区信息：跨社区结算时用结算社区，
 * 直接购买/本社区结算用用户当前社区
 */
const communityCtx = computed<Community | null>(() => {
  if (settleCommunityId.value > 0) return settleCommunity.value || userStore.communityInfo
  return userStore.communityInfo
})

// 社区渠道开关（proto omitempty：false 时字段缺省，?? true 兼容旧缓存；按结算社区口径）
const enableSelfPickup = computed(() => communityCtx.value?.enable_self_pickup ?? true)
const enableDelivery = computed(() => communityCtx.value?.enable_delivery ?? true)

// 默认渠道自适应：默认渠道被关闭时切换到可用渠道（都关闭时保持自提，页面展示联系门店提示且由提交拦截）
watch(
  [enableSelfPickup, enableDelivery],
  ([pickupOn, deliveryOn]) => {
    if (!pickupOn && deliveryOn) deliveryType.value = DELIVERY_TYPE_DELIVERY
    if (pickupOn && !deliveryOn) deliveryType.value = DELIVERY_TYPE_PICKUP
  },
  { immediate: true }
)

// 优先使用用户在地址列表页选择的地址，其次默认地址（避免选择动作无效）
const selectedAddress = computed(() => {
  const picked = addresses.value.find((item) => item.id === userStore.addressInfo?.id)
  return picked || addresses.value.find((item) => item.is_default) || addresses.value[0]
})

const totalAmount = computed(() => {
  return orderItems.value.reduce((sum, item) => sum + item.price * item.quantity, 0)
})

// 社区配送费/起送金额：proto 为 omitempty，值为 0 时字段缺省，必须 ?? 0（按结算社区口径）
const deliveryFee = computed(() => {
  if (deliveryType.value !== DELIVERY_TYPE_DELIVERY) return 0
  // 满额免配送费活动命中时实际配送费为 0（与后端 CreateOrder 口径一致）
  if (fsPreview.value?.free_shipping) return 0
  return communityCtx.value?.delivery_fee ?? 0
})

// --- 免配送费试算（配送单展示） ---
const fsPreview = ref<FreeShippingPreview | null>(null)

const fsFree = computed(() => !!fsPreview.value?.free_shipping)

const deliveryFeeText = computed(() => {
  if (fsFree.value) return '免配送费'
  return formatPrice(deliveryFee.value)
})

/** 免配送费进度提示：已满足显示活动名，未满足显示还差金额 */
const fsHint = computed(() => {
  const p = fsPreview.value
  if (!p || !p.activity_name) return ''
  if (p.free_shipping) return `${p.activity_name}·已免配送费`
  if (p.threshold > 0) return `${p.activity_name}：还差 ${formatPrice(p.remain_amount)} 免配送费`
  return ''
})

/** 不参与商品/分类提示（点击无需展开，直接一行小字） */
const excludedText = computed(() => {
  const names = fsPreview.value?.excluded_names || []
  return names.join('、')
})

/**
 * 加载免配送费试算：仅配送单且商品清单非空时调用
 * 商品清单或配送方式变化后自动刷新
 */
async function loadFsPreview() {
  if (deliveryType.value !== DELIVERY_TYPE_DELIVERY || !orderItems.value.length) {
    fsPreview.value = null
    return
  }
  // 未确定结算社区（服务端 community_id 为空）时不发起试算：
  // 服务端会直接拒绝并弹"缺少社区信息"，这里静默跳过，由提交时统一引导
  if (!effectiveCommunityId.value) {
    fsPreview.value = null
    return
  }
  try {
    const res = await previewFreeShipping(
      orderItems.value.map((it) => ({ sku_id: it.sku_id, quantity: it.quantity })),
      effectiveCommunityId.value,
    )
    fsPreview.value = res.data || null
  } catch {
    fsPreview.value = null
  }
}

watch([deliveryType, orderItems], () => {
  void loadFsPreview()
})

const minOrderAmount = computed(() => {
  if (deliveryType.value !== DELIVERY_TYPE_DELIVERY) return 0
  return communityCtx.value?.min_order_amount ?? 0
})

/** 配送单未达起送门槛的差额，> 0 时禁用提交（校验前置，替代后端报错） */
const belowMinAmount = computed(() => {
  if (minOrderAmount.value <= 0 || totalAmount.value >= minOrderAmount.value) return 0
  return minOrderAmount.value - totalAmount.value
})

const channels = ref<PaymentChannel[]>([])
const selectedChannel = ref<PaymentChannel | null>(null)
const selectedCoupon = ref<UserCoupon | null>(null)
const fullReductions = ref<FullReduction[]>([])
const couponPopupVisible = ref(false)

/** 弹窗券选项：券 + 可用性判定（不可用置灰并展示原因） */
interface CouponOption {
  coupon: UserCoupon
  available: boolean
  /** 不可用原因（可用时为空） */
  reason: string
}
const couponOptions = ref<CouponOption[]>([])
/** 用户是否手动操作过券选择：操作后不再自动改选 */
let couponTouched = false

const couponText = computed(() => {
  if (!selectedCoupon.value) return '暂不使用'
  return couponTitle(selectedCoupon.value)
})

/**
 * 按当前商品总额构建券选项可用性：
 * 过期券与未达门槛券均展示但置灰，并给出原因文案
 */
function buildCouponOptions(list: UserCoupon[]): CouponOption[] {
  const total = totalAmount.value
  return list.map((coupon) => {
    if (isCouponExpired(coupon)) {
      return { coupon, available: false, reason: '已过期' }
    }
    if (total < coupon.threshold) {
      const lack = formatPrice(coupon.threshold - total)
      return { coupon, available: false, reason: `满${formatPrice(coupon.threshold)}可用，还差 ${lack}` }
    }
    return { coupon, available: true, reason: '' }
  })
}

/**
 * 加载用户全部未使用券并构建弹窗选项
 * 进入页面即调用一次，供默认选中最优券使用
 */
async function loadCouponOptions() {
  try {
    const res = await getUserCoupons({ status: 1, page_size: 100 })
    couponOptions.value = buildCouponOptions(res.data?.list || [])
    autoSelectCoupon()
  } catch {
    couponOptions.value = []
  }
}

/**
 * 自动选中最优可用券（抵扣金额最大）
 * 用户手动选择过（含"暂不使用"）后不再自动改选，
 * 仅在已选券变为不可用时清空，避免实付金额与展示不符
 */
function autoSelectCoupon() {
  if (couponTouched) {
    if (selectedCoupon.value) {
      const current = couponOptions.value.find((o) => o.coupon.id === selectedCoupon.value?.id)
      if (!current || !current.available) {
        selectedCoupon.value = null
      }
    }
    return
  }
  let best: UserCoupon | null = null
  let bestDiscount = 0
  for (const option of couponOptions.value) {
    if (!option.available) continue
    const discount = calcCouponDiscount(totalAmount.value, option.coupon)
    if (discount > bestDiscount) {
      bestDiscount = discount
      best = option.coupon
    }
  }
  selectedCoupon.value = best
}

// 商品总额变化（商品清单加载完成等）后按新总额刷新券可用性并重选最优
watch(totalAmount, () => {
  if (!couponOptions.value.length) return
  couponOptions.value = buildCouponOptions(couponOptions.value.map((o) => o.coupon))
  autoSelectCoupon()
})

/**
 * 计算当前订单可享的满减优惠
 * 规则与后端一致（service/order.go calculateFullReduction）：
 * 在生效且满足门槛的满减活动中取减免金额最大的一项，不超过商品总额
 */
const fullReductionDiscount = computed(() => {
  const communityId = effectiveCommunityId.value
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

// 券抵扣按类型计算，与后端 calculateDiscount 逐行一致（utils/coupon.ts 有注释锚定）
const couponDiscount = computed(() => {
  return calcCouponDiscount(totalAmount.value, selectedCoupon.value)
})

/**
 * 实付金额：与后端 service/order.go CreateOrder 的
 * payAmount = totalAmount + deliveryFee - fullReductionAmount - discountAmount（负数取 0）保持一致
 */
const payAmount = computed(() => {
  return Math.max(0, totalAmount.value + deliveryFee.value - fullReductionDiscount.value - couponDiscount.value)
})

/** 合计优惠展示（满减 + 券，不含运费） */
const totalDiscount = computed(() => {
  return fullReductionDiscount.value + couponDiscount.value
})

onLoad((query) => {
  productId.value = Number(query?.productId) || 0
  skuId.value = Number(query?.skuId) || 0
  quantity.value = Number(query?.quantity) || 1
  const ids = query?.cartItemIds
  if (ids) {
    cartItemIds.value = String(ids).split(',').map(Number)
  }
  // 购物车跨社区结算：显式指定结算社区（商品/履约/活动均按该社区口径）
  settleCommunityId.value = Number(query?.communityId) || 0
})

/** 进入页面时记录的社区 ID，用于识别结算中途切换社区 */
const enteredCommunityId = ref(0)
let communityInitialized = false

onShow(() => {
  if (!userStore.isLogin) {
    goLogin()
    return
  }
  const currentCommunityId = userStore.communityInfo?.id || 0
  // 结算中途切换社区：仅本社区结算场景需要打断——购物车按社区隔离，
  // 原购物车商品在新社区查不到，商品信息会丢失；
  // 跨社区结算（URL 指定 communityId）以结算社区为准，不受当前社区切换影响；
  // 直接购买（skuId）不依赖社区购物车，仅刷新社区运费等信息，不打断
  if (
    settleCommunityId.value <= 0 &&
    communityInitialized &&
    enteredCommunityId.value > 0 &&
    currentCommunityId !== enteredCommunityId.value &&
    (cartItemIds.value.length > 0 || !skuId.value)
  ) {
    uni.showToast({ title: '已切换社区，请重新选购商品', icon: 'none' })
    setTimeout(() => {
      const pages = getCurrentPages()
      if (pages.length > 1) {
        uni.navigateBack({})
      } else {
        uni.switchTab({ url: '/pages/cart/cart' })
      }
    }, 800)
    return
  }
  enteredCommunityId.value = currentCommunityId
  communityInitialized = true
  if (settleCommunityId.value > 0 && settleCommunityId.value !== currentCommunityId) {
    loadSettleCommunity()
  } else {
    // 本社区结算：清除可能残留的跨社区上下文（如上次结算遗留）
    settleCommunity.value = null
  }
  loadAddresses()
  loadPaymentChannels()
  loadFullReductions()
  loadItems()
  // 券选项进入页面即加载：默认选中最优可用券（不依赖用户点开弹窗）
  loadCouponOptions()
})

/**
 * 加载结算社区完整信息（跨社区结算场景）
 * 自提点、配送费、起送金额、满减与免配送费试算均按该社区展示与计算
 */
async function loadSettleCommunity() {
  const community = await getCommunityById(settleCommunityId.value)
  settleCommunity.value = community
  if (!community) {
    uni.showToast({ title: '结算社区信息加载失败', icon: 'none' })
  }
}

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
 * 从购物车加载全部选中商品（未携带 cartItemIds 的结算入口兜底路径）
 * 选中项的 id 回填到 cartItemIds，保证下单成功后能清理购物车
 * 仅取结算社区（URL 指定或当前社区）的选中商品，避免跨社区混单
 */
async function loadSelectedCartItems() {
  try {
    const res = await getCart()
    const targetCid = effectiveCommunityId.value
    const cartItems = res.data?.items || []
    const selected = cartItems.filter(
      (item) => item.selected && (!targetCid || (item.community_id || 0) === targetCid),
    )
    cartItemIds.value = selected.map((item) => item.id).filter((id) => id > 0)
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
 * 从购物车按 URL 参数加载选中商品
 * 不按社区过滤购物车（跨社区结算时目标商品不在当前社区购物车范围内）
 */
async function loadCartItems() {
  try {
    const res = await getCart()
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
 * 加载结算社区生效的满减活动
 */
async function loadFullReductions() {
  try {
    const res = await getFullReductions({ community_id: effectiveCommunityId.value || undefined })
    fullReductions.value = res.data || []
  } catch {
    fullReductions.value = []
  }
}

/**
 * 打开优惠券选择弹窗
 * 展示全部未使用券：可用的正常选择，不可用的置灰并展示原因
 */
async function onSelectCoupon() {
  // 重新拉取一次，保证总额/时效变化后可用性最新
  await loadCouponOptions()
  couponPopupVisible.value = true
}

/**
 * 弹窗内选择优惠券（含"暂不使用"）
 * 手动选择后标记 touched，后续总额变化不再自动改选
 * @param coupon 优惠券，null 表示不使用
 */
function onPickCoupon(coupon: UserCoupon | null) {
  couponTouched = true
  selectedCoupon.value = coupon
  couponPopupVisible.value = false
}

/**
 * 下单成功后清理购物车：
 * 删除本次结算对应的服务端购物车项，并同步清理本地购物车与角标
 * 本地购物车按结算社区清理（跨社区结算时非当前社区）
 */
async function clearSettledCart() {
  const communityId = effectiveCommunityId.value
  for (const id of cartItemIds.value) {
    try {
      await removeCartItem(id)
    } catch {
      // 单条删除失败不阻断支付流程，购物车残留可手动删除
    }
  }
  for (const item of orderItems.value) {
    removeLocalCartItem(item.sku_id, communityId)
  }
  cartStore.syncCartCount()
}

/**
 * 提交订单并按所选渠道拉起支付
 * 仅支付真实完成才提示支付成功；用户取消或渠道未开通时不提示成功，
 * 统一跳转订单详情，便于用户在待支付状态下重新支付
 */
async function onSubmit() {
  // 渠道兜底：双渠道均关闭时不允许提交（后端 CreateOrder 亦有校验）
  if (!enableSelfPickup.value && !enableDelivery.value) {
    uni.showToast({ title: '当前社区暂未开启取货渠道', icon: 'none' })
    return
  }
  // 社区兜底：服务端按结算社区下单，本地无法确定社区时提前拦截并引导，
  // 避免提交后才收到服务端报错
  if (!effectiveCommunityId.value) {
    uni.showToast({ title: '请先选择社区', icon: 'none' })
    return
  }
  // 地址仅送货上门时必填，自提无需收货地址
  if (deliveryType.value === DELIVERY_TYPE_DELIVERY && !selectedAddress.value) {
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
      delivery_type: deliveryType.value,
      // 自提单地址传 0，后端对 AddressId > 0 才查询地址
      address_id: deliveryType.value === DELIVERY_TYPE_DELIVERY ? selectedAddress.value!.id : 0,
      // 结算社区：跨社区结算时按结算社区下单（订单归属/履约/活动均按该社区），不传按用户当前社区
      community_id: effectiveCommunityId.value || undefined,
      items: orderItems.value.map((item) => ({
        product_id: item.product_id,
        sku_id: item.sku_id,
        quantity: item.quantity,
      })),
      remark: remark.value,
      coupon_id: selectedCoupon.value?.coupon_id,
      cart_item_ids: cartItemIds.value,
    })
    if (!res.data) {
      uni.showToast({ title: '下单失败', icon: 'none' })
      return
    }
    // 下单成功后清理购物车与临时地址选择，避免重复下单或沿用上次选择
    await clearSettledCart()
    userStore.setAddress(null)
    const outcome = await payOrder(res.data.id, selectedChannel.value.channel)
    notifyPayOutcome(outcome, selectedChannel.value.channel)
    // redirectTo 替换当前确认页：返回键/滑动返回直接回到进入确认页前的页面，
    // 避免再回到确认页看到已结算商品或重复下单
    uni.redirectTo({ url: `/pages/order/detail?id=${res.data.id}` })
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
  padding-bottom: 140rpx;
  background-color: #f5f5f5;
}

.delivery-card {
  display: flex;
  background-color: #ffffff;
}

.delivery-option {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10rpx;
  padding: 24rpx 0;
  font-size: 28rpx;
  color: #666666;
}

.delivery-option.active {
  color: #07c160;
  font-weight: bold;
}

/* 双渠道均关闭时的占位提示样式 */
.delivery-option.disabled {
  color: #bbbbbb;
  font-size: 26rpx;
}

.delivery-option-text {
  font-size: 28rpx;
}

/* 单选圆点：未选中为灰色空心圆，选中为绿色实心（内嵌白色对勾感） */
.delivery-radio {
  width: 36rpx;
  height: 36rpx;
  box-sizing: border-box;
  border: 2rpx solid #cccccc;
  border-radius: 50%;
  background-color: transparent;
  transition: all 0.2s;
}

.delivery-radio.checked {
  border: 10rpx solid #07c160;
}

.address-card {
  display: flex;
  align-items: center;
  padding: 30rpx;
  background-color: #ffffff;
  border-top: 1rpx solid #f5f5f5;
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

.address-tip {
  display: block;
  margin-top: 8rpx;
  font-size: 22rpx;
  color: #999999;
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

/* 金额类明细（商品总额）用深色，与灰色占位文案区分 */
.amount-value {
  color: #333333;
}

/* 支付方式内联单选：右侧横向排列渠道，圆点样式与配送方式一致 */
.channel-list {
  flex: 1;
  display: flex;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 24rpx;
  margin-left: 12rpx;
}

.channel-option {
  display: flex;
  align-items: center;
  gap: 10rpx;
}

.channel-radio {
  width: 32rpx;
  height: 32rpx;
  box-sizing: border-box;
  border: 2rpx solid #cccccc;
  border-radius: 50%;
  background-color: transparent;
  transition: all 0.2s;
}

.channel-radio.checked {
  border: 9rpx solid #07c160;
}

.channel-option-text {
  font-size: 26rpx;
  color: #333333;
}

.channel-option-text.active {
  color: #07c160;
  font-weight: bold;
}

/* 无可用渠道时的占位提示 */
.channel-empty {
  flex: 1;
  text-align: right;
  margin-right: 12rpx;
  font-size: 26rpx;
  color: #999999;
}

/* 免配送费试算展示：右对齐两行（金额+活动进度提示） */
.fs-detail {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  margin-right: 12rpx;
}

.fs-hint {
  margin-top: 4rpx;
  font-size: 22rpx;
  color: #07c160;
}

.fs-excluded {
  margin-top: 4rpx;
  font-size: 22rpx;
  color: #999999;
  text-align: right;
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

.total-line {
  display: flex;
  align-items: center;
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

.min-order-tip {
  display: block;
  margin-top: 4rpx;
  font-size: 22rpx;
  color: #ff9800;
}

.submit-btn {
  flex-shrink: 0;
  width: 200rpx;
  margin-left: 20rpx;
}

.submit-btn .u-button {
  width: 100%;
}

.coupon-popup {
  padding: 30rpx 30rpx calc(30rpx + env(safe-area-inset-bottom));
}

.coupon-popup-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20rpx;
}

.coupon-popup-title {
  font-size: 32rpx;
  font-weight: bold;
  color: #333333;
}

.coupon-popup-body {
  max-height: 60vh;
}

.coupon-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 24rpx 20rpx;
  border-bottom: 1rpx solid #f5f5f5;
}

.coupon-option-main {
  flex: 1;
  min-width: 0;
}

.coupon-option-name {
  display: block;
  font-size: 28rpx;
  color: #333333;
}

.coupon-option-desc {
  display: block;
  margin-top: 6rpx;
  font-size: 22rpx;
  color: #999999;
}

/* 不可用券置灰展示 */
.coupon-option-disabled {
  opacity: 0.45;
  pointer-events: none;
}

.coupon-option-reason {
  display: block;
  margin-top: 6rpx;
  font-size: 22rpx;
  color: #ff4d4f;
}

/* 券为空时的占位提示 */
.coupon-option-empty {
  padding: 40rpx 0;
  text-align: center;
  font-size: 26rpx;
  color: #999999;
}
</style>
