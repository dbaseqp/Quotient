// Package timelapse renders per-round service status as an animated GIF
// heatmap with one frame per round.
package timelapse

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"io"
	"math"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/math/fixed"
)

const (
	cellW      = 34
	cellH      = 34
	iconSize   = 32 // fits inside a cell, leaving a 1px gap between cells
	fontSize   = 14 // pixels
	titleSize  = 24 // pixels
	pad        = 8
	labelAngle = 55 * math.Pi / 180 // same angle as the /graphs service labels
	maxLabel   = 32                 // longer team and service names are cut short
	maxPixels  = 1 << 21            // every frame is held in memory until encoded
	maxFrames  = 300                // longer events are sampled
	frameDelay = 10                 // centiseconds
)

// Indexes of the fixed colors at the start of the palette.
const (
	white       uint8 = 0
	black       uint8 = 1
	transparent uint8 = 5
)

// The fixed colors are the ones the /graphs status heatmap uses. The web-safe
// colors after them render the status icons.
var colors = append(color.Palette{
	color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}, // white
	color.RGBA{0x00, 0x00, 0x00, 0xFF}, // black
	color.RGBA{0x3D, 0x99, 0x70, 0xFF}, // up
	color.RGBA{0xAA, 0x07, 0x07, 0xFF}, // down
	color.RGBA{0x77, 0x77, 0x77, 0xFF}, // unknown
	color.RGBA{0x00, 0x00, 0x00, 0x00}, // transparent
}, palette.WebSafe...)

// Icons are the images drawn for each status, the same ones the /graphs
// status heatmap uses.
type Icons struct {
	Up, Down, Unknown image.Image
}

type status uint8

const (
	unknown status = iota // no check result
	up
	down
)

type round struct {
	id    uint
	cells []status // row-major, one row per team
}

// Builder collects check results into one frame per round.
type Builder struct {
	teams, services []string
	rounds          []round
}

// NewBuilder starts a timelapse with one row per team and one column per
// service, in the order given.
func NewBuilder(teams, services []string) *Builder {
	return &Builder{teams: teams, services: services}
}

// Set records whether a team's service was up in a round. Results must arrive
// grouped by round, in the order the rounds should play.
func (b *Builder) Set(roundID uint, team, service int, isUp bool) {
	if len(b.rounds) == 0 || b.rounds[len(b.rounds)-1].id != roundID {
		b.rounds = append(b.rounds, round{id: roundID, cells: make([]status, len(b.teams)*len(b.services))})
	}
	s := down
	if isUp {
		s = up
	}
	b.rounds[len(b.rounds)-1].cells[team*len(b.services)+service] = s
}

// ErrNoRounds means no result was set.
var ErrNoRounds = errors.New("no rounds to draw")

// Build lays out the image, refusing one too large to draw.
func (b *Builder) Build(icons Icons) (*Timelapse, error) {
	if len(b.rounds) == 0 {
		return nil, ErrNoRounds
	}
	if icons.Up == nil || icons.Down == nil || icons.Unknown == nil {
		return nil, errors.New("missing status icon")
	}
	face, err := newFace(goregular.TTF, fontSize)
	if err != nil {
		return nil, err
	}
	titleFace, err := newFace(gobold.TTF, titleSize)
	if err != nil {
		return nil, err
	}
	t := &Timelapse{teams: shorten(b.teams), services: shorten(b.services), rounds: sample(b.rounds, maxFrames), face: face, titleFace: titleFace}
	t.tiles[up] = tile(icons.Up)
	t.tiles[down] = tile(icons.Down)
	t.tiles[unknown] = tile(icons.Unknown)
	var maxRound uint
	for _, r := range t.rounds {
		maxRound = max(maxRound, r.id)
	}
	t.layOut(maxRound)
	if t.width > math.MaxUint16 || t.height > math.MaxUint16 || t.width*t.height > maxPixels {
		return nil, fmt.Errorf("%dx%d is too large to draw", t.width, t.height)
	}
	return t, nil
}

