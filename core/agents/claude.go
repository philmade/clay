package agents

import (
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// NewClaudeAgent creates a coding sub-agent with the given name prefix.
// The prefix ensures unique names when multiple instances exist in the same agent tree.
func NewClaudeAgent(llm model.LLM, tools []tool.Tool, namePrefix, instruction string) (agent.Agent, error) {
	name := "claude"
	if namePrefix != "" {
		name = namePrefix + "_claude"
	}
	return llmagent.New(llmagent.Config{
		Name:        name,
		Description: "Coding agent — edits files, runs bash, builds, deploys. For all coding and filesystem tasks.",
		Instruction: instruction,
		Model:       llm,
		Tools:       tools,
	})
}
