package tray

import (
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

// menuLayout es el tipo (ia{sv}av) que devuelve GetLayout.
type menuLayout struct {
	ID       int32
	Props    map[string]dbus.Variant
	Children []dbus.Variant
}

type menuProps struct {
	ID    int32
	Props map[string]dbus.Variant
}

type menuEvent struct {
	ID        int32
	EventID   string
	Data      dbus.Variant
	Timestamp uint32
}

const (
	idOpenDashboard       = 100
	idToggleNotifications = 101
	idQuit                = 102
)

// Menu implementa com.canonical.dbusmenu: el desplegable que aparece al pulsar
// el indicador. El contenido se genera al vuelo con la última medición.
type Menu struct {
	mu                   sync.Mutex
	conn                 *dbus.Conn
	revision             uint32
	rows                 []Row
	notificationsEnabled bool
	open                 bool // el desplegable está abierto ahora mismo

	OnQuit                func()
	OnOpen                func()
	OnToggleNotifications func(bool)
}

// Row es una fila del desplegable. Texto vacío = separador. Las filas Dim se
// pintan atenuadas (detalle), las demás a contraste pleno (cabecera).
type Row struct {
	Text string
	Dim  bool
}

func NewMenu() *Menu {
	return &Menu{revision: 1}
}

// SetRows reemplaza las filas informativas del desplegable.
func (m *Menu) SetRows(rows []Row) {
	m.mu.Lock()
	m.rows = rows
	m.revision++
	m.mu.Unlock()
	m.announceLayout()
}

// SetNotificationsEnabled actualiza el estado y el texto del interruptor del
// menú. La preferencia dura hasta que termine el proceso; el flag decide el
// estado inicial en cada arranque.
func (m *Menu) SetNotificationsEnabled(enabled bool) {
	m.mu.Lock()
	m.notificationsEnabled = enabled
	m.revision++
	m.mu.Unlock()
	m.announceLayout()
}

func (m *Menu) export(conn *dbus.Conn) error {
	m.mu.Lock()
	m.conn = conn
	m.mu.Unlock()

	spec := map[string]map[string]*prop.Prop{
		menuIface: {
			"Version":       ro(uint32(3)),
			"TextDirection": ro("ltr"),
			"Status":        ro("normal"),
			"IconThemePath": ro([]string{}),
		},
	}
	if _, err := prop.Export(conn, menuPath, spec); err != nil {
		return err
	}
	if err := conn.Export(m, menuPath, menuIface); err != nil {
		return err
	}
	return conn.Export(introspect.Introspectable(menuIntrospectXML), menuPath,
		"org.freedesktop.DBus.Introspectable")
}

// items construye la lista completa de filas del menú.
func (m *Menu) items() []menuLayout {
	m.mu.Lock()
	rows := append([]Row(nil), m.rows...)
	notificationsEnabled := m.notificationsEnabled
	m.mu.Unlock()

	var out []menuLayout
	var id int32 = 1
	for _, r := range rows {
		switch {
		case r.Text == "":
			out = append(out, menuLayout{ID: id, Props: map[string]dbus.Variant{
				"type": dbus.MakeVariant("separator"),
			}})
		case r.Dim:
			// Deshabilitada: el shell la pinta atenuada, que es justo lo que
			// queremos para las líneas de detalle.
			out = append(out, menuLayout{ID: id, Props: map[string]dbus.Variant{
				"label":   dbus.MakeVariant(r.Text),
				"enabled": dbus.MakeVariant(false),
			}})
		default:
			out = append(out, menuLayout{ID: id, Props: map[string]dbus.Variant{
				"label": dbus.MakeVariant(r.Text),
			}})
		}
		id++
	}
	if len(out) > 0 {
		out = append(out, menuLayout{ID: 99, Props: map[string]dbus.Variant{
			"type": dbus.MakeVariant("separator"),
		}})
	}
	out = append(out,
		menuLayout{ID: idOpenDashboard, Props: map[string]dbus.Variant{
			"label": dbus.MakeVariant("Abrir administrador de tareas"),
		}},
		menuLayout{ID: idToggleNotifications, Props: map[string]dbus.Variant{
			"label":        dbus.MakeVariant("Notificaciones"),
			"toggle-type":  dbus.MakeVariant("checkmark"),
			"toggle-state": dbus.MakeVariant(toggleState(notificationsEnabled)),
		}},
		menuLayout{ID: idQuit, Props: map[string]dbus.Variant{
			"label": dbus.MakeVariant("Salir"),
		}},
	)
	return out
}

func (m *Menu) GetLayout(parentID int32, recursionDepth int32, propertyNames []string) (uint32, menuLayout, *dbus.Error) {
	items := m.items()
	if parentID != 0 {
		for _, it := range items {
			if it.ID == parentID {
				return m.rev(), it, nil
			}
		}
		return m.rev(), menuLayout{ID: parentID, Props: map[string]dbus.Variant{}}, nil
	}

	root := menuLayout{
		ID:    0,
		Props: map[string]dbus.Variant{"children-display": dbus.MakeVariant("submenu")},
	}
	if recursionDepth != 0 {
		for _, it := range items {
			root.Children = append(root.Children, dbus.MakeVariant(it))
		}
	}
	return m.rev(), root, nil
}

func (m *Menu) GetGroupProperties(ids []int32, propertyNames []string) ([]menuProps, *dbus.Error) {
	var out []menuProps
	for _, it := range m.items() {
		if len(ids) == 0 || containsID(ids, it.ID) {
			out = append(out, menuProps{ID: it.ID, Props: it.Props})
		}
	}
	return out, nil
}

func (m *Menu) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	for _, it := range m.items() {
		if it.ID == id {
			if v, ok := it.Props[name]; ok {
				return v, nil
			}
		}
	}
	return dbus.MakeVariant(""), nil
}

