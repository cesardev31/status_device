package metrics

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// GPU es una tarjeta gráfica del equipo. Un portátil híbrido tiene dos: la
// integrada en el procesador y la dedicada, y las dos importan porque el
// trabajo salta de una a otra según la aplicación.
type GPU struct {
	Name        string  `json:"name"`
	Driver      string  `json:"driver"`
	Bus         string  `json:"bus"` // 0000:03:00.0, para distinguir dos tarjetas iguales
	Integrated  bool    `json:"integrated"`
	BusyOK      bool    `json:"busy_ok"`
	BusyPercent float64 `json:"busy_percent"`
	TempC       float64 `json:"temp_c"`
	PowerW      float64 `json:"power_w"`
	ClockMHz    float64 `json:"clock_mhz"`
	MemOK       bool    `json:"mem_ok"`
	MemUsed     uint64  `json:"mem_used"`
	MemTotal    uint64  `json:"mem_total"`
	// MemShared distingue la memoria prestada de la RAM del sistema (lo que
	// usa una integrada) de la VRAM propia de una tarjeta dedicada.
	MemShared bool `json:"mem_shared"`
}

// gpuDevice es la fuente de datos de una tarjeta: sysfs para amdgpu, i915 y
// demás controladores del núcleo, o nvidia-smi cuando el controlador propietario
// no publica nada en sysfs. Se resuelve una sola vez al arrancar.
type gpuDevice struct {
	name       string
	driver     string
	bus        string
	sysfs      string // .../device de la tarjeta; vacío si viene de nvidia-smi
	hwmon      string
	integrated bool
	// busyFromClients marca las tarjetas cuyo controlador no publica un
	// porcentaje de ocupación (Intel, entre otros): hay que deducirlo sumando
	// el tiempo de motor que declara cada cliente en /proc/<pid>/fdinfo.
	busyFromClients bool
	nvidia          bool
	nvidiaIndex     int
}

// vramCutoff es el tamaño de VRAM por debajo del cual una tarjeta se considera
// integrada: los APU reservan un recorte fijo de 512 MiB o menos de la RAM del
// sistema, y ninguna tarjeta dedicada actual baja de 2 GiB.
const vramCutoff = 2 << 30

func detectGPUs() []*gpuDevice {
	var out []*gpuDevice
	for _, card := range drmCards() {
		if g := inspectCard(card); g != nil {
			out = append(out, g)
		}
	}
	out = append(out, detectNvidiaSMI(out)...)
	nameGPUs(out)
	return out
}

// drmCards lista los directorios /sys/class/drm/cardN, sin los conectores
// (card1-eDP-1 y compañía), que cuelgan del mismo sitio con el mismo prefijo.
func drmCards() []string {
	entries, _ := filepath.Glob("/sys/class/drm/card*")
	var out []string
	for _, entry := range entries {
		name := strings.TrimPrefix(filepath.Base(entry), "card")
		if _, err := strconv.Atoi(name); err != nil {
			continue // es un conector, no una tarjeta
		}
		if info, err := os.Stat(filepath.Join(entry, "device")); err == nil && info.IsDir() {
			out = append(out, entry)
		}
	}
	sort.Strings(out)
	return out
}

func inspectCard(card string) *gpuDevice {
	dev := filepath.Join(card, "device")
	g := &gpuDevice{
		sysfs:  dev,
		driver: filepath.Base(readLink(filepath.Join(dev, "driver"))),
		bus:    filepath.Base(readLink(dev)),
	}
	if hw, _ := filepath.Glob(filepath.Join(dev, "hwmon", "hwmon*")); len(hw) > 0 {
		g.hwmon = hw[0]
	}
	if !fileReadable(filepath.Join(dev, "gpu_busy_percent")) {
		g.busyFromClients = true
	}
	g.integrated = looksIntegrated(g)
	return g
}

// looksIntegrated decide si la tarjeta comparte el silicio con el procesador.
// No hay un dato en sysfs que lo diga, así que se combinan las señales que sí
// existen; el peor caso es una etiqueta equivocada, no un número falso.
func looksIntegrated(g *gpuDevice) bool {
	switch g.driver {
	case "i915", "xe":
		// Las Arc dedicadas son las únicas Intel con memoria propia.
		return !fileReadable(filepath.Join(g.sysfs, "lmem_total_bytes"))
	case "nvidia", "nouveau":
		return false
	}
	total, ok := readUint(filepath.Join(g.sysfs, "mem_info_vram_total"))
	if !ok {
		// Sin contadores de VRAM (controladores de móvil, virtio…) lo normal
		// es que la memoria sea la del sistema.
		return true
	}
	return total < vramCutoff
}

