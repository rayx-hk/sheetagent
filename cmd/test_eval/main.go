package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rayx-hk/sheetagent/internal/sheet"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: test_eval <xlsx-path>")
		os.Exit(1)
	}
	path := os.Args[1]

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
