package main

import (
	"fmt"
	"os"
	"os/exec"
)

func main() {
	ok := true
	checks := []struct {
		name string
		cmd  string
		args []string
	}{
		{"Go", "go", []string{"version"}},
		{"Python3", "python3", []string{"--version"}},
		{"Docker", "docker", []string{"version", "--format", "{{.Client.Version}}"}},
	}
	for _, c := range checks {
		out, err := exec.Command(c.cmd, c.args...).CombinedOutput()
		if err != nil {
			fmt.Printf("  FAIL  %s: %v\n", c.name, err)
			ok = false
		} else {
			fmt.Printf("  OK    %s: %s", c.name, out)
		}
	}
	envs := []string{"ANTHROPIC_API_KEY", "OPENROUTER_API_KEY"}
	for _, e := range envs {
		if os.Getenv(e) == "" {
			fmt.Printf("  WARN  env %s not set\n", e)
		} else {
			fmt.Printf("  OK    env %s\n", e)
		}
	}
	if !ok {
		os.Exit(1)
	}
	fmt.Println("\nAll pre-checks passed.")
}
