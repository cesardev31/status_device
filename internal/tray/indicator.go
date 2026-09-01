// Package tray publica el indicador en la barra del escritorio hablando D-Bus
// directamente: un org.kde.StatusNotifierItem con su menú desplegable. El
// protocolo es el mismo en GNOME (extensión de appindicators) y en KDE Plasma
// (bandeja del sistema nativa).
package tray

import (
	"fmt"
	"os"
	"sync"
	"time"

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

	watcherName  = "org.kde.StatusNotifierWatcher"
	watcherPath  = "/StatusNotifierWatcher"
	watcherIface = "org.kde.StatusNotifierWatcher"

	// Cuánto se espera a que aparezca la bandeja del sistema. Con el servicio
	// de systemd el proceso puede arrancar antes que plasmashell o GNOME Shell.
	watcherWait = 90 * time.Second
)

// Indicator publica un StatusNotifierItem en el bus de sesión. GNOME lo muestra
// a través de la extensión ubuntu-appindicators, que lee el texto de la
// propiedad XAyatanaLabel; Plasma lo dibuja con su bandeja nativa, que ignora
// esa extensión de Ayatana y muestra icono, tooltip y menú.
type Indicator struct {
	conn    *dbus.Conn
	props   *prop.Properties
	menu    *Menu
	busName string
	title   string
	desktop Desktop

	mu          sync.Mutex
	label       string
	tooltipText string
	pixmaps     []icon.Pixmap

	done chan struct{}
	once sync.Once
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

	ind := &Indicator{
		conn:    conn,
		menu:    menu,
		busName: name,
		title:   title,
		desktop: CurrentDesktop(),
		done:    make(chan struct{}),
	}

	spec := map[string]map[string]*prop.Prop{
		sniIface: {
			"Category": ro("SystemServices"),
			"Id":       ro("status-device"),
			"Title":    ro(title),
			"Status":   ro("Active"),
			"IconName": ro(iconName),
			// El pixmap no emite PropertiesChanged: se avisa con la señal
			// NewIcon, que es la vía del protocolo (la única que escucha
			// Plasma) y evita mandar el mapa de bits entero por el bus en cada
			// refresco.
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

	// La bandeja puede tardar en aparecer (arranque de sesión) y también puede
	// irse y volver (plasmashell reiniciado, «Alt+F2 r» en GNOME X11), así que
	// primero se espera y luego se vigila para volver a registrarse.
	if err := waitForOwner(conn, watcherName, watcherWait); err != nil {
		conn.Close()
		return nil, fmt.Errorf("no hay una bandeja del sistema disponible; %s", ind.desktop.watcherHint())
	}
	if err := ind.register(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w; %s", err, ind.desktop.watcherHint())
	}
	go ind.watchWatcher()
	return ind, nil
}

// register anuncia el item al host de la bandeja.
func (i *Indicator) register() error {
	obj := i.conn.Object(watcherName, watcherPath)
	if call := obj.Call(watcherIface+".RegisterStatusNotifierItem", 0, i.busName); call.Err != nil {
		return fmt.Errorf("no se pudo registrar el indicador: %w", call.Err)
	}
	return nil
}

// watchWatcher vuelve a registrar el indicador cada vez que reaparece el host
// de la bandeja. Sin esto, reiniciar plasmashell o GNOME Shell deja el icono
// fuera de la barra hasta el siguiente inicio de sesión.
func (i *Indicator) watchWatcher() {
	signals := make(chan *dbus.Signal, 8)
	i.conn.Signal(signals)
	if err := i.conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, watcherName),
	); err != nil {
		return
	}

	for {
		select {
		case <-i.done:
			return
		case sig, ok := <-signals:
			if !ok {
				return
			}
			if sig.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(sig.Body) < 3 {
				continue
			}
			newOwner, _ := sig.Body[2].(string)
			if newOwner == "" { // la bandeja se fue; ya volverá
				continue
			}
			// Plasma no siempre tiene el objeto listo en el instante en que
			// aparece el nombre en el bus.
			time.Sleep(500 * time.Millisecond)
			if err := i.register(); err != nil {
				continue
			}
			i.republish()
		}
	}
}

