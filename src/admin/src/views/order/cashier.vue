<template>
  <n-card title="收银台" class="cashier-card">
    <div class="cashier-layout">
      <!-- 左侧：商品选择 -->
      <div class="pane pane-left">
        <div class="pane-title">选择商品</div>
        <n-input
          v-model:value="keyword"
          placeholder="按商品名称搜索"
          clearable
          class="search"
        >
          <template #prefix>
            <n-icon><SearchOutline /></n-icon>
          </template>
        </n-input>
        <n-spin :show="productLoading" class="product-list-wrap">
          <n-empty v-if="!products.length && !productLoading" description="搜索并点击商品加入小票" class="empty" />
          <div v-else class="product-list">
            <div v-for="p in products" :key="p.id" class="product-row" @click="onPickProduct(p)">
              <n-image
                :src="p.cover_image"
                object-fit="cover"
                class="cover"
                :img-props="{ referrerpolicy: 'no-referrer' }"
              />
              <div class="info">
                <div class="name">{{ p.name }}</div>
                <div class="meta">
                  <span>￥{{ (p.min_price ?? p.price).toFixed(2) }}</span>
                  <span class="stock">库存 {{ p.stock }}</span>
                  <n-tag v-if="p.is_multi_spec" size="tiny" :bordered="false">多规格</n-tag>
                </div>
              </div>
              <n-button size="tiny" type="primary" quaternary>加入</n-button>
            </div>
          </div>
        </n-spin>
      </div>

      <!-- 右侧：当前小票 -->
      <div class="pane pane-right">
        <!-- 会员 -->
        <div class="member-bar">
          <span class="pane-title">顾客</span>
          <n-tag v-if="member" type="primary" size="medium" closable @close="clearMember">
            {{ member.nickname || member.phone }}
          </n-tag>
          <n-tag v-else type="default" size="medium">散客</n-tag>
          <n-button size="small" @click="showUserPicker = true">
            {{ member ? '更换会员' : '选会员' }}
          </n-button>
          <n-select
            v-if="couponOptions.length"
            v-model:value="couponId"
            :options="couponOptions"
            placeholder="使用优惠券"
            clearable
            size="small"
            class="coupon-select"
          />
        </div>

        <!-- 商品明细 -->
        <n-empty v-if="!cart.length" description="小票为空" class="empty" />
        <div v-else class="cart-list">
          <div v-for="row in cart" :key="row.sku_id" class="cart-row">
            <div class="cart-info">
              <div class="cart-name">
                {{ row.product_name }}
                <span v-if="row.spec_name" class="spec">{{ row.spec_name }}</span>
              </div>
              <div class="cart-price">￥{{ row.price.toFixed(2) }}</div>
            </div>
            <div class="stepper">
              <n-button size="tiny" circle @click="changeQty(row.sku_id, -1)">-</n-button>
              <span class="qty">{{ row.quantity }}</span>
              <n-button size="tiny" circle @click="changeQty(row.sku_id, 1)">+</n-button>
            </div>
            <div class="cart-subtotal">￥{{ (row.price * row.quantity).toFixed(2) }}</div>
            <n-button size="tiny" quaternary type="error" @click="removeRow(row.sku_id)">删除</n-button>
          </div>
        </div>

        <!-- 配送信息 -->
        <div class="delivery-bar">
          <n-radio-group v-model:value="deliveryType" size="small">
            <n-radio-button :value="DELIVERY_TYPE.SELF_PICKUP">自提</n-radio-button>
            <n-radio-button :value="DELIVERY_TYPE.DELIVERY">配送</n-radio-button>
          </n-radio-group>
          <template v-if="deliveryType === DELIVERY_TYPE.DELIVERY">
            <n-input v-model:value="contact.name" placeholder="联系人" size="small" class="contact-input" />
            <n-input v-model:value="contact.phone" placeholder="电话" size="small" class="contact-input" />
            <n-input v-model:value="contact.address" placeholder="收货地址" size="small" class="address-input" />
          </template>
        </div>

        <!-- 合计与操作 -->
        <div class="summary">
          <div class="summary-row">
            <span>商品 {{ totalQuantity }} 件</span>
            <span class="amount">￥{{ totalAmount.toFixed(2) }}</span>
          </div>
          <div class="pay-method-row">
            <span class="pay-method-label">支付方式</span>
            <n-radio-group v-model:value="payType" size="small">
              <n-radio v-for="m in PAY_METHODS" :key="m.value" :value="m.value">{{ m.label }}</n-radio>
            </n-radio-group>
          </div>
          <div class="summary-hint">配送费与优惠券/满减以下单结果为准</div>
        </div>
        <n-space justify="end" :size="12" class="actions">
          <n-button :disabled="!cart.length" @click="openHeldList">挂单({{ heldCount }})</n-button>
          <n-button type="warning" :disabled="!cart.length" @click="onHold">挂单</n-button>
          <n-button type="primary" size="large" :loading="checkingOut" :disabled="!cart.length" @click="onCheckout">
            {{ payMethodLabel }}结账 ￥{{ totalAmount.toFixed(2) }}
          </n-button>
        </n-space>
      </div>
    </div>
  </n-card>

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
        <div class="sku-meta">￥{{ sku.price.toFixed(2) }}（库存 {{ sku.stock }}）</div>
      </div>
    </div>
  </n-modal>

  <!-- 挂单列表弹窗 -->
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
              <span class="held-amount">￥{{ h.total_amount.toFixed(2) }}</span>
            </div>
            <div class="held-sub">
              {{ h.items.map((i) => `${i.product_name}${i.spec_name ? `(${i.spec_name})` : ''}x${i.quantity}`).join('、') }}
            </div>
            <div class="held-sub muted">挂单人 {{ h.operator_name || '-' }} · {{ h.created_at }}</div>
          </div>
          <n-space :size="8">
            <n-button size="small" type="primary" @click="onRestore(h)">恢复</n-button>
            <n-button size="small" type="error" quaternary @click="onDeleteHeld(h)">删除</n-button>
          </n-space>
        </div>
      </div>
    </n-spin>
  </n-modal>

  <!-- 结账成功弹窗 -->
  <n-modal
    :show="showPaidModal"
    preset="card"
    title="收银成功"
    style="width: 420px; max-width: 92vw"
    @update:show="(v: boolean) => (showPaidModal = v)"
  >
    <n-space vertical :size="10" v-if="lastPaid">
      <div class="paid-amount">￥{{ lastPaid.pay_amount.toFixed(2) }}</div>
      <div>订单号：{{ lastPaid.order_no }}</div>
      <div v-if="lastPaid.pickup_code" class="pickup-code">取货码：{{ lastPaid.pickup_code }}</div>
      <n-text depth="3">订单已按「收银台」来源记录，可在订单列表按来源筛选查看</n-text>
    </n-space>
  </n-modal>

  <!-- 会员选择（单选） -->
  <user-picker v-model:show="showUserPicker" title="选择会员" @select="onSelectMember" />
