/**
 * API 模块共享类型定义
 */

/** 用户信息 */
export interface UserInfo {
  id: number
  nickname: string
  avatar: string
  phone: string
  community_id: number
  community_name?: string
}

/** 社区信息 */
export interface Community {
  id: number
  name: string
  address: string
  longitude: number
  latitude: number
}

/** 收货地址 */
export interface Address {
  id: number
  contact_name: string
  contact_phone: string
  province: string
  city: string
  district: string
  detail: string
  tag: string
  is_default: boolean
}

/** 商品分类 */
export interface Category {
  id: number
  name: string
  icon: string
  parent_id: number
  sort: number
}

/** SKU */
export interface Sku {
  id: number
  product_id: number
  name: string
  price: number
  original_price: number
  stock: number
  image: string
}

/** 商品 */
export interface Product {
  id: number
  name: string
  main_image: string
  images: string[]
  price: number
  original_price: number
  stock: number
  category_id: number
  category_name: string
  description: string
  skus: Sku[]
  sales: number
  is_multi_spec: boolean
  min_buy: number
}

/** 购物车项 */
export interface CartItem {
  id: number
  product_id: number
  product_name: string
  sku_id: number
  sku_name: string
  price: number
  quantity: number
  selected: boolean
  image: string
  stock: number
  community_id: number
  community_name?: string
  min_buy: number
}

/** 订单商品 */
export interface OrderItem {
  product_id: number
  product_name: string
  sku_id: number
  sku_name: string
  price: number
  quantity: number
  image: string
}

/** 订单 */
export interface Order {
  id: number
  order_no: string
  status: number
  status_text: string
  total_amount: number
  pay_amount: number
  pay_type_text?: string
  items: OrderItem[]
  address: Address
  remark: string
  created_at: string
  paid_at: string
  delivered_at: string
  received_at: string
}

/** 优惠券 */
export interface Coupon {
  id: number
  name: string
  type: number
  value: number
  threshold: number
  start_time: string
  end_time: string
  total: number
  received: number
}

/** 用户优惠券 */
export interface UserCoupon {
  id: number
  coupon_id: number
  name: string
  type: number
  value: number
  threshold: number
  start_time: string
  end_time: string
  status: number
}

/** 秒杀活动 */
export interface FlashSale {
  id: number
  product_id: number
  product_name: string
  product_image: string
  flash_price: number
  original_price: number
  stock: number
  sold: number
  start_time: string
  end_time: string
  status: number
}

/** 满减活动 */
export interface FullReduction {
  id: number
  community_id: number
  name: string
  threshold: number
  reduce_amount: number
  start_time: string
  end_time: string
  status: number
}

/** Banner 轮播图 */
export interface Banner {
  id: number
  community_id: number
  title: string
  image_url: string
  link_url: string
  position: number
  sort: number
  status: number
  start_time: string
  end_time: string
}

/** 支付渠道 */
export interface PaymentChannel {
  channel: 'wechat' | 'alipay' | 'cash'
  name: string
}

/** 支付单 */
export interface Payment {
  id: number
  order_id: number
  amount: number
  status: number
  pay_type: number
  pay_url: string
  pay_params: Record<string, string>
}
