package market

import "testing"

func TestPublicMediaKind(t *testing.T) {
	cases := map[string]bool{
		"/tenants/abc/properties/2026-10/x.jpg": true,
		"tenants/abc/units/2026-10/x.jpg":       true,
		"/tenants/abc/documents/2026-10/x.pdf":  false,
		"/tenants/abc/readings/2026-10/x.jpg":   false,
		"/tenants/abc/properties":               false,
		"/other/abc/units/2026-10/x.jpg":        false,
	}
	for k, want := range cases {
		if got := PublicMediaKind(k); got != want {
			t.Errorf("%s: got %v want %v", k, got, want)
		}
	}
	s := &Service{mediaBase: "https://api.example"}
	got := s.photoURLs([]string{"tenants/a/units/2026-10/x.jpg", "tenants/a/documents/2026-10/y.pdf", "https://elsewhere/z.jpg"})
	if len(got) != 1 || got[0] != "https://api.example/media/tenants/a/units/2026-10/x.jpg" {
		t.Fatalf("photoURLs: %v", got)
	}
}
