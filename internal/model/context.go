package model

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	eimodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const (
	DefaultMaxContextChars = 80000 // ~20K tokens — aggressively compact to minimize token spend
	minKeepRounds          = 2
	truncatedToolMaxChars  = 150
)

// ContextManagedModel wraps a ToolCallingChatModel and proactively prunes
// the message history when it exceeds a character budget. It also reactively
// prunes on 400/413 errors (payload too large).
type ContextManagedModel struct {
	inner    eimodel.ToolCallingChatModel
	maxChars int
}

func NewContextManagedModel(inner eimodel.ToolCallingChatModel, maxChars int) *ContextManagedModel {
	if maxChars <= 0 {
		maxChars = DefaultMaxContextChars
	}
	return &ContextManagedModel{inner: inner, maxChars: maxChars}
}

func (m *ContextManagedModel) Generate(ctx context.Context, input []*schema.Message, opts ...eimodel.Option) (*schema.Message, error) {
	pruned := m.ensureBudget(input)
	msg, err := m.inner.Generate(ctx, pruned, opts...)
	if err != nil && isPayloadError(err) {
		aggressive := aggressivePrune(pruned)
		if len(aggressive) < len(pruned) {
			slog.Warn("reactive context prune after API error",
				"original", len(input), "first_prune", len(pruned), "aggressive", len(aggressive))
			return m.inner.Generate(ctx, aggressive, opts...)
		}
	}
	return msg, err
}

func (m *ContextManagedModel) Stream(ctx context.Context, input []*schema.Message, opts ...eimodel.Option) (*schema.StreamReader[*schema.Message], error) {
	pruned := m.ensureBudget(input)
	reader, err := m.inner.Stream(ctx, pruned, opts...)
	if err != nil && isPayloadError(err) {
		aggressive := aggressivePrune(pruned)
		if len(aggressive) < len(pruned) {
			slog.Warn("reactive context prune (stream) after API error",
				"original", len(input), "first_prune", len(pruned), "aggressive", len(aggressive))
			return m.inner.Stream(ctx, aggressive, opts...)
		}
	}
	return reader, err
}

func (m *ContextManagedModel) WithTools(tools []*schema.ToolInfo) (eimodel.ToolCallingChatModel, error) {
	newInner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &ContextManagedModel{inner: newInner, maxChars: m.maxChars}, nil
}

func (m *ContextManagedModel) ensureBudget(msgs []*schema.Message) []*schema.Message {
	total := estimateMessageChars(msgs)
	if total <= m.maxChars {
		return msgs
	}
	slog.Info("context exceeds budget, pruning",
		"chars", total, "max_chars", m.maxChars, "messages", len(msgs))
	return pruneMessages(msgs, m.maxChars)
}

// conversationRound groups an assistant message with its corresponding tool results.
type conversationRound struct {
	messages []*schema.Message
}

func estimateMessageChars(msgs []*schema.Message) int {
	total := 0
	for _, m := range msgs {
		total += len(m.Content)
		for _, tc := range m.ToolCalls {
			total += len(tc.Function.Arguments)
		}
	}
	return total
}

func roundChars(r conversationRound) int {
	return estimateMessageChars(r.messages)
}

// groupConversation splits messages into head (system/user preamble) and rounds
// (assistant + tool result pairs). This preserves the pairing required by the API.
func groupConversation(msgs []*schema.Message) (head []*schema.Message, rounds []conversationRound) {
	i := 0
	for i < len(msgs) && msgs[i].Role != schema.Assistant {
		head = append(head, msgs[i])
		i++
	}

	for i < len(msgs) {
		if msgs[i].Role == schema.Assistant {
			round := conversationRound{messages: []*schema.Message{msgs[i]}}
			i++
			for i < len(msgs) && msgs[i].Role == schema.Tool {
				round.messages = append(round.messages, msgs[i])
				i++
			}
			rounds = append(rounds, round)
		} else {
			round := conversationRound{messages: []*schema.Message{msgs[i]}}
			i++
			rounds = append(rounds, round)
		}
	}
	return
}

func flattenRounds(head []*schema.Message, rounds []conversationRound) []*schema.Message {
	result := make([]*schema.Message, 0, len(head)+len(rounds)*3)
	result = append(result, head...)
	for _, r := range rounds {
		result = append(result, r.messages...)
	}
	return result
}

