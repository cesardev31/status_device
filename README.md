# status_device

Indicador para la barra del escritorio (GNOME Shell y KDE Plasma) que muestra el
uso de **CPU, GPU y RAM** en tiempo real. La barra se mantiene compacta y una
ventana gráfica muestra qué aplicaciones están consumiendo los recursos.

El servicio está escrito en Go puro: no usa cgo ni paquetes `-dev` de
GTK/AppIndicator. Habla D-Bus directamente y publica un
`org.kde.StatusNotifierItem`, que en GNOME dibuja la extensión **Ubuntu
AppIndicators** (activa por defecto en Ubuntu) y en **KDE Plasma** la bandeja
del sistema nativa, sin nada que instalar. La ventana usa GTK 4 y libadwaita.

```
▁▃█  CPU 12% · GPU 5% · RAM 41%
```

Un clic abre un menú pequeño con tres acciones; «Abrir administrador de tareas»
muestra la ventana grande. También se abre directamente con doble clic o con:

```bash
status-device -window
```

La ventana incluye tarjetas e historial corto de CPU, memoria y GPU; buscador;
y orden por CPU, memoria o nombre. Su interfaz, inspirada en el macOS actual,
separa un resumen visual de la lista completa mediante una barra lateral y
controles translúcidos. Los procesos de una misma aplicación se agrupan, como
en el Administrador de tareas: las múltiples pestañas de Brave, por ejemplo,
aparecen en una sola fila. También muestra la caché de Linux por separado: esa
memoria es reutilizable y no significa por sí sola que haya una fuga.

## Instalación

```bash
git clone https://github.com/cesardev31/status_device.git
cd status_device
./scripts/install.sh
```

El script compila el binario en `~/.local/bin/status-device`, instala el
servicio de usuario de systemd y lo arranca. A partir de ahí el indicador
aparece solo al iniciar sesión, sin tener que levantarlo a mano.

Requisitos: Go (`sudo apt install golang-go`), Python 3 con GTK 4/libadwaita,
una bandeja del sistema compatible con StatusNotifierItem (GNOME Shell con la
extensión de appindicators, o KDE Plasma sin más) y Linux (lee `/proc` y
`/sys`). Ubuntu con GNOME ya suele incluir las dependencias de la ventana.

## En KDE Plasma

Funciona sin instalar extensiones: la bandeja del sistema de Plasma habla el
mismo protocolo. Hay dos diferencias respecto a GNOME:

- **No se ve texto junto al icono.** Plasma implementa StatusNotifierItem pero
  no la extensión de Ayatana que pinta una etiqueta, así que las cifras se leen
  en las tres barras de color del icono, en el tooltip (pasando el ratón) y en
  el menú del indicador, que muestra el detalle completo.
- **El icono puede quedar oculto** en la zona desplegable de la bandeja. Clic
  derecho en el panel → *Configurar bandeja del sistema* → *Entradas* →
  `status-device` → **Mostrar siempre**.

Si la sesión de Plasma no arranca con systemd, la unidad se activa igualmente:
se instala también en `default.target` y el programa espera a que aparezca la
bandeja (hasta 90 s) antes de rendirse. Si reinicias `plasmashell`, el
indicador se vuelve a registrar solo.

Para la ventana del administrador de tareas hacen falta GTK 4 y libadwaita, que
en KDE no suelen venir instalados:

```bash
sudo apt install python3-gi gir1.2-gtk-4.0 gir1.2-adw-1   # Debian/Ubuntu/Neon
sudo pacman -S python-gobject gtk4 libadwaita             # Arch
sudo dnf install python3-gobject gtk4 libadwaita          # Fedora
```

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
| Crítico (≥ crit) | rojo | 🔴 | se antepone 🔴; notifica si está activado |

El icono se dibuja desde el propio programa, así que los colores son reales
aunque el tema sea simbólico. Las notificaciones están **desactivadas por
defecto**. Se pueden encender o apagar desde la casilla «Notificaciones» del
menú, o arrancar activadas con `-notify`. Cuando están activas, solo saltan si
el recurso **se mantiene** en rojo (`-notify-after`, 15 s) y no se repiten hasta
pasado `-notify-every` (5 min), para que un pico puntual no moleste.

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
| `-notify` | `false` | Notificar cuando algo se mantiene en rojo |
| `-notify-after` | `15s` | Tiempo en rojo antes de avisar |
| `-notify-every` | `5m` | Espera mínima entre avisos de la misma métrica |
| `-icon` | *(vacío)* | Nombre de icono del tema; vacío = barras de color propias |
| `-window` | | Abre el administrador de tareas gráfico |
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
internal/tray/         StatusNotifierItem y menú dbusmenu sobre D-Bus (GNOME/KDE)
ui/                    administrador de tareas en GTK 4/libadwaita
scripts/               instalador y desinstalador
systemd/               unidad de usuario
```

## De dónde salen los datos

- **CPU**: diferencia entre dos lecturas de `/proc/stat`; temperatura desde
  `hwmon` (`k10temp`, `coretemp`, `zenpower`).
- **RAM/Swap**: `/proc/meminfo`; como memoria libre se usa `MemAvailable`, que
  es la que el sistema puede entregar de verdad (incluye caché recuperable).
- **Procesos**: `/proc/<pid>/stat`, `statm` y `exe`; la CPU se mide entre dos
  muestras y la RAM RSS se agrupa por ejecutable. RSS es una aproximación y
  puede contar páginas compartidas en más de una aplicación.
- **GPU**: `/sys/class/drm/card*/device/gpu_busy_percent` y `mem_info_vram_*`
  (amdgpu, i915). Si no existen, se consulta `nvidia-smi`. Si tampoco está, la
  fila de GPU muestra «sin datos».

En APUs AMD la VRAM que se reporta es solo el bloque reservado (medio giga
típicamente), no la memoria del sistema que la GPU toma prestada por GTT; por
eso ese porcentaje suele verse alto sin que sea un problema.

## Si no aparece nada en la barra

En GNOME, casi siempre es la extensión:

```bash
gnome-extensions list --enabled | grep appindicator
gnome-extensions enable ubuntu-appindicators@ubuntu.com
```

Tras activar la extensión hay que cerrar y volver a abrir sesión en Wayland.

En KDE Plasma, comprueba que el widget «Bandeja del sistema» está en el panel y
que la entrada `status-device` no está oculta (clic derecho en el panel →
*Configurar bandeja del sistema* → *Entradas*). Para ver qué pasa:

```bash
journalctl --user -u status-device -f
systemctl --user restart status-device
```
