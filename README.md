<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img alt="claudewitz: a headphone bean beside the CLAUDEWITZ wordmark" src="assets/logo-light.svg" width="640">
  </picture>
</p>

# claudewitz

A [Crush](https://github.com/charmbracelet/crush)-style terminal UI for **Claude Code**.

claudewitz runs on top of the `claude` CLI you already have, so it uses your existing login
(including a Pro/Max subscription, with no per-token API billing) and keeps all of Claude Code's
tools, MCP servers, skills, hooks, `CLAUDE.md` and session history.

<img width="3014" height="2086" alt="claudewitz screenshot" src="https://github.com/user-attachments/assets/0d6c251d-8b3f-44e3-bb2f-358428644386" />

## Features

- **Streaming chat** with Markdown, thinking blocks, inline images and collapsible tool calls
- **Readable diffs**: edits show as syntax-highlighted diffs, side by side when there's room
- **Permission and plan dialogs**: review an edit's full diff before you allow it, answer
  Claude's questions and approve plans
- **Sidebar** with context and subscription usage, todos, live sub-agents, git status and
  changed files
- **Changes viewer** (`ctrl+s`) for browsing every file changed in the working tree
- **Sessions**: resume any past session (`ctrl+r`), with prompt history across sessions
- **Model and effort picker** (`ctrl+t`), plus autocompletion for `/` commands and `@` files
- **Desktop notifications** when Claude needs you or finishes a turn, and they work over SSH too
- **Themes**: claude, charm, flexoki, catppuccin, tokyonight, gruvbox, dracula, nord and mono,
  each in light and dark (`/theme`)

## Install

You need the [Claude Code CLI](https://github.com/anthropics/claude-code) installed and
logged in (`npm i -g @anthropic-ai/claude-code`, then run `claude` once).

```sh
brew install exmanubis/tap/claudewitz                # macOS and Linux
go install github.com/exmanubis/claudewitz@latest     # with Go 1.24+
```

Or download a prebuilt binary for macOS, Linux or Windows from the
[releases page](https://github.com/ExManubis/Claudewitz/releases) and put it on your `PATH`.

## Usage

Run it in the project you want to work on:

```sh
claudewitz
claudewitz -continue                 # pick up the latest session here
claudewitz -resume <session-id>
claudewitz -model opus -effort high -mode acceptEdits
claudewitz -- --add-dir ../lib       # anything after -- goes straight to claude
```

`esc` interrupts Claude, `tab` moves focus to the transcript, `shift+tab` cycles permission
modes and `ctrl+b` toggles the sidebar.

Sessions run in a background `claudewitz daemon`, started on demand, so they outlive the TUI and
one window can drive them all. With two or more live sessions a panel lists them, most urgent
first (a tab strip on narrow terminals): `alt+↑/↓` switches, `ctrl+]` jumps to the next one that
needs you, `alt+n` starts one in any folder and `ctrl+q` kills one. Quitting leaves them running;
`claudewitz daemon status` lists the live sessions.
The daemon exits after 10 minutes with no clients and no sessions; `claudewitz daemon stop` ends
it sooner. `claudewitz -no-daemon` runs `claude` directly instead. On macOS, the alt keys need
"Use Option as Meta key" in Terminal and iTerm2.

**Icons look like boxes?** claudewitz uses [Nerd Font](https://www.nerdfonts.com) icons by
default. Install a Nerd Font or run `/icons unicode`.

## Configuration

Settings are saved to `~/.config/claudewitz/config.json`, or to `$XDG_CONFIG_HOME/claudewitz/` if
that is set. Set `$CLAUDEWITZ_CONFIG` to use a different file. Most settings, like the theme,
diff layout and notifications, are changed from inside the app.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for building from source, how claudewitz talks to Claude
Code, and releasing.
