#!/usr/bin/env bash
# Construye los paquetes de una versión y los publica como release de GitHub.
#
# No hay ninguna acción corriendo en el servidor: todo se compila aquí y se
# sube con «gh». El programa es Go puro sin cgo, así que cruzar de amd64 a
# arm64 es cambiar una variable de entorno.
#
#   scripts/release.sh v0.1.0 [notas.md]
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARCHES="${ARCHES:-amd64 arm64}"
DIST="$REPO_DIR/dist"

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
info() { printf '  %s\n' "$*"; }
die()  { printf '\033[31merror: %s\033[0m\n' "$*" >&2; exit 1; }

VERSION="${1:-}"
NOTES_FILE="${2:-}"
[ -n "$VERSION" ] || die "uso: scripts/release.sh vX.Y.Z [notas.md]"
[[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "la versión debe tener la forma vX.Y.Z: $VERSION"

cd "$REPO_DIR"

bold "==> Comprobaciones"
command -v go >/dev/null || die "hace falta Go"
command -v gh >/dev/null || die "hace falta la herramienta gh (https://cli.github.com)"
gh auth status >/dev/null 2>&1 || die "gh no está autenticado; ejecuta: gh auth login"

[ -z "$(git status --porcelain)" ] || die "hay cambios sin confirmar; publica desde un árbol limpio"
git rev-parse "$VERSION" >/dev/null 2>&1 && die "la etiqueta $VERSION ya existe"

branch="$(git rev-parse --abbrev-ref HEAD)"
[ "$branch" = main ] || die "las versiones salen de main, no de $branch"
git fetch --quiet origin main
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] ||
  die "main y origin/main no coinciden; sube o baja los cambios antes de publicar"
info "árbol limpio, main al día"

bold "==> Pruebas"
test -z "$(gofmt -l cmd internal)" || die "hay archivos sin formatear: $(gofmt -l cmd internal)"
go vet ./...
go test ./...
python3 -m py_compile ui/status-device-window.py
info "todo en verde"

bold "==> Compilando $VERSION"
rm -rf "$DIST"
mkdir -p "$DIST"
for arch in $ARCHES; do
  name="status-device-$VERSION-linux-$arch"
  stage="$DIST/$name"
  mkdir -p "$stage/scripts" "$stage/ui" "$stage/systemd"
  # -trimpath quita las rutas de esta máquina del binario; sin cgo, el
  # ejecutable no depende de la libc del equipo que compila.
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "$stage/status-device" ./cmd/status-device
  # Se conserva la disposición del repositorio para que install.sh encuentre
  # cada pieza donde espera; al no haber cmd/, usa el binario ya compilado.
  install -m 755 scripts/install.sh scripts/uninstall.sh "$stage/scripts/"
  install -m 755 ui/status-device-window.py "$stage/ui/"
  install -m 644 systemd/status-device.service "$stage/systemd/"
  install -m 644 README.md "$stage/"
  tar -C "$DIST" -czf "$DIST/$name.tar.gz" "$name"
  rm -rf "$stage"
  info "$name.tar.gz ($(du -h "$DIST/$name.tar.gz" | cut -f1))"
done

( cd "$DIST" && sha256sum ./*.tar.gz > SHA256SUMS )
info "SHA256SUMS"

bold "==> Publicando"
if [ -z "$NOTES_FILE" ]; then
  NOTES_FILE="$DIST/notas.md"
  previous="$(git describe --tags --abbrev=0 2>/dev/null || true)"
  {
    echo "## Cambios"
    echo
    if [ -n "$previous" ]; then
      git log --no-merges --pretty='- %s' "$previous..HEAD"
    else
      git log --no-merges --pretty='- %s'
    fi
  } > "$NOTES_FILE"
fi

git tag -a "$VERSION" -m "status-device $VERSION"
git push --quiet origin "$VERSION"
info "etiqueta $VERSION subida"

gh release create "$VERSION" "$DIST"/*.tar.gz "$DIST/SHA256SUMS" \
  --title "status-device $VERSION" --notes-file "$NOTES_FILE"

bold "==> Listo"
info "$(gh release view "$VERSION" --json url --jq .url)"