</template>

<script setup lang="ts">
// 页面组件名与路由 name 保持一致，供 keep-alive include 按组件名匹配
defineOptions({ name: 'PosCashier' })
import { computed, onMounted, ref, watch } from 'vue'
import { useDialog, useMessage } from 'naive-ui'
import { SearchOutline } from '@vicons/ionicons5'
import { createPosHeldOrder, createPosOrder, deletePosHeldOrder, getPosHeldOrders } from '@/api/order'
import { getCouponRecords } from '@/api/marketing'
import { getProductDetail, getProductList } from '@/api/product'
import type { PosHeldOrder, Product, Sku, User } from '@/api/types'
import UserPicker from '@/components/UserPicker.vue'
import { DELIVERY_TYPE } from '@/constants/order'

/**
 * 收银台页面（G 后台代客下单，决策点⑥⑨）
 * 选会员（可散客）→ 搜索加商品 → 选择支付方式结账（当前仅现金，后续付款码渠道扩展）；支持挂单暂存/恢复/删除。
 * 商品实时价格与优惠（券/满减/免配送费）均由服务端下单时计算，页面金额仅作参考。
 */

const dialog = useDialog()
const message = useMessage()

// --- 会员 ---
const member = ref<User | null>(null)
const showUserPicker = ref(false)
// 所选会员名下可用券（coupon_id 即下单接口的 coupon_id，服务端按模板匹配会员持券）
const couponId = ref<number | null>(null)
const couponOptions = ref<Array<{ label: string; value: number }>>([])

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
 * 拉取会员未使用的券（status=1），拼装下拉项
 * @param userId 会员 ID
 */
async function loadCoupons(userId: number) {
  couponOptions.value = []
  try {
    const res = await getCouponRecords({ user_id: userId, status: 1, page: 1, page_size: 50 })
    if (res.data.code !== 0) return
    couponOptions.value = (res.data.data.list || []).map((r) => ({
      label: `${r.coupon_name}（满${r.threshold}可用）`,
      value: r.coupon_id,
    }))
  } catch {
    // 券加载失败不阻塞收银，仅不展示下拉
  }
}

// --- 商品搜索 ---
const keyword = ref('')
const products = ref<Product[]>([])
const productLoading = ref(false)
let searchTimer: ReturnType<typeof setTimeout> | null = null

