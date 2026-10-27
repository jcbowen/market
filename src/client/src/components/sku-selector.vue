<template>
  <u-popup :show="show" mode="bottom" @close="handleClose">
    <view class="sku-selector">
      <!-- 头部：商品图 / 名称 / 价格 / 库存 -->
      <view class="sku-header">
        <image class="sku-image" :src="activeImage" mode="aspectFill" />
        <view class="sku-info">
          <text class="sku-name">{{ innerProduct.name }}</text>
          <text class="sku-price">{{ formatPrice(activePrice) }}</text>
          <text class="sku-stock">库存 {{ activeStock }}</text>
        </view>
        <u-icon name="close" size="20" color="#999999" @click="handleClose" />
      </view>

      <!-- 规格选择区 -->
      <view class="sku-section">
        <text class="section-title">规格</text>
        <view v-if="loading" class="sku-loading">规格加载中...</view>
        <template v-else>
          <!-- 多规格：按规格树渲染选择器（与商品详情页一致） -->
          <view v-for="spec in innerProduct.specs" :key="spec.id" class="spec-group">
            <text class="spec-group-name">{{ spec.name }}</text>
            <view class="sku-options">
              <text
                v-for="value in spec.values"
                :key="value.id"
                class="sku-option"
                :class="{ active: selectedValueIds[spec.id] === value.id }"
                @click="selectSpecValue(spec.id, value.id)"
              >
                {{ value.name }}
              </text>
            </view>
          </view>
          <!-- 无规格树数据时回退为 SKU 平铺选择 -->
          <view v-if="!innerProduct.specs.length" class="sku-options">
            <text
              v-for="sku in innerProduct.skus"
              :key="sku.id"
              class="sku-option"
              :class="{ active: selectedSku.id === sku.id, disabled: sku.stock <= 0 }"
              @click="selectSku(sku)"
            >
              {{ sku.name }}
            </text>
          </view>
        </template>
      </view>

      <!-- 数量选择区（可选） -->
      <view v-if="showQuantity" class="sku-section quantity-section">
        <text class="section-title">数量</text>
        <u-number-box
          v-model="quantity"
          :min="innerProduct.min_buy || 1"
          :max="activeStock || innerProduct.stock"
          integer
        />
      </view>

      <view class="sku-confirm">
        <u-button type="success" :text="confirmText" @click="handleConfirm" />
      </view>
    </view>
  </u-popup>
</template>

<script setup lang="ts">
/**
 * 公共规格选择弹窗组件
 *
 * 支持「规格树选择 + 平铺回退」两种模式：
 * - 商品含 specs（规格树）时按规格分组逐项选择，组合完整后自动定位 SKU；
 * - 商品无规格树时回退为 SKU 平铺选择（库存为 0 的项置灰不可选）。
 * 商品列表接口通常不含 SKU/规格数据，组件内部按商品 ID 缓存详情兜底加载。
 *
 * 使用示例：
 *   <sku-selector
 *     v-model:show="showSkuPopup"
 *     :product="currentProduct"
 *     :initial-sku-id="12"
 *     confirm-text="加入购物车"
 *     @confirm="onSkuConfirm"
 *   />
 *   // onSkuConfirm({ sku, quantity }) => 处理加入购物车/切换规格等业务
 */
import { ref, computed, watch } from 'vue'
import { getProductDetail } from '@/api/product'
import { formatPrice } from '@/utils/format'
import type { Product, Sku } from '@/api/types'

interface Props {
  /** 弹窗显示状态（支持 v-model:show） */
  show: boolean
  /** 商品对象，允许仅含 id/name 等部分字段，缺 SKU/规格数据时组件内调详情接口兜底 */
  product: (Partial<Product> & { id: number }) | null
  /** 预选 SKU ID（如购物车切换规格时回填当前规格），0 表示不预选 */
  initialSkuId?: number
  /** 初始数量，0 表示使用商品最低购买份数 */
  initialQuantity?: number
  /** 确认按钮文案 */
  confirmText?: string
  /** 是否显示数量选择框 */
  showQuantity?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  initialSkuId: 0,
  initialQuantity: 0,
  confirmText: '加入购物车',
  showQuantity: true,
})

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
  /** 点击确认且规格校验通过后触发，product 为组件内部兜底加载后的完整商品对象 */
  (e: 'confirm', payload: { product: Product; sku: Sku; quantity: number }): void
}>()

