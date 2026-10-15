package recap

import "fmt"

// Badge is a small honour for the day. ID is stable, for clients to pick an
// icon; Name and Detail are for people.
type Badge struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

func badges(d Day, files map[string]bool) []Badge {
	out := []Badge{}
	add := func(id, name, detail string) { out = append(out, Badge{id, name, detail}) }
	if d.Releases > 0 {
		add("release", "Release day", plural(d.Releases, "release", "releases")+" out the door")
	} else if d.PRsMerged > 0 {
		add("shipper", "Shipper", plural(d.PRsMerged, "pull request", "pull requests")+" merged")
	}
	if d.TestsPassed >= 3 && d.TestsFailed == 0 {
		add("green", "All green", fmt.Sprintf("%d test runs, not one red", d.TestsPassed))
	} else if d.TestsFailed > 0 && d.TestsPassed > d.TestsFailed {
		add("fixer", "Red to green", fmt.Sprintf("%d red test runs, %d green", d.TestsFailed, d.TestsPassed))
	}
	if d.Longest != nil && d.Longest.Seconds >= 15*60 {
		add("marathon", "Marathon", "one prompt kept Claude busy for "+duration(d.Longest.Seconds))
	}
	if d.Sessions >= 5 {
		add("juggler", "Juggler", fmt.Sprintf("%d sessions on the go", d.Sessions))
	}
	if d.LinesRemoved > d.LinesAdded && d.LinesRemoved >= 50 {
		add("tidy", "Spring cleaning", fmt.Sprintf("%d more lines gone than added", d.LinesRemoved-d.LinesAdded))
	}
	if n := languages(files); n >= 4 {
		add("polyglot", "Polyglot", fmt.Sprintf("files in %d languages", n))
	}
	if d.First != nil && d.First.Hour() >= 4 && d.First.Hour() < 7 {
		add("early", "Early bird", "first prompt at "+d.First.Format("15:04"))
	}
	if d.Last != nil && (d.Last.Hour() >= 23 || d.Last.Hour() < 4) {
		add("owl", "Night owl", "still going at "+d.Last.Format("15:04"))
	}
	if d.Streak >= 5 {
		add("streak", "On a roll", fmt.Sprintf("%d days in a row", d.Streak))
	}
	return out
}

// duration is "1h 9m" or "52m".
func duration(sec int) string {
	if sec >= 3600 && sec%3600 >= 60 {
		return fmt.Sprintf("%dh %dm", sec/3600, sec%3600/60)
	}
	if sec >= 3600 {
		return fmt.Sprintf("%dh", sec/3600)
	}
	return fmt.Sprintf("%dm", sec/60)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
