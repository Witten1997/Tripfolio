package account

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore 是 Store 的内存实现，供本包与其他包的单元测试使用。
type MemoryStore struct {
	mu         sync.Mutex
	accounts   map[uuid.UUID]Account
	sessions   map[uuid.UUID]Session
	challenges map[uuid.UUID]Challenge
	seeded     []uuid.UUID
}

// NewMemoryStore 创建空存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		accounts:   map[uuid.UUID]Account{},
		sessions:   map[uuid.UUID]Session{},
		challenges: map[uuid.UUID]Challenge{},
	}
}

var _ Store = (*MemoryStore)(nil)

func (m *MemoryStore) AccountByEmailKey(_ context.Context, key string) (Account, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if a.EmailKey == key {
			return a, true, nil
		}
	}
	return Account{}, false, nil
}

func (m *MemoryStore) AccountByID(_ context.Context, id uuid.UUID) (Account, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	return a, ok, nil
}

func (m *MemoryStore) AccountExists(ctx context.Context, key string) (bool, error) {
	_, ok, err := m.AccountByEmailKey(ctx, key)
	return ok, err
}

func (m *MemoryStore) CreateAccount(_ context.Context, a Account) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.accounts {
		if existing.EmailKey == a.EmailKey {
			return Account{}, errors.New(`duplicate key value violates unique constraint "accounts_email_key_unique"`)
		}
	}
	m.accounts[a.ID] = a
	m.seeded = append(m.seeded, a.ID)
	return a, nil
}

func (m *MemoryStore) UpdatePassword(_ context.Context, id uuid.UUID, hash string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.accounts[id]
	a.PasswordHash = hash
	a.UpdatedAt = now
	m.accounts[id] = a
	return nil
}

func (m *MemoryStore) UpdateProfile(_ context.Context, id uuid.UUID, nickname string, avatar *uuid.UUID, tz string, now time.Time) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.accounts[id]
	a.Nickname, a.AvatarAssetID, a.DefaultTimezone, a.UpdatedAt = nickname, avatar, tz, now
	a.Version++
	m.accounts[id] = a
	return a, nil
}

func (m *MemoryStore) SetAccountStatus(_ context.Context, id uuid.UUID, status string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.accounts[id]
	a.Status, a.UpdatedAt = status, now
	a.Version++
	m.accounts[id] = a
	return nil
}

func (m *MemoryStore) CreateSession(_ context.Context, s Session) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s
	return s, nil
}

func (m *MemoryStore) SessionByID(_ context.Context, id uuid.UUID) (Session, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok, nil
}

func (m *MemoryStore) RotateSessionTx(_ context.Context, id uuid.UUID, fn func(Session) (SessionRotation, error)) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return Session{}, ErrNoSession
	}
	rot, err := fn(s)
	if err != nil {
		return Session{}, err
	}
	s.RefreshTokenHash = rot.RefreshTokenHash
	s.PreviousRefreshTokenHash = rot.PreviousRefreshTokenHash
	s.PreviousRotatedAt = rot.PreviousRotatedAt
	s.CSRFTokenHash = rot.CSRFTokenHash
	s.LastSeenAt = rot.LastSeenAt
	m.sessions[id] = s
	return s, nil
}

func (m *MemoryStore) TouchSession(_ context.Context, id uuid.UUID, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		s.LastSeenAt = now
		m.sessions[id] = s
	}
	return nil
}

func (m *MemoryStore) SetReauthenticated(_ context.Context, id uuid.UUID, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		t := now
		s.ReauthenticatedAt = &t
		m.sessions[id] = s
	}
	return nil
}

func (m *MemoryStore) RevokeSession(_ context.Context, id uuid.UUID, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok && s.RevokedAt == nil {
		t := now
		s.RevokedAt = &t
		m.sessions[id] = s
	}
	return nil
}

func (m *MemoryStore) RevokeAccountSessions(_ context.Context, accountID uuid.UUID, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		if s.AccountID == accountID && s.RevokedAt == nil {
			t := now
			s.RevokedAt = &t
			m.sessions[id] = s
		}
	}
	return nil
}

func (m *MemoryStore) RevokeOtherSessions(_ context.Context, accountID, keep uuid.UUID, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		if s.AccountID == accountID && id != keep && s.RevokedAt == nil {
			t := now
			s.RevokedAt = &t
			m.sessions[id] = s
		}
	}
	return nil
}

func (m *MemoryStore) ListActiveSessions(_ context.Context, accountID uuid.UUID, now time.Time) ([]Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Session
	for _, s := range m.sessions {
		if s.AccountID == accountID && s.Active(now) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *MemoryStore) IssueChallengeTx(_ context.Context, c Challenge, resendAfter time.Duration, perHour int) (Challenge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest time.Time
	count := 0
	for _, existing := range m.challenges {
		if existing.EmailKey != c.EmailKey || existing.Purpose != c.Purpose {
			continue
		}
		if existing.CreatedAt.After(latest) {
			latest = existing.CreatedAt
		}
		if c.CreatedAt.Sub(existing.CreatedAt) < time.Hour {
			count++
		}
	}
	if !latest.IsZero() && c.CreatedAt.Sub(latest) < resendAfter {
		return Challenge{}, ErrChallengeTooSoon
	}
	if count >= perHour {
		return Challenge{}, ErrChallengeQuotaExceeded
	}
	for id, existing := range m.challenges {
		if existing.EmailKey == c.EmailKey && existing.Purpose == c.Purpose && existing.ConsumedAt == nil && existing.InvalidatedAt == nil {
			t := c.CreatedAt
			existing.InvalidatedAt = &t
			m.challenges[id] = existing
		}
	}
	m.challenges[c.ID] = c
	return c, nil
}

func (m *MemoryStore) VerifyChallengeTx(_ context.Context, id uuid.UUID, fn func(Challenge) (bool, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.challenges[id]
	if !ok {
		return errors.New("challenge not found")
	}
	consume, err := fn(c)
	if consume {
		t := time.Now()
		c.ConsumedAt = &t
	} else {
		c.Attempts++
	}
	m.challenges[id] = c
	return err
}

func (m *MemoryStore) SetChallengeDelivery(_ context.Context, id uuid.UUID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.challenges[id]; ok {
		c.DeliveryStatus = status
		m.challenges[id] = c
	}
	return nil
}

// Challenge 返回挑战，供测试读取。
func (m *MemoryStore) Challenge(id uuid.UUID) (Challenge, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.challenges[id]
	return c, ok
}

// Session 返回会话，供测试读取。
func (m *MemoryStore) Session(id uuid.UUID) (Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}
