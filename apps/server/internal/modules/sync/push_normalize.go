package sync

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/types"
)

var operationTypes = map[string]string{
	"trip.create": "trip", "trip.update": "trip", "trip.set_archived": "trip", "trip.delete": "trip", "trip.restore": "trip",
	"expense_category.create": "expense_category", "expense_category.update": "expense_category", "expense_category.delete": "expense_category", "expense_category.reorder": "expense_category",
	"trip_member.replace": "trip_member", "itinerary_item.create": "itinerary_item", "itinerary_item.update": "itinerary_item", "itinerary_item.delete": "itinerary_item", "itinerary_item.reorder": "itinerary_item",
	"ledger_entry.create": "ledger_entry", "ledger_entry.update": "ledger_entry", "ledger_entry.delete": "ledger_entry",
	"packing_item.create": "packing_item", "packing_item.update": "packing_item", "packing_item.set_status": "packing_item", "packing_item.delete": "packing_item", "packing_item.reorder": "packing_item",
	"todo.create": "todo", "todo.update": "todo", "todo.set_completed": "todo", "todo.delete": "todo", "todo.reorder": "todo",
	"reservation.create": "reservation", "reservation.update": "reservation", "reservation.delete": "reservation",
	"document.create": "document", "document.update": "document", "document.delete": "document",
	"photo.create": "photo", "photo.update": "photo", "photo.delete": "photo", "photo.reorder": "photo", "asset.register": "asset",
}

func malformed(detail string) error { return apperr.BadRequest("MALFORMED_REQUEST", detail) }

// validateJSON rejects duplicate keys, invalid UTF-8 and excessive nesting before decoding.
func validateJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return malformed("正文必须是有效UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return malformed("JSON嵌套过深")
		}
		token, err := d.Token()
		if err != nil {
			return malformed("JSON格式错误")
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return malformed("JSON格式错误")
				}
				s, ok := key.(string)
				if !ok || seen[s] {
					return malformed("JSON包含重复字段")
				}
				seen[s] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return malformed("JSON格式错误")
		}
		_, err = d.Token()
		if err != nil {
			return malformed("JSON格式错误")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return malformed("正文必须只有一个JSON值")
	}
	return nil
}

func object(raw []byte, required []string, allowed []string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, malformed("字段必须是JSON对象")
	}
	for key := range m {
		found := false
		for _, k := range allowed {
			if key == k {
				found = true
				break
			}
		}
		if !found {
			return nil, malformed("正文包含未知字段: " + key)
		}
	}
	for _, key := range required {
		if _, ok := m[key]; !ok {
			return nil, malformed("缺少字段: " + key)
		}
	}
	return m, nil
}

