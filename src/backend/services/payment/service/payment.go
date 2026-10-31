package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/dberr"
	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/payment/model"
)

// PaymentService 支付业务服务
type PaymentService struct {
	db  *mysql.Instance
	cfg *config.Config

	// redisClient 事件总线发布用（支付异常告警经 WS 推送后台）；
	// 未注入（单测/精简部署）时事件发布静默跳过
	redisClient *redis.Client

	// payMu 保护以下支付配置缓存字段的并发读写
	payMu sync.RWMutex
	// registry 支付配置注册表（pay_config 全量行的缓存与按行构建的渠道客户端），
	// 版本号任一行变更即整表重建；nil 表示尚未加载
	registry *payClientRegistry
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
	// 启动迁移补偿：本构造函数在 main.go 的 autoMigrate 之后执行，是索引更名后清理旧索引的
	// 唯一稳定时机（AutoMigrate 不会删除既有索引）。失败只记日志不阻断启动：
	// 旧索引与新索引语义等价，留着只是冗余，不影响库级唯一约束本身
	if err := EnsurePaymentPendingFlagIndex(db.GetDb()); err != nil {
		log.Printf("清理 pending_flag 历史唯一索引失败（不影响运行，可在下次启动重试）: %v", err)
	}
	s.reloadPayConfig()
	return s
}

// WithRedis 注入 Redis 客户端（可选，用于支付异常告警的事件总线发布）
// 参数：
//   - client: Redis 客户端
//
// 返回值：
//   - *PaymentService: 自身（链式调用）
func (s *PaymentService) WithRedis(client *redis.Client) *PaymentService {
	s.redisClient = client
	return s
}

// reloadPayConfig 启动时从数据库加载支付配置并构建渠道客户端
func (s *PaymentService) reloadPayConfig() {
	rows, err := s.GetPayConfigs(context.Background())
	if err != nil {
		log.Printf("加载数据库支付配置失败（支付功能将不可用，可在后台配置后自动生效）: %v", err)
		return
	}
	s.payMu.Lock()
	defer s.payMu.Unlock()
	s.applyPayConfig(rows)
}

// siteConfigLockID 现金建单库级串行点的锚定行：site_configs 固定单行（id=1）
// 全平台单行配置表，行由建单事务内 get-or-create 兜底，不依赖启动初始化
const siteConfigLockID = int64(1)

// siteConfigLockRow 现金建单串行点锚定行的最小查询载体（仅取主键）
// 不直接复用用户服务的 SiteConfig 模型：支付服务只借该行做行锁，不关心站点配置语义，
// 避免跨服务模型依赖随站点字段演进被牵连
type siteConfigLockRow struct {
	ID int64 `gorm:"column:id"`
}

// cashCreateLockTx 现金/货到付款建单的库级串行点：锁站点设置固定行
// 现金单「创建即已支付」，不置 pending_flag（唯一索引只保护待支付态），
// 「查重 → 插入」在并发双击下非原子，同订单可落两笔已支付现金流水（交班营业额双计）。
// 旧实现用进程内分段锁，只在单容器部署下成立；改为锁表行后多实例同样串行。
// 代价是现金/货到付款建单全局串行（事务内无远程调用，收银量级可忽略）。
// 锁定目标曾是 pay_config 首行，空表时靠启动初始化占位行兜底；pay_config 多行化且
// 占位行可被管理员删除后，改锁 site_configs 固定单行（site_configs 表本身恒存在，
// 锚定行由本函数事务内 get-or-create，空库首单也能建）——锁哪一行不影响串行语义，
// 只要求全实例锁定同一目标。
// 调用方必须在同一事务内紧接着复查「已存在成功支付单」，锁才有意义。
// 参数：
//   - tx: 建单事务
//
// 返回值：
//   - error: 锚定行创建失败或加锁失败
func cashCreateLockTx(tx *gorm.DB) error {
	// get-or-create：行不存在时插入占位行（仅 id 与创建时间，无业务含义），再对同一行加锁。
	// 插入与加锁都在建单事务内完成：并发首单下唯一主键保证只有一个事务插入成功，
	// 其余事务阻塞到该事务提交后读到新行并加锁，串行语义不受影响。
	// 查询目的对象必须是可解析出 schema 的结构体：dest 为 map 时 GORM 解析不到主键列，
	// First 拼 ORDER BY 主键会直接报 model value required（v1.31 实测），现金建单全量失败
	var anchor siteConfigLockRow
	res := tx.Table("site_configs").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", siteConfigLockID).
		Take(&anchor)
	if res.Error == nil {
		return nil
	}
	if !errors.Is(res.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("锁定现金建单串行点失败: %w", res.Error)
	}
	placeholder := map[string]any{
		"id":         siteConfigLockID,
		"created_at": time.Now().Format("2006-01-02 15:04:05"),
		"updated_at": time.Now().Format("2006-01-02 15:04:05"),
	}
	if err := tx.Table("site_configs").Create(placeholder).Error; err != nil {
		// 并发首单下行已被其他事务插入，视为已存在，重新加锁读取
		if !dberr.IsDuplicate(err) {
			return fmt.Errorf("创建现金建单串行点失败: %w", err)
		}
	}
	if err := tx.Table("site_configs").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", siteConfigLockID).
		Take(&anchor).Error; err != nil {
		return fmt.Errorf("锁定现金建单串行点失败: %w", err)
	}
	return nil
}

