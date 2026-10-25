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
--   会员-社区关联（user_communities，会员列表按社区过滤与拉黑校验依赖；
--   必须显式插入——启动回填 BackfillUserCommunities 早于种子导入，覆盖不到种子用户）
--   商品划线价（product_skus.original_price + products.min_original_price）、
--   虚拟销量（virtual_sales）、汇总价格区间（min_price/max_price）、汇总库存（stock）
--   多规格商品链路（product_specs / product_spec_values / sku.spec_value_ids）
--   Banner 轮播、满减活动、优惠券（含水果分类范围 + 两张用户券：一张可用一张已用）
--   满免邮活动（含排除商品 scope）、购物车（含一条未选中）
--   秒杀活动一场进行中（flash_sales / flash_sale_items，NOW() 相对时间窗 + 独立库存口径）
--   站点设置（site_configs 单行演示配置：站名/页脚/SEO，base_url 留空按请求现推；
--   该表读取时若无行会惰性创建空默认行，种子行仅为开箱即见品牌位，非必需）
--   供应商、在途采购单、已交班历史班次（仅历史，无进行中班次）
--   订单全状态样本（待支付/备货中/配送中/已完成/已取消/退款中/已退款 ×
--   自提/配送/到店 × C端/收银台来源）+ 对应支付单（payments）
--   售后样本（refunds / refund_items / refund_logs / refund_payments）
--   进销存样本（直接入库单、报损出库单、盘点单、库存流水 stock_records）
--   收银台挂单（pos_held_orders，不占班次、不扣库存）
--   行政区划 regions 仅保留重庆市（1 省级 + 37 区县，按官方同步口径）；
--   需要全国数据时登录 admin 后台「行政区划 → 检查更新」同步
--   注意：pay_config / refund_config / client_channels 由服务启动时自举（幂等种子行），
--   勿在此插入
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

-- 社区2：重庆市南岸区（直辖市口径 + 允许社区外配送，用于测试两种锁区分支）
INSERT INTO `communities` (`id`, `name`, `address`, `longitude`, `latitude`, `contact_phone`, `manager_name`, `manager_phone`, `pickup_address`, `delivery_fee`, `min_order_amount`, `delivery_range_km`, `business_hours`, `province`, `city`, `district`, `province_code`, `city_code`, `district_code`, `allow_outside_delivery`, `sort`, `status`, `created_at`, `updated_at`)
VALUES (2, '阳光花园', '重庆市南岸区阳光路 8 号', 106.5716, 29.5218, '023-88886666', '李管家', '13900139002', '花园南门快递柜旁自提点', 2.00, 0.00, 3.00, '09:00-21:00', '重庆市', '重庆市', '南岸区', '50', '', '500108', 1, 0, 1, NOW(), NOW());

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

-- ============ 行政区划（仅重庆市，按官方同步口径） ============
-- 演示环境只保留重庆，避免地址选择器出现大量无关省份；
-- 全国数据由后台「行政区划 → 检查更新」从上游同步，种子不预置其他省份。
-- 口径说明：直辖市无 4 位市级代码，区县直接挂在省级下（level=3）。
INSERT INTO `regions` (`code`, `name`, `parent_code`, `level`, `status`, `created_at`, `updated_at`)
VALUES
  ('50', '重庆市', '0', 1, 1, NOW(), NOW()),
  ('500101', '万州区', '50', 3, 1, NOW(), NOW()),
  ('500102', '涪陵区', '50', 3, 1, NOW(), NOW()),
  ('500103', '渝中区', '50', 3, 1, NOW(), NOW()),
  ('500104', '大渡口区', '50', 3, 1, NOW(), NOW()),
  ('500106', '沙坪坝区', '50', 3, 1, NOW(), NOW()),
  ('500107', '九龙坡区', '50', 3, 1, NOW(), NOW()),
  ('500108', '南岸区', '50', 3, 1, NOW(), NOW()),
  ('500109', '北碚区', '50', 3, 1, NOW(), NOW()),
  ('500110', '綦江区', '50', 3, 1, NOW(), NOW()),
  ('500111', '大足区', '50', 3, 1, NOW(), NOW()),
  ('500113', '巴南区', '50', 3, 1, NOW(), NOW()),
  ('500114', '黔江区', '50', 3, 1, NOW(), NOW()),
  ('500115', '长寿区', '50', 3, 1, NOW(), NOW()),
  ('500116', '江津区', '50', 3, 1, NOW(), NOW()),
  ('500117', '合川区', '50', 3, 1, NOW(), NOW()),
  ('500118', '永川区', '50', 3, 1, NOW(), NOW()),
  ('500119', '南川区', '50', 3, 1, NOW(), NOW()),
  ('500120', '璧山区', '50', 3, 1, NOW(), NOW()),
  ('500151', '铜梁区', '50', 3, 1, NOW(), NOW()),
  ('500152', '潼南区', '50', 3, 1, NOW(), NOW()),
  ('500153', '荣昌区', '50', 3, 1, NOW(), NOW()),
  ('500154', '开州区', '50', 3, 1, NOW(), NOW()),
  ('500155', '梁平区', '50', 3, 1, NOW(), NOW()),
  ('500156', '武隆区', '50', 3, 1, NOW(), NOW()),
  ('500157', '两江新区', '50', 3, 1, NOW(), NOW()),
  ('500229', '城口县', '50', 3, 1, NOW(), NOW()),
  ('500230', '丰都县', '50', 3, 1, NOW(), NOW()),
  ('500231', '垫江县', '50', 3, 1, NOW(), NOW()),
  ('500233', '忠县', '50', 3, 1, NOW(), NOW()),
  ('500235', '云阳县', '50', 3, 1, NOW(), NOW()),
  ('500236', '奉节县', '50', 3, 1, NOW(), NOW()),
  ('500237', '巫山县', '50', 3, 1, NOW(), NOW()),
  ('500238', '巫溪县', '50', 3, 1, NOW(), NOW()),
  ('500240', '石柱土家族自治县', '50', 3, 1, NOW(), NOW()),
  ('500241', '秀山土家族苗族自治县', '50', 3, 1, NOW(), NOW()),
  ('500242', '酉阳土家族苗族自治县', '50', 3, 1, NOW(), NOW()),
  ('500243', '彭水苗族土家族自治县', '50', 3, 1, NOW(), NOW());

