package eval

import (
	"testing"
)

func TestOJJudge_New(t *testing.T) {
	j := NewOJJudge()
	if j == nil {
		t.Fatal("expected non-nil judge")
	}
	if j.parser == nil {
		t.Fatal("expected non-nil parser")
	}
}
