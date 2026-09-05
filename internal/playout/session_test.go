package playout

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/metrics"
)

// A fake encoder: a pipe we can write "video" into, wrapped in the same Process the real
// spawner returns. No ffmpeg — the session's job is refcounting and fan-out, and executing
// a real encoder to test a refcount would make these tests slow and flaky for no coverage.
type fakeEncoder struct {
	w       *os.File
	stopped chan struct{}
	once    sync.Once
}

// The getter keys by channel id; when a test drives two targets on one channel it should use
// newFakeSpawnerByKey instead, which distinguishes them. Kept channel-only here because the
// overwhelming majority of tests use one target and reading `get("ch1")` stays terse.
func newFakeSpawner(t *testing.T) (Spawner, func(string) *fakeEncoder) {
	t.Helper()
	spawn, byKey := newFakeSpawnerByKey(t)
	get := func(channelID string) *fakeEncoder { return byKey(channelID, PlanFull) }
	return spawn, get
}

// newFakeSpawnerByKey mirrors production's (channel, target) identity: two targets on one channel
// get two distinct fake encoders, so a test can prove they are separate sessions.
func newFakeSpawnerByKey(t *testing.T) (Spawner, func(string, EncodePlan) *fakeEncoder) {
	t.Helper()
	var mu sync.Mutex
	encoders := map[sessionKey]*fakeEncoder{}

	spawn := func(ctx context.Context, channelID string, target EncodePlan) (*Process, error) {
		pr, pw, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		fe := &fakeEncoder{w: pw, stopped: make(chan struct{})}
		mu.Lock()
		encoders[sessionKey{channel: channelID, plan: target}] = fe
		mu.Unlock()

		// Mimic Start's contract: the context is the lifetime. Cancelling it must end the
		// stream, which for the fake means closing the write end so pump sees EOF.
		go func() {
			<-ctx.Done()
			fe.once.Do(func() { _ = pw.Close(); close(fe.stopped) })
		}()

		return &Process{Stdout: pr}, nil
	}
	get := func(channelID string, target EncodePlan) *fakeEncoder {
		mu.Lock()
		defer mu.Unlock()
		return encoders[sessionKey{channel: channelID, plan: target}]
	}
	return spawn, get
}

// countingSpawner records how many encoders were started, for the race test.
func countingSpawner(t *testing.T) (Spawner, func() int) {
	t.Helper()
	inner, _ := newFakeSpawner(t)
	var mu sync.Mutex
	n := 0
	return func(ctx context.Context, channelID string, target EncodePlan) (*Process, error) {
			mu.Lock()
			n++
			mu.Unlock()
			// A real spawn is slow (ffmpeg init + a seek). Sleeping widens the race window
			// so a lock-scope bug fails reliably instead of one run in a thousand.
			time.Sleep(20 * time.Millisecond)
			return inner(ctx, channelID, target)
		}, func() int {
			mu.Lock()
			defer mu.Unlock()
			return n
		}
}

// testManager builds a Manager whose admission budget is a fixed number of concurrent TRANSCODES
// (§9.1 V49). Every cold session reserves one slot until its first block reports the real cost.
func testManager(t *testing.T, spawn Spawner, budget int, grace time.Duration) *Manager {
	t.Helper()
	m := NewManager(spawn, func() int { return budget }, grace, nil)
	t.Cleanup(m.Stop)
	return m
}

// The core model: ONE encoder serves N viewers. Anything else is both wasteful and
// incorrect — separate encodes of one channel would drift apart.
func TestAttach_OneEncoderServesManyViewers(t *testing.T) {
	spawn, started := countingSpawner(t)
	m := testManager(t, spawn, 4, time.Minute)

	for i := 0; i < 3; i++ {
		if _, _, err := m.Attach(context.Background(), "ch1", PlanFull); err != nil {
			t.Fatalf("viewer %d: %v", i, err)
		}
	}
	if n := started(); n != 1 {
		t.Errorf("started %d encoders for one channel, want 1", n)
	}
	if n := m.ActiveCount(); n != 1 {
		t.Errorf("ActiveCount = %d, want 1", n)
	}
}

func TestManagerPublishesSessionLifecycleToGenerationMetrics(t *testing.T) {
	spawn, _ := newFakeSpawner(t)
	recorder := metrics.New(metrics.Options{})
	manager := NewManager(spawn, func() int { return 4 }, time.Minute, nil).WithObserver(recorder)
	t.Cleanup(manager.Stop)

	_, _, err := manager.Attach(t.Context(), "private-channel-id", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	assertMetricsContain(t, recorder,
		`loomarr_playout_sessions_active 1`,
		`loomarr_playout_session_starts_total{result="success"} 1`,
	)

	manager.Stop()
	assertMetricsContain(t, recorder, `loomarr_playout_sessions_active 0`)
}

func TestManagerPublishesViewerDemandChanges(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 4, time.Minute)
	changes := make(chan struct{}, 8)
	m.OnChange(func() { changes <- struct{}{} })

	viewer, detach, err := m.Attach(t.Context(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	warmSession(t, viewer, encoder("ch1"))
	for len(changes) > 0 {
		<-changes
	}

	detach()
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("last-viewer release did not publish the grace-idle telemetry transition")
	}
}

func TestManagerPublishesParentProcessFailure(t *testing.T) {
	recorder := metrics.New(metrics.Options{})
	manager := NewManager(func(context.Context, string, EncodePlan) (*Process, error) {
		return nil, errors.New("ffmpeg refused to start")
	}, func() int { return 4 }, time.Minute, nil).WithObserver(recorder)

	if _, _, err := manager.Attach(t.Context(), "private-channel-id", PlanFull); err == nil {
		t.Fatal("Attach succeeded, want spawn failure")
	}
	assertMetricsContain(t, recorder,
		`loomarr_playout_session_starts_total{result="spawn_error"} 1`,
		`loomarr_playout_process_failures_total{stage="parent"} 1`,
	)
}

func assertMetricsContain(t *testing.T, recorder *metrics.Recorder, wants ...string) {
	t.Helper()
	response := httptest.NewRecorder()
	recorder.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range wants {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("generation scrape does not contain %q", want)
		}
	}
}

