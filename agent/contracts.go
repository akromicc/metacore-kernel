package agent

import "github.com/google/uuid"

// PermissionChecker decides whether a user may run an action on a model. It is
// the only authorization seam of the runtime: tools call it before touching
// data on behalf of the human the copilot acts for.
type PermissionChecker interface {
	// Can reports whether userID, within orgID, holds a permission that covers
	// action ("view", "create", "update", "delete", ...) on model.
	Can(orgID, userID uuid.UUID, model, action string) bool
}

// ModuleInfo describes one module (addon) enabled for an organization.
type ModuleInfo struct {
	Key         string
	Name        string
	Description string
	// Capabilities are the higher-level, human-readable abilities the module
	// contributes (used to tell the model what the org can actually do).
	Capabilities []string
}

// ModuleGate is the single point where the runtime learns which optional
// modules exist for an organization, so it never offers or touches data that
// belongs to a module the org has not enabled.
type ModuleGate interface {
	// IsEnabled reports whether moduleKey is enabled for orgID. Implementations
	// decide their failure policy (ops fails open on lookup errors).
	IsEnabled(orgID uuid.UUID, moduleKey string) bool
	// OwnerOf returns the module key that owns model, or "" for core models.
	OwnerOf(model string) string
	// Enabled lists the modules enabled for orgID, for prompts and catalogs.
	Enabled(orgID uuid.UUID) ([]ModuleInfo, error)
}

// Personality carries the user-facing identity of the assistant.
type Personality struct {
	Name        string
	Description string
	// SystemPrompt is the host-authored persona preamble; the runtime appends
	// its own rules after it.
	SystemPrompt string
}

// BrandProvider resolves the assistant persona (name, mascot voice, extra
// instructions) for an organization, so white-label hosts can rebrand the
// copilot without forking the runtime.
type BrandProvider interface {
	PersonalityFor(orgID uuid.UUID) Personality
}
