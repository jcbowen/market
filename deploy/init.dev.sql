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
--   商品目录为「社区便利店 · 预包装食品」口径（虚构品牌，15 个一级分类 / 56 商品 / 61 SKU，
--   双社区各自成目录），由 seed-catalog.mjs 生成、seed-images.mjs 绘制配图，
--   图片走本地相对路径 /uploads/seed/products/*.png（网关静态目录，见 start-all.mjs 拷贝步骤）；
--   SKU 带 EAN-13 条码，供收银台扫码与 C 端条码搜索
--   Banner 轮播、满减活动、优惠券（含酒水饮料分类范围 + 两张用户券：一张可用一张已用）
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
-- 社区1（盘龙金茂悦）9 类、社区2（阳光花园）6 类，均为一级分类：
-- C 端分类页只取 parent_id=0 且 status=1 的分类，商品直接挂在一级分类下
-- icon 复用该分类首个商品的图片，避免再引入一套图标素材
INSERT INTO `categories` (`id`, `community_id`, `parent_id`, `name`, `icon`, `sort`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, 0, '休闲零食', '/uploads/seed/products/latiao.png', 10, 1, NOW(), NOW()),
  (2, 1, 0, '酒水饮料', '/uploads/seed/products/water.png', 20, 1, NOW(), NOW()),
  (4, 1, 0, '方便速食', '/uploads/seed/products/noodle-cup.png', 30, 1, NOW(), NOW()),
  (5, 1, 0, '乳品烘焙', '/uploads/seed/products/milk-carton.png', 40, 1, NOW(), NOW()),
  (6, 1, 0, '粮油调味', '/uploads/seed/products/rice-bag.png', 50, 1, NOW(), NOW()),
  (7, 1, 0, '日用百货', '/uploads/seed/products/tissue-pack.png', 60, 1, NOW(), NOW()),
  (8, 1, 0, '冷冻食品', '/uploads/seed/products/dumpling-box.png', 70, 1, NOW(), NOW()),
  (9, 1, 0, '糖果巧克力', '/uploads/seed/products/chocolate.png', 80, 1, NOW(), NOW()),
  (10, 1, 0, '冲调饮品', '/uploads/seed/products/coffee-box.png', 90, 1, NOW(), NOW()),
  (3, 2, 0, '乳品蛋品', '/uploads/seed/products/egg-carton.png', 10, 1, NOW(), NOW()),
  (11, 2, 0, '休闲零食', '/uploads/seed/products/chips.png', 20, 1, NOW(), NOW()),
  (12, 2, 0, '酒水饮料', '/uploads/seed/products/water.png', 30, 1, NOW(), NOW()),
  (13, 2, 0, '方便速食', '/uploads/seed/products/noodle-cup.png', 40, 1, NOW(), NOW()),
  (14, 2, 0, '粮油调味', '/uploads/seed/products/rice-bag.png', 50, 1, NOW(), NOW()),
  (15, 2, 0, '日用百货', '/uploads/seed/products/tissue-pack.png', 60, 1, NOW(), NOW());

-- ============ 商品 ============
-- 汇总口径：min_price/max_price = SKU 价格区间；min_original_price = 最低价 SKU 的划线价（0 不展示）；
-- stock = 各启用 SKU 库存之和；展示销量 = sales_count + virtual_sales
-- 商品 1/24/30/35/39 为多规格，其余单规格；29 号为仓库中（status=0）、41 号为售罄（stock=0）、
-- 27/44 号为库存紧张（stock ≤ 安全库存），用于演示商品列表四个状态页签
INSERT INTO `products` (`id`, `community_id`, `category_id`, `name`, `description`, `cover_image`, `images`, `unit`, `is_multi_spec`, `min_price`, `max_price`, `min_original_price`, `show_sales`, `virtual_sales`, `stock`, `min_buy`, `status`, `sort`, `sales_count`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, '魔小鲜 大面筋辣条', '经典川香辣条，独立小包装，追剧解馋开袋即食', '/uploads/seed/products/latiao.png', '["/uploads/seed/products/latiao.png","/uploads/seed/products/latiao-alt.png"]', '袋', 1, 3.50, 6.50, 4.50, 1, 320, 120, 1, 1, 100, 0, NOW(), NOW()),
  (3, 1, 1, '乐游 原味薯片', '厚切马铃薯片，咸香酥脆，70g 便携装', '/uploads/seed/products/chips.png', '["/uploads/seed/products/chips.png"]', '袋', 0, 5.50, 5.50, 6.90, 1, 260, 90, 1, 1, 90, 0, NOW(), NOW()),
  (5, 1, 1, '麦香源 巧克力夹心饼干', '酥脆饼干夹可可奶油心，116g 家庭小袋装', '/uploads/seed/products/cookie.png', '["/uploads/seed/products/cookie.png"]', '袋', 0, 6.90, 6.90, 8.50, 1, 140, 96, 1, 1, 80, 0, NOW(), NOW()),
  (6, 1, 1, '果仁季 每日坚果', '七种果仁独立小包，25g×7 混合装，办公室常备', '/uploads/seed/products/nuts.png', '["/uploads/seed/products/nuts.png"]', '盒', 0, 29.90, 29.90, 39.90, 1, 88, 42, 1, 1, 70, 0, NOW(), NOW()),
  (7, 1, 1, '肉匠 蜜汁猪肉脯', '厚切猪肉脯，蜜汁入味，100g 即食装', '/uploads/seed/products/jerky.png', '["/uploads/seed/products/jerky.png"]', '袋', 0, 19.90, 19.90, 25.90, 1, 66, 36, 1, 1, 60, 0, NOW(), NOW()),
  (8, 1, 1, '雪旺 咸味雪饼', '米饼夹心，松脆咸香，84g 经典怀旧味', '/uploads/seed/products/snowcake.png', '["/uploads/seed/products/snowcake.png"]', '袋', 0, 7.50, 7.50, 9.00, 1, 120, 68, 1, 1, 50, 0, NOW(), NOW()),
  (9, 1, 1, '魔小鲜 香辣魔芋爽', '低负担辣味零食，Q 弹爽口，50g 独立装', '/uploads/seed/products/konjac.png', '["/uploads/seed/products/konjac.png"]', '袋', 0, 3.20, 3.20, 4.00, 1, 210, 120, 1, 1, 40, 0, NOW(), NOW()),
  (2, 1, 2, '碧源 饮用天然水', '弱碱性天然水，550ml 随身装，整箱更划算', '/uploads/seed/products/water.png', '["/uploads/seed/products/water.png"]', '瓶', 0, 2.00, 2.00, 2.80, 1, 480, 200, 1, 1, 100, 0, NOW(), NOW()),
  (10, 1, 2, '气泡山 经典可乐', '冰镇更带劲的碳酸饮料，330ml 易拉罐装', '/uploads/seed/products/cola.png', '["/uploads/seed/products/cola.png"]', '罐', 0, 3.00, 3.00, 3.50, 1, 350, 144, 1, 1, 90, 0, NOW(), NOW()),
  (11, 1, 2, '气泡山 柠檬味汽水', '清爽柠檬口味汽水，500ml 大瓶装', '/uploads/seed/products/sprite.png', '["/uploads/seed/products/sprite.png"]', '瓶', 0, 3.50, 3.50, 4.00, 1, 220, 96, 1, 1, 80, 0, NOW(), NOW()),
  (12, 1, 2, '茶语时光 冰红茶', '柠檬风味红茶，1L 分享装，夏日解腻', '/uploads/seed/products/iced-tea.png', '["/uploads/seed/products/iced-tea.png"]', '瓶', 0, 4.50, 4.50, 5.50, 1, 300, 84, 1, 1, 70, 0, NOW(), NOW()),
  (13, 1, 2, '茶语时光 无糖乌龙茶', '0 糖 0 卡原叶萃取，500ml 无糖茶饮', '/uploads/seed/products/oolong.png', '["/uploads/seed/products/oolong.png"]', '瓶', 0, 5.00, 5.00, 6.00, 1, 160, 72, 1, 1, 60, 0, NOW(), NOW()),
  (14, 1, 2, '倍力特 维生素功能饮料', '补充能量与维生素，250ml 金罐装', '/uploads/seed/products/energy-drink.png', '["/uploads/seed/products/energy-drink.png"]', '罐', 0, 6.50, 6.50, 7.50, 1, 190, 60, 1, 1, 50, 0, NOW(), NOW()),
  (15, 1, 2, '青岛 经典啤酒', '麦香清爽的拉格啤酒，500ml 罐装，冷藏更佳', '/uploads/seed/products/beer.png', '["/uploads/seed/products/beer.png"]', '罐', 0, 6.00, 6.00, 7.00, 1, 240, 90, 1, 1, 40, 0, NOW(), NOW()),
  (16, 1, 2, '优乐乳 乳酸菌饮品', '发酵乳酸菌风味，250ml×6 连包，饭后一瓶', '/uploads/seed/products/lactic-drink.png', '["/uploads/seed/products/lactic-drink.png"]', '提', 0, 12.90, 12.90, 15.90, 1, 130, 48, 1, 1, 30, 0, NOW(), NOW()),
  (17, 1, 4, '面匠 红烧牛肉面', '经典红烧汤底，桶装免洗碗，夜宵加班必备', '/uploads/seed/products/noodle-cup.png', '["/uploads/seed/products/noodle-cup.png"]', '桶', 0, 5.50, 5.50, 6.50, 1, 420, 120, 1, 1, 100, 0, NOW(), NOW()),
  (18, 1, 4, '面匠 老坛酸菜面', '酸爽开胃的老坛酸菜汤底，桶装 120g', '/uploads/seed/products/noodle-pickle-cup.png', '["/uploads/seed/products/noodle-pickle-cup.png"]', '桶', 0, 5.50, 5.50, 6.50, 1, 380, 108, 1, 1, 90, 0, NOW(), NOW()),
  (19, 1, 4, '汤好鲜 骨汤面', '浓骨汤底非油炸面饼，5 连包家庭囤货款', '/uploads/seed/products/noodle-5pack.png', '["/uploads/seed/products/noodle-5pack.png"]', '包', 0, 13.90, 13.90, 16.90, 1, 150, 54, 1, 1, 80, 0, NOW(), NOW()),
  (20, 1, 4, '自热派 麻辣牛肉自热饭', '不插电自加热，15 分钟吃上热饭，麻辣牛肉口味', '/uploads/seed/products/selfheat-rice.png', '["/uploads/seed/products/selfheat-rice.png"]', '盒', 0, 19.90, 19.90, 24.90, 1, 90, 30, 1, 1, 70, 0, NOW(), NOW()),
  (21, 1, 4, '旺肉多 优级火腿肠', '肉含量高的即食火腿肠，60g×10 支装', '/uploads/seed/products/sausage.png', '["/uploads/seed/products/sausage.png"]', '组', 0, 15.90, 15.90, 18.90, 1, 200, 66, 1, 1, 60, 0, NOW(), NOW()),
  (22, 1, 4, '脆爽 涪陵榨菜', '佐粥下饭的经典小菜，70g 独立鲜香装', '/uploads/seed/products/pickle.png', '["/uploads/seed/products/pickle.png"]', '袋', 0, 3.50, 3.50, 4.50, 1, 170, 110, 1, 1, 50, 0, NOW(), NOW()),
  (23, 1, 4, '香林 午餐肉罐头', '厚切肉粒午餐肉，340g 铁罐，泡面搭档', '/uploads/seed/products/luncheon-jar.png', '["/uploads/seed/products/luncheon-jar.png"]', '罐', 0, 12.90, 12.90, 15.90, 1, 75, 40, 1, 1, 40, 0, NOW(), NOW()),
  (24, 1, 5, '怡香源 纯牛奶', '3.2g 优质乳蛋白，250ml 利乐砖整箱装', '/uploads/seed/products/milk-carton.png', '["/uploads/seed/products/milk-carton.png","/uploads/seed/products/milk-carton-alt.png"]', '箱', 1, 42.90, 82.90, 52.90, 1, 260, 74, 1, 1, 100, 0, NOW(), NOW()),
  (25, 1, 5, '牧场精选 高钙牛奶', '强化钙配方，250ml×12 礼盒装，早晚一瓶', '/uploads/seed/products/milk-calcium.png', '["/uploads/seed/products/milk-calcium.png"]', '箱', 0, 59.90, 59.90, 69.90, 1, 110, 28, 1, 1, 90, 0, NOW(), NOW()),
  (26, 1, 5, '慕希 原味浓稠酸奶', '浓稠口感原味发酵乳，205g×12 瓶装', '/uploads/seed/products/yogurt-bottle.png', '["/uploads/seed/products/yogurt-bottle.png"]', '箱', 0, 49.90, 49.90, 59.90, 1, 145, 32, 1, 1, 80, 0, NOW(), NOW()),
  (27, 1, 5, '晨鲜 巴氏鲜牛奶', '低温巴氏杀菌，保留鲜奶本味，950ml 大瓶装', '/uploads/seed/products/fresh-milk.png', '["/uploads/seed/products/fresh-milk.png"]', '瓶', 0, 15.90, 15.90, 18.90, 1, 95, 8, 1, 1, 70, 0, NOW(), NOW()),
  (28, 1, 5, '麦香坊 法式软面包', '独立小包软面包，400g 约 8 枚，早餐随手带', '/uploads/seed/products/soft-bread.png', '["/uploads/seed/products/soft-bread.png"]', '袋', 0, 12.90, 12.90, 15.90, 1, 130, 45, 1, 1, 60, 0, NOW(), NOW()),
  (29, 1, 5, '醇熟 切片吐司', '松软吐司切片，400g 一袋，做三明治方便', '/uploads/seed/products/toast.png', '["/uploads/seed/products/toast.png"]', '袋', 0, 9.90, 9.90, 12.90, 1, 60, 20, 1, 0, 50, 0, NOW(), NOW()),
  (30, 1, 6, '金稻 东北长粒香米', '当季新米，颗粒饱满，5kg/10kg 家庭装', '/uploads/seed/products/rice-bag.png', '["/uploads/seed/products/rice-bag.png","/uploads/seed/products/rice-bag-alt.png"]', '袋', 1, 39.90, 74.90, 46.90, 1, 180, 72, 1, 1, 100, 0, NOW(), NOW()),
  (31, 1, 6, '油乐 压榨菜籽油', '物理压榨一级菜籽油，5L 桶装家用', '/uploads/seed/products/oil-jug.png', '["/uploads/seed/products/oil-jug.png"]', '桶', 0, 79.90, 79.90, 92.90, 1, 95, 24, 1, 1, 90, 0, NOW(), NOW()),
  (32, 1, 6, '海天下 金标生抽', '零添加头道原香酱油，1.9L 大瓶装', '/uploads/seed/products/soy-sauce.png', '["/uploads/seed/products/soy-sauce.png"]', '瓶', 0, 16.90, 16.90, 19.90, 1, 160, 38, 1, 1, 80, 0, NOW(), NOW()),
  (33, 1, 6, '海天下 五年陈醋', '粮食固态发酵陈醋，500ml 瓶装', '/uploads/seed/products/vinegar.png', '["/uploads/seed/products/vinegar.png"]', '瓶', 0, 6.90, 6.90, 8.90, 1, 85, 46, 1, 1, 70, 0, NOW(), NOW()),
  (34, 1, 6, '白盐 精制食用盐', '加碘精制盐，400g 袋装，厨房常备', '/uploads/seed/products/salt.png', '["/uploads/seed/products/salt.png"]', '袋', 0, 2.50, 2.50, 3.00, 1, 240, 90, 1, 1, 60, 0, NOW(), NOW()),
  (35, 1, 7, '维纯 抽取式面巾纸', '3 层 100 抽加厚原生木浆，6 包/12 包囤货装', '/uploads/seed/products/tissue-pack.png', '["/uploads/seed/products/tissue-pack.png","/uploads/seed/products/tissue-pack-alt.png"]', '提', 1, 12.90, 23.90, 15.90, 1, 300, 140, 1, 1, 100, 0, NOW(), NOW()),
  (36, 1, 7, '清月 深层洁净洗衣液', '低泡易漂洗，3kg 大瓶装，清新花香', '/uploads/seed/products/laundry-jug.png', '["/uploads/seed/products/laundry-jug.png"]', '瓶', 0, 29.90, 29.90, 36.90, 1, 120, 26, 1, 1, 90, 0, NOW(), NOW()),
  (37, 1, 7, '净立 柠檬洗洁精', '强力去油易冲洗，1.5kg 柠檬香型', '/uploads/seed/products/dish-soap.png', '["/uploads/seed/products/dish-soap.png"]', '瓶', 0, 12.90, 12.90, 15.90, 1, 140, 34, 1, 1, 80, 0, NOW(), NOW()),
  (38, 1, 7, '草本 薄荷牙膏', '清新薄荷口味，120g 家庭装', '/uploads/seed/products/toothpaste.png', '["/uploads/seed/products/toothpaste.png"]', '支', 0, 15.90, 15.90, 19.90, 1, 105, 40, 1, 1, 70, 0, NOW(), NOW()),
  (39, 1, 8, '三鲜 猪肉白菜水饺', '皮薄馅大的冷冻水饺，500g/1kg 家庭装', '/uploads/seed/products/dumpling-box.png', '["/uploads/seed/products/dumpling-box.png","/uploads/seed/products/dumpling-box-alt.png"]', '袋', 1, 12.90, 21.90, 15.90, 1, 190, 53, 1, 1, 100, 0, NOW(), NOW()),
  (40, 1, 8, '芝麻心 黑芝麻汤圆', '糯叽叽的黑芝麻流心汤圆，500g 约 32 颗', '/uploads/seed/products/tangyuan.png', '["/uploads/seed/products/tangyuan.png"]', '袋', 0, 9.90, 9.90, 12.90, 1, 150, 40, 1, 1, 90, 0, NOW(), NOW()),
  (41, 1, 8, '雪绒 巧克力脆皮雪糕', '脆皮裹香草冰淇淋，75g 单支装', '/uploads/seed/products/icecream.png', '["/uploads/seed/products/icecream.png"]', '支', 0, 3.50, 3.50, 4.50, 1, 260, 0, 1, 1, 80, 0, NOW(), NOW()),
  (42, 1, 9, '丝滑 牛奶巧克力', '入口丝滑的牛奶巧克力，43g 排块装', '/uploads/seed/products/chocolate.png', '["/uploads/seed/products/chocolate.png"]', '块', 0, 8.50, 8.50, 9.90, 1, 175, 60, 1, 1, 100, 0, NOW(), NOW()),
  (43, 1, 9, '能量棒 花生夹心巧克力', '花生牛糖夹心，扛饿补给，51g 单支装', '/uploads/seed/products/energy-bar.png', '["/uploads/seed/products/energy-bar.png"]', '支', 0, 6.50, 6.50, 7.50, 1, 210, 72, 1, 1, 90, 0, NOW(), NOW()),
  (44, 1, 9, '清息 无糖口香糖', '薄荷味无糖口香糖，64g 瓶装，随时清新', '/uploads/seed/products/gum-jar.png', '["/uploads/seed/products/gum-jar.png"]', '瓶', 0, 9.90, 9.90, 11.90, 1, 90, 6, 1, 1, 80, 0, NOW(), NOW()),
  (45, 1, 10, '雀语 特浓速溶咖啡', '三合一特浓配方，15g×30 条独立装', '/uploads/seed/products/coffee-box.png', '["/uploads/seed/products/coffee-box.png"]', '盒', 0, 39.90, 39.90, 46.90, 1, 130, 30, 1, 1, 100, 0, NOW(), NOW()),
  (46, 1, 10, '飘飘 芋圆奶茶', '冷热皆宜的杯装奶茶，80g 加料版', '/uploads/seed/products/milktea-cup.png', '["/uploads/seed/products/milktea-cup.png"]', '杯', 0, 6.50, 6.50, 7.90, 1, 165, 54, 1, 1, 90, 0, NOW(), NOW()),
  (47, 1, 10, '麦谷 即食燕麦片', '澳洲燕麦，热水一冲即食，400g 罐装', '/uploads/seed/products/oats-jar.png', '["/uploads/seed/products/oats-jar.png"]', '罐', 0, 19.90, 19.90, 24.90, 1, 75, 26, 1, 1, 80, 0, NOW(), NOW()),
  (4, 2, 3, '农欢 散养鲜鸡蛋', '社区农户散养，30 枚防震蛋托装', '/uploads/seed/products/egg-carton.png', '["/uploads/seed/products/egg-carton.png"]', '箱', 0, 12.90, 12.90, 15.90, 1, 120, 100, 1, 1, 100, 0, NOW(), NOW()),
  (48, 2, 3, '怡香源 纯牛奶', '3.2g 优质乳蛋白，250ml×12 整箱装', '/uploads/seed/products/milk-carton.png', '["/uploads/seed/products/milk-carton.png"]', '箱', 0, 45.90, 45.90, 52.90, 1, 60, 24, 1, 1, 90, 0, NOW(), NOW()),
  (49, 2, 3, '慕希 原味浓稠酸奶', '浓稠口感原味发酵乳，205g×12 瓶装', '/uploads/seed/products/yogurt-bottle.png', '["/uploads/seed/products/yogurt-bottle.png"]', '箱', 0, 52.90, 52.90, 59.90, 1, 48, 18, 1, 1, 80, 0, NOW(), NOW()),
  (50, 2, 11, '乐游 原味薯片', '厚切马铃薯片，咸香酥脆，70g 便携装', '/uploads/seed/products/chips.png', '["/uploads/seed/products/chips.png"]', '袋', 0, 5.90, 5.90, 6.90, 1, 88, 60, 1, 1, 100, 0, NOW(), NOW()),
  (51, 2, 11, '麦香源 巧克力夹心饼干', '酥脆饼干夹可可奶油心，116g 小袋装', '/uploads/seed/products/cookie.png', '["/uploads/seed/products/cookie.png"]', '袋', 0, 7.20, 7.20, 8.50, 1, 42, 40, 1, 1, 90, 0, NOW(), NOW()),
  (52, 2, 12, '碧源 饮用天然水', '弱碱性天然水，550ml 随身装', '/uploads/seed/products/water.png', '["/uploads/seed/products/water.png"]', '瓶', 0, 2.00, 2.00, 2.50, 1, 150, 96, 1, 1, 100, 0, NOW(), NOW()),
  (53, 2, 12, '气泡山 柠檬味汽水', '清爽柠檬口味汽水，500ml 大瓶装', '/uploads/seed/products/sprite.png', '["/uploads/seed/products/sprite.png"]', '瓶', 0, 3.80, 3.80, 4.00, 1, 66, 48, 1, 1, 90, 0, NOW(), NOW()),
  (54, 2, 13, '面匠 红烧牛肉面', '经典红烧汤底，桶装免洗碗', '/uploads/seed/products/noodle-cup.png', '["/uploads/seed/products/noodle-cup.png"]', '桶', 0, 5.80, 5.80, 6.50, 1, 110, 45, 1, 1, 100, 0, NOW(), NOW()),
  (55, 2, 14, '金稻 东北长粒香米', '当季新米，颗粒饱满，5kg 家庭装', '/uploads/seed/products/rice-bag.png', '["/uploads/seed/products/rice-bag.png"]', '袋', 0, 42.90, 42.90, 46.90, 1, 36, 18, 1, 1, 100, 0, NOW(), NOW()),
  (56, 2, 15, '维纯 抽取式面巾纸', '3 层 100 抽原生木浆，6 包装', '/uploads/seed/products/tissue-pack.png', '["/uploads/seed/products/tissue-pack.png"]', '提', 0, 13.50, 13.50, 15.90, 1, 54, 30, 1, 1, 100, 0, NOW(), NOW());

-- 商品图集：与 products.images 对应（多规格商品给一张正面 + 一张细节图）
INSERT INTO `product_images` (`id`, `product_id`, `url`, `sort`, `created_at`)
VALUES
  (1, 1, '/uploads/seed/products/latiao.png', 0, NOW()),
  (2, 1, '/uploads/seed/products/latiao-alt.png', 1, NOW()),
  (3, 3, '/uploads/seed/products/chips.png', 0, NOW()),
  (4, 5, '/uploads/seed/products/cookie.png', 0, NOW()),
  (5, 6, '/uploads/seed/products/nuts.png', 0, NOW()),
  (6, 7, '/uploads/seed/products/jerky.png', 0, NOW()),
  (7, 8, '/uploads/seed/products/snowcake.png', 0, NOW()),
  (8, 9, '/uploads/seed/products/konjac.png', 0, NOW()),
  (9, 2, '/uploads/seed/products/water.png', 0, NOW()),
  (10, 10, '/uploads/seed/products/cola.png', 0, NOW()),
  (11, 11, '/uploads/seed/products/sprite.png', 0, NOW()),
  (12, 12, '/uploads/seed/products/iced-tea.png', 0, NOW()),
  (13, 13, '/uploads/seed/products/oolong.png', 0, NOW()),
  (14, 14, '/uploads/seed/products/energy-drink.png', 0, NOW()),
  (15, 15, '/uploads/seed/products/beer.png', 0, NOW()),
  (16, 16, '/uploads/seed/products/lactic-drink.png', 0, NOW()),
  (17, 17, '/uploads/seed/products/noodle-cup.png', 0, NOW()),
  (18, 18, '/uploads/seed/products/noodle-pickle-cup.png', 0, NOW()),
  (19, 19, '/uploads/seed/products/noodle-5pack.png', 0, NOW()),
  (20, 20, '/uploads/seed/products/selfheat-rice.png', 0, NOW()),
  (21, 21, '/uploads/seed/products/sausage.png', 0, NOW()),
  (22, 22, '/uploads/seed/products/pickle.png', 0, NOW()),
  (23, 23, '/uploads/seed/products/luncheon-jar.png', 0, NOW()),
  (24, 24, '/uploads/seed/products/milk-carton.png', 0, NOW()),
  (25, 24, '/uploads/seed/products/milk-carton-alt.png', 1, NOW()),
  (26, 25, '/uploads/seed/products/milk-calcium.png', 0, NOW()),
  (27, 26, '/uploads/seed/products/yogurt-bottle.png', 0, NOW()),
  (28, 27, '/uploads/seed/products/fresh-milk.png', 0, NOW()),
  (29, 28, '/uploads/seed/products/soft-bread.png', 0, NOW()),
  (30, 29, '/uploads/seed/products/toast.png', 0, NOW()),
  (31, 30, '/uploads/seed/products/rice-bag.png', 0, NOW()),
  (32, 30, '/uploads/seed/products/rice-bag-alt.png', 1, NOW()),
  (33, 31, '/uploads/seed/products/oil-jug.png', 0, NOW()),
  (34, 32, '/uploads/seed/products/soy-sauce.png', 0, NOW()),
  (35, 33, '/uploads/seed/products/vinegar.png', 0, NOW()),
  (36, 34, '/uploads/seed/products/salt.png', 0, NOW()),
  (37, 35, '/uploads/seed/products/tissue-pack.png', 0, NOW()),
  (38, 35, '/uploads/seed/products/tissue-pack-alt.png', 1, NOW()),
  (39, 36, '/uploads/seed/products/laundry-jug.png', 0, NOW()),
  (40, 37, '/uploads/seed/products/dish-soap.png', 0, NOW()),
  (41, 38, '/uploads/seed/products/toothpaste.png', 0, NOW()),
  (42, 39, '/uploads/seed/products/dumpling-box.png', 0, NOW()),
  (43, 39, '/uploads/seed/products/dumpling-box-alt.png', 1, NOW()),
  (44, 40, '/uploads/seed/products/tangyuan.png', 0, NOW()),
  (45, 41, '/uploads/seed/products/icecream.png', 0, NOW()),
  (46, 42, '/uploads/seed/products/chocolate.png', 0, NOW()),
  (47, 43, '/uploads/seed/products/energy-bar.png', 0, NOW()),
  (48, 44, '/uploads/seed/products/gum-jar.png', 0, NOW()),
  (49, 45, '/uploads/seed/products/coffee-box.png', 0, NOW()),
  (50, 46, '/uploads/seed/products/milktea-cup.png', 0, NOW()),
  (51, 47, '/uploads/seed/products/oats-jar.png', 0, NOW()),
  (52, 4, '/uploads/seed/products/egg-carton.png', 0, NOW()),
  (53, 48, '/uploads/seed/products/milk-carton.png', 0, NOW()),
  (54, 49, '/uploads/seed/products/yogurt-bottle.png', 0, NOW()),
  (55, 50, '/uploads/seed/products/chips.png', 0, NOW()),
  (56, 51, '/uploads/seed/products/cookie.png', 0, NOW()),
  (57, 52, '/uploads/seed/products/water.png', 0, NOW()),
  (58, 53, '/uploads/seed/products/sprite.png', 0, NOW()),
  (59, 54, '/uploads/seed/products/noodle-cup.png', 0, NOW()),
  (60, 55, '/uploads/seed/products/rice-bag.png', 0, NOW()),
  (61, 56, '/uploads/seed/products/tissue-pack.png', 0, NOW());

-- ============ 多规格链路（5 个多规格商品） ============
-- sku.spec_value_ids 为规格值 ID 组合键（逗号分隔、升序），spec_json 为展示快照
INSERT INTO `product_specs` (`id`, `product_id`, `community_id`, `name`, `sort`, `created_at`, `updated_at`, `deleted_at`)
VALUES
  (1, 1, 1, '规格', 0, NOW(), NOW(), NULL),
  (2, 24, 1, '规格', 0, NOW(), NOW(), NULL),
  (3, 30, 1, '规格', 0, NOW(), NOW(), NULL),
  (4, 35, 1, '规格', 0, NOW(), NOW(), NULL),
  (5, 39, 1, '规格', 0, NOW(), NOW(), NULL);

INSERT INTO `product_spec_values` (`id`, `spec_id`, `product_id`, `community_id`, `name`, `sort`, `created_at`, `updated_at`, `deleted_at`)
VALUES
  (1, 1, 1, 1, '小包106g', 1, NOW(), NOW(), NULL),
  (2, 1, 1, 1, '大包226g', 2, NOW(), NOW(), NULL),
  (3, 2, 24, 1, '12盒装', 1, NOW(), NOW(), NULL),
  (4, 2, 24, 1, '24盒装', 2, NOW(), NOW(), NULL),
  (5, 3, 30, 1, '5kg', 1, NOW(), NOW(), NULL),
  (6, 3, 30, 1, '10kg', 2, NOW(), NOW(), NULL),
  (7, 4, 35, 1, '6包装', 1, NOW(), NOW(), NULL),
  (8, 4, 35, 1, '12包装', 2, NOW(), NOW(), NULL),
  (9, 5, 39, 1, '500g', 1, NOW(), NOW(), NULL),
  (10, 5, 39, 1, '1kg', 2, NOW(), NOW(), NULL);

-- ============ SKU ============
-- barcode 为 EAN-13（含校验位）且社区内唯一，供收银台扫码与 C 端条码搜索使用
INSERT INTO `product_skus` (`id`, `product_id`, `community_id`, `sku_code`, `spec_value_ids`, `spec_name`, `spec_json`, `price`, `original_price`, `cost_price`, `barcode`, `stock`, `safety_stock`, `image`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, 'SNACK-LATIAO-S', '1', '小包106g', '{"规格":"小包106g"}', 3.50, 4.50, 2.00, '6901000000011', 80, 20, '/uploads/seed/products/latiao.png', 1, NOW(), NOW()),
  (2, 1, 1, 'SNACK-LATIAO-L', '2', '大包226g', '{"规格":"大包226g"}', 6.50, 8.00, 3.80, '6901000000028', 40, 10, '/uploads/seed/products/latiao.png', 1, NOW(), NOW()),
  (4, 3, 1, 'SNACK-CHIPS-70', '', '70g', '{"规格":"70g"}', 5.50, 6.90, 3.00, '6901000000035', 90, 30, '/uploads/seed/products/chips.png', 1, NOW(), NOW()),
  (6, 5, 1, 'SNACK-COOKIE-116', '', '116g', '{"规格":"116g"}', 6.90, 8.50, 4.10, '6901000000042', 96, 20, '/uploads/seed/products/cookie.png', 1, NOW(), NOW()),
  (7, 6, 1, 'SNACK-NUTS-7', '', '25g×7袋', '{"规格":"25g×7袋"}', 29.90, 39.90, 19.50, '6901000000059', 42, 10, '/uploads/seed/products/nuts.png', 1, NOW(), NOW()),
  (8, 7, 1, 'SNACK-JERKY-100', '', '100g', '{"规格":"100g"}', 19.90, 25.90, 12.80, '6901000000066', 36, 8, '/uploads/seed/products/jerky.png', 1, NOW(), NOW()),
  (9, 8, 1, 'SNACK-SNOWCAKE-84', '', '84g', '{"规格":"84g"}', 7.50, 9.00, 4.50, '6901000000073', 68, 15, '/uploads/seed/products/snowcake.png', 1, NOW(), NOW()),
  (10, 9, 1, 'SNACK-KONJAC-50', '', '50g', '{"规格":"50g"}', 3.20, 4.00, 1.70, '6901000000080', 120, 25, '/uploads/seed/products/konjac.png', 1, NOW(), NOW()),
  (3, 2, 1, 'DRINK-WATER-550', '', '550ml', '{"规格":"550ml"}', 2.00, 2.80, 1.20, '6901000000097', 200, 40, '/uploads/seed/products/water.png', 1, NOW(), NOW()),
  (11, 10, 1, 'DRINK-COLA-330', '', '330ml', '{"规格":"330ml"}', 3.00, 3.50, 1.90, '6901000000103', 144, 30, '/uploads/seed/products/cola.png', 1, NOW(), NOW()),
  (12, 11, 1, 'DRINK-SPRITE-500', '', '500ml', '{"规格":"500ml"}', 3.50, 4.00, 2.10, '6901000000110', 96, 20, '/uploads/seed/products/sprite.png', 1, NOW(), NOW()),
  (13, 12, 1, 'DRINK-ICETEA-1L', '', '1L', '{"规格":"1L"}', 4.50, 5.50, 2.60, '6901000000127', 84, 18, '/uploads/seed/products/iced-tea.png', 1, NOW(), NOW()),
  (14, 13, 1, 'DRINK-OOLONG-500', '', '500ml', '{"规格":"500ml"}', 5.00, 6.00, 3.00, '6901000000134', 72, 15, '/uploads/seed/products/oolong.png', 1, NOW(), NOW()),
  (15, 14, 1, 'DRINK-ENERGY-250', '', '250ml', '{"规格":"250ml"}', 6.50, 7.50, 4.20, '6901000000141', 60, 12, '/uploads/seed/products/energy-drink.png', 1, NOW(), NOW()),
  (16, 15, 1, 'DRINK-BEER-500', '', '500ml', '{"规格":"500ml"}', 6.00, 7.00, 3.80, '6901000000158', 90, 20, '/uploads/seed/products/beer.png', 1, NOW(), NOW()),
  (17, 16, 1, 'DRINK-LACTIC-6', '', '250ml×6', '{"规格":"250ml×6"}', 12.90, 15.90, 8.60, '6901000000165', 48, 10, '/uploads/seed/products/lactic-drink.png', 1, NOW(), NOW()),
  (18, 17, 1, 'FAST-NOODLE-BEEF', '', '桶装105g', '{"规格":"桶装105g"}', 5.50, 6.50, 3.40, '6901000000172', 120, 25, '/uploads/seed/products/noodle-cup.png', 1, NOW(), NOW()),
  (19, 18, 1, 'FAST-NOODLE-PICKLE', '', '桶装120g', '{"规格":"桶装120g"}', 5.50, 6.50, 3.50, '6901000000189', 108, 25, '/uploads/seed/products/noodle-pickle-cup.png', 1, NOW(), NOW()),
  (20, 19, 1, 'FAST-NOODLE-5PK', '', '5连包', '{"规格":"5连包"}', 13.90, 16.90, 9.20, '6901000000196', 54, 12, '/uploads/seed/products/noodle-5pack.png', 1, NOW(), NOW()),
  (21, 20, 1, 'FAST-SELFHEAT-RICE', '', '260g', '{"规格":"260g"}', 19.90, 24.90, 13.20, '6901000000202', 30, 8, '/uploads/seed/products/selfheat-rice.png', 1, NOW(), NOW()),
  (22, 21, 1, 'FAST-SAUSAGE-10', '', '60g×10支', '{"规格":"60g×10支"}', 15.90, 18.90, 10.50, '6901000000219', 66, 15, '/uploads/seed/products/sausage.png', 1, NOW(), NOW()),
  (23, 22, 1, 'FAST-PICKLE-70', '', '70g', '{"规格":"70g"}', 3.50, 4.50, 2.00, '6901000000226', 110, 25, '/uploads/seed/products/pickle.png', 1, NOW(), NOW()),
  (24, 23, 1, 'FAST-LUNCHEON-340', '', '340g', '{"规格":"340g"}', 12.90, 15.90, 8.40, '6901000000233', 40, 10, '/uploads/seed/products/luncheon-jar.png', 1, NOW(), NOW()),
  (25, 24, 1, 'MILK-PURE-12', '3', '250ml×12盒', '{"规格":"250ml×12盒"}', 42.90, 52.90, 33.50, '6901000000240', 50, 12, '/uploads/seed/products/milk-carton.png', 1, NOW(), NOW()),
  (26, 24, 1, 'MILK-PURE-24', '4', '250ml×24盒', '{"规格":"250ml×24盒"}', 82.90, 99.90, 65.80, '6901000000257', 24, 6, '/uploads/seed/products/milk-carton.png', 1, NOW(), NOW()),
  (27, 25, 1, 'MILK-CALCIUM-12', '', '250ml×12盒', '{"规格":"250ml×12盒"}', 59.90, 69.90, 46.50, '6901000000264', 28, 8, '/uploads/seed/products/milk-calcium.png', 1, NOW(), NOW()),
  (28, 26, 1, 'MILK-YOGURT-12', '', '205g×12', '{"规格":"205g×12"}', 49.90, 59.90, 37.20, '6901000000271', 32, 8, '/uploads/seed/products/yogurt-bottle.png', 1, NOW(), NOW()),
  (29, 27, 1, 'MILK-FRESH-950', '', '950ml', '{"规格":"950ml"}', 15.90, 18.90, 11.20, '6901000000288', 8, 15, '/uploads/seed/products/fresh-milk.png', 1, NOW(), NOW()),
  (30, 28, 1, 'BAKERY-BUN-400', '', '400g', '{"规格":"400g"}', 12.90, 15.90, 8.20, '6901000000295', 45, 10, '/uploads/seed/products/soft-bread.png', 1, NOW(), NOW()),
  (31, 29, 1, 'BAKERY-TOAST-400', '', '400g', '{"规格":"400g"}', 9.90, 12.90, 6.30, '6901000000301', 20, 5, '/uploads/seed/products/toast.png', 1, NOW(), NOW()),
  (32, 30, 1, 'GRAIN-RICE-5KG', '5', '5kg', '{"规格":"5kg"}', 39.90, 46.90, 31.50, '6901000000318', 50, 12, '/uploads/seed/products/rice-bag.png', 1, NOW(), NOW()),
  (33, 30, 1, 'GRAIN-RICE-10KG', '6', '10kg', '{"规格":"10kg"}', 74.90, 89.90, 61.00, '6901000000325', 22, 5, '/uploads/seed/products/rice-bag.png', 1, NOW(), NOW()),
  (34, 31, 1, 'GRAIN-OIL-5L', '', '5L', '{"规格":"5L"}', 79.90, 92.90, 62.50, '6901000000332', 24, 6, '/uploads/seed/products/oil-jug.png', 1, NOW(), NOW()),
  (35, 32, 1, 'GRAIN-SOYSAUCE', '', '1.9L', '{"规格":"1.9L"}', 16.90, 19.90, 11.80, '6901000000349', 38, 10, '/uploads/seed/products/soy-sauce.png', 1, NOW(), NOW()),
  (36, 33, 1, 'GRAIN-VINEGAR', '', '500ml', '{"规格":"500ml"}', 6.90, 8.90, 4.30, '6901000000356', 46, 10, '/uploads/seed/products/vinegar.png', 1, NOW(), NOW()),
  (37, 34, 1, 'GRAIN-SALT-400', '', '400g', '{"规格":"400g"}', 2.50, 3.00, 1.40, '6901000000363', 90, 20, '/uploads/seed/products/salt.png', 1, NOW(), NOW()),
  (38, 35, 1, 'DAILY-TISSUE-6', '7', '3层×6包', '{"规格":"3层×6包"}', 12.90, 15.90, 8.90, '6901000000370', 100, 25, '/uploads/seed/products/tissue-pack.png', 1, NOW(), NOW()),
  (39, 35, 1, 'DAILY-TISSUE-12', '8', '3层×12包', '{"规格":"3层×12包"}', 23.90, 29.90, 17.20, '6901000000387', 40, 10, '/uploads/seed/products/tissue-pack.png', 1, NOW(), NOW()),
  (40, 36, 1, 'DAILY-LAUNDRY-3KG', '', '3kg', '{"规格":"3kg"}', 29.90, 36.90, 20.50, '6901000000394', 26, 6, '/uploads/seed/products/laundry-jug.png', 1, NOW(), NOW()),
  (41, 37, 1, 'DAILY-DISHSOAP', '', '1.5kg', '{"规格":"1.5kg"}', 12.90, 15.90, 8.30, '6901000000400', 34, 8, '/uploads/seed/products/dish-soap.png', 1, NOW(), NOW()),
  (42, 38, 1, 'DAILY-PASTE-120', '', '120g', '{"规格":"120g"}', 15.90, 19.90, 9.80, '6901000000417', 40, 10, '/uploads/seed/products/toothpaste.png', 1, NOW(), NOW()),
  (43, 39, 1, 'FROZEN-DUMPLING-500', '9', '500g', '{"规格":"500g"}', 12.90, 15.90, 8.60, '6901000000424', 35, 10, '/uploads/seed/products/dumpling-box.png', 1, NOW(), NOW()),
  (44, 39, 1, 'FROZEN-DUMPLING-1KG', '10', '1kg', '{"规格":"1kg"}', 21.90, 26.90, 15.40, '6901000000431', 18, 5, '/uploads/seed/products/dumpling-box.png', 1, NOW(), NOW()),
  (45, 40, 1, 'FROZEN-TANGYUAN', '', '500g', '{"规格":"500g"}', 9.90, 12.90, 6.40, '6901000000448', 40, 10, '/uploads/seed/products/tangyuan.png', 1, NOW(), NOW()),
  (46, 41, 1, 'FROZEN-ICECREAM', '', '75g', '{"规格":"75g"}', 3.50, 4.50, 1.90, '6901000000455', 0, 20, '/uploads/seed/products/icecream.png', 1, NOW(), NOW()),
  (47, 42, 1, 'CANDY-CHOCO-43', '', '43g', '{"规格":"43g"}', 8.50, 9.90, 5.30, '6901000000462', 60, 15, '/uploads/seed/products/chocolate.png', 1, NOW(), NOW()),
  (48, 43, 1, 'CANDY-BAR-51', '', '51g', '{"规格":"51g"}', 6.50, 7.50, 3.90, '6901000000479', 72, 18, '/uploads/seed/products/energy-bar.png', 1, NOW(), NOW()),
  (49, 44, 1, 'CANDY-GUM-64', '', '64g', '{"规格":"64g"}', 9.90, 11.90, 6.10, '6901000000486', 6, 10, '/uploads/seed/products/gum-jar.png', 1, NOW(), NOW()),
  (50, 45, 1, 'BREW-COFFEE-30', '', '15g×30条', '{"规格":"15g×30条"}', 39.90, 46.90, 28.60, '6901000000493', 30, 8, '/uploads/seed/products/coffee-box.png', 1, NOW(), NOW()),
  (51, 46, 1, 'BREW-MILKTEA-80', '', '80g', '{"规格":"80g"}', 6.50, 7.90, 3.80, '6901000000509', 54, 12, '/uploads/seed/products/milktea-cup.png', 1, NOW(), NOW()),
  (52, 47, 1, 'BREW-OATS-400', '', '400g', '{"规格":"400g"}', 19.90, 24.90, 13.20, '6901000000516', 26, 6, '/uploads/seed/products/oats-jar.png', 1, NOW(), NOW()),
  (5, 4, 2, 'EGG-30P', '', '30枚/箱', '{"规格":"30枚/箱"}', 12.90, 15.90, 9.50, '6901000000523', 100, 20, '/uploads/seed/products/egg-carton.png', 1, NOW(), NOW()),
  (53, 48, 2, 'MILK-PURE-C2-12', '', '250ml×12盒', '{"规格":"250ml×12盒"}', 45.90, 52.90, 35.80, '6901000000530', 24, 6, '/uploads/seed/products/milk-carton.png', 1, NOW(), NOW()),
  (54, 49, 2, 'MILK-YOGURT-C2-12', '', '205g×12', '{"规格":"205g×12"}', 52.90, 59.90, 39.80, '6901000000547', 18, 5, '/uploads/seed/products/yogurt-bottle.png', 1, NOW(), NOW()),
  (55, 50, 2, 'SNACK-CHIPS-C2-70', '', '70g', '{"规格":"70g"}', 5.90, 6.90, 3.20, '6901000000554', 60, 15, '/uploads/seed/products/chips.png', 1, NOW(), NOW()),
  (56, 51, 2, 'SNACK-COOKIE-C2-116', '', '116g', '{"规格":"116g"}', 7.20, 8.50, 4.40, '6901000000561', 40, 10, '/uploads/seed/products/cookie.png', 1, NOW(), NOW()),
  (57, 52, 2, 'DRINK-WATER-C2-550', '', '550ml', '{"规格":"550ml"}', 2.00, 2.50, 1.25, '6901000000578', 96, 24, '/uploads/seed/products/water.png', 1, NOW(), NOW()),
  (58, 53, 2, 'DRINK-SPRITE-C2-500', '', '500ml', '{"规格":"500ml"}', 3.80, 4.00, 2.20, '6901000000585', 48, 12, '/uploads/seed/products/sprite.png', 1, NOW(), NOW()),
  (59, 54, 2, 'FAST-NOODLE-C2-BEEF', '', '桶装105g', '{"规格":"桶装105g"}', 5.80, 6.50, 3.50, '6901000000592', 45, 12, '/uploads/seed/products/noodle-cup.png', 1, NOW(), NOW()),
  (60, 55, 2, 'GRAIN-RICE-C2-5KG', '', '5kg', '{"规格":"5kg"}', 42.90, 46.90, 33.80, '6901000000608', 18, 5, '/uploads/seed/products/rice-bag.png', 1, NOW(), NOW()),
  (61, 56, 2, 'DAILY-TISSUE-C2-6', '', '3层×6包', '{"规格":"3层×6包"}', 13.50, 15.90, 9.20, '6901000000615', 30, 8, '/uploads/seed/products/tissue-pack.png', 1, NOW(), NOW());

-- ============ 批次库存（FEFO 基础数据） ============
-- 各 SKU 批次 quantity 之和等于 product_skus.stock；否则订单扣减走 FEFO 会因
-- 「批次库存不足」失败（销售扣减只认 stock_batches）。
-- 日期一律用相对时间：预包装食品给 3~24 个月保质期，部分留空（盐/大米/纸品不限期），
-- 鲜奶与临期批次给 2~3 天，用于演示临期预警与 FEFO 顺序，避免种子过期后演示失真。
-- split 标记的 SKU 拆两批（6:4，先出近效期批），其余单批。
-- MANUAL_BATCHES 里的 SKU 用手写批次，与下方「进销存」段落的入库单/报损单/库存流水咬合：
-- sku 4（乐游 原味薯片）初始 30/20 → 入库 +50（IN-SEED-0001）→ 报损 10（扣 A 批），终态 20/20/50 = 90。
INSERT INTO `stock_batches` (`id`, `community_id`, `sku_id`, `batch_no`, `production_date`, `expire_date`, `quantity`, `unit_cost`, `status`, `source_type`, `ref_no`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, 'INIT-SNACK-LATIAO-S-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 47 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 144 DAY), '%Y-%m-%d'), 48, 2.00, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (2, 1, 1, 'INIT-SNACK-LATIAO-S-B', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 35 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 150 DAY), '%Y-%m-%d'), 32, 2.00, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (3, 1, 2, 'INIT-SNACK-LATIAO-L-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 24 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 150 DAY), '%Y-%m-%d'), 40, 3.80, 1, 1, 'INIT-SEED', NOW(), NOW()),
  -- 初始库存（已扣报损 10）
  (4, 1, 4, 'INIT-SNACK-CHIPS-70-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 25 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 215 DAY), '%Y-%m-%d'), 20, 3.00, 1, 1, 'INIT-SEED', DATE_SUB(NOW(), INTERVAL 25 DAY), DATE_SUB(NOW(), INTERVAL 25 DAY)),
  -- 初始库存
  (5, 1, 4, 'INIT-SNACK-CHIPS-70-B', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 20 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 220 DAY), '%Y-%m-%d'), 20, 3.00, 1, 1, 'INIT-SEED', DATE_SUB(NOW(), INTERVAL 20 DAY), DATE_SUB(NOW(), INTERVAL 20 DAY)),
  -- 直接入库单批次
  (6, 1, 4, 'IN-SEED-0001', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 5 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 235 DAY), '%Y-%m-%d'), 50, 3.00, 1, 1, 'IN-SEED-0001', DATE_SUB(NOW(), INTERVAL 5 DAY), DATE_SUB(NOW(), INTERVAL 5 DAY)),
  (7, 1, 6, 'INIT-SNACK-COOKIE-116-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 18 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 210 DAY), '%Y-%m-%d'), 96, 4.10, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (8, 1, 7, 'INIT-SNACK-NUTS-7-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 25 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 180 DAY), '%Y-%m-%d'), 42, 19.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (9, 1, 8, 'INIT-SNACK-JERKY-100-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 30 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 150 DAY), '%Y-%m-%d'), 36, 12.80, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (10, 1, 9, 'INIT-SNACK-SNOWCAKE-84-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 22 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 240 DAY), '%Y-%m-%d'), 68, 4.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (11, 1, 10, 'INIT-SNACK-KONJAC-50-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 15 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 270 DAY), '%Y-%m-%d'), 120, 1.70, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (12, 1, 3, 'INIT-DRINK-WATER-550-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 52 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 714 DAY), '%Y-%m-%d'), 120, 1.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (13, 1, 3, 'INIT-DRINK-WATER-550-B', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 40 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 720 DAY), '%Y-%m-%d'), 80, 1.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (14, 1, 11, 'INIT-DRINK-COLA-330-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 30 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 365 DAY), '%Y-%m-%d'), 144, 1.90, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (15, 1, 12, 'INIT-DRINK-SPRITE-500-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 26 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 300 DAY), '%Y-%m-%d'), 96, 2.10, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (16, 1, 13, 'INIT-DRINK-ICETEA-1L-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 28 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 270 DAY), '%Y-%m-%d'), 84, 2.60, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (17, 1, 14, 'INIT-DRINK-OOLONG-500-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 20 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 300 DAY), '%Y-%m-%d'), 72, 3.00, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (18, 1, 15, 'INIT-DRINK-ENERGY-250-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 32 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 365 DAY), '%Y-%m-%d'), 60, 4.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (19, 1, 16, 'INIT-DRINK-BEER-500-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 24 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 180 DAY), '%Y-%m-%d'), 90, 3.80, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (20, 1, 17, 'INIT-DRINK-LACTIC-6-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 16 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 180 DAY), '%Y-%m-%d'), 48, 8.60, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (21, 1, 18, 'INIT-FAST-NOODLE-BEEF-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 30 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 180 DAY), '%Y-%m-%d'), 120, 3.40, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (22, 1, 19, 'INIT-FAST-NOODLE-PICKLE-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 28 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 180 DAY), '%Y-%m-%d'), 108, 3.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (23, 1, 20, 'INIT-FAST-NOODLE-5PK-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 20 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 210 DAY), '%Y-%m-%d'), 54, 9.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (24, 1, 21, 'INIT-FAST-SELFHEAT-RICE-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 18 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 270 DAY), '%Y-%m-%d'), 30, 13.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (25, 1, 22, 'INIT-FAST-SAUSAGE-10-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 22 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 180 DAY), '%Y-%m-%d'), 66, 10.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (26, 1, 23, 'INIT-FAST-PICKLE-70-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 40 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 365 DAY), '%Y-%m-%d'), 110, 2.00, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (27, 1, 24, 'INIT-FAST-LUNCHEON-340-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 45 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 720 DAY), '%Y-%m-%d'), 40, 8.40, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (28, 1, 25, 'INIT-MILK-PURE-12-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 42 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 174 DAY), '%Y-%m-%d'), 30, 33.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (29, 1, 25, 'INIT-MILK-PURE-12-B', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 30 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 180 DAY), '%Y-%m-%d'), 20, 33.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (30, 1, 26, 'INIT-MILK-PURE-24-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 30 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 180 DAY), '%Y-%m-%d'), 24, 65.80, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (31, 1, 27, 'INIT-MILK-CALCIUM-12-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 26 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 180 DAY), '%Y-%m-%d'), 28, 46.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (32, 1, 28, 'INIT-MILK-YOGURT-12-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 20 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 90 DAY), '%Y-%m-%d'), 32, 37.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (33, 1, 29, 'INIT-MILK-FRESH-950-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 1 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 3 DAY), '%Y-%m-%d'), 8, 11.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (34, 1, 30, 'INIT-BAKERY-BUN-400-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 4 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 15 DAY), '%Y-%m-%d'), 45, 8.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (35, 1, 31, 'INIT-BAKERY-TOAST-400-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 2 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 10 DAY), '%Y-%m-%d'), 20, 6.30, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (36, 1, 32, 'INIT-GRAIN-RICE-5KG-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 32 DAY), '%Y-%m-%d'), NULL, 30, 31.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (37, 1, 32, 'INIT-GRAIN-RICE-5KG-B', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 20 DAY), '%Y-%m-%d'), NULL, 20, 31.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (38, 1, 33, 'INIT-GRAIN-RICE-10KG-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 20 DAY), '%Y-%m-%d'), NULL, 22, 61.00, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (39, 1, 34, 'INIT-GRAIN-OIL-5L-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 35 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 540 DAY), '%Y-%m-%d'), 24, 62.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (40, 1, 35, 'INIT-GRAIN-SOYSAUCE-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 30 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 540 DAY), '%Y-%m-%d'), 38, 11.80, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (41, 1, 36, 'INIT-GRAIN-VINEGAR-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 28 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 720 DAY), '%Y-%m-%d'), 46, 4.30, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (42, 1, 37, 'INIT-GRAIN-SALT-400-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 40 DAY), '%Y-%m-%d'), NULL, 90, 1.40, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (43, 1, 38, 'INIT-DAILY-TISSUE-6-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 37 DAY), '%Y-%m-%d'), NULL, 60, 8.90, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (44, 1, 38, 'INIT-DAILY-TISSUE-6-B', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 25 DAY), '%Y-%m-%d'), NULL, 40, 8.90, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (45, 1, 39, 'INIT-DAILY-TISSUE-12-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 25 DAY), '%Y-%m-%d'), NULL, 40, 17.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (46, 1, 40, 'INIT-DAILY-LAUNDRY-3KG-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 30 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 1080 DAY), '%Y-%m-%d'), 26, 20.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (47, 1, 41, 'INIT-DAILY-DISHSOAP-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 26 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 1080 DAY), '%Y-%m-%d'), 34, 8.30, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (48, 1, 42, 'INIT-DAILY-PASTE-120-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 32 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 1080 DAY), '%Y-%m-%d'), 40, 9.80, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (49, 1, 43, 'INIT-FROZEN-DUMPLING-500-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 27 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 359 DAY), '%Y-%m-%d'), 21, 8.60, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (50, 1, 43, 'INIT-FROZEN-DUMPLING-500-B', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 15 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 365 DAY), '%Y-%m-%d'), 14, 8.60, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (51, 1, 44, 'INIT-FROZEN-DUMPLING-1KG-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 15 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 365 DAY), '%Y-%m-%d'), 18, 15.40, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (52, 1, 45, 'INIT-FROZEN-TANGYUAN-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 18 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 365 DAY), '%Y-%m-%d'), 40, 6.40, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (53, 1, 47, 'INIT-CANDY-CHOCO-43-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 22 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 365 DAY), '%Y-%m-%d'), 60, 5.30, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (54, 1, 48, 'INIT-CANDY-BAR-51-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 20 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 300 DAY), '%Y-%m-%d'), 72, 3.90, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (55, 1, 49, 'INIT-CANDY-GUM-64-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 25 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 540 DAY), '%Y-%m-%d'), 6, 6.10, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (56, 1, 50, 'INIT-BREW-COFFEE-30-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 28 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 540 DAY), '%Y-%m-%d'), 30, 28.60, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (57, 1, 51, 'INIT-BREW-MILKTEA-80-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 20 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 360 DAY), '%Y-%m-%d'), 54, 3.80, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (58, 1, 52, 'INIT-BREW-OATS-400-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 24 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 360 DAY), '%Y-%m-%d'), 26, 13.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (59, 2, 5, 'INIT-EGG-30P-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 36 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 9 DAY), '%Y-%m-%d'), 60, 9.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (60, 2, 5, 'INIT-EGG-30P-B', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 24 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 15 DAY), '%Y-%m-%d'), 40, 9.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (61, 2, 53, 'INIT-MILK-PURE-C2-12-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 22 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 180 DAY), '%Y-%m-%d'), 24, 35.80, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (62, 2, 54, 'INIT-MILK-YOGURT-C2-12-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 15 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 90 DAY), '%Y-%m-%d'), 18, 39.80, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (63, 2, 55, 'INIT-SNACK-CHIPS-C2-70-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 18 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 240 DAY), '%Y-%m-%d'), 60, 3.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (64, 2, 56, 'INIT-SNACK-COOKIE-C2-116-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 16 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 210 DAY), '%Y-%m-%d'), 40, 4.40, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (65, 2, 57, 'INIT-DRINK-WATER-C2-550-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 30 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 720 DAY), '%Y-%m-%d'), 96, 1.25, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (66, 2, 58, 'INIT-DRINK-SPRITE-C2-500-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 22 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 300 DAY), '%Y-%m-%d'), 48, 2.20, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (67, 2, 59, 'INIT-FAST-NOODLE-C2-BEEF-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 20 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 180 DAY), '%Y-%m-%d'), 45, 3.50, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (68, 2, 60, 'INIT-GRAIN-RICE-C2-5KG-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 18 DAY), '%Y-%m-%d'), NULL, 18, 33.80, 1, 1, 'INIT-SEED', NOW(), NOW()),
  (69, 2, 61, 'INIT-DAILY-TISSUE-C2-6-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 20 DAY), '%Y-%m-%d'), NULL, 30, 9.20, 1, 1, 'INIT-SEED', NOW(), NOW());

-- ============ Banner 轮播 ============
-- position：1旧首页(已废弃) 2分类页(现首页)；community_id=0 表示全平台通用
INSERT INTO `banners` (`id`, `community_id`, `title`, `image_url`, `link_url`, `position`, `sort`, `status`, `start_time`, `end_time`, `created_at`, `updated_at`)
VALUES
  (1, 0, '新人专享满减', 'https://placehold.co/750x300/ff6b35/white?text=Banner1', '#', 2, 1, 1, NULL, NULL, NOW(), NOW()),
  (2, 0, '零食饮品周周上新', 'https://placehold.co/750x300/4caf50/white?text=Banner2', '#', 2, 2, 1, NULL, NULL, NOW(), NOW()),
  (3, 2, '阳光花园精选', 'https://placehold.co/750x300/2196f3/white?text=Banner3', '#', 2, 1, 1, NULL, NULL, NOW(), NOW());

-- ============ 营销活动 ============
-- 满减：start/end 为 NULL 表示长期有效
INSERT INTO `full_reduction_rules` (`id`, `community_id`, `name`, `threshold`, `reduce_amount`, `start_time`, `end_time`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, '全场满30减5', 30.00, 5.00, NULL, NULL, 1, NOW(), NOW());

-- 优惠券：满20减5，长期可领，领取后 30 天有效；限定「酒水饮料」分类可用（见下方范围）
INSERT INTO `coupons` (`id`, `community_id`, `name`, `type`, `threshold`, `discount_value`, `total_count`, `received_count`, `used_count`, `per_user_limit`, `start_time`, `end_time`, `valid_days`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, '新人满20减5券', 1, 20.00, 5.00, 100, 2, 1, 2, NULL, NULL, 30, 1, NOW(), NOW());

-- 券1 的商品范围：仅「酒水饮料」分类（category_id=2）可用；订单3（瓶装水×10）即为其正例；
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
  (1, 1, 1, 1, 1, 2, 1, '魔小鲜 大面筋辣条', '小包106g', '/uploads/seed/products/latiao.png', NOW(), NOW()),
  (2, 1, 1, 3, 4, 1, 0, '乐游 原味薯片', '70g', '/uploads/seed/products/chips.png', NOW(), NOW());

-- ============ 满免邮活动（社区1：满39免运费，排除「乐游 原味薯片」演示不参与商品） ============
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
  (1, 1, '限时秒杀·零食饮品特惠', DATE_SUB(NOW(), INTERVAL 1 HOUR), DATE_ADD(NOW(), INTERVAL 2 DAY), 1, NOW(), NOW());

-- 秒杀价低于 SKU 原价演示抢购场景：辣条小包 3.50→2.99、瓶装水 2.00→1.69
INSERT INTO `flash_sale_items` (`id`, `flash_sale_id`, `sku_id`, `flash_price`, `stock`, `sold_count`, `limit_per_user`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, 2.99, 50, 3, 2, 1, NOW(), NOW()),
  (2, 1, 3, 1.69, 100, 12, 5, 1, NOW(), NOW());

-- ============ 供应商（社区1） ============
INSERT INTO `suppliers` (`id`, `community_id`, `name`, `contact_name`, `phone`, `address`, `remark`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, '渝百快消品批发商行', '王老板', '023-68111111', '重庆市九龙坡区西彭镇食品批发城B区3号', '休闲零食/方便速食/饮料，长期合作账期月结', 1, NOW(), NOW()),
  (2, 1, '洁士日化供应链', '赵姐', '023-68222222', '重庆市江津区双福商贸城C区12号', '纸品与洗涤日化', 1, NOW(), NOW());

-- ============ 采购单（在途：已下单未入库；入库后由服务生成批次，此处不预置批次避免与现有 stock 汇总重复计数） ============
-- status 枚举：1草稿 2已下单 3部分入库 4已完结 5已取消
INSERT INTO `purchase_orders` (`id`, `community_id`, `purchase_no`, `supplier_id`, `status`, `total_amount`, `operator_id`, `remark`, `created_at`, `updated_at`)
VALUES
  (1, 1, 'PO20261018001', 1, 2, 260.00, 1, '周末档期例行补货', NOW(), NOW());

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
  -- 订单1：C端待支付·配送·无券（辣条小包×2 + 瓶装水×5，17.00 + 运费3.00，满足 15 元起送）
  (1, 'SEED20261018001', 1, 1, 1, 17.00, 0.00, 3.00, 20.00, 2, '尽快送达', 1, '张三', '13800138000', '重庆市九龙坡区彩云大道 8 号 2 单元 301', '辣条挑最新日期的', 0, NULL, 1, 0, '', 0.00, 0, NULL, NULL, NULL, NULL, NULL, NULL, '', DATE_SUB(NOW(), INTERVAL 5 MINUTE), DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  -- 订单2：C端备货中·自提·微信支付（薯片×3，16.50，满足 15 元起送）
  (2, 'SEED20261018002', 1, 1, 3, 16.50, 0.00, 0.00, 16.50, 1, '今天晚饭后自提', 0, '张三', '13800138000', '金茂悦7栋1楼保安处', '', 0, '10000001', 1, 0, '', 0.00, 0, NULL, NULL, DATE_SUB(NOW(), INTERVAL 110 MINUTE), NULL, NULL, NULL, '', DATE_SUB(NOW(), INTERVAL 120 MINUTE), DATE_SUB(NOW(), INTERVAL 105 MINUTE)),
  -- 订单3：C端已完成·自提·支付宝·用券1（瓶装水×10，20.00 - 5.00 = 15.00；券限酒水饮料分类，此单为正例）
  -- 完成时间取 1 天前，处于默认 168h 售后窗口内，可直接演示 C 端申请退款
  (3, 'SEED20261018003', 1, 1, 5, 20.00, 5.00, 0.00, 15.00, 1, '周末自提', 0, '张三', '13800138000', '金茂悦7栋1楼保安处', '', 1, '10000002', 1, 0, '', 0.00, 0, NULL, NULL, DATE_SUB(NOW(), INTERVAL 2870 MINUTE), NULL, DATE_SUB(NOW(), INTERVAL 1440 MINUTE), NULL, '', DATE_SUB(NOW(), INTERVAL 2880 MINUTE), DATE_SUB(NOW(), INTERVAL 1440 MINUTE)),
  -- 订单4：C端已取消·配送（薯片×3，16.50 + 3.00；用户主动取消）
  (4, 'SEED20261018004', 1, 1, 6, 16.50, 0.00, 3.00, 19.50, 2, '尽快送达', 1, '张三', '13800138000', '重庆市九龙坡区彩云大道 8 号 2 单元 301', '', 0, NULL, 1, 0, '', 0.00, 0, NULL, NULL, NULL, NULL, NULL, DATE_SUB(NOW(), INTERVAL 1425 MINUTE), '用户误下单，主动取消', DATE_SUB(NOW(), INTERVAL 1440 MINUTE), DATE_SUB(NOW(), INTERVAL 1425 MINUTE)),
  -- 订单5：C端已完成·到店·微信（散养鲜鸡蛋×1，12.90；到店收款即自动完结）
  (5, 'SEED20261018005', 2, 2, 5, 12.90, 0.00, 0.00, 12.90, 3, '到店取货', 0, '李四', '13900139000', '花园南门快递柜旁自提点', '', 0, NULL, 1, 0, '', 0.00, 0, NULL, NULL, DATE_SUB(NOW(), INTERVAL 120 MINUTE), NULL, DATE_SUB(NOW(), INTERVAL 120 MINUTE), NULL, '', DATE_SUB(NOW(), INTERVAL 180 MINUTE), DATE_SUB(NOW(), INTERVAL 120 MINUTE)),
  -- 订单6：C端待支付·自提（散养鲜鸡蛋×2，25.80；created_at 在超时窗内）
  (6, 'SEED20261018006', 2, 2, 1, 25.80, 0.00, 0.00, 25.80, 1, '明天上午自提', 0, '李四', '13900139000', '花园南门快递柜旁自提点', '', 0, '20000001', 1, 0, '', 0.00, 0, NULL, NULL, NULL, NULL, NULL, NULL, '', DATE_SUB(NOW(), INTERVAL 5 MINUTE), DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  -- 订单7：C端配送中·配送·支付宝（散养鲜鸡蛋×2，25.80 + 运费2.00；已发货，deliver_time 在自动完成窗内）
  (7, 'SEED20261018007', 2, 2, 4, 25.80, 0.00, 2.00, 27.80, 2, '工作日晚间', 2, '李四', '13900139000', '重庆市南岸区阳光路 8 号 5 栋 1201', '送货上门', 0, NULL, 1, 0, '', 0.00, 0, '顺丰速运', 'SF1000000001', DATE_SUB(NOW(), INTERVAL 2870 MINUTE), DATE_SUB(NOW(), INTERVAL 60 MINUTE), NULL, NULL, '', DATE_SUB(NOW(), INTERVAL 2880 MINUTE), DATE_SUB(NOW(), INTERVAL 60 MINUTE)),
  -- 订单8：收银台代客·到店·现金·已完成·挂历史班次1（辣条大包×1，收银台改价 6.50→6.00；
  -- 改价口径：item.price 记改后价、original_price 留档案价快照、manual_discount 为 0，与 AdminCreateOrder 一致）
  (8, 'SEED20261018008', 1, 1, 5, 6.00, 0.00, 0.00, 6.00, 3, '到店取货', 0, '张三', '13800138000', '金茂悦7栋1楼保安处', '收银台改价 6.50→6.00', 0, NULL, 2, 1, '系统管理员', 0.00, 1, NULL, NULL, DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 605 MINUTE), NULL, DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 605 MINUTE), NULL, '', DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 602 MINUTE), DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 605 MINUTE)),
  -- 订单9：C端已完成·配送·微信（今日经营数据：辣条小包×2 + 瓶装水×5，17.00 + 3.00）
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
  (1, 1, 1, 1, '魔小鲜 大面筋辣条', '小包106g', '/uploads/seed/products/latiao.png', 3.50, 2, 7.00, 3.50, 0, DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (2, 1, 2, 3, '碧源 饮用天然水', '550ml', '/uploads/seed/products/water.png', 2.00, 5, 10.00, 2.00, 0, DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (3, 2, 3, 4, '乐游 原味薯片', '70g', '/uploads/seed/products/chips.png', 5.50, 3, 16.50, 5.50, 0, DATE_SUB(NOW(), INTERVAL 120 MINUTE)),
  (4, 3, 2, 3, '碧源 饮用天然水', '550ml', '/uploads/seed/products/water.png', 2.00, 10, 20.00, 2.00, 0, DATE_SUB(NOW(), INTERVAL 2880 MINUTE)),
  (5, 4, 3, 4, '乐游 原味薯片', '70g', '/uploads/seed/products/chips.png', 5.50, 3, 16.50, 5.50, 0, DATE_SUB(NOW(), INTERVAL 1440 MINUTE)),
  (6, 5, 4, 5, '农欢 散养鲜鸡蛋', '30枚/箱', '/uploads/seed/products/egg-carton.png', 12.90, 1, 12.90, 12.90, 0, DATE_SUB(NOW(), INTERVAL 180 MINUTE)),
  (7, 6, 4, 5, '农欢 散养鲜鸡蛋', '30枚/箱', '/uploads/seed/products/egg-carton.png', 12.90, 2, 25.80, 12.90, 0, DATE_SUB(NOW(), INTERVAL 5 MINUTE)),
  (8, 7, 4, 5, '农欢 散养鲜鸡蛋', '30枚/箱', '/uploads/seed/products/egg-carton.png', 12.90, 2, 25.80, 12.90, 0, DATE_SUB(NOW(), INTERVAL 2880 MINUTE)),
  (9, 8, 1, 2, '魔小鲜 大面筋辣条', '大包226g', '/uploads/seed/products/latiao.png', 6.00, 1, 6.00, 6.50, 1, DATE_ADD(DATE_SUB(CURDATE(), INTERVAL 1 DAY), INTERVAL 602 MINUTE)),
  (10, 9, 1, 1, '魔小鲜 大面筋辣条', '小包106g', '/uploads/seed/products/latiao.png', 3.50, 2, 7.00, 3.50, 0, DATE_SUB(NOW(), INTERVAL 40 MINUTE)),
  (11, 9, 2, 3, '碧源 饮用天然水', '550ml', '/uploads/seed/products/water.png', 2.00, 5, 10.00, 2.00, 0, DATE_SUB(NOW(), INTERVAL 40 MINUTE)),
  (12, 10, 1, 1, '魔小鲜 大面筋辣条', '小包106g', '/uploads/seed/products/latiao.png', 3.50, 2, 7.00, 3.50, 0, DATE_SUB(NOW(), INTERVAL 7200 MINUTE)),
  (13, 10, 2, 3, '碧源 饮用天然水', '550ml', '/uploads/seed/products/water.png', 2.00, 5, 10.00, 2.00, 0, DATE_SUB(NOW(), INTERVAL 7200 MINUTE)),
  (14, 11, 3, 4, '乐游 原味薯片', '70g', '/uploads/seed/products/chips.png', 5.50, 3, 16.50, 5.50, 0, DATE_SUB(NOW(), INTERVAL 1440 MINUTE));

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
-- 订单11：部分退款（薯片 1 包），已审批进入渠道退款中（订单 7 退款中）
INSERT INTO `refunds` (`id`, `refund_no`, `order_id`, `user_id`, `payment_no`, `amount`, `freight_amount`, `type`, `reason`, `status`, `reject_reason`, `operator_id`, `refund_time`, `last_checked_at`, `retry_count`, `created_at`, `updated_at`)
VALUES
  (1, 'SEEDREFUND-0010', 10, 1, 'SEEDPAY-REFUND-0010', 20.00, 3.00, 1, '商品质量问题，申请整单退款', 5, '', 1, DATE_SUB(NOW(), INTERVAL 4260 MINUTE), NULL, 0, DATE_SUB(NOW(), INTERVAL 4800 MINUTE), DATE_SUB(NOW(), INTERVAL 4260 MINUTE)),
  (2, 'SEEDREFUND-0011', 11, 1, 'SEEDPAY-REFUND-0011', 5.50, 0.00, 2, '部分商品缺货，申请退款', 4, '', 1, NULL, DATE_SUB(NOW(), INTERVAL 60 MINUTE), 0, DATE_SUB(NOW(), INTERVAL 120 MINUTE), DATE_SUB(NOW(), INTERVAL 60 MINUTE));

-- 退款明细：order_item_id 对应上方 order_items.id；仅商品金额，运费记在 refunds.freight_amount
INSERT INTO `refund_items` (`id`, `refund_id`, `order_item_id`, `sku_id`, `product_name`, `spec_name`, `quantity`, `refund_amount`, `created_at`)
VALUES
  (1, 1, 12, 1, '魔小鲜 大面筋辣条', '小包106g', 2, 7.00, DATE_SUB(NOW(), INTERVAL 4800 MINUTE)),
  (2, 1, 13, 3, '碧源 饮用天然水', '550ml', 5, 10.00, DATE_SUB(NOW(), INTERVAL 4800 MINUTE)),
  (3, 2, 14, 4, '乐游 原味薯片', '70g', 1, 5.50, DATE_SUB(NOW(), INTERVAL 120 MINUTE));

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
-- 单据都挂在 sku 4（乐游 原味薯片，社区1，成本 3.00）上，与上方 stock_batches 的手写批次咬合：
-- 初始 50（A 批 30 + B 批 20）→ 5 天前直接入库 +50（IN-SEED-0001，批次 id=6）→ 100
--                                → 3 天前报损 10（扣 A 批，A 余 20）→ 90 = product_skus.stock
INSERT INTO `stock_in_orders` (`id`, `community_id`, `in_no`, `supplier_id`, `purchase_order_id`, `type`, `total_amount`, `operator_id`, `remark`, `created_at`, `updated_at`)
VALUES
  (1, 1, 'IN-SEED-0001', 1, 0, 2, 150.00, 1, '零食档期补货（直接入库，无采购单）', DATE_SUB(NOW(), INTERVAL 5 DAY), DATE_SUB(NOW(), INTERVAL 5 DAY));

INSERT INTO `stock_in_order_items` (`id`, `in_order_id`, `sku_id`, `quantity`, `unit_cost`, `batch_no`, `production_date`, `expire_date`, `batch_id`, `created_at`)
VALUES
  (1, 1, 4, 50, 3.00, 'IN-SEED-0001', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 5 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 235 DAY), '%Y-%m-%d'), 6, DATE_SUB(NOW(), INTERVAL 5 DAY));

-- 报损出库单：薯片运输压碎 10 包，100 → 90（扣 INIT-SNACK-CHIPS-70-A 批次，批次 id=4）
INSERT INTO `stock_out_orders` (`id`, `community_id`, `out_no`, `type`, `operator_id`, `remark`, `created_at`, `updated_at`)
VALUES
  (1, 1, 'OUT-SEED-0001', 1, 1, '薯片运输压碎报损', DATE_SUB(NOW(), INTERVAL 3 DAY), DATE_SUB(NOW(), INTERVAL 3 DAY));

INSERT INTO `stock_out_order_items` (`id`, `out_order_id`, `sku_id`, `quantity`, `created_at`)
VALUES
  (1, 1, 4, 10, DATE_SUB(NOW(), INTERVAL 3 DAY));

-- 盘点单（待提交草稿：差异不回写库存，供管理端「盘点」列表与差异展示）
-- system_stock 必须等于当前 product_skus.stock（sku 1 辣条小包 80、sku 4 薯片 90）
INSERT INTO `stock_checks` (`id`, `check_no`, `community_id`, `operator_id`, `status`, `total_items`, `diff_items`, `remark`, `created_at`, `updated_at`)
VALUES
  (1, 'CHECK-SEED-0001', 1, 1, 1, 2, 1, '月度抽盘（草稿，未提交）', DATE_SUB(NOW(), INTERVAL 2 DAY), DATE_SUB(NOW(), INTERVAL 2 DAY));

INSERT INTO `stock_check_items` (`id`, `check_id`, `sku_id`, `system_stock`, `actual_stock`, `diff_quantity`, `created_at`)
VALUES
  (1, 1, 1, 80, 80, 0, DATE_SUB(NOW(), INTERVAL 2 DAY)),
  (2, 1, 4, 90, 88, -2, DATE_SUB(NOW(), INTERVAL 2 DAY));

-- 库存流水：仅落单据产生的两条变动（销售扣减流水由下单链路实时产生）
INSERT INTO `stock_records` (`id`, `sku_id`, `community_id`, `type`, `quantity`, `before_stock`, `after_stock`, `batch_id`, `batch_no`, `production_date`, `expire_date`, `related_order_id`, `ref_no`, `operator_id`, `remark`, `created_at`)
VALUES
  (1, 4, 1, 1, 50, 50, 100, 6, 'IN-SEED-0001', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 5 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 235 DAY), '%Y-%m-%d'), 0, 'IN-SEED-0001', 1, '直接入库', DATE_SUB(NOW(), INTERVAL 5 DAY)),
  (2, 4, 1, 2, -10, 100, 90, 4, 'INIT-SNACK-CHIPS-70-A', DATE_FORMAT(DATE_SUB(CURDATE(), INTERVAL 25 DAY), '%Y-%m-%d'), DATE_FORMAT(DATE_ADD(CURDATE(), INTERVAL 215 DAY), '%Y-%m-%d'), 0, 'OUT-SEED-0001', 1, '薯片运输压碎报损', DATE_SUB(NOW(), INTERVAL 3 DAY));

-- ============ 收银台挂单（未成单草稿：不生成订单、不扣库存、不占班次） ============
-- items_json 为 proto PosHeldOrderItem 的 Go json tag（snake_case），恢复时由 service 反序列化
INSERT INTO `pos_held_orders` (`id`, `community_id`, `operator_id`, `operator_name`, `user_id`, `user_name`, `items_json`, `item_count`, `total_amount`, `remark`, `created_at`)
VALUES
  (1, 1, 1, '系统管理员', 1, '张三',
   '[{"sku_id":1,"quantity":1,"product_name":"魔小鲜 大面筋辣条","spec_name":"小包106g","product_image":"/uploads/seed/products/latiao.png","price":3.50},{"sku_id":3,"quantity":2,"product_name":"碧源 饮用天然水","spec_name":"550ml","product_image":"/uploads/seed/products/water.png","price":2.00}]',
   2, 7.50, '顾客临时取物，挂单待结', DATE_SUB(NOW(), INTERVAL 10 MINUTE));

-- ============ 站点设置（平台级单行配置，id 固定 1，见 model.SiteConfigID） ============
-- base_url 留空：空值时网关按请求现推派生回调与外链，避免种子写死部署域名；
-- footer_html 仅白名单标签（a/b/i/u/p/br），与管理端富文本消毒口径一致；
-- logo/favicon 留空：为空时前端使用内置默认图
INSERT INTO `site_configs` (`id`, `base_url`, `site_name`, `logo_url`, `favicon_url`, `footer_html`, `seo_description`, `seo_keywords`, `version`, `created_at`, `updated_at`)
VALUES
  (1, '', '社区集市', '', '', '<p>© 2026 社区集市 · 演示环境</p>', '社区便利店，线上下单到店自提', '社区团购,便利店,零食饮料,日用百货', 0, NOW(), NOW());

SET FOREIGN_KEY_CHECKS = 1;
