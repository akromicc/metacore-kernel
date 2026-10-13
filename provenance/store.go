package provenance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// TableName is the lateral table: one row per changed record (created,
// updated, deleted) that carried a provenance. Business tables get no new
// columns.
const TableName = "metacore_provenance"

// Actions stored per change.
const (
	ActionCreated = "created"
	ActionUpdated = "updated"
	ActionDeleted = "deleted"
)

// Entry is one stored change with its provenance.
type Entry struct {
	ID             uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	OrganizationID *uuid.UUID `gorm:"type:uuid;index:idx_mc_provenance_record,priority:1" json:"organization_id,omitempty"`
	Model          string     `gorm:"size:120;not null;index:idx_mc_provenance_record,priority:2" json:"model"`
	RecordID       string     `gorm:"size:64;not null;index:idx_mc_provenance_record,priority:3" json:"record_id"`
	Action         string     `gorm:"size:20;not null" json:"action"`
	ActorKind      string     `gorm:"size:20;not null" json:"actor_kind"`
	ActorID        *uuid.UUID `gorm:"type:uuid" json:"actor_id,omitempty"`
	ActorName      string     `gorm:"size:80" json:"actor_name,omitempty"`
	OnBehalfOf     *uuid.UUID `gorm:"type:uuid" json:"on_behalf_of,omitempty"`
	AuthorizedBy   *uuid.UUID `gorm:"type:uuid" json:"authorized_by,omitempty"`
	AuthorizedAt   *time.Time `json:"authorized_at,omitempty"`
	Via            string     `gorm:"size:30" json:"via,omitempty"`
	Origin         string     `gorm:"size:160" json:"origin,omitempty"`
	RefKind        string     `gorm:"size:40" json:"ref_kind,omitempty"`
	RefID          string     `gorm:"size:100" json:"ref_id,omitempty"`
	SessionID      *uuid.UUID `gorm:"type:uuid" json:"session_id,omitempty"`
	Summary        string     `gorm:"size:300" json:"summary,omitempty"`
	Mode           string     `gorm:"size:20;not null" json:"mode"`
	Proposed       bool       `gorm:"not null;default:false" json:"proposed,omitempty"`
	CorrelationID  string     `gorm:"size:100" json:"correlation_id,omitempty"`
	OccurredAt     time.Time  `gorm:"not null;index:idx_mc_provenance_record,priority:4" json:"occurred_at"`
}

// TableName pins the table.
func (Entry) TableName() string { return TableName }

// Provenance rebuilds the value carried by the entry.
func (e Entry) Provenance() Provenance {
	return Provenance{
		ActorKind: ActorKind(e.ActorKind), ActorID: e.ActorID, ActorName: e.ActorName,
		OnBehalfOf: e.OnBehalfOf, AuthorizedBy: e.AuthorizedBy, AuthorizedAt: e.AuthorizedAt,
		Via: e.Via, Origin: e.Origin, RefKind: e.RefKind, RefID: e.RefID, SessionID: e.SessionID,
		Summary: e.Summary, Mode: Mode(e.Mode), Proposed: e.Proposed,
	}
}

