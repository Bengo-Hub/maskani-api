package rbac

import "testing"

func TestRoleCodePattern(t *testing.T) {
	for _, ok := range []string{"night_guard", "estate_cashier2", "ab"} {
		if !roleCodePattern.MatchString(ok) {
			t.Errorf("%q should be a valid role code", ok)
		}
	}
	for _, bad := range []string{"", "a", "Night", "2nd_guard", "night guard", "x-y"} {
		if roleCodePattern.MatchString(bad) {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestUniqKeepsOrder(t *testing.T) {
	got := uniq([]string{"a", "b", "a", "c", "b"})
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("uniq: %v", got)
	}
}
