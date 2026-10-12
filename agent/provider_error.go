package agent

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ToolTurnError is a provider failure that happened right after a tool
// failed in the same turn. The tool is named so the user sees what broke.
type ToolTurnError struct {
	Tool    string
	ToolErr string
	Err     error
}

func (e *ToolTurnError) Error() string {
	return fmt.Sprintf("tool %s failed (%s); then: %v", e.Tool, e.ToolErr, e.Err)
}

func (e *ToolTurnError) Unwrap() error { return e.Err }

// ProviderErrorKind is what the chat UI keys its message and buttons on.
type ProviderErrorKind string

const (
	ProviderErrAuth         ProviderErrorKind = "auth"          // session expired / not connected / bad key
	ProviderErrRateLimit    ProviderErrorKind = "rate_limit"    // usage limit, 429
	ProviderErrUnavailable  ProviderErrorKind = "unavailable"   // proxy down, 5xx, overloaded
	ProviderErrTimeout      ProviderErrorKind = "timeout"       // no answer in time
	ProviderErrTool         ProviderErrorKind = "tool"          // a named tool failed
	ProviderErrBadInput     ProviderErrorKind = "invalid_input" // image / payload rejected
	ProviderErrThread       ProviderErrorKind = "thread"        // tool_use / tool_result history broken
	ProviderErrUnknown      ProviderErrorKind = "unknown"
	ProviderActionRetry                       = "retry"
	ProviderActionReconnect                   = "reconnect"
	ProviderActionSwitch                      = "switch_provider"
	ProviderActionNewThread                   = "new_thread"
)

// ProviderError is the classified, user-facing form of a failed Aby turn.
type ProviderError struct {
	Kind          ProviderErrorKind `json:"code"`
	Message       string            `json:"message"`
	Provider      string            `json:"provider,omitempty"`
	ProviderLabel string            `json:"provider_label,omitempty"`
	Tool          string            `json:"tool,omitempty"`
	ResetAt       *time.Time        `json:"reset_at,omitempty"`
	ResetHint     string            `json:"reset_hint,omitempty"`
	Detail        string            `json:"detail,omitempty"`
	Actions       []string          `json:"actions"`
}

var (
	// Claude CLI: "Claude AI usage limit reached|1759201200".
	resetEpochRe = regexp.MustCompile(`(?i)limit reached\|(\d{10})`)
	// Claude CLI: "5-hour limit reached ∙ resets 3am (<IANA zone>)".
	resetsRe = regexp.MustCompile(`(?i)resets?\s+(?:at\s+)?([0-9]{1,2}(?::[0-9]{2})?\s*(?:am|pm)?(?:\s*\([^)]+\))?)`)
	// "try again in 20s" / "please try again after 3:00 PM".
	tryAgainRe = regexp.MustCompile(`(?i)try again (?:in|after)\s+([^.;,\n]+)`)
	// Wrapping our own layers adds before the provider's text.
	wrapPrefixRe = regexp.MustCompile(`(?i)^(?:copilot processing failed|agent loop failed|ai service failed|agent processing failed|smart orchestrator failed):\s*`)
)

