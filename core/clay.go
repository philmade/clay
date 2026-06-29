package core

import (
	"embed"
	"fmt"
	"iter"
	"log"
	"os"
	"strconv"
	"strings"
	"text/template"
	"time"

	"clay/core/tools"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// ---------------------------------------------------------------------------
// Prompt loading
// ---------------------------------------------------------------------------

//go:embed prompts/*.md
var promptFS embed.FS

// promptVars are the variables available in prompt templates.
type promptVars struct {
	HandoffDir string
}

// loadPrompt reads a prompt template from the embedded filesystem and
// executes it with the given variables. Templates use Go text/template
// syntax: {{.HandoffDir}}, etc.
func loadPrompt(name string, vars promptVars) string {
	data, err := promptFS.ReadFile("prompts/" + name)
	if err != nil {
		log.Fatalf("missing prompt file prompts/%s: %v", name, err)
	}
	tmpl, err := template.New(name).Parse(string(data))
	if err != nil {
		log.Fatalf("bad template in prompts/%s: %v", name, err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, vars); err != nil {
		log.Fatalf("template exec prompts/%s: %v", name, err)
	}
	return buf.String()
}

// ---------------------------------------------------------------------------
// Clay agent construction
// ---------------------------------------------------------------------------

// opsDir returns the standard ops handoff directory.
// MANUAL.md and FEEDBACK.md live here.
func opsDir() string {
	root := os.Getenv("CLAY_ROOT")
	if root == "" {
		root = "."
	}
	return root + "/data/ops"
}

// BuildClayAgent creates the "clay" autonomous agent with build and ops lifecycle.
//
//	"clay" (LLMAgent — lifecycle orchestrator)
//	├── "build_loop" (resilient loop — construction)
//	│   ├── generator      (full tools + claude/research)
//	│   ├── build_reviewer (memory/soul/tasks)
//	│   └── build_control  (escalates on LOOP_DONE)
//	└── "ops_loop" (resilient loop — operation)
//	    ├── operator       (bash/research/memory — runs things)
//	    ├── ops_reviewer   (memory/soul/tasks)
//	    └── ops_control    (escalates on LOOP_DONE)
func BuildClayAgent(res *SharedResources, cfg OrchestratorConfig) (agent.Agent, error) {
	maxIter := uint(0)
	if v := os.Getenv("CLAW_MAX_ITERATIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			maxIter = uint(n)
		}
	}

	handoffDir := opsDir()
	pv := promptVars{HandoffDir: handoffDir}

	// ===== BUILD LOOP =====

	// Generator: full tools + own sub-agents (for construction work)
	genSubAgents, err := buildSubAgentsWithPrefix(res.Model, res.MemTool, "build")
	if err != nil {
		return nil, fmt.Errorf("generator sub-agents: %w", err)
	}
	genSubAgents = append(genSubAgents, cfg.ExtensionAgents...)

	genTools, err := buildCoordinatorTools(res.MemTool, res.Soul, res.TaskTool)
	if err != nil {
		return nil, fmt.Errorf("generator tools: %w", err)
	}
	genTools = append(genTools, cfg.ExtensionTools...)

	generator, err := llmagent.New(llmagent.Config{
		Name:        "generator",
		Description: "Generator — builds things: writes code, creates files, sets up systems.",
		Instruction: loadPrompt("generator.md", pv),
		Model:       res.Model,
		Tools:       genTools,
		SubAgents:   genSubAgents,
		OutputKey:   "build_output",
	})
	if err != nil {
		return nil, fmt.Errorf("generator: %w", err)
	}

	buildRevTools, err := buildLightTools(res)
	if err != nil {
		return nil, fmt.Errorf("build reviewer tools: %w", err)
	}
	buildReviewer, err := llmagent.New(llmagent.Config{
		Name:        "build_reviewer",
		Description: "Build reviewer — evaluates construction progress, directs next build steps.",
		Instruction: loadPrompt("build_reviewer.md", pv),
		Model:       res.Model,
		Tools:       buildRevTools,
		OutputKey:   "build_review",
	})
	if err != nil {
		return nil, fmt.Errorf("build reviewer: %w", err)
	}

	buildLoop, err := newResilientLoop("build_loop",
		"Build loop — generator-reviewer construction cycle.",
		"build_review", maxIter,
		generator, buildReviewer)
	if err != nil {
		return nil, fmt.Errorf("build loop: %w", err)
	}

	// ===== OPS LOOP =====

	// Operator: lighter tools — runs things, monitors, doesn't build
	opsSubAgents, err := buildSubAgentsWithPrefix(res.Model, res.MemTool, "ops")
	if err != nil {
		return nil, fmt.Errorf("operator sub-agents: %w", err)
	}

	opsTools, err := buildLightTools(res)
	if err != nil {
		return nil, fmt.Errorf("operator tools: %w", err)
	}

	operator, err := llmagent.New(llmagent.Config{
		Name:        "operator",
		Description: "Operator — runs systems, monitors output, gathers data, reports results.",
		Instruction: loadPrompt("operator.md", pv),
		Model:       res.Model,
		Tools:       opsTools,
		SubAgents:   opsSubAgents,
		OutputKey:   "ops_output",
	})
	if err != nil {
		return nil, fmt.Errorf("operator: %w", err)
	}

	opsRevTools, err := buildLightTools(res)
	if err != nil {
		return nil, fmt.Errorf("ops reviewer tools: %w", err)
	}
	opsReviewer, err := llmagent.New(llmagent.Config{
		Name:        "ops_reviewer",
		Description: "Ops reviewer — evaluates operational health, directs next operational steps.",
		Instruction: loadPrompt("ops_reviewer.md", pv),
		Model:       res.Model,
		Tools:       opsRevTools,
		OutputKey:   "ops_review",
	})
	if err != nil {
		return nil, fmt.Errorf("ops reviewer: %w", err)
	}

	opsLoop, err := newResilientLoop("ops_loop",
		"Ops loop — operator-reviewer operational cycle.",
		"ops_review", maxIter,
		operator, opsReviewer)
	if err != nil {
		return nil, fmt.Errorf("ops loop: %w", err)
	}

	// ===== RESEARCH LOOP =====

	researchTools, err := tools.NewResearchTools()
	if err != nil {
		return nil, fmt.Errorf("research tools: %w", err)
	}
	researchMemTool, err := tools.NewConsolidatedMemoryTool(res.MemTool)
	if err != nil {
		return nil, fmt.Errorf("researcher memory tool: %w", err)
	}
	researcherTools := append(researchTools, researchMemTool)

	researcher, err := llmagent.New(llmagent.Config{
		Name:        "researcher",
		Description: "Researcher — searches the web and fetches URLs to gather information.",
		Instruction: loadPrompt("researcher.md", pv),
		Model:       res.Model,
		Tools:       researcherTools,
		OutputKey:   "research_output",
	})
	if err != nil {
		return nil, fmt.Errorf("researcher: %w", err)
	}

	researchRevTools, err := buildLightTools(res)
	if err != nil {
		return nil, fmt.Errorf("research reviewer tools: %w", err)
	}
	researchReviewer, err := llmagent.New(llmagent.Config{
		Name:        "research_reviewer",
		Description: "Research reviewer — evaluates research findings, directs follow-up searches.",
		Instruction: loadPrompt("research_reviewer.md", pv),
		Model:       res.Model,
		Tools:       researchRevTools,
		OutputKey:   "research_review",
	})
	if err != nil {
		return nil, fmt.Errorf("research reviewer: %w", err)
	}

	researchLoop, err := newResilientLoop("research_loop",
		"Research loop — researcher-reviewer information gathering cycle.",
		"research_review", maxIter,
		researcher, researchReviewer)
	if err != nil {
		return nil, fmt.Errorf("research loop: %w", err)
	}

	// ===== CLAY ORCHESTRATOR =====

	orchTools, err := buildLightTools(res)
	if err != nil {
		return nil, fmt.Errorf("clay orchestrator tools: %w", err)
	}

	// Orchestrator gets read-only filesystem tools for inspection
	orchFSTools, err := tools.NewOrchestratorTools()
	if err != nil {
		return nil, fmt.Errorf("orchestrator fs tools: %w", err)
	}
	orchTools = append(orchTools, orchFSTools...)

	// Orchestrator delegates all creation/modification to loops
	orchSubAgents := []agent.Agent{buildLoop, opsLoop, researchLoop}

	return llmagent.New(llmagent.Config{
		Name:        "clay",
		Description: "Autonomous clay agent — orchestrates build and ops lifecycle.",
		Instruction: loadPrompt("orchestrator.md", pv),
		Model:       res.Model,
		Tools:       orchTools,
		SubAgents:   orchSubAgents,
	})
}

// ---------------------------------------------------------------------------
// Resilient loop — custom loop agent with retry logic
// ---------------------------------------------------------------------------

const maxRetries = 3

// newResilientLoop creates a loop agent that runs executor → reviewer → control
// in sequence, retrying sub-agents on error instead of killing the stream.
func newResilientLoop(name, description, reviewerStateKey string, maxIter uint, executor, reviewer agent.Agent) (agent.Agent, error) {
	controlName := name + "_control"

	loopControl, err := newLoopControl(controlName, reviewerStateKey)
	if err != nil {
		return nil, err
	}

	return agent.New(agent.Config{
		Name:        name,
		Description: description,
		SubAgents:   []agent.Agent{executor, reviewer, loopControl},
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				remaining := maxIter
				iteration := 0
				for {
					iteration++
					if maxIter > 0 {
						if remaining == 0 {
							log.Printf("%s: max iterations (%d) reached", name, maxIter)
							return
						}
						remaining--
					}

					log.Printf("%s: iteration %d", name, iteration)
					shouldExit := false

					for _, sub := range ctx.Agent().SubAgents() {
						success := false
						for attempt := 1; attempt <= maxRetries; attempt++ {
							errored := false
							for event, err := range sub.Run(ctx) {
								if err != nil {
									log.Printf("%s: %s error (attempt %d/%d): %v",
										name, sub.Name(), attempt, maxRetries, err)
									errored = true
									break
								}
								// Swallow escalation events — use them as a signal to
								// stop the loop but do NOT propagate Escalate to the parent.
								// If we yield Escalate=true, ADK terminates the parent
								// LLMAgent too, preventing the orchestrator from continuing.
								if event.Actions.Escalate {
									log.Printf("%s: escalation event from %s (swallowed, not propagated)", name, sub.Name())
									shouldExit = true
									continue
								}
								if !yield(event, nil) {
									return
								}
							}
							if !errored {
								success = true
								break
							}
							if attempt < maxRetries {
								backoff := time.Duration(attempt*2) * time.Second
								log.Printf("%s: retrying %s in %s", name, sub.Name(), backoff)
								time.Sleep(backoff)
							}
						}
						if !success {
							log.Printf("%s: %s failed after %d attempts, skipping",
								name, sub.Name(), maxRetries)
						}
						if shouldExit {
							log.Printf("%s: escalation received, exiting loop", name)
							return
						}
					}
				}
			}
		},
	})
}

