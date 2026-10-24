package daemon

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

var laNow = time.Unix(1_792_836_162, 0).UTC()

func ago(d time.Duration) *time.Time {
	t := laNow.Add(-d)
	return &t
}

func laSession(id, state string) SessionSummary {
	return SessionSummary{ID: id, CWD: "/Users/demo/Developer/" + id, Title: id, State: state}
}

func working(id string, started time.Duration, activity string) SessionSummary {
	s := laSession(id, StateWorking)
	s.TurnStartedAt = ago(started)
	if activity != "" {
		s.Activity = &Activity{Kind: "tool", Text: activity}
	}
	return s
}

func waitingOn(id string, since time.Duration) SessionSummary {
	s := laSession(id, StateWaiting)
	s.TurnStartedAt = ago(since + time.Minute)
	s.Waiting = &Waiting{RequestID: "r_" + id, Tool: "Bash", Summary: "go test ./...", Since: *ago(since)}
	return s
}

func finished(id string, ended time.Duration) SessionSummary {
	s := laSession(id, StateIdle)
	s.LastTurnEndedAt = ago(ended)
	return s
}

func i64(n int64) *int64 { return &n }

func TestTheLongestWaitingSessionLeads(t *testing.T) {
	c := buildContentState([]SessionSummary{working("busy", 10*time.Minute, ""), waitingOn("asked-late", 10*time.Second), waitingOn("asked-early", 42*time.Second)}, laNow)
	want := &Lead{Session: "asked-early", Name: "asked-early", Status: laWaiting, Tool: "Bash", Line: "go test ./...", Since: i64(laNow.Unix() - 42)}
	if !reflect.DeepEqual(c.Lead, want) {
		t.Fatalf("lead %+v", c.Lead)
	}
	if c.Others[0].Name != "asked-late" || c.Others[1].Name != "busy" || c.Waiting != 2 || c.Working != 1 || !c.active() {
		t.Fatalf("state %+v", c)
	}
}

func TestOtherwiseTheLongestTurnLeadsWithItsActivity(t *testing.T) {
	c := buildContentState([]SessionSummary{
		working("short", 30*time.Second, "Read main.go"),
		working("long", 257*time.Second, `Grep "claimAmount" src/`),
		finished("finished", 6*time.Minute),
	}, laNow)
	if c.Lead.Name != "long" || c.Lead.Status != laWorking || c.Lead.Line != `Grep "claimAmount" src/` ||
		*c.Lead.Since != laNow.Unix()-257 || c.Lead.Tool != "" {
		t.Fatalf("lead %+v", c.Lead)
	}
	want := []Row{{Name: "short", Status: laWorking}, {Name: "finished", Status: laDone, Since: i64(laNow.Unix() - 360)}}
	if !reflect.DeepEqual(c.Others, want) {
		t.Fatalf("others %+v", c.Others)
	}
}

func TestAWorkingSessionWithNoActivityShowsItsTitle(t *testing.T) {
	c := buildContentState([]SessionSummary{working("fresh", time.Second, "")}, laNow)
	if c.Lead.Line != "fresh" {
		t.Fatalf("line %q", c.Lead.Line)
	}
}

func TestAStartingSessionIsntWork(t *testing.T) {
	c := buildContentState([]SessionSummary{laSession("booting", StateStarting)}, laNow)
	if c.active() || c.Lead != nil || c.Others[0].Status != laIdle {
		t.Fatalf("state %+v", c)
	}
}

func TestWhenAllIsDoneTheLastToFinishLeads(t *testing.T) {
	c := buildContentState([]SessionSummary{
		finished("earlier", 15*time.Minute), finished("latest", 2*time.Minute),
		finished("yesterday", 24*time.Hour), laSession("crashed", StateExited),
	}, laNow)
	if c.Lead.Name != "latest" || c.Lead.Status != laDone || c.Lead.Line != "" {
		t.Fatalf("lead %+v", c.Lead)
	}
	if len(c.Others) != 2 || c.Others[0].Status != laDone || c.Others[1].Status != laIdle || c.Done != 2 || c.active() {
		t.Fatalf("state %+v", c)
	}
}

func TestNothingRecentMeansNoLead(t *testing.T) {
	c := buildContentState([]SessionSummary{finished("old", 24*time.Hour)}, laNow)
	if c.Lead != nil || len(c.Others) != 1 {
		t.Fatalf("state %+v", c)
	}
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), `"lead"`) {
		t.Fatalf("a missing lead is encoded: %s", b)
	}
}

func TestThreeRowsAndACountOfTheRest(t *testing.T) {
	var sums []SessionSummary
	for i := 1; i <= 6; i++ {
		sums = append(sums, working("s"+itoa(i), time.Duration(100*i)*time.Second, ""))
	}
	c := buildContentState(sums, laNow)
	if c.Lead.Name != "s6" || len(c.Others) != 3 || c.Others[0].Name != "s5" || c.Others[2].Name != "s3" || c.More != 2 {
		t.Fatalf("state %+v", c)
	}
}