// DecodePush is used before the generated decoder discards object field presence.
func DecodePush(raw []byte) (PushInput, error) {
	var in PushInput
	if len(raw) > MaxPushBytes {
		return in, apperr.New(413, "REQUEST_TOO_LARGE", "正文超过1 MiB")
	}
	if err := validateJSON(raw); err != nil {
		return in, err
	}
	m, err := object(raw, []string{"sync_epoch", "client_id", "operations"}, []string{"sync_epoch", "client_id", "operations"})
	if err != nil {
		return in, err
	}
	var ops []json.RawMessage
	if err := json.Unmarshal(m["operations"], &ops); err != nil || ops == nil || len(ops) == 0 || len(ops) > MaxOperations {
		return in, malformed("operations须包含1至100项")
	}
	fields := []string{"operation_id", "type", "entity_type", "entity_id", "trip_id", "base", "guards", "depends_on", "payload"}
	for _, rawOp := range ops {
		if len(rawOp) > MaxOperationBytes {
			return in, apperr.New(413, "REQUEST_TOO_LARGE", "单个操作超过64 KiB")
		}
		m, err := object(rawOp, fields, fields)
		if err != nil {
			return in, err
		}
		if !bytes.Equal(bytes.TrimSpace(m["base"]), []byte("null")) {
			if _, err := object(m["base"], nil, []string{"version", "operation_id"}); err != nil {
				return in, err
			}
		}
		var guards []json.RawMessage
		if json.Unmarshal(m["guards"], &guards) != nil || guards == nil {
			return in, malformed("guards必须为数组")
		}
		for _, g := range guards {
			if _, err := object(g, []string{"kind", "scope_id"}, []string{"kind", "scope_id", "revision", "operation_id"}); err != nil {
				return in, err
			}
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		return in, malformed("同步命令结构错误")
	}
	return in, validateBatch(in)
}

func validateBatch(in PushInput) error {
	if raw, err := json.Marshal(in); err != nil {
		return malformed("同步命令结构错误")
	} else if len(raw) > MaxPushBytes {
		return apperr.New(413, "REQUEST_TOO_LARGE", "正文超过1 MiB")
	}
	if in.SyncEpoch == uuid.Nil || in.ClientID == uuid.Nil || len(in.Operations) == 0 || len(in.Operations) > MaxOperations {
		return malformed("同步代次、安装ID及操作数组必填")
	}
	positions := map[uuid.UUID]int{}
	for i, op := range in.Operations {
		if op.OperationID == uuid.Nil {
			return malformed("操作ID不可为空")
		}
		if _, ok := positions[op.OperationID]; ok {
			return malformed("操作ID重复")
		}
		positions[op.OperationID] = i
	}
	previousEntity := map[string]uuid.UUID{}
	for i, op := range in.Operations {
		entity, known := operationTypes[op.Type]
		if !known || op.EntityType != entity {
			return malformed("未知或不匹配的操作类型")
		}
		collection := strings.HasSuffix(op.Type, ".reorder")
		if collection != (op.EntityID == nil) || (op.EntityID != nil && *op.EntityID == uuid.Nil) {
			return malformed("实体目标不合法")
		}
		if (entity == "expense_category") != (op.TripID == nil) || (op.TripID != nil && *op.TripID == uuid.Nil) {
			return malformed("旅行范围不合法")
		}
		if entity == "trip" && *op.EntityID != *op.TripID {
			return malformed("旅行目标不匹配")
		}
		if entity == "trip_member" && *op.EntityID != *op.TripID {
			return malformed("成员集合目标不匹配")
		}
		create := strings.HasSuffix(op.Type, ".create") || op.Type == "asset.register" || collection || op.Type == "trip_member.replace"
		if create != (op.Base == nil) {
			return malformed("操作base结构不合法")
		}
		if op.Guards == nil || op.DependsOn == nil || len(op.DependsOn) > 100 {
			return malformed("guards和depends_on须为受限数组")
		}
		deps := map[uuid.UUID]bool{}
		for _, id := range op.DependsOn {
			if id == uuid.Nil || deps[id] || id == op.OperationID {
				return malformed("依赖重复或指向自身")
			}
			if p, ok := positions[id]; ok && p >= i {
				return malformed("同批依赖只能指向此前操作")
			}
			deps[id] = true
		}
		if op.EntityID != nil {
			key := op.EntityType + ":" + op.EntityID.String()
			if prior, ok := previousEntity[key]; ok && !deps[prior] {
				return malformed("同实体连续操作必须声明前项依赖")
			}
			previousEntity[key] = op.OperationID
		}
		if op.Base != nil {
			b := op.Base
			if (b.Version == nil) == (b.OperationID == nil) {
				return malformed("base必须为版本或操作引用")
			}
			if b.Version != nil {
				n, ok := decimal(*b.Version)
				if !ok || n < 1 {
					return malformed("版本必须为正十进制整数字符串")
				}
			}
			if b.OperationID != nil && !deps[*b.OperationID] {
				return malformed("base引用须声明依赖")
			}
		}
		guards := map[string]bool{}
		for _, g := range op.Guards {
			if !validGuard(g) {
				return malformed("guard范围或revision格式错误")
			}
			parts := strings.Split(g.ScopeID, "/")
			id, _ := uuid.Parse(parts[0])
			parts[0] = id.String()
			key := g.Kind + ":" + strings.Join(parts, "/")
			if (g.Revision == nil) == (g.OperationID == nil) || guards[key] {
				return malformed("guard重复或引用格式错误")
			}
			guards[key] = true
			if g.OperationID != nil && !deps[*g.OperationID] {
				return malformed("guard引用须声明依赖")
			}
			if !validGuard(g) {
				return malformed("guard范围或revision格式错误")
			}
		}
		payload := bytes.TrimSpace(op.Payload)
		if len(payload) == 0 || payload[0] != '{' {
			return malformed("payload必须为对象")
		}
		raw, err := json.Marshal(op)
		if err != nil {
			return malformed("操作结构错误")
		}
		if len(raw) > MaxOperationBytes {
			return apperr.New(413, "REQUEST_TOO_LARGE", "单个操作超过64 KiB")
		}
	}
	return nil
}

func validGuard(g GuardReference) bool {
	parts := strings.Split(g.ScopeID, "/")
	_, err := uuid.Parse(parts[0])
	if err != nil {
		return false
	}
	switch g.Kind {
	case "categories", "members", "packing_order", "todo_order":
		if len(parts) != 1 {
			return false
		}
	case "itinerary_day", "photo_day":
		if len(parts) != 2 || len(parts[1]) != 10 {
			return false
		}
		if _, err := types.ParseDate(parts[1]); err != nil {
			return false
		}
	default:
		return false
	}
	if g.Revision != nil {
		s := *g.Revision
		if !strings.HasPrefix(s, "sha256:") || len(s) != 71 {
			return false
		}
		for _, r := range s[7:] {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
				return false
			}
		}
	}
	return true
}

