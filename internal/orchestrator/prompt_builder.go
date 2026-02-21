package orchestrator

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/rayx-hk/sheetagent/internal/agent"
	"github.com/rayx-hk/sheetagent/internal/sheet"
)

const maxCompressedChars = 300000 // ~90K tokens budget for sheet data

type PromptBuilder struct {
	parser     *sheet.ExcelizeParser
	compressor *sheet.Compressor
}

func NewPromptBuilder() *PromptBuilder {
	return &PromptBuilder{
		parser:     sheet.NewParser(),
		compressor: sheet.NewCompressor(),
	}
}

// BuildInput creates the CodeActInput for the first attempt.
func (p *PromptBuilder) BuildInput(ctx context.Context, instruction, answerPosition, instructionType, workDir, inputFile string) (agent.CodeActInput, error) {
	sheetDataList, err := p.parser.Parse(ctx, inputFile)
	if err != nil {
		return agent.CodeActInput{}, fmt.Errorf("parse input file: %w", err)
	}

	compressed := p.compressWithBudget(sheetDataList, answerPosition)

	return agent.CodeActInput{
		Instruction:     instruction,
		AnswerPosition:  answerPosition,
		InstructionType: instructionType,
		WorkDir:         workDir,
		InputFile:       inputFile,
		Compressed:      compressed,
		Attempt:         0,
	}, nil
}

// compressWithBudget tries LightOptions first, falls back to AggressiveOptions,
// and truncates as a last resort to stay within maxCompressedChars.
func (p *PromptBuilder) compressWithBudget(sheetDataList []sheet.SheetData, answerPosition string) string {
	compressed := p.compressSheets(sheetDataList, answerPosition, sheet.LightOptions)
	if len(compressed) <= maxCompressedChars {
		return compressed
	}

	slog.Info("compressed data exceeds budget, trying aggressive compression",
		"light_chars", len(compressed), "budget", maxCompressedChars)
	compressed = p.compressSheets(sheetDataList, answerPosition, sheet.AggressiveOptions)
	if len(compressed) <= maxCompressedChars {
		return compressed
	}

	slog.Warn("compressed data still exceeds budget after aggressive compression, truncating",
		"aggressive_chars", len(compressed), "budget", maxCompressedChars)
	return truncateCompressed(compressed, maxCompressedChars)
}

func (p *PromptBuilder) compressSheets(sheetDataList []sheet.SheetData, answerPosition string, opts sheet.CompressOptions) string {
	var compressed string
	for _, sd := range sheetDataList {
		compressed += p.compressor.CompressFull(&sd, opts) + "\n"
		if answerPosition != "" {
			compressed += p.compressor.CompressAnswerRegion(&sd, answerPosition) + "\n"
		}
	}
	return compressed
}

func truncateCompressed(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	keepHead := maxChars * 2 / 3
	keepTail := maxChars - keepHead - 100
	return s[:keepHead] +
		fmt.Sprintf("\n\n... [TRUNCATED: %d chars removed, sheet too large — use openpyxl to read the file directly] ...\n\n", len(s)-keepHead-keepTail) +
		s[len(s)-keepTail:]
}
