package agent

import (
	"strconv"
	"strings"
	"testing"
)

// fakeRegistry stands in for a host model registry: identity keys, the model
// key as label except for "products", and untranslated fields/enums.
type fakeRegistry struct{}

func (fakeRegistry) ResolveModelKey(n string) string { return n }
func (fakeRegistry) ModelLabel(k string) string {
	if k == "products" {
		return "Productos"
	}
	return k
}
func (fakeRegistry) FieldLabel(_, _, raw string) string   { return raw }
func (fakeRegistry) EnumValueLabel(_, _, v string) string { return v }

var testBuilder = NewEmbedBuilder(fakeRegistry{})

func TestBuildRecordEmbeds_Single(t *testing.T) {
	rows := []map[string]any{
		{"id": "abc-123", "name": "Almacén 1", "quantity": 70},
	}
	embeds := testBuilder.buildRecordEmbeds("stocks", rows, nil, 1)
	if len(embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(embeds))
	}
	if embeds[0].Type != "record" || embeds[0].ID != "abc-123" {
		t.Fatalf("unexpected embed: %+v", embeds[0])
	}
	if embeds[0].Title != "Almacén 1" {
		t.Fatalf("title=%q", embeds[0].Title)
	}
}

func TestBuildRecordEmbeds_TwoWithView(t *testing.T) {
	rows := []map[string]any{
		{"id": "1", "warehouse_name": "Almacén 1", "quantity": 70},
		{"id": "2", "warehouse_name": "Almacén 2", "quantity": 100},
	}
	params := map[string]any{
		"filters": map[string]any{"product_id": "prod-1"},
		"search":  "",
	}
	embeds := testBuilder.buildRecordEmbeds("inventory_stocks", rows, params, 2)
	if len(embeds) < 2 {
		t.Fatalf("expected records(+view), got %d: %+v", len(embeds), embeds)
	}
	last := embeds[len(embeds)-1]
	if last.Type != "view" {
		t.Fatalf("last should be view: %+v", last)
	}
	if last.Search == "" || last.FilterSummary == "" {
		t.Fatalf("view should carry search/filter: %+v", last)
	}
}

func TestBuildRecordEmbeds_LargeOnlyView(t *testing.T) {
	rows := make([]map[string]any, 0, 10)
	for i := 0; i < 10; i++ {
		id := strconv.Itoa(i)
		rows = append(rows, map[string]any{"id": id, "number": "SO-" + id, "state": "delivered"})
	}
	params := map[string]any{
		"filters": map[string]any{"state": "delivered"},
	}
	embeds := testBuilder.buildRecordEmbeds("sales_orders", rows, params, 47)
	if len(embeds) != 1 || embeds[0].Type != "view" {
		t.Fatalf("expected single view embed, got %+v", embeds)
	}
	if !strings.Contains(embeds[0].Title, "47") {
		t.Fatalf("title should include count: %q", embeds[0].Title)
	}
	if embeds[0].Search == "" {
		t.Fatal("expected filtered search qs")
	}
}

func TestDedupeEmbeds(t *testing.T) {
	in := []ChatEmbed{
		{Type: "record", Model: "a", ID: "1"},
		{Type: "record", Model: "a", ID: "1"},
		{Type: "view", Model: "a", Search: "?x=1"},
	}
	out := dedupeEmbeds(in)
	if len(out) != 2 {
		t.Fatalf("got %d", len(out))
	}
}

func TestEmbedsFromQueryResult_Pointer(t *testing.T) {
	qr := &QueryResult{
		Model: "products",
		Count: 1,
		Records: []map[string]any{
			{"id": "p1", "name": "Widget"},
		},
	}
	embeds := testBuilder.FromToolResult("query_data", qr, nil)
	if len(embeds) != 1 || embeds[0].Title != "Widget" {
		t.Fatalf("%+v", embeds)
	}
}

func TestEmbedsFromQueryResult_EmptyIsCountCard(t *testing.T) {
	qr := &QueryResult{Model: "products", Count: 0}
	embeds := testBuilder.FromToolResult("query_data", qr, nil)
	if len(embeds) != 1 {
		t.Fatalf("want one count card, got %+v", embeds)
	}
	e := embeds[0]
	if e.Type != "count" || e.Model != "products" || e.Count != 0 || e.ModelTitle == "" || e.ModelTitle == "products" {
		t.Fatalf("count card should carry the localized label: %+v", e)
	}

	m := map[string]any{"model": "products", "count": float64(0), "records": []any{}}
	if got := testBuilder.FromToolResult("query_data", m, nil); len(got) != 1 || got[0].Type != "count" {
		t.Fatalf("map form: %+v", got)
	}
}
