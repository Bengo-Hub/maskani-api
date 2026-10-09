package sales

import (
	"sort"
	"testing"
)

func TestNaturalLess(t *testing.T) {
	codes := []string{"A10", "a2", "B1", "A1", "A02B", "A2A", "10", "9", "Block 12", "Block 3"}
	sort.Slice(codes, func(i, j int) bool { return naturalLess(codes[i], codes[j]) })
	want := []string{"9", "10", "A1", "a2", "A2A", "A02B", "A10", "B1", "Block 3", "Block 12"}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("got %v, want %v", codes, want)
		}
	}
}

func TestValidSaleStatus(t *testing.T) {
	if !ValidSaleStatus("available") || ValidSaleStatus("bogus") {
		t.Fatal("sale status check is wrong")
	}
}
