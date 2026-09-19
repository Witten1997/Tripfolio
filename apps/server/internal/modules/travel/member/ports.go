package member

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// TripInfo 是成员所属旅行的窄只读视图。
type TripInfo struct {
	DeletedAt *time.Time
}

// Values 是成员可编辑字段的集合；Update 整体写回。
type Values struct {
	Name         string
	SharePercent string
	SortOrder    int32
}

// Values 取出资源的可编辑字段。
func (r Resource) Values() Values {
	return Values{Name: r.Name, SharePercent: r.SharePercent, SortOrder: r.SortOrder}
}

// Repo 是成员在写事务内的仓储；由 PostgreSQL 适配器绑定到当前事务。
type Repo interface {
	// Trip 读取所属旅行的窄视图；不存在或非本人返回 found=false。
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
	// ListForUpdate 锁定并返回本旅行全部有效成员，按 sort_order、id 升序。
	ListForUpdate(ctx context.Context, accountID, tripID uuid.UUID) ([]Resource, error)
	// IDExists 检查 ID 是否已被现有行（含软删除、其他旅行）或墓碑占用。
	IDExists(ctx context.Context, id uuid.UUID) (bool, error)
	Insert(ctx context.Context, accountID uuid.UUID, r Resource) (Resource, error)
	Update(ctx context.Context, accountID, tripID, id uuid.UUID, v Values, now time.Time) (Resource, error)
	SoftDelete(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) (Resource, error)
	// ReferenceCount 返回把该成员作为付款人或分摊参与人的有效账目数。
	ReferenceCount(ctx context.Context, accountID, tripID, memberID uuid.UUID) (int64, error)
}

// Reader 是事务外的只读仓储。
type Reader interface {
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
	// List 返回本旅行全部有效成员，按 sort_order、id 升序。
	List(ctx context.Context, accountID, tripID uuid.UUID) ([]Resource, error)
}