// Concurrent cold starts of DIFFERENT channels must run in PARALLEL, not serialize on the manager
// lock. The spawner here blocks until `want` spawns are in flight AT ONCE — only reachable if the
// manager holds no lock across the spawn. The old design (m.mu held across the spawn) would let only
// one spawn run at a time, the barrier would never fill, and this would hit the deadline and fail.
func TestAttach_DifferentChannelsStartConcurrently(t *testing.T) {
	const want = 4
	inner, _ := newFakeSpawner(t)
	inFlight := make(chan struct{}, want)
	release := make(chan struct{})
	spawn := func(ctx context.Context, channelID string, target EncodePlan) (*Process, error) {
		inFlight <- struct{}{} // announce this spawn is running
		<-release              // hold here until every spawn is confirmed concurrent
		return inner(ctx, channelID, target)
	}
	m := testManager(t, spawn, want, time.Minute)

	errs := make(chan error, want)
	for i := 0; i < want; i++ {
		ch := channelIDForIndex(i)
		go func() {
			_, _, err := m.Attach(context.Background(), ch, PlanFull)
			errs <- err
		}()
	}

	// All `want` spawns must be simultaneously in flight within the deadline.
	deadline := time.After(2 * time.Second)
	for i := 0; i < want; i++ {
		select {
		case <-inFlight:
		case <-deadline:
			t.Fatalf("only %d/%d channel starts ran concurrently — starts are serialized", i, want)
		}
	}
	close(release) // let them all finish

	for i := 0; i < want; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent attach: %v", err)
		}
	}
	if n := m.ActiveCount(); n != want {
		t.Errorf("ActiveCount = %d, want %d", n, want)
	}
}

func channelIDForIndex(i int) string { return "ch" + string(rune('a'+i)) }

