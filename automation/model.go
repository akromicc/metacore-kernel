package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Status of an automation row.
const (
	StatusProposed  = "proposed"  // created by the agent, waiting for a person
	StatusActive    = "active"    // scheduled / listening / callable
	StatusPaused    = "paused"    // paused by a person
	StatusError     = "error"     // auto-paused (failures, budget, permission)
	StatusCompleted = "completed" // a one-shot that already ran
	StatusArchived  = "archived"  // deleted (kept for audit)
)

// Origins of an automation.
const (
	OriginChat = "chat"
	OriginUI   = "ui"
	OriginAPI  = "api"
)

// Run statuses.
const (
	RunRunning   = "running"
	RunCompleted = "completed"
	RunSkipped   = "skipped" // a condition stopped it before delivering (no cost)
	RunFailed    = "failed"
)

// Automation is the persisted definition (kind automation or tool).
type Automation struct {
	ID                  uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	OrganizationID      uuid.UUID  `gorm:"type:uuid;not null;index:idx_mc_automations_owner,priority:1" json:"organization_id"`
	Kind                string     `gorm:"size:20;not null;default:automation" json:"kind"`
	ToolName            *string    `gorm:"size:64" json:"tool_name,omitempty"`
	Name                string     `gorm:"size:120;not null" json:"name"`
	Description         string     `gorm:"size:500" json:"description"`
	Status              string     `gorm:"size:20;not null;index:idx_mc_automations_due,priority:1" json:"status"`
	StatusReason        string     `gorm:"size:300" json:"status_reason,omitempty"`
	Spec                string     `gorm:"type:jsonb;not null" json:"-"`
	SpecVersion         string     `gorm:"size:30;not null" json:"spec_version"`
	SpecHash            string     `gorm:"size:20;not null" json:"spec_hash"`
	TriggerType         string     `gorm:"size:20" json:"trigger_type"`
	EventName           string     `gorm:"size:120;index" json:"event_name,omitempty"`
	NextRunAt           *time.Time `gorm:"index:idx_mc_automations_due,priority:2" json:"next_run_at,omitempty"`
	LastRunAt           *time.Time `json:"last_run_at,omitempty"`
	LastStatus          string     `gorm:"size:20" json:"last_status,omitempty"`
	ConsecutiveFailures int        `gorm:"not null;default:0" json:"consecutive_failures"`
	CreatedBy           uuid.UUID  `gorm:"type:uuid;not null;index:idx_mc_automations_owner,priority:2" json:"created_by"`
	AssistedBy          string     `gorm:"size:60" json:"assisted_by,omitempty"`
	Origin              string     `gorm:"size:20;not null" json:"origin"`
	SessionID           *uuid.UUID `gorm:"type:uuid" json:"session_id,omitempty"`
	AuthorizedBy        *uuid.UUID `gorm:"type:uuid" json:"authorized_by,omitempty"`
	AuthorizedAt        *time.Time `json:"authorized_at,omitempty"`
	RunsCount           int        `gorm:"not null;default:0" json:"runs_count"`
	TokensTotal         int        `gorm:"not null;default:0" json:"tokens_total"`
	CostUSDTotal        float64    `gorm:"not null;default:0" json:"cost_usd_total"`
	Version             int        `gorm:"not null;default:1" json:"version"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// TableName pins the table.
func (Automation) TableName() string { return "metacore_automations" }

// ParsedSpec decodes the stored spec.
func (a *Automation) ParsedSpec() (*Spec, error) {
	var s Spec
	if err := json.Unmarshal([]byte(a.Spec), &s); err != nil {
		return nil, fmt.Errorf("automation %s: spec: %w", a.ID, err)
	}
	return &s, nil
}

// Run is one execution (scheduled, event, manual or dry-run).
type Run struct {
	ID             uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	AutomationID   uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:uq_mc_automation_runs_slot,priority:1;index:idx_mc_automation_runs_list,priority:1" json:"automation_id"`
	OrganizationID uuid.UUID  `gorm:"type:uuid;not null;index" json:"organization_id"`
	SlotKey        string     `gorm:"size:120;not null;uniqueIndex:uq_mc_automation_runs_slot,priority:2" json:"slot_key"`
	Trigger        string     `gorm:"size:20;not null" json:"trigger"`
	Status         string     `gorm:"size:20;not null" json:"status"`
	DryRun         bool       `gorm:"not null;default:false" json:"dry_run"`
	Attempt        int        `gorm:"not null;default:1" json:"attempt"`
	StartedAt      time.Time  `gorm:"index:idx_mc_automation_runs_list,priority:2" json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	DurationMS     int64      `json:"duration_ms"`
	Steps          string     `gorm:"type:jsonb" json:"-"`
	Output         string     `gorm:"type:jsonb" json:"-"`
	Error          string     `gorm:"size:1000" json:"error,omitempty"`
	InputHash      string     `gorm:"size:20;index" json:"-"`
	LLMUsed        bool       `gorm:"not null;default:false" json:"llm_used"`
	CacheHit       bool       `gorm:"not null;default:false" json:"cache_hit"`
	InputTokens    int        `gorm:"not null;default:0" json:"input_tokens"`
	OutputTokens   int        `gorm:"not null;default:0" json:"output_tokens"`
	CostUSD        float64    `gorm:"not null;default:0" json:"cost_usd"`
	Deliveries     int        `gorm:"not null;default:0" json:"deliveries"`
	ActorID        *uuid.UUID `gorm:"type:uuid" json:"actor_id,omitempty"`
}

// TableName pins the table.
func (Run) TableName() string { return "metacore_automation_runs" }

// DeliveryRecord marks an item as already delivered by an automation.
type DeliveryRecord struct {
	AutomationID   uuid.UUID `gorm:"type:uuid;primaryKey" json:"automation_id"`
	ItemKey        string    `gorm:"size:200;primaryKey" json:"item_key"`
	OrganizationID uuid.UUID `gorm:"type:uuid;not null" json:"organization_id"`
	RunID          uuid.UUID `gorm:"type:uuid;not null" json:"run_id"`
	CreatedAt      time.Time `json:"created_at"`
}

// TableName pins the table.
func (DeliveryRecord) TableName() string { return "metacore_automation_deliveries" }

// EventRecord marks that an event already fired an automation.
type EventRecord struct {
	AutomationID uuid.UUID `gorm:"type:uuid;primaryKey"`
	EventKey     string    `gorm:"size:200;primaryKey"`
	CreatedAt    time.Time `gorm:"index"`
}

// TableName pins the table.
func (EventRecord) TableName() string { return "metacore_automation_events" }

// Migrate creates or completes the automation tables. Idempotent and safe
// from every replica: on PostgreSQL it runs under an advisory lock.
func Migrate(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			h := fnv.New64a()
			h.Write([]byte("metacore:migrate:metacore_automations"))
			if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(h.Sum64())).Error; err != nil {
				return err
			}
		}
		if err := tx.AutoMigrate(&Automation{}, &Run{}, &DeliveryRecord{}, &EventRecord{}); err != nil {
			return fmt.Errorf("automation: migrate: %w", err)
		}
		// One live dynamic tool per name and org.
		return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uq_mc_automations_tool_name
			ON metacore_automations (organization_id, tool_name)
			WHERE tool_name IS NOT NULL AND status <> 'archived'`).Error
	})
}

func specHash(raw []byte) string {
	h := fnv.New64a()
	h.Write(raw)
	return fmt.Sprintf("%016x", h.Sum64())
}
