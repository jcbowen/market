-- 2026-10-15 Banner 展示位修复
-- 背景：C 端旧首页已下线（分类页升级为首页），现首页仅取 position=2；
--       存量 banner 全部为 position=1，导致后台配置的 banner 永不展示。
-- 处理：1) position=1（旧首页坑位）统一迁移到 2；
--       2) 历史上超管无社区语境创建的 community_id=0 banner 顺势成为"全平台通用"
--          （C 端查询已支持 community_id=0 全局兜底）。
-- 幂等：可重复执行，二次执行无副作用。

UPDATE `banners` SET `position` = 2 WHERE `position` = 1;
