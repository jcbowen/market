package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/payment/model"
	wegopayment "github.com/jcbowen/wego/payment"

	alipaysdk "github.com/jcbowen/alipay-sdk-go-all"
)

// PaymentService 支付业务服务
type PaymentService struct {
	db  *mysql.Instance
	cfg *config.Config

	// payMu 保护以下支付配置缓存字段的并发读写
	payMu sync.RWMutex
	// payCfg 数据库支付配置的内存副本（pay_config 单行），updated_at 兼作版本号
	payCfg *model.PayConfig

	wechatPayClient *wegopayment.Client

	// alipayExecutor 支付宝通用执行器（V2 pageExecute + V3 API 共享），
	// 随支付配置热更新整体重建；nil 表示未配置或配置不完整
	alipayExecutor *alipaysdk.GenericExecuteApi
}

// NewPaymentService 创建支付业务服务
// 支付配置从数据库 pay_config 表加载（配置变更后各实例按版本号自动热更新，无需重启）
// 参数：
//   - db: MySQL 实例
//   - cfg: 全局配置
//
// 返回值：
//   - *PaymentService: 支付业务服务实例
func NewPaymentService(db *mysql.Instance, cfg *config.Config) *PaymentService {
	s := &PaymentService{db: db, cfg: cfg}
	if err := s.EnsureDefaultPayConfig(); err != nil {
		log.Printf("初始化支付配置失败: %v", err)
	}
	s.reloadPayConfig()
	return s
}

// reloadPayConfig 启动时从数据库加载支付配置并构建支付客户端
func (s *PaymentService) reloadPayConfig() {
	var row model.PayConfig
	if err := s.db.GetDb().First(&row, payConfigID).Error; err != nil {
		log.Printf("加载数据库支付配置失败（支付功能将不可用，可在后台配置后自动生效）: %v", err)
		return
	}
	s.payMu.Lock()
	defer s.payMu.Unlock()
	s.applyPayConfig(&row)
}

// CreatePayment 创建支付单并生成第三方支付调起参数
// 现金(4)/货到付款(3)无需第三方调起参数，创建成功后直接同步订单状态为已支付；
// 收银台全额优惠的 0 元单（pay_type=4 且 amount=0）同样落支付单，保持「订单必有支付记录」的统一口径；
// 付款码支付(5/6)只创建待支付单（无调起参数、不动订单状态），扣款由收银员扫顾客付款码后
// 通过 PayByAuthCode 完成（两步化，见 codepay.go），渠道不支持 0 元扣款
// 幂等策略：订单已有支付成功记录时拒绝创建；已有同方式同金额的待支付单时直接复用该记录
// 参数：
//   - ctx: 上下文
//   - req: 创建支付请求
//
// 返回值：
//   - *model.Payment: 支付记录模型
//   - string: 微信/支付宝调起参数 JSON，现金/货到付款/付款码支付返回空字符串
//   - error: 错误信息
func (s *PaymentService) CreatePayment(ctx context.Context, req *payment.CreatePaymentReq) (*model.Payment, string, error) {
	if req.OrderId <= 0 || req.Amount < 0 {
		return nil, "", fmt.Errorf("参数错误")
	}
	switch req.PayType {
	case 1, 2, 3, 4, 5, 6:
	default:
		return nil, "", fmt.Errorf("不支持的支付方式")
	}
	// 付款码支付依赖渠道真实扣款，渠道不受理 0 元交易；全额优惠单收银台应改用现金方式
	if (req.PayType == 5 || req.PayType == 6) && req.Amount <= 0 {
		return nil, "", fmt.Errorf("付款码支付金额必须大于 0")
	}
	// user_id 允许为 0 的场景：收银台散客单现金/付款码收款（线下当面收款，无会员账户）
	if req.UserId <= 0 && req.PayType != 3 && req.PayType != 4 && req.PayType != 5 && req.PayType != 6 {
		return nil, "", fmt.Errorf("参数错误")
	}

	// 幂等校验：订单已存在支付成功的记录时拒绝再次创建支付单；
	// 查询异常（非"记录不存在"）时同样拒绝，避免 DB 故障让幂等保护失效而产生重复支付
	if _, err := s.GetPayment(ctx, req.OrderId); err == nil {
		return nil, "", bizerr.New(codes.AlreadyExists, "订单已支付，请勿重复支付")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("查询订单支付状态失败 order_id=%d: %v", req.OrderId, err)
		return nil, "", bizerr.New(codes.Internal, "支付状态查询失败，请稍后重试")
	}

	// 复用待支付单，避免重复向第三方下单
	pending, err := s.findReusablePendingPayment(ctx, req)
	if err != nil {
		return nil, "", err
	}
	if pending != nil {
		return pending, pending.PayParams, nil
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	paymentNo := generatePaymentNo()

	p := &model.Payment{
		PaymentNo:   paymentNo,
		OrderID:     req.OrderId,
		OrderNo:     req.OrderNo,
		UserID:      req.UserId,
		CommunityID: req.CommunityId,
		PayType:     req.PayType,
		Amount:      req.Amount,
		Status:      1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	var payParams string
	switch req.PayType {
	case 1:
		payParams, err = s.buildWechatPayParams(ctx, p, req.Openid, req.AppId)
	case 2:
		payParams, err = s.buildAlipayParams(ctx, p)
	case 3, 4:
		// 货到付款(3)/收银台现金(4)直接标记为支付成功，并同步订单状态（4 含 0 元全额优惠单）
		// 确认来源标为线下：这类支付单没有渠道回调，统计「回调丢失率」时必须排除
		p.Status = 2
		p.PayTime = &now
		p.ConfirmSource = model.ConfirmSourceOffline
	case 5, 6:
		// 付款码支付：只建待支付单（无调起参数、不动订单状态），
		// 创建前先校验渠道就绪，把「渠道未配置」拦在下单而不是扫码扣款时
		if err := s.ensureCodePayChannelReady(ctx, req.PayType); err != nil {
			return nil, "", err
		}
	}
	if err != nil {
		return nil, "", err
	}
	p.PayParams = payParams

	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(p).Error; err != nil {
			return err
		}
		if req.PayType == 3 || req.PayType == 4 {
			remark := "货到付款，支付成功"
			if req.PayType == 4 {
				remark = "门店现金收款"
			}
			_, err = grpcclient.OrderClient.UpdateOrderStatus(ctx, &order.UpdateOrderStatusReq{
				Id:           req.OrderId,
				Status:       2,
				OperatorType: "system",
				Remark:       remark,
			})
			if err != nil {
				return fmt.Errorf("同步订单支付状态失败: %w", err)
			}
		}
		return nil
	}); err != nil {
		return nil, "", err
	}
	return p, payParams, nil
}

