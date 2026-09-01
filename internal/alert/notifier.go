package alert

import (
	"time"

	"github.com/godbus/dbus/v5"
)

// Notifier lanza notificaciones de escritorio cuando una métrica se mantiene en
// nivel crítico. Exige que el nivel se sostenga un rato y respeta un tiempo de
// espera entre avisos para no llenar la pantalla.
type Notifier struct {
	conn     *dbus.Conn
	sustain  time.Duration // cuánto tiene que aguantar en crítico antes de avisar
	cooldown time.Duration // espera mínima entre dos avisos de la misma métrica
	states   map[string]*state
}

type state struct {
	since    time.Time // desde cuándo está en crítico (cero si no lo está)
	lastSent time.Time
	id       uint32 // id de la notificación, para reemplazarla en vez de apilar
}

func NewNotifier(conn *dbus.Conn, sustain, cooldown time.Duration) *Notifier {
	return &Notifier{
		conn:     conn,
		sustain:  sustain,
		cooldown: cooldown,
		states:   map[string]*state{},
	}
}

// Reset olvida estados críticos pendientes al desactivar las notificaciones.
func (n *Notifier) Reset() {
	n.states = map[string]*state{}
}

// Check evalúa una métrica y avisa si procede. `key` identifica la métrica
// entre llamadas; `summary` y `body` son el título y el cuerpo del aviso.
func (n *Notifier) Check(key string, level Level, summary, body string) {
	st, ok := n.states[key]
	if !ok {
		st = &state{}
		n.states[key] = st
	}

	if level != Crit {
		st.since = time.Time{}
		return
	}

	now := time.Now()
	if st.since.IsZero() {
		st.since = now
		return
	}
	if now.Sub(st.since) < n.sustain {
		return
	}
	if !st.lastSent.IsZero() && now.Sub(st.lastSent) < n.cooldown {
		return
	}
	st.lastSent = now
	st.id = n.send(st.id, summary, body)
}

func (n *Notifier) send(replaces uint32, summary, body string) uint32 {
	obj := n.conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	hints := map[string]dbus.Variant{
		"urgency":       dbus.MakeVariant(byte(1)), // normal: aparece pero no bloquea
		"category":      dbus.MakeVariant("device"),
		"transient":     dbus.MakeVariant(true),
		"desktop-entry": dbus.MakeVariant("status-device"),
	}
	call := obj.Call("org.freedesktop.Notifications.Notify", 0,
		"status_device", replaces, "utilities-system-monitor-symbolic",
		summary, body, []string{}, hints, int32(8000))
	if call.Err != nil {
		return replaces
	}
	var id uint32
	if err := call.Store(&id); err != nil {
		return replaces
	}
	return id
}
