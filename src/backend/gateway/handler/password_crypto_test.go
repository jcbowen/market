package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/pwdpolicy"
)

/**
 * 密码传输加密私钥加载的离线单元测试（不连库不连网）
 *
 * 运行方式：go test ./gateway/handler/ -run TestPasswordKey -v
 *
 * 覆盖（对应二期「私钥文件化」）：
 *   - 文件存在 → 直接加载，重复加载 key_id 稳定（重启幂等语义）
 *   - 文件不存在 → 自动生成并落盘（0600、父目录自动创建），重读同一密钥
 *   - 文件内容非法 / 路径不可读 → 返回错误（调用方 fail-fast 拒绝启动）
 *   - keyFile 与内联私钥同时配置 → 文件路径优先
 */

// writeTestKeyPEM 生成一把测试 RSA 私钥并返回其 PKCS#1 PEM 编码
//
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - []byte: PEM 编码的私钥
//   - *rsa.PrivateKey: 原始私钥（用于 key_id 断言）
func writeTestKeyPEM(t *testing.T) ([]byte, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成测试密钥失败: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return pem.EncodeToMemory(block), key
}

// TestPasswordKeyFileLoadsExisting 已有文件直接加载且幂等
//
// 参数：
//   - t: 测试对象
func TestPasswordKeyFileLoadsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pwd_rsa.pem")
	pemBytes, key := writeTestKeyPEM(t)
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("准备测试文件失败: %v", err)
	}

	got, err := loadOrCreateKeyFile(path)
	if err != nil {
		t.Fatalf("已有文件应加载成功: %v", err)
	}
	if got.N.Cmp(key.N) != 0 {
		t.Fatal("加载的密钥应与文件内容一致")
	}

	// 二次加载走「读文件」路径，key_id 必须一致（重启读同一文件的幂等语义）
	got2, err := loadOrCreateKeyFile(path)
	if err != nil {
		t.Fatalf("二次加载应成功: %v", err)
	}
	if fingerprint(got) != fingerprint(got2) {
		t.Fatal("同一文件的两次加载 key_id 应一致")
	}
}

// TestPasswordKeyFileGeneratesAndPersists 文件缺失时生成落盘且重读幂等
//
// 参数：
//   - t: 测试对象
func TestPasswordKeyFileGeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	// 嵌套路径：父目录不存在也应自动创建（对应容器内挂载点下首次生成场景）
	path := filepath.Join(dir, "nested", "pwd_rsa.pem")

	got, err := loadOrCreateKeyFile(path)
	if err != nil {
		t.Fatalf("文件缺失应自动生成而非报错: %v", err)
	}
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("生成的私钥应已落盘: %v", err)
	}
	if runtime.GOOS != "windows" {
		if fi, statErr := os.Stat(path); statErr != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("私钥文件权限应为 0600, perm=%v err=%v", fi.Mode().Perm(), statErr)
		}
	}

	again, err2 := loadOrCreateKeyFile(path)
	if err2 != nil {
		t.Fatalf("落盘文件应可解析回读: %v", err2)
	}
	if fingerprint(again) != fingerprint(got) {
		t.Fatal("重读落盘文件 key_id 应一致（重启不换钥的核心保证）")
	}
	if fi, err := os.Stat(filepath.Dir(path)); err != nil || !fi.IsDir() {
		t.Fatalf("父目录应随生成过程自动创建, err=%v", err)
	}
}

// TestPasswordKeyFileInvalidContentFails 文件内容非法返回错误（调用方 fail-fast）
//
// 参数：
//   - t: 测试对象
func TestPasswordKeyFileInvalidContentFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pwd_rsa.pem")
	if err := os.WriteFile(path, []byte("not a pem"), 0o600); err != nil {
		t.Fatalf("准备测试文件失败: %v", err)
	}
	if _, err := loadOrCreateKeyFile(path); err == nil {
		t.Fatal("垃圾内容应返回错误触发 fail-fast，而不是静默生成新钥覆盖旧文件")
	}
}

// TestPasswordKeyFileUnreadablePathFails 路径不可读（目录当文件读）返回错误
//
// 参数：
//   - t: 测试对象
func TestPasswordKeyFileUnreadablePathFails(t *testing.T) {
	dir := t.TempDir()
	// 读目录的错误类型非 NotExist：应 fail-fast，而不是误判「文件缺失」走生成落盘
	if _, err := loadOrCreateKeyFile(dir); err == nil {
		t.Fatal("把目录当私钥文件应返回错误")
	}
}

// TestPasswordKeyFileWinsOverInlineKey keyFile 与内联私钥同时配置时文件路径优先
//
// 参数：
//   - t: 测试对象
func TestPasswordKeyFileWinsOverInlineKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pwd_rsa.pem")
	filePEM, fileKey := writeTestKeyPEM(t)
	inlinePEM, _ := writeTestKeyPEM(t)
	if err := os.WriteFile(path, filePEM, 0o600); err != nil {
		t.Fatalf("准备测试文件失败: %v", err)
	}

	cfg := &config.Config{}
	cfg.Security.PasswordRSAKeyFile = path
	cfg.Security.PasswordRSAPrivateKey = string(inlinePEM)

	h := newPasswordCrypto(cfg)
	if h.keyID != fingerprint(fileKey) {
		t.Fatal("keyFile 非空时应加载文件密钥，内联配置让位")
	}
}

