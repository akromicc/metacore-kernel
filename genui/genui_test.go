package genui

import (
	"strings"
	"testing"
)

func TestParseValidSpec(t *testing.T) {
	raw := `{"version":"genui/v1","title":"Resumen","blocks":[
	  {"type":"text","text":"Hola","tone":"muted"},
	  {"type":"kpi","items":[{"label":"Hallazgos","value":"12","trend":"up"}]},
	  {"type":"card","title":"Noticias","subtitle":"Cada día","badges":[{"label":"Propuesta","tone":"warning"}],"fields":[{"label":"Cuándo","value":"07:00"}],"link":"/automatizaciones"},
	  {"type":"table","columns":[{"key":"n","label":"Nombre"}],"rows":[{"n":"a"},{"n":2}]},
	  {"type":"links","items":[{"title":"Nota","url":"https://ejemplo.mx/a","source":"Diario","date":"2026-10-12"}]},
	  {"type":"timeline","items":[{"title":"Corrida","time":"07:00","status":"completed","tone":"success"}]},
	  {"type":"chart","kind":"bar","x":"dia","series":[{"key":"n","label":"Hallazgos"}],"rows":[{"dia":"lun","n":3}],"summary":"3 hallazgos el lunes"},
	  {"type":"confirm","title":"¿Activar?","inputs":[{"name":"hora","label":"Hora","kind":"time","value":"07:00"},{"name":"canal","label":"Canal","kind":"select","options":[{"value":"push","label":"Push"}]}],
	   "actions":[{"id":"automations.confirm","label":"Confirmar","variant":"primary","params":{"id":"x"}}]}
	]}`
	if _, err := Parse([]byte(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestParseRejectsUnsafe(t *testing.T) {
	bad := map[string]string{
		"unknown block":    `{"version":"genui/v1","blocks":[{"type":"html","text":"<script>"}]}`,
		"unknown field":    `{"version":"genui/v1","blocks":[{"type":"text","text":"x","onclick":"alert(1)"}]}`,
		"js link":          `{"version":"genui/v1","blocks":[{"type":"links","items":[{"title":"x","url":"javascript:alert(1)"}]}]}`,
		"proto-rel link":   `{"version":"genui/v1","blocks":[{"type":"card","title":"x","link":"//evil.com"}]}`,
		"data link":        `{"version":"genui/v1","blocks":[{"type":"timeline","items":[{"title":"x","url":"data:text/html,hi"}]}]}`,
		"bad action id":    `{"version":"genui/v1","blocks":[{"type":"confirm","title":"x","actions":[{"id":"https://evil","label":"x"}]}]}`,
		"object param":     `{"version":"genui/v1","blocks":[{"type":"confirm","title":"x","actions":[{"id":"a.b","label":"x","params":{"p":{"q":1}}}]}]}`,
		"foreign field":    `{"version":"genui/v1","blocks":[{"type":"text","text":"x","actions":[{"id":"a.b","label":"x"}]}]}`,
		"nested cell":      `{"version":"genui/v1","blocks":[{"type":"table","columns":[{"key":"a","label":"A"}],"rows":[{"a":[1,2]}]}]}`,
		"chart no summary": `{"version":"genui/v1","blocks":[{"type":"chart","kind":"bar","x":"d","series":[{"key":"n","label":"N"}],"rows":[]}]}`,
		"wrong version":    `{"version":"genui/v2","blocks":[{"type":"text","text":"x"}]}`,
		"empty":            `{"version":"genui/v1","blocks":[]}`,
	}
	for name, raw := range bad {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	big := `{"version":"genui/v1","blocks":[{"type":"text","text":"` + strings.Repeat("a", MaxText+1) + `"}]}`
	if _, err := Parse([]byte(big)); err == nil {
		t.Error("oversized text accepted")
	}
}

func TestSafeLink(t *testing.T) {
	for l, want := range map[string]bool{"": true, "/m/findings/1": true, "https://a.mx": true, "http://a.mx/x": true,
		"//a.mx": false, "javascript:x": false, "ftp://a": false, "/a\nb": false, "mailto:a@b": false} {
		if SafeLink(l) != want {
			t.Errorf("%q: want %v", l, want)
		}
	}
}
