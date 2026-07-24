package icon

import (
	"image"
	"image/color"
	"image/png"
	"os"
)

// DumpPNG guarda el icono como PNG, ampliado y sobre fondo oscuro, para poder
// revisar el diseño sin tener que mirarlo en la barra (flag -dump-icon).
func DumpPNG(path string, bars []Bar, scale int) error {
	p := draw(bars, 44)
	w, h := int(p.Width), int(p.Height)
	img := image.NewNRGBA(image.Rect(0, 0, w*scale, h*scale))
	bg := color.NRGBA{0x24, 0x1f, 0x31, 0xff} // panel oscuro de Ubuntu
	for y := 0; y < h*scale; y++ {
		for x := 0; x < w*scale; x++ {
			i := ((y/scale)*w + x/scale) * 4
			a := float64(p.Data[i]) / 255
			mix := func(fg, back byte) uint8 {
				return uint8(float64(fg)*a + float64(back)*(1-a) + 0.5)
			}
			img.Set(x, y, color.NRGBA{
				R: mix(p.Data[i+1], bg.R),
				G: mix(p.Data[i+2], bg.G),
				B: mix(p.Data[i+3], bg.B),
				A: 0xff,
			})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
