package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/services/payment/model"
)

// 流水字段长度上限：error_msg 列为 varchar(500)，raw_body 为 text 但需防超大通知撑爆表
const (
	notifyErrorMsgMaxLen = 500
	notifyRemarkMaxLen   = 255
	notifyRawBodyMaxLen  = 8192
)

// wechatNotifyHeaderPrefix 微信通知仅保留这一组请求头：
// 其余头（含网关转发进来的内部头、cookie）与本事实无关，白名单比黑名单更不容易误存凭据
const wechatNotifyHeaderPrefix = "Wechatpay-"

// errNotSuccessNotification 渠道通知本身合法、但描述的不是「支付成功」
// （微信的退款通知、支付宝的 WAIT_BUYER_PAY/TRADE_CLOSED 状态推送等）
// 这类是正常分流，与验签失败必须区分开：前者记幂等忽略，后者才进异常待办队列
var errNotSuccessNotification = errors.New("非支付成功通知")

// recordNotifyLog 落一条支付通知/对账流水
// 流水是可观测性载体而非业务状态，写入失败绝不可影响支付确认主流程，故只记 stderr
// handled 由 kind/result 推导（见 model.NeedsHandling），人工销账只能把 0 改成 1
//
// 参数：
//   - ctx: 上下文
//   - entry: 流水，Kind 必填；CreatedAt/UpdatedAt 为空时此处填充
//
// 使用示例：
//   - s.recordNotifyLog(ctx, &model.NotifyLog{Kind: model.NotifyKindCallback, Result: model.NotifyResultVerifyFailed, RawBody: string(body)})
func (s *PaymentService) recordNotifyLog(ctx context.Context, entry *model.NotifyLog) {
	if entry == nil || entry.Kind == 0 {
		return
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	if entry.CreatedAt == "" {
		entry.CreatedAt = now
	}
	entry.UpdatedAt = now
	entry.ErrorMsg = truncateRunes(entry.ErrorMsg, notifyErrorMsgMaxLen)
	entry.Remark = truncateRunes(entry.Remark, notifyRemarkMaxLen)
	if model.NeedsHandling(entry.Kind, entry.Result) {
		entry.Handled = model.NotifyPending
	} else {
		entry.Handled = model.NotifyHandled
	}
	if err := s.db.GetDb().WithContext(ctx).Create(entry).Error; err != nil {
		log.Printf("[payment-notify] 流水写入失败 kind=%d result=%d payment_no=%s: %v",
			entry.Kind, entry.Result, entry.PaymentNo, err)
	}
}

// notifyEntryFrom 以支付单为基准起一条流水，填好归属快照（订单 ID/订单号/社区）与渠道、单号
// 社区范围必须随流水落库，漏填会让该条只对超管可见、社区管理员永远看不到自己社区异常，
// 因此统一走本构造函数，不在各写入点手抄字段
//
// 参数：
//   - p: 关联支付单（为 nil 时返回空流水，由调用方自行补齐可解析出的字段）
//   - kind: model.NotifyKind*
//   - result: model.NotifyResult*
//
// 返回值：
//   - *model.NotifyLog: 未落库的流水，调用方补 ErrorMsg/RawBody 等后交给 recordNotifyLog
//
// 使用示例：
//   - entry := notifyEntryFrom(p, model.NotifyKindStuck, model.NotifyResultStuck)
func notifyEntryFrom(p *model.Payment, kind, result int32) *model.NotifyLog {
	entry := &model.NotifyLog{Kind: kind, Result: result}
	if p == nil {
		return entry
	}
	entry.Channel = p.PayType
	entry.PaymentNo = p.PaymentNo
	entry.OrderID = p.OrderID
	entry.OrderNo = p.OrderNo
	entry.CommunityID = p.CommunityID
	return entry
}

// scopeByPaymentNo 按支付单号反查流水归属快照
// 用于「报文能解析出支付单号、但验签或解密失败」的场景：这类异常最需要本社区管理员看见，
// 而归属信息只能来自支付单。单号来自不可信报文，只用于只读定位，查不到即返回零值流水
// （community_id=0，该条只出现在超管的全量队列）
//
// 参数：
//   - ctx: 上下文
//   - paymentNo: 从报文里解析出的支付单号，可为空
//
// 返回值：
//   - *model.Payment: 支付单，空号或查不到时返回 nil
func (s *PaymentService) scopeByPaymentNo(ctx context.Context, paymentNo string) *model.Payment {
	if paymentNo == "" {
		return nil
	}
	var p model.Payment
	if err := s.db.GetDb().WithContext(ctx).Where("payment_no = ?", paymentNo).First(&p).Error; err != nil {
		return nil
	}
	return &p
}

// maskWechatNotifyHeaders 提取微信通知的 Wechatpay-* 请求头并掩码签名与证书序列号
// 参数：
//   - headers: 回调请求头
//
// 返回值：
//   - string: JSON 字符串，无相关头时返回空串
func maskWechatNotifyHeaders(headers http.Header) string {
	masked := make(map[string]string)
	for key, values := range headers {
		if !strings.HasPrefix(key, wechatNotifyHeaderPrefix) || len(values) == 0 {
			continue
		}
		name := strings.ToLower(strings.TrimPrefix(key, wechatNotifyHeaderPrefix))
		value := values[0]
		if strings.Contains(name, "signature") || strings.Contains(name, "serial") {
			value = maskSecret(value)
		}
		masked[name] = value
	}
	return marshalOrEmpty(masked)
}

// maskAlipayNotifyParams 序列化支付宝通知参数并掩码签名
// 参数：
//   - params: 回调 form 参数
//
// 返回值：
//   - string: JSON 字符串
func maskAlipayNotifyParams(params map[string]string) string {
	masked := make(map[string]string, len(params))
	for key, value := range params {
		if strings.EqualFold(key, "sign") {
			value = maskSecret(value)
		}
		masked[key] = value
	}
	return marshalOrEmpty(masked)
}

// maskSecret 保留前 6 位便于比对「是否同一个值」，其余替换为定长掩码
// 参数：
//   - value: 敏感值
//
// 返回值：
//   - string: 掩码后的值
func maskSecret(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 6 {
		return "******"
	}
	return value[:6] + "******"
}

// marshalOrEmpty 序列化映射，失败返回空串（流水辅助字段，不值得为它中断主流程）
// 参数：
//   - v: 待序列化值
//
// 返回值：
//   - string: JSON 或空串
func marshalOrEmpty(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return truncateRunes(string(data), notifyRawBodyMaxLen)
}

// truncateRunes 按字符数截断（中文报文按字节截断会产生半个字）
// 参数：
//   - value: 原始字符串
//   - max: 最大字符数
//
// 返回值：
//   - string: 截断结果，超限部分以 … 标记
func truncateRunes(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max-1]) + "…"
}

