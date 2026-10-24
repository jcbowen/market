package handler

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/response"
)

/**
 * 密码传输加密（RSA + PKCS#1 v1.5）
 *
 * 背景：登录/改密类接口的密码字段以 JSON 明文传输，安全完全依赖 HTTPS；
 * 等保合规与纵深防御要求即便 TLS 配置失误，链路抓包也不可见密码明文。
 *
 * 协议约定：
 *   1. 前端登录前调 GET /api/v1/admin/auth/pubkey 拿 { key_id, public_key, alg }（内存缓存，不持久化）
 *   2. 密码字段以 RSA-PKCS1-V1_5 加密后的 base64 密文提交，并附带声明字段 pwd_alg / pwd_key
 *   3. 网关按 pwd_key 匹配本机私钥解密；未带声明字段时按明文兼容（存量脚本/测试不受影响）
 *   4. 解密失败或 key_id 不匹配返回 11010，前端重拉公钥后重试一次（覆盖轮换窗口）
 *
 * 密钥管理：
 *   - 该密钥对不是 TLS 证书，无有效期与身份绑定概念，公钥公开无妨，无需客户端校验
 *   - 私钥来源优先级：PASSWORD_RSA_PRIVATE_KEY（ENV 内联）> PASSWORD_RSA_PRIVATE_KEY_FILE（ENV 路径）
 *     > security.password_rsa_key_file（配置路径）> security.password_rsa_private_key（配置内联）
 *     > 均空时自动生成临时密钥对（单实例零配置可用）
 *   - 路径方式（推荐）：文件存在直接加载；不存在自动生成并落盘（0600）——路径指向挂载目录
 *     即实现持久化与多实例共享，容器重建不换钥；落盘失败降级内存密钥并告警
 */

// PasswordEncryptAlg 密码加密算法标识（请求 pwd_alg 字段与 pubkey 接口 alg 出参共用）
// 标注填充方式：RSA + PKCS#1 v1.5（jsencrypt 默认输出，Go rsa.DecryptPKCS1v15 对应解密）
const PasswordEncryptAlg = "RSA-PKCS1-V1_5"

// maxRSAPasswordLen 解密明文的长度上限（RSA-2048 + PKCS#1 v1.5 单块明文上限 = 模长 256 - 填充开销 11）
const maxRSAPasswordLen = 245

// passwordCrypto 密码传输加密器
// 持有网关本机的 RSA 私钥与派生指纹，随 AdminHandler 生命周期常驻
type passwordCrypto struct {
	// key 私钥（解密用，绝不出网关）
	key *rsa.PrivateKey
	// publicKeyPEM 公钥 PEM（SPKI 格式，pubkey 接口下发、jsencrypt 直接可用）
	publicKeyPEM string
	// keyID 私钥指纹（SHA-256 前 8 字节 hex）：随密文上送，网关据此识别「密文是给谁的」，
	// 多实例/轮换场景下不匹配即返回 11010，前端重拉公钥重试
	keyID string
}

// newPasswordCrypto 创建密码传输加密器
// 私钥按优先级取用（详见模块头「密钥管理」）：显式配置的私钥格式非法一律 fail-fast 拒绝启动——
// 带病运行会让前端拿到的公钥与实际解密能力不一致，问题排查远比启动失败痛苦
//
// 参数：
//   - cfg: 全局配置（读取 security.password_rsa_key_file / security.password_rsa_private_key，
//     已经过 ApplyEnvOverrides 的 ENV 覆盖）
//
// 返回值：
//   - *passwordCrypto: 加密器实例
func newPasswordCrypto(cfg *config.Config) *passwordCrypto {
	if path := strings.TrimSpace(cfg.Security.PasswordRSAKeyFile); path != "" {
		key, err := loadOrCreateKeyFile(path)
		if err != nil {
			log.Fatalf("配置错误: security.password_rsa_key_file: %v，拒绝启动", err)
		}
		return newPasswordCryptoStruct(key)
	}

	if pemKey := strings.TrimSpace(cfg.Security.PasswordRSAPrivateKey); pemKey != "" {
		key, err := parseRSAPrivateKey([]byte(pemKey))
		if err != nil {
			log.Fatalf("配置错误: security.password_rsa_private_key 非法: %v，拒绝启动", err)
		}
		log.Printf("密码传输加密：已加载配置的 RSA 私钥（key_id=%s）", fingerprint(key))
		return newPasswordCryptoStruct(key)
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("密码传输加密：自动生成临时 RSA 密钥对失败: %v", err)
	}
	log.Printf("警告: 未配置 security.password_rsa_key_file，已自动生成临时密钥对（key_id=%s），"+
		"重启即更换（单实例无影响，前端每次进登录页实时拉取公钥）；多实例/负载均衡部署必须显式配置同一私钥，"+
		"否则登录随机解密失败", fingerprint(key))
	return newPasswordCryptoStruct(key)
}

