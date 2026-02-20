package orchestrator

import (
	"context"
	"fmt"

	"github.com/rayx-hk/dataagent/internal/agent"
	"github.com/rayx-hk/dataagent/internal/sheet"
)

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
	// 1. Parse sheet data
	sheetDataList, err := p.parser.Parse(ctx, inputFile)
	if err != nil {
		return agent.CodeActInput{}, fmt.Errorf("parse input file: %w", err)
	}

	// 2. Compress sheet structure
	// For simplicity, we just compress the first sheet or concatenate them.
	var compressed string
	for _, sd := range sheetDataList {
		compressed += p.compressor.CompressFull(&sd, sheet.LightOptions) + "\n"
		
		// If this sheet matches answerPosition (roughly), append answer region context
		// (A more robust check would parse answerPosition's sheet name)
		if answerPosition != "" {
			compressed += p.compressor.CompressAnswerRegion(&sd, answerPosition) + "\n"
		}
	}

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
