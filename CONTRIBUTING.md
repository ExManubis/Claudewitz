# Contributing

## Building from source

Requires Go 1.24+ and the Claude Code CLI on your `PATH`.

```sh
go run .            # takes the same flags as claudewitz
make install        # installs to Go's bin folder (~/go/bin unless GOBIN is set)
make uninstall
```

## How it talks to Claude Code

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

- Starts `claude -p` in stream-json mode with `--permission-prompt-tool stdio` and
  `--forward-subagent-text`
- Sends an `initialize` control request on startup to get commands, models and account info
- Switches model with `set_model` and effort with `apply_flag_settings` (`effortLevel`), mid-session
- Answers `AskUserQuestion` by allowing it with `updatedInput.answers`; approves plans with a
  `setMode` permission update for accept-edits, and follows the CLI's own mode changes
- Renders `stream_event` deltas live and takes tool calls and results from complete messages
- Follows sub-agents through `task_started` / `task_progress` / `task_updated` /
  `task_notification` events and messages tagged with `parent_tool_use_id`
- Reads history from `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`
- Reads git state with `git status --porcelain=v2` and `git diff --numstat` after file-changing
  tools, after each turn and every 4s, with `GIT_OPTIONAL_LOCKS=0` so it never holds the index lock
- Removes `CLAUDECODE` / `CLAUDE_CODE_SESSION_ID` from the child's environment so that running
  claudewitz inside Claude Code doesn't pin every child to the parent session

## Roadmap ideas

- Drill into a sub-agent's full transcript
- Multiple concurrent sessions in tabs
- Persisted per-project "always allow" rules

## Releasing

Push a version tag and the release workflow does the rest: GoReleaser builds the binaries,
publishes a GitHub release with checksums and updates the cask in
[ExManubis/homebrew-tap](https://github.com/ExManubis/homebrew-tap).

```sh
git tag v0.1.0 && git push origin v0.1.0
```

It needs a `HOMEBREW_TAP_GITHUB_TOKEN` repository secret that can push to the tap. Dry run locally
with `goreleaser release --snapshot --clean`.