// THE RACE (prior-art §6.3). Two viewers tuning the same channel simultaneously must not
// each start an encoder — the loser's would be orphaned with no viewers and no map entry,
// i.e. a leaked ffmpeg burning a core until the process dies.
func TestAttach_SimultaneousViewersDoNotStartTwoEncoders(t *testing.T) {
	spawn, started := countingSpawner(t)
	m := testManager(t, spawn, 4, time.Minute)

	const viewers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < viewers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release them all at once
			if _, _, err := m.Attach(context.Background(), "ch1", PlanFull); err != nil {
				t.Errorf("attach: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if n := started(); n != 1 {
		t.Errorf("started %d encoders for %d simultaneous viewers, want 1 — the "+
			"find-or-create is not atomic", n, viewers)
	}
}

func TestManager_StopChannelStopsEveryPlanAndLeavesOtherChannels(t *testing.T) {
	spawn, encoder := newFakeSpawnerByKey(t)
	m := testManager(t, spawn, 4, time.Minute)

	baseline, _, err := m.Attach(context.Background(), "ch1", PlanBaseline)
	if err != nil {
		t.Fatal(err)
	}
	hevc, _, err := m.Attach(context.Background(), "ch1", PlanHEVC10)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := m.Attach(context.Background(), "ch2", PlanFull)
	if err != nil {
		t.Fatal(err)
	}

	m.StopChannel("ch1")

	for name, stream := range map[string]<-chan []byte{"baseline": baseline, "hevc": hevc} {
		select {
		case _, ok := <-stream:
			if ok {
				t.Fatalf("%s viewer remained connected after StopChannel", name)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s viewer was not disconnected", name)
		}
	}
	for _, plan := range []EncodePlan{PlanBaseline, PlanHEVC10} {
		select {
		case <-encoder("ch1", plan).stopped:
		case <-time.After(time.Second):
			t.Fatalf("%s encoder was not stopped", plan)
		}
	}
	if got := m.ActiveCount(); got != 1 {
		t.Fatalf("ActiveCount after channel stop = %d, want only ch2", got)
	}
	select {
	case _, ok := <-other:
		if !ok {
			t.Fatal("stopping ch1 disconnected ch2")
		}
	default:
	}
}

func TestManager_StopChannelDuringSpawnRetiresLateProcess(t *testing.T) {
	inner, encoder := newFakeSpawnerByKey(t)
	spawned := make(chan struct{})
	release := make(chan struct{})
	spawn := func(ctx context.Context, channelID string, plan EncodePlan) (*Process, error) {
		close(spawned)
		<-release
		return inner(ctx, channelID, plan)
	}
	m := testManager(t, spawn, 4, time.Minute)

	result := make(chan error, 1)
	go func() {
		_, _, err := m.Attach(context.Background(), "ch1", PlanBaseline)
		result <- err
	}()
	<-spawned
	m.StopChannel("ch1")
	close(release)

	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Attach racing StopChannel error = %v, want context.Canceled", err)
	}
	select {
	case <-encoder("ch1", PlanBaseline).stopped:
	case <-time.After(time.Second):
		t.Fatal("encoder that completed after StopChannel was not retired")
	}
	if got := m.ActiveCount(); got != 0 {
		t.Fatalf("ActiveCount = %d after raced stop, want 0", got)
	}
}

// Every viewer gets every byte. A viewer that receives a SUBSET of the stream has a corrupt
// one, not a delayed one.
func TestAttach_AllViewersReceiveTheSameBytes(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 4, time.Minute)

	a, _, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := encoder("ch1").w.Write([]byte("mpegts-payload")); err != nil {
		t.Fatal(err)
	}
	for name, ch := range map[string]<-chan []byte{"a": a, "b": b} {
		select {
		case got := <-ch:
			if string(got) != "mpegts-payload" {
				t.Errorf("viewer %s got %q", name, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("viewer %s received nothing", name)
		}
	}
}

// The V47 invariant: a channel's session identity is (channel, TARGET). A browser viewer and a
// tuner viewer of the SAME channel get SEPARATE encoders — because their copy plans differ (HEVC
// copied for the tuner, transcoded for the browser) — and tearing one down leaves the other live.
//
// This is the whole reason the black-frame bug existed: before V47 both shared one session, so
// whatever codec the tuner chose (HEVC) reached the browser, which cannot decode it.
func TestAttach_TargetForksTheSession(t *testing.T) {
	spawn, byKey := newFakeSpawnerByKey(t)
	m := testManager(t, spawn, 4, time.Minute)

	_, detachBrowser, err := m.Attach(context.Background(), "ch1", PlanBaseline)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}

	// Two distinct encoders for one channel — the fake keys by (channel, target), so both being
	// non-nil and distinct proves two sessions were started, not one shared.
	browserEnc, tunerEnc := byKey("ch1", PlanBaseline), byKey("ch1", PlanFull)
	if browserEnc == nil || tunerEnc == nil {
		t.Fatalf("expected an encoder per target, got browser=%v tuner=%v", browserEnc, tunerEnc)
	}
	if browserEnc == tunerEnc {
		t.Fatal("browser and tuner must be SEPARATE encoders, not one shared session")
	}
	if got := m.ActiveCount(); got != 2 {
		t.Fatalf("ActiveCount = %d, want 2 (one per target)", got)
	}

	// The two sessions are addressed independently.
	if m.session("ch1", PlanBaseline) == m.session("ch1", PlanFull) {
		t.Fatal("session(ch1, browser) and session(ch1, mediaserver) must be different sessions")
	}

	// Tearing down the browser viewer leaves the tuner session live (immediate teardown: grace is a
	// minute here, but the browser session had exactly one viewer, so detaching arms grace, not a
	// stop — the tuner session is untouched regardless).
	detachBrowser()
	if m.session("ch1", PlanFull) == nil {
		t.Fatal("detaching the browser viewer must not tear down the tuner session")
	}
}

// A stalled viewer must not freeze the channel for everyone else. Unlike the events bus
// (which drops the EVENT because the store is truth on reconnect), playout drops the
// VIEWER — there is no re-read for a byte stream, and blocking would punish the innocent.
//
// The fast viewer drains CONCURRENTLY with the writes, which is the only way to state this
// invariant honestly. An earlier version of this test wrote a burst first and read
// afterwards, then asserted "N writes ⇒ N chunks received" — false twice over: the OS pipe
// buffer coalesces small writes so one Read returns many of them, and a viewer nobody is
// draining is *correctly* dropped. It failed against correct code.
func TestBroadcast_SlowViewerIsDroppedNotBlocking(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 4, time.Minute)

	slow, _, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	fast, _, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	_ = slow // deliberately never drained — this is the stalled TV

	// The fast viewer keeps up throughout. If a stalled viewer could block the encoder,
	// this goroutine starves and the assertion below fires.
	received := make(chan int, 1)
	go func() {
		n := 0
		for range fast {
			n++
			received <- n
		}
		close(received)
	}()

	// Enough data to overrun the slow viewer's buffer many times over. 64 KiB per write so
	// each one is a distinct chunk rather than being coalesced with its neighbours.
	payload := make([]byte, 64*1024)
	w := encoder("ch1").w
	go func() {
		for i := 0; i < viewerBuffer*4; i++ {
			if _, err := w.Write(payload); err != nil {
				return // the pipe closed; the assertions below report the real problem
			}
		}
	}()

	// The property: the fast viewer keeps receiving well past the point at which the slow
	// one's buffer was exhausted.
	want := viewerBuffer * 2
	deadline := time.After(10 * time.Second)
	for {
		select {
		case n, ok := <-received:
			if !ok {
				t.Fatal("the fast viewer was closed; the slow one should have been dropped instead")
			}
			if n >= want {
				return // kept up past the slow viewer's capacity — invariant holds
			}
		case <-deadline:
			t.Fatalf("fast viewer starved before %d chunks — a slow viewer blocked the encoder", want)
		}
	}
}

