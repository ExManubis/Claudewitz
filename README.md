# claudewitz

A [Crush](https://github.com/charmbracelet/crush)-style terminal UI for **Claude Code**.

Instead of calling the Anthropic API directly (which bills per token), claudewitz drives the
`claude` CLI you already have installed. That means it runs on whatever you are logged in
with, including a Claude Pro/Max subscription, and you keep Claude Code's tools, MCP servers,
skills, hooks, `CLAUDE.md` and session history.

```
┌──────────────────────────── claudewitz (Go + Bubble Tea) ─────────────────────────────┐
│ chat viewport · input · permission dialogs · status bar (model, mode, ctx, 5h/7d usage) │
└──────────────┬──────────────────────────────────────────────────────▲───────────────────┘
   stdin: user messages, permission answers,             stdout: stream_event deltas,
   control requests (interrupt, set_model,               assistant/user messages, result,
   set_permission_mode)                                  rate_limit_event, can_use_tool
┌──────────────▼──────────────────────────────────────────────────────┴───────────────────┐
│ claude -p --input-format stream-json --output-format stream-json                        │
│        --include-partial-messages --permission-prompt-tool stdio                        │
└──────────────────────────────────────────────────────────────────────────────────────────┘
```

## Requirements

- Go 1.24+
- Claude Code CLI on your `PATH`, logged in (`npm i -g @anthropic-ai/claude-code`, then run `claude` once)

## Run

```sh
go run .                      # in the project you want to work on
go run . -model opus -mode acceptEdits
go run . -continue            # pick up the latest session in this directory
go run . -resume <session-id>
go run . -- --add-dir ../lib  # anything after -- goes straight to claude
```

## Features so far

- Streaming responses with Markdown rendering, plus thinking blocks
- Tool calls shown inline with status and collapsible output (`ctrl+o`)
- Permission prompts (allow once, always allow this tool for the session, deny), with
  Edit diffs and Write previews
- Permission modes (`shift+tab` cycles default → accept edits → plan)
- Interrupt with `esc`
- Subscription usage gauges (5-hour and 7-day windows, with time to reset), context size
- `/new`, `/resume`, `/model`, `/mode`, `/session`, `/help`; other slash commands pass through to Claude Code

## Roadmap ideas

- Session picker (read `~/.claude/projects/*/*.jsonl`) and sidebar with modified files
- Slash command / `@file` autocompletion using the `init` event's command list
- Sub-agent activity (`--forward-subagent-text`), todo list rendering
- Syntax-highlighted diffs, image paste, themes
- Multiple concurrent sessions in tabs
