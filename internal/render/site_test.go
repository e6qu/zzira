package render

import "testing"

type brandedPage struct{ look SiteLook }

func (p brandedPage) SiteLook() SiteLook { return p.look }

func TestSiteLookFallsBackToZZIRA(t *testing.T) {
	if look := siteLook(struct{}{}); look != DefaultSiteLook {
		t.Fatalf("page without a site = %+v", look)
	}
	branded := SiteLook{Title: "Acme Service", LogoURL: "/static/acme.svg"}
	if look := siteLook(brandedPage{look: branded}); look != branded {
		t.Fatalf("branded page = %+v", look)
	}
}
