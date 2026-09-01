package metrics

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// pssThreshold es el tamaño a partir del cual vale la pena leer smaps_rollup.
// Esa lectura obliga al núcleo a recorrer todas las regiones de memoria del
// proceso, así que solo se hace para los que la ventana llega a mostrar.
const pssThreshold = 16 << 20

// ProcessUsage agrupa los procesos que pertenecen al mismo ejecutable. Así,
// por ejemplo, Brave aparece como una aplicación en vez de como veinte filas.
type ProcessUsage struct {
	Name       string  `json:"name"`
	Count      int     `json:"count"`
	CPUPercent float64 `json:"cpu_percent"` // porcentaje del equipo completo, no de un solo núcleo
	RSSBytes   uint64  `json:"rss_bytes"`   // aproximado: RSS puede contar páginas compartidas más de una vez
	PSSBytes   uint64  `json:"pss_bytes"`   // reparte las páginas compartidas entre quienes las usan
	PSSOK      bool    `json:"pss_ok"`
	SwapBytes  uint64  `json:"swap_bytes"`
	PIDs       []int   `json:"pids"` // ordenados; el primero es el proceso principal
	User       string  `json:"user"`
	Threads    int     `json:"threads"`
	State      string  `json:"state"`
	Nice       int     `json:"nice"`
	Exe        string  `json:"exe"`
	Command    string  `json:"command"`
	StartTicks uint64  `json:"start_ticks"` // arranque del principal, en ticks desde el arranque del sistema
	ReadRate   uint64  `json:"read_rate"`   // bytes/s leídos de disco
	WriteRate  uint64  `json:"write_rate"`  // bytes/s escritos a disco
}

type processCounter struct {
	ticks uint64
	start uint64
	read  uint64
	write uint64
}

type processSample struct {
	pid     int
	name    string
	ticks   uint64
	start   uint64
	rss     uint64
	pss     uint64
	pssOK   bool
	swap    uint64
	threads int
	nice    int
	state   string
	uid     uint32
	exe     string
	command string
	read    uint64
	write   uint64
}

// statFields son los campos de /proc/<pid>/stat que necesita el gestor.
type statFields struct {
	ticks   uint64
	start   uint64
	threads int
	nice    int
	state   string
}

type processCollector struct {
	prev map[int]processCounter
}

func (c *processCollector) seed() {
	samples := readProcessSamples("/proc")
	c.prev = counters(samples)
}

func (c *processCollector) read(deltaTotal uint64, elapsed float64) []ProcessUsage {
	samples := readProcessSamples("/proc")
	processes := calculateProcessUsage(samples, c.prev, deltaTotal, elapsed)
	c.prev = counters(samples)
	return processes
}

func counters(samples []processSample) map[int]processCounter {
	out := make(map[int]processCounter, len(samples))
	for _, p := range samples {
		out[p.pid] = processCounter{ticks: p.ticks, start: p.start, read: p.read, write: p.write}
	}
	return out
}

func calculateProcessUsage(samples []processSample, prev map[int]processCounter, deltaTotal uint64, elapsed float64) []ProcessUsage {
	grouped := make(map[string]*ProcessUsage)
	// leader guarda, por grupo, el arranque del proceso más antiguo: es el que
	// presta los datos de identidad (usuario, ruta, línea de comando).
	leader := make(map[string]uint64)
	for _, p := range samples {
		if p.name == "" {
			continue
		}
		g := grouped[p.name]
		if g == nil {
			g = &ProcessUsage{Name: p.name}
			grouped[p.name] = g
			leader[p.name] = ^uint64(0)
		}
		g.Count++
		g.RSSBytes += p.rss
		g.SwapBytes += p.swap
		g.Threads += p.threads
		g.PIDs = append(g.PIDs, p.pid)
		if p.pssOK {
			g.PSSBytes += p.pss
			g.PSSOK = true
		}
		if p.start < leader[p.name] {
			leader[p.name] = p.start
			g.User = p.uid2name()
			g.State = p.state
			g.Nice = p.nice
			g.Exe = p.exe
			g.Command = p.command
			g.StartTicks = p.start
		}
		old, ok := prev[p.pid]
		if ok && old.start == p.start {
			if p.ticks >= old.ticks && deltaTotal > 0 {
				g.CPUPercent += float64(p.ticks-old.ticks) / float64(deltaTotal) * 100
			}
			if elapsed > 0 {
				g.ReadRate += uint64(float64(counterDelta(old.read, p.read)) / elapsed)
				g.WriteRate += uint64(float64(counterDelta(old.write, p.write)) / elapsed)
			}
		}
	}

	all := make([]ProcessUsage, 0, len(grouped))
	for _, p := range grouped {
		sort.Ints(p.PIDs)
		all = append(all, *p)
	}
	sort.Slice(all, func(i, j int) bool {
		return strings.ToLower(all[i].Name) < strings.ToLower(all[j].Name)
	})
	return all
}

