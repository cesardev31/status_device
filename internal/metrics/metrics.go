package metrics

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Snapshot es la foto de los recursos del equipo en un instante.
type Snapshot struct {
	CPUPercent float64
	CPUCores   []float64
	CPUTempC   float64
	LoadAvg    [3]float64
	MemTotal   uint64 // bytes
	MemUsed    uint64
	MemFree    uint64 // disponible de verdad (MemAvailable)
	MemCache   uint64 // caché del sistema; Linux puede reutilizar buena parte
	MemShared  uint64
	SwapTotal  uint64
	SwapUsed   uint64
	// Los campos GPU* y VRAM* describen la tarjeta principal, que es la que
	// cabe en la barra del escritorio; GPUs las lleva todas.
	GPUName    string
	GPUOK      bool
	GPUPercent float64
	GPUTempC   float64
	VRAMTotal  uint64
	VRAMUsed   uint64
	VRAMOK     bool
	VRAMShared bool // la memoria es RAM del sistema, no VRAM dedicada
	GPUs       []GPU
	Processes  []ProcessUsage
	Disks      []DiskIO
	Mounts     []Mount
	Nets       []NetIO
	DiskRead   uint64 // bytes/s sumando todos los discos
	DiskWrite  uint64
	NetRX      uint64 // bytes/s sumando todas las interfaces
	NetTX      uint64
	Uptime     float64 // segundos desde el arranque
	Battery    Battery
}

// cpuTimes son los contadores acumulados de /proc/stat para una CPU.
type cpuTimes struct {
	total uint64
	idle  uint64
}

// Collector guarda el estado necesario para calcular porcentajes por diferencia.
type Collector struct {
	prevTotal cpuTimes
	prevCores []cpuTimes
	processes processCollector
	prevDisks map[string]ioCounter
	prevNets  map[string]ioCounter
	lastRead  time.Time
	gpus      []*gpuDevice
	gpuLooked bool
	// Solo se rastrean los clientes DRM si alguna tarjeta necesita que se le
	// deduzca la ocupación: recorrer los descriptores de todo el equipo no es
	// gratis y las tarjetas AMD y NVIDIA ya dan el dato hecho.
	needClients bool
	prevClients map[drmClientKey]clientEngines
}

func NewCollector() *Collector {
	c := &Collector{}
	c.prevTotal, c.prevCores = readCPUTimes()
	c.processes.seed()
	c.prevDisks = readDiskStats()
	c.prevNets = readNetCounters()
	c.lastRead = time.Now()
	return c
}

// Read devuelve una foto nueva. El % de CPU se mide contra la lectura anterior,
// así que la primera llamada tras crear el Collector da valores poco fiables.
func (c *Collector) Read() Snapshot {
	var s Snapshot

	now := time.Now()
	elapsed := now.Sub(c.lastRead).Seconds()
	c.lastRead = now

	total, cores := readCPUTimes()
	s.CPUPercent = cpuUsage(c.prevTotal, total)
	deltaTotal := counterDelta(c.prevTotal.total, total.total)
	for i := range cores {
		if i < len(c.prevCores) {
			s.CPUCores = append(s.CPUCores, cpuUsage(c.prevCores[i], cores[i]))
		}
	}
	c.prevTotal, c.prevCores = total, cores
	s.Processes = c.processes.read(deltaTotal, elapsed)

	s.CPUTempC = readCPUTemp()
	s.LoadAvg = readLoadAvg()
	readMemInfo(&s)

	disks := readDiskStats()
	s.Disks = diskRates(c.prevDisks, disks, elapsed)
	c.prevDisks = disks
	for _, d := range s.Disks {
		s.DiskRead += d.ReadRate
		s.DiskWrite += d.WriteRate
	}
	nets := readNetCounters()
	s.Nets = netRates(c.prevNets, nets, elapsed)
	c.prevNets = nets
	for _, n := range s.Nets {
		s.NetRX += n.RXRate
		s.NetTX += n.TXRate
	}
	s.Mounts = readMounts()
	s.Uptime = readUptime()
	s.Battery = readBattery()

	c.readGPUs(&s, elapsed)
	return s
}

