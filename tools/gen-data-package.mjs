// gen-data-package.mjs —— 生成「数据导入」应用的数据包定义（演示数据单一数据源）
// 产出：src/backend/services/product/service/seeddata/full-demo.json（go:embed 编译进 product-service）
//
// 本文件是演示数据的唯一来源：商品目录来自 seed-catalog.mjs 的 collectSeedCatalog()，
// 全局段（社区/区划/账号/用户/站点）与社区域补充段（Banner/满减/券/秒杀/满免邮/用户券/购物车）在本文件定义，
// 引用关系用包内 key（comm-N / cat-N / prod-N / sku-N）或自然键（手机号/商品名）表达。
// 归属口径：每个社区域条目都带 community_key（Banner 的平台级位用 platform=true），
// 初始化按社区逐个导入时只落各自归属的条目——社区 1（盘龙金茂悦）为完整演示目录 + 全套营销，
// 社区 2（阳光花园）为 6 分类 / 20 商品与专属 Banner（2026-10-27 起两社区商品名全部唯一）。
// 原 deploy/init.dev.sql 已由本数据包取代（导入执行链路见 product service 的 dataimport*.go，
// 初始化入口为 market-admin init → DataImportService.SeedDemoBootstrap）。
// 使用示例：
//   node tools/gen-data-package.mjs   # 重新生成 full-demo.json 后随 Go 代码一并提交
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { collectSeedCatalog } from './seed-catalog.mjs';

const ROOT_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const OUT_FILE = path.join(ROOT_DIR, 'src', 'backend', 'services', 'product', 'service', 'seeddata', 'full-demo.json');

const catalog = collectSeedCatalog();

// ---------- 全局段：社区（scope=global，仅随环境初始化导入，应用中心不显示） ----------
// 直辖市口径：无 4 位市级码（city_code 为空），区县直接挂省级下
const communities = [
  {
    key: 'comm-1', name: '盘龙金茂悦', address: '重庆市九龙坡区彩云大道8号',
    longitude: 106.492104, latitude: 29.506279,
    contact_phone: '17898616950', manager_name: '万先生', manager_phone: '17898616950',
    pickup_address: '金茂悦7栋1楼保安处', delivery_fee: 3.0, min_order_amount: 15.0, delivery_range_km: 2.0,
    business_hours: '[{"start":"08:00","end":"12:00"},{"start":"14:00","end":"22:00"}]',
    province: '重庆市', city: '重庆市', district: '九龙坡区',
    province_code: '50', city_code: '', district_code: '500107',
    allow_outside_delivery: false,
  },
  {
    key: 'comm-2', name: '阳光花园', address: '重庆市南岸区阳光路 8 号',
    longitude: 106.5716, latitude: 29.5218,
    contact_phone: '023-88886666', manager_name: '李管家', manager_phone: '13900139002',
    pickup_address: '花园南门快递柜旁自提点', delivery_fee: 2.0, min_order_amount: 0.0, delivery_range_km: 3.0,
    business_hours: '09:00-21:00', // 存量单段文本格式，展示端兼容解析（演示两种营业时段口径）
    province: '重庆市', city: '重庆市', district: '南岸区',
    province_code: '50', city_code: '', district_code: '500108',
    allow_outside_delivery: true, // 允许社区外配送：测试两种锁区分支
  },
];

// ---------- 全局段：行政区划（重庆市演示子集；全国数据由后台「检查更新」同步） ----------
// 口径：省级 2 位码；直辖市无市级码，区县 level=3 直接挂 '50' 下
const regions = [
  { code: '50', name: '重庆市', parent_code: '0', level: 1 },
  ...[
    ['500101', '万州区'], ['500102', '涪陵区'], ['500103', '渝中区'], ['500104', '大渡口区'],
    ['500106', '沙坪坝区'], ['500107', '九龙坡区'], ['500108', '南岸区'], ['500109', '北碚区'],
    ['500110', '綦江区'], ['500111', '大足区'], ['500113', '巴南区'], ['500114', '黔江区'],
    ['500115', '长寿区'], ['500116', '江津区'], ['500117', '合川区'], ['500118', '永川区'],
    ['500119', '南川区'], ['500120', '璧山区'], ['500151', '铜梁区'], ['500152', '潼南区'],
    ['500153', '荣昌区'], ['500154', '开州区'], ['500155', '梁平区'], ['500156', '武隆区'],
    ['500157', '两江新区'], ['500229', '城口县'], ['500230', '丰都县'], ['500231', '垫江县'],
    ['500233', '忠县'], ['500235', '云阳县'], ['500236', '奉节县'], ['500237', '巫山县'],
    ['500238', '巫溪县'], ['500240', '石柱土家族自治县'], ['500241', '秀山土家族苗族自治县'],
    ['500242', '酉阳土家族苗族自治县'], ['500243', '彭水苗族土家族自治县'],
  ].map(([code, name]) => ({ code, name, parent_code: '50', level: 3 })),
];

// ---------- 全局段：管理员账号（弱口令仅限演示；生产由初始化流程拒绝导入演示包） ----------
// assignments 的角色仅支持内置 community_admin（不存在时自动补建）
const admins = [
  {
    key: 'admin-1', username: 'admin', password: 'admin123', real_name: '系统管理员',
    phone: '13800138001', is_super: true, assignments: [],
  },
  {
    key: 'admin-2', username: 'shequ', password: 'admin123', real_name: '阳光花园管理员',
    phone: '13800138003', is_super: false,
    assignments: [{ community_key: 'comm-2', role_code: 'community_admin' }],
  },
];