func TestTiesKeepTheDaemonsOrder(t *testing.T) {
	a, b := laSession("b", StateWorking), laSession("a", StateWorking)
	c := buildContentState([]SessionSummary{a, b}, laNow)
	if c.Lead.Name != "b" || c.Others[0].Name != "a" {
		t.Fatalf("state %+v", c)
	}
}

func TestLinesAreOneLineOfAtMostEightyCharacters(t *testing.T) {
	if got := activityLine("first\nsecond"); got != "first" {
		t.Fatal(got)
	}
	long := activityLine(strings.Repeat("x", 100))
	if r := []rune(long); len(r) != 80 || r[79] != '…' {
		t.Fatal(long)
	}
	if got := activityLine(strings.Repeat("x", 80)); len(got) != 80 {
		t.Fatal(got)
	}
}

// The app's tests decode the same file (claudewitz-ios
// Tests/ClaudewitzKitTests/Fixtures), so both ends agree on the payload.
func TestTheSharedFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/live-activity-content-state.json")
	if err != nil {
		t.Fatal(err)
	}
	var c ContentState
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c.Lead == nil || c.Lead.Session != "s_1" || c.Lead.Tool != "Bash" || *c.Lead.Since != 1792836120 ||
		len(c.Others) != 3 || *c.Others[2].Since != 1792835800 || c.Working != 2 || c.Waiting != 1 || c.Done != 1 {
		t.Fatalf("decoded %+v", c)
	}
	var want, got any
	_ = json.Unmarshal(b, &want)
	out, _ := json.Marshal(c)
	_ = json.Unmarshal(out, &got)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("encodes differently:\n%s\nvs\n%s", b, out)
	}
}

// ── planActivity ───────────────────────────────────────────────────────

func TestPushToStartWhenWorkBeginsAndNoActivityIsUp(t *testing.T) {
	m := &activityMemo{}
	busy := buildContentState([]SessionSummary{working("a", time.Second, "")}, laNow)
	quiet := buildContentState([]SessionSummary{finished("a", time.Minute)}, laNow)
	if a, _ := planActivity(Push{PushToStartToken: "pts"}, m, quiet, laNow); a != actNone {
		t.Fatalf("started for nothing: %v", a)
	}
	if a, _ := planActivity(Push{}, m, busy, laNow); a != actNone {
		t.Fatalf("started without a token: %v", a)
	}
	if a, p := planActivity(Push{PushToStartToken: "pts"}, m, busy, laNow); a != actStart || p != 10 {
		t.Fatalf("got %v %d", a, p)
	}
	// Once only, while the app hasn't told us about it.
	if a, _ := planActivity(Push{PushToStartToken: "pts"}, m, busy, laNow.Add(time.Hour)); a != actNone {
		t.Fatalf("started twice: %v", a)
	}
	if a, _ := planActivity(Push{PushToStartToken: "pts"}, m, busy, laNow.Add(activityLifetime)); a != actStart {
		t.Fatalf("didn't start again after iOS's limit: %v", a)
	}
}

func TestUpdatesAreUrgentForStateAndSpacedForLines(t *testing.T) {
	p := Push{Activity: &ActivityToken{ID: "a1", Token: "tok", Since: laNow}}
	m := &activityMemo{}
	at := laNow
	c := buildContentState([]SessionSummary{working("a", time.Minute, "Read main.go")}, at)
	if a, pr := planActivity(p, m, c, at); a != actUpdate || pr != 10 {
		t.Fatalf("first: %v %d", a, pr)
	}
	at = at.Add(time.Second)
	c2 := c
	c2.UpdatedAt = at.Unix()
	if a, _ := planActivity(p, m, c2, at); a != actNone {
		t.Fatalf("nothing changed but the clock: %v", a)
	}
	line := buildContentState([]SessionSummary{working("a", time.Minute, "Edit main.go")}, at)
	if a, _ := planActivity(p, m, line, at); a != actNone {
		t.Fatalf("a new line went out within 15s: %v", a)
	}
	at = at.Add(minorPushEvery)
	if a, pr := planActivity(p, m, line, at); a != actUpdate || pr != 5 {
		t.Fatalf("a new line after 15s: %v %d", a, pr)
	}
	asked := buildContentState([]SessionSummary{waitingOn("a", time.Second)}, at)
	if a, pr := planActivity(p, m, asked, at.Add(time.Second)); a != actUpdate || pr != 10 {
		t.Fatalf("waiting: %v %d", a, pr)
	}
}

