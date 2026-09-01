package metrics

import "testing"

func TestParseProcessStatHandlesSpacesAndParentheses(t *testing.T) {
	stat := "42 (nombre raro) aquí) S 1 2 3 4 5 6 7 8 9 10 120 30 14 15 16 17 18 19 900 21 22"
	fields, ok := parseProcessStat(stat)
	if !ok {
		t.Fatal("parseProcessStat rechazó una línea válida")
	}
	if fields.ticks != 150 {
		t.Fatalf("ticks = %d, se esperaban 150", fields.ticks)
	}
	if fields.start != 900 {
		t.Fatalf("start = %d, se esperaba 900", fields.start)
	}
	if fields.state != "S" {
		t.Fatalf("state = %q, se esperaba \"S\"", fields.state)
	}
	if fields.nice != 17 {
		t.Fatalf("nice = %d, se esperaba 17", fields.nice)
	}
	if fields.threads != 18 {
		t.Fatalf("threads = %d, se esperaban 18", fields.threads)
	}
}

func TestCalculateProcessUsageGroupsApplications(t *testing.T) {
	prev := map[int]processCounter{
		1: {ticks: 100, start: 10},
		2: {ticks: 50, start: 20},
		3: {ticks: 200, start: 30},
	}
	samples := []processSample{
		{pid: 1, name: "brave", ticks: 120, start: 10, rss: 300, threads: 4},
		{pid: 2, name: "brave", ticks: 60, start: 20, rss: 200, threads: 2},
		{pid: 3, name: "steam", ticks: 205, start: 30, rss: 900, threads: 1},
		// PID reutilizado: no debe heredar el consumo del proceso anterior.
		{pid: 4, name: "nuevo", ticks: 500, start: 40, rss: 100, threads: 1},
	}
	processes := calculateProcessUsage(samples, prev, 100, 1)
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
	if got := byName["brave"]; got.Threads != 6 {
		t.Fatalf("hilos de Brave = %d, se esperaban 6", got.Threads)
	}
	if got := byName["nuevo"]; got.CPUPercent != 0 {
		t.Fatalf("el PID reutilizado heredó consumo: %+v", got)
	}
}

func TestCalculateProcessUsageExposesPIDsAndLeader(t *testing.T) {
	samples := []processSample{
		// El proceso más antiguo (start menor) presta los datos de identidad.
		{pid: 30, name: "code", start: 900, exe: "/usr/bin/code", command: "code --hijo", state: "S", nice: 5},
		{pid: 10, name: "code", start: 100, exe: "/usr/bin/code", command: "code --principal", state: "R", nice: 0},
		{pid: 20, name: "code", start: 500, exe: "/usr/bin/code", command: "code --otro", state: "S", nice: 9},
	}
	processes := calculateProcessUsage(samples, nil, 100, 1)
	if len(processes) != 1 {
		t.Fatalf("se esperaba un solo grupo, hay %d", len(processes))
	}
	got := processes[0]
	if len(got.PIDs) != 3 || got.PIDs[0] != 10 || got.PIDs[2] != 30 {
		t.Fatalf("PIDs = %v; se esperaban ordenados [10 20 30]", got.PIDs)
	}
	if got.Command != "code --principal" || got.State != "R" || got.Nice != 0 {
		t.Fatalf("el líder del grupo no es el proceso más antiguo: %+v", got)
	}
	if got.Exe != "/usr/bin/code" {
		t.Fatalf("exe = %q", got.Exe)
	}
}

func TestCalculateProcessUsageDiskRates(t *testing.T) {
	prev := map[int]processCounter{
		7: {ticks: 0, start: 1, read: 1000, write: 2000},
	}
	samples := []processSample{
		{pid: 7, name: "rsync", start: 1, read: 5000, write: 2000},
	}
	got := calculateProcessUsage(samples, prev, 100, 2)[0]
	if got.ReadRate != 2000 {
		t.Fatalf("read_rate = %d, se esperaban 2000 B/s", got.ReadRate)
	}
	if got.WriteRate != 0 {
		t.Fatalf("write_rate = %d, se esperaba 0", got.WriteRate)
	}
}

func TestReadSmapsRollupAndPSSAggregation(t *testing.T) {
	samples := []processSample{
		{pid: 1, name: "brave", start: 1, rss: 900, pss: 500, pssOK: true, swap: 40},
		{pid: 2, name: "brave", start: 2, rss: 800, pss: 300, pssOK: true, swap: 10},
	}
	got := calculateProcessUsage(samples, nil, 100, 1)[0]
	if !got.PSSOK || got.PSSBytes != 800 {
		t.Fatalf("PSS = %d (ok=%v); se esperaban 800", got.PSSBytes, got.PSSOK)
	}
	if got.SwapBytes != 50 {
		t.Fatalf("swap = %d, se esperaban 50", got.SwapBytes)
	}
}
