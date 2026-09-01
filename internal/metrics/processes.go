package metrics

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ProcessUsage agrupa los procesos que pertenecen al mismo ejecutable. Así,
// por ejemplo, Brave aparece como una aplicación en vez de como veinte filas.
type ProcessUsage struct {
	Name       string  `json:"name"`
	Count      int     `json:"count"`
	CPUPercent float64 `json:"cpu_percent"` // porcentaje del equipo completo, no de un solo núcleo
	RSSBytes   uint64  `json:"rss_bytes"`   // aproximado: RSS puede contar páginas compartidas más de una vez
}

type processCounter struct {
	ticks uint64
	start uint64
}

type processSample struct {
	pid   int
	name  string
	ticks uint64
	start uint64
	rss   uint64
}

type processCollector struct {
	prev map[int]processCounter
}

func (c *processCollector) seed() {
	samples := readProcessSamples("/proc")
	c.prev = counters(samples)
}

func (c *processCollector) read(deltaTotal uint64) []ProcessUsage {
	samples := readProcessSamples("/proc")
	processes := calculateProcessUsage(samples, c.prev, deltaTotal)
	c.prev = counters(samples)
	return processes
}

func counters(samples []processSample) map[int]processCounter {
	out := make(map[int]processCounter, len(samples))
	for _, p := range samples {
		out[p.pid] = processCounter{ticks: p.ticks, start: p.start}
	}
	return out
}

func calculateProcessUsage(samples []processSample, prev map[int]processCounter, deltaTotal uint64) []ProcessUsage {
	grouped := make(map[string]*ProcessUsage)
	for _, p := range samples {
		if p.name == "" {
			continue
		}
		g := grouped[p.name]
		if g == nil {
			g = &ProcessUsage{Name: p.name}
			grouped[p.name] = g
		}
		g.Count++
		g.RSSBytes += p.rss
		old, ok := prev[p.pid]
		if ok && old.start == p.start && p.ticks >= old.ticks && deltaTotal > 0 {
			g.CPUPercent += float64(p.ticks-old.ticks) / float64(deltaTotal) * 100
		}
	}

	all := make([]ProcessUsage, 0, len(grouped))
	for _, p := range grouped {
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
		ticks, start, ok := parseProcessStat(string(stat))
		if !ok {
			continue
		}
		name := processName(dir)
		if name == "" {
			continue
		}
		var rss uint64
		if b, err := os.ReadFile(filepath.Join(dir, "statm")); err == nil {
			fields := strings.Fields(string(b))
			if len(fields) > 1 {
				pages, _ := strconv.ParseUint(fields[1], 10, 64)
				rss = pages * pageSize
			}
		}
		out = append(out, processSample{pid: pid, name: name, ticks: ticks, start: start, rss: rss})
	}
	return out
}

func parseProcessStat(stat string) (ticks, start uint64, ok bool) {
	// comm está entre paréntesis y puede contener espacios o ')', por eso no se
	// puede partir toda la línea con Fields sin más.
	end := strings.LastIndex(stat, ") ")
	if end < 0 {
		return 0, 0, false
	}
	fields := strings.Fields(stat[end+2:]) // empieza en el campo 3 (state)
	if len(fields) <= 19 {
		return 0, 0, false
	}
	utime, errU := strconv.ParseUint(fields[11], 10, 64)
	stime, errS := strconv.ParseUint(fields[12], 10, 64)
	start, errStart := strconv.ParseUint(fields[19], 10, 64)
	if errU != nil || errS != nil || errStart != nil {
		return 0, 0, false
	}
	return utime + stime, start, true
}

func processName(dir string) string {
	if exe, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
		name := filepath.Base(strings.TrimSuffix(exe, " (deleted)"))
		if name != "" && name != "." {
			return name
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "comm"))
	return strings.TrimSpace(string(b))
}
