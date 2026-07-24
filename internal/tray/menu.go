package tray

import (
	"os/exec"
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
	idSysMonitor = 100
	idQuit       = 101
)

// Menu implementa com.canonical.dbusmenu: el desplegable que aparece al pulsar
// el indicador. El contenido se genera al vuelo con la última medición.
type Menu struct {
	mu       sync.Mutex
	revision uint32
	rows     []Row

	OnQuit func()
}

// Row es una fila del desplegable. Texto vacío = separador. Las filas Dim se
// pintan atenuadas (detalle), las demás a contraste pleno (cabecera).
type Row struct {
	Text string
	Dim  bool
}

func NewMenu() *Menu {
	return &Menu{revision: 1, rows: []Row{{Text: "Recogiendo datos…", Dim: true}}}
}

// SetRows reemplaza las filas informativas del desplegable.
func (m *Menu) SetRows(rows []Row) {
	m.mu.Lock()
	m.rows = rows
	m.revision++
	m.mu.Unlock()
}

func (m *Menu) export(conn *dbus.Conn) error {
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
	out = append(out,
		menuLayout{ID: 99, Props: map[string]dbus.Variant{"type": dbus.MakeVariant("separator")}},
		menuLayout{ID: idSysMonitor, Props: map[string]dbus.Variant{
			"label": dbus.MakeVariant("Abrir monitor del sistema"),
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
	if eventID == "clicked" {
		m.activate(id)
	}
	return nil
}

func (m *Menu) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	for _, e := range events {
		if e.EventID == "clicked" {
			m.activate(e.ID)
		}
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

func (m *Menu) activate(id int32) {
	switch id {
	case idSysMonitor:
		go openSystemMonitor()
	case idQuit:
		if m.OnQuit != nil {
			m.OnQuit()
		}
	}
}

// onActivate se llama al hacer clic izquierdo en el icono.
func (m *Menu) onActivate() {
	go openSystemMonitor()
}

func openSystemMonitor() {
	for _, cmd := range [][]string{
		{"gnome-system-monitor"},
		{"missioncenter"},
		{"gnome-terminal", "--", "top"},
	} {
		if _, err := exec.LookPath(cmd[0]); err != nil {
			continue
		}
		_ = exec.Command(cmd[0], cmd[1:]...).Start()
		return
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
