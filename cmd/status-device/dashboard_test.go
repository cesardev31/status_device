package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cesardev31/status_device/internal/metrics"
)

func TestWriteDashboardSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "snapshot.json")
	snapshot := metrics.Snapshot{
		CPUPercent: 12.5,
		MemTotal:   16 * 1024 * 1024,
		Processes: []metrics.ProcessUsage{
			{Name: "brave", Count: 3, CPUPercent: 4.2, RSSBytes: 1024},
		},
	}
	if err := writeDashboardSnapshot(path, snapshot); err != nil {
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
	if got.Version != 1 || got.CPUPercent != 12.5 || len(got.Processes) != 1 {
		t.Fatalf("instantánea inesperada: %+v", got)
	}
	if got.Processes[0].Name != "brave" || got.Processes[0].Count != 3 {
		t.Fatalf("proceso inesperado: %+v", got.Processes[0])
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0600 {
		t.Fatalf("permisos = %o; se esperaban 600", permissions)
	}
}