// CreatePayment 创建支付单并生成第三方支付调起参数
// 现金(4)/货到付款(3)无需第三方调起参数，创建成功后直接同步订单状态为已支付
// （同步 RPC 在支付单事务提交之后发出，失败落异常待办交由对账扫描补同步，见 syncOfflineOrderPaid）；
// 收银台全额优惠的 0 元单（pay_type=4 且 amount=0）同样落支付单，保持「订单必有支付记录」的统一口径；
// 付款码支付(5/6)只创建待支付单（无调起参数、不动订单状态），扣款由收银员扫顾客付款码后
// 通过 PayByAuthCode 完成（两步化，见 codepay.go），渠道不支持 0 元扣款
// 幂等策略：订单已有支付成功记录时拒绝创建；已有同方式同金额的待支付单时直接复用该记录；
// 现金/货到付款在库级串行点（cashCreateLockTx）内重新查重后再插入，封堵并发双击的双流水
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
		return nil, "", bizerr.New(codes.InvalidArgument, "参数错误")
	}
	switch req.PayType {
	case 1, 2, 3, 4, 5, 6:
	default:
		return nil, "", bizerr.New(codes.InvalidArgument, "不支持的支付方式")
	}
	// 付款码支付依赖渠道真实扣款，渠道不受理 0 元交易；全额优惠单收银台应改用现金方式
	if (req.PayType == 5 || req.PayType == 6) && req.Amount <= 0 {
		return nil, "", bizerr.New(codes.InvalidArgument, "付款码支付金额必须大于 0")
	}
	// user_id 允许为 0 的场景：收银台散客单现金/付款码收款（线下当面收款，无会员账户）
	if req.UserId <= 0 && req.PayType != 3 && req.PayType != 4 && req.PayType != 5 && req.PayType != 6 {
		return nil, "", bizerr.New(codes.InvalidArgument, "参数错误")
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

	// 渠道支付方式（1/2）必须携带网关按渠道绑定解析出的配置 ID，
	// 并校验其平台匹配且启用就绪（配置不存在/停用/凭证不齐均拦截在下单）。
	// 付款码占位单（5/6）例外：建单发生在扫码之前、平台未知，config_id 留空，
	// 扫码后由 PayByAuthCode 按付款码前缀识别平台解析配置并回填 pay_type/config_id
	switch req.PayType {
	case 1:
		if _, err = s.resolveActivePayConfig(ctx, req.ConfigId, model.PayPlatformWechat); err != nil {
			return nil, "", err
		}
	case 2:
		if _, err = s.resolveActivePayConfig(ctx, req.ConfigId, model.PayPlatformAlipay); err != nil {
			return nil, "", err
		}
	}

	p := &model.Payment{
		PaymentNo:   paymentNo,
		OrderID:     req.OrderId,
		OrderNo:     req.OrderNo,
		UserID:      req.UserId,
		CommunityID: req.CommunityId,
		PayType:     req.PayType,
		ConfigID:    req.ConfigId,
		// 下单入口与微信 AppID 一并落库：前者是可追溯的渠道快照，后者是回调归属校验的依据
		// （建单时定死的身份，回调/撤销都按它核对，不再反查渠道当前配置），见 model.Payment
		ClientChannel: req.ClientChannel,
		AppID:         req.AppId,
		Amount:        req.Amount,
		Status:        1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	var payParams string
	switch req.PayType {
	case 1:
		payParams, err = s.buildWechatPayParams(ctx, p, req.Openid)
	case 2:
		// 支付宝按客户端渠道分流：支付宝小程序（alimp）走 V3 trade.create 预下单，
		// C 端 my.tradePay 唤起；其余（当前仅 h5）走手机网站支付跳转收银台
		if req.ClientChannel == "alimp" {
			payParams, err = s.buildAlipayMiniParams(ctx, p)
		} else {
			payParams, err = s.buildAlipayParams(ctx, p, "")
		}
	case 3, 4:
		// 货到付款(3)/收银台现金(4)只在本事务内把支付单标记为支付成功（4 含 0 元全额优惠单），
		// 订单状态同步留到事务提交后由 syncOfflineOrderPaid 发出
		// 确认来源标为线下：这类支付单没有渠道回调，统计「回调丢失率」时必须排除
		p.Status = 2
		p.PayTime = &now
		p.ConfirmSource = model.ConfirmSourceOffline
	case 5, 6:
		// 付款码支付：只建待支付单（无调起参数、不动订单状态），
		// 平台与配置在扫码后的 PayByAuthCode 按付款码前缀识别并回填
		_ = req.ConfigId
	}
	if err != nil {
		return nil, "", err
	}
	p.PayParams = payParams
	// 待支付占位键：配合 pending_flag 列的唯一索引 uni_payments_pending_flag（列级 unique tag 生成，
	// 见 model.Payment.PendingFlag），库级兜底「同订单至多一笔可扣款待支付单」；
	// 现金/货到付款创建即成功，不留待支付态，不置键
	if p.Status == 1 {
		p.PendingFlag = &req.OrderId
	}

	// 冲突分支被本地关闭的旧待支付单：事务提交后逐笔去渠道侧关单/撤销
	var staleClosed []model.Payment

	if err := s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 现金/货到付款创建即成功、不置 pending_flag，唯一索引保护不到它：
		// 先取库级串行点再复查成功支付，跨实例封堵并发双击写入两笔已支付流水
		if p.Status == model.PaymentStatusSuccess {
			if lerr := cashCreateLockTx(tx); lerr != nil {
				return lerr
			}
			var paid int64
			if cerr := tx.Model(&model.Payment{}).
				Where("order_id = ? AND status = ?", p.OrderID, model.PaymentStatusSuccess).
				Count(&paid).Error; cerr != nil {
				return cerr
			}
			if paid > 0 {
				return bizerr.New(codes.AlreadyExists, "订单已支付，请勿重复支付")
			}
		}
		if err := tx.Create(p).Error; err != nil {
			if !isDuplicateEntryErr(err) {
				return err
			}
			// 并发竞态兜底：同订单两笔创建同时通过查重后撞 pending_flag 唯一索引。
			// 先按同金额同渠道找可复用单（幂等口径与 findReusablePendingPayment 命中一致）；
			// 找不到说明是「金额/渠道已变化」的合法新建，关闭该订单遗留的旧待支付单后重建
			reused, rerr := s.findReusablePendingPayment(ctx, req)
			if rerr != nil {
				return rerr
			}
			if reused != nil {
				*p = *reused
				// 调起参数必须取已落库单的：本次新生成的 prepay 未持久化，返回它会造成
				// 前端调起参数与库内支付单不一致
				payParams = reused.PayParams
				return nil
			}
			// 先取出待关闭的旧单（提交后要拿它们去关渠道），再按 ID 精确关闭，
			// 避免用条件更新把窗口内新出现的其它待支付单也带走
			var stale []model.Payment
			if ferr := tx.
				Where("order_id = ? AND status = 1 AND payment_no <> ?", req.OrderId, p.PaymentNo).
				Find(&stale).Error; ferr != nil {
				return ferr
			}
			if len(stale) > 0 {
				ids := make([]int64, 0, len(stale))
				for i := range stale {
					ids = append(ids, stale[i].ID)
				}
				if uerr := tx.Model(&model.Payment{}).
					Where("id IN ?", ids).
					Updates(map[string]interface{}{
						"status":       model.PaymentStatusClosed,
						"pending_flag": nil,
						"updated_at":   now,
					}).Error; uerr != nil {
					return uerr
				}
				staleClosed = stale
			}
			if cerr := tx.Create(p).Error; cerr != nil {
				return cerr
			}
		}
		return nil
	}); err != nil {
		return nil, "", err
	}

	// 冲突分支在本地关掉的旧待支付单，必须同步关渠道侧交易：
	// 只关本地而渠道仍存活，用户在那条旧 payment_no 上完成付款就成了
	// 「钱进了、单已关」的挂账，只能人工退款收场。
	// inspectForClose 已含「渠道已收款则登记挂账待办、未收款才关单、查不到就不猜」的分级语义；
	// 这里不受 ReconcileEnabled 开关抑制——本地关单是本请求自己做的事，关渠道是它的收尾而非对账副产品。
	// 失败只留 kind=4 流水，不阻塞下单（下单结果已经确定）
	for i := range staleClosed {
		s.inspectForClose(ctx, &staleClosed[i])
	}

	// 线下收款（货到付款 3 / 收银台现金 4）的订单状态同步放在事务提交之后：
	// 两段式，与微信/支付宝走 handlePaySuccess 的「事务内只改支付单、提交后再 RPC 同步订单」同口径。
	// RPC 夹在事务里时，「订单已被远端置为已支付、本地事务随后回滚（死锁/断连）」会留下
	// 订单已支付而支付单根本不存在的孤儿态——scanPaidMismatch 只扫已存在的 status=2 支付单，
	// 永远覆盖不到；反过来先提交支付单再发 RPC，最坏只是「支付单已成功而订单未流转」，
	// 恰是既有对账扫描与后台同步入口能自动补同步、并能落待办的形态
	if req.PayType == 3 || req.PayType == 4 {
		s.syncOfflineOrderPaid(ctx, p, req.PayType)
	}
	return p, payParams, nil
}