// pruneMessages reduces the message history to fit within maxChars.
// Strategy:
//  0. If only head messages and still over budget, truncate the largest message
//  1. Truncate tool results in older rounds (keep last N rounds untouched)
//  2. Drop oldest rounds entirely if still over budget
//  3. Last resort: keep only head + last round
func pruneMessages(msgs []*schema.Message, maxChars int) []*schema.Message {
	head, rounds := groupConversation(msgs)

	if len(rounds) == 0 {
		return truncateHeadMessages(head, maxChars)
	}

	keepLast := minKeepRounds
	if keepLast > len(rounds) {
		keepLast = len(rounds)
	}

	// Phase 1: truncate tool outputs in older rounds
	truncated := make([]conversationRound, len(rounds))
	for i, r := range rounds {
		isRecent := i >= len(rounds)-keepLast
		if isRecent {
			truncated[i] = r
			continue
		}
		newMsgs := make([]*schema.Message, len(r.messages))
		for j, m := range r.messages {
			if m.Role == schema.Tool && len(m.Content) > truncatedToolMaxChars {
				cp := *m
				half := truncatedToolMaxChars / 2
				cp.Content = m.Content[:half] +
					fmt.Sprintf("\n...[pruned %d chars]...\n", len(m.Content)-truncatedToolMaxChars) +
					m.Content[len(m.Content)-half:]
				newMsgs[j] = &cp
			} else {
				newMsgs[j] = m
			}
		}
		truncated[i] = conversationRound{messages: newMsgs}
	}

	result := flattenRounds(head, truncated)
	if estimateMessageChars(result) <= maxChars {
		slog.Info("context pruned via tool output truncation",
			"original_msgs", len(msgs), "pruned_msgs", len(result))
		return result
	}

	// Phase 2: drop oldest rounds entirely (keep last keepLast)
	for drop := 1; drop <= len(truncated)-keepLast; drop++ {
		remaining := truncated[drop:]
		result = flattenRounds(head, remaining)
		if estimateMessageChars(result) <= maxChars {
			slog.Info("context pruned via round dropping",
				"dropped_rounds", drop, "remaining_rounds", len(remaining))
			return result
		}
	}

	// Phase 3: keep only head + last round with truncated tool outputs
	lastRound := truncated[len(truncated)-1]
	result = make([]*schema.Message, 0, len(head)+len(lastRound.messages))
	result = append(result, head...)
	result = append(result, lastRound.messages...)
	slog.Warn("aggressive context prune — keeping only head + last round",
		"original_msgs", len(msgs), "pruned_msgs", len(result))
	return result
}

// aggressivePrune is the reactive fallback after a 400/413 API error.
func aggressivePrune(msgs []*schema.Message) []*schema.Message {
	head, rounds := groupConversation(msgs)
	if len(rounds) == 0 {
		return truncateHeadMessages(head, DefaultMaxContextChars*2/3)
	}
	if len(rounds) <= 1 && len(msgs) <= 3 {
		return truncateHeadMessages(msgs, DefaultMaxContextChars*2/3)
	}

	lastRound := rounds[len(rounds)-1]
	result := make([]*schema.Message, 0, len(head)+len(lastRound.messages))
	result = append(result, head...)
	result = append(result, lastRound.messages...)
	slog.Warn("reactive aggressive prune",
		"original_msgs", len(msgs), "pruned_msgs", len(result),
		"dropped_rounds", len(rounds)-1)
	return result
}

// truncateHeadMessages truncates the content of the largest message in head
// when the head itself exceeds the budget (e.g., a massive initial prompt).
func truncateHeadMessages(head []*schema.Message, maxChars int) []*schema.Message {
	total := estimateMessageChars(head)
	if total <= maxChars {
		return head
	}

	// Find the largest message to truncate (typically the user message with sheet data)
	largestIdx := 0
	largestLen := 0
	for i, m := range head {
		if len(m.Content) > largestLen {
			largestLen = len(m.Content)
			largestIdx = i
		}
	}

	excess := total - maxChars
	target := head[largestIdx]
	newLen := len(target.Content) - excess - 200 // extra buffer for truncation marker
	if newLen < 500 {
		newLen = 500
	}

	keepHead := newLen * 2 / 3
	keepTail := newLen - keepHead
	truncated := target.Content[:keepHead] +
		fmt.Sprintf("\n\n... [CONTEXT PRUNED: %d chars removed to fit context window] ...\n\n",
			len(target.Content)-keepHead-keepTail) +
		target.Content[len(target.Content)-keepTail:]

	cp := *target
	cp.Content = truncated
	result := make([]*schema.Message, len(head))
	copy(result, head)
	result[largestIdx] = &cp

	slog.Warn("head message truncated to fit budget",
		"original_chars", total, "max_chars", maxChars,
		"truncated_msg_idx", largestIdx, "original_len", largestLen, "new_len", len(truncated))
	return result
}

func isPayloadError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// E015 is a transient proxy error, not a payload size issue
	if strings.Contains(s, "E015") {
		return false
	}
	return strings.Contains(s, "400 Bad Request") ||
		strings.Contains(s, "413") ||
		strings.Contains(s, "Request Entity Too Large")
}
