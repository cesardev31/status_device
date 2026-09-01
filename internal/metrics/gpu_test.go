package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCard arma un directorio con la forma de /sys/class/drm/cardN/device.
func fakeCard(t *testing.T, driver string, files map[string]string) *gpuDevice {
	t.Helper()
	dev := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dev, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return &gpuDevice{sysfs: dev, driver: driver}
}

func TestLooksIntegratedDistingueAPUDeTarjetaDedicada(t *testing.T) {
	apu := fakeCard(t, "amdgpu", map[string]string{"mem_info_vram_total": "536870912"})
	if !looksIntegrated(apu) {
		t.Error("un recorte de 512 MiB de VRAM es un APU, no una tarjeta dedicada")
	}
	dedicada := fakeCard(t, "amdgpu", map[string]string{"mem_info_vram_total": "8589934592"})
	if looksIntegrated(dedicada) {
		t.Error("8 GiB de VRAM propia es una tarjeta dedicada")
	}
	intel := fakeCard(t, "i915", nil)
	if !looksIntegrated(intel) {
		t.Error("una Intel sin memoria propia es integrada")
	}
	arc := fakeCard(t, "i915", map[string]string{"lmem_total_bytes": "8589934592"})
	if looksIntegrated(arc) {
		t.Error("una Intel con lmem_total_bytes es una Arc dedicada")
	}
	nvidia := fakeCard(t, "nvidia", nil)
	if looksIntegrated(nvidia) {
		t.Error("NVIDIA siempre es dedicada")
	}
}

func TestFillMemoryUsaLaCompartidaEnUnaIntegrada(t *testing.T) {
	g := fakeCard(t, "amdgpu", map[string]string{
		"mem_info_vram_total": "536870912",
		"mem_info_vram_used":  "507039744", // el recorte va casi siempre lleno
		"mem_info_gtt_total":  "7755898880",
		"mem_info_gtt_used":   "461942784",
	})
	g.integrated = true
	var out GPU
	g.fillMemory(&out)
	if !out.MemOK || !out.MemShared {
		t.Fatalf("una integrada debe declarar memoria compartida: %+v", out)
	}
	if out.MemTotal != 7755898880 || out.MemUsed != 461942784 {
		t.Errorf("se esperaba la memoria GTT, no el recorte de VRAM: %+v", out)
	}

	g.integrated = false
	out = GPU{}
	g.fillMemory(&out)
	if out.MemShared || out.MemTotal != 536870912 {
		t.Errorf("una dedicada enseña su VRAM: %+v", out)
	}
}

func TestScanPCIIDsEncuentraElNombreComercial(t *testing.T) {
	const catalogo = `# comentario
1002  Advanced Micro Devices, Inc. [AMD/ATI]
	164c  Lucienne
		103c 887a  Modelo de un portátil concreto
	73ff  Navi 23
8086  Intel Corporation
	46a6  Alder Lake-P GT2
`
	wanted := map[pciID]string{{"1002", "164c"}: "", {"8086", "46a6"}: ""}
	scanPCIIDs(strings.NewReader(catalogo),
		map[string]bool{"1002": true, "8086": true}, wanted)
	if got := wanted[pciID{"1002", "164c"}]; got != "Lucienne" {
		t.Errorf("nombre AMD = %q", got)
	}
	if got := wanted[pciID{"8086", "46a6"}]; got != "Alder Lake-P GT2" {
		t.Errorf("nombre Intel = %q", got)
	}
}

func TestParseDRMFdinfoLeeMotoresYCapacidad(t *testing.T) {
	const fdinfo = `pos:	0
drm-driver:	i915
drm-client-id:	42
drm-pdev:	0000:00:02.0
drm-engine-render:	1500000 ns
drm-engine-video:	500000 ns
drm-engine-capacity-video:	2
`
	key, engines := parseDRMFdinfo(fdinfo)
	if key.bus != "0000:00:02.0" || key.client != "42" {
		t.Fatalf("cliente mal identificado: %+v", key)
	}
	if engines["render"] != 1500000 {
		t.Errorf("motor gráfico = %d", engines["render"])
	}
	// Dos instancias de vídeo suman el doble de tiempo del que ha pasado.
	if engines["video"] != 250000 {
		t.Errorf("el motor de vídeo debe repartirse entre sus instancias: %d", engines["video"])
	}
}

func TestParseDRMFdinfoIgnoraLosDescriptoresQueNoSonDeGPU(t *testing.T) {
	if key, engines := parseDRMFdinfo("pos:\t0\nflags:\t0100002\n"); key.bus != "" || engines != nil {
		t.Errorf("un fdinfo normal no describe ninguna tarjeta: %+v %v", key, engines)
	}
}

