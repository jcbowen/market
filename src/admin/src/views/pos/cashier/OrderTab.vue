<template>
  <div class="order-tab">
    <!-- 左：分类导航 + 商品卡片网格 -->
    <div class="pane-left">
      <div class="search-bar">
        <n-input
          v-model:value="keyword"
          placeholder="搜索商品名 / 拼音首字母 / 扫条码"
          clearable
          size="large"
          class="search"
          @keyup.enter="onSearchEnter"
        >
          <template #prefix>
            <n-icon><SearchOutline /></n-icon>
          </template>
        </n-input>
        <!-- A-Z 首字母筛选：按商品名首个字拼音首字母过滤（通讯录式） -->
        <div class="letter-bar">
          <span class="letter" :class="{ active: activeLetter === '' }" @click="activeLetter = ''">全部</span>
          <span
            v-for="l in LETTERS"
            :key="l"
            class="letter"
            :class="{ active: activeLetter === l }"
            @click="activeLetter = activeLetter === l ? '' : l"
          >{{ l }}</span>
        </div>
      </div>
      <div class="product-area">
        <!-- 分类竖栏：从在售商品聚合，仅展示有商品的分类 -->
        <div class="category-bar">
          <div
            v-for="c in categoryTabs"
            :key="c.id"
            class="cat-item"
            :class="{ active: c.id === activeCategory }"
            @click="activeCategory = c.id"
          >{{ c.name }}</div>
        </div>
        <n-spin :show="productLoading" class="product-grid-wrap">
          <n-empty v-if="!filteredProducts.length && !productLoading" description="没有匹配的商品" class="empty" />
          <div v-else class="product-grid">
            <div
              v-for="p in filteredProducts"
              :key="p.id"
              class="product-card"
              :class="{ disabled: p.stock <= 0 }"
              @click="onPickProduct(p)"
            >
              <!-- 禁用预览：收银场景点击即加购，弹大图预览属误触 -->
              <!-- 无封面图时与 C 端口径一致，兜底显示默认商品图 -->
              <n-image
                :src="p.cover_image || defaultProductImage"
                object-fit="cover"
                class="cover"
                :preview-disabled="true"
                :img-props="{ referrerpolicy: 'no-referrer' }"
              >
                <template #error>
                  <img :src="defaultProductImage" alt="默认商品图" />
                </template>
              </n-image>
              <div class="p-name">{{ p.name }}</div>
              <div class="p-meta">
                <span class="p-price">￥{{ formatAmount(p.min_price ?? p.price) }}</span>
                <span class="p-stock">{{ p.stock > 0 ? `库存${p.stock}` : '缺货' }}</span>
              </div>
              <n-tag v-if="p.is_multi_spec" size="small" type="warning" :bordered="false" class="spec-badge">
                多规格
              </n-tag>
            </div>
          </div>
        </n-spin>
      </div>
    </div>

    <!-- 右：当前小票 -->
    <div class="pane-right">
      <!-- 会员与优惠券 -->
      <div class="member-bar">
        <n-tag v-if="member" type="primary" size="large" closable @close="clearMember">
          {{ member.nickname || member.phone }}
        </n-tag>
        <n-tag v-else type="default" size="large">散客</n-tag>
        <n-button size="medium" @click="showUserPicker = true">
          {{ member ? '更换会员' : '选会员' }}
        </n-button>
        <n-select
          v-if="couponOptions.length"
          v-model:value="couponId"
          :options="couponSelectOptions"
          placeholder="使用优惠券"
          clearable
          size="medium"
          class="coupon-select"
        />
      </div>

      <!-- 商品明细 -->
      <n-empty v-if="!cart.length" description="小票为空，点击左侧商品加入" class="empty" />
      <div v-else class="cart-list">
        <div v-for="row in cart" :key="row.sku_id" class="cart-row">
          <div class="cart-info">
            <div class="cart-name">
              {{ row.product_name }}
              <span v-if="row.spec_name" class="spec">{{ row.spec_name }}</span>
            </div>
            <!-- 价格：点击改价（弹窗确认，仅本单生效） -->
            <div class="cart-price" title="点击改价" @click="openPriceModal(row)">
              <template v-if="row.price_modified">￥{{ formatAmount(row.price) }}<span class="origin">￥{{ formatAmount(row.original_price) }}</span></template>
              <template v-else>￥{{ formatAmount(row.price) }} <n-icon :size="13" class="edit-hint"><CreateOutline /></n-icon></template>
            </div>
          </div>
          <div class="stepper">
            <n-button size="small" circle @click="changeQty(row.sku_id, -1)">-</n-button>
            <span class="qty">{{ row.quantity }}</span>
            <n-button size="small" circle @click="changeQty(row.sku_id, 1)">+</n-button>
          </div>
          <div class="cart-subtotal">￥{{ formatAmount(row.price * row.quantity) }}</div>
          <n-button size="small" quaternary type="error" @click="removeRow(row.sku_id)">删除</n-button>
        </div>
      </div>

      <!-- 渠道与配送信息：到店恒可用（不受社区渠道配置控制），自提/配送按社区开关渲染 -->
      <div class="delivery-bar">
        <n-radio-group v-model:value="deliveryType" size="medium">
          <n-radio-button :value="DELIVERY_TYPE.STORE">到店</n-radio-button>
          <n-radio-button v-if="enableSelfPickup" :value="DELIVERY_TYPE.SELF_PICKUP">自提</n-radio-button>
          <n-radio-button v-if="enableDelivery" :value="DELIVERY_TYPE.DELIVERY">配送</n-radio-button>
        </n-radio-group>
        <template v-if="deliveryType === DELIVERY_TYPE.DELIVERY">
          <n-input v-model:value="contact.name" placeholder="联系人" size="medium" class="contact-input" />
          <n-input v-model:value="contact.phone" placeholder="电话" size="medium" class="contact-input" />
          <n-input v-model:value="contact.address" placeholder="收货地址" size="medium" class="address-input" />
        </template>
      </div>
      <n-input v-model:value="remark" placeholder="整单备注（选填）" size="medium" class="remark-input" />

      <!-- 优惠：不优惠 / 抹零 / 整单改价（仅对本次订单生效，不改商品档案价） -->
      <div class="discount-bar">
        <n-radio-group v-model:value="discountMode" size="medium">
          <n-radio-button value="none">不优惠</n-radio-button>
          <n-radio-button value="round">抹零</n-radio-button>
          <n-radio-button value="whole">整单改价</n-radio-button>
        </n-radio-group>
        <div v-if="discountMode === 'round'" class="discount-detail">
          <n-radio-group v-model:value="roundKind" size="medium">
            <n-radio-button value="fen">抹分</n-radio-button>
            <n-radio-button value="jiao">抹角</n-radio-button>
            <n-radio-button value="custom">自定义</n-radio-button>
          </n-radio-group>
          <n-input-number
            v-if="roundKind === 'custom'"
            v-model:value="customDiscount"
            size="medium"
            :min="0"
            :max="totalAmount"
            :precision="2"
            placeholder="优惠金额"
            class="custom-discount"
          />
        </div>
        <div v-else-if="discountMode === 'whole'" class="discount-detail">
          <span class="discount-label">成交价</span>
          <n-input-number
            v-model:value="wholePrice"
            size="medium"
            :min="0.01"
            :max="totalAmount || 99999"
            :precision="2"
            placeholder="整单成交价"
            class="custom-discount"
          />
        </div>
      </div>

      <!-- 合计与结账 -->
      <div class="summary">
        <div class="summary-row muted">
          <span>商品 {{ totalQuantity }} 件 · 合计 ￥{{ formatAmount(totalAmount) }}</span>
        </div>
        <div v-if="couponDiscount > 0" class="summary-row">
          <span>券优惠</span>
          <span class="discount-amount">-￥{{ formatAmount(couponDiscount) }}</span>
        </div>
        <div v-if="couponId && couponDiscount === 0" class="summary-row muted">
          <span>所选券未达使用门槛</span>
        </div>
        <div v-if="manualDiscount > 0" class="summary-row">
          <span>手动优惠</span>
          <span class="discount-amount">-￥{{ formatAmount(manualDiscount) }}</span>
        </div>
        <div class="summary-row payable">
          <span>应付</span>
          <span class="amount">￥{{ formatAmount(payableAmount) }}</span>
        </div>
        <div class="summary-hint">满减与券减免以后端实际结算为准</div>
      </div>
      <n-space justify="end" :size="12" class="actions">
        <n-button @click="openHeldList">挂单({{ heldCount }})</n-button>
        <n-button type="warning" :disabled="!cart.length" @click="onHold">暂存挂单</n-button>
        <!-- 结账双按钮：结账即收款，免选支付方式。付款码渠道不可用时只显示现金 -->
        <n-button
          v-if="codePayMethod"
          type="primary"
          class="checkout-btn"
          :loading="checkingOut"
          :disabled="!cart.length"
          @click="onCodePay"
        >
          付款码收款 ￥{{ formatAmount(payableAmount) }}（F2）
        </n-button>
        <n-button
          type="warning"
          secondary
          :loading="checkingOut"
          :disabled="!cart.length"
          @click="onCashPay"
        >
          现金收款（F3）
        </n-button>
      </n-space>
    </div>
  </div>

  <!-- 多规格选规格弹窗 -->
  <n-modal
    :show="showSkuModal"
    preset="card"
    title="选择规格"
    style="width: 480px; max-width: 92vw"
    @update:show="(v: boolean) => (showSkuModal = v)"
  >
    <n-empty v-if="!skuCandidates.length" description="该商品暂无可售规格" />
    <div v-else class="sku-list">
      <div
        v-for="sku in skuCandidates"
        :key="sku.id"
        class="sku-row"
        :class="{ disabled: sku.stock <= 0 }"
        @click="addSkuToCart(sku)"
      >
        <div class="sku-name">{{ sku.spec_name || sku.sku_code }}</div>
        <div class="sku-meta">￥{{ formatAmount(sku.price) }}（库存 {{ sku.stock }}）</div>
      </div>
    </div>
  </n-modal>

  <!-- 挂单列表弹窗（随时可查看，不受当前小票影响） -->
  <n-modal
    :show="showHeldModal"
    preset="card"
    title="挂单列表"
    style="width: 720px; max-width: 92vw"
    @update:show="(v: boolean) => (showHeldModal = v)"
  >
    <n-spin :show="heldLoading">
      <n-empty v-if="!heldOrders.length" description="暂无挂单" />
      <div v-else class="held-list">
        <div v-for="h in heldOrders" :key="h.id" class="held-row">
          <div class="held-main">
            <div class="held-title">
              <n-tag size="small" :type="h.user_id ? 'primary' : 'default'">
                {{ h.user_id ? h.user_name || '会员' : '散客' }}
              </n-tag>
              <span>{{ h.item_count }} 件商品</span>
              <span class="held-amount">￥{{ formatAmount(h.total_amount) }}</span>
            </div>
            <div class="held-sub">
              {{ h.items.map((i) => `${i.product_name}${i.spec_name ? `(${i.spec_name})` : ''}x${i.quantity}`).join('、') }}
            </div>
            <div class="held-sub muted">挂单人 {{ h.operator_name || '-' }} · {{ formatDateTime(h.created_at) }}</div>
          </div>
          <n-space :size="8">
            <n-button size="small" type="primary" @click="onRestore(h)">恢复</n-button>
            <n-button size="small" type="error" quaternary @click="onDeleteHeld(h)">删除</n-button>
          </n-space>
        </div>
      </div>
    </n-spin>
  </n-modal>

  <!-- 付款码扫码弹窗（共用组件：下单建待支付单后弹出，支持失败改现金） -->
  <code-pay-dialog
    v-model:show="showCodePayModal"
    :order-id="codePayOrderId"
    :order-no="codePayOrderNo"
    :amount="codePayAmount"
    :payment-no="codePayPaymentNo"
    :pickup-code="codePayPickupCode"
    @paid="onCodePaid"
    @close-abandoned="onCodeAbandoned"
  />

  <!-- 结账成功弹窗（58mm 热敏小票预览与打印，与直接收款共用） -->
  <receipt-modal v-model:show="showPaidModal" :receipt="receipt" />

  <!-- 单品改价弹窗（仅对本次订单生效） -->
  <n-modal
    :show="showPriceModal"
    preset="dialog"
    type="info"
    :title="`改价 - ${editRow?.product_name ?? ''}${editRow?.spec_name ? `（${editRow.spec_name}）` : ''}`"
    positive-text="确定改价"
    negative-text="取消"
    @positive-click="confirmEditPrice"
    @negative-click="showPriceModal = false"
    @update:show="(v: boolean) => (showPriceModal = v)"
  >
    <div class="price-modal-tip">
      <template v-if="editRow">原价 ￥{{ formatAmount(editRow.original_price) }}，</template>改价仅对当前这笔订单生效，不改商品档案价
    </div>
    <n-input-number
      v-model:value="editPrice"
      size="large"
      :min="0.01"
      :max="99999"
      :precision="2"
      autofocus
      style="width: 200px"
    />
  </n-modal>

  <!-- 会员选择（单选，支持现场注册新会员） -->
  <user-picker v-model:show="showUserPicker" title="选择会员" registerable register-scene="pos" @select="onSelectMember" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { formatAmount } from '@/utils/format'
