// Package geo 定义地点检索和交通路线；供应商请求由适配器实现，不持久化派生路线。
package geo

import (
	"context"
	"errors"
	"math"
)

var ErrNoResult = errors.New("地点或路线无结果")
var ErrQuotaExceeded = errors.New("地点服务日配额已用尽")
var ErrConfiguration = errors.New("地点服务凭证或权限不可用")

type Coordinate struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

func (c Coordinate) Valid() bool {
	return !math.IsNaN(c.Latitude) && !math.IsNaN(c.Longitude) &&
		!math.IsInf(c.Latitude, 0) && !math.IsInf(c.Longitude, 0) &&
		c.Latitude >= -90 && c.Latitude <= 90 && c.Longitude >= -180 && c.Longitude <= 180
}

type Place struct {
	Name      string  `json:"name"`
	Address   string  `json:"address"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Adcode    *string `json:"adcode"`
	POIID     *string `json:"poi_id"`
	Provider  string  `json:"provider"`
}

type Mode string

const (
	Driving Mode = "driving"
	Walking Mode = "walking"
	Cycling Mode = "cycling"
)

func (m Mode) Valid() bool { return m == Driving || m == Walking || m == Cycling }

type Route struct {
	Mode            Mode         `json:"mode"`
	DistanceMeters  int64        `json:"distance_meters"`
	DurationSeconds int64        `json:"duration_seconds"`
	Path            []Coordinate `json:"path"`
	Provider        string       `json:"provider"`
}

// MaxTripRoutePoints 是一次批量算路允许的坐标数上限（与契约 points 的最大长度对应）。
const MaxTripRoutePoints = 50

type Provider interface {
	ReverseGeocode(context.Context, string, float64, float64) (Place, error)
	SearchPlaces(context.Context, string, string, *float64, *float64, string) ([]Place, error)
	CalculateRoute(context.Context, string, Coordinate, Coordinate, Mode) (Route, error)
	// CalculateTripRoutes 一次算出有序坐标的相邻路段（长度为坐标数减一）。
	// 任一段无法确定时返回错误，调用方退回逐段调用。
	CalculateTripRoutes(context.Context, string, []Coordinate, Mode) ([]Route, error)
}
