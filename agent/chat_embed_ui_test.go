package agent

import (
	"testing"

	"github.com/asteby/metacore-kernel/genui"
)

func TestUIEmbedFromToolResult(t *testing.T) {
	spec := map[string]any{"version": genui.Version, "blocks": []any{map[string]any{"type": "text", "text": "Hola"}}}
	b := NewEmbedBuilder(noModels{})
	got := b.FromToolResult("crear_automatizacion", map[string]any{"ok": true, "ui": spec}, nil)
	if len(got) != 1 || got[0].Type != "ui" || got[0].UI == nil || got[0].UI.Blocks[0].Text != "Hola" {
		t.Fatalf("embeds: %+v", got)
	}
	bad := map[string]any{"version": genui.Version, "blocks": []any{map[string]any{"type": "html", "text": "<script>"}}}
	if got := b.FromToolResult("x", map[string]any{"ui": bad}, nil); len(got) != 0 {
		t.Fatalf("invalid ui rendered: %+v", got)
	}
	two := DedupeEmbeds([]ChatEmbed{
		{Type: "ui", UI: &genui.Spec{Version: genui.Version, Blocks: []genui.Block{{Type: "text", Text: "a"}}}},
		{Type: "ui", UI: &genui.Spec{Version: genui.Version, Blocks: []genui.Block{{Type: "text", Text: "b"}}}},
	})
	if len(two) != 2 {
		t.Fatalf("distinct ui embeds deduped: %d", len(two))
	}
}