// syncOfflineOrderPaid 线下收款（现金/货到付款）在支付单事务提交后同步订单状态
// 三次重试与 handlePaySuccess 阶段二同参数同口径；仍失败时不回滚支付单——钱已在门店当面收到，
// 否认既成事实比状态滞后更危险。此时登记一条 SyncFailed 异常流水进后台待办队列，
// 由两条出口补同步（A4 起对线下单同样生效）：
//   - 自动：对账扫描 scanPaidMismatch 逐笔核对 status=2 支付单与订单实态，不再按 pay_type 排除；
//   - 人工：后台「同步支付状态」→ SyncPaymentFromChannel 对 3/4 走 syncOfflinePayment，
//     本地单即事实、不问渠道
//
// 参数：
//   - ctx: 上下文
//   - p: 已提交成功的线下支付单（status=2）
//   - payType: 支付方式（3货到付款 4收银台现金），决定同步备注文案
func (s *PaymentService) syncOfflineOrderPaid(ctx context.Context, p *model.Payment, payType int32) {
	remark := "货到付款，支付成功"
	if payType == 4 {
		remark = "门店现金收款"
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		_, lastErr = grpcclient.OrderClient.UpdateOrderStatus(ctx, &order.UpdateOrderStatusReq{
			Id:           p.OrderID,
			Status:       2,
			OperatorType: "system",
			Remark:       remark,
		})
		if lastErr == nil {
			return
		}
	}
	log.Printf("[CRITICAL] 线下收款已确认但订单状态同步失败: payment_no=%s order_id=%d pay_type=%d err=%v",
		p.PaymentNo, p.OrderID, payType, lastErr)
	// 流水类别取「后台手动同步」（kind=5）：线下收款没有渠道回调，与渠道通知区分开；
	// 结果取 SyncFailed，NeedsHandling 据它把这条送进异常队列（kind 只影响展示归类）
	entry := notifyEntryFrom(p, model.NotifyKindManualSync, model.NotifyResultSyncFailed)
	entry.ErrorMsg = fmt.Sprintf("%s（支付单 %s）已确认收款，但订单状态同步失败：%v，系统将自动补同步，也可在后台点「同步支付状态」立即修复",
		remark, p.PaymentNo, lastErr)
	s.recordNotifyLog(ctx, entry)
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
	// 仅同配置行 + 同渠道身份绑定才可复用：期间配置被换绑（同平台换配置）或下单入口变化
	// （不同渠道的 AppID/openid 不同，已生成的调起参数不再适用）时，旧待支付单
	// 应走关闭重建流程，避免退款/回调路由到已废弃的配置或按错 AppID 归属校验失败
	err := s.db.GetDb().WithContext(ctx).
		Where("order_id = ? AND pay_type = ? AND config_id = ? AND client_channel = ? AND app_id = ? AND amount = ? AND status = ?",
			req.OrderId, req.PayType, req.ConfigId, req.ClientChannel, req.AppId, req.Amount, 1).
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

// GetPayment 根据订单 ID 查询最近一笔有效支付记录
// 有效口径：支付成功(2)或已退款(4)——已退款单同样需要向用户展示原支付方式，
// 故退款后仍返回该笔支付单（Status=4），调用方按需甄别
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//
// 返回值：
//   - *model.Payment: 支付记录模型
//   - error: 错误信息
func (s *PaymentService) GetPayment(ctx context.Context, orderID int64) (*model.Payment, error) {
	var p model.Payment
	if err := s.db.GetDb().WithContext(ctx).
		Where("order_id = ? AND status IN ?", orderID, []int32{model.PaymentStatusSuccess, model.PaymentStatusRefunded}).
		Order("id desc").First(&p).Error; err != nil {
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
		// 只有记录不存在才是「支付单不存在」；DB 故障原样抛出，别让轮询端把服务异常
		// 显示成「单号无效」而放弃重试
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, bizerr.New(codes.NotFound, "支付单不存在")
		}
		return nil, err
	}
	return &p, nil
}

