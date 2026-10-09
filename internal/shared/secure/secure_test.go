package secure

import "testing"

// TestNormalizePhone pins the stored digits form: phone hashes and treasury customer keys were built
// on it, so every valid number must normalise exactly as before.
func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"0712345678": "254712345678", "+254 712 345 678": "254712345678", "712345678": "254712345678",
		"254112345678": "254112345678", "0110 123 456": "254110123456", "+44 7911 123456": "447911123456",
		"12": "", "": "", "not a phone": "",
	}
	for in, want := range cases {
		if got := NormalizePhone(in); got != want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEncryptRoundTrip(t *testing.T) {
	b, err := NewBox("test-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := b.Encrypt("A001234567Z")
	if err != nil || enc == "" || enc == "A001234567Z" {
		t.Fatalf("encrypt: %v %q", err, enc)
	}
	dec, err := b.Decrypt(enc)
	if err != nil || dec != "A001234567Z" {
		t.Fatalf("decrypt: %v %q", err, dec)
	}
	if b.Hash("x") != b.Hash("x") || b.Hash("x") == b.Hash("y") {
		t.Fatal("hash not deterministic or collides")
	}
}