// And the stalled viewer is actually dropped, rather than left accumulating memory. When it was
// the last viewer, the session must enter ordinary warm grace instead of leaking forever with an
// empty viewer map.
func TestBroadcast_StalledLastViewerIsClosedAndSessionEntersGrace(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 4, 50*time.Millisecond)

	stalled, _, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}

	payload := make([]byte, 64*1024)
	w := encoder("ch1").w
	go func() {
		for i := 0; i < viewerBuffer*4; i++ {
			if _, err := w.Write(payload); err != nil {
				return
			}
		}
	}()

	// Read nothing for a while, then drain: the channel must be closed, not merely full.
	time.Sleep(200 * time.Millisecond)
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-stalled:
			if !ok {
				goto viewerClosed
			}
		case <-deadline:
			t.Fatal("a viewer that stopped reading was never dropped")
		}
	}

viewerClosed:
	select {
	case <-encoder("ch1").stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("session with only a dropped viewer never stopped after warm grace")
	}
}

// HLS is an in-process remux sink, not a network viewer. A warm parent can release its finite
// readrate burst faster than a goroutine can cross the normal eight-chunk viewer mailbox; the sink
// must therefore be offered every chunk synchronously while retaining its own bounded queue.
func TestAttachSink_PreservesWarmStartupBurst(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 4, time.Minute)
	relay := newHLSRelay()
	lease, err := m.AttachSink(context.Background(), "ch1", PlanFull, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	const writes = viewerBuffer * 4
	payload := make([]byte, 64*1024)
	written := make(chan error, 1)
	go func() {
		for range writes {
			if _, err := encoder("ch1").w.Write(payload); err != nil {
				written <- err
				return
			}
		}
		written <- nil
	}()

	want := writes * len(payload)
	received := make(chan int, 1)
	go func() {
		total := 0
		for total < want {
			chunk, err := relay.next()
			if err != nil {
				received <- -1
				return
			}
			total += len(chunk)
		}
		received <- total
	}()

	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("encoder stalled while delivering to the HLS sink")
	}
	select {
	case got := <-received:
		if got != want {
			t.Fatalf("HLS sink received %d bytes, want %d without a mailbox drop", got, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("HLS sink did not receive the complete startup burst")
	}
}

// The admission bound (§9.1 V49 — COST-AWARE). viewra EVICTED an existing watched session to make
// room (prior-art viewra §1); for playout that means one person tuning in kills someone else's
// channel. We refuse the newcomer when every costly session has active demand. ⚠ The bound counts
// concurrent TRANSCODES. Cold sessions reserve conservatively and copy sessions release below.
func TestAttach_AtCapacityRefusesRatherThanEvicting(t *testing.T) {
	spawn, _ := newFakeSpawner(t)
	m := testManager(t, spawn, 2, time.Minute) // budget = 2 concurrent transcodes

	first, _, err := m.Attach(context.Background(), "ch1", PlanBaseline)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = m.Attach(context.Background(), "ch2", PlanBaseline); err != nil {
		t.Fatal(err)
	}

	_, _, err = m.Attach(context.Background(), "ch3", PlanBaseline)
	if err == nil {
		t.Fatal("a third transcode was admitted past a budget of 2")
	}
	if err != ErrAtCapacity {
		t.Errorf("err = %v, want ErrAtCapacity so the API can render an actionable 503", err)
	}

	// The existing channels are untouched — nothing was evicted.
	if n := m.ActiveCount(); n != 2 {
		t.Errorf("ActiveCount = %d, want 2", n)
	}
	select {
	case _, ok := <-first:
		if !ok {
			t.Error("an existing viewer was disconnected to make room — that is eviction")
		}
	default: // no bytes pending is fine; we only care that it is not closed
	}

	// A viewer already attached to an admitted channel is still fine, and attaching
	// ANOTHER viewer to an existing channel must not be refused — the cap is on encoders,
	// not on people watching.
	if _, _, err := m.Attach(context.Background(), "ch1", PlanBaseline); err != nil {
		t.Errorf("a second viewer on an admitted channel was refused: %v", err)
	}
}

func TestAttach_PreparedCostEstimatorAdmitsCopySessionsBeyondTranscodeBudget(t *testing.T) {
	spawn, _ := newFakeSpawner(t)
	m := testManager(t, spawn, 1, time.Minute).WithCostEstimator(
		func(context.Context, string, EncodePlan) int { return 0 },
	)

	for _, channel := range []string{"ch1", "ch2", "ch3"} {
		if _, _, err := m.Attach(t.Context(), channel, PlanFull); err != nil {
			t.Fatalf("prepared copy session %s was refused by transcode budget: %v", channel, err)
		}
	}
	if got := m.ActiveCount(); got != 3 {
		t.Fatalf("active prepared sessions = %d, want 3", got)
	}
}

func TestAdmitProgram_BoundsPreparedSessionsThatFallBackToTranscoding(t *testing.T) {
	spawn, _ := newFakeSpawner(t)
	m := testManager(t, spawn, 1, time.Minute).WithCostEstimator(
		func(context.Context, string, EncodePlan) int { return 0 },
	)
	for _, channel := range []string{"ch1", "ch2"} {
		if _, _, err := m.Attach(t.Context(), channel, PlanFull); err != nil {
			t.Fatal(err)
		}
	}

	if !m.AdmitProgram("ch1", PlanFull, true) {
		t.Fatal("first prepared-to-live transcode was refused with an empty budget")
	}
	if m.AdmitProgram("ch2", PlanFull, true) {
		t.Fatal("second prepared-to-live transcode oversubscribed the one-slot budget")
	}
	if !m.AdmitProgram("ch1", PlanFull, false) {
		t.Fatal("returning to a prepared copy block did not release capacity")
	}
	if !m.AdmitProgram("ch2", PlanFull, true) {
		t.Fatal("released transcode capacity was not reusable by the waiting session")
	}
}

// A grace-idle session is retained only to make a likely return tune cheap. It must not reserve
// the final transcode slot against a foreground tune: when the budget is full, reclaim the oldest
// zero-viewer session and preserve the newer warm candidate. Sessions with viewers remain protected
// by TestAttach_AtCapacityRefusesRatherThanEvicting above.
func TestAttach_AtCapacityReclaimsOldestIdleSession(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 2, time.Minute)

	first, detachFirst, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	warmSession(t, first, encoder("ch1"))
	detachFirst()

	second, detachSecond, err := m.Attach(context.Background(), "ch2", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	warmSession(t, second, encoder("ch2"))
	detachSecond()

	if _, _, err := m.Attach(context.Background(), "ch3", PlanFull); err != nil {
		t.Fatalf("foreground tune was refused while reclaimable idle work held the budget: %v", err)
	}

	select {
	case <-encoder("ch1").stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("oldest grace-idle encoder was not reclaimed")
	}
	select {
	case <-encoder("ch2").stopped:
		t.Fatal("newer grace-idle encoder was reclaimed before the oldest")
	default:
	}
	if n := m.ActiveCount(); n != 2 {
		t.Fatalf("ActiveCount = %d, want the newer idle session plus foreground session", n)
	}
}

// A copy session releases its conservative cold reservation as soon as the first block proves copy.
// This preserves cost-aware capacity without optimistically over-admitting an HEVC/full session
// whose first source may still require format conformance.
func TestAttach_CopySessionsDoNotConsumeBudget(t *testing.T) {
	spawn, _ := newFakeSpawner(t)
	m := testManager(t, spawn, 1, time.Minute) // budget = 1 transcode

	// The HEVC session reserves one while cold, then proves its first block is video-copy.
	if _, _, err := m.Attach(context.Background(), "ch1", PlanHEVC8); err != nil {
		t.Fatal(err)
	}
	m.ReportProgram("ch1", PlanHEVC8, EncoderSoftwareHEVC, false, Progress{})
	// Its released slot admits one real baseline transcode, while a third cold session is refused.
	if _, _, err := m.Attach(context.Background(), "ch2", PlanBaseline); err != nil {
		t.Errorf("copy session retained a transcode slot: %v", err)
	}
	if _, _, err := m.Attach(context.Background(), "ch3", PlanFull); err != ErrAtCapacity {
		t.Fatalf("third cold session err = %v, want ErrAtCapacity", err)
	}
}

// The transcode budget and the warm-session footprint are different limits. Fifty copy-compatible
// Channels cost no transcode slots after their first program reports, but retaining all fifty would
// still retain fifty parent processes and (for Watch) fifty HLS remuxes. The Manager keeps only the
// two most recently viewed idle sessions beside the current viewer-active Channel.
func TestWarmIdleHotSetBoundsFiftyChannelSurf(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 0, time.Minute)

	var previousDetach func()
	for i := range 50 {
		channelID := fmt.Sprintf("ch-%02d", i)
		viewer, detach, err := m.Attach(t.Context(), channelID, PlanFull)
		if err != nil {
			t.Fatalf("attach %s: %v", channelID, err)
		}
		warmSession(t, viewer, encoder(channelID))
		m.ReportProgram(channelID, PlanFull, EncoderSoftware, false, Progress{})
		if previousDetach != nil {
			previousDetach()
		}
		previousDetach = detach
	}

	stats := m.Stats(time.Now())
	active, idle := 0, 0
	for _, stat := range stats {
		if stat.Viewers > 0 {
			active++
		} else {
			idle++
		}
	}
	if active != 1 || idle != 2 || len(stats) != 3 {
		t.Fatalf("sessions = %d active / %d idle / %d total, want 1 / 2 / 3", active, idle, len(stats))
	}
	for i := range 47 {
		channelID := fmt.Sprintf("ch-%02d", i)
		select {
		case <-encoder(channelID).stopped:
		case <-time.After(2 * time.Second):
			t.Fatalf("displaced idle session %s was not stopped", channelID)
		}
	}
	for _, channelID := range []string{"ch-47", "ch-48", "ch-49"} {
		select {
		case <-encoder(channelID).stopped:
			t.Fatalf("retained hot-set session %s was stopped", channelID)
		default:
		}
	}
}

