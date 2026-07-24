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
	}
	if s.SwapTotal > 0 {
		rows = append(rows, detail(fmt.Sprintf("Swap %s · %s libres de %s",
			pct(ratio(s.SwapUsed, s.SwapTotal)), gib(s.SwapTotal-s.SwapUsed), gib(s.SwapTotal))))
	}
	rows = append(rows, tray.Row{})

	if s.GPUOK {
		rows = append(rows, header("GPU", s.GPUPercent, th))
		gpuDetail := s.GPUName
		if s.GPUTempC > 0 {
			gpuDetail += " · " + temp(s.GPUTempC)
		}
		rows = append(rows, detail(gpuDetail))
	} else {
		rows = append(rows, tray.Row{Text: "⚪  GPU sin datos disponibles"})
	}
	if s.VRAMOK {
		rows = append(rows, detail(fmt.Sprintf("VRAM %s · %s de %s · %s libres",
			pct(ratio(s.VRAMUsed, s.VRAMTotal)), gib(s.VRAMUsed), gib(s.VRAMTotal),
			gib(s.VRAMTotal-s.VRAMUsed))))
	}
	return rows
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