import { useDialog, useMessage } from 'naive-ui'
import { CreateOutline, SearchOutline } from '@vicons/ionicons5'
import defaultProductImage from '@/assets/logo.png'
import { pinyin } from 'pinyin-pro'
import { createPosHeldOrder, createPosOrder, deletePosHeldOrder, ensurePosCodePaySession, getPosHeldOrders, listPosPayMethods } from '@/api/order'
import { getCouponRecords } from '@/api/marketing'
import { getProductByBarcode, getProductDetail, getProductList } from '@/api/product'
import { getCommunityProfile } from '@/api/user'
import type { PosHeldOrder, Product, Sku, User } from '@/api/types'
import UserPicker from '@/components/UserPicker.vue'
import CodePayDialog from './CodePayDialog.vue'
import ReceiptModal, { type PaidReceipt } from './ReceiptModal.vue'
import { DELIVERY_TYPE, PAY_TYPE } from '@/constants/order'
import { usePosStore } from '@/stores/pos'
import { formatDateTime } from '@/utils/datetime'

/**
 * 商品下单 Tab（收银台核心，点单收银机风格）
 * 选会员（可散客，支持现场注册新会员）→ 分类/首字母定位商品 → 卡片选商品 → 支持单品改价/整单备注/抹零/整单改价
 * （均仅对本次订单生效，不改商品档案价）→ 券优惠预估与明细 → 挂单暂存/恢复/删除
 * → 双按钮结账（付款码 F2 / 现金 F3，结账即收款，免选支付方式）→ 成功可打印 58mm 热敏小票。
 * 支持订单查询「引用到收银台」（posStore 待引用清单挂载时消费）。最终金额以服务端结算为准。
 */

