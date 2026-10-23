package service

import (
	"os"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
)

/**
 * 数据库时钟与进程时钟一致性守卫（A5）
 *
 * 盯的故障形态：过期判定、时间窗扫描这类「库里比 NOW()」的口径，前提是数据库
 * 会话时钟和写入方（Go 进程）时钟同一时区。CI 的 mysql service 容器默认 UTC，
 * 而生产容器 TZ=Asia/Shanghai：容器时区不一致时，测试里按进程时区写入的
 * expire_at/created_at 与库侧 NOW() 差 8 小时，边界断言会在「本地绿、CI 红」
 * 或更坏的「CI 绿、生产错」之间摇摆，且失败信息只说「券应被拒核销」，指不到根因。
 *
 * 本用例把这个前提显式钉住并打印取证字段（CI 日志即为时钟证据）：
 * 一旦 mysql service 的 TZ 丢失或驱动 loc 配置漂移，这里直接红并给出定位。
 *
 * 运行：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/product/service/ -run TestDBClockMatchesProcessTimezone -v
 */

// TestDBClockMatchesProcessTimezone 断言库侧 NOW() 与进程当前时刻一致（容差 90s）
// 只断言「一致」而不断言具体时区：本地开发用 UTC 库 + UTC 进程同样成立，
// 生产与 CI 要求的是两侧同口径，而非某一边必须是 +08:00
//
// 参数：
//   - t: 测试对象（TEST_MYSQL_PASSWORD 未设置时 skip）
//
// 使用示例：
//   - go test ./services/product/service/ -run TestDBClock -v
func TestDBClockMatchesProcessTimezone(t *testing.T) {
	if os.Getenv("TEST_MYSQL_PASSWORD") == "" {
		t.Skip("TEST_MYSQL_PASSWORD 未设置，跳过后端集成测试")
	}
	dbInst, err := mysql.New(jcbaseGo.DbStruct{
		Host:      stockTestEnvOr("TEST_MYSQL_HOST", "127.0.0.1"),
		Port:      stockTestEnvOr("TEST_MYSQL_PORT", "3306"),
		Dbname:    stockTestEnvOr("TEST_MYSQL_DBNAME", "market"),
		Username:  stockTestEnvOr("TEST_MYSQL_USER", "root"),
		Password:  os.Getenv("TEST_MYSQL_PASSWORD"),
		Charset:   "utf8mb4",
		ParseTime: "True",
		Loc:       "Local",
	})
	if err != nil {
		t.Fatalf("连接测试 MySQL 失败: %v", err)
	}

	var (
		dbNow         time.Time
		systemTZ      string
		globalTZ      string
		sessionTZ     string
		formatted     string
		processOffset int
	)
	// NOW() 在 parseTime=True 下读回 time.Time，驱动按 DSN loc 解释 DATETIME 字面量；
	// DATE_FORMAT 再取一次「纯字面量」口径，用于比对库自己的墙上时间
	row := dbInst.GetDb().Raw(`SELECT NOW(), DATE_FORMAT(NOW(), '%Y-%m-%d %H:%i:%s'), @@system_time_zone, @@global.time_zone, @@session.time_zone`).Row()
	if err := row.Scan(&dbNow, &formatted, &systemTZ, &globalTZ, &sessionTZ); err != nil {
		t.Fatalf("读取数据库时钟失败: %v", err)
	}
	processNow := time.Now()
	_, processOffset = processNow.Zone()

	t.Logf("时钟取证：进程 TZ=%s(offset=%ds) | 库 NOW()=%s | 库墙上时间字面量=%s | @@system_time_zone=%s @@global.time_zone=%s @@session.time_zone=%s",
		processNow.Format("MST"), processOffset, dbNow.Format(time.RFC3339), formatted, systemTZ, globalTZ, sessionTZ)

	// 库侧字面量必须等于进程按业务写入格式（"2006-01-02 15:04:05"）生成的串同一时刻，
	// 这是「Go 写入字符串 vs 库里 NOW()」比较能成立的前提
	if wantLiteral := processNow.Format("2006-01-02 15:04:05"); literalSkewed(formatted, wantLiteral, 90*time.Second) {
		t.Fatalf("库侧 NOW() 字面量 %s 与进程写入格式 %s 不同口径：数据库容器时区与测试进程不一致（CI mysql service 需配 TZ，与生产 TZ=Asia/Shanghai 对齐），否则 expire_at > NOW() 这类库里比较会整体偏移",
			formatted, wantLiteral)
	}
	// 绝对时刻差：驱动按 DSN loc 解释后的瞬时应与进程瞬时吻合
	if diff := processNow.Sub(dbNow); diff < -90*time.Second || diff > 90*time.Second {
		t.Fatalf("库侧 NOW()=%s 与进程 now=%s 相差 %s（超过 90s 容差），DSN loc 或数据库时区配置已漂移",
			dbNow.Format(time.RFC3339), processNow.Format(time.RFC3339), diff)
	}
}

// literalSkewed 判断两个 "2006-01-02 15:04:05" 墙上时间串是否相差超过容差
// 解析失败按「未偏移」返回 false：上面的 NOW() 读取本身已成功，字面量格式异常
// 不该被读成时区问题（真出问题由调用方的第二个断言兜住）
//
// 参数：
//   - a, b: 待比较的时间串
//   - tol: 容差
//
// 返回值：
//   - bool: 确有偏移
func literalSkewed(a, b string, tol time.Duration) bool {
	layout := "2006-01-02 15:04:05"
	ta, err1 := time.ParseInLocation(layout, a, time.Local)
	tb, err2 := time.ParseInLocation(layout, b, time.Local)
	if err1 != nil || err2 != nil {
		return false
	}
	diff := ta.Sub(tb)
	return diff < -tol || diff > tol
}