watch(keyword, () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    searchTimer = null
    void searchProducts()
  }, 300)
})

/**
 * 搜索本社区在售商品（status=1）
 */
async function searchProducts() {
  productLoading.value = true
  try {
    const res = await getProductList({ page: 1, page_size: 20, status: 1, keyword: keyword.value.trim() || undefined })
    if (res.data.code === 0) {
      products.value = res.data.data.list || []
    }
  } finally {
    productLoading.value = false
  }
}

// --- 小票（购物车） ---
interface CartRow {
  sku_id: number
  product_id: number
  product_name: string
  spec_name: string
  price: number
  quantity: number
}
const cart = ref<CartRow[]>([])
const deliveryType = ref<number>(DELIVERY_TYPE.SELF_PICKUP)
const contact = ref({ name: '', phone: '', address: '' })
const checkingOut = ref(false)

const totalQuantity = computed(() => cart.value.reduce((sum, r) => sum + r.quantity, 0))
const totalAmount = computed(() => cart.value.reduce((sum, r) => sum + r.price * r.quantity, 0))

/**
 * 点击商品：单规格直接加购；多规格拉详情选规格
 * @param p 商品
 */
async function onPickProduct(p: Product) {
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
 * 清空工作区（结账/挂单成功后调用）
 */
function resetWorkspace() {
  cart.value = []
  couponId.value = null
  deliveryType.value = DELIVERY_TYPE.SELF_PICKUP
  contact.value = { name: '', phone: '', address: '' }
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
 * 挂单：暂存当前工作区（含会员与商品快照）
 */
function onHold() {
  if (!cart.value.length) return
  createPosHeldOrder({
    user_id: member.value?.id ?? 0,
    user_name: member.value ? member.value.nickname || member.value.phone : '',
    items: cart.value.map((r) => ({
      sku_id: r.sku_id,
      quantity: r.quantity,
      product_name: r.product_name,
      spec_name: r.spec_name,
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
 * 恢复挂单：回填会员与明细，随后删除挂单记录
 * @param h 挂单
 */
function onRestore(h: PosHeldOrder) {
  const apply = () => {
    member.value = h.user_id ? ({ id: h.user_id, nickname: h.user_name, phone: '' } as User) : null
    couponOptions.value = []
    couponId.value = null
    if (member.value) void loadCoupons(member.value.id)
    cart.value = h.items.map((i) => ({
      sku_id: i.sku_id,
      product_id: 0,
      product_name: i.product_name,
      spec_name: i.spec_name || '',
      price: i.price,
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
const lastPaid = ref<{ order_no: string; pay_amount: number; pickup_code?: string } | null>(null)

// 支付方式选项（pay_type：4=现金；后续微信/支付宝付款码渠道在此追加 5/6，服务端未开放的值会被拒绝）
const PAY_METHODS = [{ label: '现金', value: 4 }]
const payType = ref(PAY_METHODS[0]!.value)
const payMethodLabel = computed(() => PAY_METHODS.find((m) => m.value === payType.value)?.label ?? '现金')

/**
 * 结账：代客下单（source=收银台）+ 按所选支付方式当场结清（当前仅现金）
 */
function onCheckout() {
  if (!cart.value.length) return
  if (deliveryType.value === DELIVERY_TYPE.DELIVERY &&
      (!contact.value.name.trim() || !contact.value.phone.trim() || !contact.value.address.trim())) {
    message.warning('配送订单请填写完整的联系人、电话与地址')
    return
  }
  dialog.warning({
    title: `${payMethodLabel.value}结账`,
    content: `确认已收${payMethodLabel.value} ￥${totalAmount.value.toFixed(2)}，为${member.value ? `会员 ${member.value.nickname || member.value.phone}` : '散客'}结账？`,
    positiveText: '确认收款',
    negativeText: '取消',
    onPositiveClick: async () => {
      checkingOut.value = true
      try {
        const res = await createPosOrder({
          user_id: member.value?.id ?? 0,
          delivery_type: deliveryType.value,
          contact_name: contact.value.name.trim() || member.value?.nickname || '',
          contact_phone: contact.value.phone.trim() || member.value?.phone || '',
          contact_address: contact.value.address.trim(),
          coupon_id: couponId.value ?? 0,
          pay_type: payType.value,
          items: cart.value.map((r) => ({ sku_id: r.sku_id, quantity: r.quantity })),
        })
        if (res.data.code === 0) {
          lastPaid.value = res.data.data
          showPaidModal.value = true
          resetWorkspace()
        }
      } catch {
        // 错误提示由请求拦截器统一弹出
      } finally {
        checkingOut.value = false
      }
    },
  })
}

onMounted(() => {
  void searchProducts()
  void refreshHeld()
})
</script>

<style lang="scss" scoped>
.cashier-card {
  min-height: calc(100vh - 120px);
}

.cashier-layout {
  display: flex;
  gap: 20px;
  align-items: flex-start;

  @media (max-width: 900px) {
    flex-direction: column;
  }
}

.pane-title {
  font-weight: 600;
  font-size: 14px;
  margin-bottom: 10px;
}

.pane-left {
  flex: 1;
  min-width: 0;
  width: 100%;

  .search {
    margin-bottom: 10px;
  }
}

.product-list-wrap {
  min-height: 200px;
}

.product-list {
  max-height: 62vh;
  overflow-y: auto;
  border: 1px solid #efeff5;
  border-radius: 6px;
}

.product-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  border-bottom: 1px solid #f3f3f7;
  cursor: pointer;
  transition: background 0.15s;

  &:last-child {
    border-bottom: none;
  }

  &:hover {
    background: #f7f9fa;
  }

  .cover {
    width: 40px;
    height: 40px;
    border-radius: 4px;
    flex-shrink: 0;
  }

  .info {
    flex: 1;
    min-width: 0;

    .name {
      font-size: 14px;
      color: #333;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .meta {
      display: flex;
      align-items: center;
      gap: 10px;
      font-size: 12px;
      color: #888;
      margin-top: 2px;

      .stock {
        color: #b0b0b0;
      }
    }
  }
}

.pane-right {
  width: 400px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;

  @media (max-width: 900px) {
    width: 100%;
  }
}

.member-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 12px;

  .pane-title {
    margin-bottom: 0;
  }

  .coupon-select {
    width: 180px;
  }
}

.cart-list {
  flex: 1;
  max-height: 40vh;
  overflow-y: auto;
  border: 1px solid #efeff5;
  border-radius: 6px;
  margin-bottom: 12px;
}

.cart-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  border-bottom: 1px solid #f3f3f7;

  &:last-child {
    border-bottom: none;
  }

  .cart-info {
    flex: 1;
    min-width: 0;

    .cart-name {
      font-size: 14px;
      color: #333;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;

      .spec {
        font-size: 12px;
        color: #999;
        margin-left: 4px;
      }
    }

    .cart-price {
      font-size: 12px;
      color: #888;
    }
  }

  .stepper {
    display: flex;
    align-items: center;
    gap: 6px;

    .qty {
      min-width: 22px;
      text-align: center;
      font-variant-numeric: tabular-nums;
    }
  }

  .cart-subtotal {
    width: 76px;
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
}

.delivery-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 12px;

  .contact-input {
    width: 96px;
  }

  .address-input {
    flex: 1;
    min-width: 140px;
  }
}

.summary {
  border-top: 1px dashed #e5e5eb;
  padding-top: 10px;
  margin-bottom: 12px;

  .summary-row {
    display: flex;
    justify-content: space-between;
    align-items: center;

    .amount {
      font-size: 22px;
      font-weight: 700;
      color: #d03050;
      font-variant-numeric: tabular-nums;
    }
  }

  .pay-method-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-top: 8px;

    .pay-method-label {
      font-size: 13px;
      color: #666;
    }
  }

  .summary-hint {
    font-size: 12px;
    color: #999;
    margin-top: 4px;
  }
}

.actions {
  :deep(.n-button--large-type-primary) {
    min-width: 180px;
  }
}

.sku-list {
  display: flex;
  flex-direction: column;
}

.sku-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 14px;
  border-bottom: 1px solid #f3f3f7;
  cursor: pointer;

  &:last-child {
    border-bottom: none;
  }

  &:hover {
    background: #f7f9fa;
  }

  &.disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .sku-meta {
    font-size: 12px;
    color: #888;
  }
}

.held-list {
  display: flex;
  flex-direction: column;
  max-height: 56vh;
  overflow-y: auto;
}

.held-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 10px 4px;
  border-bottom: 1px solid #f3f3f7;

  &:last-child {
    border-bottom: none;
  }

  .held-main {
    flex: 1;
    min-width: 0;
  }

  .held-title {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 14px;

    .held-amount {
      color: #d03050;
      font-weight: 600;
      font-variant-numeric: tabular-nums;
    }
  }

  .held-sub {
    font-size: 12px;
    color: #666;
    margin-top: 4px;
    overflow: hidden;
    text-overflow: ellipsis;

    &.muted {
      color: #b0b0b0;
    }
  }
}

.paid-amount {
  font-size: 28px;
  font-weight: 700;
  color: #18a058;
  text-align: center;
}

.pickup-code {
  font-size: 18px;
  font-weight: 600;
  letter-spacing: 2px;
}

.empty {
  padding: 30px 0;
}
</style>