func (m *Menu) Event(id int32, eventID string, data dbus.Variant, timestamp uint32) *dbus.Error {
	m.handle(id, eventID)
	return nil
}

func (m *Menu) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	for _, e := range events {
		m.handle(e.ID, e.EventID)
	}
	return nil, nil
}

// AboutToShow devuelve true siempre para que el shell vuelva a pedir el layout
// justo antes de dibujar el menú, y así se vean datos frescos.
func (m *Menu) AboutToShow(id int32) (bool, *dbus.Error) {
	return true, nil
}

func (m *Menu) AboutToShowGroup(ids []int32) ([]int32, []int32, *dbus.Error) {
	return ids, nil, nil
}

// handle traduce un evento de dbusmenu. Plasma avisa de «opened»/«closed»;
// mientras el desplegable está abierto conviene anunciar cada cambio de datos
// con LayoutUpdated, que es como el importador de Qt se entera.
func (m *Menu) handle(id int32, eventID string) {
	switch eventID {
	case "clicked":
		m.activate(id)
	case "opened":
		m.mu.Lock()
		m.open = true
		m.mu.Unlock()
	case "closed":
		m.mu.Lock()
		m.open = false
		m.mu.Unlock()
	}
}

// announceLayout emite LayoutUpdated si el menú está desplegado en pantalla.
func (m *Menu) announceLayout() {
	m.mu.Lock()
	conn, open, rev := m.conn, m.open, m.revision
	m.mu.Unlock()
	if conn == nil || !open {
		return
	}
	_ = conn.Emit(menuPath, menuIface+".LayoutUpdated", rev, int32(0))
}

func (m *Menu) activate(id int32) {
	switch id {
	case idOpenDashboard:
		m.onActivate()
	case idToggleNotifications:
		m.mu.Lock()
		m.notificationsEnabled = !m.notificationsEnabled
		enabled := m.notificationsEnabled
		m.revision++
		m.mu.Unlock()
		if m.OnToggleNotifications != nil {
			m.OnToggleNotifications(enabled)
		}
	case idQuit:
		if m.OnQuit != nil {
			m.OnQuit()
		}
	}
}

func toggleState(enabled bool) int32 {
	if enabled {
		return 1
	}
	return 0
}

// onActivate se llama al hacer clic izquierdo en el icono.
func (m *Menu) onActivate() {
	if m.OnOpen != nil {
		m.OnOpen()
	}
}

func (m *Menu) rev() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.revision
}

func containsID(ids []int32, id int32) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

const menuIntrospectXML = `
<node>
  <interface name="com.canonical.dbusmenu">
    <method name="GetLayout">
      <arg type="i" direction="in"/><arg type="i" direction="in"/><arg type="as" direction="in"/>
      <arg type="u" direction="out"/><arg type="(ia{sv}av)" direction="out"/>
    </method>
    <method name="GetGroupProperties">
      <arg type="ai" direction="in"/><arg type="as" direction="in"/>
      <arg type="a(ia{sv})" direction="out"/>
    </method>
    <method name="GetProperty">
      <arg type="i" direction="in"/><arg type="s" direction="in"/><arg type="v" direction="out"/>
    </method>
    <method name="Event">
      <arg type="i" direction="in"/><arg type="s" direction="in"/>
      <arg type="v" direction="in"/><arg type="u" direction="in"/>
    </method>
    <method name="EventGroup">
      <arg type="a(isvu)" direction="in"/><arg type="ai" direction="out"/>
    </method>
    <method name="AboutToShow">
      <arg type="i" direction="in"/><arg type="b" direction="out"/>
    </method>
    <method name="AboutToShowGroup">
      <arg type="ai" direction="in"/><arg type="ai" direction="out"/><arg type="ai" direction="out"/>
    </method>
    <signal name="ItemsPropertiesUpdated"><arg type="a(ia{sv})"/><arg type="a(ias)"/></signal>
    <signal name="LayoutUpdated"><arg type="u"/><arg type="i"/></signal>
    <property name="Version" type="u" access="read"/>
    <property name="TextDirection" type="s" access="read"/>
    <property name="Status" type="s" access="read"/>
    <property name="IconThemePath" type="as" access="read"/>
  </interface>
</node>`