// ---------- 全局段：C 端用户（含默认地址与会员-社区关联） ----------
// user_communities 必须显式导入：启动回填 BackfillUserCommunities 早于种子导入，
// 覆盖不到种子用户；source 演示两种真实来源（order 下单 / address 收货地址）
const users = [
  {
    key: 'user-1', openid: 'test_openid_1', phone: '13800138000', nickname: '测试用户',
    avatar: 'https://placehold.co/100x100/orange/white?text=User', gender: 0,
    community_key: 'comm-1',
    address: {
      contact_name: '张三', contact_phone: '13800138000',
      province: '重庆市', city: '重庆市', district: '九龙坡区',
      province_code: '50', city_code: '', district_code: '500107',
      detail: '彩云大道 8 号 2 单元 301', tag: '家', is_default: true,
    },
    memberships: [{ community_key: 'comm-1', source: 'order' }],
  },
  {
    key: 'user-2', openid: 'test_openid_2', phone: '13900139000', nickname: '阳光用户',
    avatar: 'https://placehold.co/100x100/blue/white?text=User2', gender: 0,
    community_key: 'comm-2',
    address: {
      contact_name: '李四', contact_phone: '13900139000',
      province: '重庆市', city: '重庆市', district: '南岸区',
      province_code: '50', city_code: '', district_code: '500108',
      detail: '阳光路 8 号 5 栋 1201', tag: '家', is_default: true,
    },
    memberships: [{ community_key: 'comm-2', source: 'address' }],
  },
];

// ---------- 全局段：站点配置（site_configs 单行品牌位；base_url 恒留空按请求现推） ----------
const site = {
  site_name: '社区集市',
  footer_html: '<p>© 2026 社区集市 · 演示环境</p>',
  seo_description: '社区便利店，线上下单到店自提',
  seo_keywords: '社区团购,便利店,零食饮料,日用百货',
};

// ---------- Banner（社区域条目，按 community_key 归属；platform=true 为全平台通用位） ----------
// 前两条挂 community_id=0（全平台兜底展示，所有社区可见），第 3 条为阳光花园定制；
// 初始化逐社区导入时平台级 Banner 只落一次（按 community_id=0 标题判重）
const banners = [
  { key: 'banner-1', platform: true, title: '新人专享满减', image_url: 'https://placehold.co/750x300/ff6b35/white?text=Banner1', link_url: '#', position: 2, sort: 1 },
  { key: 'banner-2', platform: true, title: '零食饮品周周上新', image_url: 'https://placehold.co/750x300/4caf50/white?text=Banner2', link_url: '#', position: 2, sort: 2 },
  { key: 'banner-3', community_key: 'comm-2', title: '阳光花园精选', image_url: 'https://placehold.co/750x300/2196f3/white?text=Banner3', link_url: '#', position: 2, sort: 1 },
];

// ---------- 满减（start/end NULL=长期有效） ----------
const full_reductions = [
  { key: 'fr-1', community_key: 'comm-1', name: '全场满30减5', threshold: 30, reduce_amount: 5 },
];

// ---------- 优惠券（范围=「酒水饮料」分类） ----------
const coupons = [
  {
    key: 'coupon-1', community_key: 'comm-1', name: '新人满20减5券', type: 1, threshold: 20, discount_value: 5,
    total_count: 100, per_user_limit: 2, valid_days: 30,
    scopes: [{ scope_type: 2, category_key: 'cat-2' }], // cat-2 = 酒水饮料
  },
];

// ---------- 秒杀（时间窗导入时按相对偏移计算：-1h ~ +2d） ----------
// sold_count 为演示已售口径（订单段不改此值，导入后显式回写，与原 SQL 展示一致）
const flash_sales = [
  {
    key: 'fs-1', community_key: 'comm-1', name: '限时秒杀·零食饮品特惠',
    start_offset_minutes: -60, end_offset_minutes: 2880,
    items: [
      { key: 'fsi-1', sku_key: 'sku-1', flash_price: 2.99, stock: 50, sold_count: 3, limit_per_user: 2 }, // 辣条小包
      { key: 'fsi-2', sku_key: 'sku-3', flash_price: 1.69, stock: 100, sold_count: 12, limit_per_user: 5 }, // 瓶装水
    ],
  },
];

// ---------- 满免邮（长期有效；排除商品按社区内商品名解析） ----------
const free_shippings = [
  {
    key: 'fship-1', community_key: 'comm-1', name: '满39元免配送费', threshold: 39,
    scopes: [{ scope_type: 5, product_name: '乐游 原味薯片' }], // 演示不参与免邮商品
  },
];

// ---------- 用户券（仅未使用态；已用券绑定订单，随订单段导入） ----------
// 券模板 received_count 口径 = 本段 1 张 + 订单段已用券 1 张 = 2，used_count = 1（订单段核销计数）
const user_coupons = [
  { key: 'uc-1', community_key: 'comm-1', user_phone: '13800138000', coupon_key: 'coupon-1', coupon_name: '新人满20减5券', expire_in_days: 30 },
];

