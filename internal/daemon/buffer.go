package daemon

// replayLimit is how many events a session keeps beyond its current turn.
const replayLimit = 2000

// entry is one buffered push, already encoded.
type entry struct {
	seq   int64
	frame []byte
	// stream marks a stream_event delta; parent is its parent_tool_use_id.
	stream bool
	parent string
	// sticky entries (the CLI's init and initialize answer) are never
	// evicted, so a client attaching late still learns the session's
	// commands, models and tools.
	sticky string
}

// buffer holds a session's recent events for replay: the current turn, plus
// the last replayLimit events.
type buffer struct {
	entries   []entry
	turnStart int64 // first seq of the current turn
	limit     int
}

func newBuffer() *buffer { return &buffer{limit: replayLimit} }

func (b *buffer) add(e entry) {
	if e.sticky != "" {
		for i := range b.entries {
			if b.entries[i].sticky == e.sticky {
				b.entries[i].sticky = "" // superseded
			}
		}
	}
	b.entries = append(b.entries, e)
	b.evict()
}

// endTurn marks everything up to and including seq as a finished turn.
func (b *buffer) endTurn(seq int64) {
	b.turnStart = seq + 1
	b.evict()
}

// dropDeltas removes the stream_event deltas of the message that just
// arrived complete, so a reconnecting client gets finished messages.
func (b *buffer) dropDeltas(parent string) {
	out := b.entries[:0]
	for _, e := range b.entries {
		if !(e.stream && e.parent == parent) {
			out = append(out, e)
		}
	}
	clear(b.entries[len(out):])
	b.entries = out
}

func (b *buffer) evict() {
	over := len(b.entries) - b.limit
	if over <= 0 {
		return
	}
	out := b.entries[:0]
	for _, e := range b.entries {
		if over > 0 && e.sticky == "" && e.seq < b.turnStart {
			over--
			continue
		}
		out = append(out, e)
	}
	clear(b.entries[len(out):])
	b.entries = out
}

// since returns the frames after seq (all of them when since is nil) and
// the seq replay starts at; next is the seq the next event will get. Sticky
// entries are replayed but don't count as the start: they outlive what
// came between them and the rest.
func (b *buffer) since(seq *int64, next int64) (frames [][]byte, from int64) {
	from = next
	found := false
	for _, e := range b.entries {
		if seq != nil && e.seq <= *seq {
			continue
		}
		if !found && e.sticky == "" {
			from, found = e.seq, true
		}
		frames = append(frames, e.frame)
	}
	return frames, from
}
