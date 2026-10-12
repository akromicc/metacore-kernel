package tools

import (
	"context"
	"fmt"
	"github.com/asteby/metacore-kernel/agent/contextmgr"
	"log"
	"math"
	"strconv"
	"strings"

	"github.com/Knetic/govaluate"
)

// CalculatorTool herramienta para realizar cálculos matemáticos
type CalculatorTool struct{}

func NewCalculatorTool() *CalculatorTool {
	return &CalculatorTool{}
}

func (t *CalculatorTool) Name() string {
	return "calculator"
}

func (t *CalculatorTool) Description() string {
	return "Useful for performing mathematical calculations. Use this tool when you need to calculate prices, discounts, totals, or any other math operation. The expression should be a valid mathematical expression."
}

func (t *CalculatorTool) Category() string {
	return "utility"
}

func (t *CalculatorTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"expression": map[string]interface{}{
			"type":        "string",
			"description": "The mathematical expression to evaluate (e.g., '100 * 0.16', '500 + 200', '(100 - 20) * 1.16')",
			"required":    true,
		},
	}
}

func (t *CalculatorTool) Execute(ctx context.Context, params map[string]interface{}, convContext *contextmgr.ConversationContext) (interface{}, error) {
	expressionStr, ok := params["expression"].(string)
	if !ok {
		return nil, fmt.Errorf("expression parameter is required and must be a string")
	}

	// Clean expression
	expressionStr = strings.TrimSpace(expressionStr)

	// Create expression
	expression, err := govaluate.NewEvaluableExpression(expressionStr)
	if err != nil {
		log.Printf("❌ Invalid math expression: %s, error: %v", expressionStr, err)
		return nil, fmt.Errorf("invalid expression: %v", err)
	}

	// Evaluate
	result, err := expression.Evaluate(nil)
	if err != nil {
		log.Printf("❌ Math evaluation failed: %s, error: %v", expressionStr, err)
		return nil, fmt.Errorf("evaluation failed: %v", err)
	}

	// Format result
	var resultStr string
	switch v := result.(type) {
	case float64:
		// Format to eliminate unnecessary decimals regarding currency if possible, but keep precision
		if v == math.Trunc(v) {
			resultStr = fmt.Sprintf("%.0f", v)
		} else {
			resultStr = fmt.Sprintf("%.4f", v)
		}
	case int:
		resultStr = strconv.Itoa(v)
	default:
		resultStr = fmt.Sprintf("%v", v)
	}

	log.Printf("🧮 Calculator: %s = %s", expressionStr, resultStr)

	return map[string]interface{}{
		"result":     resultStr,
		"expression": expressionStr,
	}, nil
}
