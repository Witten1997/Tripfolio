package finance

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/modules/metadata"
)

// SettlementService 实现成员结算（接口设计 2.5、3.8）：只算有效账目与有效成员，不受筛选影响。
type SettlementService struct {
	reader LedgerReader
}

// NewSettlementService 创建服务。
func NewSettlementService(reader LedgerReader) *SettlementService {
	return &SettlementService{reader: reader}
}

// Get 返回旅行成员结算；旅行不存在或非本人 404，回收站中 410 TRIP_DELETED。
func (s *SettlementService) Get(ctx context.Context, a actor.Actor, tripID uuid.UUID) (Settlement, error) {
	info, err := loadLedgerTrip(ctx, s.reader, a.AccountID, tripID)
	if err != nil {
		return Settlement{}, err
	}
	units, ok := metadata.MinorUnits(info.CurrencyCode)
	if !ok {
		return Settlement{}, apperr.Internal(fmt.Errorf("旅行 %s 的币种 %q 不受支持", tripID, info.CurrencyCode))
	}
	rows, err := s.reader.Settlement(ctx, a.AccountID, tripID)
	if err != nil {
		return Settlement{}, apperr.Internal(err)
	}
	out := Settlement{CurrencyCode: info.CurrencyCode, Members: []MemberSettlement{}, Transfers: []SettlementTransfer{}}
	nets := make([]balance, 0, len(rows))
	for i, r := range rows {
		paid, err := money.ParseDecimal(r.Paid)
		if err != nil {
			return Settlement{}, apperr.Internal(fmt.Errorf("成员 %s 的已付金额 %q 无法解析: %w", r.MemberID, r.Paid, err))
		}
		owed, err := money.ParseDecimal(r.Owed)
		if err != nil {
			return Settlement{}, apperr.Internal(fmt.Errorf("成员 %s 的应付金额 %q 无法解析: %w", r.MemberID, r.Owed, err))
		}
		net := paid.Sub(owed)
		out.Members = append(out.Members, MemberSettlement{
			MemberID: r.MemberID, Name: r.Name, IsSelf: r.IsSelf,
			PaidAmount: paid.Format(units), OwedAmount: owed.Format(units), NetAmount: net.Format(units),
		})
		nets = append(nets, balance{id: r.MemberID, amount: net, order: i})
	}
	out.Transfers = planTransfers(nets, units)
	return out, nil
}
