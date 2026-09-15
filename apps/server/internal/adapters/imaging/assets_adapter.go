package imaging

import (
	"errors"
	"fmt"

	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/modules/assets"
)

// AssetsProcessor 把本包的能力适配为 assets.ImageProcessor：
// 转换结构并把本包的分类错误映射为模块声明的哨兵，模块不必导入本包。
type AssetsProcessor struct{}

var _ assets.ImageProcessor = AssetsProcessor{}

// SniffMediaType 实现 assets.ImageProcessor。
func (AssetsProcessor) SniffMediaType(data []byte) string { return SniffMediaType(data) }

// Inspect 实现 assets.ImageProcessor。
func (AssetsProcessor) Inspect(data []byte) (assets.ImageInfo, error) {
	info, err := Inspect(data)
	switch {
	case errors.Is(err, ErrTooLarge):
		return assets.ImageInfo{}, fmt.Errorf("%w: %v", assets.ErrImageTooLarge, err)
	case errors.Is(err, ErrNotImage):
		return assets.ImageInfo{}, fmt.Errorf("%w: %v", assets.ErrNotImage, err)
	case err != nil:
		return assets.ImageInfo{}, err
	}
	out := assets.ImageInfo{
		Width: int32(info.Width), Height: int32(info.Height),
		Latitude: info.Latitude, Longitude: info.Longitude,
	}
	if info.TakenAt != "" {
		taken := types.LocalDateTime(info.TakenAt)
		out.TakenAt = &taken
	}
	return out, nil
}

// Thumbnail 实现 assets.ImageProcessor。
func (AssetsProcessor) Thumbnail(data []byte) ([]byte, error) {
	thumb, err := Thumbnail(data)
	if errors.Is(err, ErrNotImage) {
		return nil, fmt.Errorf("%w: %v", assets.ErrNotImage, err)
	}
	return thumb, err
}