// newLoopControl creates a custom agent that reads the reviewer's state key
// and escalates when LOOP_DONE or LOOP_PAUSE is detected.
func newLoopControl(name, reviewerStateKey string) (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        name,
		Description: "Reads reviewer output and escalates to end the loop when appropriate.",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				output, err := ctx.Session().State().Get(reviewerStateKey)
				if err != nil {
					return
				}
				text, ok := output.(string)
				if !ok {
					return
				}

				upper := strings.ToUpper(text)
				if strings.Contains(upper, "LOOP_DONE") || strings.Contains(upper, "LOOP_PAUSE") {
					signal := "LOOP_DONE"
					if strings.Contains(upper, "LOOP_PAUSE") {
						signal = "LOOP_PAUSE"
					}
					log.Printf("%s: escalating (%s)", name, signal)

					evt := session.NewEvent(ctx.InvocationID())
					evt.Author = name
					evt.Branch = ctx.Branch()
					evt.LLMResponse = adkmodel.LLMResponse{
						Content: &genai.Content{
							Role: genai.RoleModel,
							Parts: []*genai.Part{genai.NewPartFromText(
								fmt.Sprintf("Loop complete (%s). Returning to orchestrator.", signal),
							)},
						},
					}
					evt.Actions.Escalate = true
					yield(evt, nil)
				}
			}
		},
	})
}

// ---------------------------------------------------------------------------
// Tool sets
// ---------------------------------------------------------------------------

// buildLightTools creates memory + soul + tasks — used by reviewers, operator, orchestrator.
func buildLightTools(res *SharedResources) ([]tool.Tool, error) {
	var out []tool.Tool

	memoryTool, err := tools.NewConsolidatedMemoryTool(res.MemTool)
	if err != nil {
		return nil, err
	}
	out = append(out, memoryTool)

	soulTool, err := tools.NewConsolidatedSoulTool(res.Soul)
	if err != nil {
		return nil, err
	}
	out = append(out, soulTool)

	tasksTool, err := tools.NewConsolidatedTaskTool(res.TaskTool)
	if err != nil {
		return nil, err
	}
	out = append(out, tasksTool)

	return out, nil
}
