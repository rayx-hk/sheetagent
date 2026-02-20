package sheet

import (
	"context"
	"strings"
	"testing"
)

func TestCompressor_CompressLight(t *testing.T) {
	p := NewParser()
	ctx := context.Background()

	sheets, err := p.Parse(ctx, testdataPath("simple.xlsx"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	c := NewCompressor()
	result := c.CompressFull(&sheets[0], LightOptions)
	if result == "" {
		t.Fatal("expected non-empty compressed output")
	}
	if !strings.Contains(result, "Sheet1") {
		t.Error("expected sheet name in output")
	}
	if !strings.Contains(result, "Headers:") {
		t.Error("expected headers in output")
	}
}

func TestCompressor_CompressAggressive(t *testing.T) {
	p := NewParser()
	ctx := context.Background()

	sheets, err := p.Parse(ctx, testdataPath("simple.xlsx"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	c := NewCompressor()
	result := c.CompressFull(&sheets[0], AggressiveOptions)
	if result == "" {
		t.Fatal("expected non-empty compressed output")
	}
	if !strings.Contains(result, "Sheet1") {
		t.Error("expected sheet name in output")
	}
}

func TestCompressor_CompressAnswerRegion(t *testing.T) {
	p := NewParser()
	ctx := context.Background()

	sheets, err := p.Parse(ctx, testdataPath("simple.xlsx"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	c := NewCompressor()
	result := c.CompressAnswerRegion(&sheets[0], "A1:B3")
	if result == "" {
		t.Fatal("expected non-empty answer region output")
	}
	if !strings.Contains(result, "Answer Region") {
		t.Error("expected answer region header in output")
	}
}
