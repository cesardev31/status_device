// Package tray publica el indicador en la barra superior de GNOME hablando
// D-Bus directamente: un org.kde.StatusNotifierItem con su menú desplegable.
package tray

import (
	"fmt"
	"os"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"

	"github.com/cesardev31/status_device/internal/icon"
)

const (
	sniIface  = "org.kde.StatusNotifierItem"
	sniPath   = "/StatusNotifierItem"
	menuIface = "com.canonical.dbusmenu"
	menuPath  = "/MenuBar"

	watcherName = "org.kde.StatusNotifierWatcher"
	watcherPath = "/StatusNotifierWatcher"
)

// Indicator publica un StatusNotifierItem en el bus de sesión. GNOME lo muestra
// en la barra superior a través de la extensión ubuntu-appindicators, que lee
// el texto de la propiedad XAyatanaLabel.
type Indicator struct {
	conn  *dbus.Conn
	props *prop.Properties
	menu  *Menu
}

func NewIndicator(iconName, title string, menu *Menu) (*Indicator, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("no se pudo conectar al bus de sesión: %w", err)
	}

	name := fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("RequestName: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return nil, fmt.Errorf("el nombre %s ya está en uso (¿otra instancia corriendo?)", name)
	}

	ind := &Indicator{conn: conn, menu: menu}

	spec := map[string]map[string]*prop.Prop{
		sniIface: {
			"Category": ro("SystemServices"),
			"Id":       ro("status-device"),
			"Title":    ro(title),
			"Status":   ro("Active"),
			"IconName": ro(iconName),
			// El pixmap no emite PropertiesChanged: se avisa con la señal
			// NewIcon, que es la vía del protocolo y evita mandar el mapa de
			// bits entero por el bus en cada refresco.
			"IconPixmap":              {Value: []icon.Pixmap{}, Emit: prop.EmitFalse},
			"IconThemePath":           ro(""),
			"IconAccessibleDesc":      ro(""),
			"AttentionIconName":       ro(""),
			"AttentionIconPixmap":     ro([]icon.Pixmap{}),
			"AttentionAccessibleDesc": ro(""),
			"AttentionMovieName":      ro(""),
			"OverlayIconName":         ro(""),
			"OverlayIconPixmap":       ro([]icon.Pixmap{}),
			"WindowId":                ro(int32(0)),
			"ToolTip":                 ro(tooltip{IconName: iconName, Title: title}),
			"ItemIsMenu":              ro(false),
			"Menu":                    ro(dbus.ObjectPath(menuPath)),
			"XAyatanaLabel":           ro(""),
			"XAyatanaLabelGuide":      ro("CPU 100% GPU 100% RAM 100%"),
			"XAyatanaOrderingIndex":   ro(uint32(0)),
		},
	}
	ind.props, err = prop.Export(conn, sniPath, spec)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("exportando propiedades: %w", err)
	}

	if err := conn.Export(ind, sniPath, sniIface); err != nil {
		conn.Close()
		return nil, fmt.Errorf("exportando %s: %w", sniIface, err)
	}
	if err := conn.Export(introspect.Introspectable(sniIntrospectXML), sniPath,
		"org.freedesktop.DBus.Introspectable"); err != nil {
		conn.Close()
		return nil, err
	}

	if err := menu.export(conn); err != nil {
		conn.Close()
		return nil, err
	}

	// Registrarse ante el watcher de GNOME Shell. Si la extensión de
	// appindicators está desactivada esta llamada falla y no se ve nada.
	obj := conn.Object(watcherName, watcherPath)
	if call := obj.Call(watcherName+".RegisterStatusNotifierItem", 0, name); call.Err != nil {
		conn.Close()
		return nil, fmt.Errorf("no hay un host de indicadores disponible (activa la extensión "+
			"«Ubuntu AppIndicators»): %w", call.Err)
	}
	return ind, nil
}

func ro(v any) *prop.Prop {
	return &prop.Prop{Value: v, Writable: false, Emit: prop.EmitTrue}
}

// tooltip es la estructura (sa(iiay)ss) que exige la especificación SNI.
type tooltip struct {
	IconName string
	IconData []icon.Pixmap
	Title    string
	Text     string
}

// SetLabel actualiza el texto visible en la barra superior.
func (i *Indicator) SetLabel(label, tooltipText string) {
	i.props.SetMust(sniIface, "XAyatanaLabel", label)
	i.props.SetMust(sniIface, "ToolTip", tooltip{
		IconName: i.props.GetMust(sniIface, "IconName").(string),
		Title:    "status_device",
		Text:     tooltipText,
	})
	// Señal específica de Ayatana: la extensión la usa para refrescar el texto.
	_ = i.conn.Emit(sniPath, sniIface+".XAyatanaNewLabel", label, "")
}

// SetIcon reemplaza el icono por el mapa de bits recién dibujado. Solo tiene
// efecto si IconName está vacío: si hay nombre de tema, ese tiene prioridad.
func (i *Indicator) SetIcon(pixmaps []icon.Pixmap) {
	i.props.SetMust(sniIface, "IconPixmap", pixmaps)
	_ = i.conn.Emit(sniPath, sniIface+".NewIcon")
}

// Conn expone la conexión para reutilizarla (p. ej. en las notificaciones).
func (i *Indicator) Conn() *dbus.Conn { return i.conn }

// Activate se dispara al hacer clic izquierdo sobre el indicador.
func (i *Indicator) Activate(x, y int32) *dbus.Error {
	i.menu.onActivate()
	return nil
}

func (i *Indicator) SecondaryActivate(x, y int32) *dbus.Error { return nil }

func (i *Indicator) Scroll(delta int32, orientation string) *dbus.Error { return nil }

func (i *Indicator) Close() { i.conn.Close() }

const sniIntrospectXML = `
<node>
  <interface name="org.kde.StatusNotifierItem">
    <method name="Activate"><arg type="i" direction="in"/><arg type="i" direction="in"/></method>
    <method name="SecondaryActivate"><arg type="i" direction="in"/><arg type="i" direction="in"/></method>
    <method name="Scroll"><arg type="i" direction="in"/><arg type="s" direction="in"/></method>
    <signal name="NewIcon"/>
    <signal name="NewStatus"><arg type="s"/></signal>
    <signal name="XAyatanaNewLabel"><arg type="s"/><arg type="s"/></signal>
    <property name="Category" type="s" access="read"/>
    <property name="Id" type="s" access="read"/>
    <property name="Title" type="s" access="read"/>
    <property name="Status" type="s" access="read"/>
    <property name="IconName" type="s" access="read"/>
    <property name="IconPixmap" type="a(iiay)" access="read"/>
    <property name="IconThemePath" type="s" access="read"/>
    <property name="OverlayIconName" type="s" access="read"/>
    <property name="AttentionIconName" type="s" access="read"/>
    <property name="ToolTip" type="(sa(iiay)ss)" access="read"/>
    <property name="Menu" type="o" access="read"/>
    <property name="ItemIsMenu" type="b" access="read"/>
    <property name="XAyatanaLabel" type="s" access="read"/>
    <property name="XAyatanaLabelGuide" type="s" access="read"/>
  </interface>
</node>`
