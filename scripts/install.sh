#!/usr/bin/env bash
# Compila status-device, lo instala en ~/.local/bin y lo deja corriendo como
# servicio de usuario de systemd (arranca solo al iniciar sesión).
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
UNIT_DIR="${UNIT_DIR:-$HOME/.config/systemd/user}"
LIB_DIR="${LIB_DIR:-$HOME/.local/lib/status-device}"
SERVICE="status-device.service"

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
info() { printf '  %s\n' "$*"; }
warn() { printf '\033[33m  ! %s\033[0m\n' "$*"; }
die()  { printf '\033[31merror: %s\033[0m\n' "$*" >&2; exit 1; }

bold "==> Comprobaciones"
command -v go >/dev/null || die "hace falta Go. Instálalo con: sudo apt install golang-go"
info "go $(go version | awk '{print $3}' | sed 's/^go//')"

command -v systemctl >/dev/null || die "systemd no está disponible en este sistema"

desktop="$(printf '%s' "${XDG_CURRENT_DESKTOP:-${DESKTOP_SESSION:-}}" | tr '[:upper:]' '[:lower:]')"
case "$desktop" in
  *kde*|*plasma*) DESKTOP_KIND=kde ;;
  *gnome*|*ubuntu*|*pop*) DESKTOP_KIND=gnome ;;
  *) DESKTOP_KIND=other ;;
esac

case "$DESKTOP_KIND" in
  gnome)
    info "escritorio: GNOME"
    if command -v gnome-extensions >/dev/null; then
      if gnome-extensions list --enabled 2>/dev/null | grep -q appindicator; then
        info "extensión de appindicators activa"
      else
        warn "la extensión de appindicators no está activa; sin ella el icono no se ve."
        warn "actívala con: gnome-extensions enable ubuntu-appindicators@ubuntu.com"
      fi
    fi
    ;;
  kde)
    info "escritorio: KDE Plasma (bandeja del sistema nativa, no hace falta extensión)"
    warn "Plasma no dibuja texto junto al icono: las cifras se ven en las barras del"
    warn "icono, en el tooltip y en el menú del indicador."
    ;;
  *)
    warn "escritorio no reconocido (${XDG_CURRENT_DESKTOP:-desconocido}); hace falta una bandeja"
    warn "compatible con StatusNotifierItem para ver el indicador."
    ;;
esac

if command -v python3 >/dev/null; then
  if python3 - <<'PYCHECK' 2>/dev/null
import gi
gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gtk  # noqa: F401
PYCHECK
  then
    info "GTK 4 y libadwaita disponibles para la ventana"
  else
    warn "faltan GTK 4/libadwaita para python3; el indicador funciona, pero la ventana no abrirá."
    if command -v apt >/dev/null; then
      warn "instálalos con: sudo apt install python3-gi gir1.2-gtk-4.0 gir1.2-adw-1"
    elif command -v pacman >/dev/null; then
      warn "instálalos con: sudo pacman -S python-gobject gtk4 libadwaita"
    elif command -v dnf >/dev/null; then
      warn "instálalos con: sudo dnf install python3-gobject gtk4 libadwaita"
    fi
  fi
else
  warn "python3 no está instalado; el indicador funciona, pero la ventana no abrirá."
fi

bold "==> Compilando"
mkdir -p "$BIN_DIR"
go build -trimpath -ldflags "-s -w" -o "$BIN_DIR/status-device" "$REPO_DIR/cmd/status-device"
info "binario en $BIN_DIR/status-device"

mkdir -p "$LIB_DIR"
install -m 755 "$REPO_DIR/ui/status-device-window.py" "$LIB_DIR/status-device-window.py"
info "ventana gráfica en $LIB_DIR/status-device-window.py"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) warn "$BIN_DIR no está en tu PATH (el servicio funciona igual, usa la ruta absoluta)" ;;
esac

bold "==> Instalando el servicio"
mkdir -p "$UNIT_DIR"
install -m 644 "$REPO_DIR/systemd/$SERVICE" "$UNIT_DIR/$SERVICE"
info "unidad en $UNIT_DIR/$SERVICE"

systemctl --user daemon-reload
systemctl --user enable "$SERVICE" >/dev/null
systemctl --user restart "$SERVICE"

sleep 1
if systemctl --user is-active --quiet "$SERVICE"; then
  bold "==> Listo"
  info "el indicador ya está en la barra y arrancará solo al iniciar sesión"
  if [ "$DESKTOP_KIND" = kde ]; then
    info "en Plasma: clic izquierdo abre el administrador de tareas, clic derecho el menú"
  fi
  info "estado:    systemctl --user status $SERVICE"
  info "registro:  journalctl --user -u $SERVICE -f"
  info "opciones:  echo 'STATUS_DEVICE_ARGS=-warn 60 -crit 85' > ~/.config/status-device/env"
  info "           (luego: systemctl --user restart $SERVICE)"
else
  systemctl --user status "$SERVICE" --no-pager || true
  die "el servicio no arrancó; revisa el estado de arriba"
fi
