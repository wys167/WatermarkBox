package main

import (
	"bytes"
	"testing"
	"time"
)

// pbUnwrap 剥掉一层 field/length 包装，返回内部消息。
func pbUnwrap(t *testing.T, data []byte, wantField byte) []byte {
	t.Helper()
	if data[0] != wantField {
		t.Fatalf("字段标签不符: 期望 % x 实际 % x", wantField, data[0])
	}
	length, offset := 0, 1
	for shift := uint(0); ; shift += 7 {
		if offset >= len(data) {
			t.Fatal("varint 越界")
		}
		length |= int(data[offset]&0x7f) << shift
		if data[offset] < 0x80 {
			offset++
			break
		}
		offset++
	}
	if offset+length != len(data) {
		t.Fatalf("长度不符: 声明 %d 实际 %d", length, len(data)-offset)
	}
	return data[offset:]
}

// 验证 protobuf 手工编码与 cls.proto 定义一致：
// LogGroupList.logGroupList(1) → LogGroup.logs(1) → Log.time(1) + Log.contents(2)。
func TestEncodeLogGroupList(t *testing.T) {
	log := clsLog{
		time:  time.Unix(1, 0),
		line:  "2026-10-20T00:00:00Z info test",
		level: "info",
		event: "test",
	}
	encoded := encodeLogGroupList([]clsLog{log})

	group := pbUnwrap(t, encoded, 0x0A) // LogGroupList → LogGroup
	entry := pbUnwrap(t, group, 0x0A)   // LogGroup → Log
	if entry[0] != 0x08 || entry[1] != 0x01 {
		t.Fatalf("Log.time 编码不符: % x", entry[:2])
	}
	// 其余应为若干 Content（field 2，wire 2 → 0x12）
	for offset := 2; offset < len(entry); {
		if entry[offset] != 0x12 {
			t.Fatalf("Log.contents 标签不符: % x", entry[offset])
		}
		contentLength := int(entry[offset+1])
		offset += 2 + contentLength
	}
}

// 验证每条日志包含 hostname/level/event/message 四个字段。
func TestEncodeClsLogContents(t *testing.T) {
	encoded := encodeClsLog(clsLog{
		time:   time.Unix(100, 0),
		level:  "error",
		event:  "magick_failed",
		fields: map[string]string{"args": "convert a.jpg"},
		line:   "line",
	})
	for _, want := range []string{"hostname", "level", "event", "message"} {
		if !bytes.Contains(encoded, []byte(want)) {
			t.Errorf("编码结果缺少字段 %s: % x", want, encoded)
		}
	}
	if !bytes.Contains(encoded, []byte("magick_failed")) {
		t.Errorf("编码结果缺少事件名: % x", encoded)
	}
}

// 验证 TC3 签名的各段格式。
func TestTencentCloudAuthorization(t *testing.T) {
	authorization := tencentCloudAuthorization("AKIDtest", "keytest", 1551113065, []byte("payload"))
	wantPrefix := "TC3-HMAC-SHA256 Credential=AKIDtest/2019-02-25/cls/tc3_request, SignedHeaders=content-type;host, Signature="
	if !bytes.HasPrefix([]byte(authorization), []byte(wantPrefix)) {
		t.Fatalf("Authorization 前缀不符: %s", authorization)
	}
	if len(authorization) <= len(wantPrefix)+63 {
		t.Fatalf("签名摘要长度不符: %s", authorization)
	}
}

// 密钥齐全才启用上传，任何一项缺失即禁用。
func TestNewClsUploaderDisabled(t *testing.T) {
	t.Setenv("TENCENTCLOUD_SECRET_ID", "")
	if newClsUploader().enabled {
		t.Error("缺少密钥时不应启用上传")
	}
}
