package sync

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/types"
)

func normalizeMediaPayload(op Operation) (json.RawMessage, error) {
	allowed, required := []string{}, []string{}
	switch op.Type {
	case "asset.register":
		allowed = []string{"scope", "trip_id", "original_name", "expected_size", "declared_media_type", "client_sha256"}
		required = []string{"scope", "trip_id", "original_name", "expected_size", "declared_media_type"}
	case "photo.create", "photo.update":
		allowed = []string{"asset_id", "taken_at_local", "recorded_on", "caption", "place_name", "address", "latitude", "longitude"}
		if op.Type == "photo.create" {
			allowed = append(allowed, "sort_order")
			required = []string{"asset_id", "recorded_on"}
		}
	case "photo.reorder":
		allowed = []string{"recorded_on", "ordered_ids"}
		required = allowed
	case "photo.delete":
	default:
		return nil, apperr.Unprocessable("OFFLINE_OPERATION_NOT_ALLOWED", "不支持的媒体操作")
	}
	if err := validateJSON(op.Payload); err != nil {
		return nil, err
	}
	m, err := object(op.Payload, required, allowed)
	if err != nil {
		return nil, mediaInvalid("payload", err.Error())
	}
	if op.Type == "photo.update" && len(m) == 0 {
		return nil, mediaInvalid("payload", "没有可更新的字段")
	}
	for k, raw := range m {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if k != "taken_at_local" && k != "latitude" && k != "longitude" && k != "client_sha256" {
				return nil, mediaInvalid(k, "字段不可为空")
			}
			m[k] = json.RawMessage("null")
			continue
		}
		var value any
		switch k {
		case "asset_id", "trip_id":
			var id uuid.UUID
			if json.Unmarshal(raw, &id) != nil || id == uuid.Nil {
				return nil, mediaInvalid(k, "UUID无效")
			}
			if k == "trip_id" && (op.TripID == nil || id != *op.TripID) {
				return nil, mediaInvalid(k, "旅行范围不匹配")
			}
			value = id.String()
		case "expected_size":
			var n int64
			if json.Unmarshal(raw, &n) != nil || n <= 0 {
				return nil, mediaInvalid(k, "声明大小必须为正整数")
			}
			value = n
		case "sort_order":
			var n int32
			if json.Unmarshal(raw, &n) != nil || n < 0 {
				return nil, mediaInvalid(k, "顺序必须为非负整数")
			}
			value = n
		case "latitude", "longitude":
			var n float64
			limit := 180.0
			if k == "latitude" {
				limit = 90
			}
			if json.Unmarshal(raw, &n) != nil || math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > limit {
				return nil, mediaInvalid(k, "坐标超出范围")
			}
			n, _ = strconv.ParseFloat(strconv.FormatFloat(n, 'f', 6, 64), 64)
			if n == 0 {
				n = 0
			}
			value = n
		case "ordered_ids":
			var ids []uuid.UUID
			if json.Unmarshal(raw, &ids) != nil || ids == nil {
				return nil, mediaInvalid(k, "排序需要数组")
			}
			seen := map[uuid.UUID]bool{}
			for _, id := range ids {
				if id == uuid.Nil || seen[id] {
					return nil, mediaInvalid(k, "排序ID必须有效且无重复")
				}
				seen[id] = true
			}
			value = ids
		default:
			var s string
			if json.Unmarshal(raw, &s) != nil || strings.ContainsRune(s, '\x00') {
				return nil, mediaInvalid(k, "字段必须为有效字符串")
			}
			switch k {
			case "scope":
				if s != "trip" {
					return nil, mediaInvalid(k, "同步资产仅允许trip范围")
				}
			case "recorded_on":
				if _, err := types.ParseDate(s); err != nil {
					return nil, mediaInvalid(k, "日期无效")
				}
			case "taken_at_local":
				if _, err := types.ParseLocalDateTime(s); err != nil {
					return nil, mediaInvalid(k, "本地时间无效")
				}
			case "declared_media_type":
				s = strings.ToLower(strings.TrimSpace(strings.SplitN(s, ";", 2)[0]))
			case "original_name":
				s = strings.TrimSpace(s)
				if s == "" || len([]rune(s)) > 255 {
					return nil, mediaInvalid(k, "文件名长度无效")
				}
			case "client_sha256":
				if len(s) != 64 || strings.Trim(s, "0123456789abcdef") != "" {
					return nil, mediaInvalid(k, "摘要必须是64位小写十六进制")
				}
			}
			value = s
		}
		m[k], _ = json.Marshal(value)
	}
	if op.Type == "photo.create" {
		for _, k := range []string{"caption", "place_name", "address"} {
			if _, ok := m[k]; !ok {
				m[k] = json.RawMessage(`""`)
			}
		}
		for _, k := range []string{"taken_at_local", "latitude", "longitude"} {
			if _, ok := m[k]; !ok {
				m[k] = json.RawMessage("null")
			}
		}
		if _, ok := m["sort_order"]; !ok {
			m["sort_order"] = json.RawMessage("0")
		}
	}
	if op.Type == "asset.register" {
		if _, ok := m["client_sha256"]; !ok {
			m["client_sha256"] = json.RawMessage("null")
		}
	}
	return json.Marshal(m)
}

func mediaInvalid(field, message string) error {
	return apperr.Validation(apperr.Field(field, "INVALID", message))
}
