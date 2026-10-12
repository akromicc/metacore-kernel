package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/asteby/metacore-kernel/agent/contextmgr"
)

const maxImagePromptLen = 800

// GenerateImageTool asks hub's shared image-generation provider (Cloudflare
// Workers AI today) for one image and stages it as a chat embed — same
// "return a spec, the frontend renders it inline" shape as ShowChartTool.
// Billing/abuse-prevention lives in hub (Pulse Sparks debit per image, see
// hub's internal/api/ai_images.go); this tool is just the Aby-facing
// wrapper, same pattern as chat completions going through hubai.Client.
type GenerateImageTool struct {
	gen ImageGenerator
}

// GeneratedImage is the provider-neutral result of an ImageGenerator.
type GeneratedImage struct {
	Provider    string
	Model       string
	ImageBase64 string
	ContentType string
}

// ImageGenerator is implemented by the host (hub client, local provider...).
// feature is the billing/usage tag the host attributes the call to.
type ImageGenerator interface {
	GenerateImage(ctx context.Context, prompt, format, feature string) (*GeneratedImage, error)
}

// NewGenerateImageTool wires the tool to a host-provided generator.
func NewGenerateImageTool(gen ImageGenerator) *GenerateImageTool { return &GenerateImageTool{gen: gen} }

func (t *GenerateImageTool) Name() string     { return "generate_image" }
func (t *GenerateImageTool) Category() string { return "creative" }
func (t *GenerateImageTool) Description() string {
	return "Generate an image from a text prompt (Stable Diffusion via hub). Use when the user asks to create/draw/generate/mockup an image, illustration, banner, or product visual. Not for editing an existing photo — this only creates new images from a description. Write the prompt in English for best results, be specific about style/composition/lighting."
}

func (t *GenerateImageTool) Parameters() map[string]any {
	return map[string]any{
		"prompt": map[string]any{
			"type": "string", "description": "Detailed image description (English works best)", "required": true,
		},
		"format": map[string]any{
			"type": "string", "description": "Aspect ratio hint: 1:1 | 4:5 | 9:16 | 16:9 (advisory, not all providers honor it)",
		},
	}
}

func (t *GenerateImageTool) Execute(ctx context.Context, params map[string]any, agentCtx *contextmgr.ConversationContext) (any, error) {
	prompt := clipRunes(strParam(params, "prompt"), maxImagePromptLen)
	if prompt == "" {
		return nil, fmt.Errorf("prompt: se necesita una descripción de la imagen a generar")
	}
	format := strings.TrimSpace(strParam(params, "format"))
	if t.gen == nil {
		return nil, fmt.Errorf("generación de imágenes no configurada")
	}

	out, err := t.gen.GenerateImage(ctx, prompt, format, "aby")
	if err != nil {
		return nil, fmt.Errorf("no se pudo generar la imagen: %w", err)
	}
	return map[string]any{
		"ok":           true,
		"prompt":       prompt,
		"provider":     out.Provider,
		"model":        out.Model,
		"image_base64": out.ImageBase64,
		"content_type": out.ContentType,
	}, nil
}
