package main

import (
	"fmt"
	"strings"

	"github.com/cesardev31/status_device/internal/alert"
	"github.com/cesardev31/status_device/internal/metrics"
	"github.com/cesardev31/status_device/internal/tray"
)

const (
	defaultFormat = "{alert}CPU {cpu} · GPU {gpu} · RAM {ram}"

	// Sangría de las líneas de detalle: espacios duros (U+00A0), porque el
	// shell colapsa los espacios normales al principio de una etiqueta.
	indent = "\u00a0\u00a0\u00a0"
)

// Label sustituye los marcadores del formato por los valores medidos.
func Label(format string, s metrics.Snapshot, th alert.Thresholds) string {
	ram := ratio(s.MemUsed, s.MemTotal)

	gpu, gpuDot := "n/d", "⚪"
	if s.GPUOK {
		gpu, gpuDot = pct(s.GPUPercent), th.Level(s.GPUPercent).Dot()
	}
	vram := "n/d"
	if s.VRAMOK {
		vram = fmt.Sprintf("%s/%s", gib(s.VRAMUsed), gib(s.VRAMTotal))
	}

	// El aviso solo aparece cuando algo se sale de lo normal, para que la barra
	// esté limpia el resto del tiempo.
	worst := th.Level(s.CPUPercent)
	if l := th.Level(ram); l > worst {
		worst = l
	}
	if s.GPUOK {
		if l := th.Level(s.GPUPercent); l > worst {
			worst = l
		}
	}
	alerta := ""
	if worst != alert.OK {
		alerta = worst.Dot() + " "
	}

	r := strings.NewReplacer(
		"{alert}", alerta,
		"{cpu}", pct(s.CPUPercent),
		"{gpu}", gpu,
		"{ram}", pct(ram),
		"{swap}", pct(ratio(s.SwapUsed, s.SwapTotal)),
		"{ram_used}", gib(s.MemUsed),
		"{ram_free}", gib(s.MemFree),
		"{vram}", vram,
		"{cpu_temp}", temp(s.CPUTempC),
		"{gpu_temp}", temp(s.GPUTempC),
		"{cpu_dot}", th.Level(s.CPUPercent).Dot(),
		"{gpu_dot}", gpuDot,
		"{ram_dot}", th.Level(ram).Dot(),
	)
	return r.Replace(format)
}

// Rows arma el desplegable: una cabecera con medidor por recurso y debajo sus
// detalles atenuados.
func Rows(s metrics.Snapshot, th alert.Thresholds) []tray.Row {
	ram := ratio(s.MemUsed, s.MemTotal)

	cpuDetail := fmt.Sprintf("%d núcleos", len(s.CPUCores))
	if s.CPUTempC > 0 {
		cpuDetail += " · " + temp(s.CPUTempC)
	}
	cpuDetail += fmt.Sprintf(" · carga %.2f  %.2f  %.2f", s.LoadAvg[0], s.LoadAvg[1], s.LoadAvg[2])

	rows := []tray.Row{
		header("CPU", s.CPUPercent, th),
		detail(cpuDetail),
		{},
		header("RAM", ram, th),
		detail(fmt.Sprintf("%s en uso · %s libres de %s", gib(s.MemUsed), gib(s.MemFree), gib(s.MemTotal))),
		detail(fmt.Sprintf("Caché del sistema %s · compartida %s", gib(s.MemCache), gib(s.MemShared))),
	}
	if s.SwapTotal > 0 {
		rows = append(rows, detail(fmt.Sprintf("Swap %s · %s libres de %s",
			pct(ratio(s.SwapUsed, s.SwapTotal)), gib(s.SwapTotal-s.SwapUsed), gib(s.SwapTotal))))
	}
	rows = append(rows, tray.Row{})

	rows = append(rows, gpuRows(s, th)...)

	return rows
}

// gpuRows arma una entrada por tarjeta. En un portátil híbrido hay dos, y cada
// una se numera para saber cuál está trabajando.
func gpuRows(s metrics.Snapshot, th alert.Thresholds) []tray.Row {
	if len(s.GPUs) == 0 {
		return []tray.Row{{Text: "⚪  GPU sin datos disponibles"}}
	}
	var rows []tray.Row
	for i, g := range s.GPUs {
		name := "GPU"
		if len(s.GPUs) > 1 {
			name = fmt.Sprintf("GPU %d", i+1)
		}
		if g.BusyOK {
			rows = append(rows, header(name, g.BusyPercent, th))
		} else {
			rows = append(rows, tray.Row{Text: "⚪  " + name + " sin datos de ocupación"})
		}
		line := g.Name + " · " + gpuKind(g)
		if g.TempC > 0 {
			line += " · " + temp(g.TempC)
		}
		if g.PowerW > 0 {
			line += fmt.Sprintf(" · %.1f W", g.PowerW)
		}
		rows = append(rows, detail(line))
		if g.MemOK {
			// En una integrada la memoria sale de la RAM del equipo, así que
			// llamarla VRAM sería mentir sobre de dónde se está gastando.
			label := "VRAM"
			if g.MemShared {
				label = "Memoria compartida"
			}
			rows = append(rows, detail(fmt.Sprintf("%s %s · %s de %s · %s libres",
				label, pct(ratio(g.MemUsed, g.MemTotal)), gib(g.MemUsed), gib(g.MemTotal),
				gib(g.MemTotal-g.MemUsed))))
		}
	}
	return rows
}

// gpuKind traduce el tipo de tarjeta a la palabra que usaría cualquiera.
func gpuKind(g metrics.GPU) string {
	if g.Integrated {
		return "integrada"
	}
	return "dedicada"
}

// Tooltip aplana las filas para el globo de ayuda del indicador.
func Tooltip(rows []tray.Row) string {
	var b strings.Builder
	for _, r := range rows {
		if r.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.ReplaceAll(r.Text, indent, "  "))
	}
	return b.String()
}

func header(name string, value float64, th alert.Thresholds) tray.Row {
	return tray.Row{Text: fmt.Sprintf("%s  %-4s %-5s %s",
		th.Level(value).Dot(), name, pct(value), gauge(value))}
}

func detail(text string) tray.Row {
	return tray.Row{Text: indent + text, Dim: true}
}

// gauge dibuja un medidor de diez segmentos con caracteres de bloque.
func gauge(v float64) string {
	const segments = 10
	filled := int(metrics.Clamp(v)/100*segments + 0.5)
	return strings.Repeat("▰", filled) + strings.Repeat("▱", segments-filled)
}

func ratio(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

func pct(v float64) string { return fmt.Sprintf("%.0f%%", v) }

func gib(b uint64) string {
	const g = 1024 * 1024 * 1024
	v := float64(b) / g
	if v < 10 {
		return fmt.Sprintf("%.1f GiB", v)
	}
	return fmt.Sprintf("%.0f GiB", v)
}

func temp(c float64) string {
	if c <= 0 {
		return "n/d"
	}
	return fmt.Sprintf("%.0f °C", c)
}

func suffixTemp(c float64) string {
	if c <= 0 {
		return ""
	}
	return fmt.Sprintf(" · %s", temp(c))
}
