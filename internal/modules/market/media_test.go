package market

import (
	"testing"

	"github.com/Bengo-Hub/httpware/objectstore"
)

func TestPublicMediaKind(t *testing.T) {
	cases := map[string]bool{
		"maskani/abc/properties/2026/10/x.jpg": true,
		"maskani/abc/units/2026/10/x.jpg":      true,
		"maskani/abc/documents/2026/10/x.pdf":  false,
		"maskani/abc/readings/2026/10/x.jpg":   false,
		"maskani/abc/properties":               false,
		"inventory/abc/units/2026/10/x.jpg":    false,
		"/tenants/abc/units/2026-10/x.jpg":     false,
	}
	for k, want := range cases {
		if got := PublicMediaKind(k); got != want {
			t.Errorf("%s: got %v want %v", k, got, want)
		}
	}

	for _, kind := range []string{"units", "documents"} {
		objectstore.Register(objectstore.MediaType{Name: kind, Public: kind == "units", MaxBytes: 1 << 20})
	}
	media, err := objectstore.New("maskani", objectstore.Config{Endpoint: "https://r2.example", AccessKeyID: "AK",
		SecretAccessKey: "SK", PublicBucket: "pub", PrivateBucket: "priv", PublicBaseURL: "https://media.example"})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{media: media}
	got := s.photoURLs([]string{"maskani/a/units/2026/10/x.jpg", "maskani/a/documents/2026/10/y.pdf", "https://elsewhere/z.jpg"})
	if len(got) != 1 || got[0] != "https://media.example/maskani/a/units/2026/10/x.jpg" {
		t.Fatalf("photoURLs: %v", got)
	}
}
