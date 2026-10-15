// Package recap tallies a day of work with Claude from Claude Code's
// transcripts: prompts, the time Claude spent working, commits, pull
// requests, releases, test runs and edited lines. It reads the files on this
// machine and nothing leaves it unless a client asks the daemon.
package recap

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Day is a day of work with Claude.
type Day struct {
	Date     string     `json:"date"` // YYYY-MM-DD
	Prompts  int        `json:"prompts"`
	Sessions int        `json:"sessions"`
	Working  int        `json:"workingSeconds"`  // Claude busy on your prompts
	First    *time.Time `json:"first,omitempty"` // first prompt
	Last     *time.Time `json:"last,omitempty"`  // last activity

	ToolCalls    int `json:"toolCalls"`
	OutputTokens int `json:"outputTokens"`
	FilesEdited  int `json:"filesEdited"`
	LinesAdded   int `json:"linesAdded"`
	LinesRemoved int `json:"linesRemoved"`

	Commits     int `json:"commits"`
	PRsOpened   int `json:"prsOpened"`
	PRsMerged   int `json:"prsMerged"`
	Releases    int `json:"releases"`
	TestsPassed int `json:"testsPassed"`
	TestsFailed int `json:"testsFailed"`

	Hours    [24]int     `json:"hours"` // seconds Claude worked in each hour
	Projects []Project   `json:"projects"`
	Moments  []Milestone `json:"moments"` // commits, pull requests and releases, in order
	Longest  *Turn       `json:"longest,omitempty"`
	Badges   []Badge     `json:"badges"`

	Week   []DayTotal `json:"week"`   // the seven days ending on Date
	Streak int        `json:"streak"` // days in a row with prompts, up to Date
}

// Project is one folder's share of the day.
type Project struct {
	Name    string `json:"name"`
	Prompts int    `json:"prompts"`
	Working int    `json:"workingSeconds"`
}

// Turn is one prompt and how long Claude worked on it.
type Turn struct {
	At      time.Time `json:"at"`
	Seconds int       `json:"seconds"`
	Prompt  string    `json:"prompt"`
	Project string    `json:"project,omitempty"`
}

// DayTotal is a day in brief, for the week strip.
type DayTotal struct {
	Date    string `json:"date"`
	Prompts int    `json:"prompts"`
	Working int    `json:"workingSeconds"`
}

// Empty reports whether nothing happened.
func (d Day) Empty() bool { return d.Prompts == 0 && d.ToolCalls == 0 }

// Recap tallies date's day, in loc. Only the date part of date is used.
func (s *Scanner) Recap(date time.Time, loc *time.Location) Day {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()

	y, mo, dd := date.Date()
	start := time.Date(y, mo, dd, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	in := func(t time.Time) bool { return !t.Before(start) && t.Before(end) }
	day := Day{Date: start.Format(time.DateOnly), Projects: []Project{}, Moments: []Milestone{}, Badges: []Badge{}}

	projects := map[string]*Project{}
	project := func(name string) *Project {
		if name == "" {
			name = "?"
		}
		p := projects[name]
		if p == nil {
			p = &Project{Name: name}
			projects[name] = p
		}
		return p
	}
	sessions := map[string]bool{}
	files := map[string]bool{}
	totals := map[string]*DayTotal{}
	var last time.Time

	for _, t := range s.files {
		for _, tn := range t.turns {
			key := tn.at.In(loc).Format(time.DateOnly)
			dt := totals[key]
			if dt == nil {
				dt = &DayTotal{Date: key}
				totals[key] = dt
			}
			dt.Prompts++
			dt.Working += int(tn.active.Seconds())
			if !in(tn.at) {
				continue
			}
			day.Prompts++
			sessions[t.session] = true
			p := project(tn.project)
			p.Prompts++
			p.Working += int(tn.active.Seconds())
			if day.First == nil || tn.at.Before(*day.First) {
				at := tn.at.In(loc)
				day.First = &at
			}
			if day.Longest == nil || int(tn.active.Seconds()) > day.Longest.Seconds {
				day.Longest = &Turn{At: tn.at.In(loc), Seconds: int(tn.active.Seconds()), Prompt: tn.prompt, Project: tn.project}
			}
		}
		for q, d := range t.active {
			at := time.Unix(q*quarter, 0)
			if in(at) {
				day.Working += int(d.Seconds())
				day.Hours[at.In(loc).Hour()] += int(d.Seconds())
				if l := t.latest[q]; l.After(last) {
					last = l
				}
			}
		}
		for q, n := range t.tokens {
			if in(time.Unix(q*quarter, 0)) {
				day.OutputTokens += n
			}
		}
		for q, n := range t.tools {
			if at := time.Unix(q*quarter, 0); in(at) {
				day.ToolCalls += n
				sessions[t.session] = true
			}
		}
		for _, e := range t.edits {
			if in(e.at) {
				files[e.path] = true
				day.LinesAdded += e.added
				day.LinesRemoved += e.removed
			}
		}
		for _, m := range t.marks {
			if !in(m.At) {
				continue
			}
			m.At = m.At.In(loc)
			switch m.Kind {
			case Commit:
				day.Commits++
			case PR:
				day.PRsOpened++
			case Merge:
				day.PRsMerged++
			case Release:
				day.Releases++
			case Tests:
				day.TestsPassed++
				continue
			case TestsFailed:
				day.TestsFailed++
				continue
			}
			day.Moments = append(day.Moments, m)
		}
	}
	if !last.IsZero() {
		l := last.In(loc)
		day.Last = &l
	}
	day.Sessions = len(sessions)
	day.FilesEdited = len(files)
	for _, p := range projects {
		day.Projects = append(day.Projects, *p)
	}
	sort.Slice(day.Projects, func(i, j int) bool {
		a, b := day.Projects[i], day.Projects[j]
		if a.Working != b.Working {
			return a.Working > b.Working
		}
		return a.Name < b.Name
	})
	sort.Slice(day.Moments, func(i, j int) bool { return day.Moments[i].At.Before(day.Moments[j].At) })

	for i := 6; i >= 0; i-- {
		key := start.AddDate(0, 0, -i).Format(time.DateOnly)
		dt := DayTotal{Date: key}
		if t := totals[key]; t != nil {
			dt = *t
		}
		day.Week = append(day.Week, dt)
	}
	// A streak survives a day that hasn't had a prompt yet.
	from := start
	if totals[day.Date] == nil {
		from = start.AddDate(0, 0, -1)
	}
	for d := from; totals[d.Format(time.DateOnly)] != nil; d = d.AddDate(0, 0, -1) {
		day.Streak++
	}
	day.Badges = badges(day, files)
	return day
}

// languages counts the kinds of files edited, by extension.
func languages(files map[string]bool) int {
	seen := map[string]bool{}
	for f := range files {
		if ext := strings.ToLower(filepath.Ext(f)); ext != "" {
			seen[ext] = true
		}
	}
	return len(seen)
}