-- ============ C 端用户 ============
INSERT INTO `users` (`id`, `openid`, `alipay_user_id`, `union_id`, `phone`, `nickname`, `avatar`, `gender`, `community_id`, `status`, `last_login_at`, `created_at`, `updated_at`)
VALUES
  (1, 'test_openid_1', NULL, NULL, '13800138000', '测试用户', 'https://placehold.co/100x100/orange/white?text=User', 0, 1, 1, NOW(), NOW(), NOW()),
  (2, 'test_openid_2', NULL, NULL, '13900139000', '阳光用户', 'https://placehold.co/100x100/blue/white?text=User2', 0, 2, 1, NOW(), NOW(), NOW());

-- 默认收货地址（前缀与所属社区锁定口径一致）
INSERT INTO `user_addresses` (`id`, `user_id`, `contact_name`, `contact_phone`, `province`, `city`, `district`, `province_code`, `city_code`, `district_code`, `detail`, `tag`, `community_id`, `is_default`, `created_at`, `updated_at`)
VALUES
  (1, 1, '张三', '13800138000', '重庆市', '重庆市', '九龙坡区', '50', '', '500107', '彩云大道 8 号 2 单元 301', '家', 1, 1, NOW(), NOW()),
  (2, 2, '李四', '13900139000', '重庆市', '重庆市', '南岸区', '50', '', '500108', '阳光路 8 号 5 栋 1201', '家', 2, 1, NOW(), NOW());

-- 会员-社区关联（多社区会员模型）：会员列表按社区过滤与拉黑校验均依赖此表。
-- 必须显式插入：启动回填 BackfillUserCommunities 在 user 服务启动时执行，早于种子导入，
-- 种子用户永远轮不到回填；source 取值演示两种真实来源（order 下单 / address 收货地址）
INSERT INTO `user_communities` (`id`, `user_id`, `community_id`, `source`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, 'order', NOW(), NOW()),
  (2, 2, 2, 'address', NOW(), NOW());

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
-- 各 SKU 批次 quantity 之和等于 product_skus.stock；否则订单扣减走 FEFO 会因
-- 「批次库存不足」失败（销售扣减只认 stock_batches）。
-- 日期一律用相对时间：部分留空（非限期品排最后扣）、部分给临期日期
-- （用于演示临期预警与 FEFO 顺序），避免种子过期后演示失真。
-- 苹果拆两批：INIT-APPLE-1P-A 为初始库存（已扣报损 10），IN-SEED-0001 为入库单
-- 新增批次，合计 150 + 50 = 200，与 SKU 库存一致。
-- 历史订单的销售扣减与退款回补不回放（种子是当前库存快照），
-- 仅单据类变动（入库单/出库单）在 stock_records 中留痕。
INSERT INTO `stock_batches` (`id`, `community_id`, `sku_id`, `batch_no`, `production_date`, `expire_date`, `quantity`, `unit_cost`, `status`, `source_type`, `ref_no`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, 'INIT-TOMATO-500G-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 35 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 2 DAY), '%Y-%m-%d'), 50, 2.00, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (2, 1, 1, 'INIT-TOMATO-500G-B', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 24 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 12 DAY), '%Y-%m-%d'), 30, 2.10, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (3, 1, 2, 'INIT-TOMATO-1000G-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 30 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 16 DAY), '%Y-%m-%d'), 40, 3.80, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (4, 1, 3, 'INIT-APPLE-1P-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 27 DAY), '%Y-%m-%d'), NULL, 150, 1.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (5, 1, 4, 'INIT-BANANA-1B-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 20 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 3 DAY), '%Y-%m-%d'), 100, 3.00, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (6, 1, 4, 'INIT-BANANA-1B-B', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 19 DAY), '%Y-%m-%d'), NULL, 50, 3.05, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (7, 2, 5, 'INIT-EGG-30P-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 24 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 7 DAY), '%Y-%m-%d'), 100, 9.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (8, 1, 3, 'IN-SEED-0001', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 5 DAY), '%Y-%m-%d'), NULL, 50, 1.20, 1, 1, 'IN-SEED-0001', DATE_SUB(NOW(), INTERVAL 5 DAY), DATE_SUB(NOW(), INTERVAL 5 DAY));

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

-- ============ 秒杀活动（社区1：一场进行中，C 端 /flash-sales 与管理端列表演示） ============
-- 时间窗必须用 NOW() 相对时间：活动按时间窗展示，固定日期会过期失效、NULL 会永不开始；
-- flash_sale_items.stock 为秒杀独立库存，下单直接扣减此处（库存流水 op_type 3/4），
-- 与 stock_batches / product_skus.stock 无对齐关系，不存在批次约束
INSERT INTO `flash_sales` (`id`, `community_id`, `name`, `start_time`, `end_time`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, '限时秒杀·生鲜特惠', DATE_SUB(NOW(), INTERVAL 1 HOUR), DATE_ADD(NOW(), INTERVAL 2 DAY), 1, NOW(), NOW());

-- 秒杀价低于 SKU 原价演示抢购场景：西红柿500g 3.50→2.99、苹果 2.00→1.69
INSERT INTO `flash_sale_items` (`id`, `flash_sale_id`, `sku_id`, `flash_price`, `stock`, `sold_count`, `limit_per_user`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, 2.99, 50, 3, 2, 1, NOW(), NOW()),
  (2, 1, 3, 1.69, 100, 12, 5, 1, NOW(), NOW());

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
-- 与本班次内收银台订单（下方订单8，现金 6.00）对齐；班次日期取「昨天 09:00-21:00」，
-- 避免固定日期随种子老化后与订单、报表脱节
INSERT INTO `pos_shifts` (`id`, `community_id`, `operator_id`, `operator_name`, `start_time`, `end_time`, `status`, `note`, `statistics_json`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, '系统管理员',
   DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 540 MINUTE),
   DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 1260 MINUTE), 2, '种子数据：历史早班',
   '{"income":[{"pay_type":4,"amount":6.00,"order_count":1}],"refund":[],"total_income":6.00,"total_refund":0,"cash_total":6.00,"order_count":1}',
   DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 540 MINUTE),
   DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 1260 MINUTE));