// FindOrdersByPayNo 按支付单号或渠道交易号反查关联订单号（后台订单列表「支付单号/渠道交易号」搜索用）
// 单框双号，字段渠道无关：pay_no 同时精确匹配 payment_no（渠道账单「商家订单号」out_trade_no）
// 与 transaction_id（渠道交易号，支付宝 trade_no / 微信 transaction_id），后续接入新渠道无需改动本查询。
// 同一订单可能有多笔支付尝试（失败重试复用同一支付单号），命中后按订单号去重，
// 供 gateway 拿订单号集合继续到 order 服务查订单
// 参数：
//   - ctx: 上下文
//   - payNo: 支付单号或渠道交易号（精确匹配）
//
// 返回值：
//   - []string: 关联订单号去重列表（未命中返回空切片，不视为错误）
//   - error: 参数为空返回 InvalidArgument；查询失败返回 Internal
//
// 使用示例：
//
//	orderNos, err := svc.FindOrdersByPayNo(ctx, "PAY202601010001")
func (s *PaymentService) FindOrdersByPayNo(ctx context.Context, payNo string) ([]string, error) {
	payNo = strings.TrimSpace(payNo)
	if payNo == "" {
		return nil, bizerr.New(codes.InvalidArgument, "支付单号不能为空")
	}
	var orderNos []string
	err := s.db.GetDb().WithContext(ctx).Model(&model.Payment{}).
		Where("payment_no = ? OR transaction_id = ?", payNo, payNo).
		Distinct().Pluck("order_no", &orderNos).Error
	if err != nil {
		return nil, bizerr.New(codes.Internal, "查询支付单失败")
	}
	return orderNos, nil
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
//   - from: 支付发起场景标记（引导页透传，白名单外忽略）
//
// 返回值：
//   - *model.Payment: 支付记录模型（任何状态均可见，供引导页分流）
//   - string: 支付宝收银台链接，仅待支付且 pay_type=2 时有值
//   - error: 参数为空或记录不存在或重新签名失败
func (s *PaymentService) GetAlipayPayUrl(ctx context.Context, paymentNo, from string) (*model.Payment, string, error) {
	p, err := s.GetPaymentStatusByNo(ctx, paymentNo)
	if err != nil {
		return nil, "", err
	}
	// 仅待支付的支付宝单可换取新链接；现金/付款码单或已终态单不给链接
	if p.Status != 1 || p.PayType != 2 {
		return p, "", nil
	}
	params, err := s.buildAlipayParams(ctx, p, from)
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

// BatchGetPayments 按订单 ID 集合批量查询有效支付记录
// 口径与 GetPayment 一致：一个订单可能有多条支付记录，只回传每个订单最近一笔有效支付单
// （status 为支付成功 2 或已退款 4，已退款单同样需要展示原支付方式）。
// 后台订单列表原先逐行 GetPayment，一页 N 行即 N 次往返；本方法一次往返取回整页。
// 待支付/无有效记录的订单不出现在结果里，调用方按「查不到即未支付」处理。
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
	// 按 id 倒序遍历、每个订单只留首条即「最近一笔有效支付」（成功/已退款），避免相关子查询
	if err := s.db.GetDb().WithContext(ctx).
		Where("order_id IN ? AND status IN ?", clean, []int32{model.PaymentStatusSuccess, model.PaymentStatusRefunded}).
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
	headers := make(http.Header)
	for k, v := range req.Headers {
		headers.Set(k, v)
	}
	params, err := parseWechatCallback(s, ctx, req.Body, headers)
	if err != nil {
		// 微信通知全文加密，验签/解密失败时连支付单号都解析不出，归属只能留空（community_id=0），
		// 这类流水因此只出现在超管的全量队列
		entry := notifyEntryFrom(nil, model.NotifyKindCallback, wechatCallbackFailureResult(err))
		entry.Channel = 1
		entry.ErrorMsg = err.Error()
		entry.RawBody = truncateRunes(string(req.Body), notifyRawBodyMaxLen)
		entry.Headers = maskWechatNotifyHeaders(headers)
		s.recordNotifyLog(ctx, entry)
		// 非支付成功通知（退款通知等）是合法分流：回 ACK 让微信停止重推（P1-1）。
		// 回 FAIL 会让渠道按策略重发，每重发一次就多一条幂等忽略流水，把表撑成噪声
		if errors.Is(err, errNotSuccessNotification) {
			return nil
		}
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
	params, err := s.parseAlipayCallback(ctx, req.Params)
	if err != nil {
		// 支付宝通知的 out_trade_no 是明文表单字段，验签失败前后都能读到；
		// 据此反查支付单即可把流水归到正确社区（反查只做只读定位，不可信的单号查不到就是空）
		paymentNo := req.Params["out_trade_no"]
		entry := notifyEntryFrom(s.scopeByPaymentNo(ctx, paymentNo), model.NotifyKindCallback, alipayCallbackFailureResult(err))
		entry.Channel = 2
		entry.PaymentNo = paymentNo
		entry.ErrorMsg = err.Error()
		entry.RawBody = alipayNotifyRawBody(req.Params)
		s.recordNotifyLog(ctx, entry)
		// 非支付成功通知（WAIT_BUYER_PAY/TRADE_CLOSED 等状态推送）是合法分流：回 ACK 让支付宝
		// 停止重发（P1-1）。回 failure 会触发按通知策略重发，每重发一次就多一条幂等忽略流水
		if errors.Is(err, errNotSuccessNotification) {
			return nil
		}
		return err
	}
	params.NotifyKind = model.NotifyKindCallback
	params.Channel = 2
	params.RawBody = alipayNotifyRawBody(req.Params)
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
// 流程分三阶段：先在事务内（FOR UPDATE 锁支付单行）完成幂等判定、累计上限校验并落「待退款」流水，
// 事务提交后再调用渠道退款，成功后将流水置「退款成功」——渠道调用不占用事务连接，
// 渠道失败时流水置「退款失败」保留审计，重试由调用方以同一 refund_no 重新发起
// P0-2：微信渠道受理常态（PROCESSING）不再视为成功——流水保持待退款并返回 completed=false，
// 调用方（order 服务）不得走成功收尾，终态由退款查单任务 reconcileRefunds 收敛
// 参数：
//   - ctx: 上下文
//   - req: 退款请求
//
// 返回值：
//   - *payment.ProcessRefundResp: completed=渠道退款是否已到成功终态
//   - error: 错误信息
//
// 使用示例：
//
//	resp, err := svc.ProcessRefund(ctx, &payment.ProcessRefundReq{OrderId: 12, RefundNo: "TK...", Amount: 20})
//	if err == nil && resp.GetCompleted() { /* 渠道已退款到账，可走订单收尾 */ }
func (s *PaymentService) ProcessRefund(ctx context.Context, req *payment.ProcessRefundReq) (*payment.ProcessRefundResp, error) {
	if req.OrderId <= 0 || req.RefundNo == "" || req.Amount <= 0 {
		return nil, bizerr.New(codes.InvalidArgument, "退款参数错误")
	}

	p, err := s.findRefundPayment(ctx, req.OrderId, req.PaymentNo)
	if err != nil {
		return nil, err
	}

	// 现金类支付（货到付款=3 / 收银台现金=4）不经过第三方退款，事务内直接落成功流水
	if p.PayType == 3 || p.PayType == 4 {
		if err := s.recordCashRefund(ctx, req, p); err != nil {
			return nil, err
		}
		return &payment.ProcessRefundResp{Completed: true}, nil
	}

	// 渠道退款：客户端/执行器按支付单绑定的配置行取用（内部含热刷新），
	// 未承接过下单的实例若沿用启动时的空配置会把可用渠道判为未配置而退款失败

	now := time.Now().Format("2006-01-02 15:04:05")
	refund := &model.RefundPayment{
		RefundNo:  req.RefundNo,
		PaymentNo: p.PaymentNo,
		// 订单 ID 一律取支付单自身的归属，不用请求声称的 req.OrderId：
		// findRefundPayment 已强校验二者一致，这里再落一次事实来源，
		// 避免任何绕过校验的调用路径把退款流水挂到别的订单上
		OrderID:   p.OrderID,
		Amount:    req.Amount,
		Status:    1,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// 第一阶段：锁支付单行，完成幂等判定、累计校验并落「待退款」流水
	// refundAlreadyDone 标记幂等命中（同 refund_no 已有退款成功流水），命中后直接返回已完成，
	// 绝不能再往下走渠道退款与第三阶段落库
	var refundAlreadyDone bool
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
				// 幂等命中：该退款单号早已退成功，标记后由外层短路返回 completed=true。
				// 此前这里是裸 return nil —— 事务提交后代码继续走第二阶段触渠道、
				// 第三阶段按 id=0（本次的 refund 从未落库）更新 status=1 的行，
				// 0 行生效被当作「退款流水状态异常，请人工核查」回一个假错误，
				// 而渠道侧其实已被二次申请退款。
				// 但「命中」只在既有流水与本次请求同支付单、同金额时成立：
				// 钱已按旧金额出账，若本次请求金额不同还回 completed=true，
				// 调用方会以为新金额已退成功，差额就此消失，故不一致直接拒绝
				if exists.PaymentNo != p.PaymentNo || math.Abs(exists.Amount-req.Amount) > 1e-6 {
					return bizerr.New(codes.FailedPrecondition, "退款单号已存在且与本次请求不一致，请更换退款单号")
				}
				refundAlreadyDone = true
				return nil
			case 3:
				// 上次渠道失败：清理失败流水后重新发起（审计留痕在调用方 refund_logs）
				if err := tx.Delete(&exists).Error; err != nil {
					return err
				}
			default:
				return bizerr.New(codes.FailedPrecondition, "退款处理中，请勿重复提交；如长时间未完成请人工核实渠道结果")
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
			return bizerr.New(codes.FailedPrecondition, "退款金额超过剩余可退金额")
		}

		return tx.Create(refund).Error
	}); err != nil {
		return nil, err
	}
	if refundAlreadyDone {
		log.Printf("[ProcessRefund] 退款幂等命中已成功流水，直接返回完成 refund_no=%s payment_no=%s",
			req.RefundNo, p.PaymentNo)
		return &payment.ProcessRefundResp{Completed: true}, nil
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
	if errors.Is(refundErr, errRefundAccepted) {
		// P0-2：渠道已受理未到终态（受理常态 PROCESSING）——流水保持待退款(1)、
		// TransactionID 已落，返回 completed=false 令调用方不得走成功收尾；
		// 终态由退款查单任务（scheduler.reconcileRefunds）按 RefundQuery 收敛
		return &payment.ProcessRefundResp{Completed: false}, nil
	}
	if refundErr != nil {
		// 渠道失败：流水置「退款失败」保留审计，重试时由幂等清理逻辑放行
		failTime := time.Now().Format("2006-01-02 15:04:05")
		if uerr := s.db.GetDb().WithContext(ctx).Model(&model.RefundPayment{}).
			Where("id = ? AND status = 1", refund.ID).
			Updates(map[string]interface{}{"status": int32(3), "updated_at": failTime}).Error; uerr != nil {
			log.Printf("[ProcessRefund] 退款流水置失败状态异常 refund_no=%s: %v", refund.RefundNo, uerr)
		}
		return nil, refundErr
	}

	// 第三阶段：渠道成功，锁定支付单行后确认流水成功并按累计判断支付单是否全额退款
	if err := s.completeChannelRefundSuccess(ctx, p, refund); err != nil {
		// 渠道已经出款而本地流水没落终态：当场用独立事务补一次（首次失败多是死锁/连接抖动，
		// 重试是新会话，成功概率高）。补偿仍失败时登记人工待办并把原错误回传调用方——
		// 支付宝是同步语义、渠道侧不会再给回执，订单退款单会停在「退款中」，
		// 只能靠这条待办（以及退款收敛任务的超期转办）兜住，绝不能静默留成 status=1
		log.Printf("[CRITICAL] 渠道退款成功而本地流水落库失败，尝试补偿 refund_no=%s payment_no=%s: %v",
			refund.RefundNo, refund.PaymentNo, err)
		if cerr := s.completeChannelRefundSuccess(ctx, p, refund); cerr == nil {
			log.Printf("[ProcessRefund] 退款流水补偿落库成功 refund_no=%s", refund.RefundNo)
			return &payment.ProcessRefundResp{Completed: true}, nil
		}
		s.recordRefundLandingFailed(ctx, p, refund, err)
		return nil, err
	}
	return &payment.ProcessRefundResp{Completed: true}, nil
}

// recordRefundLandingFailed 登记「渠道退款已成功而本地流水未落终态」的人工待办
// 资金事实与库内状态已经背离（钱已退给用户、流水仍停在待退款(1)），且当场补偿也已失败，
// 必须进后台异常队列由人工按渠道商户平台核实后处理：
// 微信侧退款查单任务后续仍会把该流水收敛掉（待办可销），支付宝侧没有退款查单接口，
// 这条待办就是唯一的收敛入口
//
// 参数：
//   - ctx: 上下文
//   - p: 原支付单（提供社区/订单归属快照）
//   - refund: 未落终态的退款流水
//   - cause: 落库失败（含补偿失败）的原始错误
func (s *PaymentService) recordRefundLandingFailed(ctx context.Context, p *model.Payment, refund *model.RefundPayment, cause error) {
	entry := notifyEntryFrom(p, model.NotifyKindReconcile, model.NotifyResultSyncFailed)
	entry.ErrorMsg = fmt.Sprintf("渠道退款已出款（退款单 %s，金额 %.2f）而本地退款流水落库失败：%v；"+
		"当场补偿仍未成功，订单退款单会停留「退款中」，请到渠道商户平台核实后人工收敛",
		refund.RefundNo, refund.Amount, cause)
	s.recordNotifyLog(ctx, entry)
}

// completeChannelRefundSuccess 渠道退款成功后的统一落库收尾（P0-2 抽取，供
// ProcessRefund 第三阶段与退款查单任务 reconcileRefunds 共用）：
// 流水置退款成功并记退款时间，累计退款金额等于支付金额时把支付单置为已退款
//
// 参数：
//   - ctx: 上下文
//   - p: 支付记录（方法内按主键重新 FOR UPDATE 加载，调用方传入的实例仅作定位）
//   - refund: 退款流水（须处于待退款(1)状态，TransactionID 应已落渠道退款单号）
//
// 返回值：
//   - error: 落库失败或流水状态冲突时返回错误
func (s *PaymentService) completeChannelRefundSuccess(ctx context.Context, p *model.Payment, refund *model.RefundPayment) error {
	return s.db.GetDb().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 按支付单号加载（查单任务只有单号没有主键）；payment_no 唯一索引保证精确定位
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(p, "payment_no = ?", p.PaymentNo).Error; err != nil {
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
			return bizerr.New(codes.FailedPrecondition, fmt.Sprintf("退款流水状态异常，请人工核查 refund_no=%s", refund.RefundNo))
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

// QueryRefundResult 查询渠道退款流水状态（读库不触渠道，供 order 侧卡单退款收敛决策）
// 参数：
//   - ctx: 上下文
//   - req: 退款单号 + 订单 ID（归属校验）
//
// 返回值：
//   - *payment.QueryRefundResultResp: status 0=流水不存在 1=处理中 2=成功 3=失败
func (s *PaymentService) QueryRefundResult(ctx context.Context, req *payment.QueryRefundResultReq) (*payment.QueryRefundResultResp, error) {
	if req.RefundNo == "" {
		return nil, bizerr.New(codes.InvalidArgument, "退款单号不能为空")
	}
	var flow model.RefundPayment
	err := s.db.GetDb().WithContext(ctx).
		Where("refund_no = ? AND order_id = ?", req.RefundNo, req.OrderId).
		First(&flow).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 流水不存在（历史失败流水被清理后重试前的窗口）：按「失败」口径让调用方重新发起
		return &payment.QueryRefundResultResp{Status: 3}, nil
	}
	if err != nil {
		return nil, err
	}
	return &payment.QueryRefundResultResp{Status: flow.Status}, nil
}

// ResetRefundFlow 人工重置「待退款」退款流水的自动收敛预算（B2）
// 退款查单任务对同一笔流水连续失败达 refundQueryMaxRetry 次后把它永久逐出自动批次，
// 而微信退款允许 1-3 个工作日到账——渠道后来给出终态也没人来收了。本方法是这个死角的出口：
// 管理员在渠道商户平台核实后把 retry_count/escalated 归零，流水重新被查单任务捞起继续收敛。
//
// 状态门禁（只有待退款(1)可重置）：
//   - 退款成功(2) 拒绝：钱已出账，放开会让 order 侧「重试退款」再次触渠道出款；
//   - 退款失败(3) 拒绝：失败态本就允许以同一退款单号重新发起，重置没有意义；
//   - 查无流水 回 recovered=false 而非报错：失败流水在重新发起前会被清理，属正常窗口。
//
// 核实依据（remark）必填：本动作等于「人工确认渠道结果后重新交给系统收敛」，
// 无依据的盲重置会把已经判定的挂账流水重新推回自动出款路径，且事后无从追溯谁批的。
//
// 参数：
//   - ctx: 上下文
//   - req: 重置请求（order_id 归属强校验 + refund_no 定位 + remark 核实依据 + 操作人）
//
// 返回值：
//   - *payment.ResetRefundFlowResp: recovered=是否真正改写了流水
//   - error: 参数缺失、状态不允许或并发下状态已变更时返回错误
//
// 使用示例：
//   - resp, err := svc.ResetRefundFlow(ctx, &payment.ResetRefundFlowReq{OrderId: 12, RefundNo: "TK...", Remark: "微信商户平台显示退款处理中"})
func (s *PaymentService) ResetRefundFlow(ctx context.Context, req *payment.ResetRefundFlowReq) (*payment.ResetRefundFlowResp, error) {
	if req.OrderId <= 0 || req.RefundNo == "" {
		return nil, bizerr.New(codes.InvalidArgument, "退款参数错误")
	}
	if strings.TrimSpace(req.Remark) == "" {
		return nil, bizerr.New(codes.InvalidArgument, "请填写渠道核实依据")
	}

	var flow model.RefundPayment
	err := s.db.GetDb().WithContext(ctx).
		Where("refund_no = ? AND order_id = ?", req.RefundNo, req.OrderId).
		First(&flow).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &payment.ResetRefundFlowResp{Message: "无对应退款流水（失败流水在重新发起前会被清理），本次未改动"}, nil
	}
	if err != nil {
		return nil, err
	}
	switch flow.Status {
	case 2:
		return nil, bizerr.New(codes.FailedPrecondition, "退款流水已确认成功，无需重置；订单未收尾请用「重试退款」")
	case 3:
		return nil, bizerr.New(codes.FailedPrecondition, "退款流水已是失败态，可直接重新发起退款，无需重置")
	}

	// 条件更新防并发：仅当流水仍处于待退款(1)时生效。
	// updated_at 是查单批次的轮转指针（Order updated_at asc），刷新为当前时间等于把它
	// 排到队尾再回到批次视野，不会与正在跑的那一轮查单抢同一笔
	now := time.Now().Format("2006-01-02 15:04:05")
	res := s.db.GetDb().WithContext(ctx).Model(&model.RefundPayment{}).
		Where("id = ? AND status = 1", flow.ID).
		Updates(map[string]interface{}{
			"retry_count": int32(0),
			"escalated":   int32(0),
			"updated_at":  now,
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, bizerr.New(codes.FailedPrecondition, "退款流水状态已变更，请刷新后重试")
	}

	s.recordRefundResetTrace(ctx, &flow, req)
	return &payment.ResetRefundFlowResp{Recovered: true, Message: "退款流水已重新纳入自动收敛"}, nil
}

// recordRefundResetTrace 记录人工重置留痕并销掉该退款单此前的转办待办
// 两步都只是留痕，失败不影响重置结果（流水预算已归零，异常队列里多留一条待办的代价
// 远小于把预算重新钉死）；留痕走 notify_logs 而非只打日志，后台异常页可回查谁在何时核实过什么
//
// 参数：
//   - ctx: 上下文
//   - flow: 已重置的退款流水
//   - req: 重置请求（取核实依据与操作人）
func (s *PaymentService) recordRefundResetTrace(ctx context.Context, flow *model.RefundPayment, req *payment.ResetRefundFlowReq) {
	var p model.Payment
	if err := s.db.GetDb().WithContext(ctx).
		Where("payment_no = ?", flow.PaymentNo).First(&p).Error; err != nil {
		// 支付单查不到时仍按流水快照登记，保证动作留痕不丢
		p = model.Payment{PaymentNo: flow.PaymentNo, OrderID: flow.OrderID}
	}
	remark := fmt.Sprintf("人工重置收敛预算：%s（操作人 %s/%d）", strings.TrimSpace(req.Remark), req.OperatorName, req.OperatorId)
	entry := notifyEntryFrom(&p, model.NotifyKindRefundReset, model.NotifyResultRefundReset)
	entry.ErrorMsg = remark
	s.recordNotifyLog(ctx, entry)

	// 销账此前为这笔流水登记的转办待办：按支付单号 + 待处理 + 退款单号匹配（待办原因里带单号），
	// 只动这一笔退款相关的，同支付单其它待办不受影响
	res := s.db.GetDb().WithContext(ctx).Model(&model.NotifyLog{}).
		Where("payment_no = ? AND handled = ? AND result = ? AND error_msg LIKE ?",
			flow.PaymentNo, model.NotifyPending, model.NotifyResultSyncFailed, "%"+flow.RefundNo+"%").
		Updates(map[string]interface{}{
			"handled": int32(model.NotifyHandled),
			"remark":  truncateRunes(remark, notifyRemarkMaxLen),
		})
	if res.Error != nil {
		log.Printf("[payment] 退款待办销账失败 refund_no=%s: %v", flow.RefundNo, res.Error)
	}
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
		return bizerr.New(codes.InvalidArgument, "参数错误")
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
		Updates(map[string]interface{}{
			"status":       model.PaymentStatusClosed,
			"pending_flag": nil, // 释放待支付占位键，允许同订单创建新待支付单
			"updated_at":   now,
		})
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
		// 关单前核实属系统自动路径：挂账待办按 kind=3 登记，不带操作人
		s.recordStuckPayment(ctx, p, res, systemActor)
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
// 走 payment_no 分支时必须校验支付单归属（P1-2 同口径）：网关只校验路径订单的社区归属，
// 管不住 body 里的支付单号——在 A 订单页面提交 B 订单的单号就能跨订单退款，
// 因此 orderID>0 且与支付单归属不一致时直接拒绝
// 参数：
//   - ctx: 上下文
//   - orderID: 订单 ID
//   - paymentNo: 支付单号，可为空
//
// 返回值：
//   - *model.Payment: 支付记录
//   - error: 未找到指定支付单、支付单与订单不匹配时返回错误
//
// 使用示例：
//   - p, err := s.findRefundPayment(ctx, req.GetOrderId(), req.GetPaymentNo())
func (s *PaymentService) findRefundPayment(ctx context.Context, orderID int64, paymentNo string) (*model.Payment, error) {
	if paymentNo != "" {
		var p model.Payment
		if err := s.db.GetDb().WithContext(ctx).Where("payment_no = ? AND status = ?", paymentNo, 2).First(&p).Error; err != nil {
			return nil, fmt.Errorf("未找到指定支付单: %w", err)
		}
		// 与 sync.go / codepay.go 的绑定校验保持一致：只在实际传入订单 ID 时比对
		if orderID > 0 && p.OrderID != orderID {
			return nil, bizerr.New(codes.FailedPrecondition, "支付单与订单不匹配，请刷新订单信息后重试")
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
// 现金/货到付款不调用第三方支付，事务内锁支付单行后完成幂等判定与累计上限校验，直接落成功流水。
// 幂等按 refund_no 命中后仍要核对支付单号与金额（差值容差 1e-6）：不一致说明是「同号换金额」
// 的重复提交，必须拒绝而非按旧流水静默成功，否则调用方拿到成功却少退/多退钱
// 参数：
//   - ctx: 上下文
//   - req: 退款请求
//   - p: 原支付记录
//
// 返回值：
//   - error: 错误信息（退款单号冲突 / 超过剩余可退金额 / 数据库错误）
//
// 使用示例：
//   - err := s.recordCashRefund(ctx, req, p)
func (s *PaymentService) recordCashRefund(ctx context.Context, req *payment.ProcessRefundReq, p *model.Payment) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	refund := &model.RefundPayment{
		RefundNo:  req.RefundNo,
		PaymentNo: p.PaymentNo,
		// 与 ProcessRefund 第一阶段一致：订单归属一律取支付单，不采信请求声称的 req.OrderId，
		// 否则跨订单单号退款会让 QueryRefundResult 的归属校验失准
		OrderID:       p.OrderID,
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

		// 幂等：同 refund_no 已存在时不再落第二笔，但必须核对既有流水与本请求一致——
		// 只按 refund_no 命中就 return nil，等于「换个金额重试也按旧金额静默成功」，
		// 调用方以为退了新金额、账上却是旧金额，属资金口径错误，故不一致直接拒绝
		var exists model.RefundPayment
		err := tx.Where("refund_no = ?", req.RefundNo).First(&exists).Error
		if err == nil {
			if exists.PaymentNo != p.PaymentNo || math.Abs(exists.Amount-req.Amount) > 1e-6 {
				return bizerr.New(codes.FailedPrecondition, "退款单号已存在且与本次请求不一致，请更换退款单号")
			}
			if exists.Status != 2 {
				return bizerr.New(codes.FailedPrecondition, "该退款单号已有非成功流水，请更换退款单号")
			}
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
			return bizerr.New(codes.FailedPrecondition, "退款金额超过剩余可退金额")
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
// RPC 失败时立即重试 3 次；若仍失败则落一条「订单同步失败」待处理异常（P0-3/P1-13）——
// 支付单已确认而订单未流转属于资金不一致，必须进后台异常队列人工介入，
// 不能只打 stderr：对账扫描（detectPaidMismatch）会尝试自动补同步，取消护栏也依赖订单实态。
// 确认成功流水在订单同步落地后才写：未同步成功的「成功」是半成品，不构成完整事实。
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
				"pending_flag":   nil, // 释放待支付占位键
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

	// 阶段二：事务外 RPC 同步订单状态（带重试，不占用 DB 连接）
	if !needSync {
		// 幂等忽略/金额不符/挂账等结论立即落流水；确认成功要等订单同步落地后才算完整动作
		s.writeCallbackLog(ctx, params, scope, notifyResult, notifyMsg)
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
			break
		}
	}
	if lastErr != nil {
		// 终次失败（P0-3/P1-13）：支付单已确认（status=2）而订单未流转。
		// 落一条待处理异常进后台队列，由对账扫描自动补同步或人工点「同步支付状态」修复
		log.Printf("[CRITICAL] 支付已确认但订单状态同步失败: payment_no=%s order_id=%d err=%v",
			params.PaymentNo, orderID, lastErr)
		s.writeCallbackLog(ctx, params, scope, model.NotifyResultSyncFailed,
			fmt.Sprintf("支付已确认但订单状态同步失败: %v", lastErr))
		return nil
	}
	s.writeCallbackLog(ctx, params, scope, notifyResult, notifyMsg)
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
		Kind:         kind,
		Result:       result,
		Channel:      params.Channel,
		PaymentNo:    params.PaymentNo,
		OrderID:      scope.orderID,
		OrderNo:      scope.orderNo,
		CommunityID:  scope.communityID,
		ErrorMsg:     msg,
		Remark:       params.Remark,
		DetectRemark: params.DetectRemark,
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
	// DetectRemark 检出留痕（谁触发、当时本地什么状态），B3；与 Remark 分工：
	// Remark 属于人工核实依据/销账备注，会被销账整列覆写，留痕写这里才留得住
	DetectRemark string
	RawBody      string
	Headers      string
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

// isDuplicateEntryErr 判断错误是否为唯一索引冲突（MySQL 1062 Duplicate entry）
// 供待支付占位键 uni_payments_pending_flag（列级 unique tag 生成，历史名为 uk_payments_order_pending）
// 的并发竞态兜底使用；调用方随后按业务逻辑（复用/关闭重建）自证语义。
// 判定走 common/dberr 的错误号精确匹配：旧实现用 strings.Contains(err, "1062")，
// 驱动把 SQL 参数原文带进错误文本时（如金额 1062.00）会把普通报错误判成唯一键冲突。
//
// 参数：
//   - err: 数据库错误
//
// 返回值：
//   - bool: 唯一键冲突返回 true
func isDuplicateEntryErr(err error) bool {
	return dberr.IsDuplicate(err)
}

// _ 占位，避免 common 未使用告警
var _ = common.CommonResponse{}

// _ 占位，避免 json 未使用告警
var _ = json.Marshal
