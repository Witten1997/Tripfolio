package dashboard

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/modules/geo"
)

type Service struct {
	store    Store
	geocoder Geocoder
	clock    clock.Clock
}

func NewService(store Store, geocoder Geocoder, clk clock.Clock) *Service {
	return &Service{store: store, geocoder: geocoder, clock: clk}
}

func (s *Service) Get(ctx context.Context, a actor.Actor, q Query) (Snapshot, error) {
	for field, raw := range map[string]string{"date_from": q.DateFrom, "date_to": q.DateTo} {
		if raw != "" {
			if _, err := types.ParseDate(raw); err != nil {
				return Snapshot{}, apperr.Validation(apperr.Field(field, "INVALID", "日期须为 YYYY-MM-DD"))
			}
		}
	}
	if q.DateFrom != "" && q.DateTo != "" && q.DateFrom > q.DateTo {
		return Snapshot{}, apperr.Validation(apperr.Field("date_to", "INVALID", "结束日期不能早于开始日期"))
	}
	if q.LocationAfter != "" {
		parts := strings.Split(q.LocationAfter, ",")
		if len(parts) != 2 || len(q.LocationAfter) > 64 {
			return Snapshot{}, apperr.Validation(apperr.Field("location_after", "INVALID", "足迹识别游标无效"))
		}
		lat, errLat := strconv.ParseFloat(parts[0], 64)
		lng, errLng := strconv.ParseFloat(parts[1], 64)
		if errLat != nil || errLng != nil || !(geo.Coordinate{Latitude: lat, Longitude: lng}).Valid() {
			return Snapshot{}, apperr.Validation(apperr.Field("location_after", "INVALID", "足迹识别游标无效"))
		}
		q.LocationAfter = fmt.Sprintf("%.6f,%.6f", lat, lng)
	}
	now := s.clock.Now()
	out, err := s.store.Read(ctx, a.AccountID, q, now)
	if err != nil {
		return Snapshot{}, apperr.Internal(err)
	}
	out.GeneratedAt = now
	out.Days, out.MonthlyDays = countDays(out.Trips)
	s.resolve(ctx, a, q.LocationAfter, &out)
	return out, nil
}

// 地理编码不阻塞其他统计；每次最多处理 20 个不同坐标，失败保留未识别状态。
func (s *Service) resolve(ctx context.Context, a actor.Actor, after string, out *Snapshot) {
	byPoint := map[string][]int{}
	order := []string{}
	for i, p := range out.Places {
		if p.Excluded || p.Kind == "transport" || p.Region != nil {
			continue
		}
		if p.Latitude == nil || p.Longitude == nil {
			out.MissingCoordinates++
			continue
		}
		key := fmt.Sprintf("%.6f,%.6f", *p.Latitude, *p.Longitude)
		if _, ok := byPoint[key]; !ok {
			order = append(order, key)
		}
		byPoint[key] = append(byPoint[key], i)
	}
	// 稳定游标允许继续处理后续坐标，避免一批无法识别的地点挡住其余国内足迹。
	sort.Strings(order)
	start := sort.Search(len(order), func(i int) bool { return order[i] > after })
	end := min(start+20, len(order))
	if end < len(order) {
		out.NextLocationAfter = order[end-1]
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	gate := make(chan struct{}, 3)
	for _, key := range order[start:end] {
		if s.geocoder == nil {
			break
		}
		indices := byPoint[key]
		point := geo.Coordinate{Latitude: *out.Places[indices[0]].Latitude, Longitude: *out.Places[indices[0]].Longitude}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case gate <- struct{}{}:
			case <-deadline.Done():
				return
			}
			defer func() { <-gate }()
			location, err := s.geocoder.Reverse(deadline, a, point)
			if err != nil {
				mu.Lock()
				if out.LocationWarning == "" {
					out.LocationWarning = "部分地点的省市暂未识别，可稍后重试；足迹统计尚不完整。"
				}
				mu.Unlock()
				return
			}
			region := regionOf(location)
			if region == nil {
				return
			}
			if err := s.store.SaveRegion(deadline, point, *region, s.clock.Now()); err != nil {
				return
			}
			mu.Lock()
			for _, index := range indices {
				out.Places[index].Region = region
			}
			out.ResolvedLocations++
			mu.Unlock()
		}()
	}
	wg.Wait()
	for _, p := range out.Places {
		if !p.Excluded && p.Kind != "transport" && p.Region == nil && p.Latitude != nil && p.Longitude != nil {
			out.UnresolvedPlaces++
		}
	}
	if out.UnresolvedPlaces > 0 && out.LocationWarning == "" {
		out.LocationWarning = "仍有地点待识别省市，足迹统计尚不完整。"
	}
}

func regionOf(p geo.Place) *Region {
	if p.Adcode == nil || len(*p.Adcode) != 6 || p.Province == "" {
		return nil
	}
	code := *p.Adcode
	for _, c := range code {
		if c < '0' || c > '9' {
			return nil
		}
	}
	// 非国内行政区不进入国内足迹统计。
	allowed := ",11,12,13,14,15,21,22,23,31,32,33,34,35,36,37,41,42,43,44,45,46,50,51,52,53,54,61,62,63,64,65,71,81,82,"
	if !strings.Contains(allowed, ","+code[:2]+",") {
		return nil
	}
	region := &Region{ProvinceCode: code[:2] + "0000", ProvinceName: p.Province, CityCode: code[:4] + "00", CityName: p.City}
	switch code[:2] {
	case "11", "12", "31", "50", "81", "82":
		region.CityCode, region.CityName = region.ProvinceCode, p.Province
	default:
		if code[2:4] == "90" {
			region.CityCode, region.CityName = code, p.District
		}
		if region.CityName == "" {
			return nil
		}
	}
	return region
}

// 按日期区间合并，而非逐日展开，跨年旅行和重叠旅行都只计算一次。
func countDays(trips []Trip) (int, []int) {
	type span struct{ start, end time.Time }
	spans := make([]span, 0, len(trips))
	for i := range trips {
		start, end := trips[i].StartDate.Time(), trips[i].EndDate.Time()
		trips[i].Days = int(end.Sub(start).Hours()/24) + 1
		spans = append(spans, span{start, end.AddDate(0, 0, 1)})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	merged := []span{}
	for _, p := range spans {
		if len(merged) == 0 || p.start.After(merged[len(merged)-1].end) {
			merged = append(merged, p)
		} else if p.end.After(merged[len(merged)-1].end) {
			merged[len(merged)-1].end = p.end
		}
	}
	total, monthly := 0, make([]int, 12)
	for _, p := range merged {
		for start := p.start; start.Before(p.end); {
			end := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			if end.After(p.end) {
				end = p.end
			}
			days := int(end.Sub(start).Hours() / 24)
			monthly[int(start.Month())-1] += days
			total += days
			start = end
		}
	}
	return total, monthly
}