const emptySku: Sku = {
  id: 0,
  product_id: 0,
  name: '',
  price: 0,
  original_price: 0,
  stock: 0,
  image: '',
  spec_name: '',
  spec_json: '{}',
  spec_value_ids: '',
}

const emptyProduct: Product = {
  id: 0,
  name: '',
  main_image: '',
  images: [],
  price: 0,
  original_price: 0,
  stock: 0,
  category_id: 0,
  category_name: '',
  description: '',
  content: '',
  skus: [],
  specs: [],
  sales: 0,
  show_sales: true,
  is_multi_spec: false,
  min_buy: 1,
}

/** 商品详情缓存：商品 ID -> 详情（含 skus/specs），避免重复请求 */
const detailCache = new Map<number, Product>()

const innerProduct = ref<Product>({ ...emptyProduct })
const selectedSku = ref<Sku>({ ...emptySku })
/** 多规格已选值：规格 ID -> 规格值 ID */
const selectedValueIds = ref<Record<number, number>>({})
/** 规格数据兜底加载中 */
const loading = ref(false)
const quantity = ref(1)

/** 已选规格组合键（规格值 ID 升序逗号串，与后端 spec_value_ids 一致） */
const selectedKey = computed(() =>
  Object.values(selectedValueIds.value)
    .sort((a, b) => a - b)
    .join(',')
)

/** 规格是否已全部选完 */
const allSelected = computed(() =>
  innerProduct.value.specs.length > 0 &&
  innerProduct.value.specs.every((s) => selectedValueIds.value[s.id])
)

/**
 * 当前匹配的 SKU
 * 规格树模式下需全部选完才能按组合键匹配；平铺回退模式返回 null
 */
const matchedSku = computed<Sku | null>(() => {
  const product = innerProduct.value
  if (!product.skus.length || !product.specs.length || !allSelected.value) return null
  return product.skus.find((s) => s.spec_value_ids === selectedKey.value) || null
})

/** 弹窗当前生效 SKU：规格树匹配结果优先，其次平铺模式手动选中项 */
const activeSku = computed<Sku | null>(() =>
  matchedSku.value || (selectedSku.value.id ? selectedSku.value : null)
)

// 注意用三目而非 ||：SKU 价格/库存为 0 时是有效值，不应回退到商品汇总字段
const activePrice = computed(() =>
  activeSku.value ? activeSku.value.price : innerProduct.value.price
)
const activeStock = computed(() =>
  activeSku.value ? activeSku.value.stock : innerProduct.value.stock
)
const activeImage = computed(
  () => activeSku.value?.image || innerProduct.value.main_image || '/static/logo.png'
)

watch(
  () => props.show,
  (val) => {
    if (val) {
      init()
    }
  },
)

/**
 * 弹窗打开时初始化内部状态
 * 商品缺 SKU/规格数据时优先读缓存，未命中再调详情接口兜底；
 * 初始化完成后按 initialSkuId 预选规格
 * @exception 详情接口失败时 toast 提示，弹窗以无规格数据状态呈现
 */
async function init() {
  const product = props.product
  if (!product || !product.id) return
  // 允许调用方仅传部分商品字段，缺失字段用空商品兜底
  innerProduct.value = { ...emptyProduct, ...product }
  quantity.value = props.initialQuantity || product.min_buy || innerProduct.value.min_buy || 1

  if (!innerProduct.value.skus.length) {
    const cached = detailCache.get(product.id)
    if (cached) {
      innerProduct.value = cached
    } else {
      loading.value = true
      try {
        const res = await getProductDetail(product.id)
        if (res.data) {
          detailCache.set(product.id, res.data)
          innerProduct.value = res.data
        }
      } catch {
        uni.showToast({ title: '规格加载失败', icon: 'none' })
      } finally {
        loading.value = false
      }
    }
  }
  applyInitialSku()
}

/**
 * 按 initialSkuId 预选规格
 * 规格树模式：解析 SKU 的 spec_value_ids（规格值 ID 集合）回填各分组选中值；
 * 平铺模式：直接选中该 SKU。未提供或找不到时回退为默认选中（首个可用 SKU）
 */
