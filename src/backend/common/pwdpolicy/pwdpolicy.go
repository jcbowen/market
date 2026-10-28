// Package pwdpolicy 后台账号口令长度策略（单一口径）
//
// 网关 binding 标签、网关解密后的明文校验、user 服务层兜底校验与
// market-admin 初始化 CLI 都引用本包，避免同一数字散落四处互相漂移。
package pwdpolicy

import "fmt"

// MinLength 口令最小长度（字节）。
// 后台账号是资金与库存的入口，6 位在本世纪已属可在线爆破的强度
const MinLength = 10

// MaxLength 口令最大长度（字节），与 RSA-2048/OAEP-SHA256 单块明文上限一致。
// 超出该上限的口令无法经加密通道上送，等于账号锁死，故写入侧必须同批拦下
const MaxLength = 190

// TooShort 判断口令是否短于下限（空串同样判为过短，由调用方决定文案）
//
// 参数：
//   - plain: 口令明文
//
// 返回值：
//   - bool: true = 不满足长度要求
func TooShort(plain string) bool {
	return len(plain) < MinLength
}

// TooLong 判断口令是否超出加密通道单块上限
//
// 参数：
//   - plain: 口令明文
//
// 返回值：
//   - bool: true = 过长
func TooLong(plain string) bool {
	return len(plain) > MaxLength
}

// Message 返回长度要求文案（与 MinLength/MaxLength 同源）
//
// 返回值：
//   - string: 形如「密码长度需为 10-190 字节」
func Message() string {
	return fmt.Sprintf("密码长度需为 %d-%d 字节", MinLength, MaxLength)
}
