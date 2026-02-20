package executor

import (
	"fmt"
	"os/exec"
)

// ForceCalculate uses AppleScript to silently open the Excel file,
// force a full recalculation of all formulas, save the file, and close it.
// This is necessary because openpyxl does not evaluate Excel formulas,
// leaving them empty when read by our Go evaluator.
func ForceCalculate(filepath string) error {
	script := fmt.Sprintf(`
		tell application "Microsoft Excel"
			set myWorkbook to open workbook workbook file name (POSIX file "%s" as string)
			calculate full
			save myWorkbook
			close myWorkbook saving no
		end tell
	`, filepath)

	cmd := exec.Command("osascript", "-e", script)
	return cmd.Run()
}
