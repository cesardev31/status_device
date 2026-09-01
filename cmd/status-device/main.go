// Comando status-device: muestra el uso de CPU, GPU y RAM en la barra del
// escritorio (GNOME Shell o KDE Plasma) como un indicador de estado.
//
// No usa cgo ni bibliotecas de sistema: habla D-Bus directamente y publica un
// StatusNotifierItem, que en GNOME dibuja la extensión «Ubuntu AppIndicators» y
// en Plasma la bandeja del sistema nativa.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cesardev31/status_device/internal/alert"
	"github.com/cesardev31/status_device/internal/icon"
	"github.com/cesardev31/status_device/internal/metrics"
	"github.com/cesardev31/status_device/internal/tray"
)

func main() {
	interval := flag.Duration("interval", 2*time.Second, "cada cuánto se refrescan los datos")
	format := flag.String("format", defaultFormat,
		"texto de la barra; admite {alert} {cpu} {gpu} {ram} {swap} {ram_free} {ram_used} "+
			"{vram} {cpu_temp} {gpu_temp} y los puntos de color {cpu_dot} {gpu_dot} {ram_dot}")
	iconName := flag.String("icon", "",
		"nombre de icono del tema; vacío = barras de color dibujadas por el programa")
	warn := flag.Float64("warn", 75, "porcentaje a partir del cual una métrica se pinta en ámbar")
	crit := flag.Float64("crit", 90, "porcentaje a partir del cual se pinta en rojo y se avisa")
	notify := flag.Bool("notify", false, "enviar una notificación cuando algo se mantiene en rojo")
	sustain := flag.Duration("notify-after", 15*time.Second,
		"tiempo que debe mantenerse en rojo antes de notificar")
	cooldown := flag.Duration("notify-every", 5*time.Minute,
		"espera mínima entre notificaciones de la misma métrica")
	once := flag.Bool("once", false, "imprime una medición por stdout y termina (sin indicador)")
	dumpIcon := flag.String("dump-icon", "", "depuración: guarda el icono actual como PNG y termina")
	window := flag.Bool("window", false, "abre la ventana gráfica del administrador de tareas y termina")
	flag.Parse()
	dashboardPath := dashboardSnapshotPath()
	if *window {
		if err := launchDashboard(dashboardPath); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	if *interval < 200*time.Millisecond {
		*interval = 200 * time.Millisecond
	}
	th := alert.Thresholds{Warn: *warn, Crit: *crit}
	if th.Crit < th.Warn {
		th.Crit = th.Warn
	}
	col := metrics.NewCollector()

	if *dumpIcon != "" {
		time.Sleep(300 * time.Millisecond)
		if err := icon.DumpPNG(*dumpIcon, iconBars(col.Read(), th), 8); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	if *once {
		time.Sleep(300 * time.Millisecond) // el % de CPU necesita dos lecturas
		s := col.Read()
		fmt.Println(Label(*format, s, th))
		for _, r := range Rows(s, th) {
			fmt.Println(r.Text)
		}
		return
	}

	menu := tray.NewMenu()
	menu.SetNotificationsEnabled(*notify)
	menu.OnOpen = func() {
		go func() {
			if err := launchDashboard(dashboardPath); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
			}
		}()
	}
	ind, err := tray.NewIndicator(*iconName, "status_device", menu)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	defer ind.Close()

	if !tray.CurrentDesktop().SupportsLabel() {
		// Plasma implementa StatusNotifierItem pero no la extensión de Ayatana
		// que pinta texto junto al icono; conviene decirlo una vez en el
		// registro para que nadie lo tome por un fallo.
		fmt.Fprintln(os.Stderr, "aviso: este escritorio no muestra texto junto al icono; "+
			"las cifras se ven en las barras del icono, en el tooltip y en el menú")
	}

	notifier := alert.NewNotifier(ind.Conn(), *sustain, *cooldown)
	notificationsEnabled := *notify
	notifyChanged := make(chan bool, 1)
	menu.OnToggleNotifications = func(enabled bool) {
		select {
		case notifyChanged <- enabled:
		default:
			// Si hay un clic pendiente, conservar el estado más reciente.
			select {
			case <-notifyChanged:
			default:
			}
			select {
			case notifyChanged <- enabled:
			default:
			}
		}
	}

	done := make(chan struct{})
	var closed bool
	stop := func() {
		if !closed {
			closed = true
			close(done)
		}
	}
	menu.OnQuit = stop

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	lastIcon := ""
	snapshotErrorLogged := false
	var history dashboardHistory
	refresh := func() {
		s := col.Read()
		rows := Rows(s, th)
		ind.SetLabel(Label(*format, s, th), Tooltip(rows))
		history.push(s)
		if err := writeDashboardSnapshot(dashboardPath, s, history, *interval); err != nil {
			if !snapshotErrorLogged {
				fmt.Fprintln(os.Stderr, "error guardando datos de la ventana:", err)
				snapshotErrorLogged = true
			}
		} else {
			snapshotErrorLogged = false
		}
		if *iconName == "" {
			// Redibujar solo si cambia algo visible: el mapa de bits viaja por
			// D-Bus y no tiene sentido reenviarlo si se ve igual.
			bars := iconBars(s, th)
			if sig := iconSignature(bars); sig != lastIcon {
				lastIcon = sig
				ind.SetIcon(icon.Build(bars))
			}
		}
		if notificationsEnabled {
			raiseAlerts(notifier, s, th)
		}
	}
	refresh()

	tick := time.NewTicker(*interval)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			refresh()
		case <-sig:
			return
		case <-done:
			return
		case notificationsEnabled = <-notifyChanged:
			if !notificationsEnabled {
				notifier.Reset()
			}
		}
	}
}

// iconBars traduce la medición a las tres barras del icono, en el mismo orden
// que el texto: CPU, GPU, RAM.
func iconBars(s metrics.Snapshot, th alert.Thresholds) []icon.Bar {
	ram := ratio(s.MemUsed, s.MemTotal)
	return []icon.Bar{
		{Value: s.CPUPercent, OK: true, Level: th.Level(s.CPUPercent)},
		{Value: s.GPUPercent, OK: s.GPUOK, Level: th.Level(s.GPUPercent)},
		{Value: ram, OK: true, Level: th.Level(ram)},
	}
}

// iconSignature resume el icono en una cadena; si no cambia, el dibujo sería
// idéntico píxel a píxel.
func iconSignature(bars []icon.Bar) string {
	s := ""
	for _, b := range bars {
		s += fmt.Sprintf("%v/%d/%.0f;", b.OK, b.Level, metrics.Clamp(b.Value))
	}
	return s
}

func raiseAlerts(n *alert.Notifier, s metrics.Snapshot, th alert.Thresholds) {
	ram := ratio(s.MemUsed, s.MemTotal)
	n.Check("cpu", th.Level(s.CPUPercent),
		fmt.Sprintf("CPU al %s", pct(s.CPUPercent)),
		fmt.Sprintf("Carga media %.2f%s", s.LoadAvg[0], suffixTemp(s.CPUTempC)))
	n.Check("ram", th.Level(ram),
		fmt.Sprintf("RAM al %s", pct(ram)),
		fmt.Sprintf("%s en uso, solo quedan %s libres", gib(s.MemUsed), gib(s.MemFree)))
	if s.GPUOK {
		n.Check("gpu", th.Level(s.GPUPercent),
			fmt.Sprintf("GPU al %s", pct(s.GPUPercent)),
			fmt.Sprintf("%s%s", s.GPUName, suffixTemp(s.GPUTempC)))
	}
}
