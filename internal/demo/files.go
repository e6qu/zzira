package demo

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"strings"
)

// demoFile is the content a demo attachment holds. A site with no files on
// any work item is a site where nobody can try the attachment list, the image
// preview or the download, so the demo writes real ones: an image that is a
// valid PNG, and text that reads like what someone would attach.
//
// The content comes from the name alone, so rebuilding the demo writes the
// same bytes and a file is only stored once.
func demoFile(name string) ([]byte, string, error) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		content, err := demoImage(name)
		return content, "image/png", err
	case ".log", ".txt":
		return []byte(demoLog(name)), "text/plain", nil
	case ".csv":
		return []byte(demoCSV(name)), "text/csv", nil
	case ".json":
		return []byte(demoJSON(name)), "application/json", nil
	default:
		return nil, "", fmt.Errorf("a demo attachment named %q has no content: use .png, .log, .txt, .csv or .json", name)
	}
}

// demoSeed is a name's own number, so two files with the same name look the
// same and two with different names do not.
func demoSeed(name string) uint32 {
	sum := fnv.New32a()
	_, _ = sum.Write([]byte(name))
	return sum.Sum32()
}

// demoImage draws a small chart: bars of one hue on a light background. It is
// not a screenshot of anything, and it is an image a browser renders.
func demoImage(name string) ([]byte, error) {
	const width, height = 320, 180
	seed := demoSeed(name)
	hue := color.RGBA{R: uint8(60 + seed%120), G: uint8(90 + (seed>>8)%100), B: uint8(140 + (seed>>16)%80), A: 255}
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	background := color.RGBA{R: 247, G: 248, B: 250, A: 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.Set(x, y, background)
		}
	}
	for bar := 0; bar < 8; bar++ {
		tall := 20 + int((seed>>(bar%16))%130)
		left := 14 + bar*38
		for y := height - 14 - tall; y < height-14; y++ {
			for x := left; x < left+26; x++ {
				canvas.Set(x, y, hue)
			}
		}
	}
	axis := color.RGBA{R: 180, G: 186, B: 196, A: 255}
	for x := 0; x < width; x++ {
		canvas.Set(x, height-14, axis)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func demoLog(name string) string {
	seed := demoSeed(name)
	levels := []string{"INFO", "INFO", "WARN", "INFO", "ERROR", "INFO"}
	messages := []string{
		"checkout.service started in 412ms",
		"cache warm: 1284 entries",
		"retrying payment authorisation (attempt 2)",
		"ledger.sync finished, 38 records",
		"upstream timed out after 30s",
		"request completed with 200",
	}
	var out strings.Builder
	for i := range levels {
		minute := (int(seed>>uint(i)) % 50) + i
		fmt.Fprintf(&out, "2026-03-%02dT%02d:%02d:%02dZ %-5s %s\n", 1+int(seed%27), 9+i, minute%60, (minute*7)%60, levels[i], messages[i])
	}
	return out.String()
}

func demoCSV(name string) string {
	seed := demoSeed(name)
	var out strings.Builder
	out.WriteString("week,raised,resolved,open\n")
	open := 20 + int(seed%30)
	for week := 1; week <= 8; week++ {
		raised := 10 + int((seed>>uint(week))%25)
		resolved := 8 + int((seed>>uint(week+3))%24)
		open += raised - resolved
		if open < 0 {
			open = 0
		}
		fmt.Fprintf(&out, "2026-W%02d,%d,%d,%d\n", week, raised, resolved, open)
	}
	return out.String()
}

func demoJSON(name string) string {
	seed := demoSeed(name)
	return fmt.Sprintf(`{
  "capturedAt": "2026-03-%02dT10:%02d:00Z",
  "release": "%d.%d.%d",
  "checks": [
    {"name": "smoke", "passed": true, "durationMs": %d},
    {"name": "contract", "passed": true, "durationMs": %d},
    {"name": "load", "passed": %t, "durationMs": %d}
  ]
}
`, 1+int(seed%27), int(seed%60), seed%5, (seed>>4)%12, (seed>>8)%20, 400+seed%900, 900+(seed>>3)%1200, seed%7 != 0, 5000+(seed>>5)%9000)
}