// readGPUs mide todas las tarjetas del equipo y resume la principal en los
// campos sueltos que consumen la barra y el icono.
func (c *Collector) readGPUs(s *Snapshot, elapsed float64) {
	if !c.gpuLooked {
		c.gpus = detectGPUs()
		c.gpuLooked = true
		for _, g := range c.gpus {
			if g.busyFromClients && !g.nvidia {
				c.needClients = true
			}
		}
		if c.needClients {
			c.prevClients = readDRMClients("/proc")
		}
	}
	var busy map[string]clientEngines
	if c.needClients {
		clients := readDRMClients("/proc")
		busy = drmBusy(c.prevClients, clients)
		c.prevClients = clients
	}
	for _, g := range c.gpus {
		s.GPUs = append(s.GPUs, g.fill(busy, elapsed))
	}

	primary, ok := primaryGPU(s.GPUs)
	if !ok {
		return
	}
	s.GPUName = primary.Name
	s.GPUOK = primary.BusyOK
	s.GPUPercent = primary.BusyPercent
	s.GPUTempC = primary.TempC
	s.VRAMTotal, s.VRAMUsed = primary.MemTotal, primary.MemUsed
	s.VRAMOK, s.VRAMShared = primary.MemOK, primary.MemShared
}

// primaryGPU elige qué tarjeta representa al equipo. Con una dedicada delante,
// es la que hace el trabajo pesado y la que interesa vigilar; si solo hay
// integrada, es esa.
func primaryGPU(gpus []GPU) (GPU, bool) {
	if len(gpus) == 0 {
		return GPU{}, false
	}
	best, found := GPU{}, false
	for _, g := range gpus {
		if !g.BusyOK {
			continue
		}
		if !found || (best.Integrated && !g.Integrated) {
			best, found = g, true
		}
	}
	if found {
		return best, true
	}
	return gpus[0], true
}

func counterDelta(prev, cur uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}

func cpuUsage(prev, cur cpuTimes) float64 {
	dt := float64(cur.total - prev.total)
	di := float64(cur.idle - prev.idle)
	if dt <= 0 {
		return 0
	}
	pct := (dt - di) / dt * 100
	return Clamp(pct)
}

// Clamp acota un porcentaje al rango 0-100.
func Clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// readCPUTimes lee /proc/stat: la línea "cpu" agregada y una por núcleo.
func readCPUTimes() (cpuTimes, []cpuTimes) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuTimes{}, nil
	}
	defer f.Close()

	var total cpuTimes
	var cores []cpuTimes
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu") {
			break
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		var t cpuTimes
		for i, raw := range fields[1:] {
			v, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				continue
			}
			t.total += v
			// campos 3 (idle) y 4 (iowait) cuentan como tiempo ocioso
			if i == 3 || i == 4 {
				t.idle += v
			}
		}
		if fields[0] == "cpu" {
			total = t
		} else {
			cores = append(cores, t)
		}
	}
	if err := sc.Err(); err != nil {
		return total, cores
	}
	return total, cores
}

func readMemInfo(s *Snapshot) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()

	vals := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		vals[strings.TrimSuffix(fields[0], ":")] = v * 1024 // kB -> bytes
	}
	if err := sc.Err(); err != nil {
		return
	}

	s.MemTotal = vals["MemTotal"]
	s.MemFree = vals["MemAvailable"]
	s.MemCache = vals["Buffers"] + vals["Cached"] + vals["SReclaimable"]
	s.MemShared = vals["Shmem"]
	if s.MemFree == 0 {
		s.MemFree = vals["MemFree"]
	}
	if s.MemTotal > s.MemFree {
		s.MemUsed = s.MemTotal - s.MemFree
	}
	s.SwapTotal = vals["SwapTotal"]
	if s.SwapTotal > vals["SwapFree"] {
		s.SwapUsed = s.SwapTotal - vals["SwapFree"]
	}
}

func readLoadAvg() [3]float64 {
	var out [3]float64
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return out
	}
	fields := strings.Fields(string(b))
	for i := 0; i < 3 && i < len(fields); i++ {
		out[i], _ = strconv.ParseFloat(fields[i], 64)
	}
	return out
}

// readCPUTemp busca el sensor del paquete de CPU en hwmon (k10temp, coretemp,
// zenpower o acpitz como último recurso).
func readCPUTemp() float64 {
	prefer := map[string]int{"k10temp": 3, "coretemp": 3, "zenpower": 3, "acpitz": 1}
	best, bestScore := 0.0, 0
	dirs, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, d := range dirs {
		name := strings.TrimSpace(readString(filepath.Join(d, "name")))
		score, ok := prefer[name]
		if !ok || score <= bestScore {
			continue
		}
		v, ok := readMilliDegrees(d)
		if !ok {
			continue
		}
		best, bestScore = v, score
	}
	return best
}

// readMilliDegrees devuelve la primera temperatura útil de un directorio hwmon.
func readMilliDegrees(dir string) (float64, bool) {
	inputs, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
	for _, in := range inputs {
		v, err := strconv.ParseFloat(strings.TrimSpace(readString(in)), 64)
		if err == nil && v > 0 {
			return v / 1000, true
		}
	}
	return 0, false
}

func readString(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func readUint(path string) (uint64, bool) {
	v, err := strconv.ParseUint(strings.TrimSpace(readString(path)), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
