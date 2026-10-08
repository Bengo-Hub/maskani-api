package notices

import (
	"strings"
	"testing"
)

func TestActiveChannels(t *testing.T) {
	cases := map[string]struct {
		in   []string
		want string
	}{
		"default":         {nil, "whatsapp,email"},
		"sms becomes wa":  {[]string{"sms"}, "whatsapp"},
		"dedupe and case": {[]string{"Email", "email", "push", "WhatsApp"}, "email,whatsapp"},
	}
	for name, c := range cases {
		if got := strings.Join(activeChannels(c.in), ","); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

func TestOneLine(t *testing.T) {
	got := oneLine("Water off:\n\tTuesday  9am\n\nto 1pm")
	if got != "Water off: Tuesday 9am to 1pm" {
		t.Errorf("oneLine = %q", got)
	}
	if long := oneLine(strings.Repeat("a ", 1000)); len([]rune(long)) > maxNoticeParam {
		t.Errorf("oneLine not capped: %d", len([]rune(long)))
	}
	if firstName("") != "there" || firstName("Jane Wanjiru") != "Jane" {
		t.Error("firstName fallback wrong")
	}
}
