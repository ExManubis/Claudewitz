package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/exmanubis/claudewitz/internal/claude"
)

// Tools that need an answer from the user rather than a yes/no.
const (
	toolAskUser  = "AskUserQuestion"
	toolExitPlan = "ExitPlanMode"
)

func isInteractiveTool(name string) bool { return name == toolAskUser || name == toolExitPlan }

func newFreeText(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.Prompt = "› "
	ti.PromptStyle = statusKey
	ti.CharLimit = 0
	return ti
}

// startInteraction prepares a dialog when the head of the permission queue
// is a question or a plan. Malformed input falls back to the plain
// permission prompt.
func (m *Model) startInteraction() {
	m.question, m.plan = nil, nil
	if len(m.pending) == 0 {
		return
	}
	p := m.pending[0]
	switch p.ToolName {
	case toolAskUser:
		qs, err := claude.ParseQuestions(p.Input)
		if err != nil || len(qs) == 0 {
			return
		}
		st := &questionState{qs: qs, input: newFreeText("Type your answer")}
		st.cursor = make([]int, len(qs))
		st.picked = make([]map[int]bool, len(qs))
		st.other = make([]string, len(qs))
		for i := range qs {
			st.picked[i] = map[int]bool{}
		}
		m.question = st
	case toolExitPlan:
		var in struct {
			Plan string `json:"plan"`
			Path string `json:"planFilePath"`
		}
		_ = json.Unmarshal(p.Input, &in)
		text := readPlanFile(in.Path, in.Plan)
		m.plan = &planState{plan: text, path: in.Path, input: newFreeText("What should Claude change?")}
		if e, ok := m.chat.byToolID[p.ToolUseID]; ok {
			e.planText, e.dirty = text, true
		}
	}
}

// readPlanFile prefers the plan file Claude Code saved over the text in the
// tool call: Claude can pass a stale copy after revising the file.
func readPlanFile(path, fallback string) string {
	if path == "" {
		return fallback
	}
	f, err := os.Open(path)
	if err != nil {
		return fallback
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return fallback
	}
	return string(b)
}

// finishPending answers the head of the permission queue.
func (m *Model) finishPending(d claude.PermissionDecision, interrupt bool) {
	p := m.pending[0]
	if m.proc != nil {
		_ = m.proc.Respond(p, d)
		if interrupt {
			_ = m.proc.Interrupt()
			m.stopped = true
		}
	}
	m.pending = m.pending[1:]
	m.afterPermission()
}

// ── Questions ───────────────────────────────────────────────────────────

type questionState struct {
	qs      []claude.Question
	idx     int // current question; len(qs) is the review step
	cursor  []int
	picked  []map[int]bool // chosen option indexes; len(options) is "Other"
	other   []string       // free-text answers
	editing bool
	input   textinput.Model
	warn    string
}

func (q *questionState) review() bool { return q.idx >= len(q.qs) }

// rows is the number of selectable rows for question i: its options,
// "Other…", and for multi-select a final continue row.
func (q *questionState) rows(i int) int {
	n := len(q.qs[i].Options) + 1
	if q.qs[i].MultiSelect {
		n++
	}
	return n
}

func (q *questionState) otherRow(i int) int { return len(q.qs[i].Options) }

// answer is the text sent back for question i ("" when unanswered).
func (q *questionState) answer(i int) string {
	var parts []string
	for k, opt := range q.qs[i].Options {
		if q.picked[i][k] {
			parts = append(parts, opt.Label)
		}
	}
	if q.picked[i][q.otherRow(i)] && strings.TrimSpace(q.other[i]) != "" {
		parts = append(parts, strings.TrimSpace(q.other[i]))
	}
	return strings.Join(parts, ", ")
}

// advance moves past the current question; a lone single-choice question
// submits straight away.
func (m *Model) advanceQuestion() tea.Cmd {
	q := m.question
	q.warn = ""
	if len(q.qs) == 1 {
		return m.submitAnswers()
	}
	q.idx++
	return nil
}

