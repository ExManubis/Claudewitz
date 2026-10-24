package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/exmanubis/claudewitz/internal/apns"
)

// The phone's Live Activity: every live session on the lock screen and in
// the Dynamic Island. The app starts it as you leave it and updates it
// while it runs; once iOS suspends the app, the daemon keeps it current
// with APNs pushes, and can start one by push-to-start while the phone was
// locked all along. See docs/daemon.md, Live Activity.

const (
	// activityTick is how often the sessions are checked for news.
	activityTick = time.Second
	// minorPushEvery spaces out pushes that only change an activity line
	// (priority 5); a change of state goes out at once with priority 10.
	minorPushEvery = 15 * time.Second
	// endAfter is how long nothing works or waits before the activity ends,
	// and lingerAfterEnd how long iOS keeps showing it then.
	endAfter       = 2 * time.Minute
	lingerAfterEnd = 15 * time.Minute
	// activityLifetime is when iOS ends an activity by itself; one still
	// needed is started afresh.
	activityLifetime = 8 * time.Hour
	// staleAfter marks the activity out of date if no push follows.
	staleAfter = 10 * time.Minute
	// doneWindow is how long a finished turn counts as done rather than idle.
	doneWindow = 30 * time.Minute
	// maxRows and lineLimit match the app's lock-screen layout.
	maxRows   = 3
	lineLimit = 80
)

// activityAttributesType is the app's ActivityAttributes type, named in
// push-to-start payloads.
const activityAttributesType = "SessionsActivityAttributes"

// ContentState is the activity's content, the JSON the app's
// SessionsActivityAttributes.ContentState decodes. Times are Unix seconds:
// ActivityKit decodes with a plain JSONDecoder, which would read a number as
// seconds since 2001 and can't read ISO strings at all.
type ContentState struct {
	Working   int   `json:"working"`
	Waiting   int   `json:"waiting"`
	Done      int   `json:"done"` // finished a turn in the last doneWindow
	Lead      *Lead `json:"lead,omitempty"`
	Others    []Row `json:"others"` // at most maxRows, the most urgent first
	More      int   `json:"more"`   // live sessions beyond Others
	UpdatedAt int64 `json:"updatedAt"`
}

// Lead is the most urgent session, shown in full.
type Lead struct {
	Session string `json:"session"`
	Name    string `json:"name"`
	Status  string `json:"status"` // waiting | working | done
	Tool    string `json:"tool,omitempty"`
	Line    string `json:"line"`
	Since   *int64 `json:"since,omitempty"`
}

// Row is another live session.
type Row struct {
	Name   string `json:"name"`
	Status string `json:"status"` // waiting | working | done | idle
	Since  *int64 `json:"since,omitempty"`
}

// Activity statuses.
const (
	laWaiting = "waiting"
	laWorking = "working"
	laDone    = "done"
	laIdle    = "idle"
)

// active reports whether something is going on, so the activity stays up.
func (c ContentState) active() bool { return c.Working+c.Waiting > 0 }

// buildContentState is the activity for these sessions: the same rules as
// ContentState.make in the app. The lead is the session that has waited
// longest, else the one whose turn has run longest, else the one that
// finished last; exited sessions don't show.
func buildContentState(sums []SessionSummary, at time.Time) ContentState {
	status := func(s SessionSummary) string {
		switch s.State {
		case StateWaiting:
			return laWaiting
		case StateWorking:
			return laWorking
		}
		if s.LastTurnEndedAt != nil && at.Sub(*s.LastTurnEndedAt) < doneWindow {
			return laDone
		}
		return laIdle
	}
	key := func(s SessionSummary) *time.Time {
		switch status(s) {
		case laWaiting:
			if s.Waiting != nil && !s.Waiting.Since.IsZero() {
				return &s.Waiting.Since
			}
			return s.TurnStartedAt
		case laWorking:
			return s.TurnStartedAt
		default:
			return s.LastTurnEndedAt
		}
	}
	rank := map[string]int{laWaiting: 0, laWorking: 1, laDone: 2, laIdle: 3}

	var live []SessionSummary
	for _, s := range sums {
		if s.State != StateExited {
			live = append(live, s)
		}
	}
	ordered := append([]SessionSummary(nil), live...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		sa, sb := status(a), status(b)
		if sa != sb {
			return rank[sa] < rank[sb]
		}
		ka, kb := key(a), key(b)
		switch {
		case ka != nil && kb != nil && !ka.Equal(*kb):
			if sa == laDone || sa == laIdle {
				return ka.After(*kb) // the latest to finish first
			}
			return ka.Before(*kb) // the longest waiting or working first
		case ka != nil && kb == nil:
			return true
		default:
			return false // stable: the daemon's order
		}
	})

	c := ContentState{Others: []Row{}, UpdatedAt: at.Unix()}
	for _, s := range live {
		switch status(s) {
		case laWaiting:
			c.Waiting++
		case laWorking:
			c.Working++
		case laDone:
			c.Done++
		}
	}
	rest := ordered
	if len(ordered) > 0 && status(ordered[0]) != laIdle {
		s := ordered[0]
		l := &Lead{Session: s.ID, Name: filepath.Base(s.CWD), Status: status(s)}
		switch l.Status {
		case laWaiting:
			if s.Waiting != nil {
				l.Tool, l.Line = s.Waiting.Tool, s.Waiting.Summary
			}
		case laWorking:
			l.Line = s.Title
			if s.Activity != nil {
				l.Line = s.Activity.Text
			}
		}
		l.Line = activityLine(l.Line)
		l.Since = unixPtr(key(s))
		c.Lead, rest = l, ordered[1:]
	}
	for i, s := range rest {
		if i == maxRows {
			c.More = len(rest) - maxRows
			break
		}
		r := Row{Name: filepath.Base(s.CWD), Status: status(s)}
		if r.Status == laDone {
			r.Since = unixPtr(s.LastTurnEndedAt)
		}
		c.Others = append(c.Others, r)
	}
	return c
}

