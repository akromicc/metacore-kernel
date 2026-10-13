package automation

import (
	"errors"
	"fmt"
)

// Domain errors. Handlers map them to HTTP status codes; agent tools turn them
// into short Spanish messages for the person.
var (
	ErrInvalid      = errors.New("automatización inválida")
	ErrNotFound     = errors.New("automatización no encontrada")
	ErrForbidden    = errors.New("no tienes permiso para esta automatización")
	ErrLimit        = errors.New("límite de automatizaciones alcanzado")
	ErrConflict     = errors.New("la automatización cambió; recarga e intenta de nuevo")
	ErrBadState     = errors.New("la automatización no admite esa acción en su estado actual")
	ErrBudget       = errors.New("presupuesto de la automatización excedido")
	ErrDisabled     = errors.New("automatizaciones desactivadas")
	errPermanentRun = errors.New("fallo permanente de la corrida")
)

// invalid wraps ErrInvalid with a detail.
func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
