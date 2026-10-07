package secure

import "testing"

func TestAccountMatchKey(t *testing.T) {
	cases := map[string]string{
		"B07": "B7", "b 07": "B7", "B-07": "B7", "B7": "B7", "b7 ": "B7",
		"S-B07": "SB7", "s b 07": "SB7", "A101": "A101", "A-0101": "A101", "007": "7", "B00": "B0",
	}
	for in, want := range cases {
		if got := AccountMatchKey(in); got != want {
			t.Errorf("AccountMatchKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"0712345678": "254712345678", "+254 712 345 678": "254712345678", "712345678": "254712345678",
		"254112345678": "254112345678", "12": "",
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
