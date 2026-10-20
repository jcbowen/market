-- 项目基础测试数据初始化脚本（开发环境专用）
-- 用于 docker compose 首次启动后填充示例数据，确保核心链路可跑通
--
-- 前提：本脚本只有 INSERT、不含任何建表语句，表结构一律由各微服务启动时经 GORM
-- AutoMigrate 生成（见 src/backend/services/*/main.go 的 autoMigrate），因此必须
-- 在服务建表完成之后执行；start-all.mjs 已按「等待表就绪 → 导入本脚本（库中已有
-- 数据则跳过）」的顺序自动处理，无需手工执行。
-- 禁止：生产环境不要导入本脚本，其中含弱口令管理员与测试业务数据。
--
-- 覆盖范围（与 2026-10 代码口径对齐）：
--   双社区 + 多社区管理员绑定（admin_communities）
--   商品划线价（product_skus.original_price + products.min_original_price）、
--   虚拟销量（virtual_sales）、汇总价格区间（min_price/max_price）、汇总库存（stock）
--   多规格商品链路（product_specs / product_spec_values / sku.spec_value_ids）
--   Banner 轮播、满减活动、优惠券（含水果分类范围 + 两张用户券：一张可用一张已用）
--   满免邮活动（含排除商品 scope）、购物车（含一条未选中）
--   供应商、在途采购单、已交班历史班次（仅历史，无进行中班次）
--   订单全状态样本（待支付/备货中/配送中/已完成/已取消 × 自提/配送/到店 × C端/收银台来源）
--   + 对应支付单（payments，admin 订单详情的支付方式来源）
--   行政区划 regions 演示子集（北京 + 广东/深圳/广州，仅供开发联调；
--   全量 ~3400 条请登录 admin 后台「行政区划 → 检查更新」自动同步）
--   注意：pay_config / refund_config 由服务启动时自举（id=1 默认行），勿在此插入
--
-- 数据口径注意：
--   待支付订单的 created_at 取 NOW() 附近（订单超时自动取消为 30 分钟，
--   见 bin/config*.yaml business.order_timeout=1800；超过窗口会被调度器改成已取消）
--   配送中订单的 deliver_time 取近期（自动完成为发货后 7 天，auto_complete_days=7）
--   pos_shifts 只插「已交班」历史班次：进行中班次会占用开工门禁并干扰收银台用例
--   本脚本为「仅新库生效」：start-all.mjs 见到 communities 有数据即整份跳过，
--   更新种子后需 docker compose down -v 重建，或对本文件新增段落手工执行一遍
--
-- 内置账号：
--   admin 后台：admin / admin123（超管，绑定社区1）；shequ / admin123（社区管理员，绑定社区2）
--   C 端用户：13800138000（社区1）、13900139000（社区2），登录验证码固定 123456

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- ============ 社区 ============
-- 社区1：重庆市九龙坡区（直辖市口径：无 4 位市级码，city_code 为空）
INSERT INTO `communities` (`id`, `name`, `address`, `longitude`, `latitude`, `contact_phone`, `manager_name`, `manager_phone`, `pickup_address`, `delivery_fee`, `min_order_amount`, `delivery_range_km`, `business_hours`, `province`, `city`, `district`, `province_code`, `city_code`, `district_code`, `allow_outside_delivery`, `sort`, `status`, `created_at`, `updated_at`)
VALUES (1, '盘龙金茂悦', '重庆市九龙坡区彩云大道8号', 106.492104, 29.506279, '17898616950', '万先生', '17898616950', '金茂悦7栋1楼保安处', 3.00, 15.00, 2.00, '[{"start":"08:00","end":"12:00"},{"start":"14:00","end":"22:00"}]', '重庆市', '重庆市', '九龙坡区', '50', '', '500107', 0, 0, 1, NOW(), NOW());

-- 社区2：广东省深圳市南山区（完整三级区划链 + 允许社区外配送，用于测试两种锁区分支）
INSERT INTO `communities` (`id`, `name`, `address`, `longitude`, `latitude`, `contact_phone`, `manager_name`, `manager_phone`, `pickup_address`, `delivery_fee`, `min_order_amount`, `delivery_range_km`, `business_hours`, `province`, `city`, `district`, `province_code`, `city_code`, `district_code`, `allow_outside_delivery`, `sort`, `status`, `created_at`, `updated_at`)
VALUES (2, '阳光花园', '广东省深圳市南山区阳光路 8 号', 113.9304, 22.5333, '0755-88886666', '李管家', '13900139002', '花园南门快递柜旁自提点', 2.00, 0.00, 3.00, '09:00-21:00', '广东省', '深圳市', '南山区', '44', '4403', '440305', 1, 0, 1, NOW(), NOW());

-- ============ 角色与管理员（多社区任职模型） ============
-- admin_roles.community_id：0=全局角色；>0=社区私有角色
-- 内置「社区管理员」角色每社区一条（code=community_admin）；超管判定看 admin_users.is_super
INSERT INTO `admin_roles` (`id`, `name`, `code`, `community_id`, `permissions`, `description`, `created_at`, `updated_at`)
VALUES
  (1, '超级管理员', 'super_admin', 0, '["*"]', '全局角色（仅兼容保留，超管判定看 is_super）', NOW(), NOW()),
  (2, '社区管理员', 'community_admin', 2, '["*"]', '内置角色：负责本社区的日常运营与员工管理', NOW(), NOW()),
  (3, '社区管理员', 'community_admin', 1, '["*"]', '内置角色：负责本社区的日常运营与员工管理', NOW(), NOW());

-- 密码均为 admin123（bcrypt）；admin_users.role_id/community_id 为旧模型遗留列，不再使用
INSERT INTO `admin_users` (`id`, `username`, `password`, `real_name`, `phone`, `is_super`, `status`, `last_login_at`, `last_login_ip`, `created_at`, `updated_at`)
VALUES
  (1, 'admin', '$2a$10$Ewx/FgR4tNyr1fUK5uo0aecXSVFh4JBvY3Xm7MaHSa77S38p9QtqC', '系统管理员', '13800138001', 1, 1, NULL, '', NOW(), NOW()),
  (2, 'shequ', '$2a$10$Ewx/FgR4tNyr1fUK5uo0aecXSVFh4JBvY3Xm7MaHSa77S38p9QtqC', '阳光花园管理员', '13800138003', 0, 1, NULL, '', NOW(), NOW());

-- 管理员社区任职（人在某社区担任某角色；超管无需任职，不受社区约束）
INSERT INTO `admin_communities` (`id`, `admin_id`, `community_id`, `role_id`)
VALUES
  (2, 2, 2, 2);

-- ============ 行政区划（演示子集，全量请走后台「检查更新」同步） ============
INSERT INTO `regions` (`code`, `name`, `parent_code`, `level`, `status`, `created_at`, `updated_at`)
VALUES
  ('11', '北京市', '0', 1, 1, NOW(), NOW()),
  ('44', '广东省', '0', 1, 1, NOW(), NOW()),
  ('4401', '广州市', '44', 2, 1, NOW(), NOW()),
  ('4403', '深圳市', '44', 2, 1, NOW(), NOW()),
  ('110101', '东城区', '11', 3, 1, NOW(), NOW()),
  ('110102', '西城区', '11', 3, 1, NOW(), NOW()),
  ('110105', '朝阳区', '11', 3, 1, NOW(), NOW()),
  ('110106', '丰台区', '11', 3, 1, NOW(), NOW()),
  ('110107', '石景山区', '11', 3, 1, NOW(), NOW()),
  ('110108', '海淀区', '11', 3, 1, NOW(), NOW()),
  ('110109', '门头沟区', '11', 3, 1, NOW(), NOW()),
  ('110111', '房山区', '11', 3, 1, NOW(), NOW()),
  ('110112', '通州区', '11', 3, 1, NOW(), NOW()),
  ('110113', '顺义区', '11', 3, 1, NOW(), NOW()),
  ('110114', '昌平区', '11', 3, 1, NOW(), NOW()),
  ('110115', '大兴区', '11', 3, 1, NOW(), NOW()),
  ('110116', '怀柔区', '11', 3, 1, NOW(), NOW()),
  ('110117', '平谷区', '11', 3, 1, NOW(), NOW()),
  ('110118', '密云区', '11', 3, 1, NOW(), NOW()),
  ('110119', '延庆区', '11', 3, 1, NOW(), NOW()),
  ('440103', '荔湾区', '4401', 3, 1, NOW(), NOW()),
  ('440104', '越秀区', '4401', 3, 1, NOW(), NOW()),
  ('440105', '海珠区', '4401', 3, 1, NOW(), NOW()),
  ('440106', '天河区', '4401', 3, 1, NOW(), NOW()),
  ('440111', '白云区', '4401', 3, 1, NOW(), NOW()),
  ('440113', '番禺区', '4401', 3, 1, NOW(), NOW()),
  ('440303', '罗湖区', '4403', 3, 1, NOW(), NOW()),
  ('440304', '福田区', '4403', 3, 1, NOW(), NOW()),
  ('440305', '南山区', '4403', 3, 1, NOW(), NOW()),
  ('440306', '宝安区', '4403', 3, 1, NOW(), NOW()),
  ('440307', '龙岗区', '4403', 3, 1, NOW(), NOW()),
  ('440308', '盐田区', '4403', 3, 1, NOW(), NOW()),
  ('440309', '龙华区', '4403', 3, 1, NOW(), NOW()),
  ('440310', '坪山区', '4403', 3, 1, NOW(), NOW()),
  ('440311', '光明区', '4403', 3, 1, NOW(), NOW());

-- ============ C 端用户 ============
INSERT INTO `users` (`id`, `openid`, `alipay_user_id`, `union_id`, `phone`, `nickname`, `avatar`, `gender`, `community_id`, `status`, `last_login_at`, `created_at`, `updated_at`)
VALUES
  (1, 'test_openid_1', NULL, NULL, '13800138000', '测试用户', 'https://placehold.co/100x100/orange/white?text=User', 0, 1, 1, NOW(), NOW(), NOW()),
  (2, 'test_openid_2', NULL, NULL, '13900139000', '阳光用户', 'https://placehold.co/100x100/blue/white?text=User2', 0, 2, 1, NOW(), NOW(), NOW());

-- 默认收货地址（前缀与所属社区锁定口径一致）
INSERT INTO `user_addresses` (`id`, `user_id`, `contact_name`, `contact_phone`, `province`, `city`, `district`, `province_code`, `city_code`, `district_code`, `detail`, `tag`, `community_id`, `is_default`, `created_at`, `updated_at`)
VALUES
  (1, 1, '张三', '13800138000', '北京市', '北京市', '朝阳区', '11', '', '110105', '幸福路 1 号 2 单元 301', '家', 1, 1, NOW(), NOW()),
  (2, 2, '李四', '13900139000', '广东省', '深圳市', '南山区', '44', '4403', '440305', '阳光路 8 号 5 栋 1201', '家', 2, 1, NOW(), NOW());

-- ============ 商品分类 ============
INSERT INTO `categories` (`id`, `community_id`, `parent_id`, `name`, `icon`, `sort`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, 0, '新鲜蔬菜', 'https://placehold.co/80x80/green/white?text=Veg', 1, 1, NOW(), NOW()),
  (2, 1, 0, '新鲜水果', 'https://placehold.co/80x80/orange/white?text=Fruit', 2, 1, NOW(), NOW()),
  (3, 2, 0, '优选好物', 'https://placehold.co/80x80/purple/white?text=Best', 1, 1, NOW(), NOW());

-- ============ 商品 ============
-- 汇总口径：min_price/max_price = SKU 价格区间；min_original_price = 最低价 SKU 的划线价（0 不展示）；
-- stock = 各启用 SKU 库存之和；展示销量 = sales_count + virtual_sales
-- 商品1 西红柿为多规格（重量：500g/1000g），其余为单规格
INSERT INTO `products` (`id`, `community_id`, `category_id`, `name`, `description`, `cover_image`, `images`, `unit`, `is_multi_spec`, `min_price`, `max_price`, `min_original_price`, `show_sales`, `virtual_sales`, `stock`, `min_buy`, `status`, `sort`, `sales_count`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, '西红柿', '现摘自然熟西红柿，酸甜多汁', 'https://placehold.co/300x300/red/white?text=Tomato', '["https://placehold.co/300x300/red/white?text=Tomato","https://placehold.co/300x300/red/white?text=Tomato2"]', '份', 1, 3.50, 6.50, 4.50, 1, 50, 120, 1, 1, 1, 0, NOW(), NOW()),
  (2, 1, 2, '红富士苹果', '山东烟台红富士，脆甜可口', 'https://placehold.co/300x300/red/white?text=Apple', '["https://placehold.co/300x300/red/white?text=Apple"]', '个', 0, 2.00, 2.00, 2.80, 1, 30, 200, 1, 1, 2, 0, NOW(), NOW()),
  (3, 1, 2, '海南香蕉', '软糯香甜，老少皆宜', 'https://placehold.co/300x300/yellow/black?text=Banana', '["https://placehold.co/300x300/yellow/black?text=Banana"]', '把', 0, 5.50, 5.50, 6.90, 1, 20, 150, 1, 1, 3, 0, NOW(), NOW()),
  (4, 2, 3, '散养土鸡蛋', '社区农户散养，30 枚装', 'https://placehold.co/300x300/brown/white?text=Egg', '["https://placehold.co/300x300/brown/white?text=Egg"]', '箱', 0, 12.90, 12.90, 15.90, 1, 10, 100, 1, 1, 1, 0, NOW(), NOW());

-- 商品图集：与 products.images 对应
INSERT INTO `product_images` (`id`, `product_id`, `url`, `sort`, `created_at`)
VALUES
  (1, 1, 'https://placehold.co/300x300/red/white?text=Tomato', 0, NOW()),
  (2, 1, 'https://placehold.co/300x300/red/white?text=Tomato2', 1, NOW()),
  (3, 2, 'https://placehold.co/300x300/red/white?text=Apple', 0, NOW()),
  (4, 3, 'https://placehold.co/300x300/yellow/black?text=Banana', 0, NOW()),
  (5, 4, 'https://placehold.co/300x300/brown/white?text=Egg', 0, NOW());

-- ============ 多规格链路（商品1：重量 500g/1000g） ============
-- sku.spec_value_ids 为规格值 ID 组合键（逗号分隔、升序），spec_json 为展示快照
INSERT INTO `product_specs` (`id`, `product_id`, `community_id`, `name`, `sort`, `created_at`, `updated_at`, `deleted_at`)
VALUES
  (1, 1, 1, '重量', 0, NOW(), NOW(), NULL);

INSERT INTO `product_spec_values` (`id`, `spec_id`, `product_id`, `community_id`, `name`, `sort`, `created_at`, `updated_at`, `deleted_at`)
VALUES
  (1, 1, 1, 1, '500g', 1, NOW(), NOW(), NULL),
  (2, 1, 1, 1, '1000g', 2, NOW(), NOW(), NULL);

-- ============ SKU ============
INSERT INTO `product_skus` (`id`, `product_id`, `community_id`, `sku_code`, `spec_value_ids`, `spec_name`, `spec_json`, `price`, `original_price`, `cost_price`, `stock`, `safety_stock`, `image`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, 'TOMATO-500G', '1', '500g', '{"重量":"500g"}', 3.50, 4.50, 2.00, 80, 10, 'https://placehold.co/300x300/red/white?text=Tomato', 1, NOW(), NOW()),
  (2, 1, 1, 'TOMATO-1000G', '2', '1000g', '{"重量":"1000g"}', 6.50, 8.00, 3.80, 40, 5, 'https://placehold.co/300x300/red/white?text=Tomato2', 1, NOW(), NOW()),
  (3, 2, 1, 'APPLE-1P', '', '单个约200g', '{"规格":"单个约200g"}', 2.00, 2.80, 1.20, 200, 20, 'https://placehold.co/300x300/red/white?text=Apple', 1, NOW(), NOW()),
  (4, 3, 1, 'BANANA-1B', '', '1把约500g', '{"规格":"1把约500g"}', 5.50, 6.90, 3.00, 150, 15, 'https://placehold.co/300x300/yellow/black?text=Banana', 1, NOW(), NOW()),
  (5, 4, 2, 'EGG-30P', '', '30枚/箱', '{"规格":"30枚/箱"}', 12.90, 15.90, 9.50, 100, 10, 'https://placehold.co/300x300/brown/white?text=Egg', 1, NOW(), NOW());

-- ============ 批次库存（FEFO 基础数据） ============
-- 每个种子 SKU 补一条初始批次，quantity 与 product_skus.stock 一致；
-- 否则订单扣减走 FEFO 会因「批次库存不足」失败（销售扣减只认 stock_batches）。
-- expire_date 部分留空（非限期品排最后扣）、部分给近期日期（用于演示临期预警与 FEFO 顺序）。
INSERT INTO `stock_batches` (`id`, `community_id`, `sku_id`, `batch_no`, `production_date`, `expire_date`, `quantity`, `unit_cost`, `status`, `source_type`, `ref_no`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, 'INIT-TOMATO-500G-A', '2026-09-20', '2026-10-25', 50, 2.00, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (2, 1, 1, 'INIT-TOMATO-500G-B', '2026-10-01', '2026-11-05', 30, 2.10, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (3, 1, 2, 'INIT-TOMATO-1000G-A', '2026-09-25', '2026-11-10', 40, 3.80, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (4, 1, 3, 'INIT-APPLE-1P-A', '2026-09-28', NULL, 200, 1.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (5, 1, 4, 'INIT-BANANA-1B-A', '2026-10-05', '2026-10-18', 100, 3.00, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (6, 1, 4, 'INIT-BANANA-1B-B', '2026-10-06', NULL, 50, 3.05, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (7, 2, 5, 'INIT-EGG-30P-A', '2026-10-01', '2026-11-01', 100, 9.50, 1, 1, 'INIT-SEED', NOW(), NOW());

-- ============ Banner 轮播 ============
-- position：1旧首页(已废弃) 2分类页(现首页)；community_id=0 表示全平台通用
INSERT INTO `banners` (`id`, `community_id`, `title`, `image_url`, `link_url`, `position`, `sort`, `status`, `start_time`, `end_time`, `created_at`, `updated_at`)
VALUES
  (1, 0, '新人专享满减', 'https://placehold.co/750x300/ff6b35/white?text=Banner1', '#', 2, 1, 1, NULL, NULL, NOW(), NOW()),
  (2, 0, '时令水果上新', 'https://placehold.co/750x300/4caf50/white?text=Banner2', '#', 2, 2, 1, NULL, NULL, NOW(), NOW()),
  (3, 2, '阳光花园精选', 'https://placehold.co/750x300/2196f3/white?text=Banner3', '#', 2, 1, 1, NULL, NULL, NOW(), NOW());

-- ============ 营销活动 ============
-- 满减：start/end 为 NULL 表示长期有效
INSERT INTO `full_reduction_rules` (`id`, `community_id`, `name`, `threshold`, `reduce_amount`, `start_time`, `end_time`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, '全场满30减5', 30.00, 5.00, NULL, NULL, 1, NOW(), NOW());

-- 优惠券：满20减5，长期可领，领取后 30 天有效；限定「新鲜水果」分类可用（见下方范围）
INSERT INTO `coupons` (`id`, `community_id`, `name`, `type`, `threshold`, `discount_value`, `total_count`, `received_count`, `used_count`, `per_user_limit`, `start_time`, `end_time`, `valid_days`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, '新人满20减5券', 1, 20.00, 5.00, 100, 2, 1, 2, NULL, NULL, 30, 1, NOW(), NOW());

-- 券1 的商品范围：仅「新鲜水果」分类（category_id=2）可用；
-- scope_type 枚举 1全场 2指定分类 3指定商品 4排除分类 5排除商品
INSERT INTO `coupon_product_scopes` (`id`, `coupon_id`, `scope_type`, `category_id`, `product_id`, `created_at`)
VALUES
  (1, 1, 2, 2, 0, NOW());

-- 用户1持有两张券：id=1 未使用（演示可用券）；id=2 已使用（挂订单3，见下方 orders）
INSERT INTO `user_coupons` (`id`, `user_id`, `coupon_id`, `name`, `type`, `threshold`, `discount_value`, `status`, `order_id`, `expire_at`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, '新人满20减5券', 1, 20.00, 5.00, 1, 0, DATE_ADD(NOW(), INTERVAL 30 DAY), NOW(), NOW()),
  (2, 1, 1, '新人满20减5券', 1, 20.00, 5.00, 2, 3, DATE_ADD(NOW(), INTERVAL 30 DAY), NOW(), NOW());

-- ============ 购物车（用户1，社区1；含一条未选中演示单选/全选） ============
INSERT INTO `carts` (`id`, `user_id`, `community_id`, `product_id`, `sku_id`, `quantity`, `selected`, `product_name`, `spec_name`, `product_image`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, 1, 1, 2, 1, '西红柿', '500g', 'https://placehold.co/300x300/red/white?text=Tomato', NOW(), NOW()),
  (2, 1, 1, 3, 4, 1, 0, '海南香蕉', '1把约500g', 'https://placehold.co/300x300/yellow/black?text=Banana', NOW(), NOW());

-- ============ 满免邮活动（社区1：满39免运费，排除「海南香蕉」演示不参与商品） ============
-- scope_type 枚举：4=排除分类 5=排除商品（当前仅排除语义）
INSERT INTO `free_shipping_activities` (`id`, `community_id`, `name`, `threshold`, `start_time`, `end_time`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, '满39元免配送费', 39.00, NULL, NULL, 1, NOW(), NOW());

INSERT INTO `free_shipping_scopes` (`id`, `activity_id`, `scope_type`, `category_id`, `product_id`, `created_at`)
VALUES
  (1, 1, 5, 0, 3, NOW());

-- ============ 供应商（社区1） ============
INSERT INTO `suppliers` (`id`, `community_id`, `name`, `contact_name`, `phone`, `address`, `remark`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, '绿源蔬菜种植合作社', '王老板', '023-68111111', '重庆市九龙坡区白市驿镇蔬菜基地', '长期合作，账期月结', 1, NOW(), NOW()),
  (2, 1, '鲜丰水果批发商行', '赵姐', '023-68222222', '重庆市江津区双福水果批发市场A区12号', '', 1, NOW(), NOW());

-- ============ 采购单（在途：已下单未入库；入库后由服务生成批次，此处不预置批次避免与现有 stock 汇总重复计数） ============
-- status 枚举：1草稿 2已下单 3部分入库 4已完结 5已取消
INSERT INTO `purchase_orders` (`id`, `community_id`, `purchase_no`, `supplier_id`, `status`, `total_amount`, `operator_id`, `remark`, `created_at`, `updated_at`)
VALUES
  (1, 1, 'PO20261018001', 1, 2, 260.00, 1, '秋季例行补货', NOW(), NOW());

INSERT INTO `purchase_order_items` (`id`, `purchase_order_id`, `sku_id`, `quantity`, `received_quantity`, `purchase_price`, `created_at`)
VALUES
  (1, 1, 1, 100, 0, 2.00, NOW()),
  (2, 1, 3, 50, 0, 1.20, NOW());

-- ============ 收银历史班次（已交班；绝不预置进行中班次，避免占用开工门禁） ============
-- statistics_json 为交班冻结快照（PosShiftStatistics），JSON key 为 proto 的 Go json tag
-- （snake_case：pay_type / total_income / cash_total / order_count），与 EndShift 里
-- json.Marshal(calcStatistics) 的输出格式一致，勿写成驼峰（解析不出会静默丢字段）
-- 与本班次内收银台订单（下方订单8，现金 6.00）对齐
INSERT INTO `pos_shifts` (`id`, `community_id`, `operator_id`, `operator_name`, `start_time`, `end_time`, `status`, `note`, `statistics_json`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, '系统管理员', '2026-10-18 09:00:00', '2026-10-18 21:00:00', 2, '种子数据：历史早班',
   '{"income":[{"pay_type":4,"amount":6.00,"order_count":1}],"refund":[],"total_income":6.00,"total_refund":0,"cash_total":6.00,"order_count":1}',
   '2026-10-18 09:00:00', '2026-10-18 21:00:00');

-- ============ 订单样本（覆盖状态 × 配送方式 × 来源三维组合） ============
-- status：1待支付 2已支付 3备货中 4配送中 5已完成 6已取消 7退款中 8已退款
-- delivery_type：1自提 2配送 3到店；source：1C端 2收银台代客
-- 时间口径：待支付单 created_at 在 30 分钟超时窗内（NOW()-5 分钟，防调度器自动取消）；
-- 配送中单 deliver_time 在 7 天自动完成窗内；历史单用固定日期
INSERT INTO `orders` (`id`, `order_no`, `community_id`, `user_id`, `status`, `total_amount`, `discount_amount`, `delivery_fee`, `pay_amount`, `delivery_type`, `delivery_time`, `address_id`, `contact_name`, `contact_phone`, `contact_address`, `remark`, `coupon_id`, `pickup_code`, `source`, `operator_id`, `operator_name`, `manual_discount`, `shift_id`, `express_company`, `express_no`, `pay_time`, `deliver_time`, `complete_time`, `cancel_time`, `cancel_reason`, `created_at`, `updated_at`)
VALUES
  -- 订单1：C端待支付·配送·无券（西红柿500g×2 + 苹果×1，9.00 + 运费3.00）
  (1, 'SEED20261018001', 1, 1, 1, 9.00, 0.00, 3.00, 12.00, 2, '尽快送达', 1, '张三', '13800138000', '北京市朝阳区幸福路 1 号 2 单元 301', '西红柿要新鲜的', 0, NULL, 1, 0, '', 0.00, 0, NULL, NULL, NULL, NULL, NULL, NULL, '', DATE_SUB(NOW(), INTERVAL 5 MINUTE), DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  -- 订单2：C端备货中·自提·微信支付（香蕉×2，11.00）
  (2, 'SEED20261018002', 1, 1, 3, 11.00, 0.00, 0.00, 11.00, 1, '今天晚饭后自提', 0, '张三', '13800138000', '金茂悦7栋1楼保安处', '', 0, '10000001', 1, 0, '', 0.00, 0, NULL, NULL, '2026-10-18 10:30:00', NULL, NULL, NULL, '', '2026-10-18 10:12:00', '2026-10-18 10:35:00'),
  -- 订单3：C端已完成·自提·支付宝·用券1（苹果×10，20.00 - 5.00 = 15.00；券仅限水果分类，此单为正例）
  (3, 'SEED20261018003', 1, 1, 5, 20.00, 5.00, 0.00, 15.00, 1, '周末自提', 0, '张三', '13800138000', '金茂悦7栋1楼保安处', '', 1, '10000002', 1, 0, '', 0.00, 0, NULL, NULL, '2026-10-18 11:00:00', NULL, '2026-10-18 19:20:00', NULL, '', '2026-10-18 10:48:00', '2026-10-18 19:20:00'),
  -- 订单4：C端已取消·配送（香蕉×1，5.50 + 3.00；用户主动取消）
  (4, 'SEED20261018004', 1, 1, 6, 5.50, 0.00, 3.00, 8.50, 2, '尽快送达', 1, '张三', '13800138000', '北京市朝阳区幸福路 1 号 2 单元 301', '', 0, NULL, 1, 0, '', 0.00, 0, NULL, NULL, NULL, NULL, NULL, '2026-10-18 12:05:00', '用户误下单，主动取消', '2026-10-18 11:50:00', '2026-10-18 12:05:00'),
  -- 订单5：C端已完成·到店·微信（土鸡蛋×1，12.90；到店收款即自动完结）
  (5, 'SEED20261018005', 2, 2, 5, 12.90, 0.00, 0.00, 12.90, 3, '到店取货', 0, '李四', '13900139000', '花园南门快递柜旁自提点', '', 0, NULL, 1, 0, '', 0.00, 0, NULL, NULL, '2026-10-19 09:15:00', NULL, '2026-10-19 09:15:00', NULL, '', '2026-10-19 09:10:00', '2026-10-19 09:15:00'),
  -- 订单6：C端待支付·自提（土鸡蛋×2，25.80；created_at 在超时窗内）
  (6, 'SEED20261018006', 2, 2, 1, 25.80, 0.00, 0.00, 25.80, 1, '明天上午自提', 0, '李四', '13900139000', '花园南门快递柜旁自提点', '', 0, '20000001', 1, 0, '', 0.00, 0, NULL, NULL, NULL, NULL, NULL, NULL, '', DATE_SUB(NOW(), INTERVAL 5 MINUTE), DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  -- 订单7：C端配送中·配送·支付宝（土鸡蛋×2，25.80 + 运费2.00；已发货，deliver_time 在自动完成窗内）
  (7, 'SEED20261018007', 2, 2, 4, 25.80, 0.00, 2.00, 27.80, 2, '工作日晚间', 2, '李四', '13900139000', '广东省深圳市南山区阳光路 8 号 5 栋 1201', '送货上门', 0, NULL, 1, 0, '', 0.00, 0, '顺丰速运', 'SF1000000001', DATE_SUB(NOW(), INTERVAL 2 DAY), DATE_SUB(NOW(), INTERVAL 1 HOUR), NULL, NULL, '', DATE_SUB(NOW(), INTERVAL 2 DAY), DATE_SUB(NOW(), INTERVAL 1 HOUR)),
  -- 订单8：收银台代客·到店·现金·已完成·挂历史班次1（西红柿1000g×1，收银台改价 6.50→6.00；
  -- 改价口径：item.price 记改后价、original_price 留档案价快照、manual_discount 为 0，与 AdminCreateOrder 一致）
  (8, 'SEED20261018008', 1, 1, 5, 6.00, 0.00, 0.00, 6.00, 3, '到店取货', 0, '张三', '13800138000', '金茂悦7栋1楼保安处', '收银台改价 6.50→6.00', 0, NULL, 2, 1, '系统管理员', 0.00, 1, NULL, NULL, '2026-10-18 10:05:00', NULL, '2026-10-18 10:05:00', NULL, '', '2026-10-18 10:02:00', '2026-10-18 10:05:00');

-- ============ 订单商品明细 ============
-- original_price/price_modified：收银台改价痕迹（订单8 抹零演示）；其余与 price 相同
INSERT INTO `order_items` (`id`, `order_id`, `product_id`, `sku_id`, `product_name`, `spec_name`, `product_image`, `price`, `quantity`, `total_price`, `original_price`, `price_modified`, `created_at`)
VALUES
  (1, 1, 1, 1, '西红柿', '500g', 'https://placehold.co/300x300/red/white?text=Tomato', 3.50, 2, 7.00, 3.50, 0, DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (2, 1, 2, 3, '红富士苹果', '单个约200g', 'https://placehold.co/300x300/red/white?text=Apple', 2.00, 1, 2.00, 2.00, 0, DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (3, 2, 3, 4, '海南香蕉', '1把约500g', 'https://placehold.co/300x300/yellow/black?text=Banana', 5.50, 2, 11.00, 5.50, 0, '2026-10-18 10:12:00'),
  (4, 3, 2, 3, '红富士苹果', '单个约200g', 'https://placehold.co/300x300/red/white?text=Apple', 2.00, 10, 20.00, 2.00, 0, '2026-10-18 10:48:00'),
  (5, 4, 3, 4, '海南香蕉', '1把约500g', 'https://placehold.co/300x300/yellow/black?text=Banana', 5.50, 1, 5.50, 5.50, 0, '2026-10-18 11:50:00'),
  (6, 5, 4, 5, '散养土鸡蛋', '30枚/箱', 'https://placehold.co/300x300/brown/white?text=Egg', 12.90, 1, 12.90, 12.90, 0, '2026-10-19 09:10:00'),
  (7, 6, 4, 5, '散养土鸡蛋', '30枚/箱', 'https://placehold.co/300x300/brown/white?text=Egg', 12.90, 2, 25.80, 12.90, 0, DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (8, 7, 4, 5, '散养土鸡蛋', '30枚/箱', 'https://placehold.co/300x300/brown/white?text=Egg', 12.90, 2, 25.80, 12.90, 0, DATE_SUB(NOW(), INTERVAL 2 DAY)),
  (9, 8, 1, 2, '西红柿', '1000g', 'https://placehold.co/300x300/red/white?text=Tomato2', 6.00, 1, 6.00, 6.50, 1, '2026-10-18 10:02:00');

-- ============ 支付单（admin 订单详情的支付方式来源；status：2支付成功） ============
-- pay_type：1微信 2支付宝 3货到付款 4收银台现金；transaction_id 现金单为空
INSERT INTO `payments` (`id`, `payment_no`, `order_id`, `user_id`, `pay_type`, `amount`, `status`, `transaction_id`, `pay_time`, `created_at`, `updated_at`)
VALUES
  (1, 'SEEDPAY20261018001', 2, 1, 1, 11.00, 2, 'SEED-WX-20261018-01', '2026-10-18 10:30:00', '2026-10-18 10:30:00', '2026-10-18 10:30:00'),
  (2, 'SEEDPAY20261018002', 3, 1, 2, 15.00, 2, 'SEED-ALI-20261018-01', '2026-10-18 11:00:00', '2026-10-18 11:00:00', '2026-10-18 11:00:00'),
  (3, 'SEEDPAY20261019001', 5, 2, 1, 12.90, 2, 'SEED-WX-20261019-01', '2026-10-19 09:15:00', '2026-10-19 09:15:00', '2026-10-19 09:15:00'),
  (4, 'SEEDPAY20261018003', 7, 2, 2, 27.80, 2, 'SEED-ALI-20261018-02', DATE_SUB(NOW(), INTERVAL 2 DAY), DATE_SUB(NOW(), INTERVAL 2 DAY), DATE_SUB(NOW(), INTERVAL 2 DAY)),
  (5, 'SEEDPAY20261018004', 8, 1, 4, 6.00, 2, NULL, '2026-10-18 10:05:00', '2026-10-18 10:05:00', '2026-10-18 10:05:00');

-- ============ 订单状态日志（operator_type：user / system / admin） ============
-- 注意：本表仅 created_at，无 updated_at（与 order_status_logs DDL 对齐）
INSERT INTO `order_status_logs` (`id`, `order_id`, `from_status`, `to_status`, `operator_type`, `operator_id`, `remark`, `created_at`)
VALUES
  (1, 1, 0, 1, 'user', 1, '用户下单', DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (2, 2, 0, 1, 'user', 1, '用户下单', '2026-10-18 10:12:00'),
  (3, 2, 1, 2, 'user', 1, '微信支付成功', '2026-10-18 10:30:00'),
  (4, 2, 2, 3, 'system', 0, '支付完成进入备货', '2026-10-18 10:35:00'),
  (5, 3, 0, 1, 'user', 1, '用户下单', '2026-10-18 10:48:00'),
  (6, 3, 1, 2, 'user', 1, '支付宝支付成功（抵扣新人券 5.00）', '2026-10-18 11:00:00'),
  (7, 3, 2, 3, 'system', 0, '支付完成进入备货', '2026-10-18 11:05:00'),
  (8, 3, 3, 5, 'system', 0, '取货码核销完成', '2026-10-18 19:20:00'),
  (9, 4, 0, 1, 'user', 1, '用户下单', '2026-10-18 11:50:00'),
  (10, 4, 1, 6, 'user', 1, '用户取消：误下单', '2026-10-18 12:05:00'),
  (11, 5, 0, 1, 'user', 2, '用户下单', '2026-10-19 09:10:00'),
  (12, 5, 1, 2, 'user', 2, '微信支付成功', '2026-10-19 09:15:00'),
  (13, 5, 2, 5, 'system', 0, '到店订单收款完成，自动完结', '2026-10-19 09:15:00'),
  (14, 6, 0, 1, 'user', 2, '用户下单', DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (15, 7, 0, 1, 'user', 2, '用户下单', DATE_SUB(NOW(), INTERVAL 2 DAY)),
  (16, 7, 1, 2, 'user', 2, '支付宝支付成功', DATE_SUB(NOW(), INTERVAL 2 DAY)),
  (17, 7, 2, 4, 'system', 0, '商家发货（顺丰速运 SF1000000001）', DATE_SUB(NOW(), INTERVAL 1 HOUR)),
  (18, 8, 0, 1, 'admin', 1, '收银台代客下单', '2026-10-18 10:02:00'),
  (19, 8, 1, 2, 'admin', 1, '收银台现金收款（改价 6.50→6.00）', '2026-10-18 10:05:00'),
  (20, 8, 2, 5, 'system', 0, '到店订单收款完成，自动完结', '2026-10-18 10:05:00');

SET FOREIGN_KEY_CHECKS = 1;
