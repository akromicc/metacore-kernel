package agent

import (
	"encoding/json"
	"testing"
)

func TestNormalizeChartKind(t *testing.T) {
	cases := map[string]string{
		"torta":      "pie",
		"doughnut":   "donut",
		"bar_h":      "horizontal_bar",
		"timeseries": "line",
		"combo":      "composed",
		"":           "bar",
		"weird":      "bar",
	}
	for in, want := range cases {
		if got := normalizeChartKind(in); got != want {
			t.Fatalf("%q → %q want %q", in, got, want)
		}
	}
}

func TestParseChartSpec_PieForcedToBar(t *testing.T) {
	rows := make([]any, 0, 12)
	for i := 0; i < 12; i++ {
		rows = append(rows, map[string]any{"mes": "M" + string(rune('A'+i)), "total": i + 1})
	}
	spec, err := parseChartSpec(map[string]any{
		"title":  "Ventas",
		"kind":   "pie",
		"x_key":  "mes",
		"series": []any{map[string]any{"key": "total", "label": "Total"}},
		"rows":   rows,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Kind != "bar" {
		t.Fatalf("kind=%s want bar", spec.Kind)
	}
	if len(spec.Rows) != 12 {
		t.Fatalf("rows=%d", len(spec.Rows))
	}
}

func TestParseChartSpec_CoercesStrings(t *testing.T) {
	spec, err := parseChartSpec(map[string]any{
		"title": "Stock",
		"kind":  "horizontal_bar",
		"x_key": "almacen",
		"unit":  "pzas",
		"series": []any{
			map[string]any{"key": "qty", "label": "Cantidad"},
		},
		"rows": []any{
			map[string]any{"almacen": "Norte", "qty": "70"},
			map[string]any{"almacen": "Sur", "qty": json.Number("100")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Rows[0]["qty"].(float64) != 70 {
		t.Fatalf("qty0=%v", spec.Rows[0]["qty"])
	}
	if spec.Rows[1]["qty"].(float64) != 100 {
		t.Fatalf("qty1=%v", spec.Rows[1]["qty"])
	}
}

func TestShowChartTool_Embeds(t *testing.T) {
	tool := NewShowChartTool()
	out, err := tool.Execute(nil, map[string]any{
		"title":  "Pedidos por mes",
		"kind":   "line",
		"x_key":  "mes",
		"series": []any{map[string]any{"key": "n", "label": "Pedidos"}},
		"rows": []any{
			map[string]any{"mes": "Ene", "n": 4},
			map[string]any{"mes": "Feb", "n": 9},
			map[string]any{"mes": "Mar", "n": 6},
		},
		"note": "Feb fue el pico",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	embeds := NewEmbedBuilder(nil).FromToolResult("show_chart", out, nil)
	if len(embeds) != 1 || embeds[0].Type != "chart" || embeds[0].Chart == nil {
		t.Fatalf("%+v", embeds)
	}
	if embeds[0].Chart.Kind != "line" || embeds[0].Chart.Title != "Pedidos por mes" {
		t.Fatalf("spec=%+v", embeds[0].Chart)
	}
	if len(embeds[0].Chart.Rows) != 3 {
		t.Fatalf("rows=%d", len(embeds[0].Chart.Rows))
	}
}

func TestShowChartTool_RejectsEmpty(t *testing.T) {
	_, err := NewShowChartTool().Execute(nil, map[string]any{
		"title": "X", "kind": "bar", "x_key": "a",
		"series": []any{map[string]any{"key": "v"}},
		"rows":   []any{},
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
