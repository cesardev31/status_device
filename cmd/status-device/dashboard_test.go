package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cesardev31/status_device/internal/metrics"
)

func TestWriteDashboardSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "snapshot.json")
	snapshot := metrics.Snapshot{
		CPUPercent: 12.5,
		MemTotal:   16 * 1024 * 1024,
		Processes: []metrics.ProcessUsage{
			{Name: "brave", Count: 3, CPUPercent: 4.2, RSSBytes: 1024, PIDs: []int{41, 42}},
		},
	}
	history := dashboardHistory{CPU: []float64{10, 20}, Memory: []float64{5}, GPU: []float64{0}}
	if err := writeDashboardSnapshot(path, snapshot, history, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got dashboardSnapshot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != snapshotVersion || got.CPUPercent != 12.5 || len(got.Processes) != 1 {
		t.Fatalf("instantánea inesperada: %+v", got)
	}
	if got.Processes[0].Name != "brave" || got.Processes[0].Count != 3 {
		t.Fatalf("proceso inesperado: %+v", got.Processes[0])
	}
	if len(got.Processes[0].PIDs) != 2 || got.Processes[0].PIDs[0] != 41 {
		t.Fatalf("la ventana necesita los PIDs para poder actuar: %+v", got.Processes[0].PIDs)
	}
	if got.Interval != 2 || len(got.History.CPU) != 2 {
		t.Fatalf("intervalo o historial no viajaron: interval=%v history=%+v", got.Interval, got.History)
	}
	if got.ClockTicks != clockTicks {
		t.Fatalf("clock_ticks = %v", got.ClockTicks)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0600 {
		t.Fatalf("permisos = %o; se esperaban 600", permissions)
	}
}

func TestDashboardHistoryMantieneVentanaDeslizante(t *testing.T) {
	var history dashboardHistory
	for i := 0; i < historyLength+15; i++ {
		history.push(metrics.Snapshot{CPUPercent: float64(i % 100)})
	}
	if len(history.CPU) != historyLength {
		t.Fatalf("historial de CPU = %d muestras, se esperaban %d", len(history.CPU), historyLength)
	}
	if last := history.CPU[len(history.CPU)-1]; last != float64((historyLength+14)%100) {
		t.Fatalf("la última muestra no es la más reciente: %v", last)
	}
}

func TestDashboardHistoryGPUAusenteNoInventaValores(t *testing.T) {
	var history dashboardHistory
	history.push(metrics.Snapshot{GPUPercent: 80, GPUOK: false})
	if history.GPU[0] != 0 {
		t.Fatalf("una GPU sin datos no debe registrar %v", history.GPU[0])
	}
}
