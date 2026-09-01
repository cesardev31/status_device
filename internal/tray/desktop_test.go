package tray

import "testing"

func TestDesktopFrom(t *testing.T) {
	cases := []struct {
		values []string
		want   Desktop
	}{
		{[]string{"KDE", "plasmawayland"}, DesktopKDE},
		{[]string{"ubuntu:GNOME", ""}, DesktopGNOME},
		{[]string{"", "plasma"}, DesktopKDE},
		{[]string{"XFCE", "xfce"}, DesktopOther},
		{[]string{"", ""}, DesktopOther},
	}
	for _, c := range cases {
		if got := desktopFrom(c.values...); got != c.want {
			t.Errorf("desktopFrom(%q) = %v; se esperaba %v", c.values, got, c.want)
		}
	}
}

func TestOnlyKDEHidesLabel(t *testing.T) {
	if DesktopKDE.SupportsLabel() {
		t.Error("Plasma no dibuja XAyatanaLabel; SupportsLabel debería ser false")
	}
	if !DesktopGNOME.SupportsLabel() || !DesktopOther.SupportsLabel() {
		t.Error("fuera de Plasma sí se intenta pintar la etiqueta")
	}
}
