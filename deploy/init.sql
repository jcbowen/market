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
--   Banner 轮播、满减活动、优惠券（含一张已领取的用户券）
--   行政区划 regions 演示子集（北京 + 广东/深圳/广州，仅供开发联调；
--   全量 ~3400 条请登录 admin 后台「行政区划 → 检查更新」自动同步）
--   注意：pay_config / refund_config 由服务启动时自举（id=1 默认行），勿在此插入
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
  (2, '社区管理员', 'community_admin', 2, '["*"]', '内置角色：负责本社区的日常运营与员工管理', NOW(), NOW());

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

-- 优惠券：满20减5，长期可领，领取后 30 天有效
INSERT INTO `coupons` (`id`, `community_id`, `name`, `type`, `threshold`, `discount_value`, `total_count`, `received_count`, `used_count`, `per_user_limit`, `start_time`, `end_time`, `valid_days`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, '新人满20减5券', 1, 20.00, 5.00, 100, 1, 0, 2, NULL, NULL, 30, 1, NOW(), NOW());

-- 用户1已领取一张未使用券
INSERT INTO `user_coupons` (`id`, `user_id`, `coupon_id`, `name`, `type`, `threshold`, `discount_value`, `status`, `order_id`, `expire_at`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, '新人满20减5券', 1, 20.00, 5.00, 1, 0, DATE_ADD(NOW(), INTERVAL 30 DAY), NOW(), NOW());

SET FOREIGN_KEY_CHECKS = 1;
