package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/cesardev31/status_device/internal/metrics"
)

const dashboardScriptName = "status-device-window.py"

type dashboardSnapshot struct {
	Version    int                    `json:"version"`
	UpdatedAt  string                 `json:"updated_at"`
	CPUPercent float64                `json:"cpu_percent"`
	CPUCores   []float64              `json:"cpu_cores"`
	CPUTempC   float64                `json:"cpu_temp_c"`
	LoadAvg    [3]float64             `json:"load_avg"`
	MemTotal   uint64                 `json:"mem_total"`
	MemUsed    uint64                 `json:"mem_used"`
	MemFree    uint64                 `json:"mem_free"`
	MemCache   uint64                 `json:"mem_cache"`
	MemShared  uint64                 `json:"mem_shared"`
	SwapTotal  uint64                 `json:"swap_total"`
	SwapUsed   uint64                 `json:"swap_used"`
	GPUName    string                 `json:"gpu_name"`
	GPUOK      bool                   `json:"gpu_ok"`
	GPUPercent float64                `json:"gpu_percent"`
	GPUTempC   float64                `json:"gpu_temp_c"`
	VRAMTotal  uint64                 `json:"vram_total"`
	VRAMUsed   uint64                 `json:"vram_used"`
	VRAMOK     bool                   `json:"vram_ok"`
	Processes  []metrics.ProcessUsage `json:"processes"`
}

func newDashboardSnapshot(s metrics.Snapshot) dashboardSnapshot {
	return dashboardSnapshot{
		Version:    1,
		UpdatedAt:  time.Now().Format(time.RFC3339Nano),
		CPUPercent: s.CPUPercent,
		CPUCores:   s.CPUCores,
		CPUTempC:   s.CPUTempC,
		LoadAvg:    s.LoadAvg,
		MemTotal:   s.MemTotal,
		MemUsed:    s.MemUsed,
		MemFree:    s.MemFree,
		MemCache:   s.MemCache,
		MemShared:  s.MemShared,
		SwapTotal:  s.SwapTotal,
		SwapUsed:   s.SwapUsed,
		GPUName:    s.GPUName,
		GPUOK:      s.GPUOK,
		GPUPercent: s.GPUPercent,
		GPUTempC:   s.GPUTempC,
		VRAMTotal:  s.VRAMTotal,
		VRAMUsed:   s.VRAMUsed,
		VRAMOK:     s.VRAMOK,
		Processes:  s.Processes,
	}
}

func dashboardSnapshotPath() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = filepath.Join(os.TempDir(), "status-device-"+strconv.Itoa(os.Getuid()))
	}
	return filepath.Join(base, "status-device", "snapshot.json")
}

func writeDashboardSnapshot(path string, s metrics.Snapshot) error {
	data, err := json.Marshal(newDashboardSnapshot(s))
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".snapshot-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func launchDashboard(snapshotPath string) error {
	script, err := findDashboardScript()
	if err != nil {
		return err
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return fmt.Errorf("python3 no está disponible: %w", err)
	}
	cmd := exec.Command(python, script, "--snapshot", snapshotPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("abriendo el administrador de tareas: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("separando la ventana del indicador: %w", err)
	}
	return nil
}

func findDashboardScript() (string, error) {
	if configured := os.Getenv("STATUS_DEVICE_WINDOW"); configured != "" {
		if fileExists(configured) {
			return configured, nil
		}
		return "", fmt.Errorf("STATUS_DEVICE_WINDOW apunta a un archivo inexistente: %s", configured)
	}
	var candidates []string
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "..", "lib", "status-device", dashboardScriptName))
	}
	candidates = append(candidates,
		filepath.Join("ui", dashboardScriptName),
		filepath.Join("..", "..", "ui", dashboardScriptName),
	)
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no se encontró %s; vuelve a ejecutar scripts/install.sh", dashboardScriptName)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
