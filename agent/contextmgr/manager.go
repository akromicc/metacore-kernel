package contextmgr

import (
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ConversationContext mantiene el estado de una conversación
type ConversationContext struct {
	ConversationID uuid.UUID `json:"conversation_id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	ContactID      uuid.UUID `json:"contact_id"`
	AgentID        uuid.UUID `json:"agent_id"`
	// UserID is the HUMAN the copilot acts on behalf of. Business agents
	// resolve their user via the agents table (AgentID lookup); the embedded
	// copilot is a transient in-memory agent (AgentID == uuid.Nil since #736),
	// so identity/permissions MUST come from here instead.
	UserID           uuid.UUID              `json:"user_id"`
	LastIntent       string                 `json:"last_intent"`
	MentionedTopics  []string               `json:"mentioned_topics"`
	SentImages       []string               `json:"sent_images"`   // URLs de imágenes ya enviadas
	SentProducts     []string               `json:"sent_products"` // Productos ya mencionados
	UserPreferences  map[string]string      `json:"user_preferences"`
	ConversationFlow []FlowStep             `json:"conversation_flow"`
	LastUpdate       time.Time              `json:"last_update"`
	MessageCount     int                    `json:"message_count"`
	Metadata         map[string]interface{} `json:"metadata"` // Generic metadata for orchestrator context, etc.
	History          []Message              `json:"history"`
	// HistorySource selects where refreshHistory reads turns from: "" (the
	// business `messages` table, default) or "copilot" (`copilot_messages`,
	// the embedded ERP copilot's own storage — separate domain on purpose).
	HistorySource string `json:"history_source,omitempty"`
}

type Message struct {
	Role    string `json:"role"` // "user" or "assistant"
	Content string `json:"content"`
}

// FlowStep representa un paso en el flujo de conversación
type FlowStep struct {
	Intent    string    `json:"intent"`
	Timestamp time.Time `json:"timestamp"`
	Action    string    `json:"action"`
}

// Manager gestiona el contexto de conversaciones (in-memory, sin Redis)
type Manager struct {
	cache sync.Map // map[uuid.UUID]*ConversationContext
	db    *gorm.DB
}

// NewManager crea un nuevo gestor de contexto
func NewManager(db *gorm.DB) *Manager {
	return &Manager{
		db: db,
	}
}

// GetContext obtiene el contexto de una conversación
func (m *Manager) GetContext(convID uuid.UUID) *ConversationContext {
	// 1. Intentar obtener de caché in-memory
	var convCtx *ConversationContext
	if cached, ok := m.cache.Load(convID); ok {
		log.Printf("📦 Context loaded from memory for %s", convID)
		convCtx = cached.(*ConversationContext)
	} else {
		// 2. Reconstruir desde DB
		convCtx = m.rebuildFromDB(convID)
		// 3. Guardar en caché
		m.cache.Store(convID, convCtx)
		log.Printf("🔨 Context rebuilt from DB for %s", convID)
	}

	// ALWAYS refresh history from DB to ensure we have the latest conversation turns
	m.refreshHistory(convCtx)
	return convCtx
}

// SaveContext guarda el contexto en caché
func (m *Manager) SaveContext(convCtx *ConversationContext) {
	convCtx.LastUpdate = time.Now()
	m.cache.Store(convCtx.ConversationID, convCtx)
}

// rebuildFromDB reconstruye el contexto desde la base de datos
func (m *Manager) rebuildFromDB(convID uuid.UUID) *ConversationContext {
	// Estructura básica
	convCtx := &ConversationContext{
		ConversationID:   convID,
		MentionedTopics:  []string{},
		SentImages:       []string{},
		SentProducts:     []string{},
		UserPreferences:  make(map[string]string),
		ConversationFlow: []FlowStep{},
		LastUpdate:       time.Now(),
		Metadata:         make(map[string]interface{}),
		History:          []Message{},
	}

	// Cargar información de la conversación
	type ConvInfo struct {
		OrganizationID uuid.UUID `gorm:"column:organization_id"`
		ContactID      uuid.UUID `gorm:"column:contact_id"`
		AgentID        uuid.UUID `gorm:"column:process_id"`
	}
	var info ConvInfo
	if err := m.db.Table("conversations").
		Select("organization_id", "contact_id", "process_id").
		Where("id = ?", convID).
		First(&info).Error; err == nil {
		convCtx.OrganizationID = info.OrganizationID
		convCtx.ContactID = info.ContactID
		convCtx.AgentID = info.AgentID
	}

	// Analizar últimos mensajes para reconstruir contexto
	type MessageInfo struct {
		Content     string
		ContentType string
		MediaURL    string
		SenderType  string
	}
	return convCtx
}

// GetCopilotContext returns (or creates) the context of a copilot thread.
// Unlike GetContext it never touches the business conversations/messages
// tables: history comes from copilot_messages. It pre-seeds the shared cache,
// so a later GetContext(convID) from inside the orchestrator finds a context
// already marked with the copilot source and keeps reading the right table.
func (m *Manager) GetCopilotContext(convID, orgID, userID uuid.UUID) *ConversationContext {
	if cached, ok := m.cache.Load(convID); ok {
		convCtx := cached.(*ConversationContext)
		// Re-stamp on every turn: the cache can outlive a session and the
		// permissions must always be the CURRENT user's.
		convCtx.UserID = userID
		convCtx.OrganizationID = orgID
		convCtx.HistorySource = "copilot"
		m.refreshHistory(convCtx)
		return convCtx
	}
	convCtx := &ConversationContext{
		ConversationID:   convID,
		OrganizationID:   orgID,
		UserID:           userID,
		HistorySource:    "copilot",
		MentionedTopics:  []string{},
		SentImages:       []string{},
		SentProducts:     []string{},
		UserPreferences:  make(map[string]string),
		ConversationFlow: []FlowStep{},
		LastUpdate:       time.Now(),
		Metadata:         make(map[string]interface{}),
		History:          []Message{},
	}
	m.cache.Store(convID, convCtx)
	m.refreshHistory(convCtx)
	return convCtx
}

// refreshCopilotHistory loads the last turns of a copilot thread from
// copilot_messages (role is already in LLM convention, no mapping needed).
func (m *Manager) refreshCopilotHistory(convCtx *ConversationContext) {
	type row struct {
		Role      string
		Content   string
		CreatedAt time.Time
	}
	var rows []row
	if err := m.db.Table("copilot_messages").
		Select("role", "content", "created_at").
		Where("conversation_id = ?", convCtx.ConversationID).
		Order("created_at DESC").
		Limit(20).
		Find(&rows).Error; err != nil {
		return
	}
	if len(rows) == 0 && len(convCtx.History) > 0 {
		return // freshly created turn not yet committed — trust memory
	}
	convCtx.MessageCount = len(rows)
	convCtx.History = make([]Message, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		convCtx.History = append(convCtx.History, Message{Role: rows[i].Role, Content: rows[i].Content})
	}
}

// refreshHistory actualiza el historial de mensajes desde la BD
func (m *Manager) refreshHistory(convCtx *ConversationContext) {
	if convCtx.HistorySource == "copilot" {
		m.refreshCopilotHistory(convCtx)
		return
	}
	// Analizar últimos mensajes para reconstruir historial
	type MessageInfo struct {
		Content     string
		ContentType string
		MediaURL    string
		SenderType  string
		CreatedAt   time.Time
	}
	var messages []MessageInfo
	if err := m.db.Table("messages").
		Select("content", "content_type", "media_url", "sender_type", "created_at").
		Where("conversation_id = ?", convCtx.ConversationID).
		Order("created_at DESC"). // Get newest first
		Limit(20).                // Last 20 messages for deeper context
		Find(&messages).Error; err == nil {

		// If DB returns empty validation, DO NOT wipe history immediately
		// because we might be in a Simulator/Memory-only session.
		// Only wipe if we actually found something or if we know for sure it's a DB session.
		if len(messages) == 0 {
			// If we have history in memory but DB is empty, trust memory (Simulator case)
			if len(convCtx.History) > 0 {
				return
			}
		}

		convCtx.MessageCount = len(messages)

		// Reset history to ensure we don't duplicate if called multiple times on same object
		convCtx.History = []Message{}

		// Note: We don't reset SentImages here because that might clear older images not in the last 10 messages.
		// However, for context correctness, we should ensuring we cover recent images.

		// Process messages (they are in reverse order: newest first)
		for i := len(messages) - 1; i >= 0; i-- {
			msg := messages[i]

			// Populate History
			role := "user"
			if msg.SenderType == "bot" || msg.SenderType == "agent" {
				role = "assistant"
			}
			convCtx.History = append(convCtx.History, Message{
				Role:    role,
				Content: msg.Content,
			})

			// Add recent images to SentImages if not there
			if (msg.SenderType == "bot" || msg.SenderType == "agent") && msg.ContentType == "image" && msg.MediaURL != "" {
				convCtx.AddSentImage(msg.MediaURL)
			}
		}
	}
}

// UpdateContext actualiza el contexto con una función
func (m *Manager) UpdateContext(convID uuid.UUID, update func(*ConversationContext)) {
	convCtx := m.GetContext(convID)
	update(convCtx)
	convCtx.LastUpdate = time.Now()
	m.SaveContext(convCtx)
}

// ClearContext limpia el contexto de una conversación
func (m *Manager) ClearContext(convID uuid.UUID) {
	m.cache.Delete(convID)
	log.Printf("🗑️ Context cleared for %s", convID)
}

// HasSentImage verifica si una imagen ya fue enviada
func (ctx *ConversationContext) HasSentImage(imageURL string) bool {
	for _, sent := range ctx.SentImages {
		if sent == imageURL {
			return true
		}
	}
	return false
}

// AddSentImage registra una imagen como enviada
func (ctx *ConversationContext) AddSentImage(imageURL string) {
	if !ctx.HasSentImage(imageURL) {
		ctx.SentImages = append(ctx.SentImages, imageURL)
	}
}

// HasMentionedProduct verifica si un producto ya fue mencionado
func (ctx *ConversationContext) HasMentionedProduct(product string) bool {
	for _, mentioned := range ctx.SentProducts {
		if mentioned == product {
			return true
		}
	}
	return false
}

// AddMentionedProduct registra un producto como mencionado
func (ctx *ConversationContext) AddMentionedProduct(product string) {
	if !ctx.HasMentionedProduct(product) {
		ctx.SentProducts = append(ctx.SentProducts, product)
	}
}