function applyInitialSku() {
  const product = innerProduct.value
  const initial = props.initialSkuId
    ? product.skus.find((s) => s.id === props.initialSkuId) || null
    : null

  if (product.specs.length) {
    const init: Record<number, number> = {}
    if (initial && initial.spec_value_ids) {
      // spec_value_ids 为规格值 ID 逗号串，按值集合在各规格分组中定位选中项
      const valueIdSet = new Set(initial.spec_value_ids.split(',').map(Number))
      for (const spec of product.specs) {
        const hit = spec.values.find((v) => valueIdSet.has(v.id))
        if (hit) {
          init[spec.id] = hit.id
        }
      }
    } else {
      for (const spec of product.specs) {
        if (spec.values.length) {
          init[spec.id] = spec.values[0].id
        }
      }
    }
    selectedValueIds.value = init
    if (matchedSku.value) {
      selectedSku.value = matchedSku.value
    }
    return
  }
  selectedSku.value = initial
    || product.skus.find((s) => s.stock > 0)
    || product.skus[0]
    || { ...emptySku }
}

/**
 * 选择规格值，组合完整时同步定位 SKU
 * @param specId 规格 ID
 * @param valueId 规格值 ID
 */
function selectSpecValue(specId: number, valueId: number) {
  selectedValueIds.value = { ...selectedValueIds.value, [specId]: valueId }
  if (matchedSku.value) {
    selectedSku.value = matchedSku.value
  }
}

/**
 * 平铺模式下选择 SKU（无库存的项不可选）
 * @param sku SKU 对象
 */
function selectSku(sku: Sku) {
  if (sku.stock <= 0) return
  selectedSku.value = sku
}

/**
 * 关闭弹窗（供 u-popup close 回调与关闭图标共用）
 */
function handleClose() {
  emit('update:show', false)
}

/**
 * 确认选择
 * 校验规格完整性后向父组件抛出 confirm 事件（携带 SKU 与数量）；
 * 校验不通过时 toast 提示且不触发事件
 */
function handleConfirm() {
  if (innerProduct.value.specs.length && !matchedSku.value) {
    uni.showToast({
      title: allSelected.value ? '该规格组合不存在' : '请选择完整规格',
      icon: 'none',
    })
    return
  }
  if (!selectedSku.value.id) {
    uni.showToast({ title: '请选择规格', icon: 'none' })
    return
  }
  emit('confirm', { product: innerProduct.value, sku: selectedSku.value, quantity: quantity.value })
}
</script>

<style scoped>
.sku-selector {
  padding: 30rpx;
  padding-bottom: 50rpx;
}

.sku-header {
  display: flex;
  align-items: center;
  margin-bottom: 30rpx;
}

.sku-image {
  width: 160rpx;
  height: 160rpx;
  border-radius: 12rpx;
}

.sku-info {
  flex: 1;
  margin: 0 20rpx;
  overflow: hidden;
}

.sku-name {
  display: block;
  font-size: 28rpx;
  color: #333333;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sku-price {
  display: block;
  margin-top: 10rpx;
  font-size: 34rpx;
  font-weight: bold;
  color: #ff4d4f;
}

.sku-stock {
  display: block;
  margin-top: 6rpx;
  font-size: 24rpx;
  color: #999999;
}

.sku-section {
  margin-bottom: 30rpx;
}

.sku-loading {
  padding: 20rpx 0;
  font-size: 26rpx;
  color: #999999;
}

.spec-group {
  margin-bottom: 20rpx;
}

.spec-group-name {
  display: block;
  margin-bottom: 12rpx;
  font-size: 24rpx;
  color: #999999;
}

.section-title {
  display: block;
  margin-bottom: 16rpx;
  font-size: 28rpx;
  color: #333333;
}

.sku-options {
  display: flex;
  flex-wrap: wrap;
  gap: 16rpx;
  margin-bottom: 10rpx;
}

.sku-option {
  padding: 12rpx 24rpx;
  border-radius: 8rpx;
  font-size: 26rpx;
  color: #666666;
  background-color: #f5f5f5;
}

.sku-option.active {
  color: #07c160;
  background-color: rgba(7, 193, 96, 0.1);
}

.sku-option.disabled {
  color: #cccccc;
  background-color: #fafafa;
}

.quantity-section {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.quantity-section .section-title {
  margin-bottom: 0;
}

.sku-confirm {
  margin-top: 20rpx;
}

.sku-confirm .u-button {
  width: 100%;
}
</style>