// A configured Channel is catalog state, not viewer demand. The session Manager deliberately has
// no inventory-loading API: even diagnostics may inspect every configured Channel without lazily
// starting its parent media process. This pins the 50+ Channel idle-host contract at the process
// ownership seam instead of relying on a composition test that could only count session rows.
func TestManagerConfiguredChannelsWithoutViewersStartNoMediaProcesses(t *testing.T) {
	spawn, started := countingSpawner(t)
	m := testManager(t, spawn, 0, time.Minute)

	for i := range 64 {
		channelID := fmt.Sprintf("configured-%02d", i)
		if runID := m.ProcessRunID(channelID, PlanFull); runID != "" {
			t.Fatalf("configured Channel %s unexpectedly has process %q", channelID, runID)
		}
	}

	if got := started(); got != 0 {
		t.Fatalf("64 configured Channels with no viewer demand started %d media processes, want 0", got)
	}
	if stats := m.Stats(time.Now()); len(stats) != 0 {
		t.Fatalf("64 configured Channels with no viewer demand created %d live sessions, want 0", len(stats))
	}
}

func TestWarmIdleHotSetIncludesCopyAndTranscodeSessions(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 0, time.Minute)

	for i, transcoding := range []bool{false, true, false} {
		channelID := fmt.Sprintf("ch-%d", i)
		viewer, detach, err := m.Attach(t.Context(), channelID, PlanFull)
		if err != nil {
			t.Fatal(err)
		}
		warmSession(t, viewer, encoder(channelID))
		m.ReportProgram(channelID, PlanFull, EncoderSoftware, transcoding, Progress{})
		detach()
		time.Sleep(time.Millisecond)
	}

	if got := m.ActiveCount(); got != 2 {
		t.Fatalf("live sessions = %d, want two idle sessions", got)
	}
	select {
	case <-encoder("ch-0").stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("oldest copy session was exempted from the aggregate idle bound")
	}
}