// sample keeps at most n rounds, evenly spaced and always including the last.
func sample(rounds []round, n int) []round {
	if len(rounds) <= n {
		return rounds
	}
	out := make([]round, n)
	for i := range out {
		out[i] = rounds[i*(len(rounds)-1)/(n-1)]
	}
	return out
}

func shorten(names []string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		if r := []rune(name); len(r) > maxLabel {
			name = string(r[:maxLabel-3]) + "..."
		}
		out[i] = name
	}
	return out
}

// Timelapse is a laid-out animation, ready to encode.
type Timelapse struct {
	teams, services []string
	rounds          []round
	gridX, gridY    int
	width, height   int
	titleH          int
	tiles           [3]*image.Paletted // one per status, cell-sized
	// Faces are not safe for concurrent use, so each Timelapse has its own.
	face, titleFace font.Face
}

func newFace(ttf []byte, size float64) (font.Face, error) {
	f, err := opentype.Parse(ttf)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}

// textSize returns the width of s in face and the face's ascent and descent.
func textSize(face font.Face, s string) (width, ascent, descent int) {
	m := face.Metrics()
	return font.MeasureString(face, s).Ceil(), m.Ascent.Ceil(), m.Descent.Ceil()
}

// tile renders an icon centered on a white cell-sized square.
func tile(icon image.Image) *image.Paletted {
	r := image.Rect(0, 0, cellW-1, cellH-1)
	rgba := image.NewRGBA(r)
	draw.Draw(rgba, r, image.White, image.Point{}, draw.Src)
	off := image.Pt((r.Dx()-iconSize)/2, (r.Dy()-iconSize)/2)
	xdraw.CatmullRom.Scale(rgba, image.Rect(0, 0, iconSize, iconSize).Add(off), icon, icon.Bounds(), xdraw.Over, nil)
	t := image.NewPaletted(r, colors)
	draw.Draw(t, r, rgba, image.Point{}, draw.Src)
	return t
}

// paste copies a tile into img with its top-left corner at at.
func paste(img, tile *image.Paletted, at image.Point) {
	for y := 0; y < tile.Rect.Dy(); y++ {
		copy(img.Pix[img.PixOffset(at.X, at.Y+y):], tile.Pix[tile.PixOffset(0, y):tile.PixOffset(tile.Rect.Dx(), y)])
	}
}

// layOut sizes the image to fit the longest names and the widest title.
func (t *Timelapse) layOut(maxRound uint) {
	longest := func(names []string) int {
		n := 0
		for _, name := range names {
			w, _, _ := textSize(t.face, name)
			n = max(n, w)
		}
		return n
	}
	titleW, titleAscent, titleDescent := textSize(t.titleFace, title(maxRound))
	_, ascent, descent := textSize(t.face, "")
	lineH := ascent + descent
	sin, cos := math.Sincos(labelAngle)
	labelLen := float64(longest(t.services))

	t.titleH = titleAscent + titleDescent
	t.gridX = pad + longest(t.teams) + pad
	t.gridY = pad + t.titleH + int(math.Ceil(labelLen*sin+float64(lineH)*cos)) + pad/2
	// The last service label leans right past the grid.
	t.width = t.gridX + len(t.services)*cellW + max(pad, int(math.Ceil(labelLen*cos))-cellW/2+pad)
	t.width = max(t.width, pad+titleW+pad)
	t.height = t.gridY + len(t.teams)*cellH + pad
}

func (t *Timelapse) cell(row, col int) image.Rectangle {
	x := t.gridX + col*cellW
	y := t.gridY + row*cellH
	return image.Rect(x, y, x+cellW-1, y+cellH-1)
}

func title(round uint) string {
	return fmt.Sprintf("Round %d", round)
}

