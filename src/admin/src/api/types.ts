/**
 * 通用分页请求参数
 */
export interface PageParams {
  page: number
  page_size: number
}

/**
 * 通用分页响应结构
 */
export interface PageResult<T> {
  list: T[]
  total: number
  page: number
  page_size: number
}

/**
 * 通用 API 响应结构
 */
export interface ApiResult<T> {
  code: number
  message: string
  data: T
}

/**
 * 管理员信息
 */
export interface AdminInfo {
  id: number
  username: string
  nickname: string
  real_name?: string
  phone?: string
  avatar?: string
  role_id: number
  role_name: string
  role_code?: string
  permissions: string[]
  status: number
}

/**
 * 登录请求参数
 */
export interface LoginParams {
  username: string
  password: string
}

/**
 * 登录响应
 */
export interface LoginResult {
  access_token: string
  refresh_token: string
  expires_in: number
  admin: AdminInfo
}

/**
 * 角色信息
 */
export interface Role {
  id: number
  name: string
  code: string
  permissions: string[]
  description?: string
  status: number
}

/**
 * 商品分类
 */
export interface Category {
  id: number
  name: string
  parent_id: number
  sort: number
  status: number
}

/**
 * 商品信息
 */
export interface Product {
  id: number
  name: string
  category_id: number
  category_name: string
  price: number
  original_price?: number
  min_price?: number
  max_price?: number
  stock: number
  status: number
  cover_image?: string
  images?: string[]
  unit?: string
  description?: string
  is_multi_spec?: boolean
  min_buy?: number
  sort?: number
  sales_count?: number
}

/**
 * 规格值信息
 */
export interface SpecValue {
  id: number
  name: string
  sort: number
}

/**
 * 规格名信息（含层级规格值）
 */
export interface Spec {
  id: number
  name: string
  sort: number
  values: SpecValue[]
}

/**
 * SKU 信息
 */
export interface Sku {
  id: number
  product_id: number
  sku_code: string
  /** 逗号分隔的规格值 ID（多规格组合键，单规格为空） */
  spec_value_ids: string
  spec_name: string
  spec_json: string
  price: number
  cost_price: number
  stock: number
  safety_stock: number
  image: string
  status: number
}

/**
 * 规格名保存项（新增节点 temp_id 非空，编辑节点携带真实 id）
 */
export interface SpecSaveItem {
  id: number
  temp_id: string
  name: string
  sort: number
  values: SpecValueSaveItem[]
}

/**
 * 规格值保存项
 */
export interface SpecValueSaveItem {
  id: number
  temp_id: string
  name: string
  sort: number
}

/**
 * SKU 保存项（spec_value_ids 为规格值 ID 列表，真实数字 ID 或临时 ID）
 */
export interface SkuSaveItem {
  id: number
  spec_value_ids: string[]
  sku_code: string
  price: number
  cost_price: number
  stock: number
  safety_stock: number
  image: string
}

/**
 * 商品保存载荷
 */
export interface ProductSavePayload {
  name: string
  category_id: number
  description: string
  cover_image: string
  images: string[]
  unit: string
  is_multi_spec: boolean
  min_buy: number
  sort: number
  specs: SpecSaveItem[]
  sku_items: SkuSaveItem[]
}

/**
 * 商品详情（含 SKU 列表与规格树）
 */
export interface ProductDetail {
  product: Product
  skus: Sku[]
  specs: Spec[]
}

/**
 * 订单信息
 */
export interface Order {
  id: number
  order_no: string
  user_id: number
  user_name: string
  total_amount: number
  pay_amount: number
  status: number
  status_text: string
  created_at: string
}

/**
 * 订单详情
 */
export interface OrderDetail extends Order {
  items: OrderItem[]
  address: OrderAddress
}

/**
 * 订单商品项
 */
export interface OrderItem {
  id: number
  product_name: string
  sku_name: string
  price: number
  quantity: number
  total_amount: number
}

/**
 * 订单地址
 */
export interface OrderAddress {
  name: string
  phone: string
  address: string
}

/**
 * 库存流水
 */
export interface InventoryLog {
  id: number
  product_id: number
  product_name: string
  type: number
  type_text: string
  quantity: number
  before_stock: number
  after_stock: number
  remark: string
  created_at: string
}

/**
 * 盘点记录
 */
export interface InventoryCheck {
  id: number
  check_no: string
  total_items: number
  diff_items: number
  status: number
  remark: string
  created_at: string
}

/**
 * 盘点单明细项
 */
export interface StockCheckItem {
  id: number
  sku_id: number
  sku_code: string
  product_name: string
  spec_name: string
  system_stock: number
  actual_stock: number
  diff_quantity: number
  created_at: string
}

/**
 * 盘点单详情
 */
export interface StockCheckDetail {
  info: InventoryCheck
  items: StockCheckItem[]
}

/**
 * 补货建议项
 */
export interface PurchaseOrderItem {
  sku_id: number
  sku_code: string
  spec_name: string
  product_name: string
  current_stock: number
  safety_stock: number
  suggest_quantity: number
}

/**
 * 社区信息
 */
export interface Community {
  id: number
  name: string
  address: string
  manager_name: string
  manager_phone: string
  longitude: number
  latitude: number
  contact_phone: string
  pickup_address: string
  delivery_fee: number
  min_order_amount: number
  delivery_range_km: number
  business_hours: string
  sort: number
  status: number
}

/**
 * 用户信息
 */
export interface User {
  id: number
  nickname: string
  phone: string
  community_id: number
  community_name: string
  status: number
  created_at: string
}

/**
 * 优惠券
 */
export interface Coupon {
  id: number
  name: string
  type: number
  /** 优惠值（后端字段 discount_value） */
  discount_value: number
  /** 使用门槛（后端字段 threshold） */
  threshold: number
  total_count: number
  per_user_limit?: number
  received_count: number
  start_time: string
  end_time: string
  status: number
}

/**
 * 秒杀活动
 */
export interface Seckill {
  id: number
  product_id: number
  product_name: string
  seckill_price: number
  stock: number
  start_time: string
  end_time: string
  status: number
}

/**
 * 满减活动
 */
export interface FullReduction {
  id: number
  name: string
  /** 满减门槛（后端字段 threshold） */
  threshold: number
  /** 减免金额（后端字段 reduce_amount） */
  reduce_amount: number
  start_time: string
  end_time: string
  status: number
}

/**
 * Banner 轮播图
 */
export interface Banner {
  id: number
  title: string
  image_url: string
  link_url?: string
  position: number
  sort: number
  status: number
  start_time?: string
  end_time?: string
  created_at?: string
  updated_at?: string
}