func readProcessSamples(root string) []processSample {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	pageSize := uint64(os.Getpagesize())
	out := make([]processSample, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		stat, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			continue // el proceso pudo terminar entre ReadDir y esta lectura
		}
		fields, ok := parseProcessStat(string(stat))
		if !ok {
			continue
		}
		exe := processExe(dir)
		name := processName(dir, exe)
		if name == "" {
			continue
		}
		sample := processSample{
			pid:     pid,
			name:    name,
			ticks:   fields.ticks,
			start:   fields.start,
			threads: fields.threads,
			nice:    fields.nice,
			state:   fields.state,
			exe:     exe,
			command: processCommand(dir),
			uid:     processUID(dir),
		}
		if b, err := os.ReadFile(filepath.Join(dir, "statm")); err == nil {
			values := strings.Fields(string(b))
			if len(values) > 1 {
				pages, _ := strconv.ParseUint(values[1], 10, 64)
				sample.rss = pages * pageSize
			}
		}
		if sample.rss >= pssThreshold {
			sample.pss, sample.swap, sample.pssOK = readSmapsRollup(dir)
		}
		sample.read, sample.write = readProcessIO(dir)
		out = append(out, sample)
	}
	return out
}

func parseProcessStat(stat string) (statFields, bool) {
	// comm está entre paréntesis y puede contener espacios o ')', por eso no se
	// puede partir toda la línea con Fields sin más.
	end := strings.LastIndex(stat, ") ")
	if end < 0 {
		return statFields{}, false
	}
	fields := strings.Fields(stat[end+2:]) // empieza en el campo 3 (state)
	if len(fields) <= 19 {
		return statFields{}, false
	}
	utime, errU := strconv.ParseUint(fields[11], 10, 64)
	stime, errS := strconv.ParseUint(fields[12], 10, 64)
	start, errStart := strconv.ParseUint(fields[19], 10, 64)
	if errU != nil || errS != nil || errStart != nil {
		return statFields{}, false
	}
	out := statFields{ticks: utime + stime, start: start, state: fields[0]}
	out.nice, _ = strconv.Atoi(fields[16])    // campo 19
	out.threads, _ = strconv.Atoi(fields[17]) // campo 20
	if out.threads < 1 {
		out.threads = 1
	}
	return out, true
}

// readSmapsRollup devuelve PSS y swap reales del proceso. Solo el dueño del
// proceso puede leerlo, así que puede fallar sin que sea un error.
func readSmapsRollup(dir string) (pss, swap uint64, ok bool) {
	b, err := os.ReadFile(filepath.Join(dir, "smaps_rollup"))
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "Pss":
			pss, ok = value*1024, true
		case "Swap":
			swap = value * 1024
		}
	}
	return pss, swap, ok
}

// readProcessIO lee los bytes que el proceso ha movido de verdad contra el
// disco (no la caché). Requiere ser el dueño del proceso.
func readProcessIO(dir string) (read, write uint64) {
	b, err := os.ReadFile(filepath.Join(dir, "io"))
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "read_bytes":
			read = value
		case "write_bytes":
			write = value
		}
	}
	return read, write
}

func processExe(dir string) string {
	exe, err := os.Readlink(filepath.Join(dir, "exe"))
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(exe, " (deleted)")
}

func processName(dir, exe string) string {
	if exe != "" {
		name := filepath.Base(exe)
		if name != "" && name != "." && name != string(filepath.Separator) {
			return name
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "comm"))
	return strings.TrimSpace(string(b))
}

// processCommand rearma la línea de comando, que en /proc viene con los
// argumentos separados por bytes nulos.
func processCommand(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil || len(b) == 0 {
		return ""
	}
	args := strings.FieldsFunc(string(b), func(r rune) bool { return r == 0 })
	return strings.Join(args, " ")
}

func processUID(dir string) uint32 {
	info, err := os.Stat(dir)
	if err != nil {
		return 0
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Uid
	}
	return 0
}

func (p processSample) uid2name() string {
	return userName(p.uid)
}

var (
	userNamesOnce sync.Once
	userNames     map[uint32]string
)

// userName traduce un UID a nombre leyendo /etc/passwd una sola vez. No se usa
// os/user porque en modo puro Go tampoco consulta NSS y esto evita el cgo.
func userName(uid uint32) string {
	userNamesOnce.Do(func() {
		userNames = map[uint32]string{}
		b, err := os.ReadFile("/etc/passwd")
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) < 3 {
				continue
			}
			id, err := strconv.ParseUint(fields[2], 10, 32)
			if err != nil {
				continue
			}
			if _, seen := userNames[uint32(id)]; !seen {
				userNames[uint32(id)] = fields[0]
			}
		}
	})
	if name, ok := userNames[uid]; ok {
		return name
	}
	return strconv.FormatUint(uint64(uid), 10)
}
