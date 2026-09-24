package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/rishang/seek/agent"
	"github.com/rishang/seek/config"
	"github.com/rishang/seek/logx"
	"github.com/urfave/cli/v3"
)

func agentCmd() *cli.Command {
	return &cli.Command{
		Name:      "agent",
		Usage:     "Answer a question from the web (fast by default, --deep for deep research)",
		UsageText: "seek agent [--deep] [--reasoning-effort E] [--sources N] [--max-steps N] [--model M] [--extract-model M] [-o markdown|json] <question>",
		Description: "Default (shallow): one search, then one model call that answers from the\n" +
			"result snippets with citations. No pages are fetched.\n\n" +
			"--deep: deep research. The model drives seek's search and fetch as tools\n" +
			"over several turns; each fetched page is distilled into deduped facts by\n" +
			"extract_model before the model sees it. Then it answers with citations.\n\n" +
			"Needs an OpenAI Chat Completions–compatible endpoint in provider.yaml:\n\n" +
			"  agent:\n" +
			"    base_url: https://api.openai.com/v1\n" +
			"    api_key: sk-...          # or SEEK_AGENT_API_KEY\n" +
			"    model: gpt-5             # synthesis\n" +
			"    extract_model: gpt-5-nano # --deep page extraction (defaults to model)\n" +
			"    reasoning_effort: medium  # optional; sent with model's requests only\n" +
			"    max_steps: 12             # optional; --deep turn cap",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "deep", Usage: "Deep research: let the model search and fetch over multiple turns"},
			&cli.StringFlag{Name: "reasoning-effort", Aliases: []string{"e"}, Usage: "Reasoning effort for the main model, e.g. low, medium, high (overrides agent.reasoning_effort)"},
			&cli.IntFlag{Name: "sources", Value: agent.DefaultSources, Usage: "Search results to answer from without --deep (overrides agent.sources)"},
			&cli.IntFlag{Name: "max-steps", Value: agent.DefaultMaxSteps, Usage: "Model turns with --deep before a forced answer (overrides agent.max_steps)"},
			&cli.StringFlag{Name: "model", Usage: "Synthesis model (overrides agent.model)"},
			&cli.StringFlag{Name: "extract-model", Usage: "Extraction model (overrides agent.extract_model)"},
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Value: "markdown", Usage: "Output format: markdown, json"},
			noCacheFlag,
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() < 1 {
				return fmt.Errorf("question required")
			}
			question := cmd.Args().First()
			applyNoCache(cmd)

			ac, err := config.LoadAgent(providersPath())
			if err != nil {
				return fmt.Errorf("read agent config: %w", err)
			}
			if key := os.Getenv("SEEK_AGENT_API_KEY"); key != "" {
				ac.APIKey = key
			}
			if cmd.IsSet("model") {
				ac.Model = cmd.String("model")
			}
			if cmd.IsSet("extract-model") {
				ac.ExtractModel = cmd.String("extract-model")
			}
			if cmd.IsSet("reasoning-effort") {
				ac.ReasoningEffort = cmd.String("reasoning-effort")
			}
			if cmd.IsSet("sources") {
				ac.Sources = int(cmd.Int("sources"))
			}
			if cmd.IsSet("max-steps") {
				ac.MaxSteps = int(cmd.Int("max-steps"))
			}
			if ac.BaseURL == "" || ac.Model == "" {
				return fmt.Errorf("agent needs base_url and model under `agent:` in %s", providersPath())
			}
			logx.Debug("agent: base_url=%s model=%s extract_model=%s reasoning_effort=%s deep=%t",
				ac.BaseURL, ac.Model, ac.ExtractModel, ac.ReasoningEffort, cmd.Bool("deep"))

			client := agent.NewClient(ac.BaseURL, ac.APIKey)
			client.Trace = logx.Debug
			deps := agent.Deps{
				Search: func(ctx context.Context, q string) ([]config.SearchResult, error) {
					return opSearch(ctx, "", q, config.SearchOptions{})
				},
				Fetch: func(ctx context.Context, url string) (string, error) {
					r, err := opFetch(ctx, "", url, string(config.FormatMarkdown))
					if err != nil {
						return "", err
					}
					return r.Content, nil
				},
				Complete: tracedComplete(client),
				Trace:    logx.Debug,
			}
			opts := agent.Options{
				Model:           ac.Model,
				ExtractModel:    ac.ExtractModel,
				ReasoningEffort: ac.ReasoningEffort,
				Sources:         ac.Sources,
				MaxSteps:        ac.MaxSteps,
			}
			run := agent.Run
			if cmd.Bool("deep") {
				run = agent.Research
			}
			res, err := run(ctx, deps, opts, question)
			if res != nil {
				for _, s := range res.Skipped {
					logx.Warn("agent: skipped %s: %s", s.URL, s.Err)
				}
			}
			if err != nil {
				return err
			}
			logx.Debug("agent: %d source(s), %d fact(s), %d skipped", len(res.Sources), len(res.Facts), len(res.Skipped))
			for _, s := range res.Sources {
				logx.Debug("agent: source [%d] %s  %s", s.ID, s.URL, s.Title)
			}

			if cmd.String("output") == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}
			fmt.Print(res.Markdown())
			return nil
		},
	}
}

// tracedComplete wraps the LLM client with a debug line per call: model,
// request shape, latency, reply shape and token usage.
func tracedComplete(c *agent.Client) func(context.Context, agent.Request) (agent.Message, error) {
	return func(ctx context.Context, req agent.Request) (agent.Message, error) {
		start := time.Now()
		msg, err := c.Complete(ctx, req)
		took := time.Since(start).Round(time.Millisecond)
		if err != nil {
			logx.Debug("llm: %s failed after %s: %v", req.Model, took, err)
			return msg, err
		}
		tokens := "n/a"
		if msg.Usage != nil {
			tokens = fmt.Sprintf("%d in / %d out", msg.Usage.PromptTokens, msg.Usage.CompletionTokens)
		}
		logx.Debug("llm: %s msgs=%d tools=%d effort=%q json=%t → %s, tool_calls=%d, text=%d chars, tokens %s",
			req.Model, len(req.Messages), len(req.Tools), req.ReasoningEffort, req.ResponseFormat != nil,
			took, len(msg.ToolCalls), len(msg.Content), tokens)
		return msg, nil
	}
}