// ListOrderPayments 按订单列出全部支付尝试（含失败/关闭，供后台订单详情展示）
// 与 GetPayment 的差异：GetPayment 只回「口径上唯一成功的那笔」，此处保留完整轨迹，
// 重复支付、金额不符等待排查线索只有在完整轨迹下才看得见
//
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - []*model.Payment: 按 ID 升序
//   - error: 查询错误
func (s *PaymentService) ListOrderPayments(ctx context.Context, orderID int64) ([]*model.Payment, error) {
	if orderID <= 0 {
		return nil, fmt.Errorf("参数错误")
	}
	var list []*model.Payment
	if err := s.db.GetDb().WithContext(ctx).
		Where("order_id = ?", orderID).
		Order("id asc").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListPaymentAnomalies 分页查询支付通知/对账流水
// 参数：
//   - ctx: 上下文
//   - req: 查询条件（handled 0全部 1仅待处理 2仅已处理；include_ok=false 时过滤掉确认成功行；
//     with_raw 决定是否回传原始报文与请求头；community_id>0 时只回该社区流水）
//
// 返回值：
//   - []*payment.PaymentAnomalyInfo: 当前页
//   - int32: 符合条件的总数
//   - int32: 待处理条数（异常页红标，不随筛选变化，但受社区范围约束）
//   - error: 查询错误
func (s *PaymentService) ListPaymentAnomalies(ctx context.Context, req *payment.ListPaymentAnomaliesReq) ([]*payment.PaymentAnomalyInfo, int32, int32, error) {
	page := req.Page
	if page <= 0 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	base := func() *gorm.DB {
		q := s.db.GetDb().WithContext(ctx).Model(&model.NotifyLog{})
		// 社区隔离由网关下发范围：普通管理员只回本社区流水，
		// community_id=0 的流水（连支付单都解析不出的验签失败）自然只出现在超管的全量队列里
		if req.CommunityId > 0 {
			q = q.Where("community_id = ?", req.CommunityId)
		}
		if req.Handled == 1 {
			q = q.Where("handled = ?", model.NotifyPending)
		} else if req.Handled == 2 {
			q = q.Where("handled = ?", model.NotifyHandled)
		}
		if req.OrderId > 0 {
			q = q.Where("order_id = ?", req.OrderId)
		}
		if !req.IncludeOk {
			q = q.Where("result <> ?", model.NotifyResultSuccess)
		}
		return q
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, 0, err
	}

	var rows []*model.NotifyLog
	if err := base().
		Order("id desc").
		Offset(int((page - 1) * pageSize)).
		Limit(int(pageSize)).
		Find(&rows).Error; err != nil {
		return nil, 0, 0, err
	}

	// 红标口径：当前社区范围内的待处理条数，不受 handled/include_ok 筛选影响
	pendingQuery := s.db.GetDb().WithContext(ctx).Model(&model.NotifyLog{}).
		Where("handled = ?", model.NotifyPending)
	if req.CommunityId > 0 {
		pendingQuery = pendingQuery.Where("community_id = ?", req.CommunityId)
	}
	var pending int64
	if err := pendingQuery.Count(&pending).Error; err != nil {
		return nil, 0, 0, err
	}

	enrich := s.loadPaymentFacts(ctx, rows)
	list := make([]*payment.PaymentAnomalyInfo, 0, len(rows))
	for _, row := range rows {
		info := &payment.PaymentAnomalyInfo{
			Id:          row.ID,
			Kind:        row.Kind,
			Result:      row.Result,
			Channel:     row.Channel,
			PaymentNo:   row.PaymentNo,
			OrderId:     row.OrderID,
			OrderNo:     row.OrderNo,
			CommunityId: row.CommunityID,
			ErrorMsg:    row.ErrorMsg,
			Handled:     row.Handled,
			CreatedAt:   row.CreatedAt,
			PayType:     enrich[row.PaymentNo].payType,
			Amount:      enrich[row.PaymentNo].amount,
		}
		if req.WithRaw {
			info.RawBody = row.RawBody
			info.Headers = row.Headers
		}
		list = append(list, info)
	}
	return list, int32(total), int32(pending), nil
}

// paymentFacts 流水展示所需的支付单摘要（一次批量查询取回，避免逐行 join）
type paymentFacts struct {
	payType int32
	amount  float64
}

// loadPaymentFacts 按支付单号批量取支付方式与金额
// 参数：
//   - ctx: 上下文
//   - rows: 流水行
//
// 返回值：
//   - map[string]paymentFacts: 以支付单号为键，查不到的行为零值
func (s *PaymentService) loadPaymentFacts(ctx context.Context, rows []*model.NotifyLog) map[string]paymentFacts {
	result := make(map[string]paymentFacts, len(rows))
	numbers := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.PaymentNo == "" {
			continue
		}
		if _, ok := seen[row.PaymentNo]; ok {
			continue
		}
		seen[row.PaymentNo] = struct{}{}
		numbers = append(numbers, row.PaymentNo)
	}
	if len(numbers) == 0 {
		return result
	}
	var payments []model.Payment
	if err := s.db.GetDb().WithContext(ctx).
		Select("payment_no", "pay_type", "amount").
		Where("payment_no IN ?", numbers).
		Find(&payments).Error; err != nil {
		log.Printf("[payment-notify] 补全支付单摘要失败: %v", err)
		return result
	}
	for _, p := range payments {
		result[p.PaymentNo] = paymentFacts{payType: p.PayType, amount: p.Amount}
	}
	return result
}

