package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TypingIndicatorManager gestiona los indicadores de "escribiendo..."
type TypingIndicatorManager struct {
	whatsappServiceURL string
	activeIndicators   sync.Map // map[conversationID]bool
	stopChannels       sync.Map // map[conversationID]chan bool
}

// NewTypingIndicatorManager crea un nuevo gestor de indicadores
func NewTypingIndicatorManager(whatsappServiceURL string) *TypingIndicatorManager {
	return &TypingIndicatorManager{
		whatsappServiceURL: whatsappServiceURL,
	}
}

// StartTyping inicia el indicador de "escribiendo..."
func (t *TypingIndicatorManager) StartTyping(sessionID, to string, conversationID uuid.UUID) {
	// Verificar si ya está activo
	if _, exists := t.activeIndicators.Load(conversationID); exists {
		log.Printf("⌨️ Typing indicator already active for %s", conversationID)
		return
	}

	// Marcar como activo
	t.activeIndicators.Store(conversationID, true)

	// Crear canal de stop
	stopChan := make(chan bool, 1)
	t.stopChannels.Store(conversationID, stopChan)

	log.Printf("⌨️ Starting typing indicator for %s", conversationID)

	// Iniciar goroutine que mantiene el indicador activo
	go t.keepTyping(sessionID, to, conversationID, stopChan)
}

// keepTyping mantiene el indicador activo enviando presencia periódicamente
func (t *TypingIndicatorManager) keepTyping(sessionID, to string, conversationID uuid.UUID, stopChan chan bool) {
	// Enviar presencia inicial
	t.sendPresence(sessionID, to, "composing")

	// Mantener activo cada 10 segundos (WhatsApp timeout es ~15s)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			// Detener indicador
			t.sendPresence(sessionID, to, "paused")
			log.Printf("⌨️ Stopped typing indicator for %s", conversationID)
			return
		case <-ticker.C:
			// Renovar indicador
			t.sendPresence(sessionID, to, "composing")
			log.Printf("⌨️ Renewed typing indicator for %s", conversationID)
		}
	}
}

// StopTyping detiene el indicador de "escribiendo..."
func (t *TypingIndicatorManager) StopTyping(conversationID uuid.UUID) {
	// Verificar si está activo
	if _, exists := t.activeIndicators.Load(conversationID); !exists {
		return
	}

	// Obtener canal de stop
	if stopChanInterface, ok := t.stopChannels.Load(conversationID); ok {
		stopChan := stopChanInterface.(chan bool)

		// Enviar señal de stop (non-blocking)
		select {
		case stopChan <- true:
		default:
		}

		// Limpiar
		t.stopChannels.Delete(conversationID)
	}

	// Marcar como inactivo
	t.activeIndicators.Delete(conversationID)
}

// sendPresence envía actualización de presencia a WhatsApp
func (t *TypingIndicatorManager) sendPresence(sessionID, to, state string) {
	payload := map[string]interface{}{
		"sessionId": sessionID,
		"to":        to,
		"state":     state, // "composing" o "paused"
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ Failed to marshal presence payload: %v", err)
		return
	}

	url := fmt.Sprintf("%s/presence/update", t.whatsappServiceURL)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("❌ Failed to send presence update: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("⚠️ Presence update returned status %d", resp.StatusCode)
	}
}

// IsTyping verifica si el indicador está activo
func (t *TypingIndicatorManager) IsTyping(conversationID uuid.UUID) bool {
	_, exists := t.activeIndicators.Load(conversationID)
	return exists
}

// StopAll detiene todos los indicadores (útil para cleanup)
func (t *TypingIndicatorManager) StopAll() {
	t.activeIndicators.Range(func(key, value interface{}) bool {
		convID := key.(uuid.UUID)
		t.StopTyping(convID)
		return true
	})
}
