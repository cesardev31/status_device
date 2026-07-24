// Package icon dibuja el icono del indicador: tres barras verticales cuyo
// relleno y color reflejan el uso de cada recurso. Se genera a mano, sin
// dependencias de dibujo, y se entrega como mapa de bits ARGB para D-Bus.
package icon

import (
	"math"

	"github.com/cesardev31/status_device/internal/alert"
)

// Pixmap es el tipo (iiay) de la propiedad IconPixmap: ancho, alto y píxeles
// ARGB32 sin premultiplicar, fila a fila.
type Pixmap struct {
	Width  int32
	Height int32
	Data   []byte
}

// Bar es una barra del icono: su relleno (0-100), su nivel de aviso y si hay
// datos disponibles para ese recurso.
type Bar struct {
	Value float64
	OK    bool
	Level alert.Level
}

type rgb struct{ R, G, B uint8 }

// Paleta pensada para el panel oscuro de Ubuntu.
var (
	colorOK    = rgb{0x3d, 0xdc, 0x84} // verde
	colorWarn  = rgb{0xf6, 0xd3, 0x2d} // ámbar
	colorCrit  = rgb{0xff, 0x5c, 0x5c} // rojo
	colorTrack = rgb{0xff, 0xff, 0xff} // canal de fondo, con alpha bajo
)

const trackAlpha = 0.20

func levelColor(l alert.Level) rgb {
	switch l {
	case alert.Crit:
		return colorCrit
	case alert.Warn:
		return colorWarn
	}
	return colorOK
}

// Build dibuja el icono en dos tamaños para que GNOME elija según el escalado
// de la pantalla.
func Build(bars []Bar) []Pixmap {
	return []Pixmap{draw(bars, 22), draw(bars, 44)}
}

// draw pinta el icono a tamaño size con supermuestreo 4x para suavizar los
// bordes redondeados.
func draw(bars []Bar, size int) Pixmap {
	const ss = 4
	n := float64(len(bars))
	s := float64(size)

	// Geometría en coordenadas del icono, proporcional al tamaño.
	pad := s * 0.10
	gap := s * 0.14
	barW := (s - 2*pad - gap*(n-1)) / n
	top := s * 0.11
	bottom := s - top
	height := bottom - top
	radius := barW / 2

	// Acumuladores en color premultiplicado, que es como se puede promediar.
	accR := make([]float64, size*size)
	accG := make([]float64, size*size)
	accB := make([]float64, size*size)
	accA := make([]float64, size*size)

	for i, b := range bars {
		x0 := pad + float64(i)*(barW+gap)
		x1 := x0 + barW

		fillH := 0.0
		if b.OK {
			fillH = math.Max(barW, height*clamp(b.Value)/100)
		}
		fillTop := bottom - fillH
		fc := levelColor(b.Level)

		// Ventana de píxeles que puede tocar esta barra.
		px0 := int(math.Floor(x0))
		px1 := int(math.Ceil(x1))
		py0 := int(math.Floor(top))
		py1 := int(math.Ceil(bottom))

		for py := py0; py < py1 && py < size; py++ {
			for px := px0; px < px1 && px < size; px++ {
				if px < 0 || py < 0 {
					continue
				}
				var sr, sg, sb, sa float64
				for sy := 0; sy < ss; sy++ {
					for sx := 0; sx < ss; sx++ {
						fx := float64(px) + (float64(sx)+0.5)/ss
						fy := float64(py) + (float64(sy)+0.5)/ss
						if !inRoundRect(fx, fy, x0, top, barW, height, radius) {
							continue
						}
						if b.OK && fy >= fillTop {
							sr += float64(fc.R)
							sg += float64(fc.G)
							sb += float64(fc.B)
							sa++
						} else {
							sr += float64(colorTrack.R) * trackAlpha
							sg += float64(colorTrack.G) * trackAlpha
							sb += float64(colorTrack.B) * trackAlpha
							sa += trackAlpha
						}
					}
				}
				idx := py*size + px
				div := float64(ss * ss)
				accR[idx] += sr / div
				accG[idx] += sg / div
				accB[idx] += sb / div
				accA[idx] += sa / div
			}
		}
	}

	data := make([]byte, size*size*4)
	for i := range accA {
		a := accA[i]
		if a <= 0 {
			continue
		}
		if a > 1 {
			a = 1
		}
		// De premultiplicado a ARGB directo, que es lo que espera Cogl.
		data[i*4+0] = byte(a*255 + 0.5)
		data[i*4+1] = unpremul(accR[i], a)
		data[i*4+2] = unpremul(accG[i], a)
		data[i*4+3] = unpremul(accB[i], a)
	}
	return Pixmap{Width: int32(size), Height: int32(size), Data: data}
}

func unpremul(c, a float64) byte {
	v := c / a
	if v < 0 {
		v = 0
	}
	if v > 255 {
		v = 255
	}
	return byte(v + 0.5)
}

// inRoundRect indica si el punto cae dentro de un rectángulo de esquinas
// redondeadas.
func inRoundRect(px, py, x, y, w, h, r float64) bool {
	if px < x || px > x+w || py < y || py > y+h {
		return false
	}
	if r <= 0 {
		return true
	}
	// Centro de la esquina más cercana en cada eje.
	cx := math.Min(math.Max(px, x+r), x+w-r)
	cy := math.Min(math.Max(py, y+r), y+h-r)
	dx, dy := px-cx, py-cy
	return dx*dx+dy*dy <= r*r
}

func clamp(v float64) float64 {
	return math.Min(math.Max(v, 0), 100)
}