// ---------- 购物车（含一条未选中，演示单选/全选；商品按社区内名称+规格名解析） ----------
const carts = [
  { key: 'cart-1', community_key: 'comm-1', user_phone: '13800138000', product_name: '魔小鲜 大面筋辣条', spec_name: '小包106g', quantity: 2, selected: true },
  { key: 'cart-2', community_key: 'comm-1', user_phone: '13800138000', product_name: '乐游 原味薯片', spec_name: '70g', quantity: 1, selected: false },
];

// ============================================================
// 二期演示样本（scope=community + init_only：仅随环境初始化灌入，应用中心不展示）
// 每条样本带 community_key 标注归属社区，初始化按社区逐个导入时只落各自样本。
// 时间全部为相对偏移：offset_minutes 相对导入时刻；day_offset+minute_of_day
// 相对今天零点（班次/收银单的「昨天营业时段」语义），导入时换算绝对时间。
// ============================================================

// ---------- 供应商（社区1） ----------
const suppliers = [
  {
    key: 'sup-1', community_key: 'comm-1', name: '渝百快消品批发商行', contact_name: '王老板',
    phone: '023-68111111', address: '重庆市九龙坡区西彭镇食品批发城B区3号',
    remark: '休闲零食/方便速食/饮料，长期合作账期月结', status: 1,
  },
  {
    key: 'sup-2', community_key: 'comm-1', name: '洁士日化供应链', contact_name: '赵姐',
    phone: '023-68222222', address: '重庆市江津区双福商贸城C区12号',
    remark: '纸品与洗涤日化', status: 1,
  },
];

// ---------- 采购单（在途：已下单未入库；入库后由服务生成批次，种子不预置批次） ----------
const purchase_orders = [
  {
    key: 'po-1', community_key: 'comm-1', purchase_no: 'PO20261018001',
    supplier_key: 'sup-1', status: 2, total_amount: 260.0, operator_key: 'admin-1',
    remark: '周末档期例行补货',
    items: [
      { sku_key: 'sku-1', quantity: 100, received_quantity: 0, purchase_price: 2.0 },
      { sku_key: 'sku-3', quantity: 50, received_quantity: 0, purchase_price: 1.2 },
    ],
  },
];

// ---------- 收银历史班次（已交班；绝不预置进行中班次，避免占用开工门禁） ----------
// statistics_json 为交班冻结快照（snake_case，与 EndShift 的 json.Marshal 输出一致）；
// 与班次内收银台订单（订单8，现金 6.00）对齐；班次日期取「昨天 09:00-21:00」
const pos_shifts = [
  {
    key: 'shift-1', community_key: 'comm-1', operator_key: 'admin-1', operator_name: '系统管理员',
    start_time: { day_offset: -1, minute_of_day: 540 },
    end_time: { day_offset: -1, minute_of_day: 1260 },
    status: 2, note: '种子数据：历史早班',
    statistics_json: '{"income":[{"pay_type":4,"amount":6.00,"order_count":1}],"refund":[],"total_income":6.00,"total_refund":0,"cash_total":6.00,"order_count":1}',
  },
];

// ---------- 收银台挂单（未成单草稿：不生成订单、不扣库存、不占班次） ----------
const pos_held_orders = [
  {
    key: 'held-1', community_key: 'comm-1', operator_key: 'admin-1', operator_name: '系统管理员',
    user_key: 'user-1', user_name: '张三',
    items: [
      { sku_key: 'sku-1', product_name: '魔小鲜 大面筋辣条', spec_name: '小包106g', product_image: '/uploads/seed/products/latiao.png', price: 3.5, quantity: 1 },
      { sku_key: 'sku-3', product_name: '碧源 饮用天然水', spec_name: '550ml', product_image: '/uploads/seed/products/water.png', price: 2.0, quantity: 2 },
    ],
    total_amount: 7.5, remark: '顾客临时取物，挂单待结',
    created_at: { offset_minutes: -10 },
  },
];

