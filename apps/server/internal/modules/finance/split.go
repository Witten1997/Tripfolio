package finance

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/modules/travel/member"
)

// SplitMode 是分摊模式：even 按参与人等分，ratio 按参与人的成员百分比归一化。
type SplitMode string

const (
	SplitEven     SplitMode = "even"
	SplitRatio    SplitMode = "ratio"
	SplitPersonal SplitMode = "personal"
)

// Valid 判断是否为已知模式。
func (m SplitMode) Valid() bool { return m == SplitEven || m == SplitRatio || m == SplitPersonal }

// LedgerSplit 是一位参与人的份额（接口设计 3.5 LedgerSplit）。
type LedgerSplit struct {
	MemberID uuid.UUID `json:"member_id"`
	Amount   string    `json:"amount"`
}

// splitPlan 是已校验的分摊输入：付款人、模式与按请求顺序排列的参与人。
type splitPlan struct {
	Payer        uuid.UUID
	Mode         SplitMode
	Participants []member.Resource
}

// participantIDs 返回参与人 ID 顺序。
func (p splitPlan) participantIDs() []uuid.UUID {
	out := make([]uuid.UUID, 0, len(p.Participants))
	for _, m := range p.Participants {
		out = append(out, m.ID)
	}
	return out
}

func splitMemberIDs(splits []LedgerSplit) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(splits))
	for _, s := range splits {
		out = append(out, s.MemberID)
	}
	return out
}

func validateSplitMode(raw *string) (*SplitMode, *apperr.FieldError) {
	if raw == nil {
		return nil, nil
	}
	m := SplitMode(*raw)
	if !m.Valid() {
		e := apperr.Field("split_mode", "INVALID", "分摊模式须为 even、ratio 或 personal")
		return nil, &e
	}
	return &m, nil
}

func validateParticipants(ids []uuid.UUID) *apperr.FieldError {
	if ids == nil {
		return nil
	}
	if len(ids) < 1 || len(ids) > member.MaxMembers {
		e := apperr.Field("participant_member_ids", "INVALID", fmt.Sprintf("参与人须为 1–%d 人", member.MaxMembers))
		return &e
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			e := apperr.Field("participant_member_ids", "INVALID", "必须是 UUID")
			return &e
		}
		if _, dup := seen[id]; dup {
			e := apperr.Field("participant_member_ids", "DUPLICATE", "参与人重复")
			return &e
		}
		seen[id] = struct{}{}
	}
	return nil
}

// resolveSplitPlan 把付款人、模式与参与人解析为有效成员：payer 为 nil 取「我」，mode 为 nil 取 even，
// participants 为 nil 取全部有效成员。成员不存在或已删除返回 422 INVALID_REFERENCE。
func resolveSplitPlan(members []member.Resource, payer *uuid.UUID, mode *SplitMode, participants []uuid.UUID) (splitPlan, error) {
	byID := make(map[uuid.UUID]member.Resource, len(members))
	var self *member.Resource
	for i := range members {
		byID[members[i].ID] = members[i]
		if members[i].IsSelf {
			self = &members[i]
		}
	}
	plan := splitPlan{Mode: SplitEven}
	if mode != nil {
		plan.Mode = *mode
	}
	if plan.Mode == SplitPersonal {
		if self == nil {
			return splitPlan{}, invalidReference("旅行没有成员「我」，无法记录个人账单")
		}
		plan.Payer = self.ID
		plan.Participants = []member.Resource{*self}
		return plan, nil
	}
	switch {
	case payer != nil:
		if _, ok := byID[*payer]; !ok {
			return splitPlan{}, invalidReference("付款人不是本旅行的有效成员")
		}
		plan.Payer = *payer
	case self != nil:
		plan.Payer = self.ID
	default:
		return splitPlan{}, invalidReference("旅行没有成员「我」，无法确定付款人")
	}
	if participants == nil {
		plan.Participants = append([]member.Resource(nil), members...)
	} else {
		for _, id := range participants {
			m, ok := byID[id]
			if !ok {
				return splitPlan{}, invalidReference(fmt.Sprintf("参与人 %s 不是本旅行的有效成员", id))
			}
			plan.Participants = append(plan.Participants, m)
		}
	}
	if len(plan.Participants) == 0 {
		return splitPlan{}, apperr.Validation(apperr.Field("participant_member_ids", "INVALID", "至少需要一位参与人"))
	}
	if plan.Mode == SplitRatio {
		total := money.Zero()
		for _, m := range plan.Participants {
			d, err := money.ParseDecimal(m.SharePercent)
			if err != nil {
				return splitPlan{}, fmt.Errorf("成员 %s 的百分比 %q 无法解析: %w", m.ID, m.SharePercent, err)
			}
			total = total.Add(d)
		}
		if total.IsZero() {
			return splitPlan{}, apperr.Validation(apperr.Field("participant_member_ids", "RATIO_UNAVAILABLE", "参与人的分摊百分比之和为 0，无法按比例分摊"))
		}
	}
	return plan, nil
}