const dialog = useDialog()
const message = useMessage()
const posStore = usePosStore()

// --- 商品搜索与筛选 ---
const keyword = ref('')
const products = ref<Product[]>([])
const productLoading = ref(false)

/** A-Z 字母表（首字母筛选条） */
const LETTERS = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'.split('')
/** 当前选中的首字母（''=全部） */
const activeLetter = ref('')
/** 当前选中的分类 ID（0=全部） */
const activeCategory = ref(0)

/**
 * 拉取本社区全部在售商品（status=1）：分页循环拉全量。
 * 搜索/分类/首字母均在前端本地过滤（含拼音首字母匹配），关键词不传后端，
 * 否则后端 name LIKE 会对拼音首字母关键词返回空列表，导致本地过滤无数据可滤。
 */
async function searchProducts() {
  productLoading.value = true
  try {
    const pageSize = 100
    const first = await getProductList({ page: 1, page_size: pageSize, status: 1 })
    if (first.data.code !== 0) return
    const total = first.data.data.total ?? (first.data.data.list || []).length
    const list = [...(first.data.data.list || [])]
    // 单页装不下时按页补齐（在售商品通常几十个，一般一两页完成）
    for (let page = 2; list.length < total && page <= 10; page++) {
      const res = await getProductList({ page, page_size: pageSize, status: 1 })
      if (res.data.code !== 0 || !res.data.data.list?.length) break
      list.push(...res.data.data.list)
    }
    products.value = list
  } finally {
    productLoading.value = false
  }
}

/** 纯数字条形码关键词（6-20 位，与后端条码口径一致） */
const barcodeKeyword = computed(() => (/^\d{6,20}$/.test(keyword.value.trim()) ? keyword.value.trim() : ''))

/** 条码查询进行中标记（防扫码枪连发重复请求） */
let barcodeLooking = false

/**
 * 搜索框回车：纯数字条形码优先走条码查询，命中直接加购；
 * 未命中提示后保留关键词走本地商品名过滤
 * 扫码枪等价于键盘输入 + 回车，无需额外驱动
 */
async function onSearchEnter() {
  const code = barcodeKeyword.value
  if (!code || barcodeLooking) return
  barcodeLooking = true
  try {
    const res = await getProductByBarcode(code)
    if (res.data.code === 0 && res.data.data?.sku) {
      const { product, sku } = res.data.data
      if (sku.stock <= 0) {
        message.warning(`「${product.name}」库存不足`)
        return
      }
      addSkuToCart(sku, product.name)
      keyword.value = ''
    }
  } catch {
    // 未命中：request 拦截器已弹出后端文案（"未找到该条码对应的商品"），
    // 这里静默保留关键词，回落到本地商品名过滤
  } finally {
    barcodeLooking = false
  }
}

/**
 * 商品名首个字的拼音首字母（非汉字取原字符），A-Z 筛选分组用
 * @param name 商品名
 */
function firstLetterOf(name: string): string {
  const first = pinyin(name, { pattern: 'first', toneType: 'none' }).charAt(0)
  return /[A-Z]/i.test(first) ? first.toUpperCase() : '#'
}

/**
 * 商品名整串拼音首字母（去空格大写），关键词首字母搜索用（如输入 "kl" 匹配「可乐」）
 * @param name 商品名
 */
function initialsOf(name: string): string {
  return pinyin(name, { pattern: 'first', toneType: 'none' }).replace(/\s+/g, '').toUpperCase()
}

/** 商品名首字母映射（products 变化时重算，避免渲染期反复计算拼音） */
const letterMap = computed(() => {
  const map = new Map<number, { letter: string; initials: string }>()
  for (const p of products.value) {
    map.set(p.id, { letter: firstLetterOf(p.name), initials: initialsOf(p.name) })
  }
  return map
})

/** 纯字母关键词：同时按名称与拼音首字母匹配 */
const keywordLetters = computed(() => (/^[a-zA-Z]+$/.test(keyword.value.trim()) ? keyword.value.trim().toUpperCase() : ''))

/** 按分类 + 首字母 + 关键词过滤后的商品列表 */
const filteredProducts = computed(() => {
  return products.value.filter((p) => {
    if (activeCategory.value > 0 && p.category_id !== activeCategory.value) return false
    const meta = letterMap.value.get(p.id)
    if (activeLetter.value && meta?.letter !== activeLetter.value) return false
    const kw = keyword.value.trim().toLowerCase()
    if (kw) {
      const hitName = p.name.toLowerCase().includes(kw)
      const hitInitials = !!keywordLetters.value && !!meta?.initials.includes(keywordLetters.value)
      if (!hitName && !hitInitials) return false
    }
    return true
  })
})

/** 分类页签：全部 + 商品中出现的分类（按首次出现顺序） */
const categoryTabs = computed(() => {
  const tabs: Array<{ id: number; name: string }> = [{ id: 0, name: '全部' }]
  const seen = new Set<number>()
  for (const p of products.value) {
    if (!p.category_id || seen.has(p.category_id)) continue
    seen.add(p.category_id)
    tabs.push({ id: p.category_id, name: p.category_name || `分类#${p.category_id}` })
  }
  return tabs
})

// --- 小票（购物车） ---
interface CartRow {
  sku_id: number
  product_id: number
  product_name: string
  spec_name: string
  /** 当前成交价（改价后覆盖） */
  price: number
  /** 商品档案原价（改价展示对照） */
  original_price: number
  /** 是否发生过单品改价 */
  price_modified: boolean
  quantity: number
}
const cart = ref<CartRow[]>([])
// 默认到店：即买即走，不生成取货码、无需核销（仅收银台可用）
const deliveryType = ref<number>(DELIVERY_TYPE.STORE)
const contact = ref({ name: '', phone: '', address: '' })
const remark = ref('')
const checkingOut = ref(false)

