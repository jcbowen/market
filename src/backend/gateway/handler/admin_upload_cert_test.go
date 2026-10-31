package handler

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
)

/**
 * 支付证书上传（UploadPaymentCert）请求体限流与转发口径测试（P2-5 网关收口）
 *
 * 背景：证书上传是后台里唯一「先读字段、后设上限」的 multipart 入口，
 * 一旦 body 被超大报文占满才想起限流，12MB 上限就形同虚设；
 * 修法是把 limitUploadBody 提到函数首行，解析中途不再重复包装
 * （MaxBytesReader 每次包装都会重置已读计数，等于把预算重发一遍）。
 *
 * 运行方式（无外部依赖）：
 *   go test ./gateway/handler/ -run 'TestUploadPaymentCert' -v
 *
 * 覆盖：
 *   - 正常小文件：config_id/cert_type/文件名/内容原样转发，凭证脱敏行回传；
 *   - 超过 12MB 上限：参数错误且 payment 服务零调用（限流确实先于解析生效）；
 *   - 缺 config_id：同样拒绝且不触达 payment 服务；
 *   - cert_type 缺省回落 private_key。
 */

// certUploadBodyMax 与生产常量对齐，用例里用它构造「刚好上限内 / 恰好超限」的报文
const certUploadBodyMax = maxUploadBytes

// stubCertUploadClient 支付服务证书上传桩
// 嵌入接口后其余方法无需实现（误调用会 panic，直接暴露测试缺陷）
type stubCertUploadClient struct {
	payment.PaymentServiceClient
	calls []*payment.UploadPayCertReq
	resp  *payment.UploadPayCertResp
	err   error
}

func (s *stubCertUploadClient) UploadPayCert(ctx context.Context, in *payment.UploadPayCertReq, opts ...grpc.CallOption) (*payment.UploadPayCertResp, error) {
	s.calls = append(s.calls, in)
	if s.err != nil {
		return nil, s.err
	}
	if s.resp != nil {
		return s.resp, nil
	}
	return &payment.UploadPayCertResp{Success: true}, nil
}

