package timelapse

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// solid returns a square icon of one palette color, so a cell's center pixel
// shows which icon was drawn.
func solid(index uint8) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	draw.Draw(img, img.Bounds(), image.NewUniform(colors[index]), image.Point{}, draw.Src)
	return img
}

var (
	testIcons   = Icons{Up: solid(2), Down: solid(3), Unknown: solid(4)}
	statusColor = [...]uint8{unknown: 4, up: 2, down: 3}
)

// play decodes the GIF and returns what a viewer shows after each frame.
func play(t require.TestingT, data []byte) (*gif.GIF, []*image.Paletted) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	require.NoError(t, err)

	canvas := image.NewPaletted(image.Rect(0, 0, g.Config.Width, g.Config.Height), colors)
	shown := make([]*image.Paletted, 0, len(g.Image))
	for i, frame := range g.Image {
		require.Equal(t, byte(gif.DisposalNone), g.Disposal[i])
		b := frame.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if c := frame.ColorIndexAt(x, y); c != transparent {
					canvas.SetColorIndex(x, y, c)
				}
			}
		}
		snapshot := image.NewPaletted(canvas.Rect, colors)
		copy(snapshot.Pix, canvas.Pix)
		shown = append(shown, snapshot)
	}
	return g, shown
}

// frame lists one round's cells, row-major; unknown cells are left unset.
type frame struct {
	id    uint
	cells []status
}

func build(teams, services []string, frames []frame) (*Timelapse, error) {
	b := NewBuilder(teams, services)
	for _, f := range frames {
		for i, s := range f.cells {
			if s != unknown {
				b.Set(f.id, i/len(services), i%len(services), s == up)
			}
		}
	}
	return b.Build(testIcons)
}

func encode(t require.TestingT, teams, services []string, frames []frame) (*Timelapse, []byte) {
	tl, err := build(teams, services, frames)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, tl.Encode(&buf))
	return tl, buf.Bytes()
}

func cellColor(img *image.Paletted, tl *Timelapse, row, col int) uint8 {
	c := tl.cell(row, col)
	return img.ColorIndexAt(c.Min.X+cellW/2, c.Min.Y+cellH/2)
}

func TestEncodeDrawsEachRound(t *testing.T) {
	teams := []string{"team07", "a-much-longer-team-name"}
	services := []string{"dns", "web"}
	frames := []frame{
		{id: 1, cells: []status{unknown, up, up, down}},
		{id: 2, cells: []status{down, down, unknown, unknown}},
		{id: 3, cells: []status{unknown, unknown, unknown, unknown}}, // no results: no frame
		{id: 4, cells: []status{down, down, unknown, unknown}},       // only the title changes
	}
	tl, data := encode(t, teams, services, frames)
	g, shown := play(t, data)

	require.Len(t, shown, 3, "one frame per round with results")
	assert.Equal(t, []int{10, 10, 30}, g.Delay, "the last round holds three times longer")
	assert.Equal(t, 0, g.LoopCount, "loops forever")
	assert.Equal(t, tl.width, g.Config.Width)
	assert.Equal(t, tl.height, g.Config.Height)

	for i, f := range []frame{frames[0], frames[1], frames[3]} {
		for row := range teams {
			for col := range services {
				want := statusColor[f.cells[row*len(services)+col]]
				assert.Equal(t, want, cellColor(shown[i], tl, row, col), "round %d row %d col %d", f.id, row, col)
			}
		}
	}
}

func TestBuildRejects(t *testing.T) {
	_, err := NewBuilder([]string{"team01"}, []string{"web"}).Build(testIcons)
	assert.ErrorIs(t, err, ErrNoRounds)

	b := NewBuilder([]string{"team01"}, []string{"web"})
	b.Set(1, 0, 0, true)
	_, err = b.Build(Icons{Up: solid(2), Down: solid(3)})
	assert.ErrorContains(t, err, "missing status icon")

	teams := make([]string, 5000)
	for i := range teams {
		teams[i] = fmt.Sprintf("team%d", i)
	}
	b = NewBuilder(teams, []string{"web"})
	b.Set(1, 0, 0, true)
	_, err = b.Build(testIcons)
	assert.ErrorContains(t, err, "too large")

	// Each side fits in a GIF, but the area is over maxPixels.
	b = NewBuilder(teams[:300], teams[:300])
	b.Set(1, 0, 0, true)
	_, err = b.Build(testIcons)
	assert.ErrorContains(t, err, "too large")
}

