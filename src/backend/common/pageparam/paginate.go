// Package pageparam 列表接口分页入参的服务层收敛。
//
// 网关 HTTP 侧已有 maxPageSize=100，但那只约束走网关的请求：gRPC 可被内部服务、
// 定时任务或直连客户端调用，页长不在归属层设限就等于把「page_size=100000 拉全表」
// 这条路径敞开——一次全表扫描会占满连接与内存，拖垮同实例上所有列表接口。
// 本包把散落在各服务的「补默认值 + 封顶」写成一处口径，调用方只需按数据量选档位。
package pageparam

// 分页档位常量
const (
	// DefaultSize 未传或传入非法页长时的默认条数（沿用各服务历史默认，避免行为漂移）
	DefaultSize = 10

	// MaxSize 面向用户的常规列表封顶：足够覆盖后台一页可见行数上限，
	// 又能挡住「一次要十万行」的放大攻击
	MaxSize = 200

	// BatchMaxSize 内部批量出口封顶：超管总览、社区选择器等需要一次取全量的聚合场景，
	// 目标表本身规模有限（社区/角色/分类为数十行量级），放宽到 1000 仍是一次可承受的读取
	BatchMaxSize = 1000
)

// Clamp 归一常规列表的页码与页长
//
// 参数：
//   - page: 请求页码（小于 1 视为首页）
//   - pageSize: 请求页长（小于 1 用 DefaultSize，大于 MaxSize 收敛到 MaxSize）
//
// 返回值：
//   - int: 归一后的页码（>=1）
//   - int: 归一后的页长（1~MaxSize）
//
// 使用示例：
//
//	page, pageSize := pageparam.Clamp(int(req.Pagination.GetPage()), int(req.Pagination.GetPageSize()))
func Clamp(page, pageSize int) (int, int) {
	return ClampMax(page, pageSize, MaxSize)
}

// ClampBatch 归一内部批量出口的页码与页长；语义同 Clamp，封顶放宽到 BatchMaxSize
//
// 参数：
//   - page: 请求页码
//   - pageSize: 请求页长
//
// 返回值：
//   - int: 归一后的页码（>=1）
//   - int: 归一后的页长（1~BatchMaxSize）
func ClampBatch(page, pageSize int) (int, int) {
	return ClampMax(page, pageSize, BatchMaxSize)
}

// ClampMax 按调用方给定的上限归一页码与页长
// 用于比通用档位更严格的出口（履约台 50、报表 100），上限来源仍是调用方自己的常量
//
// 参数：
//   - page: 请求页码
//   - pageSize: 请求页长
//   - maxSize: 本页长上限，小于 1 时按 DefaultSize 处理，避免上限倒挂返回空页
//
// 返回值：
//   - int: 归一后的页码（>=1）
//   - int: 归一后的页长（1~max(1,maxSize)）
func ClampMax(page, pageSize, maxSize int) (int, int) {
	if maxSize < 1 {
		maxSize = DefaultSize
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = DefaultSize
	}
	if pageSize > maxSize {
		pageSize = maxSize
	}
	return page, pageSize
}

// ClampWith 自定义默认页长与上限
// 面向「默认一页就是 20 条」这类历史口径（如行政区划树），不为了套用本包而改写既有默认值
//
// 参数：
//   - page: 请求页码
//   - pageSize: 请求页长
//   - defaultSize: 页长非法时使用的默认值（小于 1 时退化为 DefaultSize）
//   - maxSize: 页长上限
//
// 返回值：
//   - int: 归一后的页码（>=1）
//   - int: 归一后的页长（1~maxSize）
func ClampWith(page, pageSize, defaultSize, maxSize int) (int, int) {
	if defaultSize < 1 {
		defaultSize = DefaultSize
	}
	if pageSize < 1 {
		pageSize = defaultSize
	}
	if maxSize < 1 {
		maxSize = defaultSize
	}
	return ClampMax(page, pageSize, maxSize)
}
