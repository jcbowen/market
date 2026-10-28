package config

import (
	"strings"
	"testing"
)

/**
 * debugger 相关环境变量覆盖测试（离线）
 *
 * 运行方式：
 *   go test ./common/config/ -run TestApplyEnvOverrides -v
 *
 * 覆盖：
 *   - DEBUGGER_ALLOWED_IPS 覆盖配置文件白名单，解析风格与 TRUSTED_PROXIES 一致
 *   - DEBUGGER_ENABLED 优先于配置文件值，未设置时不改写
 *   - DEBUGGER_SERVICE_NAME 覆盖 debug_logs 来源标识（去空白），未设置时不改写
 *   - 逗号列表解析：去空白、丢空项
 *   - 外部 DB 拓扑七项：非空覆盖、空值逐项不改写；端口/库号整数校验（parseNumericEnv 正反用例）
 *   - EnsureSecureJWT：生产环境（APP_ENV=production）无视 JWT_ALLOW_INSECURE_DEV 一律 fail-fast
 */

// TestApplyEnvOverridesDebuggerIPs DEBUGGER_ALLOWED_IPS 覆盖配置文件里的白名单（M2）
func TestApplyEnvOverridesDebuggerIPs(t *testing.T) {
	cfg := &Config{}
	cfg.Debugger.AllowedIPs = []string{"10.0.0.1"}

	t.Setenv("DEBUGGER_ALLOWED_IPS", "127.0.0.1, 192.168.1.0/24 ,")
	ApplyEnvOverrides(cfg)

	want := []string{"127.0.0.1", "192.168.1.0/24"}
	if len(cfg.Debugger.AllowedIPs) != len(want) {
		t.Fatalf("白名单应被环境变量覆盖为 %v，实际 %v", want, cfg.Debugger.AllowedIPs)
	}
	for i := range want {
		if cfg.Debugger.AllowedIPs[i] != want[i] {
			t.Errorf("第 %d 项应为 %s，实际 %s", i, want[i], cfg.Debugger.AllowedIPs[i])
		}
	}
}

// TestApplyEnvOverridesDebuggerEnabledWinsOverFile DEBUGGER_ENABLED 优先于配置文件值
func TestApplyEnvOverridesDebuggerEnabledWinsOverFile(t *testing.T) {
	cfg := &Config{}
	cfg.Debugger.Enabled = true

	t.Setenv("DEBUGGER_ENABLED", "false")
	ApplyEnvOverrides(cfg)
	if cfg.Debugger.Enabled {
		t.Error("DEBUGGER_ENABLED=false 应关闭 debugger")
	}

	t.Setenv("DEBUGGER_ENABLED", "1")
	ApplyEnvOverrides(cfg)
	if !cfg.Debugger.Enabled {
		t.Error("DEBUGGER_ENABLED=1 应开启 debugger")
	}

	// 空值等同未设置，保持配置文件值
	cfg.Debugger.Enabled = true
	t.Setenv("DEBUGGER_ENABLED", "")
	ApplyEnvOverrides(cfg)
	if !cfg.Debugger.Enabled {
		t.Error("DEBUGGER_ENABLED 为空时不应改写配置文件值")
	}
}

// TestApplyEnvOverridesDebuggerServiceName DEBUGGER_SERVICE_NAME 覆盖 debug_logs 来源标识
func TestApplyEnvOverridesDebuggerServiceName(t *testing.T) {
	cfg := &Config{}
	cfg.Debugger.ServiceName = "gateway"

	// 覆盖并去空白（compose 按进程名注入 payment-service/order-service）
	t.Setenv("DEBUGGER_SERVICE_NAME", " payment-service ")
	ApplyEnvOverrides(cfg)
	if cfg.Debugger.ServiceName != "payment-service" {
		t.Errorf("DEBUGGER_SERVICE_NAME 应覆盖来源标识（去空白），实际 %q", cfg.Debugger.ServiceName)
	}

	// 空值等同未设置，保持配置文件值
	cfg.Debugger.ServiceName = "gateway"
	t.Setenv("DEBUGGER_SERVICE_NAME", "")
	ApplyEnvOverrides(cfg)
	if cfg.Debugger.ServiceName != "gateway" {
		t.Errorf("DEBUGGER_SERVICE_NAME 为空时不应改写配置文件值，实际 %q", cfg.Debugger.ServiceName)
	}
}

