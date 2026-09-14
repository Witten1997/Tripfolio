package objectstore

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestKeysForIsDeterministicAndSeparatedByPrefix(t *testing.T) {
	account := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	asset := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	keys := KeysFor(account, asset, 1)
	if got := KeysFor(account, asset, 1); got != keys {
		t.Fatalf("同一尝试两次推导结果不同：%+v vs %+v", keys, got)
	}

	// 三个前缀必须互不包含：暂存前缀单独配置生命周期清理，不能误删最终对象。
	if !strings.HasPrefix(keys.Staging, "staging/") {
		t.Errorf("暂存键前缀错误：%s", keys.Staging)
	}
	if !strings.HasPrefix(keys.Final, "assets/") {
		t.Errorf("最终键前缀错误：%s", keys.Final)
	}
	if !strings.HasPrefix(keys.Thumbnail, "thumbnails/") {
		t.Errorf("缩略图键前缀错误：%s", keys.Thumbnail)
	}

	// 不同尝试必须产生不同的键，否则重试会覆盖上一次的对象。
	next := KeysFor(account, asset, 2)
	if next.Staging == keys.Staging || next.Final == keys.Final || next.Thumbnail == keys.Thumbnail {
		t.Errorf("尝试序号未进入对象键：%+v vs %+v", keys, next)
	}

	// 不同账号不能撞键。
	other := KeysFor(uuid.MustParse("33333333-3333-4333-8333-333333333333"), asset, 1)
	if other.Final == keys.Final {
		t.Errorf("不同账号产生了相同的最终键：%s", other.Final)
	}
}

func TestContentDispositionEncodesNonASCIIFileName(t *testing.T) {
	got := contentDisposition(DispositionAttachment, "行程单 2026.pdf")

	// 头值必须是纯 ASCII，否则 HTTP 头无法安全传输。
	for i := 0; i < len(got); i++ {
		if got[i] > 0x7e {
			t.Fatalf("Content-Disposition 含非 ASCII 字节：%q", got)
		}
	}
	if !strings.HasPrefix(got, "attachment;") {
		t.Errorf("disposition 前缀错误：%q", got)
	}
	if !strings.Contains(got, "filename*=UTF-8''") {
		t.Errorf("缺少 RFC 5987 的 filename* 参数：%q", got)
	}
	// 中文被百分号编码后，ASCII 回退名里不应残留原字符。
	if strings.Contains(got, "行程单") {
		t.Errorf("回退文件名未转义：%q", got)
	}
}

func TestContentDispositionRejectsHeaderInjection(t *testing.T) {
	// 引号与换行若原样进入头值，可截断或伪造响应头。
	got := contentDisposition(DispositionInline, "a\"b\r\nX-Evil: 1.png")

	if strings.Contains(got, "\r") || strings.Contains(got, "\n") {
		t.Fatalf("头值残留换行：%q", got)
	}
	quoted := got[strings.Index(got, `filename="`)+len(`filename="`):]
	quoted = quoted[:strings.Index(quoted, `"`)]
	if strings.ContainsAny(quoted, "\"\\") {
		t.Errorf("回退文件名残留引号或反斜杠：%q", quoted)
	}
}

func TestContentDispositionWithoutFileName(t *testing.T) {
	if got := contentDisposition(DispositionInline, ""); got != "inline" {
		t.Errorf("无文件名时应只返回 disposition，得到 %q", got)
	}
}

func TestNormalizeETagStripsQuotes(t *testing.T) {
	if got := normalizeETag(`"d41d8cd98f00b204e9800998ecf8427e"`); got != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Errorf("ETag 引号未去除：%q", got)
	}
	if got := normalizeETag("d41d8cd9"); got != "d41d8cd9" {
		t.Errorf("无引号 ETag 被改动：%q", got)
	}
}

func TestNewS3StoreValidatesConfig(t *testing.T) {
	base := Config{Endpoint: "http://localhost:9000", Bucket: "tripfolio", AccessKeyID: "ak", SecretAccessKey: "sk"}

	if _, err := NewS3Store(base); err != nil {
		t.Fatalf("合法配置被拒绝：%v", err)
	}

	cases := map[string]Config{
		"缺少桶名":   {Endpoint: base.Endpoint, AccessKeyID: "ak", SecretAccessKey: "sk"},
		"缺少端点":   {Bucket: base.Bucket, AccessKeyID: "ak", SecretAccessKey: "sk"},
		"缺少访问密钥": {Endpoint: base.Endpoint, Bucket: base.Bucket, SecretAccessKey: "sk"},
		"缺少密钥":   {Endpoint: base.Endpoint, Bucket: base.Bucket, AccessKeyID: "ak"},
	}
	for name, cfg := range cases {
		if _, err := NewS3Store(cfg); err == nil {
			t.Errorf("%s 时应返回错误", name)
		}
	}
}
