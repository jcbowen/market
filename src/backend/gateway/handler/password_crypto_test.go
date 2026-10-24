package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/config"
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

// TestDecryptPwdFieldPlaintextLengthGuard 解密明文的长度校验：
// binding min=6 作用在恒 344 字符的密文上必然通过，加密通道不得借此绕过密码长度限制
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
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/probe", nil)
		field := base64.StdEncoding.EncodeToString(cipher)
		return h.decryptPwdField(c, &field, PasswordEncryptAlg, h.pwdCrypto.keyID)
	}

	if tryDecrypt("abc12") {
		t.Error("5 位密码密文应被拒绝（加密通道不得绕过 min=6 长度校验）")
	}
	if !tryDecrypt("abc123") {
		t.Error("6 位合法密码密文应解密放行")
	}
	// 明文兼容通道（未声明 pwd_alg/pwd_key）不做长度拦截，行为与一期一致
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/probe", nil)
	plainField := "abc"
	if !h.decryptPwdField(c, &plainField, "", "") || plainField != "abc" {
		t.Error("未声明加密时应走明文兼容通道原样放行")
	}
}