// republish reenvía el estado actual tras un nuevo registro.
func (i *Indicator) republish() {
	i.mu.Lock()
	label, tooltipText, pixmaps := i.label, i.tooltipText, i.pixmaps
	i.mu.Unlock()
	if len(pixmaps) > 0 {
		i.SetIcon(pixmaps)
	}
	i.SetLabel(label, tooltipText)
}

// waitForOwner espera a que alguien tome `name` en el bus, hasta `timeout`.
func waitForOwner(conn *dbus.Conn, name string, timeout time.Duration) error {
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)

	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, name),
	); err != nil {
		return err
	}
	defer conn.RemoveMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, name),
	)

	// Comprobar después de suscribirse, para no perder el aviso por carrera.
	var has bool
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, name).Store(&has); err == nil && has {
		return nil
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case <-deadline.C:
			return fmt.Errorf("nadie ofrece %s", name)
		case sig, ok := <-signals:
			if !ok {
				return fmt.Errorf("se cerró la conexión al bus")
			}
			if sig.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(sig.Body) < 3 {
				continue
			}
			if owner, _ := sig.Body[2].(string); owner != "" {
				return nil
			}
		}
	}
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

// SetLabel actualiza el texto visible en la barra y el tooltip del icono.
//
// En Plasma la etiqueta no se dibuja (la bandeja nativa no implementa la
// extensión XAyatanaLabel), así que las cifras van también en el título del
// tooltip, que es lo que se ve al pasar el ratón por encima.
func (i *Indicator) SetLabel(label, tooltipText string) {
	i.mu.Lock()
	i.label, i.tooltipText = label, tooltipText
	i.mu.Unlock()

	tipTitle := i.title
	if !i.desktop.SupportsLabel() && label != "" {
		tipTitle = label
	}
	i.props.SetMust(sniIface, "XAyatanaLabel", label)
	i.props.SetMust(sniIface, "ToolTip", tooltip{
		IconName: i.props.GetMust(sniIface, "IconName").(string),
		Title:    tipTitle,
		Text:     tooltipText,
	})
	// Señal específica de Ayatana: la extensión de GNOME la usa para refrescar
	// el texto. NewToolTip es la del protocolo, y la que escucha Plasma.
	_ = i.conn.Emit(sniPath, sniIface+".XAyatanaNewLabel", label, "")
	_ = i.conn.Emit(sniPath, sniIface+".NewToolTip")
}

// SetIcon reemplaza el icono por el mapa de bits recién dibujado. Solo tiene
// efecto si IconName está vacío: si hay nombre de tema, ese tiene prioridad.
func (i *Indicator) SetIcon(pixmaps []icon.Pixmap) {
	i.mu.Lock()
	i.pixmaps = pixmaps
	i.mu.Unlock()
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

func (i *Indicator) SecondaryActivate(x, y int32) *dbus.Error {
	i.menu.onActivate()
	return nil
}

func (i *Indicator) Scroll(delta int32, orientation string) *dbus.Error { return nil }

// ContextMenu lo llama Plasma cuando el item no expone menú propio; aquí el
// menú lo dibuja el host desde la propiedad Menu, así que no hay nada que hacer.
func (i *Indicator) ContextMenu(x, y int32) *dbus.Error { return nil }

func (i *Indicator) Close() {
	i.once.Do(func() { close(i.done) })
	i.conn.Close()
}

const sniIntrospectXML = `
<node>
  <interface name="org.kde.StatusNotifierItem">
    <method name="Activate"><arg type="i" direction="in"/><arg type="i" direction="in"/></method>
    <method name="SecondaryActivate"><arg type="i" direction="in"/><arg type="i" direction="in"/></method>
    <method name="ContextMenu"><arg type="i" direction="in"/><arg type="i" direction="in"/></method>
    <method name="Scroll"><arg type="i" direction="in"/><arg type="s" direction="in"/></method>
    <signal name="NewIcon"/>
    <signal name="NewAttentionIcon"/>
    <signal name="NewOverlayIcon"/>
    <signal name="NewToolTip"/>
    <signal name="NewTitle"/>
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
