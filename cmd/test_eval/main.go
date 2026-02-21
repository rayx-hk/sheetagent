package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rayx-hk/sheetagent/internal/sheet"
)

func main() {
	path := "/Users/razil/Desktop/razil/dev/opensource/sheetagent/output/run_20260220_12_bench400_claude-opus-4-6/50088_1/1_50088_init.xlsx"

	parser := sheet.NewParser()
	sheets, err := parser.Parse(context.Background(), path)
	if err != nil {
		fmt.Println("Parse err:", err)
		os.Exit(1)
	}

	for _, s := range sheets {
		for _, c := range s.Cells {
			if c.Col == 6 && c.Row >= 6 && c.Row <= 8 {
				fmt.Printf("Parsed R%dC%d: %q (type: %s)\n", c.Row, c.Col, c.Value, c.Type)
			}
		}
	}
}
