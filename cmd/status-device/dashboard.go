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

// snapshotVersion sube cuando cambia la forma del JSON. La ventana lo compara
// para avisar en vez de romperse contra un servicio antiguo.
const snapshotVersion = 3

// historyLength son las muestras que el servicio guarda de cada métrica. Con el
// intervalo por defecto son dos minutos de historial, y así las gráficas de la
// ventana ya aparecen pobladas al abrirla.
const historyLength = 60

// dashboardHistory es un búfer circular por métrica. Vive en el servicio para
// que el historial sobreviva a cerrar y volver a abrir la ventana.
type dashboardHistory struct {
	CPU    []float64 `json:"cpu"`
	Memory []float64 `json:"memory"`
	GPU    []float64 `json:"gpu"`
}

func (h *dashboardHistory) push(s metrics.Snapshot) {
	h.CPU = appendSample(h.CPU, s.CPUPercent)
	h.Memory = appendSample(h.Memory, ratio(s.MemUsed, s.MemTotal))
	gpu := s.GPUPercent
	if !s.GPUOK {
		gpu = 0
	}
	h.GPU = appendSample(h.GPU, gpu)
}

func appendSample(samples []float64, value float64) []float64 {
	samples = append(samples, metrics.Clamp(value))
	if len(samples) > historyLength {
		samples = samples[len(samples)-historyLength:]
	}
	return samples
}

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
	VRAMShared bool                   `json:"vram_shared"`
	GPUs       []metrics.GPU          `json:"gpus"`
	Processes  []metrics.ProcessUsage `json:"processes"`
	Disks      []metrics.DiskIO       `json:"disks"`
	Mounts     []metrics.Mount        `json:"mounts"`
	Nets       []metrics.NetIO        `json:"nets"`
	DiskRead   uint64                 `json:"disk_read"`
	DiskWrite  uint64                 `json:"disk_write"`
	NetRX      uint64                 `json:"net_rx"`
	NetTX      uint64                 `json:"net_tx"`
	Uptime     float64                `json:"uptime"`
	Battery    metrics.Battery        `json:"battery"`
	BootTime   float64                `json:"boot_time"`   // epoch en segundos
	ClockTicks float64                `json:"clock_ticks"` // ticks por segundo de /proc
	Interval   float64                `json:"interval"`    // cada cuánto refresca el servicio
	History    dashboardHistory       `json:"history"`
}

func newDashboardSnapshot(s metrics.Snapshot, history dashboardHistory, interval time.Duration) dashboardSnapshot {
	return dashboardSnapshot{
		Version:    snapshotVersion,
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
		VRAMShared: s.VRAMShared,
		GPUs:       s.GPUs,
		Processes:  s.Processes,
		Disks:      s.Disks,
		Mounts:     s.Mounts,
		Nets:       s.Nets,
		DiskRead:   s.DiskRead,
		DiskWrite:  s.DiskWrite,
		NetRX:      s.NetRX,
		NetTX:      s.NetTX,
		Uptime:     s.Uptime,
		Battery:    s.Battery,
		BootTime:   float64(time.Now().Unix()) - s.Uptime,
		ClockTicks: clockTicks,
		Interval:   interval.Seconds(),
		History:    history,
	}
}

// clockTicks es el USER_HZ del núcleo. Linux lo fija en 100 en todas las
// arquitecturas que soporta este programa, y sin cgo no hay forma de
// preguntarlo, así que se declara aquí en un sitio único.
const clockTicks = 100

func dashboardSnapshotPath() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = filepath.Join(os.TempDir(), "status-device-"+strconv.Itoa(os.Getuid()))
	}
	return filepath.Join(base, "status-device", "snapshot.json")
}

func writeDashboardSnapshot(path string, s metrics.Snapshot, history dashboardHistory, interval time.Duration) error {
	data, err := json.Marshal(newDashboardSnapshot(s, history, interval))
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