-- ============ 订单样本（覆盖状态 × 配送方式 × 来源三维组合） ============
-- status：1待支付 2已支付 3备货中 4配送中 5已完成 6已取消 7退款中 8已退款
-- delivery_type：1自提 2配送 3到店；source：1C端 2收银台代客
-- 时间口径：待支付单 created_at 在 30 分钟超时窗内（NOW()-5 分钟，防调度器自动取消）；
-- 配送中单 deliver_time 在 7 天自动完成窗内；其余时间一律相对 NOW()，
-- 避免种子放久后「已完成单超出 7 天售后窗口」「配送中单被自动完成」导致演示失真。
INSERT INTO `orders` (`id`, `order_no`, `community_id`, `user_id`, `status`, `total_amount`, `discount_amount`, `delivery_fee`, `pay_amount`, `delivery_type`, `delivery_time`, `address_id`, `contact_name`, `contact_phone`, `contact_address`, `remark`, `coupon_id`, `pickup_code`, `source`, `operator_id`, `operator_name`, `manual_discount`, `shift_id`, `express_company`, `express_no`, `pay_time`, `deliver_time`, `complete_time`, `cancel_time`, `cancel_reason`, `created_at`, `updated_at`)
VALUES
  -- 订单1：C端待支付·配送·无券（西红柿500g×2 + 苹果×5，17.00 + 运费3.00，满足 15 元起送）
  (1, 'SEED20261018001', 1, 1, 1, 17.00, 0.00, 3.00, 20.00, 2, '尽快送达', 1, '张三', '13800138000', '重庆市九龙坡区彩云大道 8 号 2 单元 301', '西红柿要新鲜的', 0, NULL, 1, 0, '', 0.00, 0, NULL, NULL, NULL, NULL, NULL, NULL, '', DATE_SUB(NOW(), INTERVAL 5 MINUTE), DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  -- 订单2：C端备货中·自提·微信支付（香蕉×3，16.50，满足 15 元起送）
  (2, 'SEED20261018002', 1, 1, 3, 16.50, 0.00, 0.00, 16.50, 1, '今天晚饭后自提', 0, '张三', '13800138000', '金茂悦7栋1楼保安处', '', 0, '10000001', 1, 0, '', 0.00, 0, NULL, NULL, DATE_SUB(NOW(), INTERVAL 110 MINUTE), NULL, NULL, NULL, '', DATE_SUB(NOW(), INTERVAL 120 MINUTE), DATE_SUB(NOW(), INTERVAL 105 MINUTE)),
  -- 订单3：C端已完成·自提·支付宝·用券1（苹果×10，20.00 - 5.00 = 15.00；券仅限水果分类，此单为正例）
  -- 完成时间取 1 天前，处于默认 168h 售后窗口内，可直接演示 C 端申请退款
  (3, 'SEED20261018003', 1, 1, 5, 20.00, 5.00, 0.00, 15.00, 1, '周末自提', 0, '张三', '13800138000', '金茂悦7栋1楼保安处', '', 1, '10000002', 1, 0, '', 0.00, 0, NULL, NULL, DATE_SUB(NOW(), INTERVAL 2870 MINUTE), NULL, DATE_SUB(NOW(), INTERVAL 1440 MINUTE), NULL, '', DATE_SUB(NOW(), INTERVAL 2880 MINUTE), DATE_SUB(NOW(), INTERVAL 1440 MINUTE)),
  -- 订单4：C端已取消·配送（香蕉×3，16.50 + 3.00；用户主动取消）
  (4, 'SEED20261018004', 1, 1, 6, 16.50, 0.00, 3.00, 19.50, 2, '尽快送达', 1, '张三', '13800138000', '重庆市九龙坡区彩云大道 8 号 2 单元 301', '', 0, NULL, 1, 0, '', 0.00, 0, NULL, NULL, NULL, NULL, NULL, DATE_SUB(NOW(), INTERVAL 1425 MINUTE), '用户误下单，主动取消', DATE_SUB(NOW(), INTERVAL 1440 MINUTE), DATE_SUB(NOW(), INTERVAL 1425 MINUTE)),
  -- 订单5：C端已完成·到店·微信（土鸡蛋×1，12.90；到店收款即自动完结）
  (5, 'SEED20261018005', 2, 2, 5, 12.90, 0.00, 0.00, 12.90, 3, '到店取货', 0, '李四', '13900139000', '花园南门快递柜旁自提点', '', 0, NULL, 1, 0, '', 0.00, 0, NULL, NULL, DATE_SUB(NOW(), INTERVAL 120 MINUTE), NULL, DATE_SUB(NOW(), INTERVAL 120 MINUTE), NULL, '', DATE_SUB(NOW(), INTERVAL 180 MINUTE), DATE_SUB(NOW(), INTERVAL 120 MINUTE)),
  -- 订单6：C端待支付·自提（土鸡蛋×2，25.80；created_at 在超时窗内）
  (6, 'SEED20261018006', 2, 2, 1, 25.80, 0.00, 0.00, 25.80, 1, '明天上午自提', 0, '李四', '13900139000', '花园南门快递柜旁自提点', '', 0, '20000001', 1, 0, '', 0.00, 0, NULL, NULL, NULL, NULL, NULL, NULL, '', DATE_SUB(NOW(), INTERVAL 5 MINUTE), DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  -- 订单7：C端配送中·配送·支付宝（土鸡蛋×2，25.80 + 运费2.00；已发货，deliver_time 在自动完成窗内）
  (7, 'SEED20261018007', 2, 2, 4, 25.80, 0.00, 2.00, 27.80, 2, '工作日晚间', 2, '李四', '13900139000', '重庆市南岸区阳光路 8 号 5 栋 1201', '送货上门', 0, NULL, 1, 0, '', 0.00, 0, '顺丰速运', 'SF1000000001', DATE_SUB(NOW(), INTERVAL 2870 MINUTE), DATE_SUB(NOW(), INTERVAL 60 MINUTE), NULL, NULL, '', DATE_SUB(NOW(), INTERVAL 2880 MINUTE), DATE_SUB(NOW(), INTERVAL 60 MINUTE)),
  -- 订单8：收银台代客·到店·现金·已完成·挂历史班次1（西红柿1000g×1，收银台改价 6.50→6.00；
  -- 改价口径：item.price 记改后价、original_price 留档案价快照、manual_discount 为 0，与 AdminCreateOrder 一致）
  (8, 'SEED20261018008', 1, 1, 5, 6.00, 0.00, 0.00, 6.00, 3, '到店取货', 0, '张三', '13800138000', '金茂悦7栋1楼保安处', '收银台改价 6.50→6.00', 0, NULL, 2, 1, '系统管理员', 0.00, 1, NULL, NULL, DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 605 MINUTE), NULL, DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 605 MINUTE), NULL, '', DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 602 MINUTE), DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 605 MINUTE)),
  -- 订单9：C端已完成·配送·微信（今日经营数据：西红柿500g×2 + 苹果×5，17.00 + 3.00）
  (9, 'SEED-TODAY-0009', 1, 1, 5, 17.00, 0.00, 3.00, 20.00, 2, '尽快送达', 1, '张三', '13800138000', '重庆市九龙坡区彩云大道 8 号 2 单元 301', '今日配送单（种子，用于工作台今日经营数据）', 0, NULL, 1, 0, '', 0.00, 0, '顺丰速运', 'SF1000000009', DATE_SUB(NOW(), INTERVAL 25 MINUTE), DATE_SUB(NOW(), INTERVAL 15 MINUTE), DATE_SUB(NOW(), INTERVAL 5 MINUTE), NULL, '', DATE_SUB(NOW(), INTERVAL 40 MINUTE), DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  -- 订单10：C端已退款·配送·微信（整单退款完成，售后列表/详情样本）
  (10, 'SEED-REFUND-0010', 1, 1, 8, 17.00, 0.00, 3.00, 20.00, 2, '尽快送达', 1, '张三', '13800138000', '重庆市九龙坡区彩云大道 8 号 2 单元 301', '整单退款：商品质量问题', 0, NULL, 1, 0, '', 0.00, 0, '顺丰速运', 'SF1000000010', DATE_SUB(NOW(), INTERVAL 7190 MINUTE), DATE_SUB(NOW(), INTERVAL 5760 MINUTE), DATE_SUB(NOW(), INTERVAL 5640 MINUTE), NULL, '', DATE_SUB(NOW(), INTERVAL 7200 MINUTE), DATE_SUB(NOW(), INTERVAL 4260 MINUTE)),
  -- 订单11：C端退款中·自提·微信（退款已审核，渠道退款处理中）
  (11, 'SEED-REFUND-0011', 1, 1, 7, 16.50, 0.00, 0.00, 16.50, 1, '今天晚饭后自提', 0, '张三', '13800138000', '金茂悦7栋1楼保安处', '部分商品缺货，申请退款', 0, '10000011', 1, 0, '', 0.00, 0, NULL, NULL, DATE_SUB(NOW(), INTERVAL 1430 MINUTE), NULL, DATE_SUB(NOW(), INTERVAL 1200 MINUTE), NULL, '', DATE_SUB(NOW(), INTERVAL 1440 MINUTE), DATE_SUB(NOW(), INTERVAL 60 MINUTE));

-- ============ 订单商品明细 ============
-- original_price/price_modified：收银台改价痕迹（订单8 抹零演示）；其余与 price 相同
-- 明细小计之和 = 订单 total_amount（运费与优惠单列在 orders，不摊入明细）
INSERT INTO `order_items` (`id`, `order_id`, `product_id`, `sku_id`, `product_name`, `spec_name`, `product_image`, `price`, `quantity`, `total_price`, `original_price`, `price_modified`, `created_at`)
VALUES
  (1, 1, 1, 1, '西红柿', '500g', 'https://placehold.co/300x300/red/white?text=Tomato', 3.50, 2, 7.00, 3.50, 0, DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (2, 1, 2, 3, '红富士苹果', '单个约200g', 'https://placehold.co/300x300/red/white?text=Apple', 2.00, 5, 10.00, 2.00, 0, DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (3, 2, 3, 4, '海南香蕉', '1把约500g', 'https://placehold.co/300x300/yellow/black?text=Banana', 5.50, 3, 16.50, 5.50, 0, DATE_SUB(NOW(), INTERVAL 120 MINUTE)),
  (4, 3, 2, 3, '红富士苹果', '单个约200g', 'https://placehold.co/300x300/red/white?text=Apple', 2.00, 10, 20.00, 2.00, 0, DATE_SUB(NOW(), INTERVAL 2880 MINUTE)),
  (5, 4, 3, 4, '海南香蕉', '1把约500g', 'https://placehold.co/300x300/yellow/black?text=Banana', 5.50, 3, 16.50, 5.50, 0, DATE_SUB(NOW(), INTERVAL 1440 MINUTE)),
  (6, 5, 4, 5, '散养土鸡蛋', '30枚/箱', 'https://placehold.co/300x300/brown/white?text=Egg', 12.90, 1, 12.90, 12.90, 0, DATE_SUB(NOW(), INTERVAL 180 MINUTE)),
  (7, 6, 4, 5, '散养土鸡蛋', '30枚/箱', 'https://placehold.co/300x300/brown/white?text=Egg', 12.90, 2, 25.80, 12.90, 0, DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (8, 7, 4, 5, '散养土鸡蛋', '30枚/箱', 'https://placehold.co/300x300/brown/white?text=Egg', 12.90, 2, 25.80, 12.90, 0, DATE_SUB(NOW(), INTERVAL 2880 MINUTE)),
  (9, 8, 1, 2, '西红柿', '1000g', 'https://placehold.co/300x300/red/white?text=Tomato2', 6.00, 1, 6.00, 6.50, 1, DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 602 MINUTE)),
  (10, 9, 1, 1, '西红柿', '500g', 'https://placehold.co/300x300/red/white?text=Tomato', 3.50, 2, 7.00, 3.50, 0, DATE_SUB(NOW(), INTERVAL 40 MINUTE)),
  (11, 9, 2, 3, '红富士苹果', '单个约200g', 'https://placehold.co/300x300/red/white?text=Apple', 2.00, 5, 10.00, 2.00, 0, DATE_SUB(NOW(), INTERVAL 40 MINUTE)),
  (12, 10, 1, 1, '西红柿', '500g', 'https://placehold.co/300x300/red/white?text=Tomato', 3.50, 2, 7.00, 3.50, 0, DATE_SUB(NOW(), INTERVAL 7200 MINUTE)),
  (13, 10, 2, 3, '红富士苹果', '单个约200g', 'https://placehold.co/300x300/red/white?text=Apple', 2.00, 5, 10.00, 2.00, 0, DATE_SUB(NOW(), INTERVAL 7200 MINUTE)),
  (14, 11, 3, 4, '海南香蕉', '1把约500g', 'https://placehold.co/300x300/yellow/black?text=Banana', 5.50, 3, 16.50, 5.50, 0, DATE_SUB(NOW(), INTERVAL 1440 MINUTE));

-- ============ 支付单（admin 订单详情的支付方式来源；status：2支付成功） ============
-- pay_type：1微信 2支付宝 3货到付款 4收银台现金；transaction_id 现金单为空
-- community_id 必填：payment 服务不查订单表，支付流水列表的社区隔离只认本列（0 仅超管可见）
-- confirm_source：1渠道回调 4线下/收银台；pending_flag 离开待支付后一律 NULL
INSERT INTO `payments` (`id`, `payment_no`, `order_id`, `order_no`, `pending_flag`, `user_id`, `community_id`, `pay_type`, `amount`, `status`, `transaction_id`, `pay_time`, `confirm_source`, `confirm_ref`, `created_at`, `updated_at`)
VALUES
  (1, 'SEEDPAY20261018001', 2, 'SEED20261018002', NULL, 1, 1, 1, 16.50, 2, 'SEED-WX-20261018-01', DATE_SUB(NOW(), INTERVAL 110 MINUTE), 1, '', DATE_SUB(NOW(), INTERVAL 110 MINUTE), DATE_SUB(NOW(), INTERVAL 110 MINUTE)),
  (2, 'SEEDPAY20261018002', 3, 'SEED20261018003', NULL, 1, 1, 2, 15.00, 2, 'SEED-ALI-20261018-01', DATE_SUB(NOW(), INTERVAL 2870 MINUTE), 1, '', DATE_SUB(NOW(), INTERVAL 2870 MINUTE), DATE_SUB(NOW(), INTERVAL 2870 MINUTE)),
  (3, 'SEEDPAY20261019001', 5, 'SEED20261018005', NULL, 2, 2, 1, 12.90, 2, 'SEED-WX-20261019-01', DATE_SUB(NOW(), INTERVAL 120 MINUTE), 1, '', DATE_SUB(NOW(), INTERVAL 120 MINUTE), DATE_SUB(NOW(), INTERVAL 120 MINUTE)),
  (4, 'SEEDPAY20261018003', 7, 'SEED20261018007', NULL, 2, 2, 2, 27.80, 2, 'SEED-ALI-20261018-02', DATE_SUB(NOW(), INTERVAL 2870 MINUTE), 1, '', DATE_SUB(NOW(), INTERVAL 2870 MINUTE), DATE_SUB(NOW(), INTERVAL 2870 MINUTE)),
  (5, 'SEEDPAY20261018004', 8, 'SEED20261018008', NULL, 1, 1, 4, 6.00, 2, NULL, DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 605 MINUTE), 4, 'pos-cash', DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 605 MINUTE), DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 605 MINUTE)),
  (6, 'SEEDPAY-TODAY-0009', 9, 'SEED-TODAY-0009', NULL, 1, 1, 1, 20.00, 2, 'SEED-WX-TODAY-09', DATE_SUB(NOW(), INTERVAL 25 MINUTE), 1, '', DATE_SUB(NOW(), INTERVAL 25 MINUTE), DATE_SUB(NOW(), INTERVAL 25 MINUTE)),
  (7, 'SEEDPAY-REFUND-0010', 10, 'SEED-REFUND-0010', NULL, 1, 1, 1, 20.00, 2, 'SEED-WX-REFUND-10', DATE_SUB(NOW(), INTERVAL 7190 MINUTE), 1, '', DATE_SUB(NOW(), INTERVAL 7190 MINUTE), DATE_SUB(NOW(), INTERVAL 7190 MINUTE)),
  (8, 'SEEDPAY-REFUND-0011', 11, 'SEED-REFUND-0011', NULL, 1, 1, 1, 16.50, 2, 'SEED-WX-REFUND-11', DATE_SUB(NOW(), INTERVAL 1430 MINUTE), 1, '', DATE_SUB(NOW(), INTERVAL 1430 MINUTE), DATE_SUB(NOW(), INTERVAL 1430 MINUTE));