// activityLine is the first line, cut to lineLimit characters.
func activityLine(text string) string {
	for _, l := range strings.Split(text, "\n") {
		if l != "" {
			if r := []rune(l); len(r) > lineLimit {
				return string(r[:lineLimit-1]) + "…"
			}
			return l
		}
	}
	return ""
}

func unixPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	u := t.Unix()
	return &u
}

// ── Deciding what to send ───────────────────────────────────────────────

// activityMemo is what the daemon remembers about one device's activity
// between ticks.
type activityMemo struct {
	sent       *ContentState // last content pushed
	sentAt     time.Time
	quietSince time.Time // nothing working or waiting since
	startedAt  time.Time // a push-to-start went out; zero once that activity is known or over
}

type activityAction int

const (
	actNone activityAction = iota
	actStart
	actUpdate
	actEnd
	actForget // iOS has ended it (8 hours): forget the token
)

// planActivity decides what one device's activity needs now, and the
// push priority. It updates m as if the push went out.
func planActivity(p Push, m *activityMemo, c ContentState, at time.Time) (activityAction, int) {
	if c.active() {
		m.quietSince = time.Time{}
	} else if m.quietSince.IsZero() {
		m.quietSince = at
	}

	if p.Activity == nil {
		// None on the phone: start one if there's work to show and the
		// phone can be started remotely, unless one is already on its way
		// (the app registers its token when iOS wakes it).
		if !c.active() || p.PushToStartToken == "" ||
			!m.startedAt.IsZero() && at.Sub(m.startedAt) < activityLifetime {
			return actNone, 0
		}
		m.startedAt, m.sent, m.sentAt = at, &c, at
		return actStart, 10
	}
	m.startedAt = time.Time{}
	if at.Sub(p.Activity.Since) >= activityLifetime {
		*m = activityMemo{}
		return actForget, 0
	}
	if !c.active() && at.Sub(m.quietSince) >= endAfter {
		*m = activityMemo{}
		return actEnd, 10
	}
	if m.sent != nil && sameContent(*m.sent, c) {
		return actNone, 0
	}
	priority := 10
	if m.sent != nil && !majorChange(*m.sent, c) {
		if at.Sub(m.sentAt) < minorPushEvery {
			return actNone, 0
		}
		priority = 5
	}
	m.sent, m.sentAt = &c, at
	return actUpdate, priority
}

// sameContent compares everything but the time it was made.
func sameContent(a, b ContentState) bool {
	a.UpdatedAt, b.UpdatedAt = 0, 0
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// majorChange is a change worth a priority push: something started or
// stopped waiting or working, or another session took the lead.
func majorChange(a, b ContentState) bool {
	if a.Working != b.Working || a.Waiting != b.Waiting || a.Done != b.Done || (a.Lead == nil) != (b.Lead == nil) {
		return true
	}
	if a.Lead == nil {
		return false
	}
	return a.Lead.Session != b.Lead.Session || a.Lead.Status != b.Lead.Status || a.Lead.Tool != b.Lead.Tool
}

// ── Sending ─────────────────────────────────────────────────────────────

// runActivities pushes Live Activity changes until done closes.
func (s *Server) runActivities(done <-chan struct{}) {
	t := time.NewTicker(activityTick)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			s.tickActivities(time.Now())
		}
	}
}

// tickActivities brings every registered phone's activity up to date.
func (s *Server) tickActivities(at time.Time) {
	client, topic := s.pusher()
	if client == nil {
		return
	}
	devs, err := s.devices().List()
	if err != nil {
		return
	}
	var c *ContentState
	for _, d := range devs {
		if d.Push == nil || d.Push.Activity == nil && d.Push.PushToStartToken == "" {
			continue
		}
		if c == nil {
			built := buildContentState(s.list(), at.UTC())
			c = &built
		}
		s.actMu.Lock()
		m := s.actMemo[d.ID]
		if m == nil {
			m = &activityMemo{}
			s.actMemo[d.ID] = m
		}
		action, priority := planActivity(*d.Push, m, *c, at)
		s.actMu.Unlock()
		s.pushActivity(d, action, priority, *c, topic+".push-type.liveactivity", at)
	}
}

