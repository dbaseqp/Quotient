package main

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"time"

	"git.sr.ht/~sbinet/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font/gofont/goregular"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
	"gonum.org/v1/plot/vg/vgimg"
)

const (
	iconWidth  = 50
	iconHeight = 50
	fontSize   = 16
)

var (
	// lcg state
	lcg int
)

type GraphConfig struct {
	Color color.Color
	Light float64
}

func makeGraphs() error {
	lightmode := GraphConfig{Color: color.Black, Light: 0.45}
	darkmode := GraphConfig{Color: color.White, Light: 0.45}

	dataOverTime, err := dbGetChecks()
	if err != nil {
		return err
	}

	dataThisRound, err := dbGetChecksThisRound(roundNumber - 1)
	if err != nil {
		return err
	}

	graphScoresOverTime(dataOverTime, "plots/points-over-time-light.png", lightmode)
	graphScoresOverTime(dataOverTime, "plots/points-over-time-dark.png", darkmode)
	graphCurrentChecks(dataThisRound, "plots/current-status-light.png", lightmode)
	graphCurrentChecks(dataThisRound, "plots/current-status-dark.png", darkmode)

	return nil
}

func graphCurrentChecks(data map[uint][]CheckData, path string, config GraphConfig) error {
	W := 945
	H := 454
	// const W = 50
	// const H = 50

	if len(data) == 0 {
		return nil
	}

	upIcon, err := gg.LoadPNG("assets/services/up.png")
	if err != nil {
		errorPrint(err)
	}
	downIcon, err := gg.LoadPNG("assets/services/down.png")
	if err != nil {
		errorPrint(err)
	}
	uiw, uih := upIcon.Bounds().Dx(), upIcon.Bounds().Dy()
	// diw, dih := upIcon.Bounds().Dx(), downIcon.Bounds().Dy()
	teams, err := dbGetTeams()
	if err != nil {
		return err
	}

	xoffset := int(iconWidth * 1.3)
	yoffset := int(iconHeight * 1.3)
	scaleX := iconWidth / float64(uiw)
	scaleY := iconHeight / float64(uih)

	dc := gg.NewContext(W, H)
	font, err := truetype.Parse(goregular.TTF)
	if err != nil {
		panic("")
	}
	face := truetype.NewFace(font, &truetype.Options{
		Size: fontSize,
	})
	dc.SetFontFace(face)

	var maxTeamNameLength float64
	maxTeamNameLength = 0
	for _, team := range teams {
		if w, _ := dc.MeasureString(team.Name); w > maxTeamNameLength {
			maxTeamNameLength = w
		}
	}

	// get keys and sort them so that rendering is deterministic
	dc.Push()
	dc.Rotate(gg.Degrees(30))
	var maxServiceNameHeight float64
	maxServiceNameHeight = 0
	var services []string
	for _, check := range data[teams[0].ID] {
		services = append(services, check.ServiceName)
		if w, h := dc.MeasureString(check.ServiceName); (w*math.Sin(math.Pi/float64(6)))+(h*math.Cos(math.Pi/float64(6))) > maxServiceNameHeight {
			maxServiceNameHeight = (w * math.Sin(math.Pi/float64(6))) + (h * math.Cos(math.Pi/float64(6)))
		}
	}

	if int(maxServiceNameHeight)+(len(teams)*yoffset) > H {
		H = int(maxServiceNameHeight) + (len(teams) * yoffset) + fontSize + (yoffset * 2)
	}
	if int(maxTeamNameLength)+(len(services)*xoffset) > W {
		W = int(maxTeamNameLength) + (len(services) * xoffset)
	}
	dc = gg.NewContext(W, H)

	dc.SetFontFace(face)
	dc.SetColor(config.Color)
	dc.Translate(maxTeamNameLength, maxServiceNameHeight)

	dc.Push()
	dc.Translate(float64(xoffset), 0)
	face = truetype.NewFace(font, &truetype.Options{
		Size: fontSize * 2,
	})
	dc.SetFontFace(face)
	dc.DrawStringAnchored(fmt.Sprint("Service Status: ", time.Now().In(loc).Format("2006-01-02 15:04:05")), 0, 0, 0, 0)
	dc.Pop()
	dc.Translate(0, float64(yoffset*2))

	dc.Push()
	dc.Translate(float64(xoffset/2), 0)
	for _, serviceName := range services {
		dc.Push()
		dc.Rotate(gg.Radians(30))
		dc.DrawStringAnchored(serviceName, 0, 0, 1, 0.5)
		dc.Pop()
		dc.Translate(float64(xoffset), 0)
	}
	dc.Pop()
	dc.Translate(0, fontSize/2)

	for _, team := range teams {
		dc.Push()
		dc.DrawStringAnchored(team.Name, 0, float64(yoffset/2), 1, 0.5)
		for _, check := range data[team.ID] {
			dc.Push()
			dc.DrawRectangle(0, 0, float64(xoffset), float64(yoffset))
			dc.Stroke()
			dc.Translate(float64(xoffset/2), 0)
			dc.Scale(scaleX, scaleY)
			if check.Result == true {
				dc.DrawImageAnchored(upIcon, 0, (yoffset / 2), 0.5, 0)
			} else {
				dc.DrawImageAnchored(downIcon, 0, (yoffset / 2), 0.5, 0)
			}
			dc.Pop()
			dc.Translate(float64(xoffset), 0)
		}
		dc.Pop()
		dc.Translate(0, float64(yoffset))
	}

	dc.SavePNG(path)
	return nil
}

