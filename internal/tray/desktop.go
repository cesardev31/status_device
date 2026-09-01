package tray

import (
	"os"
	"strings"
)

// Desktop identifica el escritorio en el que corremos. Solo se usa para dar
// mensajes de ayuda útiles: el protocolo (StatusNotifierItem + dbusmenu) es el
// mismo en GNOME y en KDE Plasma.
type Desktop int

const (
	DesktopOther Desktop = iota
	DesktopGNOME
	DesktopKDE
)

// CurrentDesktop lee XDG_CURRENT_DESKTOP (y DESKTOP_SESSION como respaldo).
func CurrentDesktop() Desktop {
	return desktopFrom(os.Getenv("XDG_CURRENT_DESKTOP"), os.Getenv("DESKTOP_SESSION"))
}

func desktopFrom(values ...string) Desktop {
	joined := strings.ToLower(strings.Join(values, ":"))
	for _, part := range strings.FieldsFunc(joined, func(r rune) bool { return r == ':' || r == ';' }) {
		switch part {
		case "kde", "plasma", "plasma5", "plasmawayland", "plasma-wayland", "plasmax11":
			return DesktopKDE
		case "gnome", "ubuntu", "gnome-classic", "gnome-flashback", "pop":
			return DesktopGNOME
		}
	}
	return DesktopOther
}

// SupportsLabel indica si el escritorio pinta el texto de XAyatanaLabel junto
// al icono. Plasma implementa StatusNotifierItem pero no esa extensión de
// Ayatana: allí las cifras se ven en el icono, en el tooltip y en el menú.
func (d Desktop) SupportsLabel() bool { return d != DesktopKDE }

// watcherHint explica qué falta cuando no aparece ningún host de indicadores.
func (d Desktop) watcherHint() string {
	switch d {
	case DesktopKDE:
		return "en KDE Plasma lo proporciona la bandeja del sistema: añade el widget " +
			"«Bandeja del sistema» al panel o reinicia plasmashell"
	case DesktopGNOME:
		return "en GNOME lo proporciona la extensión «Ubuntu AppIndicators»: actívala con " +
			"gnome-extensions enable ubuntu-appindicators@ubuntu.com"
	default:
		return "hace falta una bandeja del sistema compatible con StatusNotifierItem " +
			"(GNOME con la extensión de appindicators, KDE Plasma, XFCE con xfce4-statusnotifier-plugin…)"
	}
}
