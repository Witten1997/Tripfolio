package sync

import (
	"context"
	"strconv"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/modules/account"
)

type Service struct {
	reader Reader
	codec  paging.Codec
	clock  clock.Clock
}

func NewService(reader Reader, codec paging.Codec, clk clock.Clock) *Service {
	return &Service{reader: reader, codec: codec, clock: clk}
}

func nativeActor(a actor.Actor) error {
	if a.AccountID == uuid.Nil || a.SessionID == uuid.Nil {
		return apperr.Unauthorized("SESSION_EXPIRED", "请重新登录")
	}
	if a.ClientKind != actor.ClientHarmony && a.ClientKind != actor.ClientAndroid {
		return apperr.Forbidden("NATIVE_SESSION_REQUIRED", "同步需要原生客户端会话")
	}
	return nil
}

func validState(st State) error {
	if st.AccountStatus != "active" {
		return account.StatusError(st.AccountStatus)
	}
	if !st.SessionActive {
		return apperr.Unauthorized("SESSION_EXPIRED", "请重新登录")
	}
	if st.Epoch == uuid.Nil || st.RetainedAfterSeq < 0 || st.LastSeq < st.RetainedAfterSeq {
		return apperr.New(503, "DEPENDENCY_UNAVAILABLE", "同步状态不可用")
	}
	return nil
}

func (s *Service) Status(ctx context.Context, a actor.Actor) (Status, error) {
	var result Status
	if err := nativeActor(a); err != nil {
		return result, err
	}
	err := s.reader.Read(ctx, a, func(v View) error {
		st, err := v.State(ctx)
		if err != nil {
			return err
		}
		if err := validState(st); err != nil {
			return err
		}
		result = Status{SupportedProtocolVersions: []int{ProtocolVersion}, RecommendedProtocolVersion: ProtocolVersion,
			SyncEpoch: st.Epoch, ServerTime: s.clock.Now().UTC(), RetentionDays: 90, SnapshotTTLSeconds: 86400,
			Limits: Limits{100, 65536, 1048576, 100}}
		return nil
	})
	return result, err
}

func (s *Service) Changes(ctx context.Context, a actor.Actor, protocol, token string, limit int) (Page, error) {
	var result Page
	if err := nativeActor(a); err != nil {
		return result, err
	}
	if protocol != "2" {
		return result, apperr.New(426, "SYNC_PROTOCOL_UNSUPPORTED", "需要同步协议版本 2")
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return result, apperr.Validation(apperr.Field("limit", "INVALID", "limit 须在 1–500 之间"))
	}
	c, after, upper, err := s.decode(a.AccountID, token)
	if err != nil {
		return result, err
	}
	err = s.reader.Read(ctx, a, func(v View) error {
		st, err := v.State(ctx)
		if err != nil {
			return err
		}
		if err := validState(st); err != nil {
			return err
		}
		if c.Epoch != st.Epoch {
			return apperr.Conflicted("SYNC_EPOCH_MISMATCH", "数据已恢复，请重新建立基线并核对旧操作")
		}
		h := st.LastSeq
		if upper != nil {
			h = *upper
		}
		if after > h || h > st.LastSeq {
			return paging.InvalidCursor()
		}
		if after < st.RetainedAfterSeq {
			return apperr.Gone("CURSOR_EXPIRED", "变更历史已过期，请重新建立基线")
		}
		rows, err := v.Changes(ctx, after, h, limit)
		if err != nil {
			return err
		}
		out := make([]Change, 0, len(rows))
		position := after
		for _, row := range rows {
			if position == h || row.Seq != position+1 || row.BatchEndSeq < row.Seq || row.BatchEndSeq > h {
				return apperr.Gone("CURSOR_EXPIRED", "变更历史不连续，请重新建立基线")
			}
			change, err := project(row)
			if err != nil {
				return err
			}
			out = append(out, change)
			position = row.Seq
		}
		if position < h && len(rows) < limit {
			return apperr.Gone("CURSOR_EXPIRED", "变更历史不完整，请重新建立基线")
		}
		next := cursor{Protocol: ProtocolVersion, Epoch: st.Epoch, Purpose: "checkpoint", After: strconv.FormatInt(position, 10)}
		if position < h {
			high := strconv.FormatInt(h, 10)
			next.Purpose = "page"
			next.Upper = &high
		}
		signed, err := s.codec.Encode(a.AccountID, changesScope, next)
		if err != nil {
			return apperr.Internal(err)
		}
		result = Page{SyncEpoch: st.Epoch, Changes: out, NextCursor: signed, HasMore: position < h}
		return nil
	})
	return result, err
}