-- ============ 订单状态日志（operator_type：user / system / admin） ============
-- 注意：本表仅 created_at，无 updated_at（与 order_status_logs DDL 对齐）
-- 退款单的 5→7（申请）与 7→8（退款完成）同样落日志，保证详情时间线完整
INSERT INTO `order_status_logs` (`id`, `order_id`, `from_status`, `to_status`, `operator_type`, `operator_id`, `remark`, `created_at`)
VALUES
  (1, 1, 0, 1, 'user', 1, '用户下单', DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (2, 2, 0, 1, 'user', 1, '用户下单', DATE_SUB(NOW(), INTERVAL 120 MINUTE)),
  (3, 2, 1, 2, 'user', 1, '微信支付成功', DATE_SUB(NOW(), INTERVAL 110 MINUTE)),
  (4, 2, 2, 3, 'system', 0, '支付完成进入备货', DATE_SUB(NOW(), INTERVAL 105 MINUTE)),
  (5, 3, 0, 1, 'user', 1, '用户下单', DATE_SUB(NOW(), INTERVAL 2880 MINUTE)),
  (6, 3, 1, 2, 'user', 1, '支付宝支付成功（抵扣新人券 5.00）', DATE_SUB(NOW(), INTERVAL 2870 MINUTE)),
  (7, 3, 2, 3, 'system', 0, '支付完成进入备货', DATE_SUB(NOW(), INTERVAL 2865 MINUTE)),
  (8, 3, 3, 5, 'system', 0, '取货码核销完成', DATE_SUB(NOW(), INTERVAL 1440 MINUTE)),
  (9, 4, 0, 1, 'user', 1, '用户下单', DATE_SUB(NOW(), INTERVAL 1440 MINUTE)),
  (10, 4, 1, 6, 'user', 1, '用户取消：误下单', DATE_SUB(NOW(), INTERVAL 1425 MINUTE)),
  (11, 5, 0, 1, 'user', 2, '用户下单', DATE_SUB(NOW(), INTERVAL 180 MINUTE)),
  (12, 5, 1, 2, 'user', 2, '微信支付成功', DATE_SUB(NOW(), INTERVAL 120 MINUTE)),
  (13, 5, 2, 5, 'system', 0, '到店订单收款完成，自动完结', DATE_SUB(NOW(), INTERVAL 120 MINUTE)),
  (14, 6, 0, 1, 'user', 2, '用户下单', DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (15, 7, 0, 1, 'user', 2, '用户下单', DATE_SUB(NOW(), INTERVAL 2880 MINUTE)),
  (16, 7, 1, 2, 'user', 2, '支付宝支付成功', DATE_SUB(NOW(), INTERVAL 2870 MINUTE)),
  (17, 7, 2, 4, 'system', 0, '商家发货（顺丰速运 SF1000000001）', DATE_SUB(NOW(), INTERVAL 60 MINUTE)),
  (18, 8, 0, 1, 'admin', 1, '收银台代客下单', DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 602 MINUTE)),
  (19, 8, 1, 2, 'admin', 1, '收银台现金收款（改价 6.50→6.00）', DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 605 MINUTE)),
  (20, 8, 2, 5, 'system', 0, '到店订单收款完成，自动完结', DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 605 MINUTE)),
  (21, 9, 0, 1, 'user', 1, '用户下单', DATE_SUB(NOW(), INTERVAL 40 MINUTE)),
  (22, 9, 1, 2, 'user', 1, '微信支付成功', DATE_SUB(NOW(), INTERVAL 25 MINUTE)),
  (23, 9, 2, 3, 'system', 0, '支付完成进入备货', DATE_SUB(NOW(), INTERVAL 24 MINUTE)),
  (24, 9, 3, 4, 'system', 0, '商家发货（顺丰速运 SF1000000009）', DATE_SUB(NOW(), INTERVAL 15 MINUTE)),
  (25, 9, 4, 5, 'user', 1, '确认收货', DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (26, 10, 0, 1, 'user', 1, '用户下单', DATE_SUB(NOW(), INTERVAL 7200 MINUTE)),
  (27, 10, 1, 2, 'user', 1, '微信支付成功', DATE_SUB(NOW(), INTERVAL 7190 MINUTE)),
  (28, 10, 2, 3, 'system', 0, '支付完成进入备货', DATE_SUB(NOW(), INTERVAL 7185 MINUTE)),
  (29, 10, 3, 4, 'system', 0, '商家发货（顺丰速运 SF1000000010）', DATE_SUB(NOW(), INTERVAL 5760 MINUTE)),
  (30, 10, 4, 5, 'user', 1, '确认收货', DATE_SUB(NOW(), INTERVAL 5640 MINUTE)),
  (31, 10, 5, 7, 'user', 1, '申请退款: 商品质量问题', DATE_SUB(NOW(), INTERVAL 4800 MINUTE)),
  (32, 10, 7, 8, 'admin', 1, '退款完成', DATE_SUB(NOW(), INTERVAL 4260 MINUTE)),
  (33, 11, 0, 1, 'user', 1, '用户下单', DATE_SUB(NOW(), INTERVAL 1440 MINUTE)),
  (34, 11, 1, 2, 'user', 1, '微信支付成功', DATE_SUB(NOW(), INTERVAL 1430 MINUTE)),
  (35, 11, 2, 3, 'system', 0, '支付完成进入备货', DATE_SUB(NOW(), INTERVAL 1425 MINUTE)),
  (36, 11, 3, 5, 'system', 0, '取货码核销完成', DATE_SUB(NOW(), INTERVAL 1200 MINUTE)),
  (37, 11, 5, 7, 'user', 1, '申请退款: 部分商品缺货', DATE_SUB(NOW(), INTERVAL 120 MINUTE));

-- ============ 售后（退款单 / 明细 / 审批日志 / 渠道退款流水） ============
-- 退款状态：1待审核 2已同意 3已拒绝 4退款中 5已退款
-- 订单10：整单退款完成（订单 8 已退款，金额含运费 3.00，明细合计 17.00）
-- 订单11：部分退款（香蕉 1 把），已审批进入渠道退款中（订单 7 退款中）
INSERT INTO `refunds` (`id`, `refund_no`, `order_id`, `user_id`, `payment_no`, `amount`, `freight_amount`, `type`, `reason`, `status`, `reject_reason`, `operator_id`, `refund_time`, `last_checked_at`, `retry_count`, `created_at`, `updated_at`)
VALUES
  (1, 'SEEDREFUND-0010', 10, 1, 'SEEDPAY-REFUND-0010', 20.00, 3.00, 1, '商品质量问题，申请整单退款', 5, '', 1, DATE_SUB(NOW(), INTERVAL 4260 MINUTE), NULL, 0, DATE_SUB(NOW(), INTERVAL 4800 MINUTE), DATE_SUB(NOW(), INTERVAL 4260 MINUTE)),
  (2, 'SEEDREFUND-0011', 11, 1, 'SEEDPAY-REFUND-0011', 5.50, 0.00, 2, '部分商品缺货，申请退款', 4, '', 1, NULL, DATE_SUB(NOW(), INTERVAL 60 MINUTE), 0, DATE_SUB(NOW(), INTERVAL 120 MINUTE), DATE_SUB(NOW(), INTERVAL 60 MINUTE));

-- 退款明细：order_item_id 对应上方 order_items.id；仅商品金额，运费记在 refunds.freight_amount
INSERT INTO `refund_items` (`id`, `refund_id`, `order_item_id`, `sku_id`, `product_name`, `spec_name`, `quantity`, `refund_amount`, `created_at`)
VALUES
  (1, 1, 12, 1, '西红柿', '500g', 2, 7.00, DATE_SUB(NOW(), INTERVAL 4800 MINUTE)),
  (2, 1, 13, 3, '红富士苹果', '单个约200g', 5, 10.00, DATE_SUB(NOW(), INTERVAL 4800 MINUTE)),
  (3, 2, 14, 4, '海南香蕉', '1把约500g', 1, 5.50, DATE_SUB(NOW(), INTERVAL 120 MINUTE));

-- 退款审批日志：create 发起 / approve 同意
INSERT INTO `refund_logs` (`id`, `refund_id`, `action`, `operator_type`, `operator_id`, `remark`, `created_at`)
VALUES
  (1, 1, 'create', 'user', 1, '用户发起退款', DATE_SUB(NOW(), INTERVAL 4800 MINUTE)),
  (2, 1, 'approve', 'admin', 1, '同意退款', DATE_SUB(NOW(), INTERVAL 4270 MINUTE)),
  (3, 2, 'create', 'user', 1, '用户发起退款', DATE_SUB(NOW(), INTERVAL 120 MINUTE)),
  (4, 2, 'approve', 'admin', 1, '同意退款', DATE_SUB(NOW(), INTERVAL 60 MINUTE));

-- 渠道退款流水：1待退款 2退款成功 3退款失败；refund_time 仅成功单有值
INSERT INTO `refund_payments` (`id`, `refund_no`, `payment_no`, `order_id`, `amount`, `status`, `transaction_id`, `retry_count`, `escalated`, `refund_time`, `created_at`, `updated_at`)
VALUES
  (1, 'SEEDREFUND-0010', 'SEEDPAY-REFUND-0010', 10, 20.00, 2, 'SEED-WX-RF-0010', 0, 0, DATE_SUB(NOW(), INTERVAL 4260 MINUTE), DATE_SUB(NOW(), INTERVAL 4270 MINUTE), DATE_SUB(NOW(), INTERVAL 4260 MINUTE)),
  (2, 'SEEDREFUND-0011', 'SEEDPAY-REFUND-0011', 11, 5.50, 1, '', 0, 0, NULL, DATE_SUB(NOW(), INTERVAL 60 MINUTE), DATE_SUB(NOW(), INTERVAL 60 MINUTE));

-- ============ 进销存单据与库存流水 ============
-- 入库单（直接入库，无采购单）：对应 stock_batches.id=8，苹果 +50，160 → 210
INSERT INTO `stock_in_orders` (`id`, `community_id`, `in_no`, `supplier_id`, `purchase_order_id`, `type`, `total_amount`, `operator_id`, `remark`, `created_at`, `updated_at`)
VALUES
  (1, 1, 'IN-SEED-0001', 2, 0, 2, 60.00, 1, '水果临时补货（直接入库）', DATE_SUB(NOW(), INTERVAL 5 DAY), DATE_SUB(NOW(), INTERVAL 5 DAY));

INSERT INTO `stock_in_order_items` (`id`, `in_order_id`, `sku_id`, `quantity`, `unit_cost`, `batch_no`, `production_date`, `expire_date`, `batch_id`, `created_at`)
VALUES
  (1, 1, 3, 50, 1.20, 'IN-SEED-0001', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 5 DAY), '%Y-%m-%d'), NULL, 8, DATE_SUB(NOW(), INTERVAL 5 DAY));

