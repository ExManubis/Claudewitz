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

## Features

**Chat**
- Streaming responses with Markdown rendering, plus thinking blocks
- Tool calls inline with status and collapsible output (`ctrl+o`)
- Edits and writes shown as syntax-highlighted diffs with line numbers
- Interrupt with `esc`

**Sidebar** (`ctrl+b`, auto-hidden below 100 columns)
- Session title, directory, model and account type
- Context size and subscription usage meters (5-hour and weekly, with time to reset)
- Todo list (from `TodoWrite` / `TaskCreate` / `TaskUpdate`)
- Live sub-agent activity: type, task, tool count, tokens, elapsed time and their latest
  tool calls, also mirrored under the `Agent` call in the chat
- Modified files with added/removed line counts

**Sessions** (`ctrl+r` or `/resume`)
- Browse sessions for this project or all projects (`tab`), filter as you type
- Resuming replays the transcript and switches to the session's directory

**Autocompletion**
- `/` commands, including every command, skill and plugin Claude Code reports
- Arguments for `/model` (from the CLI's model list), `/mode` and `/theme`
- `@` file mentions, fuzzy-matched against `git ls-files` (or a directory walk)

**Permissions**
- Allow once, always allow a tool for the session, deny, or deny and stop
- Edit/Write requests preview the diff; sub-agent requests name the agent asking
- Permission modes: `shift+tab` cycles default → accept edits → plan

**Themes**: `claude` (default), `charm`, `mono`, via `-theme` or `/theme`, each with light and
dark variants.

## How it talks to Claude Code

- Starts `claude -p` in stream-json mode with `--permission-prompt-tool stdio` and
  `--forward-subagent-text`
- Sends an `initialize` control request on startup to get commands, models and account info
- Renders `stream_event` deltas live and takes tool calls and results from complete messages
- Follows sub-agents through `task_started` / `task_progress` / `task_updated` /
  `task_notification` events and messages tagged with `parent_tool_use_id`
- Reads history from `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`
- Removes `CLAUDECODE` / `CLAUDE_CODE_SESSION_ID` from the child's environment so that running
  claudewitz inside Claude Code doesn't pin every child to the parent session

## Roadmap ideas

- Drill into a sub-agent's full transcript
- Image paste, and an external editor for long prompts
- Multiple concurrent sessions in tabs
- Persisted per-project "always allow" rules
