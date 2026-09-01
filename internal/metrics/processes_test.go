package metrics

import "testing"

func TestParseProcessStatHandlesSpacesAndParentheses(t *testing.T) {
	stat := "42 (nombre raro) aquí) S 1 2 3 4 5 6 7 8 9 10 120 30 14 15 16 17 18 19 900 21 22"
	ticks, start, ok := parseProcessStat(stat)
	if !ok {
		t.Fatal("parseProcessStat rechazó una línea válida")
	}
	if ticks != 150 {
		t.Fatalf("ticks = %d, se esperaban 150", ticks)
	}
	if start != 900 {
		t.Fatalf("start = %d, se esperaba 900", start)
	}
}

func TestCalculateProcessUsageGroupsApplications(t *testing.T) {
	prev := map[int]processCounter{
		1: {ticks: 100, start: 10},
		2: {ticks: 50, start: 20},
		3: {ticks: 200, start: 30},
	}
	samples := []processSample{
		{pid: 1, name: "brave", ticks: 120, start: 10, rss: 300},
		{pid: 2, name: "brave", ticks: 60, start: 20, rss: 200},
		{pid: 3, name: "steam", ticks: 205, start: 30, rss: 900},
		// PID reutilizado: no debe heredar el consumo del proceso anterior.
		{pid: 4, name: "nuevo", ticks: 500, start: 40, rss: 100},
	}
	processes := calculateProcessUsage(samples, prev, 100)
	byName := map[string]ProcessUsage{}
	for _, p := range processes {
		byName[p.Name] = p
	}

	if got := byName["brave"]; got.Count != 2 || got.CPUPercent != 30 || got.RSSBytes != 500 {
		t.Fatalf("grupo de Brave inesperado: %+v", got)
	}
	if got := byName["steam"]; got.RSSBytes != 900 || got.CPUPercent != 5 {
		t.Fatalf("grupo de Steam inesperado: %+v", got)
	}
}
