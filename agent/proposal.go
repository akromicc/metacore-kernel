package agent

import (
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MutationProposal is a staged create/update/delete waiting for explicit user approval.
type MutationProposal struct {
	ID             uuid.UUID         `json:"id"`
	OrganizationID uuid.UUID         `json:"organization_id"`
	UserID         uuid.UUID         `json:"user_id"`
	SessionID      uuid.UUID         `json:"session_id"`
	Action         string            `json:"action"` // create | update | delete
	Model          string            `json:"model"`
	ModelTitle     string            `json:"model_title,omitempty"`
	RecordID       string            `json:"record_id,omitempty"`
	Before         map[string]any    `json:"before,omitempty"`
	After          map[string]any    `json:"after,omitempty"`
	Data           map[string]any    `json:"data,omitempty"`
	Status         string            `json:"status"` // pending | approved | rejected
	CreatedAt      time.Time         `json:"created_at"`
	FieldLabels    map[string]string `json:"field_labels,omitempty"`
	LineItems      []map[string]any  `json:"line_items,omitempty"` // nested order/sale lines preview
	Embed          map[string]any    `json:"embed,omitempty"`      // chat embed preview payload
}

// ProposalStore is an in-memory pending-mutation store (org+user scoped).
type ProposalStore struct {
	mu   sync.RWMutex
	byID map[uuid.UUID]*MutationProposal
}

var GlobalProposalStore = NewProposalStore()

func NewProposalStore() *ProposalStore {
	return &ProposalStore{byID: make(map[uuid.UUID]*MutationProposal)}
}

func (s *ProposalStore) Put(p *MutationProposal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[p.ID] = p
}

func (s *ProposalStore) Get(id uuid.UUID) (*MutationProposal, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byID[id]
	return p, ok
}

func (s *ProposalStore) ListPending(sessionID, userID uuid.UUID) []*MutationProposal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*MutationProposal, 0)
	for _, p := range s.byID {
		if p.SessionID == sessionID && p.UserID == userID && p.Status == "pending" {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (s *ProposalStore) SetStatus(id uuid.UUID, status string) (*MutationProposal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[id]
	if !ok {
		return nil, false
	}
	p.Status = status
	return p, true
}