const totalQuantity = computed(() => cart.value.reduce((sum, r) => sum + r.quantity, 0))
const totalAmount = computed(() => cart.value.reduce((sum, r) => sum + r.price * r.quantity, 0))

// --- 社区渠道配置（自提/配送开关；到店不受控恒可用） ---
const enableSelfPickup = ref(true)
const enableDelivery = ref(true)

/**
 * 拉取当前社区渠道配置（失败静默按双开渲染，后端下单仍有兜底校验）
 */
async function loadChannelConfig() {
  try {
    const res = await getCommunityProfile()
    if (res.data.code === 0 && res.data.data) {
      enableSelfPickup.value = res.data.data.enable_self_pickup ?? true
      enableDelivery.value = res.data.data.enable_delivery ?? true
    }
  } catch {
    // 配置拉取失败不阻塞收银，按默认开启渲染
  }
}

// --- 改价弹窗 ---
/** 改价弹窗展示状态 */
const showPriceModal = ref(false)
/** 正在改价的行 */
const editRow = ref<CartRow | null>(null)
/** 改价输入中的价格 */
const editPrice = ref(0)

/**
 * 打开改价弹窗
 * @param row 购物车行
 */
function openPriceModal(row: CartRow) {
  editRow.value = row
  editPrice.value = row.price
  showPriceModal.value = true
}

/**
 * 确认改价：仅对当前订单生效，恢复原价则取消改价标记
 */
function confirmEditPrice() {
  const row = editRow.value
  if (!row) return
  const next = Math.round((editPrice.value || 0) * 100) / 100
  if (next < 0.01) {
    message.warning('请输入有效的价格')
    return
  }
  row.price = next
  row.price_modified = next !== row.original_price
  message.success('已改价，仅本单生效')
  showPriceModal.value = false
  // 注意：不清空 editRow——弹窗离场过渡期间仍会渲染内容，提前置 null 会触发渲染错误中断视图更新
}

// --- 优惠：不优惠 / 抹零 / 整单改价 ---
const discountMode = ref<'none' | 'round' | 'whole'>('none')
const roundKind = ref<'fen' | 'jiao' | 'custom'>('fen')
const customDiscount = ref<number | null>(null)
const wholePrice = ref<number | null>(null)

/**
 * 手动优惠总额（抹零/整单改价折算，仅对本次订单生效）
 * 抹分=取整到角（去掉分位），抹角=取整到元；整单改价=商品小计-成交价；
 * 上限=商品额−券优惠（服务端按「手动优惠不得超券后应付」拒单，前置对齐避免结账被拒）
 */
const manualDiscount = computed(() => {
  const total = Math.round(totalAmount.value * 100) / 100
  const cap = Math.max(0, Math.round((total - couponDiscount.value) * 100) / 100)
  let raw = 0
  if (discountMode.value === 'round') {
    if (roundKind.value === 'fen') raw = Math.round((total - Math.floor(total * 10) / 10) * 100) / 100
    else if (roundKind.value === 'jiao') raw = total - Math.floor(total)
    else raw = Math.max(customDiscount.value ?? 0, 0)
  } else if (discountMode.value === 'whole') {
    const target = wholePrice.value ?? 0
    if (target > 0 && target < total) raw = Math.round((total - target) * 100) / 100
  }
  return Math.min(raw, cap)
})

// --- 会员与优惠券 ---
const member = ref<User | null>(null)
const showUserPicker = ref(false)
// 所选会员名下可用券（coupon_id 即下单接口的 coupon_id，服务端按模板匹配会员持券）
const couponId = ref<number | null>(null)

/** 券下拉项（携带门槛/优惠类型/优惠值，前端预估减免用；实际减免以后端结算为准） */
interface CouponOption {
  label: string
  value: number
  disabled?: boolean
  threshold: number
  discount_value: number
  type: number
}
const couponOptions = ref<CouponOption[]>([])

/**
 * 下拉选项：未满门槛的券置灰（下单会被后端拒绝，提前拦截避免收银失败）
 */
const couponSelectOptions = computed(() =>
  couponOptions.value.map((o) => ({
    label: o.label,
    value: o.value,
    disabled: o.disabled,
  }))
)

/**
 * 所选券的预估优惠（口径对齐后端 calculateDiscount）：
 * 现金券=min(面额, 商品额)；折扣券=商品额×(1-折扣率)；未满门槛按 0
 * 注意：券限品类时后端按券范围内商品计算，此处预估偏大，以服务端为准
 */
const couponDiscount = computed(() => {
  const c = couponOptions.value.find((o) => o.value === couponId.value)
  if (!c) return 0
  const total = Math.round(totalAmount.value * 100) / 100
  if (total < c.threshold) return 0
  if (c.type === 1) return Math.min(c.discount_value, total)
  if (c.type === 2) return Math.max(0, Math.min(total * (1 - c.discount_value), total))
  return 0
})

/**
 * 预计应付金额（商品小计 - 券优惠 - 手动优惠，最低 0）
 * 下限对齐服务端「实付 ≥ 0」口径：0 元全额优惠单由现金路径收款（付款码路径自行拦截）
 */
const payableAmount = computed(
  () => Math.max(0, Math.round((totalAmount.value - couponDiscount.value - manualDiscount.value) * 100) / 100)
)

/**
 * 选择会员后加载其名下未使用券，供代选
 * @param u 会员
 */
function onSelectMember(u: User) {
  if (member.value && member.value.id !== u.id) {
    couponId.value = null
  }
  member.value = u
  void loadCoupons(u.id)
}

/**
 * 清除会员：回到散客，券不可用
 */
function clearMember() {
  member.value = null
  couponId.value = null
  couponOptions.value = []
}

/**
 * 拉取会员未使用的券（status=1），拼装下拉项（未满当前小票门槛的置灰）
 * @param userId 会员 ID
 */
async function loadCoupons(userId: number) {
  couponOptions.value = []
  try {
    const res = await getCouponRecords({ user_id: userId, status: 1, page: 1, page_size: 50 })
    if (res.data.code !== 0) return
    couponOptions.value = (res.data.data.list || []).map((r) => {
      const below = totalAmount.value < (r.threshold ?? 0)
      return {
        label: `${r.coupon_name}（满${r.threshold}可用）${below ? ' · 未满门槛' : ''}`,
        value: r.coupon_id,
        disabled: below,
        threshold: r.threshold ?? 0,
        discount_value: r.discount_value ?? 0,
        type: r.type ?? 1,
      }
    })
  } catch {
    // 券加载失败不阻塞收银，仅不展示下拉
  }
}