// TestApplyEnvOverridesDbTopology 外部数据库连接拓扑六项：非空覆盖、留空不改写
func TestApplyEnvOverridesDbTopology(t *testing.T) {
	cfg := &Config{}
	cfg.Db.Host = "mysql"
	cfg.Db.Port = "3306"
	cfg.Db.Dbname = "market"
	cfg.Db.Username = "root"
	cfg.Redis.Host = "redis"
	cfg.Redis.Port = "6379"
	cfg.Redis.Db = "0"

	// 非空时覆盖配置文件值（external-db compose 注入口径）
	t.Setenv("DB_HOST", "db.example.internal")
	t.Setenv("DB_PORT", "3307")
	t.Setenv("DB_NAME", "market_prod")
	t.Setenv("DB_USER", "market_rw")
	t.Setenv("REDIS_HOST", "cache.example.internal")
	t.Setenv("REDIS_PORT", "6380")
	t.Setenv("REDIS_DB", "2")
	ApplyEnvOverrides(cfg)

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"DB_HOST", cfg.Db.Host, "db.example.internal"},
		{"DB_PORT", cfg.Db.Port, "3307"},
		{"DB_NAME", cfg.Db.Dbname, "market_prod"},
		{"DB_USER", cfg.Db.Username, "market_rw"},
		{"REDIS_HOST", cfg.Redis.Host, "cache.example.internal"},
		{"REDIS_PORT", cfg.Redis.Port, "6380"},
		{"REDIS_DB", cfg.Redis.Db, "2"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s 应覆盖为 %q，实际 %q", c.name, c.want, c.got)
		}
	}

	// 空值等同未设置：全部 7 项逐一断言保持配置文件值（内部 compose 部署不设这些变量的口径；
	// compose 以 ${VAR:-} 注入空串属常态，空串若报错/覆盖即破坏「留空沿用配置」契约）
	t.Setenv("DB_HOST", "")
	t.Setenv("DB_PORT", "")
	t.Setenv("DB_NAME", "")
	t.Setenv("DB_USER", "")
	t.Setenv("REDIS_HOST", "")
	t.Setenv("REDIS_PORT", "")
	t.Setenv("REDIS_DB", "")
	ApplyEnvOverrides(cfg)
	emptyChecks := []struct {
		name string
		got  string
		want string
	}{
		{"DB_HOST 为空", cfg.Db.Host, "db.example.internal"},
		{"DB_PORT 为空", cfg.Db.Port, "3307"},
		{"DB_NAME 为空", cfg.Db.Dbname, "market_prod"},
		{"DB_USER 为空", cfg.Db.Username, "market_rw"},
		{"REDIS_HOST 为空", cfg.Redis.Host, "cache.example.internal"},
		{"REDIS_PORT 为空", cfg.Redis.Port, "6380"},
		{"REDIS_DB 为空", cfg.Redis.Db, "2"},
	}
	for _, c := range emptyChecks {
		if c.got != c.want {
			t.Errorf("%s 时不应改写配置文件值，期望保持 %q，实际 %q", c.name, c.want, c.got)
		}
	}
}

// TestParseNumericEnv 端口/库号类环境变量的整数校验：合法值通过、非法值报错且带变量名
// 非法口径：非纯整数（夹带空白、写成 tcp、带引号）或超出区间；空串不进入本函数
// （调用方 applyNumericEnvOverride 已短路为「沿用配置文件」，见拓扑用例的空值阶段）
func TestParseNumericEnv(t *testing.T) {
	// 正例：区间内的纯整数原样解析
	for _, c := range []struct {
		key  string
		val  string
		min  int
		max  int
		want int
	}{
		{"DB_PORT", "3306", minPort, maxPort, 3306},
		{"DB_PORT", "1", minPort, maxPort, 1},
		{"DB_PORT", "65535", minPort, maxPort, 65535},
		{"REDIS_PORT", "6379", minPort, maxPort, 6379},
		{"REDIS_DB", "0", minRedisDB, maxRedisDB, 0},
		{"REDIS_DB", "2", minRedisDB, maxRedisDB, 2},
	} {
		got, err := parseNumericEnv(c.key, c.val, c.min, c.max)
		if err != nil {
			t.Errorf("parseNumericEnv(%s, %q) 不应报错，实际 %v", c.key, c.val, err)
		}
		if got != c.want {
			t.Errorf("parseNumericEnv(%s, %q) = %d，期望 %d", c.key, c.val, got, c.want)
		}
	}

	// 反例：伪装成「网络不可达」的经典误配，必须报出变量名而非透传
	for _, c := range []struct {
		key string
		val string
		min int
		max int
	}{
		{"DB_PORT", "3306 ", minPort, maxPort},       // 夹带尾随空格
		{"DB_PORT", " tcp", minPort, maxPort},        // 把协议名写进端口
		{"DB_PORT", "tcp", minPort, maxPort},         // 同上
		{"REDIS_PORT", "6379\t", minPort, maxPort},   // 夹带制表符
		{"DB_PORT", "0", minPort, maxPort},           // 端口下界外
		{"DB_PORT", "70000", minPort, maxPort},       // 端口上界外
		{"REDIS_DB", "-1", minRedisDB, maxRedisDB},   // 库号不能为负
		{"REDIS_DB", "6379", minRedisDB, maxRedisDB}, // 把端口误填进库号
	} {
		_, err := parseNumericEnv(c.key, c.val, c.min, c.max)
		if err == nil {
			t.Errorf("parseNumericEnv(%s, %q) 应拒绝非法值，实际通过", c.key, c.val)
			continue
		}
		if !strings.Contains(err.Error(), c.key) {
			t.Errorf("parseNumericEnv(%s, %q) 错误文案应包含变量名，实际 %v", c.key, c.val, err)
		}
	}
}

