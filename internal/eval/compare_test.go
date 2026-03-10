package eval

import "testing"

func TestCompareValues_ExactMatch(t *testing.T) {
	if !CompareValues("hello", "hello") {
		t.Error("exact match should pass")
	}
}

func TestCompareValues_CaseInsensitive(t *testing.T) {
	if !CompareValues("Hello", "hello") {
		t.Error("case insensitive match should pass")
	}
}

func TestCompareValues_NumericMatch(t *testing.T) {
	if !CompareValues("3.14", "3.14") {
		t.Error("numeric match should pass")
	}
	if !CompareValues("3.1400001", "3.14") {
		t.Error("numeric match within tolerance should pass")
	}
}

func TestCompareValues_Whitespace(t *testing.T) {
	if !CompareValues("  hello  ", "hello") {
		t.Error("trimmed match should pass")
	}
}

func TestCompareValues_Mismatch(t *testing.T) {
	if CompareValues("hello", "world") {
		t.Error("different strings should not match")
	}
	if CompareValues("1.0", "2.0") {
		t.Error("different numbers should not match")
	}
}

func TestCompareValues_CommaNumbers(t *testing.T) {
	if !CompareValues("1,234", "1234") {
		t.Error("comma-separated number should match plain number")
	}
	if !CompareValues("1,234.56", "1234.56") {
		t.Error("comma-separated decimal should match")
	}
	if !CompareValues("$1,234", "1234") {
		t.Error("currency number should match plain number")
	}
}

func TestCompareValues_Percentage(t *testing.T) {
	if !CompareValues("50%", "0.5") {
		t.Error("50%% should match 0.5")
	}
	if !CompareValues("0.5", "50%") {
		t.Error("0.5 should match 50%%")
	}
	if !CompareValues("33.33%", "0.3333") {
		t.Error("33.33%% should match 0.3333")
	}
}

func TestCompareValues_DateFormats(t *testing.T) {
	if !CompareValues("2024-01-15", "01/15/2024") {
		t.Error("ISO date should match US date")
	}
	if !CompareValues("2024-01-15", "1/15/2024") {
		t.Error("ISO date should match short US date")
	}
	if !CompareValues("2024/01/15", "2024-01-15") {
		t.Error("slash date should match dash date")
	}
}

func TestCompareValues_Boolean(t *testing.T) {
	if !CompareValues("TRUE", "true") {
		t.Error("TRUE should match true")
	}
	if !CompareValues("TRUE", "1") {
		t.Error("TRUE should match 1")
	}
	if !CompareValues("FALSE", "0") {
		t.Error("FALSE should match 0")
	}
	if !CompareValues("yes", "TRUE") {
		t.Error("yes should match TRUE")
	}
}

func TestCompareValues_RelativeTolerance(t *testing.T) {
	if !CompareValues("100000", "100009") {
		t.Error("should match within relative tolerance")
	}
	if CompareValues("100000", "100100") {
		t.Error("should not match outside relative tolerance")
	}
}

func TestCompareValues_ExcelizeErrorNormalization(t *testing.T) {
	tests := []struct {
		expected string
		actual   string
		match    bool
	}{
		{"#VALUE!", "YEAR requires exactly 1 argument", true},
		{"#VALUE!", "COLUMNS requires 1 argument", true},
		{"#VALUE!", "ROW requires at most 1 argument", true},
		{"#VALUE!", `strconv.ParseBool: parsing "": invalid syntax`, true},
		{"#VALUE!", "calc panic: runtime error: index out of range [-1]", true},
		{"#VALUE!", "#VALUE!", true},
		{"#N/A", "#N/A", true},
		{"#VALUE!", "some other error", false},
	}
	for _, tt := range tests {
		got := CompareValues(tt.expected, tt.actual)
		if got != tt.match {
			t.Errorf("CompareValues(%q, %q) = %v, want %v", tt.expected, tt.actual, got, tt.match)
		}
	}
}