// 小票商品变化时刷新券的门槛可用态（避免先选券后减商品导致门槛失真）
watch(totalAmount, () => {
  if (!couponOptions.value.length) return
  const total = Math.round(totalAmount.value * 100) / 100
  couponOptions.value = couponOptions.value.map((o) => {
    const below = total < o.threshold
    return {
      ...o,
      disabled: below,
      label: o.label.replace(/( · 未满门槛)?$/, below ? ' · 未满门槛' : ''),
    }
  })
  if (couponId.value != null) {
    const cur = couponOptions.value.find((o) => o.value === couponId.value)
    if (cur?.disabled) couponId.value = null
  }
})

/**
 * 点击商品：单规格直接加购；多规格拉详情选规格
 * @param p 商品
 */
async function onPickProduct(p: Product) {
  if (p.stock <= 0) {
    message.warning(`「${p.name}」已缺货`)
    return
  }
  productLoading.value = true
  try {
    const res = await getProductDetail(p.id)
    if (res.data.code !== 0) return
    const skus = (res.data.data.skus || []).filter((s) => s.status === 1)
    const sellable = skus.filter((s) => s.stock > 0)
    if (!sellable.length) {
      message.warning(`「${p.name}」暂无可售库存`)
      return
    }
    if (skus.length === 1) {
      addSkuToCart(skus[0], p.name)
    } else {
      pickedProductName = p.name
      skuCandidates.value = sellable
      showSkuModal.value = true
    }
  } finally {
    productLoading.value = false
  }
}

const showSkuModal = ref(false)
const skuCandidates = ref<Sku[]>([])
// 多规格弹窗当前对应的商品名（加入小票时回填行名）
let pickedProductName = ''

/**
 * 加入小票（同 SKU 合并行，数量 +1）
 * @param sku SKU
 * @param productName 商品名（弹窗路径由外层传入）
 */
function addSkuToCart(sku: Sku, productName?: string) {
  const existing = cart.value.find((r) => r.sku_id === sku.id)
  if (existing) {
    if (existing.quantity + 1 > sku.stock) {
      message.warning(`「${sku.spec_name || sku.sku_code}」库存不足`)
      return
    }
    existing.quantity++
  } else {
    const name = productName || pickedProductName || products.value.find((x) => x.id === sku.product_id)?.name || `商品#${sku.product_id}`
    cart.value.push({
      sku_id: sku.id,
      product_id: sku.product_id,
      product_name: name,
      spec_name: sku.spec_name || '',
      price: sku.price,
      original_price: sku.price,
      price_modified: false,
      quantity: 1,
    })
  }
  showSkuModal.value = false
  message.success('已加入小票')
}

/**
 * 调整数量（下限 1，删除用移除按钮）
 * @param skuId SKU ID
 * @param delta 增量
 */
function changeQty(skuId: number, delta: number) {
  const row = cart.value.find((r) => r.sku_id === skuId)
  if (!row) return
  row.quantity = Math.max(1, row.quantity + delta)
}

/**
 * 移除明细行
 * @param skuId SKU ID
 */
function removeRow(skuId: number) {
  cart.value = cart.value.filter((r) => r.sku_id !== skuId)
}

/**
 * 清空工作区（结账/挂单成功后调用；渠道恢复默认到店）
 */
function resetWorkspace() {
  cart.value = []
  couponId.value = null
  deliveryType.value = DELIVERY_TYPE.STORE
  contact.value = { name: '', phone: '', address: '' }
  remark.value = ''
  discountMode.value = 'none'
  customDiscount.value = null
  wholePrice.value = null
  activeCategory.value = 0
  activeLetter.value = ''
}

// --- 挂单 ---
const heldOrders = ref<PosHeldOrder[]>([])
const heldCount = ref(0)
const showHeldModal = ref(false)
const heldLoading = ref(false)

/**
 * 打开挂单列表并刷新计数
 */
async function openHeldList() {
  showHeldModal.value = true
  await refreshHeld()
}

/**
 * 拉取挂单列表（当前社区语境）
 */
async function refreshHeld() {
  heldLoading.value = true
  try {
    const res = await getPosHeldOrders()
    if (res.data.code === 0) {
      heldOrders.value = res.data.data.list || []
      heldCount.value = heldOrders.value.length
    }
  } finally {
    heldLoading.value = false
  }
}

/**
 * 挂单：暂存当前工作区（含会员与商品快照，改价随快照保存）
 */
function onHold() {
  if (!cart.value.length) return
  createPosHeldOrder({
    user_id: member.value?.id ?? 0,
    user_name: member.value ? member.value.nickname || member.value.phone : '',
    remark: remark.value.trim() || undefined,
    items: cart.value.map((r) => ({
      sku_id: r.sku_id,
      quantity: r.quantity,
      product_name: r.product_name,
      spec_name: r.spec_name,
      product_image: '',
      price: r.price,
    })),
  })
    .then(() => {
      message.success('已挂单')
      resetWorkspace()
      void refreshHeld()
    })
    .catch(() => {
      // 错误提示由请求拦截器统一弹出
    })
}

/**
 * 恢复挂单：回填会员与明细（挂单价格作为当前成交价），随后删除挂单记录
 * @param h 挂单
 */
function onRestore(h: PosHeldOrder) {
  const apply = () => {
    member.value = h.user_id ? ({ id: h.user_id, nickname: h.user_name, phone: '' } as User) : null
    couponOptions.value = []
    couponId.value = null
    if (member.value) void loadCoupons(member.value.id)
    remark.value = h.remark || ''
    cart.value = h.items.map((i) => ({
      sku_id: i.sku_id,
      product_id: 0,
      product_name: i.product_name,
      spec_name: i.spec_name || '',
      price: i.price,
      original_price: i.price,
      price_modified: false,
      quantity: i.quantity,
    }))
    deletePosHeldOrder(h.id)
      .then(() => {
        showHeldModal.value = false
        void refreshHeld()
      })
      .catch(() => {
        // 错误提示由请求拦截器统一弹出
      })
  }
  if (cart.value.length) {
    dialog.warning({
      title: '恢复挂单',
      content: '当前小票不为空，恢复挂单将覆盖当前工作区，是否继续？',
      positiveText: '覆盖并恢复',
      negativeText: '取消',
      onPositiveClick: apply,
    })
  } else {
    apply()
  }
}

/**
 * 删除挂单（顾客放弃）
 * @param h 挂单
 */
function onDeleteHeld(h: PosHeldOrder) {
  dialog.warning({
    title: '删除挂单',
    content: `确认删除该挂单（${h.item_count} 件商品）？删除后不可恢复。`,
    positiveText: '确认删除',
    negativeText: '取消',
    onPositiveClick: async () => {
      await deletePosHeldOrder(h.id)
      void refreshHeld()
    },
  })
}

// --- 结账 ---
const showPaidModal = ref(false)

/** 结账成功的小票数据（快照，供成功弹窗展示与打印；resetWorkspace 前组装） */
const receipt = ref<PaidReceipt | null>(null)