// newMultipartRequest 组装 multipart 上传请求
// 参数：
//   - fields: 普通表单字段（按顺序写入，config_id 置空表示不发送该字段）
//   - fileField: 文件段字段名（空则不带文件段）
//   - fileName: 文件名
//   - fileContent: 文件内容
//
// 返回值：
//   - *http.Request: 带正确 Content-Type（含 boundary）的请求
func newMultipartRequest(t *testing.T, fields map[string]string, fileField, fileName string, fileContent []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	// 固定字段顺序，避免 map 遍历无序导致「超限报文」的截断位置随机
	for _, key := range []string{"config_id", "cert_type"} {
		if v, ok := fields[key]; ok && v != "" {
			if err := w.WriteField(key, v); err != nil {
				t.Fatalf("写入表单字段 %s 失败: %v", key, err)
			}
		}
	}
	if fileField != "" {
		part, err := w.CreateFormFile(fileField, fileName)
		if err != nil {
			t.Fatalf("创建文件段失败: %v", err)
		}
		if _, err := part.Write(fileContent); err != nil {
			t.Fatalf("写入文件内容失败: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭 multipart writer 失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/payment/pay-config/cert", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// newCertTestContext 构造证书上传用的 gin 上下文
func newCertTestContext(t *testing.T, fields map[string]string, fileField, fileName string, fileContent []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = newMultipartRequest(t, fields, fileField, fileName, fileContent)
	return c, recorder
}

// TestUploadPaymentCertForwardsParsedFields 正常上传：字段与内容原样转发，并回传脱敏配置行
func TestUploadPaymentCertForwardsParsedFields(t *testing.T) {
	content := []byte("-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----")
	stub := &stubCertUploadClient{resp: &payment.UploadPayCertResp{
		Success: true,
		Row:     &payment.PayConfigRow{Id: 7, Name: "微信商户", Platform: "wechat", UpdatedAt: "2026-10-31 12:00:00"},
	}}
	h := &AdminHandler{paymentClient: stub}

	c, recorder := newCertTestContext(t, map[string]string{"config_id": "7", "cert_type": "apiclient_key"}, "file", "apiclient_key.pem", content)
	h.UploadPaymentCert(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP 状态不符: got=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if len(stub.calls) != 1 {
		t.Fatalf("证书上传应恰好转发一次: calls=%d", len(stub.calls))
	}
	got := stub.calls[0]
	if got.GetConfigId() != 7 {
		t.Errorf("config_id 不符: got=%d", got.GetConfigId())
	}
	if got.GetCertType() != "apiclient_key" {
		t.Errorf("cert_type 不符: got=%q", got.GetCertType())
	}
	if got.GetFilename() != "apiclient_key.pem" {
		t.Errorf("filename 不符: got=%q", got.GetFilename())
	}
	if !bytes.Equal(got.GetContent(), content) {
		t.Errorf("证书内容不符: got=%q", got.GetContent())
	}
	// 回传行携带最新 updated_at（乐观锁版本），前端据此免二次「已被其他操作更新」
	if !strings.Contains(recorder.Body.String(), "2026-10-31 12:00:00") {
		t.Errorf("响应应回传脱敏后的最新配置行: body=%q", recorder.Body.String())
	}
}

// TestUploadPaymentCertRejectsOversizedBody 超过 12MB 的报文必须在解析阶段就被拒，且零转发
func TestUploadPaymentCertRejectsOversizedBody(t *testing.T) {
	stub := &stubCertUploadClient{}
	h := &AdminHandler{paymentClient: stub}

	c, recorder := newCertTestContext(t, map[string]string{"config_id": "7"}, "file", "big.pem",
		bytes.Repeat([]byte("A"), certUploadBodyMax+1<<10))
	h.UploadPaymentCert(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("网关应仍以业务错误码响应而非 5xx: got=%d body=%q", recorder.Code, recorder.Body.String())
	}
	code, msg := decodeGatewayResponse(t, recorder)
	if code != errcode.ParamError {
		t.Errorf("超限报文应回参数错误: code=%d msg=%q", code, msg)
	}
	if !strings.Contains(msg, "12MB") {
		t.Errorf("超限提示应说明 12MB 上限: msg=%q", msg)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("超限报文不得转发给 payment 服务: calls=%d", len(stub.calls))
	}
}

// TestUploadPaymentCertRequiresConfigID 缺 config_id 时拒绝且不触达 payment 服务
func TestUploadPaymentCertRequiresConfigID(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]string
	}{
		{name: "完全不带 config_id", fields: map[string]string{}},
		{name: "config_id 为非数字", fields: map[string]string{"config_id": "abc"}},
		{name: "config_id 为 0", fields: map[string]string{"config_id": "0"}},
		{name: "config_id 为负", fields: map[string]string{"config_id": strconv.FormatInt(-3, 10)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubCertUploadClient{}
			h := &AdminHandler{paymentClient: stub}
			c, recorder := newCertTestContext(t, tc.fields, "file", "k.pem", []byte("content"))
			h.UploadPaymentCert(c)

			code, _ := decodeGatewayResponse(t, recorder)
			if code != errcode.ParamError {
				t.Errorf("应回参数错误: code=%d body=%q", code, recorder.Body.String())
			}
			if len(stub.calls) != 0 {
				t.Errorf("缺配置 ID 不得转发: calls=%d", len(stub.calls))
			}
		})
	}
}

// TestUploadPaymentCertDefaultsCertType cert_type 缺省回落 private_key，空文件段拒绝
func TestUploadPaymentCertDefaultsCertType(t *testing.T) {
	stub := &stubCertUploadClient{}
	h := &AdminHandler{paymentClient: stub}
	c, _ := newCertTestContext(t, map[string]string{"config_id": "3"}, "file", "k.pem", []byte("x"))
	h.UploadPaymentCert(c)
	if len(stub.calls) != 1 {
		t.Fatalf("应恰好转发一次: calls=%d", len(stub.calls))
	}
	if got := stub.calls[0].GetCertType(); got != "private_key" {
		t.Errorf("缺省 cert_type 应为 private_key: got=%q", got)
	}

	noFile := &stubCertUploadClient{}
	h2 := &AdminHandler{paymentClient: noFile}
	c2, recorder2 := newCertTestContext(t, map[string]string{"config_id": "3"}, "", "", nil)
	h2.UploadPaymentCert(c2)
	code, msg := decodeGatewayResponse(t, recorder2)
	if code != errcode.ParamError || !strings.Contains(msg, "证书文件") {
		t.Errorf("缺文件段应回参数错误: code=%d msg=%q", code, msg)
	}
	if len(noFile.calls) != 0 {
		t.Errorf("缺文件段不得转发: calls=%d", len(noFile.calls))
	}
}
