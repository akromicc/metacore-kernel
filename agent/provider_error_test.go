package agent

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// wrapLikeProd wraps a provider error the way the Aby turn does before it
// reaches the handler.
func wrapLikeProd(msg string) error {
	e := errors.New(msg)
	e = fmt.Errorf("all GenerateChatWithFunctionCalling strategies failed. Last error: %w", e)
	e = fmt.Errorf("AI service failed: %w", e)
	e = fmt.Errorf("agent loop failed: %w", e)
	return fmt.Errorf("copilot processing failed: %w", e)
}

func TestClassifyProviderError(t *testing.T) {
	cases := []struct {
		name, raw, mode string
		kind            ProviderErrorKind
		wantMsg         []string
		wantAction      string
	}{
		{"claude not logged in", "API returned unexpected status code: 503: claude tool calling failed — claude api error: Invalid API key · Please run /login", "claude_code",
			ProviderErrAuth, []string{"Claude", "Reconéctala"}, ProviderActionReconnect},
		{"oauth expired", "API returned unexpected status code: 503: OAuth token has expired. Please obtain a new token", "claude_code",
			ProviderErrAuth, []string{"venció"}, ProviderActionReconnect},
		{"oauth session expired", "all backends failed: claude exited 1: Failed to authenticate: OAuth session expired and could not be refreshed (503)", "claude_code",
			ProviderErrAuth, []string{"venció"}, ProviderActionReconnect},
		{"usage limit epoch", "API returned unexpected status code: 503: claude api error: Claude AI usage limit reached|1759201200", "claude_code",
			ProviderErrRateLimit, []string{"límite de uso de Claude"}, ProviderActionSwitch},
		{"usage limit wording", "claude api error: 5-hour limit reached ∙ resets 3am (Europe/Madrid)", "claude_code",
			ProviderErrRateLimit, []string{"3am (Europe/Madrid)"}, ProviderActionRetry},
		{"proxy down", `Post "http://127.0.0.1:8765/v1/chat/completions": dial tcp 127.0.0.1:8765: connect: connection refused`, "claude_code",
			ProviderErrUnavailable, []string{"No pude comunicarme con Claude"}, ProviderActionRetry},
		{"timeout", "context deadline exceeded (Client.Timeout exceeded while awaiting headers)", "codex",
			ProviderErrTimeout, []string{"Codex tardó demasiado"}, ProviderActionRetry},
		{"unknown keeps cause", "API returned unexpected status code: 400: model 'claude-9' not found", "claude_code",
			ProviderErrUnknown, []string{"model 'claude-9' not found"}, ProviderActionRetry},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pe := ClassifyProviderError(wrapLikeProd(c.raw), c.mode)
			if pe.Kind != c.kind {
				t.Fatalf("kind=%s want %s (%s)", pe.Kind, c.kind, pe.Message)
			}
			for _, w := range c.wantMsg {
				if !strings.Contains(pe.Message, w) {
					t.Errorf("message %q missing %q", pe.Message, w)
				}
			}
			found := false
			for _, a := range pe.Actions {
				found = found || a == c.wantAction
			}
			if !found {
				t.Errorf("actions %v missing %s", pe.Actions, c.wantAction)
			}
			if strings.Contains(pe.Detail, "copilot processing failed") || strings.Contains(pe.Detail, "Last error") {
				t.Errorf("detail keeps our wrapping: %q", pe.Detail)
			}
		})
	}
}

func TestClassifyProviderError_ResetEpoch(t *testing.T) {
	pe := ClassifyProviderError(wrapLikeProd("Claude AI usage limit reached|1759201200"), "claude_code")
	if pe.ResetAt == nil || pe.ResetAt.Unix() != 1759201200 {
		t.Fatalf("reset_at=%v", pe.ResetAt)
	}
}

func TestClassifyProviderError_StatusNotInsideNumbers(t *testing.T) {
	// "429"/"401" inside an id or port must not decide the kind.
	pe := ClassifyProviderError(errors.New("request req_84291 failed: weird payload 14013"), "claude_code")
	if pe.Kind != ProviderErrUnknown {
		t.Fatalf("kind=%s", pe.Kind)
	}
}

func TestClassifyProviderError_HubHasNoReconnect(t *testing.T) {
	pe := ClassifyProviderError(errors.New("API returned unexpected status code: 401: unauthorized"), "hub")
	for _, a := range pe.Actions {
		if a == ProviderActionReconnect {
			t.Fatalf("hub auth must not offer reconnect: %v", pe.Actions)
		}
	}
}
