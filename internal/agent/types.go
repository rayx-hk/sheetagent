package agent

import "github.com/cloudwego/eino/schema"

type CodeActInput struct {
	Instruction      string `json:"instruction"`
	AnswerPosition   string `json:"answer_position"`
	InstructionType  string `json:"instruction_type"`
	WorkDir          string `json:"work_dir"`
	InputFile        string `json:"input_file"`
	Compressed       string `json:"compressed"` // Go SheetCompressor output
	PreviousError    string `json:"previous_error,omitempty"` // Go OJ Judge output/diff
	Attempt          int    `json:"attempt"`
	PromptAdditions  string `json:"prompt_additions,omitempty"` // SOP-injected: formula strategy, skills, failure warnings
}

type ExecResult struct {
	Code     string `json:"code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

type CodeActOutput struct {
	Success    bool              `json:"success"`
	OutputFile string            `json:"output_file,omitempty"`
	Code       string            `json:"code,omitempty"`
	Error      string            `json:"error,omitempty"`
	AgentTrace []*schema.Message `json:"agent_trace,omitempty"`
}
