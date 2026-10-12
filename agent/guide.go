package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/asteby/metacore-kernel/agent/contextmgr"
)

// GuideDescriptor is the safe, compact catalog published by the UI bundles
// loaded in the user's browser (host built-ins, addon manifests and the
// synthetic CRUD tours derived from the nav + model metadata). It
// intentionally excludes routes/selectors.
type GuideDescriptor struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Intents     []string `json:"intents"`
}

type SuggestedGuide struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Confidence float64 `json:"confidence"`
}

const (
	maxGuideCatalog   = 100
	maxGuideTextRunes = 160
)

// GuideTurn carries one chat turn's guide catalog into the agent loop and the
// guide the model decided to start (if any) back out to the handler.
//
// Teach-vs-do is a language decision, so the model makes it — not a verb list.
// The catalog is whatever the browser advertises, so new models, nav entries
// and addon guides show up here with no backend change.
type GuideTurn struct {
	catalog []GuideDescriptor
	mu      sync.Mutex
	chosen  *SuggestedGuide
}

// NewGuideTurn sanitizes the client catalog (it is untrusted input that ends
// up in a tool description): drops incomplete rows, dedupes, caps size/length.
func NewGuideTurn(catalog []GuideDescriptor) *GuideTurn {
	seen := map[string]bool{}
	clean := make([]GuideDescriptor, 0, len(catalog))
	for _, g := range catalog {
		id := strings.TrimSpace(g.ID)
		title := clipGuideText(g.Title)
		if id == "" || title == "" || seen[id] || len(id) > 128 {
			continue
		}
		seen[id] = true
		clean = append(clean, GuideDescriptor{ID: id, Title: title, Description: clipGuideText(g.Description)})
		if len(clean) == maxGuideCatalog {
			break
		}
	}
	return &GuideTurn{catalog: clean}
}

func clipGuideText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxGuideTextRunes {
		s = string(r[:maxGuideTextRunes]) + "…"
	}
	return s
}

// Chosen is the guide start_guide selected during the turn, or nil.
func (t *GuideTurn) Chosen() *SuggestedGuide {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.chosen
}

func (t *GuideTurn) find(id string) *GuideDescriptor {
	for i := range t.catalog {
		if t.catalog[i].ID == id {
			return &t.catalog[i]
		}
	}
	return nil
}

type guideTurnKey struct{}

// WithGuideTurn attaches the turn's guide catalog to ctx.
func WithGuideTurn(ctx context.Context, turn *GuideTurn) context.Context {
	return context.WithValue(ctx, guideTurnKey{}, turn)
}

// GuideTurnFrom returns the turn attached by WithGuideTurn, or nil.
func GuideTurnFrom(ctx context.Context) *GuideTurn {
	turn, _ := ctx.Value(guideTurnKey{}).(*GuideTurn)
	return turn
}

// StartGuideTool lets the model start a visual walkthrough. It is registered
// per turn only when the browser advertised guides, and its guide_id is an
// enum of exactly those ids, so the model can never invent a capability.
type StartGuideTool struct{ turn *GuideTurn }

func NewStartGuideTool(turn *GuideTurn) *StartGuideTool { return &StartGuideTool{turn: turn} }

func (t *StartGuideTool) Name() string     { return "start_guide" }
func (t *StartGuideTool) Category() string { return "workflow" }

func (t *StartGuideTool) Description() string {
	var b strings.Builder
	b.WriteString("Inicia una guía visual paso a paso en la pantalla del usuario (ilumina el menú, los botones y los campos). ")
	b.WriteString("Úsala SOLO cuando el usuario quiere aprender o que le muestres cómo se hace algo en la interfaz. ")
	b.WriteString("Si pide que TÚ hagas la acción (crear, registrar, modificar… aunque todavía no dé los datos), NO la uses: usa las tools de datos. ")
	b.WriteString("Guías disponibles en esta sesión:")
	for _, g := range t.turn.catalog {
		b.WriteString("\n- ")
		b.WriteString(g.ID)
		b.WriteString(": ")
		b.WriteString(g.Title)
		if g.Description != "" {
			b.WriteString(" — ")
			b.WriteString(g.Description)
		}
	}
	return b.String()
}

func (t *StartGuideTool) Parameters() map[string]interface{} {
	ids := make([]string, 0, len(t.turn.catalog))
	for _, g := range t.turn.catalog {
		ids = append(ids, g.ID)
	}
	return map[string]interface{}{
		"guide_id": map[string]interface{}{
			"type":        "string",
			"enum":        ids,
			"description": "Id de la guía a iniciar (de la lista de guías disponibles).",
			"required":    true,
		},
	}
}

func (t *StartGuideTool) Execute(_ context.Context, params map[string]interface{}, _ *contextmgr.ConversationContext) (interface{}, error) {
	id, _ := params["guide_id"].(string)
	guide := t.turn.find(strings.TrimSpace(id))
	if guide == nil {
		return nil, fmt.Errorf("guía %q no disponible en esta sesión", id)
	}
	t.turn.mu.Lock()
	t.turn.chosen = &SuggestedGuide{ID: guide.ID, Title: guide.Title, Confidence: 1}
	t.turn.mu.Unlock()
	return map[string]interface{}{
		"started": true,
		"title":   guide.Title,
		"note":    "La guía ya se muestra en pantalla. Responde en una sola frase corta; no repitas los pasos.",
	}, nil
}