func (m *Model) submitAnswers() tea.Cmd {
	q := m.question
	answers := map[string]string{}
	var summary [][2]string
	for i, qq := range q.qs {
		a := q.answer(i)
		if a == "" {
			q.idx, q.warn = i, "Answer this question first"
			return nil
		}
		answers[qq.Question] = a
		summary = append(summary, [2]string{firstNonEmpty(qq.Header, qq.Question), a})
	}
	p := m.pending[0]
	input, err := claude.AnswerQuestions(p.Input, answers)
	if err != nil {
		return m.notify(toastError, "Couldn't send answers", err.Error())
	}
	if e, ok := m.chat.byToolID[p.ToolUseID]; ok {
		e.answers = summary
		e.dirty = true
	}
	m.finishPending(claude.PermissionDecision{Behavior: "allow", UpdatedInput: input}, false)
	return nil
}

func (m *Model) declineQuestions(interrupt bool) {
	if e, ok := m.chat.byToolID[m.pending[0].ToolUseID]; ok {
		e.toolStatus, e.toolOutput, e.dirty = toolFailed, "declined", true
	}
	m.finishPending(claude.PermissionDecision{Behavior: "deny", Message: "The user declined to answer."}, interrupt)
}

func (m *Model) handleQuestionKey(k tea.KeyMsg) tea.Cmd {
	q := m.question
	defer m.layout()

	if q.editing {
		switch k.String() {
		case "esc":
			q.editing = false
			q.input.Blur()
			return nil
		case "enter":
			q.editing = false
			q.input.Blur()
			i := q.idx
			q.other[i] = q.input.Value()
			if strings.TrimSpace(q.other[i]) == "" {
				delete(q.picked[i], q.otherRow(i))
				return nil
			}
			if q.qs[i].MultiSelect {
				q.picked[i][q.otherRow(i)] = true
				return nil
			}
			q.picked[i] = map[int]bool{q.otherRow(i): true}
			return m.advanceQuestion()
		}
		var c tea.Cmd
		q.input, c = q.input.Update(k)
		return c
	}

	switch k.String() {
	case "ctrl+c":
		m.declineQuestions(true)
		return nil
	case "esc":
		m.declineQuestions(false)
		return nil
	case "tab", "right", "l":
		if !q.review() {
			q.idx++
		}
		return nil
	case "shift+tab", "left", "h":
		if q.idx > 0 {
			q.idx--
		}
		return nil
	}

	if q.review() {
		switch s := k.String(); {
		case s == "enter":
			return m.submitAnswers()
		case len(s) == 1 && s[0] >= '1' && s[0] <= '9':
			if n := int(s[0] - '1'); n < len(q.qs) {
				q.idx = n
			}
		}
		return nil
	}

	i := q.idx
	qq := q.qs[i]
	choose := func(row int) tea.Cmd {
		q.cursor[i] = row
		switch {
		case row == q.otherRow(i):
			q.editing = true
			q.input.SetValue(q.other[i])
			q.input.CursorEnd()
			return q.input.Focus()
		case qq.MultiSelect && row == q.otherRow(i)+1: // continue
			if q.answer(i) == "" {
				q.warn = "Pick at least one option"
				return nil
			}
			return m.advanceQuestion()
		case qq.MultiSelect:
			q.picked[i][row] = !q.picked[i][row]
			return nil
		default:
			q.picked[i] = map[int]bool{row: true}
			return m.advanceQuestion()
		}
	}
	switch s := k.String(); {
	case s == "up" || s == "k" || s == "ctrl+p":
		q.cursor[i] = (q.cursor[i] - 1 + q.rows(i)) % q.rows(i)
	case s == "down" || s == "j" || s == "ctrl+n":
		q.cursor[i] = (q.cursor[i] + 1) % q.rows(i)
	case s == "enter" || s == " ":
		return choose(q.cursor[i])
	case len(s) == 1 && s[0] >= '1' && s[0] <= '9':
		if n := int(s[0] - '1'); n < len(qq.Options) {
			return choose(n)
		}
	}
	return nil
}

