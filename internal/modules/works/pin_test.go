package works

import "testing"

func TestValidGuardPIN(t *testing.T) {
	for pin, want := range map[string]bool{"1234": true, "123456": true, "123": false, "1234567": false, "12a4": false, "": false} {
		if ValidGuardPIN(pin) != want {
			t.Fatalf("ValidGuardPIN(%q) = %v, want %v", pin, !want, want)
		}
	}
}
