package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/rayx-hk/dataagent/internal/eval"
)

func main() {
	b1, err := os.ReadFile("/Users/razil/Desktop/razil/dev/opensource/dataagent/reports/bench_400_20260220_102608.json")
	if err != nil { panic(err) }
	b2, err := os.ReadFile("/Users/razil/Desktop/razil/dev/opensource/dataagent/reports/bench_400_20260220_131406.json")
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