func (m Model) questionView(width int) string {
	q := m.question
	inner := width - 4
	title := agentStyle.Render("? Claude has a question")
	if len(q.qs) > 1 {
		title = agentStyle.Render("? Claude has some questions")
	}
	lines := []string{title}

	if len(q.qs) > 1 {
		var tabs []string
		for i, qq := range q.qs {
			label := firstNonEmpty(qq.Header, fmt.Sprintf("Q%d", i+1))
			switch {
			case i == q.idx:
				tabs = append(tabs, selStyle.Render(" "+label+" "))
			case q.answer(i) != "":
				tabs = append(tabs, okStyle.Render(" ✓ "+label+" "))
			default:
				tabs = append(tabs, mutedStyle.Render(" "+label+" "))
			}
		}
		review := mutedStyle.Render(" Review ")
		if q.review() {
			review = selStyle.Render(" Review ")
		}
		lines = append(lines, strings.Join(tabs, mutedStyle.Render("·"))+mutedStyle.Render("·")+review)
	}
	lines = append(lines, "")

	if q.review() {
		lines = append(lines, textStyle.Bold(true).Render("Review your answers"), "")
		for i, qq := range q.qs {
			a := q.answer(i)
			val := okStyle.Render(a)
			if a == "" {
				val = warnStyle.Render("not answered")
			}
			lines = append(lines, fmt.Sprintf("%s %s %s",
				mutedStyle.Render(fmt.Sprintf("%d.", i+1)),
				textStyle.Render(truncate(firstNonEmpty(qq.Header, qq.Question), inner/2)+":"),
				val))
		}
	} else {
		i := q.idx
		qq := q.qs[i]
		lines = append(lines, lipgloss.NewStyle().Width(inner).Render(textStyle.Bold(true).Render(qq.Question)))
		hint := "Choose one"
		if qq.MultiSelect {
			hint = "Choose any · space to toggle"
		}
		lines = append(lines, mutedStyle.Render(hint), "")
		for row := 0; row < q.rows(i); row++ {
			lines = append(lines, m.questionRow(i, row, inner))
		}
	}
	if q.warn != "" {
		lines = append(lines, "", warnStyle.Render("▲ "+q.warn))
	}
	return popupBox.BorderForeground(currentTheme().second).Width(width - 2).Render(strings.Join(lines, "\n"))
}

func (m Model) questionRow(i, row, inner int) string {
	q := m.question
	qq := q.qs[i]
	cur := q.cursor[i] == row
	pointer := "  "
	if cur {
		pointer = statusKey.Render("❯ ")
	}
	mark := func(on bool) string {
		switch {
		case qq.MultiSelect && on:
			return okStyle.Render("☑ ")
		case qq.MultiSelect:
			return mutedStyle.Render("☐ ")
		case on:
			return okStyle.Render("● ")
		}
		return mutedStyle.Render("○ ")
	}
	labelStyle := textStyle
	if cur {
		labelStyle = textStyle.Bold(true)
	}

	switch {
	case row < len(qq.Options):
		opt := qq.Options[row]
		num := mutedStyle.Render(fmt.Sprintf("%d. ", row+1))
		head := pointer + num + mark(q.picked[i][row]) + labelStyle.Render(opt.Label)
		if opt.Description != "" && opt.Description != opt.Label {
			head += mutedStyle.Render("  " + truncate(opt.Description, max(10, inner-lipgloss.Width(head)-2)))
		}
		return head
	case row == q.otherRow(i):
		if q.editing {
			q.input.Width = max(10, inner-10)
			return pointer + "   " + q.input.View()
		}
		label := "Other…"
		if t := strings.TrimSpace(q.other[i]); t != "" {
			label = "Other: " + t
		}
		return pointer + "   " + mark(q.picked[i][row]) + labelStyle.Render(truncate(label, inner-8))
	default:
		label := "Next →"
		if i == len(q.qs)-1 {
			label = "Review →"
		}
		if len(q.qs) == 1 {
			label = "Submit ⏎"
		}
		return pointer + "   " + statusKey.Render(label)
	}
}

// ── Plan approval ───────────────────────────────────────────────────────

var planChoices = []struct{ label, desc string }{
	{"Yes, and auto-accept edits", "switch to accept-edits mode and start"},
	{"Yes, and approve each edit", "start in default mode"},
	{"No, keep planning", "tell Claude what to change"},
}

type planState struct {
	plan, path string
	cursor     int
	editing    bool
	input      textinput.Model
}

