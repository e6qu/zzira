package demo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/demo"
	"github.com/e6qu/zzira/internal/models"
)

// A gadget nobody has put on a dashboard is a gadget nobody has looked at. The
// company's dashboards are where a reader meets them, so the catalog and what
// the company shows are kept the same on purpose.
func TestShippedCompanyShowsEveryGadget(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "demo", "company.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scenario, err := demo.Read(file)
	if err != nil {
		t.Fatalf("read the shipped company: %v", err)
	}
	shown := map[string]bool{}
	for _, dashboard := range scenario.Dashboards {
		for _, gadget := range dashboard.Gadgets {
			shown[gadget.Type] = true
		}
	}
	missing := []string{}
	for _, gadget := range models.GadgetCatalog() {
		key := strings.TrimPrefix(gadget.ModuleKey, "com.zzira:")
		if !shown[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("the company's dashboards show none of these gadgets: %s", strings.Join(missing, ", "))
	}
}
