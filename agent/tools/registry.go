package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/asteby/metacore-kernel/agent/contextmgr"
)

// BaseTool interface base para todas las herramientas
type BaseTool interface {
	Name() string
	Description() string
	Category() string
	Parameters() map[string]interface{}
	Execute(ctx context.Context, args map[string]interface{}, agentCtx *contextmgr.ConversationContext) (interface{}, error)
}

// ToolMetadata información adicional opcional
type ToolMetadata interface {
	BaseTool
	Version() string
	Examples() []map[string]interface{}
}

// ToolCategory categorías de herramientas
type ToolCategory string

const (
	CategoryKnowledge     ToolCategory = "knowledge"
	CategoryCommunication ToolCategory = "communication"
	CategoryWorkflow      ToolCategory = "workflow"
	CategoryContext       ToolCategory = "context"
	CategoryData          ToolCategory = "data"
)

// ToolRegistry gestiona todas las herramientas disponibles
type ToolRegistry struct {
	tools      map[string]BaseTool
	categories map[ToolCategory][]string
}

// NewToolRegistry crea un nuevo registro
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools:      make(map[string]BaseTool),
		categories: make(map[ToolCategory][]string),
	}
}

// Register registra una herramienta
func (r *ToolRegistry) Register(tool BaseTool) {
	r.tools[tool.Name()] = tool
	r.categories[ToolCategory(tool.Category())] = append(r.categories[ToolCategory(tool.Category())], tool.Name())
	log.Printf("🔧 Tool registered: %s (category: %s)", tool.Name(), tool.Category())
}