// TestApplyEnvOverridesValidPortNormalize 合法端口经校验后规范化覆盖（走生产读取的真实变量名）
func TestApplyEnvOverridesValidPortNormalize(t *testing.T) {
	cfg := &Config{}
	cfg.Db.Port = "3306"
	cfg.Redis.Port = "6379"
	cfg.Redis.Db = "0"

	t.Setenv("DB_PORT", "3307")
	t.Setenv("REDIS_PORT", "6380")
	t.Setenv("REDIS_DB", "3")
	ApplyEnvOverrides(cfg)

	if cfg.Db.Port != "3307" || cfg.Redis.Port != "6380" || cfg.Redis.Db != "3" {
		t.Errorf("合法数值应校验后覆盖：db.port=%q redis.port=%q redis.db=%q",
			cfg.Db.Port, cfg.Redis.Port, cfg.Redis.Db)
	}
}

// TestApplyEnvOverridesTrustedProxiesStillWorks 复用逗号解析后可信代理网段行为不变
func TestApplyEnvOverridesTrustedProxiesStillWorks(t *testing.T) {
	cfg := &Config{}
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1/32, ::1")
	ApplyEnvOverrides(cfg)

	want := []string{"127.0.0.1/32", "::1"}
	if len(cfg.Security.TrustedProxies) != len(want) {
		t.Fatalf("可信代理应为 %v，实际 %v", want, cfg.Security.TrustedProxies)
	}
	for i := range want {
		if cfg.Security.TrustedProxies[i] != want[i] {
			t.Errorf("第 %d 项应为 %s，实际 %s", i, want[i], cfg.Security.TrustedProxies[i])
		}
	}
}

// TestApplyEnvOverridesApiRateLimit 接口 IP 洪水兜底阈值的环境变量覆盖：
// 空串保持原值（compose 用 ${VAR:-} 注入空串属常态），正数覆盖，
// 非正数/非数字应当 fail-fast（此处只验证前两种，Fatal 路径会把测试进程打死无法断言）
func TestApplyEnvOverridesApiRateLimit(t *testing.T) {
	cfg := &Config{}
	cfg.Security.ApiRateQPS = 100
	cfg.Security.ApiRateBurst = 150

	t.Setenv("RATE_LIMIT_API_QPS", "")
	t.Setenv("RATE_LIMIT_API_BURST", "")
	ApplyEnvOverrides(cfg)
	if cfg.Security.ApiRateQPS != 100 || cfg.Security.ApiRateBurst != 150 {
		t.Errorf("空环境变量不应覆盖：实际 qps=%v burst=%v", cfg.Security.ApiRateQPS, cfg.Security.ApiRateBurst)
	}

	t.Setenv("RATE_LIMIT_API_QPS", "2000")
	t.Setenv("RATE_LIMIT_API_BURST", "3000.5")
	ApplyEnvOverrides(cfg)
	if cfg.Security.ApiRateQPS != 2000 {
		t.Errorf("RATE_LIMIT_API_QPS=2000 应覆盖为 2000，实际 %v", cfg.Security.ApiRateQPS)
	}
	if cfg.Security.ApiRateBurst != 3000.5 {
		t.Errorf("RATE_LIMIT_API_BURST=3000.5 应覆盖为 3000.5，实际 %v", cfg.Security.ApiRateBurst)
	}
}

// TestSplitCSVList 逗号列表解析：去空白、丢空项，全空白返回空切片
func TestSplitCSVList(t *testing.T) {
	got := splitCSVList(" a , b ")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("splitCSVList(\" a , b \") = %v，期望 [a b]", got)
	}
	if list := splitCSVList(",, ,"); len(list) != 0 {
		t.Errorf("全空白输入应返回空切片，实际 %v", list)
	}
	if list := splitCSVList(""); len(list) != 0 {
		t.Errorf("空输入应返回空切片，实际 %v", list)
	}
}

