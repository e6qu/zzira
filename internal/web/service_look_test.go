package web

import (
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestServiceLookFor(t *testing.T) {
	if look := serviceLookFor(models.ServiceHelpCenter{}); look != nil {
		t.Fatalf("an unbranded help center = %+v", look)
	}
	look := serviceLookFor(models.ServiceHelpCenter{BannerColour: "#0052CC", NavigationBackgroundColour: "#172B4D", NavigationTextColour: "#FFFFFF"})
	if look == nil || *look != (serviceLookView{NavigationBackground: "#172B4D", NavigationText: "#FFFFFF", Banner: "#0052CC", BannerText: "#FFFFFF", Accent: "#0052CC"}) {
		t.Fatalf("readable branding = %+v", look)
	}
	// Pairs whose text would be hard to read are not applied.
	pale := serviceLookFor(models.ServiceHelpCenter{BannerColour: "#FFEB3B", BannerTextColour: "#FFFFFF", NavigationBackgroundColour: "#DEEBFF", NavigationTextColour: "#FFFFFF"})
	if pale != nil {
		t.Fatalf("unreadable branding = %+v", pale)
	}
	// A pale banner with dark text is readable, but too pale for links and buttons on white.
	dark := serviceLookFor(models.ServiceHelpCenter{BannerColour: "#FFEB3B", BannerTextColour: "#172B4D"})
	if dark == nil || dark.Banner != "#FFEB3B" || dark.BannerText != "#172B4D" || dark.Accent != "" {
		t.Fatalf("pale banner branding = %+v", dark)
	}
	if contrast := colourContrast("#000000", "#FFFFFF"); contrast < 20.9 || contrast > 21.1 {
		t.Fatalf("black on white contrast = %.2f", contrast)
	}
}