// ---------- 订单样本（状态 × 配送方式 × 来源三维组合） ----------
// status：1待支付 2已支付 3备货中 4配送中 5已完成 6已取消 7退款中 8已退款
// delivery_type：1自提 2配送 3到店；source：1C端 2收银台代客
// 时间口径：待支付单 created 在 30 分钟超时窗内；配送中单 deliver_time 在 7 天
// 自动完成窗内；已完成单在售后窗口内（可直接演示 C 端申请退款）。
// 订单8：收银台改价 6.50→6.00（item.price 记改后价、original_price 留档案价、manual_discount=0）
const IMG_LATIAO = '/uploads/seed/products/latiao.png';
const IMG_WATER = '/uploads/seed/products/water.png';
const IMG_CHIPS = '/uploads/seed/products/chips.png';
const IMG_EGG = '/uploads/seed/products/egg-carton.png';
const orders = [
  {
    key: 'order-1', community_key: 'comm-1', order_no: 'SEED20261018001', user_key: 'user-1',
    status: 1, total_amount: 17.0, discount_amount: 0, delivery_fee: 3.0, pay_amount: 20.0,
    delivery_type: 2, delivery_time: '尽快送达', address_detail: '彩云大道 8 号 2 单元 301',
    contact_name: '张三', contact_phone: '13800138000', contact_address: '重庆市九龙坡区彩云大道 8 号 2 单元 301',
    remark: '辣条挑最新日期的', source: 1, created_at: { offset_minutes: -5 },
    items: [
      { sku_key: 'sku-1', product_name: '魔小鲜 大面筋辣条', spec_name: '小包106g', product_image: IMG_LATIAO, price: 3.5, quantity: 2, total_price: 7.0, original_price: 3.5, price_modified: 0 },
      { sku_key: 'sku-3', product_name: '碧源 饮用天然水', spec_name: '550ml', product_image: IMG_WATER, price: 2.0, quantity: 5, total_price: 10.0, original_price: 2.0, price_modified: 0 },
    ],
    status_logs: [
      { from_status: 0, to_status: 1, operator_type: 'user', operator_key: 'user-1', remark: '用户下单', created_at: { offset_minutes: -5 } },
    ],
  },
  {
    key: 'order-2', community_key: 'comm-1', order_no: 'SEED20261018002', user_key: 'user-1',
    status: 3, total_amount: 16.5, discount_amount: 0, delivery_fee: 0, pay_amount: 16.5,
    delivery_type: 1, delivery_time: '今天晚饭后自提', address_detail: '',
    contact_name: '张三', contact_phone: '13800138000', contact_address: '金茂悦7栋1楼保安处',
    pickup_code: '10000001', source: 1, created_at: { offset_minutes: -120 }, pay_time: { offset_minutes: -110 },
    items: [
      { sku_key: 'sku-4', product_name: '乐游 原味薯片', spec_name: '70g', product_image: IMG_CHIPS, price: 5.5, quantity: 3, total_price: 16.5, original_price: 5.5, price_modified: 0 },
    ],
    payments: [
      { pay_type: 1, amount: 16.5, transaction_id: 'SEED-WX-20261018-01', confirm_source: 1, confirm_ref: '', pay_time: { offset_minutes: -110 } },
    ],
    status_logs: [
      { from_status: 0, to_status: 1, operator_type: 'user', operator_key: 'user-1', remark: '用户下单', created_at: { offset_minutes: -120 } },
      { from_status: 1, to_status: 2, operator_type: 'user', operator_key: 'user-1', remark: '微信支付成功', created_at: { offset_minutes: -110 } },
      { from_status: 2, to_status: 3, operator_type: 'system', operator_key: '', remark: '支付完成进入备货', created_at: { offset_minutes: -105 } },
    ],
  },
  {
    key: 'order-3', community_key: 'comm-1', order_no: 'SEED20261018003', user_key: 'user-1',
    status: 5, total_amount: 20.0, discount_amount: 5.0, delivery_fee: 0, pay_amount: 15.0,
    delivery_type: 1, delivery_time: '周末自提', address_detail: '',
    contact_name: '张三', contact_phone: '13800138000', contact_address: '金茂悦7栋1楼保安处',
    used_coupon: { coupon_name: '新人满20减5券', expire_in_days: 30 },
    pickup_code: '10000002', source: 1, created_at: { offset_minutes: -2880 },
    pay_time: { offset_minutes: -2870 }, complete_time: { offset_minutes: -1440 },
    items: [
      { sku_key: 'sku-3', product_name: '碧源 饮用天然水', spec_name: '550ml', product_image: IMG_WATER, price: 2.0, quantity: 10, total_price: 20.0, original_price: 2.0, price_modified: 0 },
    ],
    payments: [
      { pay_type: 2, amount: 15.0, transaction_id: 'SEED-ALI-20261018-01', confirm_source: 1, confirm_ref: '', pay_time: { offset_minutes: -2870 } },
    ],
    status_logs: [
      { from_status: 0, to_status: 1, operator_type: 'user', operator_key: 'user-1', remark: '用户下单', created_at: { offset_minutes: -2880 } },
      { from_status: 1, to_status: 2, operator_type: 'user', operator_key: 'user-1', remark: '支付宝支付成功（抵扣新人券 5.00）', created_at: { offset_minutes: -2870 } },
      { from_status: 2, to_status: 3, operator_type: 'system', operator_key: '', remark: '支付完成进入备货', created_at: { offset_minutes: -2865 } },
      { from_status: 3, to_status: 5, operator_type: 'system', operator_key: '', remark: '取货码核销完成', created_at: { offset_minutes: -1440 } },
    ],
  },
  {
    key: 'order-4', community_key: 'comm-1', order_no: 'SEED20261018004', user_key: 'user-1',
    status: 6, total_amount: 16.5, discount_amount: 0, delivery_fee: 3.0, pay_amount: 19.5,
    delivery_type: 2, delivery_time: '尽快送达', address_detail: '彩云大道 8 号 2 单元 301',
    contact_name: '张三', contact_phone: '13800138000', contact_address: '重庆市九龙坡区彩云大道 8 号 2 单元 301',
    source: 1, created_at: { offset_minutes: -1440 }, cancel_time: { offset_minutes: -1425 },
    cancel_reason: '用户误下单，主动取消',
    items: [
      { sku_key: 'sku-4', product_name: '乐游 原味薯片', spec_name: '70g', product_image: IMG_CHIPS, price: 5.5, quantity: 3, total_price: 16.5, original_price: 5.5, price_modified: 0 },
    ],
    status_logs: [
      { from_status: 0, to_status: 1, operator_type: 'user', operator_key: 'user-1', remark: '用户下单', created_at: { offset_minutes: -1440 } },
      { from_status: 1, to_status: 6, operator_type: 'user', operator_key: 'user-1', remark: '用户取消：误下单', created_at: { offset_minutes: -1425 } },
    ],
  },
  {
    key: 'order-5', community_key: 'comm-2', order_no: 'SEED20261018005', user_key: 'user-2',
    status: 5, total_amount: 12.9, discount_amount: 0, delivery_fee: 0, pay_amount: 12.9,
    delivery_type: 3, delivery_time: '到店取货', address_detail: '',
    contact_name: '李四', contact_phone: '13900139000', contact_address: '花园南门快递柜旁自提点',
    source: 1, created_at: { offset_minutes: -180 }, pay_time: { offset_minutes: -120 }, complete_time: { offset_minutes: -120 },
    items: [
      { sku_key: 'sku-5', product_name: '农欢 散养鲜鸡蛋', spec_name: '30枚/箱', product_image: IMG_EGG, price: 12.9, quantity: 1, total_price: 12.9, original_price: 12.9, price_modified: 0 },
    ],
    payments: [
      { pay_type: 1, amount: 12.9, transaction_id: 'SEED-WX-20261019-01', confirm_source: 1, confirm_ref: '', pay_time: { offset_minutes: -120 } },
    ],
    status_logs: [
      { from_status: 0, to_status: 1, operator_type: 'user', operator_key: 'user-2', remark: '用户下单', created_at: { offset_minutes: -180 } },
      { from_status: 1, to_status: 2, operator_type: 'user', operator_key: 'user-2', remark: '微信支付成功', created_at: { offset_minutes: -120 } },
      { from_status: 2, to_status: 5, operator_type: 'system', operator_key: '', remark: '到店订单收款完成，自动完结', created_at: { offset_minutes: -120 } },
    ],
  },
  {
    key: 'order-6', community_key: 'comm-2', order_no: 'SEED20261018006', user_key: 'user-2',
    status: 1, total_amount: 25.8, discount_amount: 0, delivery_fee: 0, pay_amount: 25.8,
    delivery_type: 1, delivery_time: '明天上午自提', address_detail: '',
    contact_name: '李四', contact_phone: '13900139000', contact_address: '花园南门快递柜旁自提点',
    pickup_code: '20000001', source: 1, created_at: { offset_minutes: -5 },
    items: [
      { sku_key: 'sku-5', product_name: '农欢 散养鲜鸡蛋', spec_name: '30枚/箱', product_image: IMG_EGG, price: 12.9, quantity: 2, total_price: 25.8, original_price: 12.9, price_modified: 0 },
    ],
    status_logs: [
      { from_status: 0, to_status: 1, operator_type: 'user', operator_key: 'user-2', remark: '用户下单', created_at: { offset_minutes: -5 } },
    ],
  },
  {
    key: 'order-7', community_key: 'comm-2', order_no: 'SEED20261018007', user_key: 'user-2',
    status: 4, total_amount: 25.8, discount_amount: 0, delivery_fee: 2.0, pay_amount: 27.8,
    delivery_type: 2, delivery_time: '工作日晚间', address_detail: '阳光路 8 号 5 栋 1201',
    contact_name: '李四', contact_phone: '13900139000', contact_address: '重庆市南岸区阳光路 8 号 5 栋 1201',
    remark: '送货上门', express_company: '顺丰速运', express_no: 'SF1000000001',
    source: 1, created_at: { offset_minutes: -2880 }, pay_time: { offset_minutes: -2870 }, deliver_time: { offset_minutes: -60 },
    items: [
      { sku_key: 'sku-5', product_name: '农欢 散养鲜鸡蛋', spec_name: '30枚/箱', product_image: IMG_EGG, price: 12.9, quantity: 2, total_price: 25.8, original_price: 12.9, price_modified: 0 },
    ],
    payments: [
      { pay_type: 2, amount: 27.8, transaction_id: 'SEED-ALI-20261018-02', confirm_source: 1, confirm_ref: '', pay_time: { offset_minutes: -2870 } },
    ],
    status_logs: [
      { from_status: 0, to_status: 1, operator_type: 'user', operator_key: 'user-2', remark: '用户下单', created_at: { offset_minutes: -2880 } },
      { from_status: 1, to_status: 2, operator_type: 'user', operator_key: 'user-2', remark: '支付宝支付成功', created_at: { offset_minutes: -2870 } },
      { from_status: 2, to_status: 4, operator_type: 'system', operator_key: '', remark: '商家发货（顺丰速运 SF1000000001）', created_at: { offset_minutes: -60 } },
    ],
  },
  {
    key: 'order-8', community_key: 'comm-1', order_no: 'SEED20261018008', user_key: 'user-1',
    status: 5, total_amount: 6.0, discount_amount: 0, delivery_fee: 0, pay_amount: 6.0,
    delivery_type: 3, delivery_time: '到店取货', address_detail: '',
    contact_name: '张三', contact_phone: '13800138000', contact_address: '金茂悦7栋1楼保安处',
    remark: '收银台改价 6.50→6.00', source: 2, operator_key: 'admin-1', operator_name: '系统管理员',
    shift_key: 'shift-1', created_at: { day_offset: -1, minute_of_day: 602 },
    pay_time: { day_offset: -1, minute_of_day: 605 }, complete_time: { day_offset: -1, minute_of_day: 605 },
    items: [
      { sku_key: 'sku-2', product_name: '魔小鲜 大面筋辣条', spec_name: '大包226g', product_image: IMG_LATIAO, price: 6.0, quantity: 1, total_price: 6.0, original_price: 6.5, price_modified: 1 },
    ],
    payments: [
      { pay_type: 4, amount: 6.0, transaction_id: '', confirm_source: 4, confirm_ref: 'pos-cash', pay_time: { day_offset: -1, minute_of_day: 605 } },
    ],
    status_logs: [
      { from_status: 0, to_status: 1, operator_type: 'admin', operator_key: 'admin-1', remark: '收银台代客下单', created_at: { day_offset: -1, minute_of_day: 602 } },
      { from_status: 1, to_status: 2, operator_type: 'admin', operator_key: 'admin-1', remark: '收银台现金收款（改价 6.50→6.00）', created_at: { day_offset: -1, minute_of_day: 605 } },
      { from_status: 2, to_status: 5, operator_type: 'system', operator_key: '', remark: '到店订单收款完成，自动完结', created_at: { day_offset: -1, minute_of_day: 605 } },
    ],
  },
  {
    key: 'order-9', community_key: 'comm-1', order_no: 'SEED-TODAY-0009', user_key: 'user-1',
    status: 5, total_amount: 17.0, discount_amount: 0, delivery_fee: 3.0, pay_amount: 20.0,
    delivery_type: 2, delivery_time: '尽快送达', address_detail: '彩云大道 8 号 2 单元 301',
    contact_name: '张三', contact_phone: '13800138000', contact_address: '重庆市九龙坡区彩云大道 8 号 2 单元 301',
    remark: '今日配送单（种子，用于工作台今日经营数据）',
    express_company: '顺丰速运', express_no: 'SF1000000009',
    source: 1, created_at: { offset_minutes: -40 }, pay_time: { offset_minutes: -25 },
    deliver_time: { offset_minutes: -15 }, complete_time: { offset_minutes: -5 },
    items: [
      { sku_key: 'sku-1', product_name: '魔小鲜 大面筋辣条', spec_name: '小包106g', product_image: IMG_LATIAO, price: 3.5, quantity: 2, total_price: 7.0, original_price: 3.5, price_modified: 0 },
      { sku_key: 'sku-3', product_name: '碧源 饮用天然水', spec_name: '550ml', product_image: IMG_WATER, price: 2.0, quantity: 5, total_price: 10.0, original_price: 2.0, price_modified: 0 },
    ],
    payments: [
      { pay_type: 1, amount: 20.0, transaction_id: 'SEED-WX-TODAY-09', confirm_source: 1, confirm_ref: '', pay_time: { offset_minutes: -25 } },
    ],
    status_logs: [
      { from_status: 0, to_status: 1, operator_type: 'user', operator_key: 'user-1', remark: '用户下单', created_at: { offset_minutes: -40 } },
      { from_status: 1, to_status: 2, operator_type: 'user', operator_key: 'user-1', remark: '微信支付成功', created_at: { offset_minutes: -25 } },
      { from_status: 2, to_status: 3, operator_type: 'system', operator_key: '', remark: '支付完成进入备货', created_at: { offset_minutes: -24 } },
      { from_status: 3, to_status: 4, operator_type: 'system', operator_key: '', remark: '商家发货（顺丰速运 SF1000000009）', created_at: { offset_minutes: -15 } },
      { from_status: 4, to_status: 5, operator_type: 'user', operator_key: 'user-1', remark: '确认收货', created_at: { offset_minutes: -5 } },
    ],
  },
  {
    key: 'order-10', community_key: 'comm-1', order_no: 'SEED-REFUND-0010', user_key: 'user-1',
    status: 8, total_amount: 17.0, discount_amount: 0, delivery_fee: 3.0, pay_amount: 20.0,
    delivery_type: 2, delivery_time: '尽快送达', address_detail: '彩云大道 8 号 2 单元 301',
    contact_name: '张三', contact_phone: '13800138000', contact_address: '重庆市九龙坡区彩云大道 8 号 2 单元 301',
    remark: '整单退款：商品质量问题', express_company: '顺丰速运', express_no: 'SF1000000010',
    source: 1, created_at: { offset_minutes: -7200 }, pay_time: { offset_minutes: -7190 },
    deliver_time: { offset_minutes: -5760 }, complete_time: { offset_minutes: -5640 },
    items: [
      { sku_key: 'sku-1', product_name: '魔小鲜 大面筋辣条', spec_name: '小包106g', product_image: IMG_LATIAO, price: 3.5, quantity: 2, total_price: 7.0, original_price: 3.5, price_modified: 0 },
      { sku_key: 'sku-3', product_name: '碧源 饮用天然水', spec_name: '550ml', product_image: IMG_WATER, price: 2.0, quantity: 5, total_price: 10.0, original_price: 2.0, price_modified: 0 },
    ],
    payments: [
      { pay_type: 1, amount: 20.0, transaction_id: 'SEED-WX-REFUND-10', confirm_source: 1, confirm_ref: '', pay_time: { offset_minutes: -7190 } },
    ],
    status_logs: [
      { from_status: 0, to_status: 1, operator_type: 'user', operator_key: 'user-1', remark: '用户下单', created_at: { offset_minutes: -7200 } },
      { from_status: 1, to_status: 2, operator_type: 'user', operator_key: 'user-1', remark: '微信支付成功', created_at: { offset_minutes: -7190 } },
      { from_status: 2, to_status: 3, operator_type: 'system', operator_key: '', remark: '支付完成进入备货', created_at: { offset_minutes: -7185 } },
      { from_status: 3, to_status: 4, operator_type: 'system', operator_key: '', remark: '商家发货（顺丰速运 SF1000000010）', created_at: { offset_minutes: -5760 } },
      { from_status: 4, to_status: 5, operator_type: 'user', operator_key: 'user-1', remark: '确认收货', created_at: { offset_minutes: -5640 } },
      { from_status: 5, to_status: 7, operator_type: 'user', operator_key: 'user-1', remark: '申请退款: 商品质量问题', created_at: { offset_minutes: -4800 } },
      { from_status: 7, to_status: 8, operator_type: 'admin', operator_key: 'admin-1', remark: '退款完成', created_at: { offset_minutes: -4260 } },
    ],
  },
  {
    key: 'order-11', community_key: 'comm-1', order_no: 'SEED-REFUND-0011', user_key: 'user-1',
    status: 7, total_amount: 16.5, discount_amount: 0, delivery_fee: 0, pay_amount: 16.5,
    delivery_type: 1, delivery_time: '今天晚饭后自提', address_detail: '',
    contact_name: '张三', contact_phone: '13800138000', contact_address: '金茂悦7栋1楼保安处',
    remark: '部分商品缺货，申请退款', pickup_code: '10000011',
    source: 1, created_at: { offset_minutes: -1440 }, pay_time: { offset_minutes: -1430 },
    complete_time: { offset_minutes: -1200 },
    items: [
      { sku_key: 'sku-4', product_name: '乐游 原味薯片', spec_name: '70g', product_image: IMG_CHIPS, price: 5.5, quantity: 3, total_price: 16.5, original_price: 5.5, price_modified: 0 },
    ],
    payments: [
      { pay_type: 1, amount: 16.5, transaction_id: 'SEED-WX-REFUND-11', confirm_source: 1, confirm_ref: '', pay_time: { offset_minutes: -1430 } },
    ],
    status_logs: [
      { from_status: 0, to_status: 1, operator_type: 'user', operator_key: 'user-1', remark: '用户下单', created_at: { offset_minutes: -1440 } },
      { from_status: 1, to_status: 2, operator_type: 'user', operator_key: 'user-1', remark: '微信支付成功', created_at: { offset_minutes: -1430 } },
      { from_status: 2, to_status: 3, operator_type: 'system', operator_key: '', remark: '支付完成进入备货', created_at: { offset_minutes: -1425 } },
      { from_status: 3, to_status: 5, operator_type: 'system', operator_key: '', remark: '取货码核销完成', created_at: { offset_minutes: -1200 } },
      { from_status: 5, to_status: 7, operator_type: 'user', operator_key: 'user-1', remark: '申请退款: 部分商品缺货', created_at: { offset_minutes: -120 } },
    ],
  },
];