// ClassifyProviderError maps a failed turn to a specific cause with the
// actions that recover from it. mode is the org AI mode (claude_code, codex,
// hub…); it only names the provider and decides whether "reconnect" applies.
func ClassifyProviderError(err error, mode string) *ProviderError {
	pe := &ProviderError{
		Kind:          ProviderErrUnknown,
		Provider:      mode,
		ProviderLabel: ProviderLabel(mode),
	}
	if err == nil {
		pe.Message = "No se pudo procesar el mensaje. Tu mensaje quedó guardado; reintenta."
		pe.Actions = []string{ProviderActionRetry}
		return pe
	}
	raw := err.Error()
	low := strings.ToLower(raw)
	pe.Detail = providerDetail(raw)
	who := pe.ProviderLabel

	var tte *ToolTurnError
	hasTool := errors.As(err, &tte)
	if hasTool {
		pe.Tool = tte.Tool
		// The tool is context; the provider's own cause still decides the kind.
		low = strings.ToLower(tte.Err.Error())
		pe.Detail = providerDetail(tte.Err.Error())
	}

	switch {
	case containsAny(low, "usage limit", "limit reached", "hit your limit", "rate limit", "rate_limit",
		"quota", "too many requests", "ocupado") || hasStatus(low, 429):
		pe.Kind = ProviderErrRateLimit
		pe.ResetAt, pe.ResetHint = parseReset(raw)
		msg := fmt.Sprintf("Llegaste al límite de uso de %s.", who)
		switch {
		case pe.ResetAt != nil:
			// reset_at travels as an instant; the chat renders it in the
			// viewer's zone instead of the server guessing one.
		case pe.ResetHint != "":
			msg += " Se restablece: " + pe.ResetHint + "."
		default:
			msg += " Espera unos minutos o cambia de proveedor."
		}
		pe.Message = msg
		pe.Actions = []string{ProviderActionRetry, ProviderActionSwitch}
	case containsAny(low, "unauthorized", "not logged in", "/login", "invalid api key",
		"invalid x-api-key", "authentication_error", "authentication failed", "oauth token",
		"token has expired", "token expired", "session expired", "failed to authenticate", "credential", "forbidden", "api key required") ||
		hasStatus(low, 401, 403):
		pe.Kind = ProviderErrAuth
		if isLocalSeatMode(mode) {
			pe.Message = fmt.Sprintf("La sesión de %s venció o no está conectada. Reconéctala y reintenta: tu mensaje quedó guardado.", who)
		} else {
			pe.Message = fmt.Sprintf("%s rechazó las credenciales (clave inválida o vencida). Revisa la conexión y reintenta: tu mensaje quedó guardado.", who)
		}
		pe.Actions = []string{ProviderActionReconnect, ProviderActionSwitch, ProviderActionRetry}
		if mode == "" || mode == "hub" {
			// Hub credentials belong to the instance, not to something the org reconnects.
			pe.Actions = []string{ProviderActionSwitch, ProviderActionRetry}
		}
	case containsAny(low, "timeout", "timed out", "deadline exceeded", "sin salida durante"):
		pe.Kind = ProviderErrTimeout
		pe.Message = fmt.Sprintf("%s tardó demasiado en responder. Reintenta: tu mensaje quedó guardado.", who)
		pe.Actions = []string{ProviderActionRetry, ProviderActionSwitch}
	case containsAny(low, "connection refused", "no such host", "dial tcp", "connection reset",
		"broken pipe", "bad gateway", "service unavailable", "overloaded",
		"all backends failed", "no está disponible", "unexpected eof") || hasStatus(low, 500, 502, 503, 504, 529):
		pe.Kind = ProviderErrUnavailable
		pe.Message = fmt.Sprintf("No pude comunicarme con %s: no está respondiendo. Reintenta en un momento o cambia de proveedor.", who)
		pe.Actions = []string{ProviderActionRetry, ProviderActionSwitch}
	case hasStatus(low, 422) && containsAny(low, "image", "vision", "binary"):
		pe.Kind = ProviderErrBadInput
		pe.Message = "No se pudo leer la imagen. Prueba con otra foto (JPG/PNG) o describe el contenido en texto."
		pe.Actions = []string{ProviderActionRetry}
	case hasStatus(low, 422):
		pe.Kind = ProviderErrBadInput
		pe.Message = fmt.Sprintf("%s no aceptó el mensaje o el adjunto. Reintenta o abre una conversación nueva.", who)
		pe.Actions = []string{ProviderActionRetry, ProviderActionNewThread}
	case containsAny(low, "tool_use_id", "tool_result"):
		pe.Kind = ProviderErrThread
		pe.Message = "El historial de esta conversación quedó inconsistente. Abre una conversación nueva para seguir."
		pe.Actions = []string{ProviderActionNewThread, ProviderActionRetry}
	case hasTool:
		pe.Kind = ProviderErrTool
		pe.Message = fmt.Sprintf("Falló la herramienta «%s»", tte.Tool)
		if d := strings.TrimSpace(tte.ToolErr); d != "" {
			pe.Message += ": " + TruncateRunes(d, 160)
		}
		pe.Message += ". Reintenta; tu mensaje quedó guardado."
		pe.Actions = []string{ProviderActionRetry}
	default:
		pe.Message = fmt.Sprintf("%s devolvió un error", who)
		if pe.Detail != "" {
			pe.Message += ": " + TruncateRunes(pe.Detail, 180)
		}
		pe.Message += ". Reintenta: tu mensaje quedó guardado."
		pe.Actions = []string{ProviderActionRetry, ProviderActionSwitch}
	}
	// A tool that failed before the provider gave out is still worth naming.
	if hasTool && pe.Kind != ProviderErrTool {
		pe.Message += fmt.Sprintf(" (Antes falló la herramienta «%s».)", tte.Tool)
	}
	return pe
}

// ProviderLabel is the name the user picked in /organization/ai.
func ProviderLabel(mode string) string {
	switch mode {
	case "claude_code", "claude_code_proxy":
		return "Claude"
	case "codex":
		return "Codex"
	case "cursor":
		return "Cursor"
	case "anthropic":
		return "Anthropic"
	case "openai":
		return "OpenAI"
	case "deepseek":
		return "DeepSeek"
	case "openai_api":
		return "el proveedor configurado"
	case "local":
		return "el modelo local"
	default:
		return "el servicio de IA"
	}
}

func isLocalSeatMode(mode string) bool {
	switch mode {
	case "claude_code", "claude_code_proxy", "codex", "cursor":
		return true
	}
	return false
}

// parseReset pulls the reset moment out of a usage-limit message: an exact
// time when the provider sends an epoch, otherwise its own wording.
func parseReset(raw string) (*time.Time, string) {
	if m := resetEpochRe.FindStringSubmatch(raw); m != nil {
		if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			t := time.Unix(sec, 0).UTC()
			return &t, ""
		}
	}
	if m := resetsRe.FindStringSubmatch(raw); m != nil {
		return nil, strings.TrimSpace(m[1])
	}
	if m := tryAgainRe.FindStringSubmatch(raw); m != nil {
		return nil, "en " + strings.TrimSpace(m[1])
	}
	return nil, ""
}

// providerDetail strips our own wrapping so the provider's words remain.
func providerDetail(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.LastIndex(s, "Last error: "); i >= 0 {
		s = s[i+len("Last error: "):]
	}
	for {
		next := wrapPrefixRe.ReplaceAllString(s, "")
		if next == s {
			break
		}
		s = next
	}
	return strings.TrimSpace(s)
}

// hasStatus matches an HTTP status as a whole number, so "429" inside an
// epoch or a port never reads as a rate limit.
func hasStatus(s string, codes ...int) bool {
	for _, c := range codes {
		if regexp.MustCompile(`(^|[^0-9])` + strconv.Itoa(c) + `([^0-9]|$)`).MatchString(s) {
			return true
		}
	}
	return false
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