func TestWarmIdleHotSetUsesMostRecentViewNotOriginalStart(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 0, time.Minute)

	for _, channelID := range []string{"ch1", "ch2"} {
		viewer, detach, err := m.Attach(t.Context(), channelID, PlanFull)
		if err != nil {
			t.Fatal(err)
		}
		warmSession(t, viewer, encoder(channelID))
		m.ReportProgram(channelID, PlanFull, EncoderSoftware, false, Progress{})
		detach()
		time.Sleep(time.Millisecond)
	}

	// Rejoining ch1 makes it the most recently viewed idle session without spawning again.
	original := encoder("ch1")
	_, detach, err := m.Attach(t.Context(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	if encoder("ch1") != original {
		t.Fatal("reattaching a retained session spawned a new encoder")
	}
	detach()
	time.Sleep(time.Millisecond)

	viewer3, detach3, err := m.Attach(t.Context(), "ch3", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	warmSession(t, viewer3, encoder("ch3"))
	m.ReportProgram("ch3", PlanFull, EncoderSoftware, false, Progress{})
	detach3()

	select {
	case <-encoder("ch2").stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("least-recently-viewed idle session was not stopped")
	}
	select {
	case <-encoder("ch1").stopped:
		t.Fatal("recently reattached session was stopped before an older idle session")
	default:
	}
}

func TestWarmIdleHotSetStaleCloseCallbackCannotForgetReplacement(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 1, time.Minute)

	viewer, detach, err := m.Attach(t.Context(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	warmSession(t, viewer, encoder("ch1"))
	detach()

	old := m.session("ch1", PlanFull)
	old.mu.Lock()
	idleGeneration := old.idleGeneration
	originalOnClosed := old.onClosed
	closeEntered := make(chan struct{})
	allowCallback := make(chan struct{})
	old.onClosed = func() {
		close(closeEntered)
		<-allowCallback
		originalOnClosed()
	}
	old.mu.Unlock()

	closed := make(chan bool, 1)
	go func() { closed <- old.closeIfIdle(idleGeneration) }()
	<-closeEntered

	// Attach observes the old session as closed, removes that exact pointer, and installs a
	// replacement before the old close callback resumes.
	replacementViewer, replacementDetach, err := m.Attach(t.Context(), "ch1", PlanFull)
	if err != nil {
		close(allowCallback)
		t.Fatal(err)
	}
	defer replacementDetach()
	replacement := m.session("ch1", PlanFull)
	if replacement == nil || replacement == old {
		close(allowCallback)
		t.Fatal("foreground attach did not install a replacement session")
	}
	warmSession(t, replacementViewer, encoder("ch1"))

	close(allowCallback)
	if ok := <-closed; !ok {
		t.Fatal("selected exact idle generation was not closed")
	}
	if got := m.session("ch1", PlanFull); got != replacement {
		t.Fatal("stale close callback forgot the replacement session")
	}
	if got := m.ActiveCount(); got != 1 {
		t.Fatalf("live sessions = %d after stale callback, want replacement only", got)
	}
}

func TestWarmIdleHotSetConcurrentDetachesRemainBounded(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 0, time.Minute)

	detaches := make([]func(), 0, 50)
	for i := range 50 {
		channelID := fmt.Sprintf("ch-%02d", i)
		viewer, detach, err := m.Attach(t.Context(), channelID, PlanFull)
		if err != nil {
			t.Fatal(err)
		}
		warmSession(t, viewer, encoder(channelID))
		m.ReportProgram(channelID, PlanFull, EncoderSoftware, false, Progress{})
		detaches = append(detaches, detach)
	}

	var wg sync.WaitGroup
	for _, detach := range detaches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			detach()
		}()
	}
	wg.Wait()

	if got := m.ActiveCount(); got != 2 {
		t.Fatalf("live sessions after concurrent detaches = %d, want 2", got)
	}
	for _, stat := range m.Stats(time.Now()) {
		if stat.Viewers != 0 {
			t.Fatalf("session %s retained %d viewers after every detach", stat.ChannelID, stat.Viewers)
		}
	}
}