// ---------- 售后样本（依赖订单段；退款状态：1待审核 2已同意 3已拒绝 4退款中 5已退款） ----------
// 退款1：整单退款完成（金额 20.00 = 明细 17.00 + 运费 3.00）
// 退款2：部分退款（薯片 1 包 5.50），已审批进入渠道退款中
const refunds = [
  {
    key: 'refund-1', community_key: 'comm-1', refund_no: 'SEEDREFUND-0010', order_key: 'order-10',
    amount: 20.0, freight_amount: 3.0, type: 1, reason: '商品质量问题，申请整单退款',
    status: 5, operator_key: 'admin-1',
    refund_time: { offset_minutes: -4260 }, created_at: { offset_minutes: -4800 },
    items: [
      { order_key: 'order-10', item_index: 0, sku_key: 'sku-1', product_name: '魔小鲜 大面筋辣条', spec_name: '小包106g', quantity: 2, refund_amount: 7.0 },
      { order_key: 'order-10', item_index: 1, sku_key: 'sku-3', product_name: '碧源 饮用天然水', spec_name: '550ml', quantity: 5, refund_amount: 10.0 },
    ],
    logs: [
      { action: 'create', operator_type: 'user', operator_key: 'user-1', remark: '用户发起退款', created_at: { offset_minutes: -4800 } },
      { action: 'approve', operator_type: 'admin', operator_key: 'admin-1', remark: '同意退款', created_at: { offset_minutes: -4270 } },
    ],
    channel_payment: {
      status: 2, transaction_id: 'SEED-WX-RF-0010',
      created_at: { offset_minutes: -4270 }, refund_time: { offset_minutes: -4260 },
    },
  },
  {
    key: 'refund-2', community_key: 'comm-1', refund_no: 'SEEDREFUND-0011', order_key: 'order-11',
    amount: 5.5, freight_amount: 0, type: 2, reason: '部分商品缺货，申请退款',
    status: 4, operator_key: 'admin-1',
    last_checked_at: { offset_minutes: -60 }, created_at: { offset_minutes: -120 },
    items: [
      { order_key: 'order-11', item_index: 0, sku_key: 'sku-4', product_name: '乐游 原味薯片', spec_name: '70g', quantity: 1, refund_amount: 5.5 },
    ],
    logs: [
      { action: 'create', operator_type: 'user', operator_key: 'user-1', remark: '用户发起退款', created_at: { offset_minutes: -120 } },
      { action: 'approve', operator_type: 'admin', operator_key: 'admin-1', remark: '同意退款', created_at: { offset_minutes: -60 } },
    ],
    channel_payment: {
      status: 1, transaction_id: '',
      created_at: { offset_minutes: -60 }, refund_time: null,
    },
  },
];

