package metrics

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// DiskIO es el tráfico de un disco físico, ya convertido a bytes por segundo.
type DiskIO struct {
	Name      string `json:"name"`
	ReadRate  uint64 `json:"read_rate"`
	WriteRate uint64 `json:"write_rate"`
}

// Mount es el espacio de un sistema de archivos montado.
type Mount struct {
	Path   string `json:"path"`
	Device string `json:"device"`
	FSType string `json:"fstype"`
	Total  uint64 `json:"total"`
	Used   uint64 `json:"used"`
	Free   uint64 `json:"free"`
}

// NetIO es el tráfico de una interfaz de red.
type NetIO struct {
	Name    string `json:"name"`
	RXRate  uint64 `json:"rx_rate"`
	TXRate  uint64 `json:"tx_rate"`
	RXTotal uint64 `json:"rx_total"`
	TXTotal uint64 `json:"tx_total"`
}

// Battery es el estado de la batería, si el equipo tiene.
type Battery struct {
	Present bool    `json:"present"`
	Percent float64 `json:"percent"`
	Status  string  `json:"status"`
}

type ioCounter struct {
	read  uint64
	write uint64
}

// realFilesystems son los tipos de sistema de archivos que representan
// almacenamiento de verdad; el resto (tmpfs, proc, cgroup…) solo haría ruido.
var realFilesystems = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "btrfs": true, "xfs": true,
	"f2fs": true, "zfs": true, "jfs": true, "reiserfs": true, "vfat": true,
	"exfat": true, "ntfs": true, "ntfs3": true, "fuseblk": true,
}

// readDiskStats devuelve los contadores acumulados de cada disco físico.
// /proc/diskstats cuenta en sectores de 512 bytes.
func readDiskStats() map[string]ioCounter {
	f, err := os.Open("/proc/diskstats")
	if err != nil {
		return nil
	}
	defer f.Close()

	out := map[string]ioCounter{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 {
			continue
		}
		name := fields[2]
		if !isWholeDisk(name) {
			continue
		}
		readSectors, errR := strconv.ParseUint(fields[5], 10, 64)
		writeSectors, errW := strconv.ParseUint(fields[9], 10, 64)
		if errR != nil || errW != nil {
			continue
		}
		out[name] = ioCounter{read: readSectors * 512, write: writeSectors * 512}
	}
	return out
}

// isWholeDisk descarta particiones (no tienen entrada propia en /sys/block) y
// los dispositivos virtuales que no interesan en un gestor de tareas.
func isWholeDisk(name string) bool {
	for _, prefix := range []string{"loop", "ram", "zram", "sr", "fd"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	info, err := os.Stat(filepath.Join("/sys/block", name))
	return err == nil && info.IsDir()
}

func diskRates(prev, cur map[string]ioCounter, elapsed float64) []DiskIO {
	if elapsed <= 0 {
		elapsed = 1
	}
	out := make([]DiskIO, 0, len(cur))
	for name, now := range cur {
		var disk DiskIO
		disk.Name = name
		if before, ok := prev[name]; ok {
			disk.ReadRate = uint64(float64(counterDelta(before.read, now.read)) / elapsed)
			disk.WriteRate = uint64(float64(counterDelta(before.write, now.write)) / elapsed)
		}
		out = append(out, disk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// readMounts lista los sistemas de archivos reales con su espacio ocupado.
func readMounts() []Mount {
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return nil
	}
	defer f.Close()

	seen := map[string]bool{}
	var out []Mount
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || !realFilesystems[fields[2]] {
			continue
		}
		path := unescapeMount(fields[1])
		if seen[path] {
			continue
		}
		var stat syscall.Statfs_t
		if err := syscall.Statfs(path, &stat); err != nil || stat.Blocks == 0 {
			continue
		}
		seen[path] = true
		block := uint64(stat.Bsize)
		total := stat.Blocks * block
		free := stat.Bavail * block
		used := total - stat.Bfree*block
		out = append(out, Mount{
			Path:   path,
			Device: unescapeMount(fields[0]),
			FSType: fields[2],
			Total:  total,
			Used:   used,
			Free:   free,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// unescapeMount deshace el escapado octal que usa /proc/self/mounts para los
// espacios y otros caracteres especiales de las rutas.
func unescapeMount(path string) string {
	if !strings.Contains(path, `\`) {
		return path
	}
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		if path[i] == '\\' && i+3 < len(path) {
			if v, err := strconv.ParseUint(path[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(path[i])
	}
	return b.String()
}

// readNetCounters lee los bytes acumulados de cada interfaz de /proc/net/dev.
func readNetCounters() map[string]ioCounter {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil
	}
	defer f.Close()

	out := map[string]ioCounter{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue // las dos primeras líneas son cabeceras
		}
		name := strings.TrimSpace(line[:colon])
		if name == "lo" {
			continue
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 9 {
			continue
		}
		rx, errR := strconv.ParseUint(fields[0], 10, 64)
		tx, errT := strconv.ParseUint(fields[8], 10, 64)
		if errR != nil || errT != nil {
			continue
		}
		out[name] = ioCounter{read: rx, write: tx}
	}
	return out
}

func netRates(prev, cur map[string]ioCounter, elapsed float64) []NetIO {
	if elapsed <= 0 {
		elapsed = 1
	}
	out := make([]NetIO, 0, len(cur))
	for name, now := range cur {
		iface := NetIO{Name: name, RXTotal: now.read, TXTotal: now.write}
		if before, ok := prev[name]; ok {
			iface.RXRate = uint64(float64(counterDelta(before.read, now.read)) / elapsed)
			iface.TXRate = uint64(float64(counterDelta(before.write, now.write)) / elapsed)
		}
		out = append(out, iface)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RXRate+out[i].TXRate != out[j].RXRate+out[j].TXRate {
			return out[i].RXRate+out[i].TXRate > out[j].RXRate+out[j].TXRate
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func readUptime() float64 {
	fields := strings.Fields(readString("/proc/uptime"))
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return v
}

func readBattery() Battery {
	dirs, _ := filepath.Glob("/sys/class/power_supply/BAT*")
	for _, dir := range dirs {
		capacity, ok := readUint(filepath.Join(dir, "capacity"))
		if !ok {
			continue
		}
		return Battery{
			Present: true,
			Percent: Clamp(float64(capacity)),
			Status:  strings.TrimSpace(readString(filepath.Join(dir, "status"))),
		}
	}
	return Battery{}
}
