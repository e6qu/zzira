package store

import (
	"errors"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestReportGadgetsNormalizeTheirWindow(t *testing.T) {
	for _, key := range []string{"com.zzira:created-vs-resolved", "com.zzira:resolution-time", "com.zzira:velocity", "com.zzira:sprint-burndown"} {
		if !models.ReportGadget(key) {
			t.Fatalf("%s is not a report gadget", key)
		}
		found := false
		for _, definition := range models.GadgetCatalog() {
			found = found || definition.ModuleKey == key
		}
		if !found {
			t.Fatalf("%s is not in the catalog", key)
		}
	}
	if models.ReportGadget("com.zzira:pie-chart") {
		t.Fatal("pie chart reported as a report gadget")
	}
	config := models.GadgetConfig{ProjectKey: "ZZ"}
	if err := NormalizeGadgetConfig(&config); err != nil || config.Days != 30 {
		t.Fatalf("default window = %+v, %v", config, err)
	}
	bad := models.GadgetConfig{BoardID: "brd_default", Days: 14}
	if err := NormalizeGadgetConfig(&bad); !errors.Is(err, ErrDashboardValidation) {
		t.Fatalf("14 day window error = %v", err)
	}
}
