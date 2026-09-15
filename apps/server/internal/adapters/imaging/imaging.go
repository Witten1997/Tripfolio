// Package imaging 解码图片、提取 EXIF 建议值并生成缩略图。
// 只被 worker 使用；业务模块通过自己声明的窄接口调用，不导入本包。
//
// 解码有尺寸与像素上限：恶意构造的小文件可以声明巨大画布（解压炸弹），
// 必须在分配内存前拒绝，而不是等 OOM。
package imaging

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/tiff"
	"golang.org/x/image/draw"

	// 注册解码器：JPEG、PNG 在标准库，WebP 在 x/image。
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// MaxPixels 是允许解码的最大像素数（宽 × 高）。
// 4 亿像素约等于 20000×20000，远超手机照片，又能挡住解压炸弹。
const MaxPixels = 400_000_000

// ThumbnailMaxEdge 是缩略图长边像素数。
const ThumbnailMaxEdge = 512

// ThumbnailQuality 是缩略图 JPEG 质量。
const ThumbnailQuality = 82

// ErrNotImage 表示内容不是可识别的图片。
var ErrNotImage = errors.New("内容不是可识别的图片")

// ErrTooLarge 表示图片像素数超过解码上限。
var ErrTooLarge = errors.New("图片尺寸超过解码上限")

// Info 是图片的尺寸与 EXIF 建议值。
type Info struct {
	Width  int
	Height int
	// Format 是实际识别出的格式名："jpeg"、"png"、"webp"。
	Format string
	// TakenAt 是 EXIF 的拍摄时间，按原样保留不带时区；没有则为空。
	TakenAt string
	// Latitude 与 Longitude 是 EXIF 的 WGS-84 原始坐标；没有则为 nil。
	Latitude  *float64
	Longitude *float64
}

// Inspect 读取图片的尺寸与 EXIF 建议值。
// 只解码头部，不解码全部像素，因此可以在生成缩略图之前先拒绝超大图。
func Inspect(data []byte) (Info, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Info{}, fmt.Errorf("%w: %v", ErrNotImage, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return Info{}, ErrNotImage
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return Info{}, fmt.Errorf("%w: %d×%d", ErrTooLarge, cfg.Width, cfg.Height)
	}

	info := Info{Width: cfg.Width, Height: cfg.Height, Format: format}
	// EXIF 只存在于 JPEG（与部分 TIFF）；解析失败不是错误，只是没有建议值。
	if x, err := exif.Decode(bytes.NewReader(data)); err == nil {
		info.TakenAt = exifTakenAt(x)
		info.Latitude, info.Longitude = exifCoords(x)
	}
	return info, nil
}

// exifTakenAt 取 DateTimeOriginal，退到 DateTime。
// 返回 YYYY-MM-DDTHH:mm:ss，不带时区：EXIF 本身没有时区信息，按原样保存供客户端判断。
func exifTakenAt(x *exif.Exif) string {
	for _, field := range []exif.FieldName{exif.DateTimeOriginal, exif.DateTime} {
		tag, err := x.Get(field)
		if err != nil {
			continue
		}
		raw, err := tag.StringVal()
		if err != nil {
			continue
		}
		// EXIF 格式是 "2026:09:14 10:30:00"。
		t, err := time.Parse("2006:01:02 15:04:05", raw)
		if err != nil {
			continue
		}
		return t.Format("2006-01-02T15:04:05")
	}
	return ""
}

// exifCoords 取 GPS 坐标并按参考方向取负。返回 WGS-84 原始值，不做坐标系转换。
func exifCoords(x *exif.Exif) (*float64, *float64) {
	lat, err := x.Get(exif.GPSLatitude)
	if err != nil {
		return nil, nil
	}
	lng, err := x.Get(exif.GPSLongitude)
	if err != nil {
		return nil, nil
	}
	latVal, ok := dmsToDegrees(lat)
	if !ok {
		return nil, nil
	}
	lngVal, ok := dmsToDegrees(lng)
	if !ok {
		return nil, nil
	}
	if ref, err := x.Get(exif.GPSLatitudeRef); err == nil {
		if s, err := ref.StringVal(); err == nil && (s == "S" || s == "s") {
			latVal = -latVal
		}
	}
	if ref, err := x.Get(exif.GPSLongitudeRef); err == nil {
		if s, err := ref.StringVal(); err == nil && (s == "W" || s == "w") {
			lngVal = -lngVal
		}
	}
	// 越界值说明 EXIF 损坏，按没有坐标处理，避免写入非法建议值。
	if latVal < -90 || latVal > 90 || lngVal < -180 || lngVal > 180 {
		return nil, nil
	}
	return &latVal, &lngVal
}

// dmsToDegrees 把 EXIF 的度分秒有理数转为十进制度。
func dmsToDegrees(tag *tiff.Tag) (float64, bool) {
	if tag.Count < 3 {
		return 0, false
	}
	var total float64
	for i := 0; i < 3; i++ {
		num, den, err := tag.Rat2(i)
		if err != nil || den == 0 {
			return 0, false
		}
		total += float64(num) / float64(den) / pow60(i)
	}
	return total, true
}

func pow60(i int) float64 {
	switch i {
	case 0:
		return 1
	case 1:
		return 60
	default:
		return 3600
	}
}

// Thumbnail 生成长边不超过 ThumbnailMaxEdge 的 JPEG 缩略图。
// 原图小于目标尺寸时不放大，直接按原尺寸转码，避免虚假的清晰度。
func Thumbnail(data []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotImage, err)
	}
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return nil, ErrNotImage
	}

	tw, th := w, h
	if w > ThumbnailMaxEdge || h > ThumbnailMaxEdge {
		if w >= h {
			tw = ThumbnailMaxEdge
			th = h * ThumbnailMaxEdge / w
		} else {
			th = ThumbnailMaxEdge
			tw = w * ThumbnailMaxEdge / h
		}
	}
	if tw < 1 {
		tw = 1
	}
	if th < 1 {
		th = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	// CatmullRom 在缩小照片时质量与速度平衡较好。
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: ThumbnailQuality}); err != nil {
		return nil, fmt.Errorf("编码缩略图: %w", err)
	}
	return buf.Bytes(), nil
}

// SniffMediaType 按内容嗅探媒体类型，不信任客户端声明的类型与扩展名。
// 只识别本项目允许的四种类型，其余返回空串。
func SniffMediaType(data []byte) string {
	switch {
	case len(data) >= 3 && bytes.Equal(data[:3], []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	case len(data) >= 5 && bytes.Equal(data[:5], []byte("%PDF-")):
		return "application/pdf"
	}
	return ""
}

// ReadLimited 读取最多 limit+1 字节，便于调用方判断是否超限。
func ReadLimited(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit+1))
}
