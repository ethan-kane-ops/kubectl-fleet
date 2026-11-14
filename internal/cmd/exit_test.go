package cmd

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitErrorIsDetectable(t *testing.T) {
	var ee *ExitError
	if !errors.As(fmt.Errorf("wrap: %w", errAttention), &ee) || ee.Code != 2 {
		t.Fatalf("want an *ExitError with code 2, got %v", ee)
	}
}
