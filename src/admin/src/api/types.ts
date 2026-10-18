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
 * 管理员信息（多社区任职模型：账号层 + 任职层）
 */
export interface AdminInfo {
  id: number
  username: string
  nickname?: string
  real_name?: string
  phone?: string
  avatar?: string
  /** 是否超级管理员（不受社区与角色约束） */
  is_super?: boolean
  /** 旧单角色字段，仅兼容保留 */
  role_id?: number
  role_name?: string
  role_code?: string
  /** 当前社区语境下的角色列表（员工管理列表用） */
  roles?: Role[]
  permissions?: string[]
  status: number
  /** 默认社区 ID（登录时取任职列表第一个） */
  community_id?: number
  /** 任职的全部社区 ID 列表（超管为空数组，表示不限制社区） */
  community_ids?: number[]
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
  status?: number
  /** 所属社区：0=全局角色（仅超管可见可管），>0=社区私有角色 */
  community_id?: number
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
  /** C端是否显示销量 */
  show_sales?: boolean
  /** 虚拟销量（展示口径=真实销量+虚拟销量） */
  virtual_sales?: number
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
  /** 划线价（原价，0 表示不展示） */
  original_price: number
  cost_price: number
  stock: number
  safety_stock: number
  image: string
  status: number
  /** 商品条形码（EAN-13 等，社区内唯一，空表示未设置） */
  barcode: string
  /** 冗余商品名（仅 SKU 搜索接口返回，按商品查询的接口为空） */
  product_name?: string
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
  /** 划线价（原价，0 表示不展示） */
  original_price: number
  cost_price: number
  stock: number
  safety_stock: number
  image: string
  /** 商品条形码（EAN-13 等，社区内唯一，空表示未设置） */
  barcode: string
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
  /** C端是否显示销量 */
  show_sales: boolean
  /** 虚拟销量（展示口径=真实销量+虚拟销量） */
  virtual_sales: number
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
  /** 订单所属社区（跨社区查询时回显） */
  community_id?: number
  community_name?: string
  total_amount: number
  discount_amount: number
  delivery_fee: number
  pay_amount: number
  delivery_type: number
  status: number
  status_text: string
  /** 订单来源：1=C端 2=收银台（决策点⑨） */
  source?: number
  /** 手动优惠金额（收银台整单改价/抹零折算，仅收银单） */
  manual_discount?: number
  /** 所属班次 ID（收银单归属，用于交班统计） */
  shift_id?: number
  /** 支付方式码（1微信 2支付宝 3货到付款 4现金；0=未支付/未知） */
  pay_type?: number
  /** 支付方式文案（后端直出，未支付为空串） */
  pay_type_text?: string
  /** 取货码（自提单，收银台核销/订单查询用） */
  pickup_code?: string
  /** 经手人（收银员/管理员）姓名快照 */
  operator_name?: string
  /** 指派的履约人员（0=未指派）与姓名快照（B 履约工作台） */
  delivery_user_id?: number
  delivery_user_name?: string
  /** 送达签收凭证照片 URL（决策点②） */
  delivery_proof_images?: string[]
  created_at: string
}

/**
 * 收银台挂单（未成单草稿，后端暂存）
 */
export interface PosHeldOrder {
  id: number
  community_id: number
  operator_id: number
  operator_name: string
  /** 会员 ID（0=散客） */
  user_id: number
  user_name: string
  items: PosHeldOrderItem[]
  item_count: number
  total_amount: number
  remark: string
  created_at: string
}

/**
 * 收银台挂单明细项（快照）
 */
export interface PosHeldOrderItem {
  sku_id: number
  quantity: number
  product_name: string
  spec_name?: string
  product_image?: string
  price: number
}

/**
 * 订单详情
 */
export interface OrderDetail extends Order {
  delivery_time: string
  remark: string
  pay_time: string
  deliver_time: string
  complete_time: string
  cancel_time: string
  cancel_reason: string
  express_company?: string
  express_no?: string
  items: OrderItem[]
  address: OrderAddress
  status_logs: OrderStatusLog[]
  /** 本单每次售后（退款）记录，按时间正序 */
  refunds?: Refund[]
}

/**
 * 订单商品项
 */
export interface OrderItem {
  id: number
  product_id: number
  sku_id: number
  product_name: string
  sku_name: string
  product_image: string
  price: number
  /** 商品档案原价（price_modified=1 时 price 为收银改价后的成交价） */
  original_price?: number
  /** 是否发生过单品改价：1=是 */
  price_modified?: number
  quantity: number
  total_amount: number
}