// MarkPaymentAnomalyHandled 异常流水销账
// 只允许「待处理 → 已处理」，条件更新保证并发重复点击不会互相覆盖备注
// 参数：
//   - ctx: 上下文
//   - id: 流水主键
//   - remark: 人工核实依据（必填，留痕「谁凭什么判定这笔可以关掉」）
//   - communityID: 社区范围（网关按管理员权限下发），>0 时禁止销其他社区的流水，0 表示不限（超管）
//
// 返回值：
//   - error: 记录不存在、越社区或已销账时返回错误
func (s *PaymentService) MarkPaymentAnomalyHandled(ctx context.Context, id int64, remark string, communityID int64) error {
	if id <= 0 || strings.TrimSpace(remark) == "" {
		return bizerr.New(codes.InvalidArgument, "请填写销账备注")
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	query := s.db.GetDb().WithContext(ctx).Model(&model.NotifyLog{}).
		Where("id = ? AND handled = ?", id, model.NotifyPending)
	if communityID > 0 {
		query = query.Where("community_id = ?", communityID)
	}
	res := query.Updates(map[string]interface{}{
		"handled":    int32(model.NotifyHandled),
		"remark":     truncateRunes(remark, notifyRemarkMaxLen),
		"updated_at": now,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// 队列是异步刷新的：别人已销账或单号过期都会走到这里，
		// 归为 NotFound 让网关回「记录不存在或已销账」，运营据此刷新页面而不是以为系统故障
		return bizerr.New(codes.NotFound, "记录不存在或已销账")
	}
	return nil
}
