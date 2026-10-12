package agent

// strParam reads a string tool argument ("" when absent or not a string).
func strParam(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

// TruncateRunes shortens s to at most max runes, ending in "…" when cut.
func TruncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
