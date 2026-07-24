package metrics

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// gpuDevice abstrae de dónde salen las métricas de GPU: sysfs (amdgpu/i915) o
// nvidia-smi. Se resuelve una sola vez al arrancar.
type gpuDevice struct {
	name   string
	sysfs  string // .../device de la tarjeta, vacío si usamos nvidia-smi
	nvidia bool
	hwmon  string
}

func detectGPU() *gpuDevice {
	// amdgpu e i915 exponen el % de ocupación directamente en sysfs.
	paths, _ := filepath.Glob("/sys/class/drm/card*/device/gpu_busy_percent")
	if len(paths) > 0 {
		p := paths[0]
		dev := filepath.Dir(p)
		g := &gpuDevice{name: gpuNameFromSysfs(dev), sysfs: dev}
		if hw, _ := filepath.Glob(filepath.Join(dev, "hwmon", "hwmon*")); len(hw) > 0 {
			g.hwmon = hw[0]
		}
		return g
	}
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		return &gpuDevice{name: "NVIDIA", nvidia: true}
	}
	return nil
}

// gpuNameFromSysfs arma un nombre legible a partir del vendor/device PCI.
func gpuNameFromSysfs(dev string) string {
	vendor := strings.TrimSpace(readString(filepath.Join(dev, "vendor")))
	switch vendor {
	case "0x1002":
		return "AMD GPU"
	case "0x8086":
		return "Intel GPU"
	case "0x10de":
		return "NVIDIA GPU"
	}
	return "GPU"
}

func (g *gpuDevice) fill(s *Snapshot) {
	s.GPUName = g.name
	if g.nvidia {
		g.fillNvidia(s)
		return
	}

	if v, ok := readUint(filepath.Join(g.sysfs, "gpu_busy_percent")); ok {
		s.GPUPercent = Clamp(float64(v))
		s.GPUOK = true
	}
	total, okT := readUint(filepath.Join(g.sysfs, "mem_info_vram_total"))
	used, okU := readUint(filepath.Join(g.sysfs, "mem_info_vram_used"))
	if okT && okU && total > 0 {
		s.VRAMTotal, s.VRAMUsed, s.VRAMOK = total, used, true
	}
	if g.hwmon != "" {
		if v, ok := readMilliDegrees(g.hwmon); ok {
			s.GPUTempC = v
		}
	}
}

// fillNvidia consulta nvidia-smi. Es un proceso externo, así que solo se usa
// cuando no hay sysfs disponible.
func (g *gpuDevice) fillNvidia(s *Snapshot) {
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=name,utilization.gpu,memory.used,memory.total,temperature.gpu",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	f := strings.Split(line, ",")
	if len(f) < 5 {
		return
	}
	num := func(i int) float64 {
		v, _ := strconv.ParseFloat(strings.TrimSpace(f[i]), 64)
		return v
	}
	s.GPUName = strings.TrimSpace(f[0])
	s.GPUPercent = Clamp(num(1))
	s.GPUOK = true
	s.VRAMUsed = uint64(num(2)) * 1024 * 1024 // MiB -> bytes
	s.VRAMTotal = uint64(num(3)) * 1024 * 1024
	s.VRAMOK = s.VRAMTotal > 0
	s.GPUTempC = num(4)
}
