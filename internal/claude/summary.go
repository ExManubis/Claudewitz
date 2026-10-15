package claude

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/exmanubis/claudewitz/internal/safetext"
)

// SummarizeInput is a tool call's main argument on one line, cut to max
// cells (when max > 10): the TUI's tool rows and the daemon's overview
// line use it, so they read the same. Control characters are escaped,
// since permission requests keep their input as the CLI sent it.
func SummarizeInput(tool string, raw json.RawMessage, max int) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || len(m) == 0 {
		return ""
	}
	var s string
	switch tool {
	case "Bash":
		s = str(m, "command")
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		s = filepath.Base(firstNonEmpty(str(m, "file_path"), str(m, "notebook_path")))
	case "Glob", "Grep":
		s = str(m, "pattern")
		if p := str(m, "path"); p != "" {
			s += " in " + p
		}
	case "WebFetch":
		s = str(m, "url")
	case "WebSearch":
		s = str(m, "query")
	case "Task", "Agent":
		s = firstNonEmpty(str(m, "description"), str(m, "prompt"))
		if t := str(m, "subagent_type"); t != "" {
			s = t + " · " + s
		}
	case "TodoWrite":
		items, _ := m["todos"].([]any)
		s = fmt.Sprintf("%d items", len(items))
	case "TaskCreate", "TaskUpdate":
		s = firstNonEmpty(str(m, "subject"), str(m, "status"))
	case "Skill":
		s = str(m, "skill")
	case "AskUserQuestion":
		var qs []map[string]any
		if raw, err := json.Marshal(m["questions"]); err == nil {
			_ = json.Unmarshal(raw, &qs)
		}
		if len(qs) > 0 {
			s = str(qs[0], "question")
			if len(qs) > 1 {
				s += fmt.Sprintf(" (+%d more)", len(qs)-1)
			}
		}
	case "ExitPlanMode":
		s = "plan ready for review"
	default:
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := []string{}
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
		}
		s = strings.Join(parts, " ")
	}
	s = safetext.Line(strings.ReplaceAll(s, "\n", " ⏎ "))
	if max > 10 {
		s = ansi.Truncate(s, max, "…")
	}
	return s
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