// computeSplits 按币种最小单位计算各参与人份额：向下取整后余数按参与人顺序逐个加一个最小单位，
// 份额之和恒等于 amount。返回份额列表与「我」的份额（不参与时为 0）。
func computeSplits(amount string, minorUnits int, plan splitPlan) ([]LedgerSplit, string, error) {
	total, err := money.ParseDecimal(amount)
	if err != nil {
		return nil, "", fmt.Errorf("金额 %q 无法解析: %w", amount, err)
	}
	quantum := money.Quantum(minorUnits)
	units := new(big.Int).Quo(total.Units(), quantum)
	n := len(plan.Participants)
	shares := make([]*big.Int, n)
	remainder := new(big.Int).Set(units)
	switch plan.Mode {
	case SplitRatio:
		weights := make([]*big.Int, n)
		sum := new(big.Int)
		for i, m := range plan.Participants {
			d, err := money.ParseDecimal(m.SharePercent)
			if err != nil {
				return nil, "", fmt.Errorf("成员 %s 的百分比 %q 无法解析: %w", m.ID, m.SharePercent, err)
			}
			weights[i] = d.Units()
			sum.Add(sum, weights[i])
		}
		for i := range shares {
			shares[i] = new(big.Int).Mul(units, weights[i])
			shares[i].Quo(shares[i], sum)
			remainder.Sub(remainder, shares[i])
		}
	default:
		base := new(big.Int).Quo(units, big.NewInt(int64(n)))
		for i := range shares {
			shares[i] = new(big.Int).Set(base)
			remainder.Sub(remainder, base)
		}
	}
	one := big.NewInt(1)
	for i := 0; remainder.Sign() > 0 && i < n; i++ {
		shares[i].Add(shares[i], one)
		remainder.Sub(remainder, one)
	}
	personal := money.Zero().Format(minorUnits)
	out := make([]LedgerSplit, 0, n)
	for i, m := range plan.Participants {
		value := money.FromUnits(new(big.Int).Mul(shares[i], quantum)).Format(minorUnits)
		out = append(out, LedgerSplit{MemberID: m.ID, Amount: value})
		if m.IsSelf {
			personal = value
		}
	}
	return out, personal, nil
}

// MemberSettlement 是单个成员的结算（接口设计 3.8）。
type MemberSettlement struct {
	MemberID   uuid.UUID `json:"member_id"`
	Name       string    `json:"name"`
	IsSelf     bool      `json:"is_self"`
	PaidAmount string    `json:"paid_amount"`
	OwedAmount string    `json:"owed_amount"`
	NetAmount  string    `json:"net_amount"`
}

// SettlementTransfer 是一笔建议转账。
type SettlementTransfer struct {
	FromMemberID uuid.UUID `json:"from_member_id"`
	ToMemberID   uuid.UUID `json:"to_member_id"`
	Amount       string    `json:"amount"`
}

// Settlement 是旅行成员结算响应（接口设计 3.8 TripSettlement）。
type Settlement struct {
	CurrencyCode string               `json:"currency_code"`
	Members      []MemberSettlement   `json:"members"`
	Transfers    []SettlementTransfer `json:"transfers"`
}

// MemberAggregate 是仓储返回的成员原始聚合：Paid、Owed 为 NUMERIC 文本，可为负。
type MemberAggregate struct {
	MemberID uuid.UUID
	Name     string
	IsSelf   bool
	Paid     string
	Owed     string
}

type balance struct {
	id     uuid.UUID
	amount money.Decimal
	order  int
}

// planTransfers 用贪心生成最少转账：净额为负者向净额为正者转账，双方都按金额从大到小配对。
func planTransfers(nets []balance, minorUnits int) []SettlementTransfer {
	var creditors, debtors []balance
	for _, b := range nets {
		switch b.amount.Sign() {
		case 1:
			creditors = append(creditors, b)
		case -1:
			debtors = append(debtors, balance{id: b.id, amount: money.Zero().Sub(b.amount), order: b.order})
		}
	}
	byAmountDesc := func(list []balance) {
		sort.SliceStable(list, func(i, j int) bool {
			if c := list[i].amount.Cmp(list[j].amount); c != 0 {
				return c > 0
			}
			return list[i].order < list[j].order
		})
	}
	byAmountDesc(creditors)
	byAmountDesc(debtors)
	transfers := []SettlementTransfer{}
	i, j := 0, 0
	for i < len(creditors) && j < len(debtors) {
		pay := creditors[i].amount
		if debtors[j].amount.Cmp(pay) < 0 {
			pay = debtors[j].amount
		}
		if pay.Sign() > 0 {
			transfers = append(transfers, SettlementTransfer{FromMemberID: debtors[j].id, ToMemberID: creditors[i].id, Amount: pay.Format(minorUnits)})
		}
		creditors[i].amount = creditors[i].amount.Sub(pay)
		debtors[j].amount = debtors[j].amount.Sub(pay)
		if creditors[i].amount.IsZero() {
			i++
		}
		if debtors[j].amount.IsZero() {
			j++
		}
	}
	return transfers
}