func (m *Model) handlePlanKey(k tea.KeyMsg) tea.Cmd {
	pl := m.plan
	defer m.layout()

	if pl.editing {
		switch k.String() {
		case "esc":
			pl.editing = false
			pl.input.Blur()
			return nil
		case "enter":
			return m.rejectPlan(pl.input.Value(), false)
		}
		var c tea.Cmd
		pl.input, c = pl.input.Update(k)
		return c
	}

	choose := func(i int) tea.Cmd {
		pl.cursor = i
		switch i {
		case 0:
			return m.approvePlan("acceptEdits")
		case 1:
			return m.approvePlan("default")
		}
		pl.editing = true
		return pl.input.Focus()
	}
	switch s := k.String(); {
	case s == "up" || s == "k" || s == "ctrl+p":
		pl.cursor = (pl.cursor - 1 + len(planChoices)) % len(planChoices)
	case s == "down" || s == "j" || s == "ctrl+n" || s == "tab":
		pl.cursor = (pl.cursor + 1) % len(planChoices)
	case s == "enter":
		return choose(pl.cursor)
	case len(s) == 1 && s[0] >= '1' && int(s[0]-'1') < len(planChoices):
		return choose(int(s[0] - '1'))
	case s == "esc":
		return m.rejectPlan("", false)
	case s == "ctrl+c":
		return m.rejectPlan("", true)
	}
	return nil
}

func (m *Model) approvePlan(mode string) tea.Cmd {
	d := claude.PermissionDecision{Behavior: "allow"}
	if mode == "acceptEdits" {
		d.UpdatedPermissions = []claude.PermissionUpdate{claude.SetModeUpdate(mode)}
	}
	m.permMode = mode
	m.finishPending(d, false)
	body := "Claude will ask before each edit"
	if mode == "acceptEdits" {
		body = "File edits are approved automatically"
	}
	return m.notify(toastSuccess, "Plan approved", body)
}

// rejectPlan sends the plan back. Feedback travels as a real user message:
// text inside a tool result reads to Claude as untrusted content, and it
// may (rightly) refuse to follow instructions found there.
func (m *Model) rejectPlan(feedback string, interrupt bool) tea.Cmd {
	feedback = strings.TrimSpace(feedback)
	if e, ok := m.chat.byToolID[m.pending[0].ToolUseID]; ok {
		e.feedback, e.dirty = firstNonEmpty(feedback, "kept planning"), true
	}
	if feedback == "" {
		m.finishPending(claude.PermissionDecision{
			Behavior: "deny",
			Message:  "The user rejected this plan in the approval dialog and wants to keep planning. Ask what they'd like changed.",
		}, interrupt)
		return nil
	}
	// Stop the turn so the feedback starts a fresh one, still in plan mode.
	m.finishPending(claude.PermissionDecision{
		Behavior: "deny",
		Message:  "The user rejected this plan and is sending their feedback as a new message.",
	}, true)
	return m.submit(feedback)
}

func (m Model) planView(width int) string {
	pl := m.plan
	inner := width - 4
	title := assistLabel.Render("✻ Plan ready for review")
	if pl.path != "" {
		title += mutedStyle.Render("  " + truncateLeft(prettyPath(pl.path), max(10, inner-26)))
	}
	lines := []string{title, mutedStyle.Render("The plan is shown above. Would you like to proceed?"), ""}
	for i, c := range planChoices {
		pointer := "  "
		label := textStyle.Render(c.label)
		if i == pl.cursor {
			pointer = statusKey.Render("❯ ")
			label = textStyle.Bold(true).Render(c.label)
		}
		lines = append(lines, pointer+mutedStyle.Render(fmt.Sprintf("%d. ", i+1))+label+mutedStyle.Render("  "+c.desc))
	}
	if pl.editing {
		pl.input.Width = max(10, inner-6)
		lines = append(lines, "", "   "+pl.input.View())
	}
	return popupBox.BorderForeground(currentTheme().accent).Width(width - 2).Render(strings.Join(lines, "\n"))
}

// interactionHelp is the help bar for an open question or plan dialog.
func (m Model) interactionHelp() [][2]string {
	switch {
	case m.question != nil && m.question.editing, m.plan != nil && m.plan.editing:
		return [][2]string{{"enter", "send"}, {"esc", "back"}}
	case m.question != nil && m.question.review():
		return [][2]string{{"enter", "submit"}, {"←", "back"}, {"1-9", "edit"}, {"esc", "decline"}}
	case m.question != nil:
		pairs := [][2]string{{"↑↓", "move"}, {"enter", "choose"}}
		if len(m.question.qs) > 1 {
			pairs = append(pairs, [2]string{"←→", "question"})
		}
		return append(pairs, [2]string{"esc", "decline"})
	case m.plan != nil:
		return [][2]string{{"↑↓", "move"}, {"enter", "choose"}, {"1-3", "pick"}, {"esc", "keep planning"}}
	}
	return nil
}
