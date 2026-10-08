package gate

import (
	"testing"

	"github.com/google/uuid"
)

func TestDeployedTo(t *testing.T) {
	p := uuid.New()
	if !deployedTo([]string{uuid.NewString(), p.String()}, p) {
		t.Fatal("personnel deployed to the property must pass")
	}
	if deployedTo(nil, p) || deployedTo([]string{uuid.NewString()}, p) {
		t.Fatal("personnel not deployed to the device's property must be rejected")
	}
}