// newPwdProbeCtx 构造一个一次性测试上下文（响应写入 recorder，供断言错误码与文案）
//
// 返回值：
//   - *gin.Context: 测试上下文
//   - *httptest.ResponseRecorder: 响应记录器
func newPwdProbeCtx() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/probe", nil)
	return c, recorder
}

// TestDecryptPwdFieldPlaintextLengthGuard 口令长度的分层校验（C4）
// 传输层（decryptPwdField）只判上限：binding min 作用在恒 344 字符的密文上必然通过，
// 上限不补就会设出加密通道解不出来的超长口令（账号锁死）；
// 下限不在传输层判——登录与写入口共用本函数，按新下限拦截会让 6-9 位历史口令登不进去；
// 下限由策略层 validateNewPassword 在写入口（新建/改密/重置）逐处判定
//
// 参数：
//   - t: 测试对象
func TestDecryptPwdFieldPlaintextLengthGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成测试密钥失败: %v", err)
	}
	h := &AdminHandler{pwdCrypto: newPasswordCryptoStruct(key)}

	// tryDecrypt 以给定明文走完整解密链路，返回是否放行
	tryDecrypt := func(plain string) bool {
		t.Helper()
		cipher, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &key.PublicKey, []byte(plain), nil)
		if err != nil {
			t.Fatalf("加密失败: %v", err)
		}
		c, _ := newPwdProbeCtx()
		field := base64.StdEncoding.EncodeToString(cipher)
		return h.decryptPwdField(c, &field, PasswordEncryptAlg, h.pwdCrypto.keyID)
	}

	// tryDecryptPlain 以给定明文走明文兼容通道（未声明 pwd_alg/pwd_key），返回是否放行及响应体
	tryDecryptPlain := func(plain string) (bool, string) {
		t.Helper()
		c, recorder := newPwdProbeCtx()
		field := plain
		return h.decryptPwdField(c, &field, "", ""), recorder.Body.String()
	}

	// 传输层不判下限：5 位明文可正常解密（由写入口的策略层拒绝，登录路径原样透传给 bcrypt）
	if !tryDecrypt("abc12") {
		t.Error("传输层不应判口令下限：短口令解密应成功，否则存量短口令账号无法登录")
	}
	// 上限边界：190 恰好是 RSA-2048 + OAEP-SHA256 单块可承载的最大明文
	// （256 - 2×32 - 2），再长连加密都失败、根本进不了解密分支——
	// 函数内 len(plain) > maxRSAPasswordLen 的判定是防御性的，越界路径改由明文兼容通道覆盖
	if !tryDecrypt(strings.Repeat("a", maxRSAPasswordLen)) {
		t.Errorf("%d 字节口令应解密放行（等于 RSA 单块上限）", maxRSAPasswordLen)
	}

	// 明文兼容通道不受 RSA 单块限制，是超长口令的真实入口
	if ok, _ := tryDecryptPlain("abc"); !ok {
		t.Error("未声明加密时应走明文兼容通道原样放行")
	}
	if ok, _ := tryDecryptPlain("abc12"); !ok {
		t.Error("明文兼容通道不应判口令下限（下限归写入口策略层）")
	}
	if ok, body := tryDecryptPlain(strings.Repeat("a", maxRSAPasswordLen+1)); ok {
		t.Errorf("明文通道超出 %d 字节上限的口令应被拒绝", maxRSAPasswordLen)
	} else if !strings.Contains(body, fmt.Sprintf("%d", maxRSAPasswordLen)) {
		t.Errorf("超长拒绝文案应带出上限值，body=%s", body)
	}
}

// TestValidateNewPasswordPolicy 写入口下限：低于 pwdpolicy.MinLength 拒绝，空串放行
// 空串放行对应「复用已有账号可不传密码」，必填与否由 binding 标签负责
//
// 参数：
//   - t: 测试对象
func TestValidateNewPasswordPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AdminHandler{}

	probe := func(plain string) (bool, string) {
		t.Helper()
		c, recorder := newPwdProbeCtx()
		return h.validateNewPassword(c, plain), recorder.Body.String()
	}

	if ok, _ := probe(""); !ok {
		t.Error("空口令不应由本函数判定（复用账号不传密码的场景应继续走业务校验）")
	}
	short := strings.Repeat("a", pwdpolicy.MinLength-1)
	ok, body := probe(short)
	if ok {
		t.Errorf("%d 位口令应被拒绝", len(short))
	}
	if !strings.Contains(body, fmt.Sprintf("%d", pwdpolicy.MinLength)) {
		t.Errorf("拒绝文案应带出长度要求，body=%s", body)
	}
	if ok, _ := probe(strings.Repeat("a", pwdpolicy.MinLength)); !ok {
		t.Errorf("%d 位口令应放行", pwdpolicy.MinLength)
	}
}