// Get obtiene una herramienta por nombre
func (r *ToolRegistry) Get(name string) (BaseTool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// List retorna todas las herramientas
func (r *ToolRegistry) List() []BaseTool {
	tools := make([]BaseTool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}

// ListByCategory retorna herramientas de una categoría
func (r *ToolRegistry) ListByCategory(category ToolCategory) []BaseTool {
	names := r.categories[category]
	tools := make([]BaseTool, 0, len(names))
	for _, name := range names {
		if tool, ok := r.tools[name]; ok {
			tools = append(tools, tool)
		}
	}
	return tools
}

// IsActionTool determina si es herramienta de acción
func (r *ToolRegistry) IsActionTool(toolName string) bool {
	actionCategories := map[ToolCategory]bool{
		CategoryCommunication: true,
		CategoryWorkflow:      true,
	}

	tool, exists := r.tools[toolName]
	if !exists {
		knownActionTools := map[string]bool{
			"send_image":        true,
			"handoff_to_human":  true,
			"final_answer":      true,
			"schedule_followup": true,
		}
		return knownActionTools[toolName]
	}

	return actionCategories[ToolCategory(tool.Category())]
}

// GetCategories retorna todas las categorías
func (r *ToolRegistry) GetCategories() []ToolCategory {
	cats := make([]ToolCategory, 0, len(r.categories))
	for cat := range r.categories {
		cats = append(cats, cat)
	}
	return cats
}

// Describe retorna información detallada
func (r *ToolRegistry) Describe() map[string]interface{} {
	result := map[string]interface{}{
		"total_tools": len(r.tools),
		"categories":  make(map[string][]string),
		"tools":       []map[string]interface{}{},
	}

	for cat, names := range r.categories {
		result["categories"].(map[string][]string)[string(cat)] = names
	}

	for _, tool := range r.tools {
		toolInfo := map[string]interface{}{
			"name":        tool.Name(),
			"description": tool.Description(),
			"category":    tool.Category(),
			"parameters":  tool.Parameters(),
		}
		if meta, ok := tool.(ToolMetadata); ok {
			toolInfo["version"] = meta.Version()
			toolInfo["examples"] = meta.Examples()
		}
		toolsList := result["tools"].([]map[string]interface{})
		result["tools"] = append(toolsList, toolInfo)
	}

	return result
}

// ToLangChainFormat convierte al formato de LangChain
func (r *ToolRegistry) ToLangChainFormat() []map[string]interface{} {
	functions := make([]map[string]interface{}, 0, len(r.tools))

	for _, tool := range r.tools {
		function := map[string]interface{}{
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters": map[string]interface{}{
				"type":       "object",
				"properties": cleanProperties(tool.Parameters()),
				"required":   getRequiredParams(tool.Parameters()),
			},
		}
		functions = append(functions, function)
	}

	return functions
}

// ToolCall llamada a herramienta
type ToolCall struct {
	ID         string
	ToolName   string
	Parameters map[string]interface{}
}

// ToolResult resultado de ejecutar herramienta
type ToolResult struct {
	ToolCallID string
	ToolName   string
	Success    bool
	Result     interface{}
	Error      string
}

// ExecuteTool ejecuta una herramienta
func (r *ToolRegistry) ExecuteTool(ctx context.Context, call ToolCall, agentCtx *contextmgr.ConversationContext) ToolResult {
	tool, ok := r.Get(call.ToolName)
	if !ok {
		return ToolResult{
			ToolCallID: call.ID,
			ToolName:   call.ToolName,
			Success:    false,
			Error:      fmt.Sprintf("tool not found: %s", call.ToolName),
		}
	}

	log.Printf("🔧 Executing tool: %s with params: %v", call.ToolName, call.Parameters)

	result, err := tool.Execute(ctx, call.Parameters, agentCtx)
	if err != nil {
		log.Printf("❌ Tool %s failed: %v", call.ToolName, err)
		return ToolResult{
			ToolCallID: call.ID,
			ToolName:   call.ToolName,
			Success:    false,
			Error:      err.Error(),
		}
	}

	log.Printf("✅ Tool %s succeeded", call.ToolName)
	return ToolResult{
		ToolCallID: call.ID,
		ToolName:   call.ToolName,
		Success:    true,
		Result:     result,
	}
}

// FormatForLLM formatea resultado para LLM
func (r ToolResult) FormatForLLM() string {
	if !r.Success {
		return fmt.Sprintf("Error executing %s: %s", r.ToolName, r.Error)
	}

	jsonBytes, err := json.MarshalIndent(r.Result, "", "  ")
	if err != nil {
		return fmt.Sprintf("Result of %s: %v", r.ToolName, r.Result)
	}

	return fmt.Sprintf("Result of %s:\n%s", r.ToolName, string(jsonBytes))
}

// Helper functions
func cleanProperties(params map[string]interface{}) map[string]interface{} {
	cleaned := make(map[string]interface{})
	for name, schema := range params {
		if schemaMap, ok := schema.(map[string]interface{}); ok {
			result := make(map[string]interface{})
			for key, value := range schemaMap {
				if key != "required" {
					result[key] = value
				}
			}
			cleaned[name] = result
		}
	}
	return cleaned
}

func getRequiredParams(params map[string]interface{}) []string {
	required := []string{}
	for name, schema := range params {
		if schemaMap, ok := schema.(map[string]interface{}); ok {
			if req, ok := schemaMap["required"].(bool); ok && req {
				required = append(required, name)
			}
		}
	}
	return required
}

// ToolBuilder ayuda a construir herramientas
type ToolBuilder struct {
	name        string
	category    string
	description string
	parameters  map[string]interface{}
	version     string
	examples    []map[string]interface{}
}

// NewToolBuilder crea un builder de herramientas
func NewToolBuilder(name string) *ToolBuilder {
	return &ToolBuilder{
		name:       name,
		parameters: make(map[string]interface{}),
	}
}

// WithCategory establece la categoría
func (b *ToolBuilder) WithCategory(category string) *ToolBuilder {
	b.category = category
	return b
}

// WithDescription establece la descripción
func (b *ToolBuilder) WithDescription(desc string) *ToolBuilder {
	b.description = desc
	return b
}

// WithParameter añade un parámetro
func (b *ToolBuilder) WithParameter(name string, paramType, description string, required bool) *ToolBuilder {
	b.parameters[name] = map[string]interface{}{
		"type":        paramType,
		"description": description,
		"required":    required,
	}
	return b
}

// WithVersion establece la versión
func (b *ToolBuilder) WithVersion(version string) *ToolBuilder {
	b.version = version
	return b
}

// WithExamples establece ejemplos
func (b *ToolBuilder) WithExamples(examples []map[string]interface{}) *ToolBuilder {
	b.examples = examples
	return b
}

// BuildStringParameter añade un parámetro string
func (b *ToolBuilder) BuildStringParameter(name, description string, required bool) *ToolBuilder {
	b.parameters[name] = map[string]interface{}{
		"type":        "string",
		"description": description,
		"required":    required,
	}
	return b
}

// BuildIntParameter añade un parámetro entero
func (b *ToolBuilder) BuildIntParameter(name, description string, required bool, defaultValue int) *ToolBuilder {
	b.parameters[name] = map[string]interface{}{
		"type":        "integer",
		"description": description,
		"required":    required,
		"default":     defaultValue,
	}
	return b
}

// BuildBoolParameter añade un parámetro booleano
func (b *ToolBuilder) BuildBoolParameter(name, description string, required bool, defaultValue bool) *ToolBuilder {
	b.parameters[name] = map[string]interface{}{
		"type":        "boolean",
		"description": description,
		"required":    required,
		"default":     defaultValue,
	}
	return b
}

// Tool implementation helper
type BaseToolImpl struct {
	NameVal        string
	CategoryVal    string
	DescriptionVal string
	ParametersVal  map[string]interface{}
	ExecuteFn      func(ctx context.Context, args map[string]interface{}, agentCtx *contextmgr.ConversationContext) (interface{}, error)
}

func (t *BaseToolImpl) Name() string                       { return t.NameVal }
func (t *BaseToolImpl) Category() string                   { return t.CategoryVal }
func (t *BaseToolImpl) Description() string                { return t.DescriptionVal }
func (t *BaseToolImpl) Parameters() map[string]interface{} { return t.ParametersVal }
func (t *BaseToolImpl) Execute(ctx context.Context, args map[string]interface{}, agentCtx *contextmgr.ConversationContext) (interface{}, error) {
	return t.ExecuteFn(ctx, args, agentCtx)
}

// NewTool crea una herramienta desde un builder
func (b *ToolBuilder) NewTool(executeFn func(ctx context.Context, args map[string]interface{}, agentCtx *contextmgr.ConversationContext) (interface{}, error)) BaseTool {
	return &BaseToolImpl{
		NameVal:        b.name,
		CategoryVal:    b.category,
		DescriptionVal: b.description,
		ParametersVal:  b.parameters,
		ExecuteFn:      executeFn,
	}
}