// findReusablePendingPayment 查找可复用的待支付支付单
// 仅需第三方渠道处理的支付方式（微信/支付宝在线支付 1/2、付款码 5/6）才复用：
// 在线支付避免重复向第三方下单产生多笔可实际扣款的支付单；付款码复用保证扣款失败重试时
// out_trade_no 不变（同一笔渠道交易反复提交，避免同一订单挂多笔待扣款交易）；
// 现金支付创建时即置为已支付并同步订单状态，不复用异常遗留的待支付现金单，否则会跳过该副作用导致订单停留在待支付
// 参数：
//   - ctx: 上下文
//   - req: 创建支付请求
//
// 返回值：
//   - *model.Payment: 可复用的待支付单，渠道不支持复用或不存在时返回 nil
//   - error: 查询异常时返回内部错误（不含"记录不存在"），避免故障被当作无待支付单而重复下单
func (s *PaymentService) findReusablePendingPayment(ctx context.Context, req *payment.CreatePaymentReq) (*model.Payment, error) {
	switch req.PayType {
	case 1, 2, 5, 6:
	default:
		return nil, nil
	}

	var pending model.Payment
	err := s.db.GetDb().WithContext(ctx).
		Where("order_id = ? AND pay_type = ? AND amount = ? AND status = ?", req.OrderId, req.PayType, req.Amount, 1).
		Order("id desc").First(&pending).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		log.Printf("查询可复用待支付单失败 order_id=%d: %v", req.OrderId, err)
		return nil, bizerr.New(codes.Internal, "支付单查询失败，请稍后重试")
	}
	return &pending, nil
}

