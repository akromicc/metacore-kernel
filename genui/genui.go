// Package genui is the declarative, metadata-only "generative UI" contract the
// agent uses to show structured answers (cards, KPIs, tables, links, timelines,
// charts and confirmation forms) instead of free text.
//
// The agent never sends HTML or JavaScript: a tool returns a Spec (JSON) whose
// blocks come from a closed whitelist; Parse validates it strictly (unknown
// block types, unknown fields, oversized payloads, non-http(s) links and
// malformed action ids are rejected) and the SDK renderer
// (@asteby/metacore-genui) validates it again before drawing it with its own
// components. Interactive elements only carry an action id plus scalar params;
// the host maps each id to a concrete call (for example
// "automations.confirm" → POST /api/automations/:id/confirm), so a spec can
// never reach an arbitrary endpoint.
package genui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Version is the only spec version this package accepts.
const Version = "genui/v1"

// Limits keep a spec small enough for a chat bubble and cheap to store.
const (
	MaxBlocks      = 12
	MaxItems       = 50
	MaxColumns     = 8
	MaxFields      = 12
	MaxActions     = 4
	MaxText        = 2000
	MaxLabel       = 160
	MaxSpecBytes   = 64 << 10
	maxParamKeys   = 8
	maxOptionCount = 30
)

// Block types (the whitelist).
const (
	BlockText     = "text"
	BlockKPI      = "kpi"
	BlockCard     = "card"
	BlockTable    = "table"
	BlockLinks    = "links"
	BlockTimeline = "timeline"
	BlockChart    = "chart"
	BlockConfirm  = "confirm"
)

// Spec is a validated generative-UI document.
type Spec struct {
	Version string  `json:"version"`
	Title   string  `json:"title,omitempty"`
	Blocks  []Block `json:"blocks"`
}

// Block is one whitelisted component. Only the fields relevant to Type are
// allowed; Parse rejects the rest.
type Block struct {
	Type string `json:"type"`

	// text / card
	Text     string  `json:"text,omitempty"`
	Tone     string  `json:"tone,omitempty"` // default | muted | info | success | warning | danger
	Title    string  `json:"title,omitempty"`
	Subtitle string  `json:"subtitle,omitempty"`
	Body     string  `json:"body,omitempty"`
	Badges   []Badge `json:"badges,omitempty"`
	Fields   []Field `json:"fields,omitempty"`
	Link     string  `json:"link,omitempty"`

	// kpi / links / timeline
	Items []Item `json:"items,omitempty"`

	// table / chart
	Columns []Column         `json:"columns,omitempty"`
	Rows    []map[string]any `json:"rows,omitempty"`
	Caption string           `json:"caption,omitempty"`
	Kind    string           `json:"kind,omitempty"` // chart: bar | line
	X       string           `json:"x,omitempty"`
	Series  []Column         `json:"series,omitempty"`
	Summary string           `json:"summary,omitempty"` // chart: accessible text summary (required)

	// confirm
	Description string   `json:"description,omitempty"`
	Inputs      []Input  `json:"inputs,omitempty"`
	Actions     []Action `json:"actions,omitempty"`
}

// Badge is a short status label.
type Badge struct {
	Label string `json:"label"`
	Tone  string `json:"tone,omitempty"`
}

// Field is a read-only label/value pair.
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Item is an entry of a kpi, links or timeline block.
type Item struct {
	Label       string `json:"label,omitempty"`
	Value       string `json:"value,omitempty"`
	Hint        string `json:"hint,omitempty"`
	Trend       string `json:"trend,omitempty"` // up | down | flat
	Title       string `json:"title,omitempty"`
	URL         string `json:"url,omitempty"`
	Source      string `json:"source,omitempty"`
	Date        string `json:"date,omitempty"`
	Time        string `json:"time,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
	Tone        string `json:"tone,omitempty"`
}

// Column describes a table column or a chart series.
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// Input is an editable field of a confirm block.
type Input struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"` // text | textarea | number | switch | select | time | date
	Value    any      `json:"value,omitempty"`
	Required bool     `json:"required,omitempty"`
	Options  []Option `json:"options,omitempty"`
	Help     string   `json:"help,omitempty"`
}

// Option is a select choice.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Action is a declared interaction: the host maps ID to a concrete call.
type Action struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Variant string         `json:"variant,omitempty"` // primary | secondary | ghost | danger
	Params  map[string]any `json:"params,omitempty"`
}

var (
	actionIDRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*){1,3}$`)
	keyRe      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]{0,63}$`)
	tones      = set("", "default", "muted", "info", "success", "warning", "danger")
	trends     = set("", "up", "down", "flat")
	variants   = set("", "primary", "secondary", "ghost", "danger")
	inputKinds = set("text", "textarea", "number", "switch", "select", "time", "date")
	chartKinds = set("bar", "line")
)