/**
 * 订单状态流转日志
 */
export interface OrderStatusLog {
  from_status: number
  to_status: number
  operator_type: string
  remark: string
  created_at: string
}

/**
 * 售后单（退款单）
 */
export interface Refund {
  id: number
  refund_no: string
  order_id: number
  order_no: string
  user_id: number
  payment_no: string
  amount: number
  freight_amount: number
  type: number
  reason: string
  status: number
  reject_reason: string
  refund_time: string
  created_at: string
  items: RefundItem[]
  logs?: RefundLog[]
}

/**
 * 退款审批日志
 */
export interface RefundLog {
  id: number
  /** 动作：approve 同意 / reject 拒绝 / retry 重试 */
  action: string
  operator_type: string
  operator_id: number
  remark: string
  created_at: string
}

/**
 * 售后单商品明细
 */
export interface RefundItem {
  id: number
  refund_id: number
  order_item_id: number
  sku_id: number
  product_name: string
  spec_name: string
  quantity: number
  refund_amount: number
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
  sku_id: number
  product_id: number
  product_name: string
  type: number
  type_text: string
  quantity: number
  before_stock: number
  after_stock: number
  batch_id: number
  batch_no: string
  ref_no: string
  related_order_id: number
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

/** 供应商 */
export interface Supplier {
  id: number
  community_id: number
  name: string
  contact_name: string
  phone: string
  address: string
  remark: string
  status: number
  created_at: string
}

/** 采购单明细 */
export interface PurchaseOrderItemRow {
  id: number
  sku_id: number
  sku_code: string
  spec_name: string
  product_name: string
  quantity: number
  received_quantity: number
  purchase_price: number
}

/** 采购单头 */
export interface PurchaseOrder {
  id: number
  purchase_no: string
  community_id: number
  supplier_id: number
  supplier_name: string
  status: number
  total_amount: number
  remark: string
  operator_id: number
  created_at: string
  updated_at: string
}

/** 采购单详情 */
export interface PurchaseOrderDetail {
  info: PurchaseOrder
  items: PurchaseOrderItemRow[]
}

/** 入库单明细 */
export interface StockInOrderItemRow {
  id: number
  sku_id: number
  sku_code: string
  product_name: string
  spec_name: string
  quantity: number
  unit_cost: number
  batch_no: string
  production_date: string
  expire_date: string
  batch_id: number
}

/** 入库单头 */
export interface StockInOrder {
  id: number
  in_no: string
  community_id: number
  supplier_id: number
  supplier_name: string
  purchase_order_id: number
  purchase_no: string
  type: number
  total_amount: number
  operator_id: number
  remark: string
  created_at: string
}

/** 入库单详情 */
export interface StockInOrderDetail {
  info: StockInOrder
  items: StockInOrderItemRow[]
}

/** 出库单头 */
export interface StockOutOrder {
  id: number
  out_no: string
  community_id: number
  type: number
  operator_id: number
  remark: string
  created_at: string
}

/** 出库单明细 */
export interface StockOutOrderItemRow {
  id: number
  sku_id: number
  sku_code: string
  product_name: string
  spec_name: string
  quantity: number
}

/** 出库单详情 */
export interface StockOutOrderDetail {
  info: StockOutOrder
  items: StockOutOrderItemRow[]
}

/** 批次库存 */
export interface StockBatch {
  id: number
  sku_id: number
  sku_code: string
  product_name: string
  spec_name: string
  batch_no: string
  production_date: string
  expire_date: string
  quantity: number
  unit_cost: number
  status: number
  source_type: number
  ref_no: string
  created_at: string
}

/** 进销存汇总行 */
export interface InventorySummaryItem {
  sku_id: number
  sku_code: string
  spec_name: string
  product_name: string
  opening_stock: number
  in_quantity: number
  out_quantity: number
  closing_stock: number
  cost_price: number
  price: number
}

/** 毛利分析行 */
export interface ProfitReportItem {
  sku_id: number
  sku_code: string
  spec_name: string
  product_name: string
  quantity: number
  sales_amount: number
  cost_amount: number
  profit: number
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
  province: string
  city: string
  district: string
  /** 省/市/区行政区划代码（来自区划选择器，历史数据允许为空） */
  province_code?: string
  city_code?: string
  district_code?: string
  allow_outside_delivery: boolean
  /** 取货渠道开关（社区级配置，C 端与收银台共用；到店不受控恒可用） */
  enable_self_pickup: boolean
  enable_delivery: boolean
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
  gender?: number
  community_id: number
  community_name: string
  status: number
  /** 是否本社区履约人员（B 履约工作台） */
  is_fulfiller?: boolean
  created_at: string
  last_login_at?: string
}

/**
 * 后台账号信息（系统管理·账号管理页，仅账号本身维度）
 */
export interface Account {
  id: number
  username: string
  real_name: string
  phone: string
  is_super: boolean
  status: number
  last_login_at: string
  /** 任职社区 ID 列表（超管为空数组，表示不限制社区） */
  community_ids: number[]
}

/**
 * 优惠券商品范围项
 * scope_type：1全场 2指定分类 3指定商品 4排除分类 5排除商品；无行=全场通用
 */
export interface CouponScope {
  id?: number
  scope_type: number
  category_id: number
  category_name?: string
  product_id: number
  product_name?: string
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
  used_count: number
  /** 领取时间窗口（与可用范围分离） */
  start_time: string
  end_time: string
  valid_days: number
  status: number
  /** 发布状态：0草稿 1已发布 2已下架 */
  publish_status: number
  /** 发放状态：0停止发放 1发放中 */
  grant_status: number
  /** 是否进入领券中心（0/1） */
  is_center_visible: number
  /** 到期类型：1领取后N天 2指定可用范围 */
  expire_type: number
  use_start_time?: string
  use_end_time?: string
  /** 规则文案 */
  rule_text?: string
  /** 商品范围（空=全场通用） */
  scopes?: CouponScope[]
}

/**
 * 优惠券领取/发放记录（后台）
 */
export interface CouponRecord {
  id: number
  coupon_id: number
  coupon_name: string
  type: number
  threshold: number
  discount_value: number
  user_id: number
  user_nickname: string
  user_phone: string
  /** 1未使用 2已使用 3已过期 */
  status: number
  order_id: number
  /** 来源：1用户自领 2后台发放 */
  source: number
  operator_id: number
  operator_name: string
  expire_at: string
  /** 领取/发放时间 */
  created_at: string
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
 * 免配送费排除范围项（4=排除分类 5=排除商品）
 */
export interface FreeShippingScope {
  id?: number
  scope_type: number
  category_id: number
  /** 展示名（服务端回填） */
  category_name?: string
  product_id: number
  /** 展示名（服务端回填） */
  product_name?: string
}

/**
 * 满额免配送费活动
 * 语义：配送订单中「参与商品额合计 >= threshold」则免收配送费；
 * scopes 命中的商品金额不计入门槛
 */
export interface FreeShipping {
  id: number
  community_id: number
  name: string
  /** 免配送费门槛（参与商品额） */
  threshold: number
  start_time: string
  end_time: string
  status: number
  scopes?: FreeShippingScope[]
}

/**
 * Banner 轮播图
 */
export interface Banner {
  id: number
  /** 适用社区：0=全部社区通用 */
  community_id: number
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

/**
 * 交班统计项（按支付方式分组的收款/退款）
 */
export interface PosShiftStatItem {
  /** 支付方式：1=微信 2=支付宝 3=货到付款 4=现金 */
  pay_type: number
  amount: number
  order_count: number
}

/**
 * 交班统计汇总（进行中实时计算，已结束读快照）
 * 字段对齐 proto PosShiftStatistics：income/refund 为按支付方式分组明细，总额在 total_income/total_refund
 */
export interface PosShiftStatistics {
  /** 收款分组明细（按支付方式） */
  income: PosShiftStatItem[]
  /** 退款分组明细（按支付方式） */
  refund: PosShiftStatItem[]
  /** 收款合计（排除待支付/已取消） */
  total_income: number
  /** 退款合计 */
  total_refund: number
  /** 现金合计 = 现金收款 - 现金退款（交班实缴参考） */
  cash_total: number
  /** 班内订单数 */
  order_count: number
}

/**
 * 收银班次
 */
export interface PosShift {
  id: number
  community_id: number
  operator_id: number
  operator_name: string
  /** 开班时间 */
  start_time: string
  /** 交班时间（进行中为空） */
  end_time: string
  /** 状态：1=进行中 2=已结束 */
  status: number
  /** 交班备注 */
  note: string
  statistics?: PosShiftStatistics
  created_at: string
}

/**
 * 班次分页
 */
export interface PosShiftPage {
  list: PosShift[]
  total: number
}

/**
 * 应用偏好（常用应用，社区级共享存储）
 */
export interface AppPreferences {
  /** 应用 key 列表（有序，排前面的展示靠前） */
  app_keys: string[]
}
