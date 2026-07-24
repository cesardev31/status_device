// Package alert traduce porcentajes de uso a niveles de aviso y gestiona las
// notificaciones de escritorio cuando un recurso se mantiene saturado.
package alert

// Level es el nivel de aviso de una métrica.
type Level int

const (
	OK Level = iota
	Warn
	Crit
)

// Thresholds define a partir de qué porcentaje una métrica se considera alta.
type Thresholds struct {
	Warn float64
	Crit float64
}

// Level clasifica un porcentaje según los umbrales.
func (t Thresholds) Level(v float64) Level {
	switch {
	case v >= t.Crit:
		return Crit
	case v >= t.Warn:
		return Warn
	default:
		return OK
	}
}

// Dot es el punto de color con el que se representa el nivel en texto.
func (l Level) Dot() string {
	switch l {
	case Crit:
		return "🔴"
	case Warn:
		return "🟡"
	}
	return "🟢"
}