// TestEnsureSecureJWTProductionIgnoresAllowInsecure 生产闸门：APP_ENV=production 时
// 密钥为空/默认/过短一律拒绝启动，即使显式设了 JWT_ALLOW_INSECURE_DEV=true 也无效
// （默认占位密钥是公开值，放行等于任何人可伪造超管上线）；错误文案须带变量名
func TestEnsureSecureJWTProductionIgnoresAllowInsecure(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv(EnvAllowInsecureJWT, "true")

	cfg := &Config{}
	cfg.JWT.Secret = DefaultJWTSecret
	if err := EnsureSecureJWT(cfg); err == nil {
		t.Fatal("production + 默认占位密钥 + 放行开关应拒绝启动，实际放行")
	} else if !strings.Contains(err.Error(), EnvAllowInsecureJWT) {
		t.Errorf("拒绝启动的错误文案应包含变量名 %s，实际: %v", EnvAllowInsecureJWT, err)
	}

	// 空密钥与过短密钥同属不安全口径，生产下一视同仁拒绝
	cfg.JWT.Secret = ""
	if err := EnsureSecureJWT(cfg); err == nil {
		t.Error("production + 空密钥应拒绝启动")
	}
	cfg.JWT.Secret = "short-secret"
	if err := EnsureSecureJWT(cfg); err == nil {
		t.Error("production + 过短密钥应拒绝启动")
	}

	// 生产环境注入合格强密钥应放行（闸门只管不安全值，不拦正常部署）
	cfg.JWT.Secret = strings.Repeat("k", minJWTSecretLen)
	if err := EnsureSecureJWT(cfg); err != nil {
		t.Errorf("production + 32 位强密钥应通过，实际 %v", err)
	}
}

// TestEnsureSecureJWTDevAllowInsecure 非生产环境保留零配置放行：
// 不安全密钥 + JWT_ALLOW_INSECURE_DEV=true 可启动（仅告警），不设开关则拒绝
func TestEnsureSecureJWTDevAllowInsecure(t *testing.T) {
	t.Setenv("APP_ENV", "")
	cfg := &Config{}
	cfg.JWT.Secret = DefaultJWTSecret

	t.Setenv(EnvAllowInsecureJWT, "true")
	if err := EnsureSecureJWT(cfg); err != nil {
		t.Errorf("非生产 + 放行开关应可用，实际 %v", err)
	}

	t.Setenv(EnvAllowInsecureJWT, "")
	if err := EnsureSecureJWT(cfg); err == nil {
		t.Error("非生产未设放行开关时，默认占位密钥应拒绝启动")
	}
}

// TestApplyEnvOverridesPasswordRSAKey 登录密码加密私钥两类 ENV 的覆盖与互斥语义：
// 内联 PEM 设值须清空文件路径配置（防模板写死的默认路径抢占 ENV 注入），
// 文件路径 ENV 覆盖配置文件值且去首尾空白；两 ENV 同设时文件路径优先（后处理覆盖）
func TestApplyEnvOverridesPasswordRSAKey(t *testing.T) {
	// ENV 内联：覆盖内联字段、\n 转义还原、清空配置文件里的文件路径
	cfg := &Config{}
	cfg.Security.PasswordRSAKeyFile = "/app/data/ssl/pwd_rsa.pem"
	t.Setenv("PASSWORD_RSA_PRIVATE_KEY", `-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY-----`)
	ApplyEnvOverrides(cfg)
	if cfg.Security.PasswordRSAKeyFile != "" {
		t.Errorf("ENV 内联设值时应清空文件路径配置，实际 %q", cfg.Security.PasswordRSAKeyFile)
	}
	if !strings.Contains(cfg.Security.PasswordRSAPrivateKey, "\n") {
		t.Error("PEM 的 \\n 字面转义应还原为真实换行")
	}
	// 清掉内联，避免影响后续断言
	t.Setenv("PASSWORD_RSA_PRIVATE_KEY", "")

	// ENV 文件路径：覆盖配置文件值并去空白；此时代理内联未设，文件路径生效
	cfg2 := &Config{}
	cfg2.Security.PasswordRSAKeyFile = "/old/path.pem"
	t.Setenv("PASSWORD_RSA_PRIVATE_KEY_FILE", " /app/data/ssl/pwd_rsa.pem ")
	ApplyEnvOverrides(cfg2)
	if cfg2.Security.PasswordRSAKeyFile != "/app/data/ssl/pwd_rsa.pem" {
		t.Errorf("ENV 文件路径应覆盖配置值并去空白，实际 %q", cfg2.Security.PasswordRSAKeyFile)
	}
	if cfg2.Security.PasswordRSAPrivateKey != "" {
		t.Errorf("未设内联 ENV 时不应改写内联字段，实际 %q", cfg2.Security.PasswordRSAPrivateKey)
	}
}
