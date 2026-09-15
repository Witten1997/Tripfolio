package imaging

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// 生成一张纯色图片，供各测试构造真实的 JPEG / PNG 字节。
func makeImage(t *testing.T, w, h int, encode func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := encode(&buf, img); err != nil {
		t.Fatalf("编码测试图片：%v", err)
	}
	return buf.Bytes()
}

func encodeJPEG(buf *bytes.Buffer, img image.Image) error {
	return jpeg.Encode(buf, img, &jpeg.Options{Quality: 90})
}

func encodePNG(buf *bytes.Buffer, img image.Image) error {
	return png.Encode(buf, img)
}

// 嗅探只看魔数，不信任扩展名或声明类型。
func TestSniffMediaTypeByMagicBytes(t *testing.T) {
	cases := map[string]struct {
		data []byte
		want string
	}{
		"JPEG": {makeImage(t, 4, 4, encodeJPEG), "image/jpeg"},
		"PNG":  {makeImage(t, 4, 4, encodePNG), "image/png"},
		"WebP": {[]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "image/webp"},
		"PDF":  {[]byte("%PDF-1.7\n%âãÏÓ"), "application/pdf"},
		// 声称是图片的文本文件必须被识别为未知，交由上层判定失败。
		"文本冒充": {[]byte("GIF89a not really"), ""},
		"空内容":  {nil, ""},
		"截断头":  {[]byte{0xFF, 0xD8}, ""},
	}
	for name, c := range cases {
		if got := SniffMediaType(c.data); got != c.want {
			t.Errorf("%s：嗅探得到 %q，期望 %q", name, got, c.want)
		}
	}
}

// Inspect 只解码头部即可得到尺寸与格式。
func TestInspectReadsDimensionsWithoutFullDecode(t *testing.T) {
	data := makeImage(t, 640, 480, encodeJPEG)
	info, err := Inspect(data)
	if err != nil {
		t.Fatalf("Inspect：%v", err)
	}
	if info.Width != 640 || info.Height != 480 {
		t.Errorf("尺寸 %d×%d，期望 640×480", info.Width, info.Height)
	}
	if info.Format != "jpeg" {
		t.Errorf("格式 %q，期望 jpeg", info.Format)
	}
	// 测试图片没有 EXIF，建议值必须为空而不是伪造。
	if info.TakenAt != "" || info.Latitude != nil || info.Longitude != nil {
		t.Errorf("无 EXIF 的图片不应有建议值：%+v", info)
	}
}

func TestInspectRejectsNonImage(t *testing.T) {
	_, err := Inspect([]byte("%PDF-1.7 definitely not an image"))
	if !errors.Is(err, ErrNotImage) {
		t.Errorf("PDF 内容应返回 ErrNotImage，得到 %v", err)
	}
}

// 解压炸弹：PNG 头部声明巨大画布，文件本身很小。必须在分配像素前拒绝。
func TestInspectRejectsDecompressionBomb(t *testing.T) {
	// 手工拼一个只有 IHDR 的 PNG 头：宽高各 30000，像素数 9 亿 > MaxPixels。
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	ihdr := []byte{
		0, 0, 0, 13, // 长度
		'I', 'H', 'D', 'R',
		0x00, 0x00, 0x75, 0x30, // 宽 30000
		0x00, 0x00, 0x75, 0x30, // 高 30000
		8, 6, 0, 0, 0, // 位深、颜色类型、压缩、过滤、隔行
	}
	buf.Write(ihdr)
	// CRC 随便给，DecodeConfig 在读到尺寸后就返回，不校验 IHDR 的 CRC 也行；
	// 若实现校验则本用例仍应因尺寸被拒或因格式错误被拒，两者都不会分配 9 亿像素。
	buf.Write([]byte{0, 0, 0, 0})

	_, err := Inspect(buf.Bytes())
	if err == nil {
		t.Fatal("30000×30000 的画布应被拒绝")
	}
	if !errors.Is(err, ErrTooLarge) && !errors.Is(err, ErrNotImage) {
		t.Errorf("应返回 ErrTooLarge 或 ErrNotImage，得到 %v", err)
	}
}

// 缩略图长边不超过上限，且保持宽高比。
func TestThumbnailScalesDownPreservingAspect(t *testing.T) {
	data := makeImage(t, 2000, 1000, encodePNG)
	thumb, err := Thumbnail(data)
	if err != nil {
		t.Fatalf("Thumbnail：%v", err)
	}
	// 输出必须是 JPEG，与下载授权固定的 image/jpeg 一致。
	if SniffMediaType(thumb) != "image/jpeg" {
		t.Fatalf("缩略图应为 JPEG，嗅探得到 %q", SniffMediaType(thumb))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("解码缩略图：%v", err)
	}
	if cfg.Width != ThumbnailMaxEdge {
		t.Errorf("长边应为 %d，得到 %d", ThumbnailMaxEdge, cfg.Width)
	}
	if cfg.Height != ThumbnailMaxEdge/2 {
		t.Errorf("宽高比未保持：%d×%d", cfg.Width, cfg.Height)
	}
}

// 竖图按高度缩放。
func TestThumbnailPortrait(t *testing.T) {
	data := makeImage(t, 300, 1200, encodeJPEG)
	thumb, err := Thumbnail(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(thumb))
	if cfg.Height != ThumbnailMaxEdge || cfg.Width != ThumbnailMaxEdge/4 {
		t.Errorf("竖图缩放错误：%d×%d", cfg.Width, cfg.Height)
	}
}

// 小图不放大：放大只会制造虚假清晰度，还浪费存储。
func TestThumbnailDoesNotUpscale(t *testing.T) {
	data := makeImage(t, 100, 80, encodePNG)
	thumb, err := Thumbnail(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(thumb))
	if cfg.Width != 100 || cfg.Height != 80 {
		t.Errorf("小图不应放大，得到 %d×%d", cfg.Width, cfg.Height)
	}
}

func TestThumbnailRejectsNonImage(t *testing.T) {
	if _, err := Thumbnail([]byte("%PDF-1.7")); !errors.Is(err, ErrNotImage) {
		t.Errorf("PDF 不应能生成缩略图，得到 %v", err)
	}
}

// ReadLimited 多读一字节，让调用方能区分「恰好等于上限」与「超过上限」。
func TestReadLimitedReadsOneExtraByte(t *testing.T) {
	src := bytes.Repeat([]byte("x"), 10)

	got, err := ReadLimited(bytes.NewReader(src), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Errorf("恰好等于上限时应读到 10 字节，得到 %d", len(got))
	}

	got, err = ReadLimited(bytes.NewReader(src), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Errorf("超限时应读到 limit+1=6 字节供判定，得到 %d", len(got))
	}
}
