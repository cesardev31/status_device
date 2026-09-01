#!/usr/bin/env bash
# Para el servicio, lo desinstala y borra el binario.
set -euo pipefail

BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
UNIT_DIR="${UNIT_DIR:-$HOME/.config/systemd/user}"
LIB_DIR="${LIB_DIR:-$HOME/.local/lib/status-device}"
SERVICE="status-device.service"

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
info() { printf '  %s\n' "$*"; }

bold "==> Parando el servicio"
systemctl --user disable --now "$SERVICE" 2>/dev/null || info "no estaba activo"

bold "==> Borrando archivos"
rm -f "$UNIT_DIR/$SERVICE" && info "unidad eliminada"
rm -f "$BIN_DIR/status-device" && info "binario eliminado"
rm -f "$LIB_DIR/status-device-window.py" && info "ventana gráfica eliminada"
rmdir "$LIB_DIR" 2>/dev/null || true
systemctl --user daemon-reload

bold "==> Hecho"
info "la configuración en ~/.config/status-device se conserva; bórrala a mano si no la quieres"