-- 报损出库单：苹果磕碰 10 个，210 → 200（扣 INIT-APPLE-1P-A 批次）
INSERT INTO `stock_out_orders` (`id`, `community_id`, `out_no`, `type`, `operator_id`, `remark`, `created_at`, `updated_at`)
VALUES
  (1, 1, 'OUT-SEED-0001', 1, 1, '苹果磕碰报损', DATE_SUB(NOW(), INTERVAL 3 DAY), DATE_SUB(NOW(), INTERVAL 3 DAY));

INSERT INTO `stock_out_order_items` (`id`, `out_order_id`, `sku_id`, `quantity`, `created_at`)
VALUES
  (1, 1, 3, 10, DATE_SUB(NOW(), INTERVAL 3 DAY));

-- 盘点单（待提交草稿：差异不回写库存，供管理端「盘点」列表与差异展示）
INSERT INTO `stock_checks` (`id`, `check_no`, `community_id`, `operator_id`, `status`, `total_items`, `diff_items`, `remark`, `created_at`, `updated_at`)
VALUES
  (1, 'CHECK-SEED-0001', 1, 1, 1, 2, 1, '月度抽盘（草稿，未提交）', DATE_SUB(NOW(), INTERVAL 2 DAY), DATE_SUB(NOW(), INTERVAL 2 DAY));

