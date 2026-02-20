package executor

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// copyFile is a helper to copy a file from src to dst.
func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()

	_, err = io.Copy(d, s)
	return err
}

// ForceCalculate uses AppleScript to silently open the Excel file,
// force a full recalculation of all formulas, save the file, and close it.
//
// To bypass macOS Sandbox/TCC permission prompts (which pop up when Excel tries
// to open files in protected directories like Desktop/Documents), it temporarily
// copies the file to the globally accessible temp directory, runs the calculation
// there, and copies it back.
func ForceCalculate(targetPath string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get user home dir: %w", err)
	}
	
	// Use MS Office's shared group container on macOS.
	// This directory is explicitly whitelisted by the App Sandbox for Microsoft Office apps,
	// so Excel can open files here without triggering any TCC permission prompts.
	officeContainer := filepath.Join(homeDir, "Library", "Group Containers", "UBF8T346G9.Office")
	if err := os.MkdirAll(officeContainer, 0755); err != nil {
		return fmt.Errorf("create office container dir: %w", err)
	}

	// Create a temporary file in the explicitly accessible Office container directory
	tempFile, err := os.CreateTemp(officeContainer, "dataagent_excel_*.xlsx")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	tempFile.Close() // Close it immediately so we can overwrite/copy into it
	defer os.Remove(tempPath)

	// Copy the target file to the temp path
	if err := copyFile(targetPath, tempPath); err != nil {
		return fmt.Errorf("copy to temp: %w", err)
	}

	// Make sure the file has broad permissions
	os.Chmod(tempPath, 0666)

	// AppleScript has issues with symlinks, so resolve them to the true absolute path
	// e.g., /var is actually a symlink to /private/var on macOS
	resolvedPath, err := filepath.EvalSymlinks(tempPath)
	if err == nil {
		tempPath = resolvedPath
	}

	script := fmt.Sprintf(`
		tell application "Microsoft Excel"
			set myWorkbook to open workbook workbook file name (POSIX file "%s" as string)
			calculate full
			save myWorkbook
			close myWorkbook saving no
		end tell
	`, tempPath)

	cmd := exec.Command("osascript", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("osascript run failed: %w, output: %s", err, string(out))
	}

	// Copy the calculated file back to the original location
	if err := copyFile(tempPath, targetPath); err != nil {
		return fmt.Errorf("copy back from temp: %w", err)
	}

	return nil
}
