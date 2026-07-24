# status_device

Indicador para la barra superior de Ubuntu (GNOME Shell) que muestra el uso de
**CPU, GPU y RAM** en tiempo real, con alertas por color cuando algo se dispara.

Escrito en Go puro: no usa cgo ni hace falta instalar paquetes `-dev` de
GTK/AppIndicator. Habla D-Bus directamente y publica un
`org.kde.StatusNotifierItem`, que la extensión **Ubuntu AppIndicators** (activa
por defecto en Ubuntu) dibuja en la barra.

```
▁▃█  CPU 12% · GPU 5% · RAM 41%
```

Al pulsar sobre el indicador se abre el detalle:

```
🟢  CPU  6%    ▰▱▱▱▱▱▱▱▱▱
    8 núcleos · 49 °C · carga 0.43  1.09  1.13
🟢  RAM  37%   ▰▰▰▰▱▱▱▱▱▱
    5.2 GiB en uso · 9.2 GiB libres de 14 GiB
    Swap 0% · 4.0 GiB libres de 4.0 GiB
🟢  GPU  4%    ▰▱▱▱▱▱▱▱▱▱
    AMD GPU · 42 °C
    VRAM 83% · 0.4 GiB de 0.5 GiB · 0.1 GiB libres
```

## Instalación

```bash
git clone https://github.com/cesardev31/status_device.git
cd status_device
./scripts/install.sh
```

El script compila el binario en `~/.local/bin/status-device`, instala el
servicio de usuario de systemd y lo arranca. A partir de ahí el indicador
aparece solo al iniciar sesión, sin tener que levantarlo a mano.

Requisitos: Go (`sudo apt install golang-go`), GNOME Shell con la extensión de
appindicators activa y Linux (lee `/proc` y `/sys`).

### Manejo del servicio

```bash
systemctl --user status status-device      # estado
systemctl --user restart status-device     # reiniciar
systemctl --user stop status-device        # parar hasta el próximo login
systemctl --user disable --now status-device   # que no arranque más
journalctl --user -u status-device -f      # registro
```

### Cambiar opciones sin tocar la unidad

```bash
mkdir -p ~/.config/status-device
echo 'STATUS_DEVICE_ARGS=-warn 60 -crit 85 -interval 1s' > ~/.config/status-device/env
systemctl --user restart status-device
```

### Desinstalar

```bash
./scripts/uninstall.sh
```

## Alertas por color

Cada recurso tiene dos umbrales, `-warn` (75% por defecto) y `-crit` (90%):

| Nivel | Barras del icono | Punto del menú | Barra superior |
|---|---|---|---|
| Normal | verde | 🟢 | solo el texto |
| Alto (≥ warn) | ámbar | 🟡 | se antepone 🟡 |
| Crítico (≥ crit) | rojo | 🔴 | se antepone 🔴 + notificación |

El icono se dibuja desde el propio programa, así que los colores son reales
aunque el tema sea simbólico. La notificación solo salta si el recurso **se
mantiene** en rojo (`-notify-after`, 15 s) y no se repite hasta pasado
`-notify-every` (5 min), para que un pico puntual no moleste.

## Uso manual

```bash
go build -o status-device ./cmd/status-device
./status-device
```

| Flag | Por defecto | Descripción |
|---|---|---|
| `-interval` | `2s` | Frecuencia de refresco |
| `-format` | `{alert}CPU {cpu} · GPU {gpu} · RAM {ram}` | Texto de la barra |
| `-warn` / `-crit` | `75` / `90` | Umbrales de color, en porcentaje |
| `-notify` | `true` | Notificar cuando algo se mantiene en rojo |
| `-notify-after` | `15s` | Tiempo en rojo antes de avisar |
| `-notify-every` | `5m` | Espera mínima entre avisos de la misma métrica |
| `-icon` | *(vacío)* | Nombre de icono del tema; vacío = barras de color propias |
| `-once` | | Imprime una medición por stdout y termina |
| `-dump-icon` | | Depuración: guarda el icono como PNG y termina |

Marcadores admitidos en `-format`: `{alert}` `{cpu}` `{gpu}` `{ram}` `{swap}`
`{ram_used}` `{ram_free}` `{vram}` `{cpu_temp}` `{gpu_temp}` y los puntos de
color `{cpu_dot}` `{gpu_dot}` `{ram_dot}`.

```bash
./status-device -format '{cpu_dot}{cpu} {gpu_dot}{gpu} {ram_dot}{ram}'  # puntos siempre
./status-device -format '{cpu} {gpu} {ram}' -interval 1s                # compacto
```

## Estructura

```
cmd/status-device/     comando: flags, bucle de refresco y formato del texto
internal/metrics/      lectura de /proc y /sys (CPU, RAM, swap, GPU, temperaturas)
internal/alert/        umbrales, niveles de color y notificaciones de escritorio
internal/icon/         dibujo del icono ARGB de tres barras (sin dependencias)
internal/tray/         StatusNotifierItem y menú dbusmenu sobre D-Bus
scripts/               instalador y desinstalador
systemd/               unidad de usuario
```

## De dónde salen los datos

- **CPU**: diferencia entre dos lecturas de `/proc/stat`; temperatura desde
  `hwmon` (`k10temp`, `coretemp`, `zenpower`).
- **RAM/Swap**: `/proc/meminfo`; como memoria libre se usa `MemAvailable`, que
  es la que el sistema puede entregar de verdad (incluye caché recuperable).
- **GPU**: `/sys/class/drm/card*/device/gpu_busy_percent` y `mem_info_vram_*`
  (amdgpu, i915). Si no existen, se consulta `nvidia-smi`. Si tampoco está, la
  fila de GPU muestra «sin datos».

En APUs AMD la VRAM que se reporta es solo el bloque reservado (medio giga
típicamente), no la memoria del sistema que la GPU toma prestada por GTT; por
eso ese porcentaje suele verse alto sin que sea un problema.

## Si no aparece nada en la barra

```bash
gnome-extensions list --enabled | grep appindicator
gnome-extensions enable ubuntu-appindicators@ubuntu.com
```

Tras activar la extensión hay que cerrar y volver a abrir sesión en Wayland.
