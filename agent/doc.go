// Package agent is the reusable runtime for conversational copilots built on
// the metacore kernel: tool registry, conversation context, chat embeds
// (record/count/chart/image cards), provider-error classification, outbound
// message queue, guided-tour suggestions and the egress guard for tool SQL.
//
// The package follows the kernel's dependency-inversion rule: it never imports
// a host's models or services. Everything host-specific is an interface in
// contracts.go (PermissionChecker, ModuleGate, ModelRegistry, BrandProvider)
// or a small injected port (ImageGenerator, vector.Store, vector.Embedder).
//
// What deliberately stays in the host: ERP prompts and aliases, tools that
// know the host's schema (query_erp, product_search), the orchestrator loop
// that glues them together, and the AI provider/org configuration services.
//
// Sub-packages:
//
//	agent/contextmgr  per-conversation state (in-memory cache + gorm history)
//	agent/tools       ToolRegistry, BaseTool and the calculator tool
package agent
