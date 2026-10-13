package automation

import (
	"testing"
	"time"
)

func TestCronNext(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	cases := []struct {
		expr  string
		after time.Time
		loc   *time.Location
		want  time.Time
	}{
		{"0 7 * * *", time.Date(2026, 10, 13, 6, 59, 0, 0, mx), mx, time.Date(2026, 10, 13, 7, 0, 0, 0, mx)},
		{"0 7 * * *", time.Date(2026, 10, 13, 7, 0, 0, 0, mx), mx, time.Date(2026, 10, 14, 7, 0, 0, 0, mx)},
		{"30 8 * * 1-5", time.Date(2026, 10, 16, 9, 0, 0, 0, mx), mx, time.Date(2026, 10, 19, 8, 30, 0, 0, mx)}, // Friday → Monday
		{"*/20 9-10 * * *", time.Date(2026, 10, 13, 10, 41, 0, 0, mx), mx, time.Date(2026, 10, 14, 9, 0, 0, 0, mx)},
		{"0 9 1 * *", time.Date(2026, 10, 13, 0, 0, 0, 0, mx), mx, time.Date(2026, 11, 1, 9, 0, 0, 0, mx)},
		{"0 0 * * 7", time.Date(2026, 10, 13, 0, 0, 0, 0, mx), mx, time.Date(2026, 10, 18, 0, 0, 0, 0, mx)},   // 7 = Sunday
		{"0 9 13 * 5", time.Date(2026, 10, 13, 10, 0, 0, 0, mx), mx, time.Date(2026, 10, 16, 9, 0, 0, 0, mx)}, // dom OR dow
		// DST spring forward in New York (2026-03-08 02:00 → 03:00): 02:30 does not exist that day.
		{"30 2 * * *", time.Date(2026, 3, 8, 0, 0, 0, 0, ny), ny, time.Date(2026, 3, 9, 2, 30, 0, 0, ny)},
		// DST fall back (2026-11-01 01:00 repeats): fires once.
		{"30 1 * * *", time.Date(2026, 11, 1, 1, 31, 0, 0, ny), ny, time.Date(2026, 11, 2, 1, 30, 0, 0, ny)},
		{"@daily", time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC), time.UTC, time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		cr, err := ParseCron(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := cr.Next(c.after, c.loc); !got.Equal(c.want) {
			t.Errorf("%s after %v: got %v want %v", c.expr, c.after, got, c.want)
		}
	}
}

func TestCronInvalidAndGap(t *testing.T) {
	for _, bad := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "0 0 0 * *", "0 0 * 13 *", "*/0 * * * *", "a b c d e", "5-1 * * * *"} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
	c, _ := ParseCron("*/5 * * * *")
	if g := c.MinGap(time.Now(), time.UTC, 50); g != 5*time.Minute {
		t.Fatalf("gap %v", g)
	}
	c, _ = ParseCron("0 7,8 * * *")
	if g := c.MinGap(time.Now(), time.UTC, 300); g != time.Hour {
		t.Fatalf("gap %v", g)
	}
	if c, _ := ParseCron("0 0 30 2 *"); !c.Next(time.Now(), time.UTC).IsZero() {
		t.Fatal("Feb 30 must never fire")
	}
}

func TestDescribeTrigger(t *testing.T) {
	cases := map[string]*Trigger{
		"Cada día a las 07:00 (America/Mexico_City)":           {Type: TriggerCron, Cron: "0 7 * * *", Timezone: "America/Mexico_City"},
		"De lunes a viernes a las 08:30 (America/Mexico_City)": {Type: TriggerCron, Cron: "30 8 * * 1-5"},
		"Cada lunes a las 09:05 (America/Mexico_City)":         {Type: TriggerCron, Cron: "5 9 * * 1"},
		"Cada 6 horas": {Type: TriggerInterval, Every: "6h"},
		"Una vez, el 14/10/2026 a las 09:00 (America/Mexico_City)": {Type: TriggerOnce, At: "2026-10-14T09:00"},
	}
	for want, tr := range cases {
		if got := DescribeTrigger(tr, "America/Mexico_City"); got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}