// GetPayment 根据订单 ID 查询最新成功支付记录
// 多次支付场景下返回该订单最近一次支付成功的记录
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - *model.Payment: 支付记录模型
//   - error: 错误信息
func (s *PaymentService) GetPayment(ctx context.Context, orderID int64) (*model.Payment, error) {
	var p model.Payment
	if err := s.db.GetDb().WithContext(ctx).Where("order_id = ? AND status = ?", orderID, 2).Order("id desc").First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPaymentStatusByNo 按支付单号查询支付状态（免登录结果页轮询用）
// 调用方约束：入参 payment_no 是 ZF+UUID 形态的不可枚举凭据，
// 网关须把本方法暴露在公开路由组并只回传最小字段集，不得回传用户侧隐私字段
// 参数：
//   - ctx: 上下文
//   - paymentNo: 支付单号
//
// 返回值：
//   - *model.Payment: 支付记录模型（待支付/已支付/已关闭均可见，供结果页区分展示）
//   - error: 参数为空或记录不存在
func (s *PaymentService) GetPaymentStatusByNo(ctx context.Context, paymentNo string) (*model.Payment, error) {
	paymentNo = strings.TrimSpace(paymentNo)
	if paymentNo == "" {
		return nil, bizerr.New(codes.InvalidArgument, "支付单号不能为空")
	}
	var p model.Payment
	if err := s.db.GetDb().WithContext(ctx).Where("payment_no = ?", paymentNo).First(&p).Error; err != nil {
		return nil, bizerr.New(codes.NotFound, "支付单不存在")
	}
	return &p, nil
}

// GetAlipayPayUrl 凭支付单号重新生成支付宝手机网站支付收银台链接（H5 支付引导页免登录换取用）
// 引导页 URL 只携带不可枚举的支付单号，在系统浏览器打开后凭单号实时换取新签名的收银台链接，
// 规避支付链接（含 # 等保留字符）经 uni-app H5 路由 query 传递时被框架编解码破坏的问题；
// 实时重新签名也顺带解决引导页打开时旧链接 timestamp 已过期的问题
// 调用方约束：与 GetPaymentStatusByNo 相同，payment_no 是不可枚举凭据，网关暴露在公开路由组；
// 仅对待支付的支付宝单返回链接，其它状态返回 nil 链接，引导页据此跳结果页展示状态
// 参数：
//   - ctx: 上下文
//   - paymentNo: 支付单号
//
// 返回值：
//   - *model.Payment: 支付记录模型（任何状态均可见，供引导页分流）
//   - string: 支付宝收银台链接，仅待支付且 pay_type=2 时有值
//   - error: 参数为空或记录不存在或重新签名失败
func (s *PaymentService) GetAlipayPayUrl(ctx context.Context, paymentNo string) (*model.Payment, string, error) {
	p, err := s.GetPaymentStatusByNo(ctx, paymentNo)
	if err != nil {
		return nil, "", err
	}
	// 仅待支付的支付宝单可换取新链接；现金/付款码单或已终态单不给链接
	if p.Status != 1 || p.PayType != 2 {
		return p, "", nil
	}
	params, err := s.buildAlipayParams(ctx, p)
	if err != nil {
		return nil, "", err
	}
	var parsed struct {
		AlipayPayURL string `json:"alipay_pay_url"`
	}
	if err := json.Unmarshal([]byte(params), &parsed); err != nil {
		return nil, "", bizerr.New(codes.Internal, "解析支付宝调起参数失败")
	}
	return p, parsed.AlipayPayURL, nil
}

// BatchGetPaymentsMaxOrderIDs 单次批量查询的订单 ID 上限（后台列表每页最多 50，留足余量并封顶超大 IN）
const BatchGetPaymentsMaxOrderIDs = 200

// BatchGetPayments 按订单 ID 集合批量查询成功支付记录
// 口径与 GetPayment 一致：一个订单可能有多条支付记录，只回传每个订单最近一次支付成功（status=2）的那条。
// 后台订单列表原先逐行 GetPayment，一页 N 行即 N 次往返；本方法一次往返取回整页。
// 未支付/无成功记录的订单不出现在结果里，调用方按「查不到即未支付」处理。
//
// 参数：
//   - ctx: 上下文
//   - orderIDs: 订单 ID 集合（可重复、可含 0，自动忽略）
//
// 返回值：
//   - []*model.Payment: 每个订单至多一条支付记录
//   - error: 参数超限或数据库错误
//
// 使用示例：
//
//	payments, err := paymentService.BatchGetPayments(ctx, []int64{11, 12})
func (s *PaymentService) BatchGetPayments(ctx context.Context, orderIDs []int64) ([]*model.Payment, error) {
	clean := make([]int64, 0, len(orderIDs))
	for _, id := range orderIDs {
		if id > 0 {
			clean = append(clean, id)
		}
	}
	if len(clean) == 0 {
		return nil, nil
	}
	if len(clean) > BatchGetPaymentsMaxOrderIDs {
		return nil, bizerr.New(codes.InvalidArgument, fmt.Sprintf("单次批量查询支付记录数不能超过 %d", BatchGetPaymentsMaxOrderIDs))
	}

	var rows []*model.Payment
	// 按 id 倒序遍历、每个订单只留首条即「最近一次成功支付」，避免相关子查询
	if err := s.db.GetDb().WithContext(ctx).
		Where("order_id IN ? AND status = ?", clean, 2).
		Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	seen := make(map[int64]struct{}, len(clean))
	list := make([]*model.Payment, 0, len(clean))
	for _, p := range rows {
		if _, ok := seen[p.OrderID]; ok {
			continue
		}
		seen[p.OrderID] = struct{}{}
		list = append(list, p)
	}
	return list, nil
}

// HandleWechatCallback 处理微信支付回调
// 解析回调体，更新支付单状态，并同步订单状态为已支付
// 解析（验签/解密）失败同样落一条流水：否则事后无法区分「渠道发过但被拒」与「渠道根本没发」
// 参数：
//   - ctx: 上下文
//   - req: 微信支付回调请求
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) HandleWechatCallback(ctx context.Context, req *payment.WechatCallbackReq) error {
	// 回调前先检测配置版本：验签与解密依赖渠道客户端，未承接过下单的实例若沿用启动时的空配置，
	// 会把合法通知判为验签失败
	s.getPayConfigCached(ctx)

	headers := make(http.Header)
	for k, v := range req.Headers {
		headers.Set(k, v)
	}
	params, err := parseWechatCallback(s, req.Body, headers)
	if err != nil {
		// 微信通知全文加密，验签/解密失败时连支付单号都解析不出，归属只能留空（community_id=0），
		// 这类流水因此只出现在超管的全量队列
		entry := notifyEntryFrom(nil, model.NotifyKindCallback, wechatCallbackFailureResult(err))
		entry.Channel = 1
		entry.ErrorMsg = err.Error()
		entry.RawBody = truncateRunes(string(req.Body), notifyRawBodyMaxLen)
		entry.Headers = maskWechatNotifyHeaders(headers)
		s.recordNotifyLog(ctx, entry)
		return err
	}
	params.NotifyKind = model.NotifyKindCallback
	params.Channel = 1
	params.RawBody = string(req.Body)
	params.Headers = maskWechatNotifyHeaders(headers)
	return s.handlePaySuccess(ctx, params)
}

