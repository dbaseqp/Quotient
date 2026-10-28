// Package timelapse renders per-round service status as an animated GIF
// heatmap with one frame per round.
package timelapse

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"io"
	"math"
	"unicode/utf8"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/math/fixed"
)

const (
	cellW      = 24
	cellH      = 16
	pad        = 8
	titleH     = 16
	glyphW     = 7                  // basicfont.Face7x13
	glyphH     = 13                 // basicfont.Face7x13
	labelAngle = 55 * math.Pi / 180 // same angle as the /graphs service labels
	maxLabel   = 32                 // longer team and service names are cut short
	maxPixels  = 1 << 20            // every frame is held in memory until encoded
	maxFrames  = 300                // longer events are sampled
	frameDelay = 10                 // centiseconds
)

// Palette indexes.
const (
	white uint8 = iota
	black
	green
	red
	grey
	transparent
)

// The status colors are the ones the /graphs status heatmap uses.
var palette = color.Palette{
	color.RGBA{0xFF, 0xFF, 0xFF, 0xFF},
	color.RGBA{0x00, 0x00, 0x00, 0xFF},
	color.RGBA{0x3D, 0x99, 0x70, 0xFF},
	color.RGBA{0xAA, 0x07, 0x07, 0xFF},
	color.RGBA{0x77, 0x77, 0x77, 0xFF},
	color.RGBA{0x00, 0x00, 0x00, 0x00},
}

type status uint8

const (
	unknown status = iota // no check result
	up
	down
)

var statusColor = [...]uint8{unknown: grey, up: green, down: red}

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
func (b *Builder) Build() (*Timelapse, error) {
	if len(b.rounds) == 0 {
		return nil, ErrNoRounds
	}
	t := &Timelapse{teams: shorten(b.teams), services: shorten(b.services), rounds: sample(b.rounds, maxFrames)}
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
}

// layOut sizes the image to fit the longest names and the widest title.
func (t *Timelapse) layOut(maxRound uint) {
	longest := func(names []string) int {
		n := 0
		for _, name := range names {
			n = max(n, utf8.RuneCountInString(name))
		}
		return n * glyphW
	}
	sin, cos := math.Sincos(labelAngle)
	labelLen := float64(longest(t.services))

	t.gridX = pad + longest(t.teams) + pad
	t.gridY = pad + titleH + int(math.Ceil(labelLen*sin+glyphH*cos)) + pad/2
	// The last service label leans right past the grid.
	t.width = t.gridX + len(t.services)*cellW + max(pad, int(math.Ceil(labelLen*cos))-cellW/2+pad)
	t.width = max(t.width, pad+utf8.RuneCountInString(title(maxRound))*glyphW+pad)
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
	cur := image.NewPaletted(bounds, palette)
	fill(cur, bounds, white)
	for i, name := range t.teams {
		drawText(cur, pad, t.gridY+i*cellH+cellH-4, name)
	}
	for j, svc := range t.services {
		drawRotatedText(cur, t.gridX+j*cellW+cellW/2, t.gridY-2, svc)
	}

	g := &gif.GIF{}
	prev := image.NewPaletted(bounds, palette)
	for i, round := range t.rounds {
		fill(cur, image.Rect(0, 0, t.width, pad+titleH), white)
		drawText(cur, pad, pad+glyphH, title(round.id))
		for row := range t.teams {
			for col := range t.services {
				fill(cur, t.cell(row, col), statusColor[round.cells[row*len(t.services)+col]])
			}
		}

		var frame *image.Paletted
		if i == 0 {
			// cur is reused for later rounds, so keep a copy.
			frame = image.NewPaletted(bounds, palette)
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
	draw.Draw(img, r, image.NewUniform(palette[c]), image.Point{}, draw.Src)
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
	out := image.NewPaletted(changed, palette)
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

func drawText(img *image.Paletted, x, baseline int, s string) {
	d := font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(palette[black]),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(x, baseline),
	}
	d.DrawString(s)
}

// drawRotatedText draws s rising to the right at labelAngle, with the
// bottom-left of the text at (x, y).
func drawRotatedText(img *image.Paletted, x, y int, s string) {
	text := image.NewRGBA(image.Rect(0, 0, utf8.RuneCountInString(s)*glyphW, glyphH))
	d := font.Drawer{
		Dst:  text,
		Src:  image.Black,
		Face: basicfont.Face7x13,
		Dot:  fixed.P(0, glyphH-2),
	}
	d.DrawString(s)

	// Rotate about the text's bottom-left corner and move it to (x, y). The
	// half-pixel terms map pixel centers onto pixel centers.
	sin, cos := math.Sincos(labelAngle)
	qx, qy := 0.5, 0.5+glyphH
	m := f64.Aff3{
		cos, sin, float64(x) + 0.5 - (cos*qx + sin*qy),
		-sin, cos, float64(y) + 0.5 - (-sin*qx + cos*qy),
	}
	xdraw.NearestNeighbor.Transform(img, m, text, text.Bounds(), xdraw.Over, nil)
}
