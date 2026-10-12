package agent

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MessageQueue gestiona la cola de mensajes por conversación
type MessageQueue struct {
	queues          sync.Map // map[uuid.UUID]*ConversationQueue
	debounceTime    time.Duration
	processingDelay time.Duration
	typingManager   *TypingIndicatorManager
}

// ConversationQueue cola de mensajes para una conversación
type ConversationQueue struct {
	conversationID uuid.UUID
	sessionID      string // WhatsApp session ID
	to             string // Destinatario (JID)
	messages       []QueuedMessage
	timer          *time.Timer
	processing     bool
	mu             sync.Mutex
	processor      MessageProcessor
}

// QueuedMessage mensaje en cola
type QueuedMessage struct {
	Content   string
	Timestamp time.Time
	MessageID string
}

// MessageProcessor interfaz para procesar mensajes
type MessageProcessor interface {
	ProcessBatch(ctx context.Context, conversationID uuid.UUID, messages []QueuedMessage) error
}

// NewMessageQueue crea una nueva cola de mensajes
func NewMessageQueue(debounceTime, processingDelay time.Duration, whatsappServiceURL string) *MessageQueue {
	if debounceTime == 0 {
		debounceTime = 2 * time.Second // Default: esperar 2s después del último mensaje
	}
	if processingDelay == 0 {
		processingDelay = 500 * time.Millisecond // Default: delay mínimo entre procesamiento
	}

	return &MessageQueue{
		debounceTime:    debounceTime,
		processingDelay: processingDelay,
		typingManager:   NewTypingIndicatorManager(whatsappServiceURL),
	}
}

// Enqueue añade un mensaje a la cola
func (mq *MessageQueue) Enqueue(conversationID uuid.UUID, sessionID, to string, message QueuedMessage, processor MessageProcessor) {
	// Obtener o crear cola para esta conversación
	queueInterface, _ := mq.queues.LoadOrStore(conversationID, &ConversationQueue{
		conversationID: conversationID,
		sessionID:      sessionID,
		to:             to,
		messages:       []QueuedMessage{},
		processor:      processor,
	})

	queue := queueInterface.(*ConversationQueue)
	queue.mu.Lock()
	defer queue.mu.Unlock()

	// Si ya está procesando, añadir a cola y salir
	if queue.processing {
		log.Printf("⏳ Conversation %s is processing, queueing message", conversationID)
		queue.messages = append(queue.messages, message)
		return
	}

	// Añadir mensaje a la cola
	queue.messages = append(queue.messages, message)
	log.Printf("📥 Queued message for %s (total: %d)", conversationID, len(queue.messages))

	// Si es el primer mensaje, iniciar typing indicator
	if len(queue.messages) == 1 {
		mq.typingManager.StartTyping(sessionID, to, conversationID)
	}

	// Cancelar timer anterior si existe
	if queue.timer != nil {
		queue.timer.Stop()
	}

	// Crear nuevo timer para debounce
	queue.timer = time.AfterFunc(mq.debounceTime, func() {
		mq.processQueue(conversationID)
	})
}

// processQueue procesa todos los mensajes en cola
func (mq *MessageQueue) processQueue(conversationID uuid.UUID) {
	queueInterface, ok := mq.queues.Load(conversationID)
	if !ok {
		return
	}

	queue := queueInterface.(*ConversationQueue)
	queue.mu.Lock()

	// Verificar si hay mensajes
	if len(queue.messages) == 0 {
		queue.mu.Unlock()
		return
	}

	// Marcar como procesando
	queue.processing = true
	messagesToProcess := make([]QueuedMessage, len(queue.messages))
	copy(messagesToProcess, queue.messages)
	queue.messages = []QueuedMessage{} // Limpiar cola
	queue.mu.Unlock()

	log.Printf("🔄 Processing %d messages for conversation %s", len(messagesToProcess), conversationID)

	// Delay de "pensamiento" antes de procesar (simula comportamiento humano)
	// Entre 1.5s y 3s dependiendo de la longitud del mensaje
	combinedLen := 0
	for _, msg := range messagesToProcess {
		combinedLen += len(msg.Content)
	}
	// Más texto = más tiempo de "lectura" (min 1.5s, max 3.5s)
	thinkDelay := 1500 + (combinedLen * 15) // 15ms por caracter
	if thinkDelay > 3500 {
		thinkDelay = 3500
	}
	log.Printf("💭 Thinking delay: %dms for %d chars", thinkDelay, combinedLen)
	time.Sleep(time.Duration(thinkDelay) * time.Millisecond)

	// Procesar mensajes
	ctx := context.Background()
	if err := queue.processor.ProcessBatch(ctx, conversationID, messagesToProcess); err != nil {
		log.Printf("❌ Failed to process messages for %s: %v", conversationID, err)
	}

	// Detener typing indicator DESPUÉS de enviar la respuesta
	// El processor debe manejar esto internamente antes de retornar
	mq.typingManager.StopTyping(conversationID)

	// Esperar delay mínimo antes de permitir nuevo procesamiento
	time.Sleep(mq.processingDelay)

	queue.mu.Lock()
	queue.processing = false

	// Si llegaron más mensajes mientras procesábamos, programar nuevo procesamiento
	if len(queue.messages) > 0 {
		log.Printf("📬 New messages arrived during processing, scheduling next batch")

		// Reiniciar typing indicator
		mq.typingManager.StartTyping(queue.sessionID, queue.to, conversationID)

		if queue.timer != nil {
			queue.timer.Stop()
		}
		queue.timer = time.AfterFunc(mq.debounceTime, func() {
			mq.processQueue(conversationID)
		})
	}
	queue.mu.Unlock()
}

