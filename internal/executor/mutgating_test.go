package executor

import (
	"strings"
	"testing"
)

func TestValidateMutationGating(t *testing.T) {
	tests := []struct {
		name           string
		changeLog      interface{}
		answerPosition string
		wantErr        bool
	}{
		{
			name: "pass - exact match",
			changeLog: map[string]interface{}{
				"changes": []interface{}{
					map[string]interface{}{"sheet": "Sheet1", "range": "B12:B15", "action": "write_value"},
				},
			},
			answerPosition: "B12:B15",
			wantErr:        false,
		},
		{
			name: "pass - overlapping range",
			changeLog: map[string]interface{}{
				"changes": []interface{}{
					map[string]interface{}{"sheet": "Sheet1", "range": "B10:B20", "action": "write_value"},
				},
			},
			answerPosition: "B12:B15",
			wantErr:        false,
		},
		{
			name: "fail - wrong range",
			changeLog: map[string]interface{}{
				"changes": []interface{}{
					map[string]interface{}{"sheet": "Sheet1", "range": "A1:A10", "action": "write_value"},
				},
			},
			answerPosition: "B12:B15",
			wantErr:        true,
		},
		{
			name: "fail - wrong column",
			changeLog: map[string]interface{}{
				"changes": []interface{}{
					map[string]interface{}{"sheet": "Sheet1", "range": "C12:C15", "action": "write_value"},
				},
			},
			answerPosition: "B12:B15",
			wantErr:        true,
		},
		{
			name: "pass - multiple changes one overlaps",
			changeLog: map[string]interface{}{
				"changes": []interface{}{
					map[string]interface{}{"sheet": "Sheet1", "range": "A1:A5", "action": "write_value"},
					map[string]interface{}{"sheet": "Sheet1", "range": "B12:B15", "action": "write_value"},
				},
			},
			answerPosition: "B12:B15",
			wantErr:        false,
		},
		{
			name: "pass - with sheet",
			changeLog: map[string]interface{}{
				"changes": []interface{}{
					map[string]interface{}{"sheet": "Sheet2", "range": "B3:B14", "action": "write_value"},
				},
			},
			answerPosition: "Sheet2!B3:B14",
			wantErr:        false,
		},
		{
			name:           "pass - empty answer_position",
			changeLog:      map[string]interface{}{"changes": []interface{}{}},
			answerPosition: "",
			wantErr:        false,
		},
		{
			name:           "fail - no change log",
			changeLog:      nil,
			answerPosition: "B12:B15",
			wantErr:        true,
		},
		{
			name: "fail - empty changes",
			changeLog: map[string]interface{}{
				"changes": []interface{}{},
			},
			answerPosition: "B12:B15",
			wantErr:        true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errMsg := ValidateMutationGating(tt.changeLog, tt.answerPosition)
			gotErr := errMsg != ""
			if gotErr != tt.wantErr {
				t.Errorf("ValidateMutationGating() gotErr = %v, want %v; errMsg = %q", gotErr, tt.wantErr, errMsg)
			}
			if tt.wantErr && errMsg != "" {
			if !strings.Contains(errMsg, "Mutation Error") {
				t.Errorf("expected 'Mutation Error' in message, got %q", errMsg)
			}
			if !strings.Contains(errMsg, tt.answerPosition) {
				t.Errorf("expected answer_position in error, got %q", errMsg)
			}
			}
		})
	}
}