// wechatCallbackFailureResult 回调解析失败的原因分类
// 非支付成功通知（如退款通知）是正常分流，其余（验签、解密、报文解析）都是需要人工排查的异常
// 参数：
//   - err: parseWechatCallback 返回的错误
//
// 返回值：
//   - int32: model.NotifyResult* 之一
func wechatCallbackFailureResult(err error) int32 {
	if errors.Is(err, errNotSuccessNotification) {
		return model.NotifyResultIgnored
	}
	return model.NotifyResultVerifyFailed
}

// HandleAlipayCallback 处理支付宝支付回调
// RSA2 验签（支付宝公钥）后解析回调参数，更新支付单状态，并同步订单状态为已支付
// 参数：
//   - ctx: 上下文
//   - req: 支付宝回调请求
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) HandleAlipayCallback(ctx context.Context, req *payment.AlipayCallbackReq) error {
	// 验签用的支付宝公钥来自配置缓存，先按版本号刷新，避免未承接过下单的实例因空配置拒绝合法通知
	s.getPayConfigCached(ctx)

	params, err := s.parseAlipayCallback(req.Params)
	if err != nil {
		// 支付宝通知的 out_trade_no 是明文表单字段，验签失败前后都能读到；
		// 据此反查支付单即可把流水归到正确社区（反查只做只读定位，不可信的单号查不到就是空）
		paymentNo := req.Params["out_trade_no"]
		entry := notifyEntryFrom(s.scopeByPaymentNo(ctx, paymentNo), model.NotifyKindCallback, alipayCallbackFailureResult(err))
		entry.Channel = 2
		entry.PaymentNo = paymentNo
		entry.ErrorMsg = err.Error()
		entry.RawBody = truncateRunes(maskAlipayNotifyParams(req.Params), notifyRawBodyMaxLen)
		s.recordNotifyLog(ctx, entry)
		return err
	}
	params.NotifyKind = model.NotifyKindCallback
	params.Channel = 2
	params.RawBody = maskAlipayNotifyParams(req.Params)
	return s.handlePaySuccess(ctx, params)
}

// alipayCallbackFailureResult 支付宝回调解析失败的原因分类
// 参数：
//   - err: parseAlipayCallback 返回的错误
//
// 返回值：
//   - int32: model.NotifyResult* 之一
func alipayCallbackFailureResult(err error) int32 {
	if errors.Is(err, errNotSuccessNotification) {
		return model.NotifyResultIgnored
	}
	return model.NotifyResultVerifyFailed
}

