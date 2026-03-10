package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/rayx-hk/sheetagent/internal/eval"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: test_compare <report1.json> <report2.json>")
		os.Exit(1)
	}

	b1, err := os.ReadFile(os.Args[1])
	if err != nil { panic(err) }
	b2, err := os.ReadFile(os.Args[2])
	if err != nil { panic(err) }

	var r1, r2 eval.BenchReport
	json.Unmarshal(b1, &r1)
	json.Unmarshal(b2, &r2)

	failed1 := make(map[string]bool)
	for _, f := range r1.Failures {
		failed1[f.TaskID] = true
	}

	for _, f := range r2.Failures {
		if !failed1[f.TaskID] {
			fmt.Printf("Regression (Failed in new, passed in old): %s (Type: %s)\n  Reason: %s\n\n", f.TaskID, f.InstructionType, f.Reason)
		}
	}
}
