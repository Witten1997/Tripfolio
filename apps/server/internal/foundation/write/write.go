// Package write 定义统一写事务的抽象（总览 4.1）：写入范围、变更记录、结果与请求指纹。
// 具体实现在 adapters/postgres/pgcore；业务模块只依赖这里的接口。
package write

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/types"
)

// ChangeKind 是同步变更类型。
type ChangeKind string

const (
	ChangeUpsert   ChangeKind = "upsert"
	ChangeDelete   ChangeKind = "delete"
	ChangePurge    ChangeKind = "purge"
	ChangeRedacted ChangeKind = "redacted"
)

// Change 是一条进入同步日志的资源变更。Snapshot 是与 GET 响应相同的资源结构（仅 upsert）。
type Change struct {
	EntityType       string
	EntityID         uuid.UUID
	TripID           *uuid.UUID
	Version          int64
	Kind             ChangeKind
	Snapshot         any
	ChangedFields    []string
	RequiresSnapshot bool
}

// EntityRef 指向一个资源及其版本；任务等没有版本时 Version 为 nil。
type EntityRef struct {
	Type    string         `json:"type"`
	ID      uuid.UUID      `json:"id"`
	Version *types.Version `json:"version"`
}

// Ref 构造带版本的引用。
func Ref(entityType string, id uuid.UUID, version int64) EntityRef {
	v := types.Version(version)
	return EntityRef{Type: entityType, ID: id, Version: &v}
}

// JobArgs 与 river.JobArgs 的方法集相同，使基础包不依赖 River。
type JobArgs interface {
	Kind() string
}

// Scope 是一次写事务内的记录器：业务代码通过它登记变更、警告、主资源、附加受影响资源与后台任务。
type Scope interface {
	Record(change Change)
	Warn(code string)
	SetPrimary(ref EntityRef)
	// AddAffected 登记没有变更日志但出现在 affected 中的资源，例如永久清理任务。
	AddAffected(ref EntityRef)
	Enqueue(args JobArgs) error
}

// AffectedRefs 汇总 affected：primary 在前，其后是各变更资源与附加引用，按类型与 ID 去重。
func AffectedRefs(primary *EntityRef, changes []Change, extra []EntityRef) []EntityRef {
	seen := map[string]struct{}{}
	out := []EntityRef{}
	add := func(ref EntityRef) {
		key := ref.Type + ":" + ref.ID.String()
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	if primary != nil {
		add(*primary)
	}
	for _, c := range changes {
		add(Ref(c.EntityType, c.EntityID, c.Version))
	}
	for _, ref := range extra {
		add(ref)
	}
	return out
}

// Result 是写操作的统一响应（接口设计 1.4 WriteResult）。
type Result struct {
	OperationID  uuid.UUID   `json:"operation_id"`
	Primary      *EntityRef  `json:"primary"`
	Affected     []EntityRef `json:"affected"`
	CommitCursor *string     `json:"commit_cursor"`
	Warnings     []string    `json:"warnings"`
	Replayed     bool        `json:"replayed"`
	Data         any         `json:"data"`
}

// Request 标识一次写操作：账号、操作编号、类型与规范化指纹。
type Request struct {
	AccountID     uuid.UUID
	OperationID   uuid.UUID
	OperationType string
	Fingerprint   [32]byte
	// SingleChangeFastPath combines the lock/receipt read and the final writes for a one-change transaction.
	SingleChangeFastPath bool
	BatchChangesFastPath bool
}

// Fingerprint 计算规范化指纹（接口设计 1.2）：操作类型、目标标识、基线版本与命令结构体的确定性 JSON。
// 命令中的金额必须已按币种规范化；结构体字段顺序固定，缺省与显式 null 由字段类型区分。
func Fingerprint(operationType, target string, baseVersion *int64, command any) [32]byte {
	body, err := json.Marshal(command)
	if err != nil {
		body = []byte(fmt.Sprintf("!marshal-error:%v", err))
	}
	h := sha256.New()
	h.Write([]byte(operationType))
	h.Write([]byte{0})
	h.Write([]byte(target))
	h.Write([]byte{0})
	if baseVersion != nil {
		fmt.Fprintf(h, "%d", *baseVersion)
	}
	h.Write([]byte{0})
	h.Write(body)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// MergeSource 读取基线之后各版本的变更字段，由 pgcore 在事务内实现。
type MergeSource interface {
	// ChangedFieldsSince 返回 (baseVersion, currentVersion] 各版本 changed_fields 的并集；
	// complete 为 false 表示部分版本的日志已过期，无法判断。
	ChangedFieldsSince(ctx context.Context, accountID uuid.UUID, entityType string, entityID uuid.UUID, baseVersion, currentVersion int64) (fields []string, complete bool, err error)
}

// PatchDecision 是字段级合并的结论。
type PatchDecision struct {
	// Merge 为 true 表示可以直接应用。
	Merge bool
	// Merged 为 true 表示基线落后但字段不相交，应返回 MERGED_WITH_NEWER_VERSION 警告。
	Merged bool
	// Conflicting 是相交字段；nil 表示无法判断。
	Conflicting []string
}

// ResolvePatch 按接口设计 1.2 判断局部更新能否应用。
func ResolvePatch(ctx context.Context, src MergeSource, accountID uuid.UUID, entityType string, entityID uuid.UUID, baseVersion, currentVersion int64, submitted []string) (PatchDecision, error) {
	if baseVersion == currentVersion {
		return PatchDecision{Merge: true}, nil
	}
	if baseVersion > currentVersion {
		return PatchDecision{Conflicting: nil}, nil
	}
	changed, complete, err := src.ChangedFieldsSince(ctx, accountID, entityType, entityID, baseVersion, currentVersion)
	if err != nil {
		return PatchDecision{}, err
	}
	if !complete {
		return PatchDecision{Conflicting: nil}, nil
	}
	set := make(map[string]struct{}, len(changed))
	for _, f := range changed {
		set[f] = struct{}{}
	}
	var conflicting []string
	for _, f := range submitted {
		if _, ok := set[f]; ok {
			conflicting = append(conflicting, f)
		}
	}
	if len(conflicting) > 0 {
		sort.Strings(conflicting)
		return PatchDecision{Conflicting: conflicting}, nil
	}
	return PatchDecision{Merge: true, Merged: true}, nil
}

// 警告代码清单（接口设计 1.4）。
const (
	WarnMergedWithNewerVersion       = "MERGED_WITH_NEWER_VERSION"
	WarnRefundsUnlinked              = "REFUNDS_UNLINKED"
	WarnItineraryOutsideTripDates    = "ITINERARY_OUTSIDE_TRIP_DATES"
	WarnTimezoneInterpretationChange = "TIMEZONE_INTERPRETATION_CHANGED"
)