// ProcessRefund 处理退款
// 按 payment_no 找到对应支付单后创建退款记录，支持一笔支付单多次部分退款
// 通过 refund_no 唯一索引保证幂等，重复提交时直接返回成功
// 流程分两阶段：先在事务内（FOR UPDATE 锁支付单行）完成幂等判定、累计上限校验并落「待退款」流水，
// 事务提交后再调用渠道退款，成功后将流水置「退款成功」——渠道调用不占用事务连接，
// 渠道失败时流水置「退款失败」保留审计，重试由调用方以同一 refund_no 重新发起
// 参数：
//   - ctx: 上下文
//   - req: 退款请求
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) ProcessRefund(ctx context.Context, req *payment.ProcessRefundReq) error {
	if req.OrderId <= 0 || req.RefundNo == "" || req.Amount <= 0 {
		return fmt.Errorf("退款参数错误")
	}

	p, err := s.findRefundPayment(ctx, req.OrderId, req.PaymentNo)
	if err != nil {
		return err
	}

	// 现金类支付（货到付款=3 / 收银台现金=4）不经过第三方退款，事务内直接落成功流水
	if p.PayType == 3 || p.PayType == 4 {
		return s.recordCashRefund(ctx, req, p)
	}

	// 渠道退款前检测配置版本：执行器/客户端随配置热更新重建，
	// 未承接过下单的实例若沿用启动时的空配置会把可用渠道判为未配置而退款失败
	s.getPayConfigCached(ctx)

	now := time.Now().Format("2006-01-02 15:04:05")
	refund := &model.RefundPayment{
		RefundNo:  req.RefundNo,
		PaymentNo: p.PaymentNo,
		OrderID:   req.OrderId,
		Amount:    req.Amount,
		Status:    1,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// 第一阶段：锁支付单行，完成幂等判定、累计校验并落「待退款」流水
	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// FOR UPDATE 锁支付单行：串行化同一支付单的并发退款，保证累计校验不被绕过
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(p, "id = ?", p.ID).Error; err != nil {
			return err
		}

		// 幂等判定：同 refund_no 已成功直接返回；上次失败允许清理后重新发起；
		// 处理中(1)视为在途，拒绝重复提交（防渠道调用期间双重出款，需人工核实后在库中清理）
		var exists model.RefundPayment
		err := tx.Where("refund_no = ?", req.RefundNo).First(&exists).Error
		switch {
		case err == nil:
			switch exists.Status {
			case 2:
				return nil
			case 3:
				// 上次渠道失败：清理失败流水后重新发起（审计留痕在调用方 refund_logs）
				if err := tx.Delete(&exists).Error; err != nil {
					return err
				}
			default:
				return fmt.Errorf("退款处理中，请勿重复提交；如长时间未完成请人工核实渠道结果")
			}
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return err
		}

		// 累计上限校验：含待退款(1)与成功(2)流水（保守口径，失败流水不占用额度）
		var refunded float64
		if err := tx.Model(&model.RefundPayment{}).
			Select("COALESCE(SUM(amount), 0)").
			Where("payment_no = ? AND status IN ?", p.PaymentNo, []int32{1, 2}).
			Scan(&refunded).Error; err != nil {
			return err
		}
		// 金额为 float64（decimal 化立项前），累计比较带 1e-6 容差防浮点误差误判
		if refunded+req.Amount > p.Amount+1e-6 {
			return fmt.Errorf("退款金额超过剩余可退金额")
		}

		return tx.Create(refund).Error
	}); err != nil {
		return err
	}

	// 第二阶段：渠道调用移出事务（事务内调外部接口会长时间占用连接，且回滚无法撤回已发出的渠道请求）
	// 渠道映射：1微信 / 5微信付款码 走微信退款，2支付宝 / 6支付宝付款码 走支付宝退款
	// （现金类 3/4 已在前面分流至 recordCashRefund，不会落到这里）
	var refundErr error
	if p.PayType == 1 || p.PayType == 5 {
		refundErr = s.wechatRefund(ctx, p, refund)
	} else {
		refundErr = s.alipayRefund(ctx, p, refund)
	}
	if refundErr != nil {
		// 渠道失败：流水置「退款失败」保留审计，重试时由幂等清理逻辑放行
		failTime := time.Now().Format("2006-01-02 15:04:05")
		if uerr := s.db.GetDb().WithContext(ctx).Model(&model.RefundPayment{}).
			Where("id = ? AND status = 1", refund.ID).
			Updates(map[string]interface{}{"status": int32(3), "updated_at": failTime}).Error; uerr != nil {
			log.Printf("[ProcessRefund] 退款流水置失败状态异常 refund_no=%s: %v", refund.RefundNo, uerr)
		}
		return refundErr
	}

	// 第三阶段：渠道成功，锁定支付单行后确认流水成功并按累计判断支付单是否全额退款
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(p, "id = ?", p.ID).Error; err != nil {
			return err
		}

		refundTime := time.Now().Format("2006-01-02 15:04:05")
		res := tx.Model(&model.RefundPayment{}).Where("id = ? AND status = 1", refund.ID).Updates(map[string]interface{}{
			"status":         int32(2),
			"transaction_id": refund.TransactionID,
			"refund_time":    refundTime,
			"updated_at":     refundTime,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("退款流水状态异常，请人工核查 refund_no=%s", refund.RefundNo)
		}

		// 累计退款金额等于支付金额时才标记支付单为已退款
		var totalRefunded float64
		if err := tx.Model(&model.RefundPayment{}).
			Select("COALESCE(SUM(amount), 0)").
			Where("payment_no = ? AND status = ?", p.PaymentNo, 2).
			Scan(&totalRefunded).Error; err != nil {
			return err
		}
		if totalRefunded >= p.Amount-1e-6 {
			// 条件更新：仅支付成功(2)状态可置已退款
			return tx.Model(&model.Payment{}).Where("id = ? AND status = 2", p.ID).
				Updates(map[string]interface{}{"status": int32(4), "updated_at": refundTime}).Error
		}
		return nil
	})
}

