package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// 腾讯云 CLS 日志上传：不引入官方 SDK（体积过大），直接手写
// TC3-HMAC-SHA256 签名（https://cloud.tencent.com/document/api/213/30654）
// 和 UploadLog 接口的 protobuf 请求体（https://cloud.tencent.com/document/product/614/59470）。
//
// 密钥只从环境变量读取，读到且配置齐全才上传；密钥错误或网络失败一律静默忽略，
// 不影响应用本身。因此分发给普通用户的包默认只写本地日志。

const (
	clsHost        = "cls.tencentcloudapi.com"
	clsService     = "cls"
	clsContentType = "application/x-protobuf"
	clsMaxPending  = 500 // 本地积压上限，超过丢弃最早日志
)

type clsLog struct {
	time   time.Time
	level  string
	event  string
	fields map[string]string
	line   string
}

type clsUploader struct {
	enabled   bool
	secretID  string
	secretKey string
	topicID   string
	region    string

	mu      sync.Mutex
	pending []clsLog
	client  *http.Client
}

// newClsUploader 从环境变量读取配置：
//
//	TENCENTCLOUD_SECRET_ID / TENCENTCLOUD_SECRET_KEY  腾讯云密钥（官方 SDK 同名变量）
//	WATERMARKBOX_CLS_TOPIC_ID                          CLS 日志主题 ID
//	WATERMARKBOX_CLS_REGION                            CLS 地域，如 ap-shanghai
//
// 任何一项缺失即禁用上传。
func newClsUploader() *clsUploader {
	uploader := &clsUploader{client: &http.Client{Timeout: 8 * time.Second}}
	uploader.secretID = os.Getenv("TENCENTCLOUD_SECRET_ID")
	uploader.secretKey = os.Getenv("TENCENTCLOUD_SECRET_KEY")
	uploader.topicID = os.Getenv("WATERMARKBOX_CLS_TOPIC_ID")
	uploader.region = os.Getenv("WATERMARKBOX_CLS_REGION")
	if uploader.secretID == "" || uploader.secretKey == "" ||
		uploader.topicID == "" || uploader.region == "" {
		return uploader
	}
	uploader.enabled = true
	return uploader
}

func (u *clsUploader) enqueue(log clsLog) {
	if !u.enabled {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pending = append(u.pending, log)
	if len(u.pending) > clsMaxPending {
		u.pending = u.pending[len(u.pending)-clsMaxPending:]
	}
}

// loop 周期性批量上传，直到进程退出。
func (u *clsUploader) loop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		u.flush()
	}
}

func (u *clsUploader) flush() {
	u.mu.Lock()
	if !u.enabled || len(u.pending) == 0 {
		u.mu.Unlock()
		return
	}
	batch := u.pending
	u.pending = nil
	u.mu.Unlock()

	body := encodeLogGroupList(batch)
	request, err := u.buildRequest(body, time.Now().Unix())
	if err != nil {
		u.requeue(batch, false)
		return
	}
	response, err := u.client.Do(request)
	if err != nil {
		u.requeue(batch, false) // 网络问题，稍后重试
		return
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))

	switch {
	case response.StatusCode == http.StatusOK:
		// 上传成功
	case response.StatusCode >= 400 && response.StatusCode < 500:
		// 4xx 属于配置/密钥问题，重试没有意义，永久禁用
		logEvent("error", "cls_upload_disabled", map[string]string{
			"status": strconv.Itoa(response.StatusCode),
			"body":   truncateForLog(string(responseBody), 300),
		})
		u.mu.Lock()
		u.enabled = false
		u.mu.Unlock()
	default:
		u.requeue(batch, false) // 5xx，稍后重试
	}
}

