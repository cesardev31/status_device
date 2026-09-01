package main

import (
	"strings"
	"testing"

	"github.com/cesardev31/status_device/internal/alert"
	"github.com/cesardev31/status_device/internal/metrics"
)

func filas(s metrics.Snapshot) string {
	var b strings.Builder
	for _, r := range Rows(s, alert.Thresholds{Warn: 75, Crit: 90}) {
		b.WriteString(r.Text + "\n")
	}
	return b.String()
}

func TestRowsLlamaCompartidaALaMemoriaDeUnaIntegrada(t *testing.T) {
	texto := filas(metrics.Snapshot{GPUs: []metrics.GPU{{
		Name: "AMD Lucienne", Integrated: true, BusyOK: true, BusyPercent: 12,
		TempC: 40, MemOK: true, MemShared: true,
		MemUsed: 1 << 30, MemTotal: 8 << 30,
	}}})
	if !strings.Contains(texto, "integrada") {
		t.Errorf("falta el tipo de tarjeta:\n%s", texto)
	}
	if strings.Contains(texto, "VRAM") {
		t.Errorf("una integrada no tiene VRAM propia:\n%s", texto)
	}
	if !strings.Contains(texto, "Memoria compartida") {
		t.Errorf("falta la memoria compartida:\n%s", texto)
	}
}

func TestRowsNumeraLasTarjetasDeUnEquipoHíbrido(t *testing.T) {
	texto := filas(metrics.Snapshot{GPUs: []metrics.GPU{
		{Name: "Intel Alder Lake-P GT2", Integrated: true},
		{Name: "NVIDIA RTX 4060", BusyOK: true, BusyPercent: 47, MemOK: true,
			MemUsed: 3 << 30, MemTotal: 8 << 30},
	}})
	for _, esperado := range []string{"GPU 1", "GPU 2", "dedicada", "VRAM"} {
		if !strings.Contains(texto, esperado) {
			t.Errorf("falta %q:\n%s", esperado, texto)
		}
	}
	// La integrada sin datos se dice, no se inventa un cero.
	if !strings.Contains(texto, "GPU 1 sin datos de ocupación") {
		t.Errorf("una tarjeta sin ocupación debe declararlo:\n%s", texto)
	}
}

func TestRowsAvisaCuandoElEquipoNoDeclaraTarjeta(t *testing.T) {
	if texto := filas(metrics.Snapshot{}); !strings.Contains(texto, "GPU sin datos disponibles") {
		t.Errorf("sin tarjetas hay que decirlo:\n%s", texto)
	}
}