// ClosePayments 关闭订单的全部待支付支付单（status 1→5）
// 订单取消/超时取消后由 order 服务调用：支付单置已关闭后，迟到的支付回调
// 会被 handlePaySuccess 的终态兜底忽略，避免已取消订单被误置为已支付
//
// 调用本方法时订单取消事务已提交（订单已是 6，状态机禁止 6→2），
// 因此这里绝不尝试把渠道核实到的收款「补同步」成已支付——那样只会得到
// 「支付单已成功、订单已取消」的不一致体。正确处置是登记挂账异常，由人工退款。
//
// 对账开关（business.payment_reconcile.enabled）开启时，本地置 5 之前先逐笔问渠道：
//   - 渠道已收款 → 写「挂账检出」异常流水（含渠道交易号与金额），进后台待办队列；
//   - 渠道未支付/在途 → 顺带关闭渠道侧交易，用户在渠道 App 里不会再看到待付订单；
//   - 渠道查询失败 → 不盲调关单（网络失败不等于用户没付），只本地置 5，交由对账调度器复核。
//
// 开关关闭时行为与改造前逐字一致：纯本地把 status=1 批量置 5，零渠道调用。
//
// 参数：
//   - ctx: 上下文
//   - req: 关闭请求（order_id）
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) ClosePayments(ctx context.Context, req *payment.ClosePaymentsReq) error {
	if req.OrderId <= 0 {
		return fmt.Errorf("参数错误")
	}
	var pending []model.Payment
	if err := s.db.GetDb().WithContext(ctx).
		Where("order_id = ? AND status = 1", req.OrderId).
		Find(&pending).Error; err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	if s.cfg.ReconcileEnabled() {
		for i := range pending {
			s.inspectForClose(ctx, &pending[i])
		}
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	res := s.db.GetDb().WithContext(ctx).Model(&model.Payment{}).
		Where("order_id = ? AND status = 1", req.OrderId).
		Updates(map[string]interface{}{"status": int32(5), "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		log.Printf("[ClosePayments] 订单 %d 已关闭 %d 笔待支付支付单", req.OrderId, res.RowsAffected)
	}
	return nil
}

// inspectForClose 关单前与渠道核实一笔待支付单，只做「留证据」与「关渠道交易」两件事
// 参数：
//   - ctx: 上下文
//   - p: 待关闭的支付单
func (s *PaymentService) inspectForClose(ctx context.Context, p *model.Payment) {
	res, err := s.queryChannelOrder(ctx, p)
	if err != nil || res == nil {
		// 查询失败时不猜测资金状态：既不关渠道单也不写异常，
		// 本地照常置 5 后由对账调度器按状态=5 再复核（挂账检出扫描）
		log.Printf("[ClosePayments] 渠道查询失败 payment_no=%s: %v", p.PaymentNo, err)
		return
	}
	if res.State == ChannelStatePaid {
		s.recordStuckPayment(ctx, p, res)
		return
	}
	if res.State == ChannelStateNotExist {
		// 渠道从未创建这笔交易，无需关单
		return
	}
	s.closeChannelTrade(ctx, p, res)
}

// findRefundPayment 查找退款对应的原支付单
// 若指定 payment_no 则直接查询；否则返回该订单最近一次支付成功记录
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//   - paymentNo: 支付单号，可为空
//
// 返回值：
//   - *model.Payment: 支付记录
//   - error: 错误信息
func (s *PaymentService) findRefundPayment(ctx context.Context, orderID int64, paymentNo string) (*model.Payment, error) {
	if paymentNo != "" {
		var p model.Payment
		if err := s.db.GetDb().WithContext(ctx).Where("payment_no = ? AND status = ?", paymentNo, 2).First(&p).Error; err != nil {
			return nil, fmt.Errorf("未找到指定支付单: %w", err)
		}
		return &p, nil
	}
	p, err := s.GetPayment(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("未找到有效支付单: %w", err)
	}
	return p, nil
}

// recordCashRefund 记录货到付款退款
// 现金/货到付款不调用第三方支付，事务内锁支付单行后完成幂等判定与累计上限校验，直接落成功流水
// 参数：
//   - ctx: 上下文
//   - req: 退款请求
//   - p: 原支付记录
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) recordCashRefund(ctx context.Context, req *payment.ProcessRefundReq, p *model.Payment) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	refund := &model.RefundPayment{
		RefundNo:      req.RefundNo,
		PaymentNo:     p.PaymentNo,
		OrderID:       req.OrderId,
		Amount:        req.Amount,
		Status:        2,
		TransactionID: "CASH_REFUND",
		RefundTime:    &now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// FOR UPDATE 锁支付单行：串行化同一支付单的并发现金退款，保证累计校验不被绕过
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(p, "id = ?", p.ID).Error; err != nil {
			return err
		}

		// 幂等：同 refund_no 已存在（现金退款直接成功）时直接返回成功
		var exists model.RefundPayment
		err := tx.Where("refund_no = ?", req.RefundNo).First(&exists).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		// 累计上限校验：含待退款(1)与成功(2)流水，比较带 1e-6 容差防浮点误差
		var refunded float64
		if err := tx.Model(&model.RefundPayment{}).
			Select("COALESCE(SUM(amount), 0)").
			Where("payment_no = ? AND status IN ?", p.PaymentNo, []int32{1, 2}).
			Scan(&refunded).Error; err != nil {
			return err
		}
		if refunded+req.Amount > p.Amount+1e-6 {
			return fmt.Errorf("退款金额超过剩余可退金额")
		}
		if err := tx.Create(refund).Error; err != nil {
			return err
		}

		// 累计退款金额等于支付金额时标记支付单为已退款，与微信/支付宝路径口径一致
		var totalRefunded float64
		if err := tx.Model(&model.RefundPayment{}).
			Select("COALESCE(SUM(amount), 0)").
			Where("payment_no = ? AND status = ?", p.PaymentNo, 2).
			Scan(&totalRefunded).Error; err != nil {
			return err
		}
		if totalRefunded >= p.Amount-1e-6 {
			// 条件更新：仅支付成功(2)状态可置已退款
			return tx.Model(&model.Payment{}).Where("id = ? AND status = 2", p.ID).
				Updates(map[string]interface{}{"status": int32(4), "updated_at": now}).Error
		}
		return nil
	})
}

