package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/asteby/metacore-kernel/vector"

	"github.com/google/uuid"
)

// SimpleKnowledgeService servicio de conocimiento simplificado (sin Redis)
type SimpleKnowledgeService struct {
	vectorStore VectorStore
	embeddings  EmbeddingService
}

// VectorStore interfaz para búsqueda vectorial
// VectorStore interfaz para búsqueda vectorial
type VectorStore interface {
	Search(collection string, vector []float32, limit int, filter map[string]interface{}) ([]vector.Point, error)
}

// EmbeddingService interfaz para generar embeddings
type EmbeddingService interface {
	GenerateEmbeddings(text string) ([]float32, error)
}

// KnowledgeResult resultado de búsqueda
type KnowledgeResult struct {
	Chunks []KnowledgeChunk `json:"chunks"`
	Images []KnowledgeImage `json:"images"`
}

// KnowledgeChunk fragmento de conocimiento
type KnowledgeChunk struct {
	Content     string  `json:"content"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Type        string  `json:"type"`
	Score       float64 `json:"score"`
}

// KnowledgeImage imagen en el conocimiento
type KnowledgeImage struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Caption     string `json:"caption"`
}

// NewSimpleKnowledgeService crea un nuevo servicio
func NewSimpleKnowledgeService(vectorStore VectorStore, embeddings EmbeddingService) *SimpleKnowledgeService {
	return &SimpleKnowledgeService{
		vectorStore: vectorStore,
		embeddings:  embeddings,
	}
}

// SearchSimple búsqueda con BGE-M3 embeddings
func (s *SimpleKnowledgeService) SearchSimple(ctx context.Context, query string, agentID uuid.UUID, maxResults int, filters map[string]interface{}) (interface{}, error) {
	log.Printf("🔍 Searching knowledge with BGE-M3: '%s' (max: %d)", query, maxResults)

	// 1. Generar embedding con BGE-M3
	vector, err := s.embeddings.GenerateEmbeddings(query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate embedding: %w", err)
	}

	// 2. Filtros por agente
	// 2. Filtros por agente
	combinedFilters := map[string]interface{}{
		"agent_id": agentID.String(),
	}

	// 3. Búsqueda vectorial con BGE-M3
	collection := "knowledge_items"
	rawResults, err := s.vectorStore.Search(collection, vector, maxResults, combinedFilters)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}

	// 4. Procesar resultados
	result := s.processResults(rawResults)

	log.Printf("📊 Found %d chunks, %d images", len(result.Chunks), len(result.Images))
	return result, nil
}

// processResults procesa los resultados de Vector Store
func (s *SimpleKnowledgeService) processResults(rawResults []vector.Point) *KnowledgeResult {
	result := &KnowledgeResult{
		Chunks: []KnowledgeChunk{},
		Images: []KnowledgeImage{},
	}

	seenContent := make(map[string]bool)
	seenImages := make(map[string]bool)

	for _, point := range rawResults {
		payload := point.Payload
		if payload == nil {
			continue
		}

		score := 0.0
		// PGVectorStore returns similarity in payload as "_similarity"
		// Qdrant might return it differently, but our store implementation handles it.
		if s, ok := payload["_similarity"].(float64); ok {
			score = s
		} else if s, ok := payload["score"].(float64); ok {
			score = s
		}

		contentType, _ := payload["type"].(string)
		content, _ := payload["content"].(string)
		title, _ := payload["title"].(string)
		description, _ := payload["description"].(string)
		fileURL, _ := payload["file_url"].(string)

		// Procesar imágenes
		if contentType == "image" && fileURL != "" && !seenImages[fileURL] {
			img := KnowledgeImage{
				URL:         fileURL,
				Title:       title,
				Description: description,
				Caption:     content,
			}
			result.Images = append(result.Images, img)
			seenImages[fileURL] = true
		}

		// Procesar chunks de texto
		if content != "" && !seenContent[content] {
			chunk := KnowledgeChunk{
				Content:     content,
				Title:       title,
				Description: description,
				Type:        contentType,
				Score:       score,
			}
			result.Chunks = append(result.Chunks, chunk)
			seenContent[content] = true
		}
	}

	return result
}

// getFloat64 extrae float64 de manera segura
func getFloat64(item map[string]interface{}, key string, defaultVal float64) float64 {
	if val, ok := item[key].(float64); ok {
		return val
	}
	if val, ok := item[key].(int); ok {
		return float64(val)
	}
	return defaultVal
}

// FormatForAgent formatea el resultado para el agente
func (r *KnowledgeResult) FormatForAgent() string {
	if len(r.Chunks) == 0 && len(r.Images) == 0 {
		return "No se encontró información relevante en la base de conocimiento."
	}

	var builder strings.Builder

	// Detectar si hay múltiples productos/variantes similares
	ambiguityWarning := r.detectAmbiguity()
	if ambiguityWarning != "" {
		builder.WriteString(ambiguityWarning)
	}

	builder.WriteString("Información encontrada (ordenada por relevancia):\n\n")
	builder.WriteString("⚠️ IMPORTANTE: Los resultados con mayor score son MÁS relevantes. Prioriza siempre la información con score más alto.\n\n")

	// Chunks de texto con scores visibles
	for i, chunk := range r.Chunks {
		builder.WriteString(fmt.Sprintf("%d. [RELEVANCIA: %.2f] ", i+1, chunk.Score))
		if chunk.Title != "" {
			builder.WriteString(fmt.Sprintf("*%s*\n", chunk.Title))
		}
		builder.WriteString(fmt.Sprintf("%s\n\n", chunk.Content))
	}

	// Imágenes disponibles
	if len(r.Images) > 0 {
		builder.WriteString("Imágenes disponibles:\n")
		for _, img := range r.Images {
			builder.WriteString(fmt.Sprintf("- URL: %s", img.URL))
			if img.Title != "" {
				builder.WriteString(fmt.Sprintf(" (Título: %s)", img.Title))
			}
			builder.WriteString("\n")
		}
		builder.WriteString("\nPuedes usar send_image para enviar estas imágenes al usuario.\n")
	}

	return builder.String()
}

// detectAmbiguity detecta si hay múltiples productos/variantes similares
// MODIFICADO: Ahora es INTELIGENTE - solo advierte cuando hay ambigüedad REAL
// (mismo producto con diferentes variantes como tamaños o colores)
// NO advierte para productos claramente diferentes
func (r *KnowledgeResult) detectAmbiguity() string {
	if len(r.Chunks) < 2 {
		return ""
	}

	// Extraer títulos únicos de productos
	titles := make(map[string]bool)
	var uniqueTitles []string
	for _, chunk := range r.Chunks {
		title := strings.TrimSpace(chunk.Title)
		if title != "" && !titles[title] {
			titles[title] = true
			uniqueTitles = append(uniqueTitles, title)
		}
	}

	// Si hay múltiples títulos, verificar ambigüedad real
	if len(uniqueTitles) >= 2 {
		maxScore := r.Chunks[0].Score
		similarCount := 0
		for _, chunk := range r.Chunks {
			if maxScore-chunk.Score < 0.10 { // Umbral más estricto
				similarCount++
			}
		}

		// Solo advertir si hay AMBIGÜEDAD REAL (scores muy similares)
		// y los títulos son CASI IGUALES (mismo producto, diferente presentación)
		if similarCount >= 2 {
			// Verificar si son prácticamente el mismo producto
			if areEssentiallySameProduct(uniqueTitles) {
				var warning strings.Builder
				warning.WriteString("⚠️ Múltiples opciones similares encontradas.\n")
				return warning.String()
			}

			// Son productos diferentes - no hay ambigüedad
			log.Printf("📋 Múltiples productos (%v), el LLM decidirá", uniqueTitles)
			return ""
		}
	}

	return ""
}

// areEssentiallySameProduct verifica si los títulos representan el mismo producto
// con diferentes variantes (tamaño, color, presentación)
func areEssentiallySameProduct(titles []string) bool {
	if len(titles) < 2 {
		return false
	}

	// Comparar los primeros 2-3 palabras de cada título
	// Si son iguales, es el mismo producto con diferente variante
	var baseNames []string
	for _, title := range titles {
		words := strings.Fields(title)
		if len(words) >= 2 {
			// Tomar las primeras 2 palabras como "base" del producto
			baseNames = append(baseNames, strings.ToLower(strings.Join(words[:2], " ")))
		} else {
			baseNames = append(baseNames, strings.ToLower(title))
		}
	}

	// Si todas las bases son iguales, es el mismo producto
	firstBase := baseNames[0]
	for i := 1; i < len(baseNames); i++ {
		// Comparar con tolerancia (contiene o es igual)
		if !strings.Contains(firstBase, baseNames[i]) && !strings.Contains(baseNames[i], firstBase) {
			return false // Son productos diferentes
		}
	}

	return true // Son el mismo producto con diferentes variantes
}