// ---------- 进销存样本（与目录段批次咬合：薯片 50(A20+B20)+50(IN-SEED-0001) - 10 报损 = 90） ----------
const stock_in_orders = [
  {
    key: 'in-1', community_key: 'comm-1', in_no: 'IN-SEED-0001', supplier_name: '渝百快消品批发商行',
    type: 2, total_amount: 150.0, operator_key: 'admin-1', remark: '零食档期补货（直接入库，无采购单）',
    created_at: { offset_minutes: -7200 },
    items: [
      { sku_key: 'sku-4', quantity: 50, unit_cost: 3.0, batch_no: 'IN-SEED-0001', production_ago: 5, expire_in: 235 },
    ],
  },
];
const stock_out_orders = [
  {
    key: 'out-1', community_key: 'comm-1', out_no: 'OUT-SEED-0001', type: 1,
    operator_key: 'admin-1', remark: '薯片运输压碎报损', created_at: { offset_minutes: -4320 },
    sku_key: 'sku-4', quantity: 10,
  },
];
const stock_checks = [
  {
    key: 'check-1', community_key: 'comm-1', check_no: 'CHECK-SEED-0001', operator_key: 'admin-1',
    status: 1, remark: '月度抽盘（草稿，未提交）', created_at: { offset_minutes: -2880 },
    items: [
      { sku_key: 'sku-1', system_stock: 80, actual_stock: 80 },
      { sku_key: 'sku-4', system_stock: 90, actual_stock: 88 },
    ],
  },
];
const stock_records = [
  {
    key: 'rec-1', community_key: 'comm-1', sku_key: 'sku-4', type: 1, quantity: 50,
    before_stock: 50, after_stock: 100, batch_no: 'IN-SEED-0001',
    production_ago: 5, expire_in: 235, ref_no: 'IN-SEED-0001',
    operator_key: 'admin-1', remark: '直接入库', created_at: { offset_minutes: -7200 },
  },
  {
    key: 'rec-2', community_key: 'comm-1', sku_key: 'sku-4', type: 2, quantity: -10,
    before_stock: 100, after_stock: 90, batch_no: 'INIT-SNACK-CHIPS-70-A',
    production_ago: 25, expire_in: 215, ref_no: 'OUT-SEED-0001',
    operator_key: 'admin-1', remark: '薯片运输压碎报损', created_at: { offset_minutes: -4320 },
  },
];