// handlePaySuccess 统一处理支付成功逻辑
// 四重防护（微信/支付宝回调、对账补偿、后台同步四条路径通用）：
//  1. 金额强制比对：回调金额与应付金额统一换算为分后比对，不一致拒绝确认（防伪造回调少付）
//  2. 条件更新幂等：仅 status=1（待支付）可流转为已支付，重复/并发回调安全
//  3. 终态兜底：非待支付状态的回调直接忽略；已关闭(5) 的迟到成功回调记 CRITICAL + 挂账流水（资金挂账需人工退款）
//  4. 全程留痕：确认成功/幂等忽略/金额不符/挂账都落 payment_notify_logs，异常项进后台待办队列
//
// 两阶段设计：事务内仅更新支付单状态（快、无外部依赖），事务提交后再 RPC 同步订单状态；
// 避免事务持锁期间依赖 order-service（慢/宕机时不会拖垮支付服务连接池）。
// RPC 失败时立即重试 3 次；若仍失败则记 CRITICAL 日志（支付已确认但订单未同步）。
// 兜底保障：支付服务自身的对账调度器每分钟回查待支付单（见 service/scheduler.go），
// 回调丢失与订单同步失败都能被捞回——不再依赖「取消前反查」这一条路径。
//
// 参数：
//   - ctx: 上下文
//   - params: 解析后的回调参数（NotifyKind 非 0 时写流水，ConfirmSource 标明确认来源）
//
// 返回值：
//   - error: 错误信息
func (s *PaymentService) handlePaySuccess(ctx context.Context, params *payCallbackParams) error {
	if params.PaymentNo == "" || params.TransactionID == "" {
		return fmt.Errorf("回调参数缺失")
	}
	// 渠道成功回调必带金额；缺失视为解析异常，拒绝确认
	if params.Amount <= 0 {
		return fmt.Errorf("回调金额缺失或非法，拒绝确认支付")
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	var orderID int64
	var needSync bool
	// 流水结果以「确认成功」起草，各拒绝/忽略分支覆写；订单 ID 以支付单反查到的为准
	notifyResult := int32(model.NotifyResultSuccess)
	notifyMsg := ""
	scope := notifyScope{orderID: params.OrderID}

	// 阶段一：事务内完成支付单状态流转（纯 DB 操作，无外部依赖）
	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var p model.Payment
		if err := tx.Where("payment_no = ?", params.PaymentNo).First(&p).Error; err != nil {
			notifyResult = model.NotifyResultError
			notifyMsg = fmt.Sprintf("支付单不存在或查询失败: %s", params.PaymentNo)
			return err
		}
		scope = notifyScope{orderID: p.OrderID, orderNo: p.OrderNo, communityID: p.CommunityID}
		// 终态兜底：已支付/已关闭等状态的回调直接忽略
		if p.Status != 1 {
			if p.Status == 5 {
				// 已关闭（订单取消/超时后关闭）却收到成功通知：钱已收、订单已取消，
				// 属资金挂账，必须人工在渠道侧核对退款，故升级为 CRITICAL 供告警接入；
				// 已支付等状态的重复通知是正常幂等，仍按普通日志忽略以免淹没告警
				notifyResult = model.NotifyResultStuck
				notifyMsg = fmt.Sprintf("订单已取消而用户完成付款（渠道交易号 %s，金额 %.2f），需人工退款",
					params.TransactionID, params.Amount)
				log.Printf("[CRITICAL] 已关闭支付单收到成功回调（订单已取消而用户仍完成付款，需人工对账退款）: "+
					"payment_no=%s order_id=%d amount=%.2f transaction_id=%s",
					p.PaymentNo, p.OrderID, p.Amount, params.TransactionID)
			} else {
				notifyResult = model.NotifyResultIgnored
				notifyMsg = fmt.Sprintf("支付单状态 %d，非待支付，本次通知按幂等忽略", p.Status)
				log.Printf("支付回调忽略非待支付单: payment_no=%s status=%d", p.PaymentNo, p.Status)
			}
			return nil
		}
		// 金额强制比对（统一换算为分，规避浮点精度误差）
		if yuanToFen(params.Amount) != yuanToFen(p.Amount) {
			notifyResult = model.NotifyResultAmountMismatch
			notifyMsg = fmt.Sprintf("应付 %.2f，实际 %.2f", p.Amount, params.Amount)
			log.Printf("支付回调金额不一致，拒绝确认: payment_no=%s 应付=%.2f 回调=%.2f",
				p.PaymentNo, p.Amount, params.Amount)
			return fmt.Errorf("回调金额与应付金额不一致")
		}
		// 确认来源：回调路径不显式赋值即渠道回调，其余路径由调用方标注
		source := params.ConfirmSource
		if source == 0 {
			source = model.ConfirmSourceCallback
		}
		// 条件更新保证幂等：仅 status=1 时流转成功，并发回调只有一笔生效
		res := tx.Model(&model.Payment{}).
			Where("payment_no = ? AND status = ?", p.PaymentNo, 1).
			Updates(map[string]interface{}{
				"status":         2,
				"transaction_id": params.TransactionID,
				"pay_time":       now,
				"updated_at":     now,
				"confirm_source": source,
				"confirm_ref":    params.ConfirmRef,
			})
		if res.Error != nil {
			notifyResult = model.NotifyResultError
			notifyMsg = res.Error.Error()
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 并发下另一笔回调已完成流转，幂等返回
			notifyResult = model.NotifyResultIgnored
			notifyMsg = "并发确认已先行完成流转"
			return nil
		}
		orderID = p.OrderID
		needSync = true
		return nil
	}); err != nil {
		s.writeCallbackLog(ctx, params, scope, notifyResult, notifyMsg)
		return err
	}
	s.writeCallbackLog(ctx, params, scope, notifyResult, notifyMsg)

	// 阶段二：事务外 RPC 同步订单状态（带重试，不占用 DB 连接）
	if !needSync {
		return nil
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		_, lastErr = grpcclient.OrderClient.UpdateOrderStatus(ctx, &order.UpdateOrderStatusReq{
			Id:           orderID,
			Status:       2,
			OperatorType: "system",
			Remark:       "支付成功",
		})
		if lastErr == nil {
			return nil
		}
	}
	log.Printf("[CRITICAL] 支付已确认但订单状态同步失败（对账调度器与取消前置会兜底）: payment_no=%s order_id=%d err=%v",
		params.PaymentNo, orderID, lastErr)
	return nil
}

