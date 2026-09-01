package tray

import "testing"

func TestCompactMenuActions(t *testing.T) {
	menu := NewMenu()
	items := menu.items()
	if len(items) != 3 {
		t.Fatalf("el menú compacto tiene %d elementos; se esperaban 3", len(items))
	}
	if items[0].ID != idOpenDashboard || items[1].ID != idToggleNotifications || items[2].ID != idQuit {
		t.Fatalf("acciones inesperadas: %d, %d, %d", items[0].ID, items[1].ID, items[2].ID)
	}
}

func TestMenuCallbacks(t *testing.T) {
	menu := NewMenu()
	opened, quit, notifications := false, false, false
	menu.OnOpen = func() { opened = true }
	menu.OnQuit = func() { quit = true }
	menu.OnToggleNotifications = func(enabled bool) { notifications = enabled }

	menu.activate(idOpenDashboard)
	menu.activate(idToggleNotifications)
	menu.activate(idQuit)
	if !opened || !quit || !notifications {
		t.Fatalf("callbacks: abrir=%v salir=%v notificaciones=%v", opened, quit, notifications)
	}
}