// 支付方式动态获取：结账区不再摆 radio（结账即收款），付款码入口取第一个非现金方式——
// 两渠道齐备返回 5「付款码支付」（渠道按扫码时的付款码前缀自动识别）、仅单渠道时对应占位值；
// 无可用付款码渠道时只显示现金按钮
const codePayMethod = ref<{ label: string; value: number } | null>(null)

/**
 * 拉取收银台可用支付方式（挂载时调用一次；服务端未开放的值会被下单接口拒绝）
 */
async function loadPayMethods() {
  codePayMethod.value = null
  try {
    const res = await listPosPayMethods()
    if (res.data.code !== 0) return
    const list = (res.data.data.list || []).map((m) => ({ label: m.label, value: m.pay_type }))
    codePayMethod.value = list.find((m) => m.value !== PAY_TYPE.CASH) ?? null
  } catch {
    // 拉取失败降级为仅现金，服务端渠道校验兜底
  }
}

/**
 * 渠道文案（小票展示）
 * @param type 配送方式
 */
function channelLabelOf(type: number): string {
  if (type === DELIVERY_TYPE.STORE) return '到店消费'
  if (type === DELIVERY_TYPE.DELIVERY) return '送货上门'
  return '社区自提'
}

/**
 * 组装结账小票快照（必须 resetWorkspace 前调用，购物车清空后取不到值）
 * @param orderNo 订单号
 * @param payAmount 实收金额
 * @param pickupCode 取货码（自提单）
 * @param payLabel 支付方式文案
 */
function buildReceipt(orderNo: string, payAmount: number, pickupCode: string | undefined, payLabel: string) {
  const now = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  receipt.value = {
    order_no: orderNo,
    pay_amount: payAmount,
    pickup_code: pickupCode || undefined,
    channel_label: channelLabelOf(deliveryType.value),
    pay_label: payLabel,
    member_label: member.value ? member.value.nickname || member.value.phone || `会员#${member.value.id}` : '散客',
    items: cart.value.map((r) => ({
      name: r.product_name,
      spec: r.spec_name,
      qty: r.quantity,
      subtotal: Math.round(r.price * r.quantity * 100) / 100,
    })),
    total_amount: Math.round(totalAmount.value * 100) / 100,
    coupon_discount: Math.round(couponDiscount.value * 100) / 100,
    manual_discount: Math.round(manualDiscount.value * 100) / 100,
    time: `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())} ${pad(now.getHours())}:${pad(now.getMinutes())}`,
  }
}

/** 下单请求体（付款码/现金两路共用） */
function buildOrderPayload(discount: number) {
  return {
    user_id: member.value?.id ?? 0,
    delivery_type: deliveryType.value,
    contact_name: contact.value.name.trim() || member.value?.nickname || '',
    contact_phone: contact.value.phone.trim() || member.value?.phone || '',
    contact_address: contact.value.address.trim(),
    remark: remark.value.trim() || undefined,
    coupon_id: couponId.value ?? 0,
    manual_discount: discount > 0 ? discount : undefined,
    items: cart.value.map((r) => ({
      sku_id: r.sku_id,
      quantity: r.quantity,
      price: r.price_modified ? r.price : undefined,
    })),
  }
}

/** 校验配送联系信息（配送单必填），不通过时提示并返回 false */
function validateContact(): boolean {
  if (deliveryType.value === DELIVERY_TYPE.DELIVERY &&
      (!contact.value.name.trim() || !contact.value.phone.trim() || !contact.value.address.trim())) {
    message.warning('配送订单请填写完整的联系人、电话与地址')
    return false
  }
  return true
}

/** 折算到分位的手动优惠 */
const roundedManualDiscount = computed(() => Math.round(manualDiscount.value * 100) / 100)

/**
 * 付款码收款（F2）：建待支付订单后直接弹扫码窗（无确认弹窗——金额已明示，
 * 渠道由顾客付款码前缀自动识别），扣款失败可在弹窗内改用现金；
 * 上次放弃扫码的同载荷遗留单走 pay-code-session 复用，不重复建待支付单
 */
async function onCodePay() {
  if (!cart.value.length || checkingOut.value) return
  if (showCodePayModal.value || cashConfirmOpen.value) return
  if (!validateContact()) return
  // 渠道不受理 0 元扣款：前置拦截，避免产生无法收款的订单（改用现金）
  if (payableAmount.value <= 0) {
    message.warning('付款码支付不支持 0 元订单，请改用现金收款')
    return
  }
  if (!codePayMethod.value) return
  checkingOut.value = true
  try {
    const payload = { ...buildOrderPayload(roundedManualDiscount.value), pay_type: codePayMethod.value.value }
    const key = JSON.stringify(payload)
    // 载荷未变且存在放弃扫码的遗留待支付单：复用原单继续收款（金额/明细一致，out_trade_no 不变）
    if (abandonedPending.value && abandonedPending.value.key === key) {
      const reused = await ensurePosCodePaySession(abandonedPending.value.orderId)
      if (reused.data.code !== 0) return
      const p = abandonedPending.value
      codePayOrderId.value = p.orderId
      codePayOrderNo.value = p.orderNo
      codePayPaymentNo.value = reused.data.data.payment_no || p.paymentNo
      codePayAmount.value = p.amount
      codePayPickupCode.value = p.pickupCode
      showCodePayModal.value = true
      return
    }
    const res = await createPosOrder(payload)
    if (res.data.code !== 0) return
    codePayOrderId.value = res.data.data.id
    codePayOrderNo.value = res.data.data.order_no
    codePayPaymentNo.value = res.data.data.payment_no || ''
    codePayAmount.value = res.data.data.pay_amount
    codePayPickupCode.value = res.data.data.pickup_code || ''
    abandonedPending.value = {
      orderId: codePayOrderId.value,
      orderNo: codePayOrderNo.value,
      paymentNo: codePayPaymentNo.value,
      amount: codePayAmount.value,
      pickupCode: codePayPickupCode.value,
      key,
    }
    showCodePayModal.value = true
  } catch {
    // 错误提示由请求拦截器统一弹出
  } finally {
    checkingOut.value = false
  }
}

/**
 * 弹窗确认按钮聚焦：渲染完成后原生回车即确认（不依赖鼠标）。
 * 定时器随组件卸载清理；新弹窗打开会取消上一个未触发的聚焦，多窗叠开时焦点归最近一窗
 * @param selector 确认按钮选择器
 */
function focusDialogConfirm(selector = '.n-dialog__action button:last-of-type') {
  if (dialogFocusTimer) clearTimeout(dialogFocusTimer)
  dialogFocusTimer = setTimeout(() => {
    const btn = document.querySelector(selector) as HTMLButtonElement | null
    btn?.focus()
  }, 150)
}

/**
 * 现金收款（F3）：确认后下单即收款完结、出小票。
 * 确认弹窗打开后自动聚焦确认按钮，回车即确认（不依赖鼠标）
 */