// detectNvidiaSMI añade las tarjetas NVIDIA que el controlador propietario no
// publica en sysfs. Se salta las que ya se hayan encontrado por ahí para no
// contar dos veces la misma tarjeta con nouveau.
func detectNvidiaSMI(found []*gpuDevice) []*gpuDevice {
	for _, g := range found {
		if g.driver == "nvidia" {
			return nil
		}
	}
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return nil
	}
	lines := nvidiaQuery("index,name,pci.bus_id")
	var out []*gpuDevice
	for _, fields := range lines {
		if len(fields) < 3 {
			continue
		}
		index, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		out = append(out, &gpuDevice{
			name:        fields[1],
			driver:      "nvidia",
			bus:         strings.ToLower(fields[2]),
			nvidia:      true,
			nvidiaIndex: index,
		})
	}
	return out
}

// nameGPUs pone nombre a las tarjetas de sysfs. Se resuelven todas de una
// pasada porque pci.ids ocupa más de un megabyte y no merece leerlo dos veces.
func nameGPUs(gpus []*gpuDevice) {
	wanted := map[pciID]string{}
	for _, g := range gpus {
		if g.nvidia || g.sysfs == "" {
			continue
		}
		wanted[gpuPCIID(g)] = ""
	}
	names := pciNames(wanted)
	for _, g := range gpus {
		if g.name != "" {
			continue
		}
		id := gpuPCIID(g)
		vendor := pciVendorShortName(id.vendor)
		if model := names[id]; model != "" {
			g.name = strings.TrimSpace(vendor + " " + model)
			continue
		}
		g.name = strings.TrimSpace(vendor + " GPU")
	}
}

type pciID struct{ vendor, device string }

func gpuPCIID(g *gpuDevice) pciID {
	return pciID{
		vendor: hexID(readString(filepath.Join(g.sysfs, "vendor"))),
		device: hexID(readString(filepath.Join(g.sysfs, "device"))),
	}
}

// hexID normaliza «0x164C\n» a «164c», que es como se escriben los códigos en
// pci.ids.
func hexID(raw string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "0x"))
}

func pciVendorShortName(vendor string) string {
	switch vendor {
	case "1002":
		return "AMD"
	case "8086":
		return "Intel"
	case "10de":
		return "NVIDIA"
	case "1af4", "1b36":
		return "VirtIO"
	}
	return ""
}

// pciIDsPaths son los sitios donde las distribuciones dejan la base de datos
// de identificadores PCI. Si no está, se usa solo el nombre del fabricante.
var pciIDsPaths = []string{
	"/usr/share/hwdata/pci.ids",
	"/usr/share/misc/pci.ids",
	"/usr/share/pci.ids",
}

// pciNames busca en pci.ids el nombre comercial de cada identificador pedido.
// El formato es un vendedor por línea y sus dispositivos indentados debajo.
func pciNames(wanted map[pciID]string) map[pciID]string {
	if len(wanted) == 0 {
		return wanted
	}
	vendors := map[string]bool{}
	for id := range wanted {
		vendors[id.vendor] = true
	}
	for _, path := range pciIDsPaths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanPCIIDs(f, vendors, wanted)
		f.Close()
		return wanted
	}
	return wanted
}

func scanPCIIDs(r io.Reader, vendors map[string]bool, wanted map[pciID]string) {
	sc := bufio.NewScanner(r)
	current := ""
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "\t\t") {
			continue // subsistema: demasiado específico para el nombre visible
		}
		if strings.HasPrefix(line, "\t") {
			if current == "" {
				continue
			}
			id, name := splitPCILine(line[1:])
			if _, ok := wanted[pciID{current, id}]; ok {
				wanted[pciID{current, id}] = name
			}
			continue
		}
		id, _ := splitPCILine(line)
		if vendors[id] {
			current = id
		} else {
			current = ""
		}
	}
}

// splitPCILine parte «164c  Lucienne» en el código y el nombre.
func splitPCILine(line string) (string, string) {
	code, name, found := strings.Cut(line, "  ")
	if !found {
		return strings.TrimSpace(line), ""
	}
	return strings.TrimSpace(code), strings.TrimSpace(name)
}

// fill traduce el estado actual de la tarjeta a la foto que ve la interfaz.
func (g *gpuDevice) fill(clients map[string]clientEngines, elapsed float64) GPU {
	out := GPU{
		Name:       g.name,
		Driver:     g.driver,
		Bus:        g.bus,
		Integrated: g.integrated,
	}
	if g.nvidia {
		g.fillNvidia(&out)
		return out
	}

	if v, ok := readUint(filepath.Join(g.sysfs, "gpu_busy_percent")); ok {
		out.BusyPercent, out.BusyOK = Clamp(float64(v)), true
	} else if busy, ok := clients[g.bus].busiest(elapsed); ok {
		// Intel y compañía no publican un porcentaje: se deduce del tiempo que
		// los clientes declaran haber tenido ocupado el motor más cargado.
		out.BusyPercent, out.BusyOK = busy, true
	}
	g.fillMemory(&out)
	g.fillSensors(&out)
	return out
}

