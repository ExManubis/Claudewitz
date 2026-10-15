package recap

import (
	"regexp"
	"strings"
	"time"
)

// Milestone kinds.
const (
	Commit      = "commit"
	PR          = "pr"      // a pull request opened
	Merge       = "merge"   // a pull request merged
	Release     = "release" // a tag pushed or a release published
	Tests       = "tests"   // a test run that passed
	TestsFailed = "testsFailed"
)

// Milestone is a moment in a session worth marking: a commit, a pull
// request, a release or a test run.
type Milestone struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Text    string    `json:"text"` // commit subject, PR link, tag, test runner
	Project string    `json:"project,omitempty"`
}

// Celebrated reports whether the kind is good news.
func (m Milestone) Celebrated() bool { return m.Kind != TestsFailed }

var (
	// git commit prints "[branch abc1234] Subject", or "[main (root-commit) abc1234] Subject".
	commitLine = regexp.MustCompile(`(?m)^\[[^\]\s]+(?: \([^)]*\))? [0-9a-f]{7,40}\] (.+)$`)
	gitCommit  = regexp.MustCompile(`\bgit(?:\s+-[Cc]\s+\S+)*\s+commit\b`)
	// -m "Subject…", -m 'Subject…' or -m "$(cat <<'EOF'⏎Subject…", to the end of its first line.
	commitMsg  = regexp.MustCompile(`\s-[a-zA-Z]*m\s*(?:"\$\(cat <<-?'?EOF'?\n([^\n]+)|"((?:\\.|[^"\\\n])+)|'([^'\n]+))`)
	newTag     = regexp.MustCompile(`\[new tag\]\s+(\S+)\s+->`)
	pushedTag  = regexp.MustCompile(`git push\b[^;&|]*?\s(v\d+\.\d+(?:\.\d+)?(?:-[\w.]+)?)(?:\s|$|[;&|])`)
	releaseURL = regexp.MustCompile(`https://github\.com/\S+/releases/tag/(\S+)`)
	prURL      = regexp.MustCompile(`https://github\.com/[^\s/]+/[^\s/]+/pull/\d+`)
	mergedLine = regexp.MustCompile(`(?m)Merged pull request (\S+(?: \(.*\))?)`)
	prArg      = regexp.MustCompile(`gh pr merge\s+(?:-\S+\s+)*#?(\d+)`)

	// testRunner finds a test command anywhere in a shell line, such as
	// "cd app && go test ./..." or "make test".
	testRunner = regexp.MustCompile(`(?:^|[\s;&|(])(go test|swift test|xcodebuild\b[^;&|]*\btest\b|cargo (?:nextest run|test)|(?:npm|pnpm|yarn|bun)(?: run)? test\b|bun test|deno test|pytest|python3? -m (?:pytest|unittest)|rspec|jest|vitest|phpunit|mix test|(?:\./)?gradlew? test|mvn test|dotnet test|ctest|make (?:test|check))\b`)
	// testFailure catches failures that a pipe (| tail) hid from the exit code.
	testFailure = regexp.MustCompile(`(?m)^(?:--- )?FAIL\b|\*\* TEST FAILED \*\*|\b[1-9]\d* (?:failed|failing)\b|with [1-9]\d* failures?\b|Tests? failed|^FAILED |test result: FAILED`)
)

var shellUnescape = strings.NewReplacer(`\"`, `"`, "\\`", "`", `\$`, `$`, `\\`, `\`)

var heredoc = regexp.MustCompile(`<<-?\s*['"]?(\w+)['"]?`)

// stripHeredocs drops the bodies of here-documents, so a script that
// mentions "git push origin v1.2.3" or "go test" isn't taken for running it.
func stripHeredocs(command string) string {
	var b strings.Builder
	for {
		m := heredoc.FindStringSubmatchIndex(command)
		if m == nil {
			b.WriteString(command)
			return b.String()
		}
		b.WriteString(command[:m[1]])
		rest := command[m[1]:]
		nl := strings.IndexByte(rest, '\n')
		if nl < 0 {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:nl+1])
		delim := command[m[2]:m[3]]
		end := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(delim) + `\s*$`).FindStringIndex(rest[nl+1:])
		if end == nil {
			return b.String()
		}
		command = rest[nl+1+end[1]:]
	}
}

// Classify finds the milestones in a finished shell command, from its command
// line, its output and whether it failed. One command can hold several, as in
// "git commit … && git push --tags".
func Classify(command, output string, failed bool) []Milestone {
	var out []Milestone
	full := command // a commit message can be a here-document
	command = stripHeredocs(command)
	if m := testRunner.FindStringSubmatch(command); m != nil {
		kind := Tests
		if failed || testFailure.MatchString(output) {
			kind = TestsFailed
		}
		runner := m[1]
		if strings.HasPrefix(runner, "xcodebuild") {
			runner = "xcodebuild test"
		}
		out = append(out, Milestone{Kind: kind, Text: runner})
	}
	if failed {
		return out
	}
	if gitCommit.MatchString(command) && !strings.Contains(command, "--dry-run") {
		found := commitLine.FindAllStringSubmatch(output, -1)
		for _, m := range found {
			out = append(out, Milestone{Kind: Commit, Text: strings.TrimSpace(m[1])})
		}
		if len(found) == 0 && !strings.Contains(output, "nothing to commit") {
			// git commit -q says nothing: take the subject from -m.
			text := ""
			if m := commitMsg.FindStringSubmatch(full); m != nil {
				text = strings.TrimSpace(m[1] + shellUnescape.Replace(m[2]) + m[3])
			}
			out = append(out, Milestone{Kind: Commit, Text: text})
		}
	}
	if strings.Contains(command, "gh pr create") {
		if u := prURL.FindString(output); u != "" {
			out = append(out, Milestone{Kind: PR, Text: u})
		}
	}
	if strings.Contains(command, "gh pr merge") {
		text := ""
		if m := mergedLine.FindStringSubmatch(output); m != nil {
			text = m[1]
		} else if m := prArg.FindStringSubmatch(command); m != nil {
			text = "#" + m[1]
		}
		out = append(out, Milestone{Kind: Merge, Text: text})
	}
	if strings.Contains(command, "git push") {
		tags := newTag.FindAllStringSubmatch(output, -1)
		if len(tags) == 0 {
			// git push -q origin v1.2.3 says nothing.
			tags = pushedTag.FindAllStringSubmatch(command, -1)
		}
		for _, m := range tags {
			out = append(out, Milestone{Kind: Release, Text: m[1]})
		}
	}
	if strings.Contains(command, "gh release create") {
		if m := releaseURL.FindStringSubmatch(output); m != nil {
			out = append(out, Milestone{Kind: Release, Text: m[1]})
		}
	}
	return out
}