func set(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

// Parse decodes and validates raw JSON. Unknown fields are rejected.
func Parse(raw []byte) (*Spec, error) {
	if len(raw) > MaxSpecBytes {
		return nil, fmt.Errorf("genui: spec exceeds %d bytes", MaxSpecBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Spec
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("genui: %w", err)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// FromAny validates a value that is already decoded (a *Spec, Spec or a
// generic map from a tool result).
func FromAny(v any) (*Spec, error) {
	switch s := v.(type) {
	case nil:
		return nil, errors.New("genui: empty spec")
	case *Spec:
		if err := s.Validate(); err != nil {
			return nil, err
		}
		return s, nil
	case Spec:
		if err := s.Validate(); err != nil {
			return nil, err
		}
		return &s, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("genui: %w", err)
	}
	return Parse(raw)
}

// Validate checks the whole spec against the whitelist and limits.
func (s *Spec) Validate() error {
	if s.Version != Version {
		return fmt.Errorf("genui: version must be %q", Version)
	}
	if err := text("title", s.Title, MaxLabel); err != nil {
		return err
	}
	if len(s.Blocks) == 0 || len(s.Blocks) > MaxBlocks {
		return fmt.Errorf("genui: between 1 and %d blocks required", MaxBlocks)
	}
	for i := range s.Blocks {
		if err := s.Blocks[i].validate(); err != nil {
			return fmt.Errorf("genui: block %d (%s): %w", i, s.Blocks[i].Type, err)
		}
	}
	return nil
}

func (b *Block) validate() error {
	if !tones[b.Tone] {
		return fmt.Errorf("unknown tone %q", b.Tone)
	}
	for _, f := range []struct {
		name, v string
		max     int
	}{{"text", b.Text, MaxText}, {"title", b.Title, MaxLabel}, {"subtitle", b.Subtitle, MaxLabel},
		{"body", b.Body, MaxText}, {"caption", b.Caption, MaxLabel}, {"summary", b.Summary, MaxText},
		{"description", b.Description, MaxText}} {
		if err := text(f.name, f.v, f.max); err != nil {
			return err
		}
	}
	if err := safeLink(b.Link); err != nil {
		return err
	}
	allowed := map[string]bool{}
	switch b.Type {
	case BlockText:
		if strings.TrimSpace(b.Text) == "" {
			return errors.New("text required")
		}
		allowed = set("text", "tone")
	case BlockKPI:
		if len(b.Items) == 0 || len(b.Items) > MaxFields {
			return fmt.Errorf("1..%d items required", MaxFields)
		}
		for _, it := range b.Items {
			if it.Label == "" || it.Value == "" {
				return errors.New("kpi items need label and value")
			}
			if !trends[it.Trend] || !tones[it.Tone] {
				return errors.New("unknown trend or tone")
			}
		}
		allowed = set("items", "title")
	case BlockCard:
		if b.Title == "" {
			return errors.New("title required")
		}
		if len(b.Badges) > 6 || len(b.Fields) > MaxFields {
			return errors.New("too many badges or fields")
		}
		for _, bd := range b.Badges {
			if bd.Label == "" || len(bd.Label) > 60 || !tones[bd.Tone] {
				return errors.New("invalid badge")
			}
		}
		for _, f := range b.Fields {
			if f.Label == "" || len(f.Label) > MaxLabel || len(f.Value) > MaxText {
				return errors.New("invalid field")
			}
		}
		allowed = set("title", "subtitle", "body", "badges", "fields", "link", "tone")
	case BlockTable:
		if err := columns(b.Columns, MaxColumns); err != nil {
			return err
		}
		if err := rows(b.Rows); err != nil {
			return err
		}
		allowed = set("title", "columns", "rows", "caption")
	case BlockLinks:
		if len(b.Items) == 0 || len(b.Items) > MaxItems {
			return fmt.Errorf("1..%d items required", MaxItems)
		}
		for _, it := range b.Items {
			if it.Title == "" {
				return errors.New("link title required")
			}
			if err := safeLink(it.URL); err != nil {
				return err
			}
		}
		allowed = set("title", "items")
	case BlockTimeline:
		if len(b.Items) == 0 || len(b.Items) > MaxItems {
			return fmt.Errorf("1..%d items required", MaxItems)
		}
		for _, it := range b.Items {
			if it.Title == "" || !tones[it.Tone] {
				return errors.New("timeline item needs a title")
			}
			if err := safeLink(it.URL); err != nil {
				return err
			}
		}
		allowed = set("title", "items")
	case BlockChart:
		if !chartKinds[b.Kind] {
			return fmt.Errorf("chart kind must be bar or line")
		}
		if !keyRe.MatchString(b.X) {
			return errors.New("chart x key invalid")
		}
		if err := columns(b.Series, 4); err != nil {
			return err
		}
		if err := rows(b.Rows); err != nil {
			return err
		}
		if strings.TrimSpace(b.Summary) == "" {
			return errors.New("chart summary (accessible text) required")
		}
		allowed = set("title", "kind", "x", "series", "rows", "summary", "caption")
	case BlockConfirm:
		if b.Title == "" {
			return errors.New("title required")
		}
		if len(b.Inputs) > MaxFields || len(b.Fields) > MaxFields {
			return errors.New("too many inputs")
		}
		for _, in := range b.Inputs {
			if err := in.validate(); err != nil {
				return err
			}
		}
		if len(b.Actions) == 0 || len(b.Actions) > MaxActions {
			return fmt.Errorf("1..%d actions required", MaxActions)
		}
		for _, a := range b.Actions {
			if err := a.validate(); err != nil {
				return err
			}
		}
		allowed = set("title", "description", "fields", "inputs", "actions", "badges", "tone")
	default:
		return fmt.Errorf("unknown block type %q", b.Type)
	}
	return onlyFields(b, allowed)
}

// onlyFields rejects populated fields that do not belong to the block type,
// so a "text" block cannot smuggle actions or rows.
func onlyFields(b *Block, allowed map[string]bool) error {
	present := map[string]bool{
		"text": b.Text != "", "tone": b.Tone != "", "title": b.Title != "", "subtitle": b.Subtitle != "",
		"body": b.Body != "", "badges": len(b.Badges) > 0, "fields": len(b.Fields) > 0, "link": b.Link != "",
		"items": len(b.Items) > 0, "columns": len(b.Columns) > 0, "rows": len(b.Rows) > 0,
		"caption": b.Caption != "", "kind": b.Kind != "", "x": b.X != "", "series": len(b.Series) > 0,
		"summary": b.Summary != "", "description": b.Description != "", "inputs": len(b.Inputs) > 0,
		"actions": len(b.Actions) > 0,
	}
	for k, on := range present {
		if on && !allowed[k] {
			return fmt.Errorf("field %q not allowed", k)
		}
	}
	return nil
}

func (in Input) validate() error {
	if !keyRe.MatchString(in.Name) || in.Label == "" || len(in.Label) > MaxLabel || !inputKinds[in.Kind] {
		return fmt.Errorf("invalid input %q", in.Name)
	}
	if len(in.Help) > MaxLabel {
		return errors.New("input help too long")
	}
	if in.Kind == "select" && (len(in.Options) == 0 || len(in.Options) > maxOptionCount) {
		return fmt.Errorf("select %q needs 1..%d options", in.Name, maxOptionCount)
	}
	if in.Kind != "select" && len(in.Options) > 0 {
		return fmt.Errorf("options only for select (%q)", in.Name)
	}
	if !scalar(in.Value) {
		return fmt.Errorf("input %q value must be scalar", in.Name)
	}
	return nil
}

func (a Action) validate() error {
	if !actionIDRe.MatchString(a.ID) {
		return fmt.Errorf("invalid action id %q", a.ID)
	}
	if a.Label == "" || len(a.Label) > 60 || !variants[a.Variant] {
		return fmt.Errorf("invalid action %q", a.ID)
	}
	if len(a.Params) > maxParamKeys {
		return fmt.Errorf("action %q has too many params", a.ID)
	}
	for k, v := range a.Params {
		if !keyRe.MatchString(k) || !scalar(v) {
			return fmt.Errorf("action %q param %q must be a scalar", a.ID, k)
		}
		if s, ok := v.(string); ok && len(s) > MaxLabel {
			return fmt.Errorf("action %q param %q too long", a.ID, k)
		}
	}
	return nil
}

func columns(cols []Column, max int) error {
	if len(cols) == 0 || len(cols) > max {
		return fmt.Errorf("1..%d columns required", max)
	}
	for _, c := range cols {
		if !keyRe.MatchString(c.Key) || c.Label == "" || len(c.Label) > MaxLabel {
			return fmt.Errorf("invalid column %q", c.Key)
		}
	}
	return nil
}

func rows(rs []map[string]any) error {
	if len(rs) > MaxItems {
		return fmt.Errorf("at most %d rows", MaxItems)
	}
	for _, r := range rs {
		if len(r) > 16 {
			return errors.New("row has too many cells")
		}
		for k, v := range r {
			if !keyRe.MatchString(k) || !scalar(v) {
				return fmt.Errorf("cell %q must be a scalar", k)
			}
			if s, ok := v.(string); ok && len(s) > MaxText {
				return fmt.Errorf("cell %q too long", k)
			}
		}
	}
	return nil
}

func scalar(v any) bool {
	switch v.(type) {
	case nil, string, bool, float64, float32, int, int32, int64, uint, uint32, uint64, json.Number:
		return true
	}
	return false
}

func text(name, v string, max int) error {
	if len(v) > max {
		return fmt.Errorf("%s exceeds %d characters", name, max)
	}
	return nil
}

// SafeLink reports whether link may be rendered: empty, an app-relative path
// ("/m/findings/…", no scheme, no "//") or an absolute http(s) URL.
func SafeLink(link string) bool { return safeLink(link) == nil }

func safeLink(link string) error {
	if link == "" {
		return nil
	}
	if len(link) > 2048 {
		return errors.New("link too long")
	}
	if strings.HasPrefix(link, "/") && !strings.HasPrefix(link, "//") && !strings.ContainsAny(link, "\\\n\r\t") {
		return nil
	}
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("unsafe link %q", link)
	}
	return nil
}