// NewEntry builds the row for one change. orgID may be uuid.Nil (global
// tables); recordID and model are required.
func NewEntry(orgID uuid.UUID, model, recordID, action string, p Provenance, at time.Time) Entry {
	p = p.Normalize()
	e := Entry{
		ID: uuid.New(), Model: strings.TrimSpace(model), RecordID: strings.TrimSpace(recordID), Action: action,
		ActorKind: string(p.ActorKind), ActorID: p.ActorID, ActorName: clip(p.ActorName, 80),
		OnBehalfOf: p.OnBehalfOf, AuthorizedBy: p.AuthorizedBy, AuthorizedAt: p.AuthorizedAt,
		Via: clip(p.Via, 30), Origin: p.Origin, RefKind: clip(p.RefKind, 40), RefID: clip(p.RefID, 100),
		SessionID: p.SessionID, Summary: p.Summary, Mode: string(p.Mode), Proposed: p.Proposed,
		OccurredAt: at.UTC(),
	}
	if orgID != uuid.Nil {
		o := orgID
		e.OrganizationID = &o
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	return e
}

// Migrate creates or completes the lateral table. Idempotent; on PostgreSQL
// it also adds the partial unique index that keeps ONE creation row per
// record (the backfill and retried writes rely on it).
func Migrate(db *gorm.DB) error {
	if db == nil {
		return errors.New("provenance: nil db")
	}
	if err := db.AutoMigrate(&Entry{}); err != nil {
		return fmt.Errorf("provenance: migrate: %w", err)
	}
	if db.Dialector.Name() == "postgres" || db.Dialector.Name() == "sqlite" {
		if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uq_mc_provenance_created ON ` + TableName +
			` (model, record_id) WHERE action = 'created'`).Error; err != nil {
			return fmt.Errorf("provenance: unique created index: %w", err)
		}
	}
	return nil
}

// Store reads and writes the lateral table.
type Store struct {
	db *gorm.DB
}

// NewStore builds a Store over db.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// session is a clean session that never stamps itself and keeps the caller's
// transaction (ConnPool) when db is a tx.
func session(ctx context.Context, db *gorm.DB) *gorm.DB {
	return db.Session(&gorm.Session{NewDB: true, Context: Without(ctx)})
}

// Record stores changes. Creation rows are idempotent per (model, record_id):
// a second "created" for the same record keeps the first (the original author
// never gets overwritten by a retry or a backfill).
func (s *Store) Record(ctx context.Context, entries ...Entry) error {
	return record(session(ctx, s.db), entries)
}

func record(tx *gorm.DB, entries []Entry) error {
	var created, other []Entry
	for _, e := range entries {
		if e.Model == "" || e.RecordID == "" || e.ActorKind == "" {
			continue
		}
		if e.ID == uuid.Nil {
			e.ID = uuid.New()
		}
		if e.Action == ActionCreated {
			created = append(created, e)
		} else {
			other = append(other, e)
		}
	}
	if len(created) > 0 {
		if err := tx.Clauses(clause.OnConflict{
			Columns:     []clause.Column{{Name: "model"}, {Name: "record_id"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "action = 'created'"}}},
			DoNothing:   true,
		}).Create(&created).Error; err != nil {
			return err
		}
	}
	if len(other) > 0 {
		if err := tx.Create(&other).Error; err != nil {
			return err
		}
	}
	return nil
}

// Created returns the creation provenance of each record id (tables: one
// query per page). orgID uuid.Nil skips the tenant filter (global tables).
// Silent: a missing table (old databases, tests) yields an empty map.
func (s *Store) Created(ctx context.Context, orgID uuid.UUID, model string, ids []string) map[string]Entry {
	out := map[string]Entry{}
	if s == nil || s.db == nil || len(ids) == 0 {
		return out
	}
	q := session(ctx, s.db).Session(&gorm.Session{Logger: logger.Discard}).
		Where("record_id IN ? AND action = ?", ids, ActionCreated)
	if model != "" {
		q = q.Where("model = ?", model)
	}
	if orgID != uuid.Nil {
		q = q.Where("(organization_id = ? OR organization_id IS NULL)", orgID)
	}
	var rows []Entry
	if err := q.Find(&rows).Error; err != nil {
		return out
	}
	for _, r := range rows {
		out[r.RecordID] = r
	}
	return out
}

// History returns the changes of one record, oldest first (limit ≤ 0 → 200).
func (s *Store) History(ctx context.Context, orgID uuid.UUID, model, recordID string, limit int) ([]Entry, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := session(ctx, s.db).Where("record_id = ?", recordID)
	if model != "" {
		q = q.Where("model = ?", model)
	}
	if orgID != uuid.Nil {
		q = q.Where("(organization_id = ? OR organization_id IS NULL)", orgID)
	}
	var rows []Entry
	err := q.Order("occurred_at ASC, id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

// RecordView is what the read API returns for one record.
type RecordView struct {
	Created    *Entry  `json:"created,omitempty"`
	LastChange *Entry  `json:"last_change,omitempty"`
	History    []Entry `json:"history"`
}

// ForRecord bundles creation, last change and history of one record.
func (s *Store) ForRecord(ctx context.Context, orgID uuid.UUID, model, recordID string, limit int) (RecordView, error) {
	h, err := s.History(ctx, orgID, model, recordID, limit)
	if err != nil {
		return RecordView{History: []Entry{}}, err
	}
	v := RecordView{History: h}
	if v.History == nil {
		v.History = []Entry{}
	}
	for i := range h {
		if h[i].Action == ActionCreated && v.Created == nil {
			v.Created = &h[i]
		}
	}
	if n := len(h); n > 0 {
		v.LastChange = &h[n-1]
	}
	return v, nil
}