func prepare(op Operation) (PreparedOperation, error) {
	out := PreparedOperation{Operation: op}
	if op.EntityType != "trip" && op.EntityType != "packing_item" && op.EntityType != "todo" && op.EntityType != "reservation" && op.EntityType != "document" && op.EntityType != "expense_category" && op.EntityType != "trip_member" && op.EntityType != "ledger_entry" {
		return out, apperr.Unprocessable("OFFLINE_OPERATION_NOT_ALLOWED", "该操作尚未实现")
	}
	allowed := []string{}
	required := []string{}
	switch op.Type {
	case "expense_category.create", "expense_category.update":
		allowed = []string{"name", "icon"}
		if op.Type == "expense_category.create" {
			required = []string{"name"}
		}
	case "trip_member.replace":
		allowed = []string{"members"}
		required = allowed
	case "ledger_entry.create":
		allowed = []string{"kind", "amount", "currency_code", "category_id", "occurred_on", "notes", "refunded_entry_id", "attachment_asset_ids", "payer_member_id", "split_mode", "participant_member_ids"}
		required = []string{"kind", "amount", "currency_code", "category_id", "occurred_on", "payer_member_id", "split_mode", "participant_member_ids"}
	case "ledger_entry.update":
		allowed = []string{"amount", "currency_code", "category_id", "occurred_on", "notes", "refunded_entry_id", "attachment_asset_ids", "payer_member_id", "split_mode", "participant_member_ids"}
	case "reservation.create", "reservation.update":
		allowed = []string{"kind", "title", "booking_reference", "transport_number", "provider_name", "start_local", "end_local", "origin", "destination", "address", "contact_name", "contact_phone", "notes"}
		if op.Type == "reservation.create" {
			required = []string{"kind", "title"}
		}
	case "document.create", "document.update":
		allowed = []string{"title", "notes", "asset_id", "reservation_id"}
		if op.Type == "document.create" {
			required = []string{"title", "asset_id"}
		}
	case "packing_item.create":
		allowed = []string{"name", "category", "quantity", "notes", "status"}
		required = []string{"name", "category"}
	case "packing_item.update":
		allowed = []string{"name", "category", "quantity", "notes"}
	case "packing_item.set_status":
		allowed = []string{"status"}
		required = allowed
	case "todo.create":
		allowed = []string{"title", "due_on", "notes", "completed"}
		required = []string{"title"}
	case "todo.update":
		allowed = []string{"title", "due_on", "notes"}
	case "todo.set_completed":
		allowed = []string{"completed"}
		required = allowed
	case "packing_item.reorder", "todo.reorder", "expense_category.reorder":
		allowed = []string{"ordered_ids"}
		required = allowed
	case "trip.create":
		allowed = []string{"name", "start_date", "end_date", "destination", "notes", "timezone", "currency_code", "budget_amount", "self_member_id"}
		required = []string{"name", "start_date", "end_date", "timezone", "currency_code", "self_member_id"}
	case "trip.update":
		allowed = []string{"name", "start_date", "end_date", "destination", "notes", "timezone", "currency_code", "budget_amount", "route_short_mode", "route_short_distance_meters"}
	case "trip.set_archived":
		allowed = []string{"archived"}
		required = allowed
	}
	m, err := object(op.Payload, required, allowed)
	if err != nil {
		return out, apperr.Validation(apperr.Field("payload", "INVALID", err.Error()))
	}
	for k, raw := range m {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if k != "budget_amount" && k != "due_on" && !contentNullable(op.EntityType, k) {
				return out, apperr.Validation(apperr.Field(k, "NOT_NULL", "字段不可为空"))
			}
			continue
		}
		switch k {
		case "members":
			var members []json.RawMessage
			if json.Unmarshal(raw, &members) != nil || members == nil {
				return out, apperr.Validation(apperr.Field(k, "INVALID", "需要完整成员数组"))
			}
			normalized := make([]map[string]any, 0, len(members))
			for _, rawMember := range members {
				fields, e := object(rawMember, []string{"id", "name", "share_percent"}, []string{"id", "name", "share_percent"})
				if e != nil {
					return out, apperr.Validation(apperr.Field(k, "INVALID", e.Error()))
				}
				values := map[string]string{}
				for key, value := range fields {
					var str string
					if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &str) != nil || strings.ContainsRune(str, '\x00') {
						return out, apperr.Validation(apperr.Field(k, "INVALID", "成员字段必须是文本"))
					}
					values[key] = str
				}
				id, e := uuid.Parse(values["id"])
				if e != nil || id == uuid.Nil {
					return out, apperr.Validation(apperr.Field(k, "INVALID", "成员ID无效"))
				}
				percent, e := money.Compact(strings.TrimSpace(values["share_percent"]))
				if e != nil {
					return out, apperr.Validation(apperr.Field(k, "INVALID", "成员比例无效"))
				}
				normalized = append(normalized, map[string]any{"id": id, "name": strings.TrimSpace(values["name"]), "share_percent": percent})
			}
			m[k], _ = json.Marshal(normalized)
		case "archived", "completed":
			var b bool
			if json.Unmarshal(raw, &b) != nil {
				return out, apperr.Validation(apperr.Field(k, "INVALID", "需要布尔值"))
			}
		case "quantity", "route_short_distance_meters":
			var n int32
			if json.Unmarshal(raw, &n) != nil {
				return out, apperr.Validation(apperr.Field(k, "INVALID", "需要整数"))
			}
		case "ordered_ids", "participant_member_ids", "attachment_asset_ids":
			var ids []uuid.UUID
			if json.Unmarshal(raw, &ids) != nil || ids == nil {
				return out, apperr.Validation(apperr.Field(k, "INVALID", "需要ID数组"))
			}
			seen := map[uuid.UUID]bool{}
			for _, id := range ids {
				if id == uuid.Nil || seen[id] {
					return out, apperr.Validation(apperr.Field(k, "INVALID", "ID必须有效且无重复"))
				}
				seen[id] = true
			}
			m[k], _ = json.Marshal(ids)
		default:
			var s string
			if json.Unmarshal(raw, &s) != nil || strings.ContainsRune(s, '\x00') {
				return out, apperr.Validation(apperr.Field(k, "INVALID", "需要有效文本"))
			}
			if op.EntityType != "reservation" && op.EntityType != "document" && (k == "name" || k == "destination" || k == "title") {
				s = strings.TrimSpace(s)
			}
			if k == "budget_amount" || k == "amount" {
				s, err = money.Compact(s)
				if err != nil {
					return out, apperr.Validation(apperr.Field(k, "INVALID", "金额格式错误"))
				}
			}
			if k == "self_member_id" || k == "asset_id" || k == "reservation_id" || k == "category_id" || k == "payer_member_id" || k == "refunded_entry_id" {
				id, e := uuid.Parse(s)
				if e != nil || id == uuid.Nil {
					return out, apperr.Validation(apperr.Field(k, "INVALID", "资源ID无效"))
				}
				s = id.String()
			}
			m[k], _ = json.Marshal(s)
		}
	}
	if op.Type == "trip.create" {
		for _, k := range []string{"destination", "notes"} {
			if _, ok := m[k]; !ok {
				m[k] = json.RawMessage(`""`)
			}
		}
		if _, ok := m["budget_amount"]; !ok {
			m["budget_amount"] = json.RawMessage(`null`)
		}
	}
	if op.Type == "packing_item.create" {
		for k, v := range map[string]string{"quantity": "1", "notes": `""`, "status": `"pending"`} {
			if _, ok := m[k]; !ok {
				m[k] = json.RawMessage(v)
			}
		}
	}
	if op.Type == "todo.create" {
		for k, v := range map[string]string{"due_on": "null", "notes": `""`, "completed": "false"} {
			if _, ok := m[k]; !ok {
				m[k] = json.RawMessage(v)
			}
		}
	}
	if op.Type == "reservation.create" {
		for _, k := range []string{"booking_reference", "address", "notes"} {
			if _, ok := m[k]; !ok {
				m[k] = json.RawMessage(`""`)
			}
		}
		for _, k := range []string{"transport_number", "provider_name", "start_local", "end_local", "origin", "destination", "contact_name", "contact_phone"} {
			if _, ok := m[k]; !ok {
				m[k] = json.RawMessage(`null`)
			}
		}
	}
	if op.Type == "document.create" {
		if _, ok := m["notes"]; !ok {
			m["notes"] = json.RawMessage(`""`)
		}
		if _, ok := m["reservation_id"]; !ok {
			m["reservation_id"] = json.RawMessage(`null`)
		}
	}
	if op.Type == "expense_category.create" {
		if _, ok := m["icon"]; !ok {
			m["icon"] = json.RawMessage(`null`)
		}
	}
	if op.Type == "ledger_entry.create" {
		for k, v := range map[string]string{"notes": `""`, "refunded_entry_id": "null", "attachment_asset_ids": "[]"} {
			if _, ok := m[k]; !ok {
				m[k] = json.RawMessage(v)
			}
		}
	}

	out.Payload, _ = json.Marshal(m)
	out.DependsOn = append([]uuid.UUID{}, op.DependsOn...)
	sort.Slice(out.DependsOn, func(i, j int) bool { return out.DependsOn[i].String() < out.DependsOn[j].String() })
	out.Guards = append([]GuardReference{}, op.Guards...)
	for i := range out.Guards {
		parts := strings.Split(out.Guards[i].ScopeID, "/")
		id, err := uuid.Parse(parts[0])
		if err != nil {
			return out, malformed("guard范围无效")
		}
		parts[0] = id.String()
		out.Guards[i].ScopeID = strings.Join(parts, "/")
	}
	sort.Slice(out.Guards, func(i, j int) bool {
		return out.Guards[i].Kind+out.Guards[i].ScopeID < out.Guards[j].Kind+out.Guards[j].ScopeID
	})
	raw, err := json.Marshal(struct {
		Protocol  int       `json:"protocol"`
		Operation Operation `json:"operation"`
	}{2, out.Operation})
	if err != nil {
		return out, fmt.Errorf("canonical command: %w", err)
	}
	out.Fingerprint = sha256.Sum256(raw)
	return out, nil
}

func contentNullable(entity, field string) bool {
	if entity == "expense_category" {
		return field == "icon"
	}
	if entity == "ledger_entry" {
		return field == "refunded_entry_id"
	}
	if entity == "document" {
		return field == "reservation_id"
	}
	if entity == "reservation" {
		switch field {
		case "transport_number", "provider_name", "start_local", "end_local", "origin", "destination", "contact_name", "contact_phone":
			return true
		}
	}
	return false
}
