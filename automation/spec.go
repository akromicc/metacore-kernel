package automation

import (
	"encoding/json"
)

// SpecVersion is the declarative automation contract this package executes.
const SpecVersion = "automation/v1"

// Kinds of automation rows.
const (
	KindAutomation = "automation" // has a trigger and runs on its own
	KindTool       = "tool"       // a "dynamic tool": composition exposed to the agent
)

// Trigger types.
const (
	TriggerCron     = "cron"
	TriggerInterval = "interval"
	TriggerOnce     = "once"
	TriggerEvent    = "event"
	TriggerManual   = "manual" // run-now from the UI/agent; never stored as a spec trigger
)

// Step types (the whitelist). There is deliberately no http, sql, shell or
// code step.
const (
	StepTool      = "tool"
	StepFilter    = "filter"
	StepCondition = "condition"
	StepTemplate  = "template"
	StepSummarize = "summarize"
	StepDeliver   = "deliver"
)

// Delivery channels a spec may name. The host decides which ones it supports
// (Policy.Channels) and how each reaches the person.
const (
	ChannelInApp    = "in_app"
	ChannelPush     = "push"
	ChannelEmail    = "email"
	ChannelWhatsApp = "whatsapp"
)

// Spec is the declarative definition stored as JSON in the automation row.
// It is validated against the embedded JSON Schema and then semantically
// (Validate) before it is stored or executed.
type Spec struct {
	SchemaVersion string       `json:"schema_version"`
	Name          string       `json:"name"`
	Description   string       `json:"description,omitempty"`
	Trigger       *Trigger     `json:"trigger,omitempty"` // required for kind=automation, absent for kind=tool
	Inputs        []InputParam `json:"inputs,omitempty"`  // kind=tool: typed parameters the agent fills
	Steps         []Step       `json:"steps"`
	Limits        *Limits      `json:"limits,omitempty"`
}

// Trigger says when an automation runs.
type Trigger struct {
	Type     string      `json:"type"`
	Cron     string      `json:"cron,omitempty"`     // cron: 5 fields
	Timezone string      `json:"timezone,omitempty"` // cron/once: IANA name; default Policy.DefaultTimezone
	Every    string      `json:"every,omitempty"`    // interval: Go duration ("6h")
	At       string      `json:"at,omitempty"`       // once: RFC 3339 or "2006-01-02T15:04" in Timezone
	Event    string      `json:"event,omitempty"`    // event: name or "prefix.*"
	Where    []Condition `json:"where,omitempty"`    // event: conditions on the payload
	Cooldown string      `json:"cooldown,omitempty"` // event: minimum time between runs
}

// InputParam is a typed parameter of a dynamic tool.
type InputParam struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"` // string | integer | number | boolean
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
}

// Condition compares a context value with a literal.
type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"` // eq ne contains not_contains gt gte lt lte in is_empty is_not_empty
	Value any    `json:"value,omitempty"`
}

// Step is one node of the (linear) automation.
type Step struct {
	ID   string `json:"id"`
	Type string `json:"type"`

	// tool
	Tool string         `json:"tool,omitempty"`
	Args map[string]any `json:"args,omitempty"`

	// filter / summarize / deliver: list source ("buscar.items")
	From string `json:"from,omitempty"`

	// filter
	Where  []Condition `json:"where,omitempty"`
	Unseen bool        `json:"unseen,omitempty"` // drop items already delivered by this automation
	Limit  int         `json:"limit,omitempty"`

	// condition
	If *Condition `json:"if,omitempty"`

	// template / summarize
	Template     string `json:"template,omitempty"`
	Mode         string `json:"mode,omitempty"` // summarize: template (default) | llm
	Instructions string `json:"instructions,omitempty"`
	MaxTokens    int    `json:"max_tokens,omitempty"`

	// deliver
	Channels  []string `json:"channels,omitempty"`
	Title     string   `json:"title,omitempty"`
	Body      string   `json:"body,omitempty"`
	Link      string   `json:"link,omitempty"`
	ItemsFrom string   `json:"items_from,omitempty"`
}

// Limits are per-automation caps; the effective value is the smaller of the
// spec and the Policy.
type Limits struct {
	MaxTokensPerRun  int     `json:"max_tokens_per_run,omitempty"`
	MaxCostUSDPerRun float64 `json:"max_cost_usd_per_run,omitempty"`
	MaxRunsPerDay    int     `json:"max_runs_per_day,omitempty"`
}

// ParseSpec decodes raw JSON into a Spec after validating it against the
// JSON Schema. Semantic checks (catalog, references, frequency) live in
// Validator.Validate.
func ParseSpec(raw []byte) (*Spec, error) {
	if err := validateSchema(raw); err != nil {
		return nil, err
	}
	var s Spec
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, invalid("spec: %v", err)
	}
	return &s, nil
}

// JSON returns the canonical JSON of the spec.
func (s *Spec) JSON() []byte {
	b, _ := json.Marshal(s)
	return b
}

// UsesLLM reports whether any step asks for a language model.
func (s *Spec) UsesLLM() bool {
	for _, st := range s.Steps {
		if st.Type == StepSummarize && st.Mode == "llm" {
			return true
		}
	}
	return false
}

// IsPersonalReminder reports whether the spec is a trivial personal reminder:
// a one-shot trigger whose only step delivers a fixed message to its own
// creator (no tools, no model). Policy.AutoApprovePersonalReminders lets such
// a spec skip the proposal step when the person asked for it themselves.
func (s *Spec) IsPersonalReminder() bool {
	if s.Trigger == nil || s.Trigger.Type != TriggerOnce || len(s.Steps) != 1 {
		return false
	}
	st := s.Steps[0]
	if st.Type != StepDeliver || st.ItemsFrom != "" {
		return false
	}
	for _, ch := range st.Channels {
		if ch != ChannelInApp && ch != ChannelPush {
			return false
		}
	}
	return true
}