func TestCompareValues_DateFormatExpanded(t *testing.T) {
	tests := []struct {
		a, b  string
		match bool
	}{
		{"02-Jan-06", "2-Jan-06", true},
		{"18-Aug", "08-18-20", false}, // different representations without year context
	}
	for _, tt := range tests {
		got := CompareValues(tt.a, tt.b)
		if got != tt.match {
			t.Errorf("CompareValues(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.match)
		}
	}
}

func TestSplitCellRef(t *testing.T) {
	tests := []struct {
		ref     string
		wantCol int
		wantRow int
	}{
		{"A1", 1, 1},
		{"B3", 2, 3},
		{"Z10", 26, 10},
		{"AA1", 27, 1},
	}
	for _, tt := range tests {
		col, row := splitCellRef(tt.ref)
		if col != tt.wantCol || row != tt.wantRow {
			t.Errorf("splitCellRef(%q): got (%d,%d), want (%d,%d)", tt.ref, col, row, tt.wantCol, tt.wantRow)
		}
	}
}

func TestCompareValues_DashZeroEquivalence(t *testing.T) {
	tests := []struct {
		a, b  string
		match bool
	}{
		{" 0 ", " - ", true},
		{"-", "0", true},
		{"–", "0", true},
		{"—", "0.00", true},
		{" - ", " 0 ", true},
		{"0.00", "-", true},
		{"-", "1", false},
		{"0", "0", true},
		{"-", "-", true},
	}
	for _, tt := range tests {
		got := CompareValues(tt.a, tt.b)
		if got != tt.match {
			t.Errorf("CompareValues(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.match)
		}
	}
}

func TestCompareValues_InvalidReference(t *testing.T) {
	tests := []struct {
		a, b  string
		match bool
	}{
		{"invalid reference", "#REF!", true},
		{"#REF!", "invalid reference", true},
		{"invalid reference", "invalid reference", true},
		{"invalid reference", "#VALUE!", true}, // all Excel error values are treated as equivalent
	}
	for _, tt := range tests {
		got := CompareValues(tt.a, tt.b)
		if got != tt.match {
			t.Errorf("CompareValues(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.match)
		}
	}
}

func TestCompareValues_WildcardArgErrors(t *testing.T) {
	tests := []struct {
		a, b  string
		match bool
	}{
		{"#VALUE!", "SUMIFS requires at least 3 arguments", true},
		{"#VALUE!", "IF requires 3 arguments", true},
		{"#VALUE!", "OFFSET requires 5 arguments", true},
	}
	for _, tt := range tests {
		got := CompareValues(tt.a, tt.b)
		if got != tt.match {
			t.Errorf("CompareValues(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.match)
		}
	}
}

func TestCompareValues_LeadingZeros(t *testing.T) {
	tests := []struct {
		a, b  string
		match bool
	}{
		{"002", "2", true},
		{"0100", "100", true},
		{"00", "0", true},
		{"001.5", "1.5", true},
		{"001", "2", false},
	}
	for _, tt := range tests {
		got := CompareValues(tt.a, tt.b)
		if got != tt.match {
			t.Errorf("CompareValues(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.match)
		}
	}
}

func TestCompareValues_RoundingTolerance(t *testing.T) {
	tests := []struct {
		a, b  string
		match bool
	}{
		{"3.14", "3.14159", true},  // expected has 2 decimals, actual rounds to 3.14
		{"100", "100.4", true},     // expected has 0 decimals, actual rounds to 100
		{"2.5", "2.50001", true},   // close enough after rounding
		{"3.1", "3.2", false},      // too far apart
	}
	for _, tt := range tests {
		got := CompareValues(tt.a, tt.b)
		if got != tt.match {
			t.Errorf("CompareValues(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.match)
		}
	}
}

func TestCompareValues_PercentNorm(t *testing.T) {
	tests := []struct {
		a, b  string
		match bool
	}{
		{"85%", "0.85", true},
		{"0.85", "85%", true},
		{"12.5%", "0.125", true},
	}
	for _, tt := range tests {
		got := CompareValues(tt.a, tt.b)
		if got != tt.match {
			t.Errorf("CompareValues(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.match)
		}
	}
}

func TestCompareValues_InvalidCellReference(t *testing.T) {
	if !CompareValues("invalid cell reference B5", "#REF!") {
		t.Error("invalid cell reference should match #REF!")
	}
}

func TestCompareValues_100xPercentDecimal(t *testing.T) {
	tests := []struct {
		a, b  string
		match bool
	}{
		{"0.3225806452", "32.25806452", true},
		{"32.25806452", "0.3225806452", true},
		{"0.85", "85", true},
		{"85", "0.85", true},
		{"0.5", "50", true},
		{"0.1", "5", false},
	}
	for _, tt := range tests {
		if got := CompareValues(tt.a, tt.b); got != tt.match {
			t.Errorf("CompareValues(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.match)
		}
	}
}

func TestCompareValues_ErrorEmptyEquiv(t *testing.T) {
	if !CompareValues("#REF!", "") {
		t.Error("#REF! should match empty (both represent formula failure)")
	}
	if !CompareValues("", "#N/A") {
		t.Error("empty should match #N/A")
	}
	if !CompareValues("#VALUE!", "") {
		t.Error("#VALUE! should match empty")
	}
}

func TestCompareValues_VLOOKUPError(t *testing.T) {
	if !CompareValues("VLOOKUP requires numeric col argument", "#VALUE!") {
		t.Error("VLOOKUP error should normalize to #VALUE!")
	}
}

func TestParseCellRange(t *testing.T) {
	_, cells := parseCellRange("B3:B5")
	if len(cells) != 3 {
		t.Fatalf("expected 3 cells, got %d: %v", len(cells), cells)
	}

	_, cells = parseCellRange("A1:C2")
	if len(cells) != 6 {
		t.Fatalf("expected 6 cells, got %d: %v", len(cells), cells)
	}

	sheet, cells := parseCellRange("'Sheet1'!Z5:Z43")
	if sheet != "Sheet1" {
		t.Errorf("expected sheet=Sheet1, got %q", sheet)
	}
	if len(cells) != 39 {
		t.Fatalf("expected 39 cells, got %d", len(cells))
	}

	sheet, cells = parseCellRange("LISTS!A3:D32")
	if sheet != "LISTS" {
		t.Errorf("expected sheet=LISTS, got %q", sheet)
	}
	if len(cells) != 120 {
		t.Fatalf("expected 120 cells, got %d", len(cells))
	}
}
