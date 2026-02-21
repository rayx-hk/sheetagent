package model

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func makeMsg(role schema.RoleType, content string) *schema.Message {
	return &schema.Message{Role: role, Content: content}
}

func TestEstimateMessageChars(t *testing.T) {
	msgs := []*schema.Message{
		makeMsg(schema.User, "hello"),
		makeMsg(schema.Assistant, "world"),
	}
	got := estimateMessageChars(msgs)
	if got != 10 {
		t.Errorf("estimateMessageChars = %d, want 10", got)
	}
}

func TestGroupConversation(t *testing.T) {
	msgs := []*schema.Message{
		makeMsg(schema.User, "instruction"),
		makeMsg(schema.Assistant, "call tool"),
		makeMsg(schema.Tool, "result1"),
		makeMsg(schema.Assistant, "call tool2"),
		makeMsg(schema.Tool, "result2"),
		makeMsg(schema.Assistant, "final answer"),
	}

	head, rounds := groupConversation(msgs)
	if len(head) != 1 {
		t.Errorf("head length = %d, want 1", len(head))
	}
	if len(rounds) != 3 {
		t.Errorf("rounds = %d, want 3", len(rounds))
	}
	if len(rounds[0].messages) != 2 {
		t.Errorf("round 0 messages = %d, want 2", len(rounds[0].messages))
	}
	if len(rounds[2].messages) != 1 {
		t.Errorf("round 2 (final) messages = %d, want 1", len(rounds[2].messages))
	}
}

func TestPruneMessages_UnderBudget(t *testing.T) {
	msgs := []*schema.Message{
		makeMsg(schema.User, "short instruction"),
		makeMsg(schema.Assistant, "ok"),
		makeMsg(schema.Tool, "done"),
	}
	pruned := pruneMessages(msgs, 100000)
	if len(pruned) != len(msgs) {
		t.Errorf("should not prune when under budget: got %d, want %d", len(pruned), len(msgs))
	}
}

func TestPruneMessages_OverBudget_TruncatesOlderRounds(t *testing.T) {
	bigToolOutput := strings.Repeat("x", 5000)
	msgs := []*schema.Message{
		makeMsg(schema.User, "instruction"),
		makeMsg(schema.Assistant, "call1"),
		makeMsg(schema.Tool, bigToolOutput),
		makeMsg(schema.Assistant, "call2"),
		makeMsg(schema.Tool, bigToolOutput),
		makeMsg(schema.Assistant, "call3"),
		makeMsg(schema.Tool, bigToolOutput),
		makeMsg(schema.Assistant, "call4"),
		makeMsg(schema.Tool, "small"),
	}

	original := estimateMessageChars(msgs)
	pruned := pruneMessages(msgs, original-1000)
	prunedChars := estimateMessageChars(pruned)

	if prunedChars >= original {
		t.Errorf("pruned should be smaller: original=%d, pruned=%d", original, prunedChars)
	}
	if len(pruned) == 0 {
		t.Error("should not prune to empty")
	}
}

func TestPruneMessages_DropsOldRounds(t *testing.T) {
	bigContent := strings.Repeat("x", 10000)
	msgs := []*schema.Message{
		makeMsg(schema.User, "instruction"),
		makeMsg(schema.Assistant, bigContent),
		makeMsg(schema.Tool, bigContent),
		makeMsg(schema.Assistant, bigContent),
		makeMsg(schema.Tool, bigContent),
		makeMsg(schema.Assistant, "recent call"),
		makeMsg(schema.Tool, "recent result"),
		makeMsg(schema.Assistant, "latest call"),
		makeMsg(schema.Tool, "latest result"),
	}

	pruned := pruneMessages(msgs, 1000)
	if len(pruned) > len(msgs) {
		t.Errorf("pruned should not be longer than original")
	}
	last := pruned[len(pruned)-1]
	if last.Content != "latest result" {
		t.Errorf("last message should be preserved, got %q", last.Content)
	}
}

func TestAggressivePrune(t *testing.T) {
	msgs := []*schema.Message{
		makeMsg(schema.User, "instruction"),
		makeMsg(schema.Assistant, "call1"),
		makeMsg(schema.Tool, "result1"),
		makeMsg(schema.Assistant, "call2"),
		makeMsg(schema.Tool, "result2"),
		makeMsg(schema.Assistant, "call3"),
		makeMsg(schema.Tool, "result3"),
	}

	pruned := aggressivePrune(msgs)
	if len(pruned) > 3 {
		t.Errorf("aggressive prune should keep head + last round, got %d msgs", len(pruned))
	}
	if pruned[0].Content != "instruction" {
		t.Error("should preserve head")
	}
	if pruned[len(pruned)-1].Content != "result3" {
		t.Error("should preserve last round")
	}
}

func TestIsPayloadError(t *testing.T) {
	tests := []struct {
		errStr string
		want   bool
	}{
		{`POST "https://example.com": 400 Bad Request {"error":"too large"}`, true},
		{`413 Request Entity Too Large`, true},
		{`503 Service Unavailable`, false},
		{`connection timeout`, false},
		{`POST "https://proxy.example.com": 400 Bad Request {"error":{"code":"E015","message":"Internal server error"},"status":500}`, false},
	}
	for _, tt := range tests {
		err := &testError{msg: tt.errStr}
		if got := isPayloadError(err); got != tt.want {
			t.Errorf("isPayloadError(%q) = %v, want %v", tt.errStr, got, tt.want)
		}
	}
}

func TestTruncateHeadMessages(t *testing.T) {
	// Simulate a massive initial prompt (2M+ chars) with only 2 messages
	bigSheet := strings.Repeat("cell data | ", 200000) // ~2.4M chars
	msgs := []*schema.Message{
		makeMsg(schema.System, "You are a spreadsheet expert."),
		makeMsg(schema.User, "Fix this spreadsheet:\n"+bigSheet),
	}

	original := estimateMessageChars(msgs)
	if original < 2000000 {
		t.Fatalf("test setup: expected >2M chars, got %d", original)
	}

	result := truncateHeadMessages(msgs, 480000)
	truncatedSize := estimateMessageChars(result)

	if truncatedSize > 480000+500 {
		t.Errorf("truncated size %d exceeds budget 480000 (with margin)", truncatedSize)
	}
	if len(result) != 2 {
		t.Errorf("should preserve message count: got %d", len(result))
	}
	// System message should be unchanged
	if result[0].Content != msgs[0].Content {
		t.Error("system message should not be modified")
	}
	// User message should contain truncation marker
	if !strings.Contains(result[1].Content, "CONTEXT PRUNED") {
		t.Error("truncated message should contain pruning marker")
	}
}

func TestPruneMessages_HeadOnly_OverBudget(t *testing.T) {
	bigSheet := strings.Repeat("x", 1000000)
	msgs := []*schema.Message{
		makeMsg(schema.System, "system prompt"),
		makeMsg(schema.User, bigSheet),
	}

	pruned := pruneMessages(msgs, 100000)
	prunedChars := estimateMessageChars(pruned)

	if prunedChars > 100500 {
		t.Errorf("pruned chars %d should be near budget 100000", prunedChars)
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