func TestReadDRMClientsSoloMiraLosDescriptoresDeVídeo(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "1234")
	if err := os.MkdirAll(filepath.Join(proc, "fd"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proc, "fdinfo"), 0700); err != nil {
		t.Fatal(err)
	}
	// El 3 apunta a la tarjeta; el 4 es un archivo cualquiera y no debe abrirse.
	if err := os.Symlink("/dev/dri/renderD128", filepath.Join(proc, "fd", "3")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(proc, "fd", "4")); err != nil {
		t.Fatal(err)
	}
	fdinfo := "drm-driver:\tamdgpu\ndrm-client-id:\t7\ndrm-pdev:\t0000:03:00.0\n" +
		"drm-engine-gfx:\t900000 ns\n"
	for _, name := range []string{"3", "4"} {
		if err := os.WriteFile(filepath.Join(proc, "fdinfo", name), []byte(fdinfo), 0600); err != nil {
			t.Fatal(err)
		}
	}
	clients := readDRMClients(root)
	if len(clients) != 1 {
		t.Fatalf("se esperaba un solo cliente, hay %d: %+v", len(clients), clients)
	}
	engines := clients[drmClientKey{"0000:03:00.0", "7"}]
	if engines["gfx"] != 900000 {
		t.Errorf("tiempo de motor = %d", engines["gfx"])
	}
}

func TestDrmBusyMideLaDiferenciaYSaltaLosClientesNuevos(t *testing.T) {
	viejo := drmClientKey{"0000:03:00.0", "1"}
	nuevo := drmClientKey{"0000:03:00.0", "2"}
	prev := map[drmClientKey]clientEngines{viejo: {"gfx": 1_000_000}}
	cur := map[drmClientKey]clientEngines{
		viejo: {"gfx": 1_500_000},
		nuevo: {"gfx": 9_000_000}, // recién abierto: no hay diferencia que medir
	}
	busy := drmBusy(prev, cur)
	if got := busy["0000:03:00.0"]["gfx"]; got != 500_000 {
		t.Errorf("diferencia = %d, se esperaba 500000", got)
	}
}

func TestBusiestSeQuedaConElMotorMásCargado(t *testing.T) {
	engines := clientEngines{"gfx": 250_000_000, "video": 750_000_000}
	busy, ok := engines.busiest(1) // un segundo de ventana
	if !ok || busy != 75 {
		t.Errorf("ocupación = %.1f (ok=%v), se esperaba 75", busy, ok)
	}
	if _, ok := (clientEngines{}).busiest(1); ok {
		t.Error("sin motores no hay ocupación que declarar")
	}
	// Un motor no puede estar ocupado más tiempo del que ha pasado, pero el
	// reloj del núcleo y el nuestro no son el mismo: hay que acotar.
	if busy, _ := (clientEngines{"gfx": 3_000_000_000}).busiest(1); busy != 100 {
		t.Errorf("ocupación sin acotar: %.1f", busy)
	}
}

func TestPrimaryGPUPrefiereLaDedicadaConDatos(t *testing.T) {
	integrada := GPU{Name: "AMD Lucienne", Integrated: true, BusyOK: true, BusyPercent: 5}
	dedicada := GPU{Name: "NVIDIA RTX", BusyOK: true, BusyPercent: 60}
	got, ok := primaryGPU([]GPU{integrada, dedicada})
	if !ok || got.Name != "NVIDIA RTX" {
		t.Errorf("principal = %+v", got)
	}
	// Si la dedicada no publica ocupación, manda la que sí tiene datos.
	got, _ = primaryGPU([]GPU{integrada, {Name: "NVIDIA RTX"}})
	if got.Name != "AMD Lucienne" {
		t.Errorf("principal sin datos de la dedicada = %+v", got)
	}
	if _, ok := primaryGPU(nil); ok {
		t.Error("sin tarjetas no hay principal")
	}
}

func TestReadLabelledInputPrefiereLaEtiquetaPedida(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("temp1_input", "60000")
	write("temp1_label", "junction")
	write("temp2_input", "41000")
	write("temp2_label", "edge")
	if v, ok := readLabelledInput(dir, "temp", "edge"); !ok || v != 41000 {
		t.Errorf("temperatura = %v (ok=%v), se esperaba la del borde", v, ok)
	}
	// Sin etiquetas vale la primera lectura útil.
	otro := t.TempDir()
	if err := os.WriteFile(filepath.Join(otro, "temp1_input"), []byte("55000"), 0600); err != nil {
		t.Fatal(err)
	}
	if v, ok := readLabelledInput(otro, "temp", "edge"); !ok || v != 55000 {
		t.Errorf("sin etiqueta debe valer la primera: %v (ok=%v)", v, ok)
	}
}