func (s *Server) pushActivity(d Device, action activityAction, priority int, c ContentState, topic string, at time.Time) {
	aps := map[string]any{
		"timestamp":     at.Unix(),
		"content-state": c,
	}
	n := apns.Notification{Sandbox: d.Push.Sandbox, Topic: topic, PushType: "liveactivity", Priority: priority}
	forget := func(p *Push) { p.Activity = nil }
	switch action {
	case actNone:
		return
	case actForget:
		_ = s.devices().UpdatePush(d.ID, forget)
		return
	case actStart:
		theme := d.Push.Theme
		if theme == "" {
			theme = "claude"
		}
		aps["event"] = "start"
		aps["attributes-type"] = activityAttributesType
		aps["attributes"] = map[string]string{"macName": macName(), "theme": theme}
		aps["stale-date"] = at.Add(staleAfter).Unix()
		n.DeviceToken = d.Push.PushToStartToken
		forget = func(p *Push) { p.PushToStartToken = "" }
	case actUpdate:
		aps["event"] = "update"
		aps["stale-date"] = at.Add(staleAfter).Unix()
		aps["relevance-score"] = relevance(c)
		n.DeviceToken = d.Push.Activity.Token
	case actEnd:
		aps["event"] = "end"
		aps["dismissal-date"] = at.Add(lingerAfterEnd).Unix()
		n.DeviceToken = d.Push.Activity.Token
	}
	n.Payload = mustJSON(map[string]any{"aps": aps})
	s.send(n, func() { _ = s.devices().UpdatePush(d.ID, forget) })
	if action == actEnd {
		// Over, whether or not the push arrived; the app starts the next one.
		_ = s.devices().UpdatePush(d.ID, func(p *Push) { p.Activity = nil })
	}
}

// relevance orders this activity against others on the lock screen: up
// front while something waits on you.
func relevance(c ContentState) int {
	if c.Waiting > 0 {
		return 100
	}
	return 50
}

// macName is this Mac's name, for an activity started by push.
func macName() string {
	h, err := os.Hostname()
	if err != nil {
		return "Mac"
	}
	return strings.TrimSuffix(h, ".local")
}

// ── Registering ─────────────────────────────────────────────────────────

// RegisterActivityParams are the phone's Live Activity tokens
// (device.registerActivity). Either may come alone.
type RegisterActivityParams struct {
	// PushToStartToken starts an activity when the phone has none.
	PushToStartToken string `json:"pushToStartToken,omitempty"`
	// Activity is a running activity and the token that updates it.
	Activity *ActivityToken `json:"activity,omitempty"`
	Sandbox  bool           `json:"sandbox"`
	// Theme is the app's palette, for activities the daemon starts.
	Theme string `json:"theme,omitempty"`
}

// ActivityToken is a running activity on the phone.
type ActivityToken struct {
	ID    string    `json:"id"`
	Token string    `json:"pushToken"`
	Since time.Time `json:"since"` // when the daemon learnt of it; iOS ends it 8 hours on
}

// EndActivityParams says the phone ended an activity on purpose
// (device.endActivity): the daemon starts no other until the phone
// registers a push-to-start token again.
type EndActivityParams struct {
	ID string `json:"id"`
}

func (s *Server) registerActivity(c *conn, p RegisterActivityParams) (any, error) {
	dev := c.deviceID()
	if dev == "" {
		return nil, &Error{CodeBadRequest, "only a paired phone registers a Live Activity"}
	}
	if p.PushToStartToken == "" && (p.Activity == nil || p.Activity.ID == "" || p.Activity.Token == "") {
		return nil, &Error{CodeBadRequest, "pushToStartToken or activity {id, pushToken} is required"}
	}
	err := s.devices().UpdatePush(dev, func(push *Push) {
		push.Sandbox = p.Sandbox
		if p.Theme != "" {
			push.Theme = p.Theme
		}
		if p.PushToStartToken != "" {
			push.PushToStartToken = p.PushToStartToken
		}
		if a := p.Activity; a != nil && a.ID != "" && a.Token != "" {
			// The same activity's token can change; its age doesn't.
			since := now()
			if push.Activity != nil && push.Activity.ID == a.ID {
				since = push.Activity.Since
			}
			push.Activity = &ActivityToken{ID: a.ID, Token: a.Token, Since: since}
		}
	})
	if err != nil {
		return nil, wrap(err)
	}
	if p.Activity != nil {
		// The phone shows what it showed; the next tick decides afresh.
		s.actMu.Lock()
		delete(s.actMemo, dev)
		s.actMu.Unlock()
	}
	return struct{}{}, nil
}

func (s *Server) endActivity(c *conn, p EndActivityParams) (any, error) {
	dev := c.deviceID()
	if dev == "" {
		return nil, &Error{CodeBadRequest, "only a paired phone has a Live Activity"}
	}
	err := s.devices().UpdatePush(dev, func(push *Push) {
		if push.Activity != nil && push.Activity.ID == p.ID {
			push.Activity = nil
		}
		push.PushToStartToken = ""
	})
	s.actMu.Lock()
	delete(s.actMemo, dev)
	s.actMu.Unlock()
	return struct{}{}, wrap(err)
}