// loadOrCreateKeyFile 从指定路径加载 RSA 私钥；文件不存在时自动生成并落盘持久化
// 落盘后重启读到同一文件，key_id 恒定（解决内存临时密钥「重启即换」）；
// 多实例挂载同一宿主文件即共享同一密钥对。多实例同时首启存在生成竞争
// （各自生成、后写者胜），严格场景应预生成文件后挂载，此处不做文件锁
//
// 参数：
//   - path: PEM 私钥文件路径（PKCS#8/PKCS#1 均可读回）
//
// 返回值：
//   - *rsa.PrivateKey: 加载或新生成的私钥
//   - error: 仅 fail-fast 场景（文件读取失败、内容非法、密钥生成失败）；
//     「目录不可写导致落盘失败」不视为错误——降级内存密钥并告警，保持网关可用
func loadOrCreateKeyFile(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		key, perr := parseRSAPrivateKey(data)
		if perr != nil {
			return nil, fmt.Errorf("私钥文件 %s 内容非法: %w", path, perr)
		}
		log.Printf("密码传输加密：已加载 RSA 私钥文件 %s（key_id=%s）", path, fingerprint(key))
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取私钥文件 %s 失败: %w", path, err)
	}

	key, genErr := rsa.GenerateKey(rand.Reader, 2048)
	if genErr != nil {
		return nil, fmt.Errorf("生成 RSA 密钥对失败: %w", genErr)
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
		log.Printf("警告: 私钥目录 %s 创建失败: %v，本次使用内存密钥（重启更换）", filepath.Dir(path), mkErr)
		return key, nil
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	if wErr := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); wErr != nil {
		log.Printf("警告: 私钥写入 %s 失败: %v，本次使用内存密钥（重启更换）；"+
			"多实例部署各实例将持有不同密钥，请检查挂载目录权限后重启", path, wErr)
		return key, nil
	}
	log.Printf("密码传输加密：已生成 RSA 私钥并落盘 %s（key_id=%s）", path, fingerprint(key))
	return key, nil
}

// newPasswordCryptoStruct 构造辅助（Go 无私有构造器，集中初始化派生字段避免多处拼装不一致）
//
// 参数：
//   - key: 已就绪的 RSA 私钥
//
// 返回值：
//   - *passwordCrypto: 加密器实例
func newPasswordCryptoStruct(key *rsa.PrivateKey) *passwordCrypto {
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		// 内存中刚生成的标准 RSA 公钥不存在序列化失败路径，仅防御性兜底
		log.Fatalf("密码传输加密：公钥序列化失败: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	return &passwordCrypto{
		key:          key,
		publicKeyPEM: string(pubPEM),
		keyID:        fingerprint(key),
	}
}

// fingerprint 计算私钥指纹（SHA-256(N×E) 前 8 字节 hex）
// 取模数与公钥指数的确定性序列化做哈希：同一密钥对无论以 PKCS#8 还是 PKCS#1 编码
// 注入，指纹恒一致，key_id 语义稳定
//
// 参数：
//   - key: RSA 私钥
//
// 返回值：
//   - string: 16 位 hex 指纹（作为 key_id 随密文上送）
func fingerprint(key *rsa.PrivateKey) string {
	h := sha256.New()
	h.Write(key.N.Bytes())
	h.Write(big.NewInt(int64(key.PublicKey.E)).Bytes())
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// parseRSAPrivateKey 解析 PEM 编码的 RSA 私钥（PKCS#8 优先，兼容 PKCS#1）
// openssl genrsa 新旧版本、各类云密钥服务的导出格式不一，双格式兼容降低接入成本
//
// 参数：
//   - data: PEM 字节流
//
// 返回值：
//   - *rsa.PrivateKey: 解析成功的私钥
//   - error: PEM 非法、非 RSA 密钥或格式不支持
func parseRSAPrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("不是有效的 PEM 格式")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("PEM 内容不是 RSA 私钥")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("仅支持 PKCS#8 / PKCS#1 格式的 RSA 私钥")
}