function onCashPay() {
  if (!cart.value.length || checkingOut.value) return
  if (showCodePayModal.value || cashConfirmOpen.value) return
  if (!validateContact()) return
  const discount = roundedManualDiscount.value
  const parts = [`商品￥${formatAmount(totalAmount.value)}`]
  if (couponDiscount.value > 0) parts.push(`券优惠￥${formatAmount(couponDiscount.value)}`)
  if (discount > 0) parts.push(`优惠￥${formatAmount(discount)}`)
  // 确认窗打开期间屏蔽 F2/F3/再次进入（2026-10-25 审查 P1：快捷键可叠第二个确认窗重复建单）
  cashConfirmOpen.value = true
  dialog.warning({
    title: '现金收款',
    content: `确认已收现金 ￥${formatAmount(payableAmount.value)}（${parts.join('，')}），为${member.value ? `会员 ${member.value.nickname || member.value.phone}` : '散客'}结账？（回车确认）`,
    positiveText: '确认收款',
    negativeText: '取消',
    onPositiveClick: async () => {
      // 二重门禁：确认时点若已有收款请求在途（叠窗遗留/重复点击），拒绝再次提交
      if (checkingOut.value) return false
      checkingOut.value = true
      try {
        const res = await createPosOrder({ ...buildOrderPayload(discount), pay_type: PAY_TYPE.CASH })
        if (res.data.code === 0) {
          // 组装小票快照（resetWorkspace 会清空购物车，必须先取值）
          buildReceipt(res.data.data.order_no, res.data.data.pay_amount, res.data.data.pickup_code, '现金')
          showPaidModal.value = true
          resetWorkspace()
        }
      } catch {
        // 错误提示由请求拦截器统一弹出
      } finally {
        checkingOut.value = false
      }
    },
    // 覆盖所有关闭路径（确认/取消/Esc），关闭动画结束即释放快捷键守卫
    onAfterLeave: () => {
      cashConfirmOpen.value = false
    },
  })
  // 回车确认：弹窗渲染后聚焦确认按钮，原生回车触发其 click
  focusDialogConfirm()
}

/**
 * F2/F3 全局结账快捷键：F2 付款码收款、F3 现金收款（收款弹窗/确认窗打开时忽略，避免重复建单）
 * @param e 键盘事件
 */
function onGlobalKeydown(e: KeyboardEvent) {
  if (e.key !== 'F2' && e.key !== 'F3') return
  if (showCodePayModal.value || showPaidModal.value || cashConfirmOpen.value) return
  if (!cart.value.length) return
  e.preventDefault()
  if (e.key === 'F2') void onCodePay()
  else onCashPay()
}

// --- 付款码收款（共用 CodePayDialog 的凭据状态） ---
const showCodePayModal = ref(false)
const codePayOrderNo = ref('')
const codePayOrderId = ref(0)
const codePayPaymentNo = ref('')
const codePayAmount = ref(0)
/** 下单响应缓存的取货码（自提单收款成功后回填小票） */
const codePayPickupCode = ref('')
/** 现金确认窗打开标记：F2/F3/输入框回车在窗打开期间一律忽略，防叠窗重复建单 */
const cashConfirmOpen = ref(false)
/** 弹窗确认按钮聚焦定时器（组件卸载清理） */
let dialogFocusTimer: ReturnType<typeof setTimeout> | null = null
/**
 * 放弃扫码的遗留待支付单快照：再次 F2 且载荷与 key 一致时复用原单
 * （pay-code-session 续收），载荷有变才新建，防同一购物车连按 F2 叠多笔待支付单；
 * 收款成功即清除
 */
const abandonedPending = ref<{
  orderId: number
  orderNo: string
  paymentNo: string
  amount: number
  pickupCode: string
  key: string
} | null>(null)

/**
 * 付款码收款成功收尾：关扫码窗 → 出小票 → 清工作区
 * @param payload 收款结果（payLabel 渠道文案；pickupCode 自提单取货码）
 */
function onCodePaid(payload: { payLabel: string; pickupCode?: string }) {
  showCodePayModal.value = false
  abandonedPending.value = null
  message.success('收款成功')
  buildReceipt(codePayOrderNo.value, codePayAmount.value, payload.pickupCode, payload.payLabel)
  showPaidModal.value = true
  resetWorkspace()
}

/**
 * 收银员放弃扫码：订单保留待支付，提示去订单查询处理（可继续收款或取消）
 */
function onCodeAbandoned() {
  message.info(`订单 ${codePayOrderNo.value} 已创建未收款，可在「订单查询」继续收款`)
}

/**
 * 消费订单查询写入的待引用清单：按订单明细快照回填购物车（追加模式），
 * 价格取订单档案价（original_price），改价与优惠不带入（"再来一单"按当前档案价，
 * 后端下单仍按服务端实时价结算）；软删/缺货商品由下单接口兜底拦截
 */
function consumePendingImport() {
  const items = posStore.consumePendingImport()
  if (!items.length) return
  for (const it of items) {
    const existing = cart.value.find((r) => r.sku_id === it.sku_id)
    if (existing) {
      existing.quantity += it.quantity
      continue
    }
    cart.value.push({
      sku_id: it.sku_id,
      product_id: 0,
      product_name: it.product_name,
      spec_name: it.spec_name || '',
      price: it.price,
      original_price: it.price,
      price_modified: false,
      quantity: it.quantity,
    })
  }
  message.success(`已引用 ${items.length} 项商品到小票，可继续加减后结账`)
}

onMounted(() => {
  void searchProducts()
  void refreshHeld()
  void loadChannelConfig()
  void loadPayMethods()
  consumePendingImport()
  window.addEventListener('keydown', onGlobalKeydown)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onGlobalKeydown)
  if (dialogFocusTimer) {
    clearTimeout(dialogFocusTimer)
    dialogFocusTimer = null
  }
})
</script>

