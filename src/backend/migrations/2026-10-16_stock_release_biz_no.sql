-- 2026-10-16 退款/释放库存幂等键改造：stock_op_logs 幂等键由 (order_id, sku_id, op_type)
-- 扩展为 (order_id, sku_id, op_type, biz_no)。
--
-- 背景：旧幂等键下，同一订单同一 SKU 的第二笔部分退款「释放库存」会被唯一索引吞掉
-- （RowsAffected=0 静默跳过），导致库存少回补。新键按业务号（退款单号/cancel:{orderID}/
-- comp:{orderID}:{skuID}）隔离，多笔部分退款各自独立幂等。
--
-- 说明：GORM AutoMigrate 只会新增 uk_stock_op_order_sku_biz 索引，
-- 旧的 uk_stock_op_order_sku 索引不会自动删除，会继续拦截重复释放，必须手动执行本脚本。
-- 存量数据 biz_no 为空串，与新键 (order_id, sku_id, op_type, '') 语义一致，无需回填。

ALTER TABLE stock_op_logs DROP INDEX uk_stock_op_order_sku;
