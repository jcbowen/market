-- 2026-10-15 收银台现金细分迁移
-- 背景：收银台现金收款此前复用 pay_type=3（货到付款），与「货到付款未发货可取消」逻辑叠加，
--       导致门店已收款订单可被 C 端按未收款口径直接取消（无退款流水，资损）。
--       收银台支付方式细分为 pay_type=4（现金）后，需将存量收银台支付单同步迁移。
-- 幂等性：重复执行时 pay_type=3 的存量行已迁移为 4，不再命中条件，天然幂等。

UPDATE payments p
JOIN orders o ON o.id = p.order_id
SET p.pay_type = 4,
    p.updated_at = NOW()
WHERE o.source = 2
  AND p.pay_type = 3;