<style lang="scss" scoped>
.order-tab {
  height: 100%;
  display: flex;
  gap: 14px;
  padding: 14px;
  box-sizing: border-box;
  align-items: stretch;
}
.pane-left {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}
.search-bar {
  flex-shrink: 0;
  margin-bottom: 10px;
  .search {
    margin-bottom: 8px;
  }
}
// A-Z 首字母筛选条：横向可滚动，选中绿底白字
.letter-bar {
  display: flex;
  gap: 4px;
  overflow-x: auto;
  padding-bottom: 2px;
  .letter {
    flex-shrink: 0;
    min-width: 26px;
    text-align: center;
    padding: 3px 6px;
    font-size: 13px;
    color: #666;
    border-radius: 6px;
    cursor: pointer;
    user-select: none;
    transition: background 0.15s, color 0.15s;
    &:hover {
      background: #f0faf4;
      color: #07c160;
    }
    &.active {
      background: #07c160;
      color: #fff;
      font-weight: 600;
    }
  }
}
.product-area {
  flex: 1;
  min-height: 0;
  display: flex;
  gap: 10px;
}
// 分类竖栏（点单机式）：选中态绿色高亮
.category-bar {
  width: 96px;
  flex-shrink: 0;
  overflow-y: auto;
  background: #fff;
  border-radius: 10px;
  padding: 6px;
  .cat-item {
    padding: 13px 8px;
    font-size: 14px;
    color: #444;
    text-align: center;
    border-radius: 8px;
    cursor: pointer;
    transition: background 0.15s, color 0.15s;
    &:hover {
      background: #f5f6f8;
    }
    &.active {
      background: #e8f8ef;
      color: #07c160;
      font-weight: 700;
    }
  }
}
.product-grid-wrap {
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
}
.product-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 12px;
}
.product-card {
  position: relative;
  background: #fff;
  border-radius: 10px;
  padding: 10px;
  cursor: pointer;
  border: 1px solid transparent;
  transition: box-shadow 0.2s, border-color 0.2s;
  &:hover {
    border-color: #07c160;
    box-shadow: 0 2px 10px rgba(7, 193, 96, 0.14);
  }
  &.disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .cover {
    width: 100%;
    aspect-ratio: 1;
    border-radius: 8px;
    overflow: hidden;
    display: block;
    background: #f5f6f8;
    // 图片铺满容器并按 C 端口径裁剪（方形 + cover），避免原图比例导致显示不全
    :deep(img) {
      width: 100%;
      height: 100%;
      object-fit: cover;
      display: block;
    }
  }
  .p-name {
    font-size: 15px;
    font-weight: 600;
    margin-top: 8px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .p-meta {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-top: 4px;
    .p-price {
      color: #d03050;
      font-weight: 700;
      font-size: 16px;
    }
    .p-stock {
      font-size: 12px;
      color: #b0b0b0;
    }
  }
  .spec-badge {
    position: absolute;
    top: 14px;
    right: 14px;
  }
}
.pane-right {
  width: 400px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
  overflow-y: auto;
}
.member-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  .coupon-select {
    min-width: 150px;
    flex: 1;
  }
}
.cart-list {
  flex: 1;
  min-height: 120px;
  overflow-y: auto;
  background: #fff;
  border-radius: 10px;
  padding: 6px 10px;
}
.cart-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 0;
  border-bottom: 1px dashed #eee;
  &:last-child {
    border-bottom: none;
  }
  .cart-info {
    flex: 1;
    min-width: 0;
  }
  .cart-name {
    font-size: 15px;
    font-weight: 500;
    .spec {
      font-size: 12px;
      color: #999;
      margin-left: 4px;
    }
  }
  .cart-price {
    font-size: 15px;
    color: #d03050;
    cursor: pointer;
    margin-top: 2px;
    .origin {
      font-size: 12px;
      color: #bbb;
      text-decoration: line-through;
      margin-left: 4px;
    }
    .edit-hint {
      color: #c2c2c2;
    }
  }
  .stepper {
    display: flex;
    align-items: center;
    gap: 6px;
    .qty {
      min-width: 20px;
      text-align: center;
      font-size: 16px;
      font-weight: 600;
    }
  }
  .cart-subtotal {
    min-width: 64px;
    text-align: right;
    font-size: 15px;
    font-weight: 600;
  }
}
.delivery-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  .contact-input {
    width: 110px;
  }
  .address-input {
    width: 150px;
  }
}
.remark-input {
  flex-shrink: 0;
}
.discount-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  .discount-detail {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .discount-label {
    font-size: 14px;
    color: #666;
  }
  .custom-discount {
    width: 130px;
  }
}
.summary {
  flex-shrink: 0;
  background: #fff;
  border-radius: 10px;
  padding: 10px 14px;
  .summary-row {
    display: flex;
    justify-content: space-between;
    font-size: 14px;
    padding: 3px 0;
    &.muted {
      color: #999;
      font-size: 13px;
    }
    .discount-amount {
      color: #d03050;
      font-weight: 600;
    }
    &.payable {
      align-items: baseline;
      padding-top: 6px;
      font-size: 15px;
      font-weight: 600;
      .amount {
        font-size: 26px;
        font-weight: 700;
        color: #07c160;
      }
    }
  }
  .pay-method-row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 0 0;
    .pay-method-label {
      font-size: 14px;
      color: #666;
    }
  }
  .summary-hint {
    font-size: 12px;
    color: #c0c0c0;
    padding-top: 4px;
  }
}
.actions {
  flex-shrink: 0;
  .checkout-btn {
    min-width: 220px;
    height: 52px;
    font-size: 18px;
    font-weight: 700;
  }
}
.empty {
  margin-top: 60px;
}

// --- 弹窗内样式 ---
.sku-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.sku-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 14px 16px;
  border-radius: 10px;
  background: #f7f8fa;
  cursor: pointer;
  transition: background 0.15s;
  &:hover {
    background: #e8f8ef;
  }
  &.disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .sku-name {
    font-size: 15px;
    font-weight: 500;
  }
  .sku-meta {
    font-size: 14px;
    color: #d03050;
  }
}
.held-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.held-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border-radius: 10px;
  background: #f7f8fa;
}
.held-main {
  flex: 1;
  min-width: 0;
}
.held-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  .held-amount {
    color: #d03050;
    font-weight: 600;
  }
}
.held-sub {
  font-size: 13px;
  color: #666;
  margin-top: 4px;
  &.muted {
    color: #bbb;
  }
}
// 付款码扫码弹窗：金额突出展示 + 状态提示（失败红色），扫码枪连续作业友好
.codepay-wrap {
  display: flex;
  flex-direction: column;
  gap: 12px;
  text-align: center;

  .codepay-amount {
    font-size: 34px;
    font-weight: 700;
    line-height: 1.2;
  }

  .codepay-order {
    font-size: 12px;
  }

  .codepay-tip {
    font-size: 13px;
    color: #999;

    &.error {
      color: #d03050;
      font-weight: 600;
    }
  }
}

.paid-wrap {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}
.paid-amount {
  font-size: 32px;
  font-weight: 700;
  color: #07c160;
}
.paid-receipt {
  width: 260px;
  background: #fff;
  border: 1px dashed #ddd;
  border-radius: 8px;
  padding: 14px 16px;
  font-size: 13px;
  .r-title {
    text-align: center;
    font-size: 16px;
    font-weight: 700;
    margin-bottom: 10px;
  }
  .r-line {
    margin: 4px 0;
    &.center {
      text-align: center;
    }
    &.muted {
      color: #999;
    }
    &.strong {
      font-weight: 700;
      color: #d03050;
    }
  }
  .r-divider {
    margin: 8px 0;
    color: #bbb;
    text-align: center;
  }
  .r-row {
    display: flex;
    justify-content: space-between;
    margin: 3px 0;
    &.total {
      font-weight: 700;
      font-size: 14px;
      margin-top: 6px;
      color: #07c160;
    }
  }
}
.print-btn {
  max-width: 260px;
}
.price-modal-tip {
  color: #999;
  font-size: 13px;
  margin-bottom: 10px;
}
</style>
