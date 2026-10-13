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
  province: string
  city: string
  district: string
  /** 是否允许社区外配送（false 时地址前缀锁定为本社区） */
  allow_outside_delivery: boolean
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
  /** 地址所属社区（保存时由服务端按用户当前社区回填） */
  community_id: number
  community_name: string
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

/** 规格值 */
export interface SpecValue {
  id: number
  name: string
  sort: number
}

/** 规格（含规格值列表） */
export interface Spec {
  id: number
  name: string
  sort: number
  values: SpecValue[]
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
  /** 规格名称（如 "红色 / 大号"） */
  spec_name: string
  /** 规格 JSON（如 {"颜色":"红色"}），单规格为 "{}" */
  spec_json: string
  /** 逗号分隔的规格值 ID 组合键（多规格用，单规格为空） */
  spec_value_ids: string
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
  /** 规格树（多规格时非空） */
  specs: Spec[]
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
  spec_name: string
  price: number
  quantity: number
  selected: boolean
  product_image: string
  stock: number
  community_id: number
  community_name?: string
  min_buy: number
  /** 商品 SKU 总数，>1 表示多规格（可切换规格） */
  sku_total: number
  /** 是否失效（商品下架/删除、SKU 删除、库存为 0） */
  invalid: boolean
  /** 失效原因文案，未失效为空 */
  invalid_reason: string
  /** 库存紧张提示文案（如"仅剩 5 件"），库存充足为空 */
  stock_hint: string
}

/** 订单商品 */
export interface OrderItem {
  id: number
  product_id: number
  product_name: string
  sku_id: number
  spec_name: string
  price: number
  quantity: number
  total_price: number
  product_image: string
}

/** 订单 */
export interface Order {
  id: number
  order_no: string
  status: number
  status_text: string
  total_amount: number
  discount_amount: number
  delivery_fee: number
  delivery_type: number
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
  payment_no: string
  order_id: number
  user_id: number
  pay_type: number
  amount: number
  status: number
  transaction_id: string
  pay_time: string
}

/** 创建支付单响应 */
export interface CreatePaymentResult {
  payment_no: string
  pay_type: number
  amount: number
  /** 微信调起参数 JSON：小程序 JSAPI 为签名参数，H5 为 { h5_url } */
  wechat_pay_params?: string
  /** 支付宝调起参数 JSON，当前渠道未开放 */
  alipay_pay_params?: string
}
