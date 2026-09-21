package render

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// minimumBoxTarget is the smallest a tick box or radio may be drawn. WCAG 2.2
// asks for a 24-pixel target, or a smaller one with 24 pixels of undisturbed
// space around it. This size plus the gaps the forms use clears that, while a
// browser's own 16-pixel box clears it only on some machines: the service
// forms passed the accessibility sweep here for three runs and failed it on
// Linux, where the same boxes sat 20.6 pixels apart.
const minimumBoxTarget = 20

var (
	boxSelector = regexp.MustCompile(`input\[type=(?:"|')?(?:checkbox|radio)(?:"|')?\]`)
	boxSize     = regexp.MustCompile(`(?:^|;|\{)\s*(width|height)\s*:\s*([0-9.]+)px`)
	cssBlock    = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
)

// TestStylesheetsDrawTickBoxesBigEnough reads the rule rather than the render:
// a browser sweep only sees the boxes a journey happens to open, and only as
// the machine running it draws them.
func TestStylesheetsDrawTickBoxesBigEnough(t *testing.T) {
	sheets, err := filepath.Glob(filepath.Join("..", "..", "web", "static", "css", "*.css"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sheets) == 0 {
		t.Fatal("no stylesheets found")
	}
	for _, sheet := range sheets {
		content, err := os.ReadFile(sheet) // #nosec G304 -- a stylesheet of this repository, named by the glob above
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range cssBlock.FindAllStringSubmatch(string(content), -1) {
			selector, body := strings.TrimSpace(block[1]), block[2]
			if !boxSelector.MatchString(selector) {
				continue
			}
			for _, size := range boxSize.FindAllStringSubmatch(body, -1) {
				pixels, err := strconv.ParseFloat(size[2], 64)
				if err != nil {
					t.Fatalf("%s: %s: %v", filepath.Base(sheet), selector, err)
				}
				if pixels < minimumBoxTarget {
					t.Errorf("%s: %s draws a %s of %spx; a tick box is at least %dpx",
						filepath.Base(sheet), selector, size[1], size[2], minimumBoxTarget)
				}
			}
		}
	}
	if t.Failed() {
		t.Log(fmt.Sprintf("read %d stylesheets", len(sheets)))
	}
}