const pkg = {
  key: 'full-demo',
  name: '社区便利店演示包',
  description: '演示环境整包数据：社区/账号/区划/用户（全局段，仅初始化导入）+ 15 分类 / 66 商品 / 72 SKU（含 FEFO 演示批次；两社区商品名全部唯一，应用中心数据集为全部商品：社区1 9类46品46SKU、社区2 6类20品21SKU，初始化仍按 community_key 归属各自社区）+ Banner + 满减/优惠券/秒杀/满免邮 + 用户券与购物车 + 订单/售后/收银/采购/进销存样本（仅初始化导入）',
  communities,
  regions,
  admins,
  users,
  site,
  ...catalog,
  banners,
  full_reductions,
  coupons,
  flash_sales,
  free_shippings,
  user_coupons,
  carts,
  suppliers,
  purchase_orders,
  pos_shifts,
  pos_held_orders,
  orders,
  refunds,
  stock_in_orders,
  stock_out_orders,
  stock_checks,
  stock_records,
};

// 归属自检：社区域条目必须显式标注 community_key（平台级 Banner 除外）——
// 漏标的条目会被灌进所有演示社区（同名不同价商品与重复发券互相冲突）
const scopedItems = [
  ...catalog.categories, ...catalog.products,
  ...banners.filter((b) => !b.platform),
  ...full_reductions, ...coupons, ...flash_sales, ...free_shippings, ...user_coupons, ...carts,
];
const unscoped = scopedItems.filter((x) => !x.community_key).map((x) => x.key);
if (unscoped.length) throw new Error('社区域条目缺少 community_key：' + unscoped.join(', '));

fs.mkdirSync(path.dirname(OUT_FILE), { recursive: true });
fs.writeFileSync(OUT_FILE, JSON.stringify(pkg, null, 1) + '\n');

const batchCount = pkg.skus.reduce((s, x) => s + x.batches.length, 0);
console.log(`written: ${OUT_FILE}`);
console.log(`communities=${pkg.communities.length} regions=${pkg.regions.length} admins=${pkg.admins.length} users=${pkg.users.length}`);
console.log(`categories=${pkg.categories.length} products=${pkg.products.length} skus=${pkg.skus.length} batches=${batchCount} banners=${pkg.banners.length}`);
console.log(`orders=${pkg.orders.length} refunds=${pkg.refunds.length} pos_shifts=${pkg.pos_shifts.length} pos_held=${pkg.pos_held_orders.length} suppliers=${pkg.suppliers.length} purchase_orders=${pkg.purchase_orders.length} stock_in=${pkg.stock_in_orders.length} stock_out=${pkg.stock_out_orders.length} stock_checks=${pkg.stock_checks.length} stock_records=${pkg.stock_records.length}`);