// GetPasswordPubkey 下发密码传输加密公钥（公开接口，免登录）
// 前端每次进登录页实时拉取（内存缓存，不持久化），服务端密钥轮换后刷新页面即无感切换
//
// 参数：
//   - c: gin 上下文
func (h *AdminHandler) GetPasswordPubkey(c *gin.Context) {
	response.Success(c, gin.H{
		"key_id":     h.pwdCrypto.keyID,
		"public_key": h.pwdCrypto.publicKeyPEM,
		"alg":        PasswordEncryptAlg,
	})
}

// decryptPwdField 按请求声明解密密码字段（原地覆写）
// 请求未声明 pwd_alg/pwd_key 时原样返回（明文兼容通道，存量脚本与未迁移测试不受影响）；
// 已声明时校验算法与 key_id → base64 解码 → RSA-PKCS1-v1.5 解密 → 覆写目标字段。
// 任一环节失败直接写响应（11010）并返回 false，调用方立即 return。
// 密码字段本身为空时跳过（对应 CreateAdmin 的「复用账号可不传密码」场景）
//
// 参数：
//   - c: gin 上下文（失败时写响应）
//   - field: 待解密的密码字段指针（原地覆写为明文）
//   - alg: 请求声明的算法标识（空串 = 未声明）
//   - keyID: 请求声明的密钥指纹（空串 = 未声明）
//
// 返回值：
//   - bool: true = 解析完成（含明文兼容），继续业务；false = 已写响应，调用方 return
func (h *AdminHandler) decryptPwdField(c *gin.Context, field *string, alg, keyID string) bool {
	// 未声明 → 明文兼容通道（全环境保留，产品口径）：密码明文过网仅剩 TLS 单层保护，
	// 每次记录 WARN 观测降级流量——正常前端已全走加密链路，持续出现说明存在未迁移的
	// 第三方脚本，或 pubkey 接口被干扰导致前端降级（后者属攻击信号，需结合限流排查）
	if alg == "" && keyID == "" {
		log.Printf("警告: 密码接口收到未声明加密的明文请求 client_ip=%s path=%s（明文兼容通道）",
			c.ClientIP(), c.FullPath())
		return true
	}
	// 空密码 + 声明加密：视为「未传密码」，交由后续业务校验（避免为空值跑解密链路）
	if *field == "" {
		return true
	}
	if alg != PasswordEncryptAlg || keyID != h.pwdCrypto.keyID {
		// 算法不认识或密钥已轮换：同一错误码收口，前端重拉公钥重试即可覆盖两种场景
		response.Error(c, errcode.PasswordEncryptedError)
		return false
	}
	cipherBytes, err := base64.StdEncoding.DecodeString(*field)
	if err != nil {
		response.Error(c, errcode.PasswordEncryptedError)
		return false
	}
	// rand 是 legacy 占位参数：新版标准库忽略该值（内部防护无条件启用），传 nil 行为等价、零开销；
	// 仍传 rand.Reader 是兼容旧工具链语义（旧版本该参数控制 RSA 盲化，传值才启用防护），任意版本下均安全
	plain, err := rsa.DecryptPKCS1v15(rand.Reader, h.pwdCrypto.key, cipherBytes)
	if err != nil {
		response.Error(c, errcode.PasswordEncryptedError)
		return false
	}
	// 解密明文补长度约束：请求结构体上的 binding min=6 作用在密文（恒 344 字符）上必然通过，
	// 不在此补校验则加密通道可绕过长度限制设置超短密码（改密/重置链路服务层无二次校验）。
	// 下限 6 与创建路径的明文校验对齐（存量密码均 ≥6，登录场景无额外影响）；
	// 上限 245 为 RSA-2048/PKCS1v1.5 单块明文上限，越界密文本就无法成立
	if l := len(plain); l < 6 || l > maxRSAPasswordLen {
		response.Error(c, errcode.PasswordEncryptedError)
		return false
	}
	*field = string(plain)
	return true
}