// notifyScope 一条流水的归属定位（订单号、社区）
// 三项均取自反查到的支付单快照；验签失败等连支付单都解析不出的场景全部为 0/空，
// 这类流水因 community_id=0 只出现在超管的全量队列，不会串到别的社区管理员眼前
type notifyScope struct {
	orderID     int64
	orderNo     string
	communityID int64
}

// writeCallbackLog 登记一次「确认支付」动作的流水
// NotifyKind 为 0 表示该入口不写流水；挂账事实的类别统一为 kind=3，
// 无论它由渠道回调暴露还是对账任务查出，异常页只认这一类别
// 原始报文只在异常结果下保存：成功条目事实已落 payments 表，重复存正文只会把表撑大
//
// 参数：
//   - ctx: 上下文
//   - params: 触发本次确认的参数（携带流水类别与原始报文）
//   - scope: 流水归属（订单/社区/订单号快照）
//   - result: model.NotifyResult* 结果
//   - msg: 结果摘要
func (s *PaymentService) writeCallbackLog(ctx context.Context, params *payCallbackParams, scope notifyScope, result int32, msg string) {
	if params.NotifyKind == 0 {
		return
	}
	kind := params.NotifyKind
	if result == model.NotifyResultStuck {
		kind = model.NotifyKindStuck
	}
	entry := &model.NotifyLog{
		Kind:        kind,
		Result:      result,
		Channel:     params.Channel,
		PaymentNo:   params.PaymentNo,
		OrderID:     scope.orderID,
		OrderNo:     scope.orderNo,
		CommunityID: scope.communityID,
		ErrorMsg:    msg,
		Remark:      params.Remark,
	}
	if result != model.NotifyResultSuccess && result != model.NotifyResultIgnored {
		entry.RawBody = truncateRunes(params.RawBody, notifyRawBodyMaxLen)
		entry.Headers = params.Headers
	}
	s.recordNotifyLog(ctx, entry)
}

// payCallbackParams 支付回调解析后的通用参数
// 三个字段扩展用于「谁确认了这笔支付」的可观测性：
//   - ConfirmSource: 确认来源（model.ConfirmSource*），0 时按渠道回调处理
//   - ConfirmRef: 来源凭据（后台手动/取消前置的场景标记），写入 payments.confirm_ref
//   - NotifyKind: 流水类别（model.NotifyKind*），0 表示本次确认不写流水
//   - Channel/RawBody/Headers: 流水的渠道与原始报文，仅失败场景需要保存正文
type payCallbackParams struct {
	PaymentNo     string
	TransactionID string
	Amount        float64
	ConfirmSource int32
	ConfirmRef    string
	NotifyKind    int32
	Channel       int32
	OrderID       int64
	Remark        string
	RawBody       string
	Headers       string
}

// ToPaymentInfo 将支付模型转换为 proto 信息
// 参数：
//   - p: 支付记录模型
//
// 返回值：
//   - *payment.PaymentInfo: proto 支付信息
func ToPaymentInfo(p *model.Payment) *payment.PaymentInfo {
	if p == nil {
		return nil
	}
	return &payment.PaymentInfo{
		Id:               p.ID,
		PaymentNo:        p.PaymentNo,
		OrderId:          p.OrderID,
		UserId:           p.UserID,
		PayType:          p.PayType,
		Amount:           p.Amount,
		Status:           p.Status,
		TransactionId:    p.TransactionID,
		PayTime:          ptrString(p.PayTime),
		ConfirmSource:    p.ConfirmSource,
		ConfirmRef:       p.ConfirmRef,
		ReconcileCount:   p.ReconcileCount,
		LastReconciledAt: ptrString(p.LastReconciledAt),
		CreatedAt:        p.CreatedAt,
	}
}

// ptrString 将字符串指针安全转换为字符串
//
// 参数：
//   - s: 字符串指针
//
// 返回值：
//   - string: 空字符串或指针指向的值
func ptrString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// generatePaymentNo 生成支付单号
// 使用 UUID 替代时间戳+纳秒，避免高并发下重复
// 返回值：
//   - string: 支付单号
func generatePaymentNo() string {
	return "ZF" + uuid.New().String()
}

// _ 占位，避免 common 未使用告警
var _ = common.CommonResponse{}

// _ 占位，避免 json 未使用告警
var _ = json.Marshal