func TestWarmIdleHotSetNeverEvictsActiveViewers(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 0, time.Minute)

	for i := range 4 {
		channelID := fmt.Sprintf("active-%d", i)
		viewer, _, err := m.Attach(t.Context(), channelID, PlanFull)
		if err != nil {
			t.Fatal(err)
		}
		warmSession(t, viewer, encoder(channelID))
		m.ReportProgram(channelID, PlanFull, EncoderSoftware, false, Progress{})
	}
	for i := range 3 {
		channelID := fmt.Sprintf("idle-%d", i)
		viewer, detach, err := m.Attach(t.Context(), channelID, PlanFull)
		if err != nil {
			t.Fatal(err)
		}
		warmSession(t, viewer, encoder(channelID))
		m.ReportProgram(channelID, PlanFull, EncoderSoftware, false, Progress{})
		detach()
	}

	stats := m.Stats(time.Now())
	active, idle := 0, 0
	for _, stat := range stats {
		if stat.Viewers > 0 {
			active++
		} else {
			idle++
		}
	}
	if active != 4 || idle != 2 {
		t.Fatalf("sessions = %d active / %d idle, want 4 / 2", active, idle)
	}
	for i := range 4 {
		channelID := fmt.Sprintf("active-%d", i)
		select {
		case <-encoder(channelID).stopped:
			t.Fatalf("viewer-active session %s was evicted", channelID)
		default:
		}
	}
}

// An unset cap must not block playout — see AtCapacity.
func TestAttach_UnconfiguredCapDoesNotBlock(t *testing.T) {
	spawn, _ := newFakeSpawner(t)
	m := testManager(t, spawn, 0, time.Minute)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if _, _, err := m.Attach(context.Background(), id, PlanFull); err != nil {
			t.Fatalf("channel %s refused with no cap configured: %v", id, err)
		}
	}
}

// When the encoder dies, viewers must be CLOSED, not left parked forever. A tuner handler
// blocked on a channel receive learns the stream ended only this way.
func TestSession_EncoderExitDisconnectsViewers(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 4, time.Minute)

	v, _, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	_ = encoder("ch1").w.Close() // encoder exits → pump sees EOF

	select {
	case _, ok := <-v:
		if ok {
			t.Error("expected the viewer channel to be closed when the encoder exits")
		}
	case <-time.After(2 * time.Second):
		t.Error("viewer was left parked after the encoder exited")
	}
}

// Detach must be idempotent: a handler may reasonably call it from a defer AND on an error
// path, and a double-decrement would tear down a channel other people are watching.
func TestDetach_IsIdempotent(t *testing.T) {
	spawn, _ := newFakeSpawner(t)
	m := testManager(t, spawn, 4, time.Minute)

	if _, _, err := m.Attach(context.Background(), "ch1", PlanFull); err != nil {
		t.Fatal(err)
	}
	_, detach, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}

	detach()
	detach()
	detach()

	s := m.session("ch1", PlanFull)
	if s == nil {
		t.Fatal("session gone")
	}
	if n := s.ViewerCount(); n != 1 {
		t.Errorf("ViewerCount = %d, want 1 — repeated detach double-decremented", n)
	}
}

// Shutdown must stop every encoder. A live encoder never exits on its own (process.go), so
// without this they outlive the process that started them.
func TestManagerStop_TearsDownEveryEncoder(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := NewManager(spawn, func() int { return 4 }, time.Minute, nil)

	for _, id := range []string{"ch1", "ch2"} {
		if _, _, err := m.Attach(context.Background(), id, PlanFull); err != nil {
			t.Fatal(err)
		}
	}
	m.Stop()

	for _, id := range []string{"ch1", "ch2"} {
		select {
		case <-encoder(id).stopped:
		case <-time.After(2 * time.Second):
			t.Errorf("%s: encoder was not stopped by Manager.Stop", id)
		}
	}
	if n := m.ActiveCount(); n != 0 {
		t.Errorf("ActiveCount = %d after Stop, want 0", n)
	}
}

// --- Grace-period teardown (onIdle) ---------------------------------------------------
//
// These four cover the behaviour onIdle owes. They fail until it is implemented.