INSERT INTO `stock_check_items` (`id`, `check_id`, `sku_id`, `system_stock`, `actual_stock`, `diff_quantity`, `created_at`)
VALUES
  (1, 1, 1, 80, 80, 0, DATE_SUB(NOW(), INTERVAL 2 DAY)),
  (2, 1, 4, 150, 148, -2, DATE_SUB(NOW(), INTERVAL 2 DAY));

-- 库存流水：仅落单据产生的两条变动（销售扣减流水由下单链路实时产生）
INSERT INTO `stock_records` (`id`, `sku_id`, `community_id`, `type`, `quantity`, `before_stock`, `after_stock`, `batch_id`, `batch_no`, `production_date`, `expire_date`, `related_order_id`, `ref_no`, `operator_id`, `remark`, `created_at`)
VALUES
  (1, 3, 1, 1, 50, 160, 210, 8, 'IN-SEED-0001', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 5 DAY), '%Y-%m-%d'), NULL, 0, 'IN-SEED-0001', 1, '直接入库', DATE_SUB(NOW(), INTERVAL 5 DAY)),
  (2, 3, 1, 2, -10, 210, 200, 4, 'INIT-APPLE-1P-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 27 DAY), '%Y-%m-%d'), NULL, 0, 'OUT-SEED-0001', 1, '苹果磕碰报损', DATE_SUB(NOW(), INTERVAL 3 DAY));

-- ============ 收银台挂单（未成单草稿：不生成订单、不扣库存、不占班次） ============
-- items_json 为 proto PosHeldOrderItem 的 Go json tag（snake_case），恢复时由 service 反序列化
INSERT INTO `pos_held_orders` (`id`, `community_id`, `operator_id`, `operator_name`, `user_id`, `user_name`, `items_json`, `item_count`, `total_amount`, `remark`, `created_at`)
VALUES
  (1, 1, 1, '系统管理员', 1, '张三',
   '[{"sku_id":1,"quantity":1,"product_name":"西红柿","spec_name":"500g","product_image":"https://placehold.co/300x300/red/white?text=Tomato","price":3.50},{"sku_id":3,"quantity":2,"product_name":"红富士苹果","spec_name":"单个约200g","product_image":"https://placehold.co/300x300/red/white?text=Apple","price":2.00}]',
   2, 7.50, '顾客临时取物，挂单待结', DATE_SUB(NOW(), INTERVAL 10 MINUTE));

-- ============ 站点设置（平台级单行配置，id 固定 1，见 model.SiteConfigID） ============
-- base_url 留空：空值时网关按请求现推派生回调与外链，避免种子写死部署域名；
-- footer_html 仅白名单标签（a/b/i/u/p/br），与管理端富文本消毒口径一致；
-- logo/favicon 留空：为空时前端使用内置默认图
INSERT INTO `site_configs` (`id`, `base_url`, `site_name`, `logo_url`, `favicon_url`, `footer_html`, `seo_description`, `seo_keywords`, `version`, `created_at`, `updated_at`)
VALUES
  (1, '', '社区集市', '', '', '<p>© 2026 社区集市 · 演示环境</p>', '社区团购，新鲜到家', '社区团购,生鲜,果蔬', 0, NOW(), NOW());

SET FOREIGN_KEY_CHECKS = 1;