// IsProcessing verifica si una conversación está siendo procesada
func (mq *MessageQueue) IsProcessing(conversationID uuid.UUID) bool {
	queueInterface, ok := mq.queues.Load(conversationID)
	if !ok {
		return false
	}

	queue := queueInterface.(*ConversationQueue)
	queue.mu.Lock()
	defer queue.mu.Unlock()

	return queue.processing
}

// GetQueueSize obtiene el tamaño de la cola para una conversación
func (mq *MessageQueue) GetQueueSize(conversationID uuid.UUID) int {
	queueInterface, ok := mq.queues.Load(conversationID)
	if !ok {
		return 0
	}

	queue := queueInterface.(*ConversationQueue)
	queue.mu.Lock()
	defer queue.mu.Unlock()

	return len(queue.messages)
}

// CombineMessages combina múltiples mensajes en uno solo de forma inteligente
// Detecta mensajes fragmentados como "hola" + "como" + "estas" → "hola como estas"
func CombineMessages(messages []QueuedMessage) string {
	if len(messages) == 0 {
		return ""
	}

	if len(messages) == 1 {
		return messages[0].Content
	}

	// Analizar si son mensajes fragmentados (palabras sueltas sin puntuación)
	allShort := true
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		// Mensaje "largo" = más de 30 caracteres o tiene puntuación final
		if len(content) > 30 || endsWithPunctuation(content) {
			allShort = false
			break
		}
	}

	combined := ""
	for i, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		combined += content

		if i < len(messages)-1 {
			nextContent := strings.TrimSpace(messages[i+1].Content)

			if allShort {
				// Mensajes fragmentados: unir con espacio simple
				combined += " "
			} else if endsWithPunctuation(content) {
				// Mensaje completo: separar con salto de línea
				combined += "\n"
			} else if startsWithLowercase(nextContent) {
				// Continuación de frase: espacio simple
				combined += " "
			} else {
				// Default: separar con salto de línea
				combined += "\n"
			}
		}
	}

	log.Printf("🔗 Combined %d messages (fragmented=%v): '%s'", len(messages), allShort, truncateForLog(combined, 100))
	return combined
}

// endsWithPunctuation verifica si termina con puntuación
func endsWithPunctuation(content string) bool {
	if len(content) == 0 {
		return false
	}
	lastChar := content[len(content)-1]
	return lastChar == '.' || lastChar == '?' || lastChar == '!' || lastChar == ':'
}

// startsWithLowercase verifica si empieza con minúscula (continuación de frase)
func startsWithLowercase(content string) bool {
	if len(content) == 0 {
		return false
	}
	firstChar := rune(content[0])
	return firstChar >= 'a' && firstChar <= 'z'
}

// truncateForLog trunca texto para logging
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Stats estadísticas de la cola
type QueueStats struct {
	TotalQueues      int
	ProcessingQueues int
	TotalMessages    int
}

// GetStats obtiene estadísticas de todas las colas
func (mq *MessageQueue) GetStats() QueueStats {
	stats := QueueStats{}

	mq.queues.Range(func(key, value interface{}) bool {
		stats.TotalQueues++
		queue := value.(*ConversationQueue)

		queue.mu.Lock()
		if queue.processing {
			stats.ProcessingQueues++
		}
		stats.TotalMessages += len(queue.messages)
		queue.mu.Unlock()

		return true
	})

	return stats
}

// Cleanup limpia colas inactivas (llamar periódicamente)
func (mq *MessageQueue) Cleanup(maxIdleTime time.Duration) {
	toDelete := []uuid.UUID{}

	mq.queues.Range(func(key, value interface{}) bool {
		convID := key.(uuid.UUID)
		queue := value.(*ConversationQueue)

		queue.mu.Lock()
		isEmpty := len(queue.messages) == 0
		isProcessing := queue.processing

		// Si está vacía y no está procesando, verificar última actividad
		if isEmpty && !isProcessing && len(queue.messages) == 0 {
			// Marcar para eliminación si no hay actividad reciente
			toDelete = append(toDelete, convID)
		}
		queue.mu.Unlock()

		return true
	})

	// Eliminar colas inactivas
	for _, convID := range toDelete {
		mq.queues.Delete(convID)
		log.Printf("🗑️ Cleaned up inactive queue for %s", convID)
	}

	if len(toDelete) > 0 {
		log.Printf("🧹 Cleanup: removed %d inactive queues", len(toDelete))
	}
}