func TestAllDoneEndsAfterTwoMinutesUnlessWorkResumes(t *testing.T) {
	p := Push{Activity: &ActivityToken{ID: "a1", Token: "tok", Since: laNow}}
	m := &activityMemo{}
	quiet := buildContentState([]SessionSummary{finished("a", 0)}, laNow)
	busy := buildContentState([]SessionSummary{working("a", 0, "")}, laNow)
	if a, _ := planActivity(p, m, quiet, laNow); a != actUpdate {
		t.Fatalf("all done isn't shown: %v", a)
	}
	if a, _ := planActivity(p, m, busy, laNow.Add(time.Minute)); a != actUpdate {
		t.Fatalf("work resuming isn't shown: %v", a)
	}
	planActivity(p, m, quiet, laNow.Add(90*time.Second))
	if a, _ := planActivity(p, m, quiet, laNow.Add(3*time.Minute)); a != actNone {
		t.Fatalf("ended before two quiet minutes: %v", a)
	}
	if a, pr := planActivity(p, m, quiet, laNow.Add(90*time.Second+endAfter)); a != actEnd || pr != 10 {
		t.Fatalf("didn't end: %v %d", a, pr)
	}
}

func TestAnActivityPastIOSsLimitIsForgotten(t *testing.T) {
	p := Push{Activity: &ActivityToken{ID: "a1", Token: "tok", Since: laNow.Add(-activityLifetime)}}
	busy := buildContentState([]SessionSummary{working("a", 0, "")}, laNow)
	if a, _ := planActivity(p, &activityMemo{}, busy, laNow); a != actForget {
		t.Fatalf("got %v", a)
	}
}

// ── Through the daemon ─────────────────────────────────────────────────

func TestThePhonesActivityFollowsTheSessions(t *testing.T) {
	fake := newFakeAPNs(t)
	h := startRemoteDaemonWith(t, "", func(s *Server) { s.NewAPNs, s.manualActivities = fake.client, true })
	local := h.local(t)
	local.ok("apns.set", APNsSetParams{KeyPath: writeTestKey(t, t.TempDir()), KeyID: "KEY1234567", TeamID: "TEAM123456"}, nil)
	addr := remoteOn(t, local)
	phone, _ := h.dialRemote(t, addr, bearer(pairDevice(t, h, local, addr, "iPhone")))
	phone.hello()
	if code := local.fails("device.registerActivity", RegisterActivityParams{PushToStartToken: "pts"}); code != CodeBadRequest {
		t.Fatalf("local registerActivity: %s", code)
	}
	phone.ok("device.registerActivity", RegisterActivityParams{PushToStartToken: "pts", Sandbox: true, Theme: "nord"}, nil)

	var started StartResult
	local.ok("session.start", StartParams{CWD: h.dir}, &started)
	local.ok("session.send", SendParams{Session: started.Session.ID, Text: "run the tests"}, nil)
	h.srv.tickActivities(time.Now())
	got := fake.pushes()
	if len(got) != 1 || got[0].Token != "pts" || got[0].PushType != "liveactivity" ||
		got[0].Topic != DefaultAPNsTopic+".push-type.liveactivity" || got[0].Priority != "10" {
		t.Fatalf("pushes %+v", got)
	}
	aps := got[0].Payload["aps"].(map[string]any)
	attrs := aps["attributes"].(map[string]any)
	state := aps["content-state"].(map[string]any)
	if aps["event"] != "start" || aps["attributes-type"] != "SessionsActivityAttributes" || attrs["theme"] != "nord" ||
		state["working"] != float64(1) || state["lead"].(map[string]any)["line"] != "run the tests" {
		t.Fatalf("start payload %+v", aps)
	}

	// The app tells the daemon about the activity it now shows.
	phone.ok("device.registerActivity", RegisterActivityParams{Activity: &ActivityToken{ID: "act1", Token: "upd"}, Sandbox: true}, nil)
	h.srv.tickActivities(time.Now())
	got = fake.pushes()
	if len(got) != 2 || got[1].Token != "upd" || got[1].Payload["aps"].(map[string]any)["event"] != "update" {
		t.Fatalf("pushes %+v", got)
	}
	h.srv.tickActivities(time.Now())
	if n := len(fake.pushes()); n != 2 {
		t.Fatalf("pushed %d times for no change", n)
	}

	// An activity APNs calls gone is forgotten.
	fake.setGone("upd")
	local.ok("session.stop", SessionParams{Session: started.Session.ID}, nil)
	time.Sleep(50 * time.Millisecond)
	h.srv.tickActivities(time.Now())
	devs, _ := h.srv.Devices.List()
	if devs[0].Push.Activity != nil {
		t.Fatalf("kept a dead activity token: %+v", devs[0].Push)
	}

	// Ending it on purpose stops push-to-start too.
	phone.ok("device.endActivity", EndActivityParams{ID: "act1"}, nil)
	devs, _ = h.srv.Devices.List()
	if devs[0].Push.PushToStartToken != "" {
		t.Fatal("kept the push-to-start token after the phone ended its activity")
	}
}
