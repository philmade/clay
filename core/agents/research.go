package agents

import (
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// NewResearchAgent creates a web research sub-agent with the given name prefix.
func NewResearchAgent(llm model.LLM, tools []tool.Tool, namePrefix, instruction string) (agent.Agent, error) {
	name := "research"
	if namePrefix != "" {
		name = namePrefix + "_research"
	}
	return llmagent.New(llmagent.Config{
		Name:        name,
		Description: "Research agent — searches the web and fetches URLs via Chawan browser.",
		Instruction: instruction,
		Model:       llm,
		Tools:       tools,
	})
}