// Encode writes the animation to w. Each frame after the first holds only the
// region that changed, with unchanged pixels transparent.
func (t *Timelapse) Encode(w io.Writer) error {
	bounds := image.Rect(0, 0, t.width, t.height)
	cur := image.NewPaletted(bounds, colors)
	fill(cur, bounds, white)
	_, ascent, descent := textSize(t.face, "")
	_, titleAscent, _ := textSize(t.titleFace, "")
	for i, name := range t.teams {
		drawText(cur, t.face, pad, t.gridY+i*cellH+(cellH+ascent-descent)/2, name)
	}
	for j, svc := range t.services {
		t.drawRotatedText(cur, t.gridX+j*cellW+cellW/2, t.gridY-2, svc)
	}

	g := &gif.GIF{}
	prev := image.NewPaletted(bounds, colors)
	for i, round := range t.rounds {
		fill(cur, image.Rect(0, 0, t.width, pad+t.titleH), white)
		drawText(cur, t.titleFace, pad, pad+titleAscent, title(round.id))
		for row := range t.teams {
			for col := range t.services {
				paste(cur, t.tiles[round.cells[row*len(t.services)+col]], t.cell(row, col).Min)
			}
		}

		var frame *image.Paletted
		if i == 0 {
			// cur is reused for later rounds, so keep a copy.
			frame = image.NewPaletted(bounds, colors)
			copy(frame.Pix, cur.Pix)
		} else {
			frame = delta(prev, cur)
		}
		g.Image = append(g.Image, frame)
		g.Delay = append(g.Delay, frameDelay)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
		prev, cur = cur, prev
		copy(cur.Pix, prev.Pix)
	}
	g.Delay[len(g.Delay)-1] *= 3 // hold the final round
	return gif.EncodeAll(w, g)
}

func fill(img *image.Paletted, r image.Rectangle, c uint8) {
	draw.Draw(img, r, image.NewUniform(colors[c]), image.Point{}, draw.Src)
}

// delta returns the smallest image covering every pixel that differs between
// prev and cur, with unchanged pixels transparent.
func delta(prev, cur *image.Paletted) *image.Paletted {
	b := cur.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, -1, -1
	for i := range cur.Pix {
		if cur.Pix[i] != prev.Pix[i] {
			x, y := i%cur.Stride, i/cur.Stride
			minX, minY = min(minX, x), min(minY, y)
			maxX, maxY = max(maxX, x), max(maxY, y)
		}
	}
	changed := image.Rect(0, 0, 1, 1)
	if maxX >= 0 {
		changed = image.Rect(minX, minY, maxX+1, maxY+1)
	}
	out := image.NewPaletted(changed, colors)
	for y := changed.Min.Y; y < changed.Max.Y; y++ {
		for x := changed.Min.X; x < changed.Max.X; x++ {
			c := cur.ColorIndexAt(x, y)
			if c == prev.ColorIndexAt(x, y) {
				c = transparent
			}
			out.SetColorIndex(x, y, c)
		}
	}
	return out
}

func drawText(img *image.Paletted, face font.Face, x, baseline int, s string) {
	d := font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(colors[black]),
		Face: face,
		Dot:  fixed.P(x, baseline),
	}
	d.DrawString(s)
}

// drawRotatedText draws s rising to the right at labelAngle, with the
// bottom-left of the text at (x, y).
func (t *Timelapse) drawRotatedText(img *image.Paletted, x, y int, s string) {
	w, ascent, descent := textSize(t.face, s)
	h := ascent + descent
	text := image.NewRGBA(image.Rect(0, 0, w, h))
	d := font.Drawer{
		Dst:  text,
		Src:  image.Black,
		Face: t.face,
		Dot:  fixed.P(0, ascent),
	}
	d.DrawString(s)

	// Rotate about the text's bottom-left corner and move it to (x, y). The
	// half-pixel terms map pixel centers onto pixel centers.
	sin, cos := math.Sincos(labelAngle)
	qx, qy := 0.5, 0.5+float64(h)
	m := f64.Aff3{
		cos, sin, float64(x) + 0.5 - (cos*qx + sin*qy),
		-sin, cos, float64(y) + 0.5 - (-sin*qx + cos*qy),
	}
	xdraw.ApproxBiLinear.Transform(img, m, text, text.Bounds(), xdraw.Over, nil)
}