func graphScoresOverTime(data map[uint][]RoundPointsData, path string, config GraphConfig) error {
	plot := plot.New()
	plot.X.Label.Text = "Round"
	plot.X.Width = 2

	plot.X.Label.TextStyle.Color = config.Color
	plot.X.Color = config.Color
	plot.X.Tick.Color = config.Color
	plot.X.Tick.Label.Color = config.Color
	plot.Y.Label.TextStyle.Color = config.Color
	plot.Y.Color = config.Color
	plot.Y.Tick.Color = config.Color
	plot.Y.Tick.Label.Color = config.Color
	plot.Legend.TextStyle.Color = config.Color
	plot.Y.Label.Text = "Score"
	plot.Y.Width = 2
	plot.BackgroundColor = color.Transparent
	plot.BackgroundColor = color.Transparent

	// graphData := make([]interface{}, len(data)*2)

	// randomize colors for teams
	teams, err := dbGetTeams()
	if err != nil {
		return err
	}
	offset := 1.0 / float64(len(data))
	colors := map[uint]RGB{}
	lcg = 37
	h := 0.0
	for _, team := range teams {
		h += offset
		colors[team.ID] = HSL{
			H: h,
			S: 0.9 + (float64(LCG()%20)-10)/100.0,
			L: config.Light + (float64(LCG()%20)-10)/100.0,
		}.ToRGB()
	}

	for _, team := range teams {
		line, _ := plotter.NewLine(getTeamPlotPoints(data[team.ID]))
		line.LineStyle.Width = vg.Points(2)
		line.LineStyle.Color = color.RGBA{
			R: uint8(float64(0xff) * colors[team.ID].R),
			G: uint8(float64(0xff) * colors[team.ID].G),
			B: uint8(float64(0xff) * colors[team.ID].B),
			A: 255}
		plot.Add(line)
		plot.Legend.Add(team.Name, line)
	}

	canvas := vgimg.PngCanvas{Canvas: vgimg.NewWith(
		vgimg.UseWH(25*vg.Centimeter, 12*vg.Centimeter),
		vgimg.UseBackgroundColor(color.Transparent),
	)}
	plot.Draw(draw.New(canvas))

	// Save the plot to a png file
	f, err := os.Create(path)
	if err != nil {
		errorPrint(err)
		return err
	}
	defer f.Close()

	_, err = canvas.WriteTo(f)
	if err != nil {
		errorPrint(err)
		return err
	}
	return nil
}

func getTeamPlotPoints(records []RoundPointsData) plotter.XYs {
	plotPoints := make(plotter.XYs, len(records))
	var sum int
	for i := range plotPoints {
		sum += records[i].PointsThisRound
		plotPoints[i].X = float64(records[i].RoundID)
		plotPoints[i].Y = float64(sum)
	}
	return plotPoints
}

// ZX81 <3. Range of 0-100
func LCG() int {
	lcg = (75*lcg + 74) % 101
	return lcg
}

/*
	RGB/HSL code from https://github.com/gerow/go-color/blob/master/color.go
*/

type RGB struct {
	R, G, B float64
}

type HSL struct {
	H, S, L float64
}

func hueToRGB(v1, v2, h float64) float64 {
	if h < 0 {
		h += 1
	}
	if h > 1 {
		h -= 1
	}
	switch {
	case 6*h < 1:
		return (v1 + (v2-v1)*6*h)
	case 2*h < 1:
		return v2
	case 3*h < 2:
		return v1 + (v2-v1)*((2.0/3.0)-h)*6
	}
	return v1
}

func (c HSL) ToRGB() RGB {
	h := c.H
	s := c.S
	l := c.L

	if s == 0 {
		// it's gray
		return RGB{l, l, l}
	}

	var v1, v2 float64
	if l < 0.5 {
		v2 = l * (1 + s)
	} else {
		v2 = (l + s) - (s * l)
	}

	v1 = 2*l - v2

	r := hueToRGB(v1, v2, h+(1.0/3.0))
	g := hueToRGB(v1, v2, h)
	b := hueToRGB(v1, v2, h-(1.0/3.0))

	return RGB{r, g, b}
}

func writeFile(buf []byte, filename string) error {
	tmpPath := "plots/"
	err := os.MkdirAll(tmpPath, 0700)
	if err != nil {
		return err
	}

	file := filepath.Join(tmpPath, filename)
	err = os.WriteFile(file, buf, 0600)
	if err != nil {
		return err
	}
	return nil
}