func TestBuildShortensLongNames(t *testing.T) {
	long := strings.Repeat("x", 10000)
	tl, err := build([]string{long}, []string{long}, []frame{{id: 1, cells: []status{up}}})
	require.NoError(t, err)
	assert.Less(t, tl.width*tl.height, 200_000, "a long name must not blow up the image")
	assert.Equal(t, strings.Repeat("x", maxLabel-3)+"...", tl.services[0])
}

func TestBuildSizesTitleForLargestRound(t *testing.T) {
	// Rounds play in the order they arrive; the title must fit the widest ID.
	b := NewBuilder([]string{"a"}, []string{"b"})
	b.Set(1000000, 0, 0, true)
	b.Set(1, 0, 0, false)
	tl, err := b.Build(testIcons)
	require.NoError(t, err)
	titleW, _, _ := textSize(tl.titleFace, title(1000000))
	assert.GreaterOrEqual(t, tl.width, pad+titleW+pad)
}

func manyRounds(n int) *Timelapse {
	b := NewBuilder([]string{"team01"}, []string{"web"})
	for i := range n {
		b.Set(uint(i+1), 0, 0, i%2 == 0)
	}
	tl, err := b.Build(testIcons)
	if err != nil {
		panic(err)
	}
	return tl
}

func TestBuildSamplesLongEvents(t *testing.T) {
	tl := manyRounds(1000)
	require.Len(t, tl.rounds, maxFrames)
	assert.Equal(t, uint(1), tl.rounds[0].id, "starts at the first round")
	assert.Equal(t, uint(1000), tl.rounds[maxFrames-1].id, "ends at the last round")
	for i := 1; i < len(tl.rounds); i++ {
		gap := tl.rounds[i].id - tl.rounds[i-1].id
		assert.Contains(t, []uint{3, 4}, gap, "evenly spaced")
	}

	assert.Len(t, manyRounds(maxFrames).rounds, maxFrames, "short events keep every round")
}

type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, errors.New("client gone") }

func TestEncodeReportsWriterError(t *testing.T) {
	assert.ErrorContains(t, manyRounds(10).Encode(failingWriter{}), "client gone")
}

func TestEncodeProperties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringMatching(`[ -~]{1,40}`)
		teams := rapid.SliceOfNDistinct(name, 1, 5, rapid.ID[string]).Draw(t, "teams")
		services := rapid.SliceOfNDistinct(name, 1, 5, rapid.ID[string]).Draw(t, "services")
		ids := rapid.SliceOfNDistinct(rapid.UintRange(1, 100000), 1, 4, rapid.ID[uint]).Draw(t, "rounds")
		slices.Sort(ids)

		var frames, drawn []frame
		for _, id := range ids {
			cells := make([]status, len(teams)*len(services))
			for c := range cells {
				cells[c] = status(rapid.IntRange(0, 2).Draw(t, fmt.Sprintf("round %d cell %d", id, c)))
			}
			frames = append(frames, frame{id: id, cells: cells})
			if slices.ContainsFunc(cells, func(s status) bool { return s != unknown }) {
				drawn = append(drawn, frame{id: id, cells: cells})
			}
		}
		if len(drawn) == 0 {
			t.Skip("no results")
		}

		tl, data := encode(t, teams, services, frames)
		_, shown := play(t, data)
		if len(shown) != len(drawn) {
			t.Fatalf("%d frames for %d rounds with results", len(shown), len(drawn))
		}
		for i, f := range drawn {
			for row := range teams {
				for col := range services {
					want := statusColor[f.cells[row*len(services)+col]]
					if got := cellColor(shown[i], tl, row, col); got != want {
						t.Fatalf("round %d row %d col %d: got %d, want %d", f.id, row, col, got, want)
					}
				}
			}
		}

		// Labels and the title must fit: nothing is drawn on the outer edge.
		last := shown[len(shown)-1]
		b := last.Bounds()
		for x := b.Min.X; x < b.Max.X; x++ {
			for y := b.Min.Y; y < b.Max.Y; y++ {
				onEdge := x == b.Min.X || x == b.Max.X-1 || y == b.Min.Y || y == b.Max.Y-1
				if onEdge && last.ColorIndexAt(x, y) != white {
					t.Fatalf("pixel drawn on edge at %d,%d", x, y)
				}
			}
		}
	})
}
