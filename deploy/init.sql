-- 项目基础测试数据初始化脚本
-- 用于 docker compose 首次启动后填充示例数据，确保核心链路可跑通

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- 社区
INSERT INTO `communities` (`id`, `name`, `address`, `longitude`, `latitude`, `contact_phone`, `pickup_address`, `delivery_fee`, `min_order_amount`, `delivery_range_km`, `business_hours`, `sort`, `status`, `created_at`, `updated_at`)
VALUES (1, '幸福小区', '北京市朝阳区幸福路 1 号', 116.3974, 39.9093, '010-12345678', '小区东门自提点', 3.00, 15.00, 5.00, '08:00-22:00', 1, 1, NOW(), NOW());

-- 角色
INSERT INTO `admin_roles` (`id`, `name`, `code`, `permissions`, `description`, `created_at`, `updated_at`)
VALUES (1, '超级管理员', 'super_admin', '["*"]', '拥有全部权限', NOW(), NOW());

-- 管理员：admin / admin123
INSERT INTO `admin_users` (`id`, `username`, `password`, `real_name`, `phone`, `role_id`, `community_id`, `status`, `last_login_at`, `last_login_ip`, `created_at`, `updated_at`)
VALUES (1, 'admin', '$2a$10$Ewx/FgR4tNyr1fUK5uo0aecXSVFh4JBvY3Xm7MaHSa77S38p9QtqC', '系统管理员', '13800138001', 1, 1, 1, NULL, '', NOW(), NOW());

-- C 端用户：13800138000，登录验证码固定 123456
INSERT INTO `users` (`id`, `openid`, `alipay_user_id`, `union_id`, `phone`, `nickname`, `avatar`, `gender`, `community_id`, `status`, `last_login_at`, `created_at`, `updated_at`)
VALUES (1, 'test_openid_1', NULL, NULL, '13800138000', '测试用户', 'https://placehold.co/100x100/orange/white?text=User', 0, 1, 1, NOW(), NOW(), NOW());

-- 默认收货地址
INSERT INTO `user_addresses` (`id`, `user_id`, `contact_name`, `contact_phone`, `province`, `city`, `district`, `detail`, `tag`, `is_default`, `created_at`, `updated_at`)
VALUES (1, 1, '张三', '13800138000', '北京市', '北京市', '朝阳区', '幸福路 1 号 2 单元 301', '家', 1, NOW(), NOW());

-- 商品分类
INSERT INTO `categories` (`id`, `community_id`, `parent_id`, `name`, `icon`, `sort`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, 0, '新鲜蔬菜', 'https://placehold.co/80x80/green/white?text=Veg', 1, 1, NOW(), NOW()),
  (2, 1, 0, '新鲜水果', 'https://placehold.co/80x80/orange/white?text=Fruit', 2, 1, NOW(), NOW());

-- 商品
INSERT INTO `products` (`id`, `community_id`, `category_id`, `name`, `description`, `cover_image`, `images`, `unit`, `is_multi_spec`, `status`, `sort`, `sales_count`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, '西红柿', '现摘自然熟西红柿，酸甜多汁', 'https://placehold.co/300x300/red/white?text=Tomato', '["https://placehold.co/300x300/red/white?text=Tomato"]', '500g', 0, 1, 1, 0, NOW(), NOW()),
  (2, 1, 2, '红富士苹果', '山东烟台红富士，脆甜可口', 'https://placehold.co/300x300/red/white?text=Apple', '["https://placehold.co/300x300/red/white?text=Apple"]', '个', 0, 1, 2, 0, NOW(), NOW()),
  (3, 1, 2, '海南香蕉', '软糯香甜，老少皆宜', 'https://placehold.co/300x300/yellow/black?text=Banana', '["https://placehold.co/300x300/yellow/black?text=Banana"]', '把', 0, 1, 3, 0, NOW(), NOW());

-- SKU
INSERT INTO `product_skus` (`id`, `product_id`, `community_id`, `sku_code`, `spec_name`, `spec_json`, `price`, `cost_price`, `stock`, `safety_stock`, `image`, `status`, `created_at`, `updated_at`)
VALUES
  (1, 1, 1, 'TOMATO-500G', '500g/份', '{"重量":"500g"}', 3.50, 2.00, 100, 10, 'https://placehold.co/300x300/red/white?text=Tomato', 1, NOW(), NOW()),
  (2, 2, 1, 'APPLE-1P', '单个约200g', '{"规格":"单个约200g"}', 2.00, 1.20, 200, 20, 'https://placehold.co/300x300/red/white?text=Apple', 1, NOW(), NOW()),
  (3, 3, 1, 'BANANA-1B', '1把约500g', '{"规格":"1把约500g"}', 5.50, 3.00, 150, 15, 'https://placehold.co/300x300/yellow/black?text=Banana', 1, NOW(), NOW());

SET FOREIGN_KEY_CHECKS = 1;
