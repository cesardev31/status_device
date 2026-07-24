#!/usr/bin/env bash
# Compila status-device, lo instala en ~/.local/bin y lo deja corriendo como
# servicio de usuario de systemd (arranca solo al iniciar sesión).
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
UNIT_DIR="${UNIT_DIR:-$HOME/.config/systemd/user}"
SERVICE="status-device.service"

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
info() { printf '  %s\n' "$*"; }
warn() { printf '\033[33m  ! %s\033[0m\n' "$*"; }
die()  { printf '\033[31merror: %s\033[0m\n' "$*" >&2; exit 1; }

bold "==> Comprobaciones"
command -v go >/dev/null || die "hace falta Go. Instálalo con: sudo apt install golang-go"
info "go $(go version | awk '{print $3}' | sed 's/^go//')"

command -v systemctl >/dev/null || die "systemd no está disponible en este sistema"

if command -v gnome-extensions >/dev/null; then
  if gnome-extensions list --enabled 2>/dev/null | grep -q appindicator; then
    info "extensión de appindicators activa"
  else
    warn "la extensión de appindicators no está activa; sin ella el icono no se ve."
    warn "actívala con: gnome-extensions enable ubuntu-appindicators@ubuntu.com"
  fi
fi

bold "==> Compilando"
mkdir -p "$BIN_DIR"
go build -trimpath -ldflags "-s -w" -o "$BIN_DIR/status-device" "$REPO_DIR/cmd/status-device"
info "binario en $BIN_DIR/status-device"

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
  info "estado:    systemctl --user status $SERVICE"
  info "registro:  journalctl --user -u $SERVICE -f"
  info "opciones:  echo 'STATUS_DEVICE_ARGS=-warn 60 -crit 85' > ~/.config/status-device/env"
  info "           (luego: systemctl --user restart $SERVICE)"
else
  systemctl --user status "$SERVICE" --no-pager || true
  die "el servicio no arrancó; revisa el estado de arriba"
fi