// requeue 把没发出去的日志放回队列；discard 为 true 时直接丢弃。
func (u *clsUploader) requeue(batch []clsLog, discard bool) {
	if discard {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pending = append(batch, u.pending...)
	if len(u.pending) > clsMaxPending {
		u.pending = u.pending[len(u.pending)-clsMaxPending:]
	}
}

func (u *clsUploader) buildRequest(body []byte, timestamp int64) (*http.Request, error) {
	authorization := tencentCloudAuthorization(u.secretID, u.secretKey, timestamp, body)
	request, err := http.NewRequest(http.MethodPost, "https://"+clsHost, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", clsContentType)
	request.Header.Set("Authorization", authorization)
	request.Header.Set("X-TC-Action", "UploadLog")
	request.Header.Set("X-TC-Version", "2020-10-16")
	request.Header.Set("X-TC-Timestamp", strconv.FormatInt(timestamp, 10))
	request.Header.Set("X-TC-Region", u.region)
	request.Header.Set("X-CLS-TopicId", u.topicID)
	return request, nil
}

// tencentCloudAuthorization 计算 TC3-HMAC-SHA256 签名。
// 固定签名 content-type 与 host 两个头部（文档要求的最低集合）。
func tencentCloudAuthorization(secretID, secretKey string, timestamp int64, payload []byte) string {
	date := time.Unix(timestamp, 0).UTC().Format("2006-01-02")
	payloadHash := sha256Hex(payload)
	canonicalHeaders := "content-type:" + clsContentType + "\nhost:" + clsHost + "\n"
	canonicalRequest := "POST\n/\n\n" + canonicalHeaders + "\ncontent-type;host\n" + payloadHash
	credentialScope := date + "/" + clsService + "/tc3_request"
	stringToSign := "TC3-HMAC-SHA256\n" + strconv.FormatInt(timestamp, 10) + "\n" +
		credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))

	secretDate := hmacSHA256([]byte("TC3"+secretKey), date)
	secretService := hmacSHA256(secretDate, clsService)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))

	return "TC3-HMAC-SHA256 Credential=" + secretID + "/" + credentialScope +
		", SignedHeaders=content-type;host, Signature=" + signature
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func hmacSHA256(key []byte, message string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return mac.Sum(nil)
}

// ---- protobuf 手工编码（cls.proto 子集）----
//
//	message Content { string key = 1; string value = 2; }
//	message Log { int64 time = 1; repeated Content contents = 2; }
//	message LogGroup { repeated Log logs = 1; }
//	message LogGroupList { repeated LogGroup logGroupList = 1; }

func pbAppendVarint(buffer []byte, value uint64) []byte {
	for value >= 0x80 {
		buffer = append(buffer, byte(value)|0x80)
		value >>= 7
	}
	return append(buffer, byte(value))
}

func pbAppendTag(buffer []byte, field, wireType int) []byte {
	return pbAppendVarint(buffer, uint64(field<<3|wireType))
}

func pbAppendString(buffer []byte, field int, value string) []byte {
	buffer = pbAppendTag(buffer, field, 2)
	buffer = pbAppendVarint(buffer, uint64(len(value)))
	return append(buffer, value...)
}

func pbAppendMessage(buffer []byte, field int, message []byte) []byte {
	buffer = pbAppendTag(buffer, field, 2)
	buffer = pbAppendVarint(buffer, uint64(len(message)))
	return append(buffer, message...)
}

func encodeClsLog(log clsLog) []byte {
	var out []byte
	out = pbAppendTag(out, 1, 0) // time
	out = pbAppendVarint(out, uint64(log.time.Unix()))

	contents := []struct{ key, value string }{
		{"hostname", logHostname},
		{"level", log.level},
		{"event", log.event},
		{"message", log.line},
	}
	var content []byte
	for _, item := range contents {
		content = pbAppendString(content, 1, item.key)
		content = pbAppendString(content, 2, item.value)
		out = pbAppendMessage(out, 2, content)
		content = content[:0]
	}
	return out
}

func encodeLogGroupList(logs []clsLog) []byte {
	var group []byte
	for _, log := range logs {
		group = pbAppendMessage(group, 1, encodeClsLog(log))
	}
	var list []byte
	list = pbAppendMessage(list, 1, group)
	return list
}
