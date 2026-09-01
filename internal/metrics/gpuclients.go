package metrics

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Los controladores que no publican un porcentaje de ocupación (i915, xe y la
// mayoría de los de móvil) sí anotan, en /proc/<pid>/fdinfo, cuántos
// nanosegundos ha tenido ocupado cada motor de la tarjeta el cliente dueño de
// ese descriptor. Sumando lo que declaran todos los clientes entre dos lecturas
// sale el porcentaje que amdgpu regala hecho.

// clientEngines es el tiempo de cada motor de una tarjeta, en nanosegundos.
type clientEngines map[string]uint64

// drmClientKey identifica a un cliente: la misma tarjeta puede tener varios y
// un mismo cliente puede aparecer en varios descriptores duplicados.
type drmClientKey struct {
	bus    string
	client string
}

// busiest devuelve la ocupación del motor más cargado. Se toma el máximo y no
// la suma porque los motores trabajan en paralelo: si el de vídeo va al 100 %
// mientras el gráfico está parado, la tarjeta está al 100 %, no al 50 %.
func (c clientEngines) busiest(elapsed float64) (float64, bool) {
	if len(c) == 0 || elapsed <= 0 {
		return 0, false
	}
	window := elapsed * 1e9 // el fdinfo cuenta en nanosegundos
	best := 0.0
	for _, ns := range c {
		if busy := float64(ns) / window * 100; busy > best {
			best = busy
		}
	}
	return Clamp(best), true
}

// readDRMClients lee los contadores acumulados de todos los clientes de todas
// las tarjetas. Solo mira los descriptores que apuntan a /dev/dri: comprobar el
// enlace es una llamada al sistema, y abrir el fdinfo de cada descriptor del
// equipo serían decenas de miles.
func readDRMClients(root string) map[drmClientKey]clientEngines {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := map[drmClientKey]clientEngines{}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue // proceso de otro usuario o ya terminado
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(target, "/dev/dri/") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, "fdinfo", fd.Name()))
			if err != nil {
				continue
			}
			key, engines := parseDRMFdinfo(string(b))
			if key.bus == "" || len(engines) == 0 {
				continue
			}
			// Un cliente duplicado en dos descriptores declara los mismos
			// nanosegundos en los dos: contarlo una vez.
			if _, seen := out[key]; !seen {
				out[key] = engines
			}
		}
	}
	return out
}

// parseDRMFdinfo saca de un fdinfo la tarjeta, el cliente y el tiempo de cada
// motor, ya repartido entre las instancias que tenga ese motor.
func parseDRMFdinfo(text string) (drmClientKey, clientEngines) {
	var key drmClientKey
	engines := clientEngines{}
	capacity := map[string]uint64{}
	for _, line := range strings.Split(text, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.HasPrefix(name, "drm-") {
			continue
		}
		value = strings.TrimSpace(value)
		switch {
		case name == "drm-pdev":
			key.bus = strings.ToLower(value)
		case name == "drm-client-id":
			key.client = value
		case strings.HasPrefix(name, "drm-engine-capacity-"):
			if v, err := strconv.ParseUint(value, 10, 64); err == nil && v > 0 {
				capacity[strings.TrimPrefix(name, "drm-engine-capacity-")] = v
			}
		case strings.HasPrefix(name, "drm-engine-"):
			// El valor viene como «14469329 ns».
			number := strings.TrimSpace(strings.TrimSuffix(value, "ns"))
			if v, err := strconv.ParseUint(number, 10, 64); err == nil {
				engines[strings.TrimPrefix(name, "drm-engine-")] = v
			}
		}
	}
	// Un motor con varias instancias suma el tiempo de todas, así que su tope
	// no es el tiempo transcurrido sino ese tiempo por instancia.
	for engine, slots := range capacity {
		if ns, ok := engines[engine]; ok && slots > 1 {
			engines[engine] = ns / slots
		}
	}
	if key.client == "" {
		return drmClientKey{}, nil
	}
	return key, engines
}

// drmBusy convierte dos lecturas en el tiempo de motor consumido por cada
// tarjeta durante el intervalo. Un cliente que aparece o desaparece no cuenta:
// sin lectura anterior no hay diferencia que medir, y restar sobre su ausencia
// daría un salto que nadie ha ejecutado.
func drmBusy(prev, cur map[drmClientKey]clientEngines) map[string]clientEngines {
	out := map[string]clientEngines{}
	for key, engines := range cur {
		before, ok := prev[key]
		if !ok {
			continue
		}
		for engine, ns := range engines {
			delta := counterDelta(before[engine], ns)
			if delta == 0 {
				continue
			}
			if out[key.bus] == nil {
				out[key.bus] = clientEngines{}
			}
			out[key.bus][engine] += delta
		}
	}
	return out
}
