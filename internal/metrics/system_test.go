package metrics

import "testing"

func TestDiskRatesUsesElapsedSeconds(t *testing.T) {
	prev := map[string]ioCounter{"nvme0n1": {read: 1024, write: 2048}}
	cur := map[string]ioCounter{"nvme0n1": {read: 3072, write: 2048}}
	rates := diskRates(prev, cur, 2)
	if len(rates) != 1 {
		t.Fatalf("se esperaba un disco, hay %d", len(rates))
	}
	if rates[0].ReadRate != 1024 {
		t.Fatalf("read_rate = %d, se esperaban 1024 B/s", rates[0].ReadRate)
	}
	if rates[0].WriteRate != 0 {
		t.Fatalf("write_rate = %d, se esperaba 0", rates[0].WriteRate)
	}
}

func TestDiskRatesIgnoresContadorQueSeReinicia(t *testing.T) {
	// Tras un reinicio del contador (o del dispositivo) no debe salir un pico.
	prev := map[string]ioCounter{"sda": {read: 9000, write: 9000}}
	cur := map[string]ioCounter{"sda": {read: 10, write: 10}}
	rates := diskRates(prev, cur, 1)
	if rates[0].ReadRate != 0 || rates[0].WriteRate != 0 {
		t.Fatalf("un contador reiniciado produjo un pico: %+v", rates[0])
	}
}

func TestNetRatesOrdenaPorTraficoYConservaTotales(t *testing.T) {
	prev := map[string]ioCounter{
		"wlp3s0": {read: 0, write: 0},
		"enp2s0": {read: 0, write: 0},
	}
	cur := map[string]ioCounter{
		"wlp3s0": {read: 100, write: 100},
		"enp2s0": {read: 5000, write: 0},
	}
	rates := netRates(prev, cur, 1)
	if rates[0].Name != "enp2s0" {
		t.Fatalf("primera interfaz = %q; se esperaba la de más tráfico", rates[0].Name)
	}
	if rates[0].RXTotal != 5000 {
		t.Fatalf("rx_total = %d, se esperaban 5000", rates[0].RXTotal)
	}
}

func TestUnescapeMountDecodificaOctal(t *testing.T) {
	if got := unescapeMount(`/media/cesar/Disco\040Externo`); got != "/media/cesar/Disco Externo" {
		t.Fatalf("ruta = %q", got)
	}
	if got := unescapeMount("/home"); got != "/home" {
		t.Fatalf("una ruta sin escapes no debe cambiar: %q", got)
	}
}

func TestIsWholeDiskDescartaVirtuales(t *testing.T) {
	for _, name := range []string{"loop0", "ram1", "zram0", "sr0"} {
		if isWholeDisk(name) {
			t.Fatalf("%s no debería contar como disco físico", name)
		}
	}
}