// fillMemory elige qué memoria enseñar. En una integrada la VRAM es un recorte
// fijo que el controlador mantiene casi lleno siempre: enseñarlo haría creer
// que la tarjeta está al límite cuando la memoria de verdad es la compartida.
func (g *gpuDevice) fillMemory(out *GPU) {
	if !g.integrated {
		total, okT := readUint(filepath.Join(g.sysfs, "mem_info_vram_total"))
		used, okU := readUint(filepath.Join(g.sysfs, "mem_info_vram_used"))
		if okT && okU && total > 0 {
			out.MemTotal, out.MemUsed, out.MemOK = total, used, true
		}
		return
	}
	total, okT := readUint(filepath.Join(g.sysfs, "mem_info_gtt_total"))
	used, okU := readUint(filepath.Join(g.sysfs, "mem_info_gtt_used"))
	if okT && okU && total > 0 {
		out.MemTotal, out.MemUsed, out.MemOK, out.MemShared = total, used, true, true
	}
}

// fillSensors lee el hwmon de la propia tarjeta: temperatura, consumo y
// frecuencia del núcleo gráfico.
func (g *gpuDevice) fillSensors(out *GPU) {
	if g.hwmon == "" {
		return
	}
	if v, ok := readLabelledInput(g.hwmon, "temp", "edge"); ok {
		out.TempC = v / 1000
	}
	// power1_input viene en microvatios; algunas tarjetas solo dan la media.
	if v, ok := readUint(filepath.Join(g.hwmon, "power1_input")); ok {
		out.PowerW = float64(v) / 1e6
	} else if v, ok := readUint(filepath.Join(g.hwmon, "power1_average")); ok {
		out.PowerW = float64(v) / 1e6
	}
	if v, ok := readLabelledInput(g.hwmon, "freq", "sclk"); ok {
		out.ClockMHz = v / 1e6
	}
}

// readLabelledInput busca en un hwmon la entrada de la familia pedida cuya
// etiqueta sea la preferida (el sensor del borde del chip, el reloj del núcleo)
// y, si no la encuentra, se queda con la primera que dé un valor.
func readLabelledInput(dir, family, preferred string) (float64, bool) {
	inputs, _ := filepath.Glob(filepath.Join(dir, family+"*_input"))
	sort.Strings(inputs)
	fallback, hasFallback := 0.0, false
	for _, input := range inputs {
		v, err := strconv.ParseFloat(strings.TrimSpace(readString(input)), 64)
		if err != nil || v <= 0 {
			continue
		}
		label := strings.TrimSpace(readString(strings.TrimSuffix(input, "_input") + "_label"))
		if strings.EqualFold(label, preferred) {
			return v, true
		}
		if !hasFallback {
			fallback, hasFallback = v, true
		}
	}
	return fallback, hasFallback
}

// fillNvidia consulta nvidia-smi. Es un proceso externo, así que solo se usa
// para las tarjetas que no publican nada en sysfs.
func (g *gpuDevice) fillNvidia(out *GPU) {
	rows := nvidiaQuery("index,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,clocks.sm")
	for _, f := range rows {
		if len(f) < 8 {
			continue
		}
		if index, err := strconv.Atoi(f[0]); err != nil || index != g.nvidiaIndex {
			continue
		}
		num := func(i int) float64 {
			v, _ := strconv.ParseFloat(strings.TrimSpace(f[i]), 64)
			return v
		}
		out.Name = f[1]
		out.BusyPercent, out.BusyOK = Clamp(num(2)), true
		out.MemUsed = uint64(num(3)) * 1024 * 1024 // MiB -> bytes
		out.MemTotal = uint64(num(4)) * 1024 * 1024
		out.MemOK = out.MemTotal > 0
		out.TempC = num(5)
		out.PowerW = num(6)
		out.ClockMHz = num(7)
		return
	}
}

// nvidiaQuery lanza nvidia-smi y devuelve cada tarjeta como una lista de campos
// ya recortados.
func nvidiaQuery(fields string) [][]string {
	out, err := exec.Command("nvidia-smi",
		"--query-gpu="+fields, "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	var rows [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		rows = append(rows, parts)
	}
	return rows
}

func readLink(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return target
}

func fileReadable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	f.Close()
	return true
}