func warmSession(t *testing.T, viewer <-chan []byte, encoder *fakeEncoder) {
	t.Helper()
	if _, err := encoder.w.Write([]byte("warm")); err != nil {
		t.Fatal(err)
	}
	select {
	case chunk, ok := <-viewer:
		if !ok || string(chunk) != "warm" {
			t.Fatalf("warming chunk = %q, open=%v", chunk, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session produced no warming transport")
	}
}

// Grace retains a WARM channel, not a parent that has never emitted transport. Keeping a zero-byte
// full session alive after the waiting tuner gives up would retain its conservative cost and make
// the baseline recovery fail at capacity on a one-channel machine.
func TestOnIdle_ZeroByteSessionStopsImmediatelyAndReleasesCapacity(t *testing.T) {
	spawn, encoder := newFakeSpawnerByKey(t)
	m := testManager(t, spawn, 1, time.Minute)

	_, detach, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	detach()

	select {
	case <-encoder("ch1", PlanFull).stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("zero-byte full session remained in grace and retained its capacity slot")
	}
	if _, _, err := m.Attach(context.Background(), "ch1", PlanBaseline); err != nil {
		t.Fatalf("baseline recovery after failed full session: %v", err)
	}
}

// The last viewer leaving must NOT stop the encoder immediately — that is what makes
// channel surfing cheap.
func TestOnIdle_EncoderSurvivesBrieflyAfterTheLastViewerLeaves(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 4, 10*time.Second)

	v, detach, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	warmSession(t, v, encoder("ch1"))
	detach()

	select {
	case <-encoder("ch1").stopped:
		t.Fatal("the encoder stopped immediately; the grace period should keep it alive")
	case <-time.After(200 * time.Millisecond):
	}
}

// After the grace period with nobody watching, it must actually stop. Otherwise an
// abandoned channel burns a core forever.
func TestOnIdle_EncoderStopsAfterTheGracePeriod(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 4, 50*time.Millisecond)

	v, detach, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	warmSession(t, v, encoder("ch1"))
	detach()

	select {
	case <-encoder("ch1").stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("the encoder never stopped after the grace period expired")
	}
}

// A viewer returning inside the grace window must ABORT the teardown. This is the bug in
// the naive `time.AfterFunc(grace, close)`: the timer fires and kills a channel someone is
// now watching.
func TestOnIdle_ReconnectInsideGraceAbortsTeardown(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 4, 300*time.Millisecond)

	initial, detach, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	warmSession(t, initial, encoder("ch1"))
	detach()

	time.Sleep(50 * time.Millisecond) // still inside the grace window
	v, _, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatalf("reattach inside the grace window: %v", err)
	}

	// Well past the original deadline: the returning viewer must still be connected.
	time.Sleep(500 * time.Millisecond)
	select {
	case _, ok := <-v:
		if !ok {
			t.Fatal("the grace timer tore down a session that a viewer had rejoined")
		}
	default:
	}
	select {
	case <-encoder("ch1").stopped:
		t.Fatal("the encoder was stopped despite a viewer reconnecting inside the grace window")
	default:
	}
}

// The ABA problem. Viewers leave and rejoin repeatedly; each idle period arms a timer, and
// an early timer must not tear down a LATER session (or a later live viewer) just because
// it holds the same channel key.
func TestOnIdle_StaleTimerDoesNotKillALaterViewer(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 4, 200*time.Millisecond)

	// Three quick join/leave cycles, each arming a grace timer.
	for i := 0; i < 3; i++ {
		viewer, detach, err := m.Attach(context.Background(), "ch1", PlanFull)
		if err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		warmSession(t, viewer, encoder("ch1"))
		time.Sleep(30 * time.Millisecond)
		detach()
		time.Sleep(30 * time.Millisecond)
	}

	// A viewer settles in for the long haul.
	v, _, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}

	// Let every earlier timer's deadline pass.
	time.Sleep(600 * time.Millisecond)

	select {
	case _, ok := <-v:
		if !ok {
			t.Fatal("a stale grace timer disconnected a live viewer")
		}
	default:
	}
	select {
	case <-encoder("ch1").stopped:
		t.Fatal("a stale grace timer stopped an encoder that has a viewer")
	default:
	}
}

// A stale timer must not shorten a LATER idle interval either. Checking only that the viewer map is
// empty is insufficient: after leave → reattach → leave, the first timer sees zero viewers and can
// close the session before the second idle interval has received its full grace period.
func TestOnIdle_StaleTimerDoesNotShortenLaterIdleGrace(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 4, 400*time.Millisecond)

	initial, detachInitial, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	warmSession(t, initial, encoder("ch1"))
	detachInitial()

	time.Sleep(250 * time.Millisecond)
	_, detachAgain, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatalf("reattach inside first grace interval: %v", err)
	}
	detachAgain()

	// The first timer has fired, but the second idle interval still has substantial grace left.
	time.Sleep(220 * time.Millisecond)
	select {
	case <-encoder("ch1").stopped:
		t.Fatal("the first idle timer shortened the later idle interval")
	default:
	}

	select {
	case <-encoder("ch1").stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the current idle generation never stopped after its own grace period")
	}
}

// A session torn down by the grace period must be removed from the manager, so the next
// viewer starts a fresh encoder rather than attaching to a dead one and waiting forever.
func TestOnIdle_TornDownSessionIsReplacedOnNextAttach(t *testing.T) {
	spawn, encoder := newFakeSpawner(t)
	m := testManager(t, spawn, 4, 50*time.Millisecond)

	initial, detach, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatal(err)
	}
	warmSession(t, initial, encoder("ch1"))
	detach()
	select {
	case <-encoder("ch1").stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("encoder never stopped")
	}

	// A new viewer must get a working stream.
	v, _, err := m.Attach(context.Background(), "ch1", PlanFull)
	if err != nil {
		t.Fatalf("attach after teardown: %v", err)
	}
	if _, err := encoder("ch1").w.Write([]byte("fresh")); err != nil {
		t.Fatal(err)
	}
	select {
	case got, ok := <-v:
		if !ok {
			t.Fatal("attached to a dead session")
		}
		if string(got) != "fresh" {
			t.Errorf("got %q, want %q", got, "fresh")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no bytes from the replacement encoder")
	}
}
