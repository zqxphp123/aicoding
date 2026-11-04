// 生成 PWA 图标（纯 Go，无外部素材）。
package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/image/vector"
)

func main() {
	out := "internal/web/assets/static/icons"
	if err := os.MkdirAll(out, 0o755); err != nil {
		log.Fatal(err)
	}
	for _, size := range []int{180, 192, 512} {
		name := "icon-" + strconv.Itoa(size) + ".png"
		if size == 180 {
			name = "apple-touch-icon.png"
		}
		if err := writeIcon(filepath.Join(out, name), size); err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote %s", name)
	}
}

func writeIcon(path string, size int) error {
	img := render(size)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func render(size int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{0x0f, 0x76, 0x6e, 0xff}
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)

	s := float32(size) / 512
	white := image.NewUniform(color.RGBA{0xff, 0xff, 0xff, 0xff})
	r := vector.NewRasterizer(size, size)

	stem := float32(30) * s
	rect(r, 256*s-stem/2, 150*s, 256*s+stem/2, 392*s)
	seg(r, 148*s, 108*s, 256*s, 246*s, 34*s)
	seg(r, 364*s, 108*s, 256*s, 246*s, 34*s)
	rect(r, 158*s, 300*s, 354*s, 326*s)
	rect(r, 158*s, 350*s, 354*s, 376*s)

	r.Draw(dst, dst.Bounds(), white, image.Point{})
	return dst
}

func rect(r *vector.Rasterizer, x1, y1, x2, y2 float32) {
	r.MoveTo(x1, y1)
	r.LineTo(x2, y1)
	r.LineTo(x2, y2)
	r.LineTo(x1, y2)
	r.ClosePath()
}

// seg 以厚度 t 画一条有宽度的线段。
func seg(r *vector.Rasterizer, x1, y1, x2, y2, t float32) {
	dx, dy := x2-x1, y2-y1
	l := float32(math.Hypot(float64(dx), float64(dy)))
	if l == 0 {
		return
	}
	nx, ny := -dy/l*t/2, dx/l*t/2
	r.MoveTo(x1+nx, y1+ny)
	r.LineTo(x2+nx, y2+ny)
	r.LineTo(x2-nx, y2-ny)
	r.LineTo(x1-nx, y1-ny)
	r.ClosePath()
}
