package proc

import (
	"os"
	"testing"
)

func TestStatStart(t *testing.T) {
	line := "4242 (we (ird) name) S 1 4242 4242 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 987654 1000 10"
	if got := statStart(line); got != "987654" {
		t.Fatalf("statStart = %q, want 987654", got)
	}
	if got := statStart("garbage"); got != "" {
		t.Fatalf("statStart(garbage) = %q", got)
	}
}

func TestStatPPID(t *testing.T) {
	line := "4242 (we (ird) name) S 17 4242 4242 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 987654 1000 10"
	if got := statPPID(line); got != 17 {
		t.Fatalf("statPPID = %d, want 17", got)
	}
	if got := statPPID("garbage"); got != 0 {
		t.Fatalf("statPPID(garbage) = %d", got)
	}
}

func TestAncestors(t *testing.T) {
	got := Ancestors(os.Getpid())
	if len(got) == 0 || got[0] != os.Getppid() {
		t.Fatalf("Ancestors = %v, want the parent %d first", got, os.Getppid())
	}
}
