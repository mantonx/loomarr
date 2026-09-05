package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/loomarr/loomarr/internal/library"
	"github.com/loomarr/loomarr/internal/playout"
	"github.com/loomarr/loomarr/internal/prepared"
	"github.com/loomarr/loomarr/internal/schedule"
	"github.com/loomarr/loomarr/internal/store"
)

type preparedChannels struct{ channels []store.Channel }

func (s preparedChannels) ListChannels(context.Context) ([]store.Channel, error) {
	return s.channels, nil
}

func (s preparedChannels) GetChannel(_ context.Context, id string) (store.Channel, error) {
	for _, channel := range s.channels {
		if channel.ID == id {
			return channel, nil
		}
	}
	return store.Channel{}, store.ErrNotFound
}

type preparedTimelineFake struct {
	broadcasts          map[string][]playout.Broadcast
	audioTrackByChannel map[string]int
	inventoryAudio      bool
	audioCalls          int
	inventoryAudioCalls int
	lastFrom            time.Time
	lastTo              time.Time
}

func (f *preparedTimelineFake) ScheduledBroadcasts(
	_ context.Context, channelID string, from, to time.Time,
) ([]playout.Broadcast, error) {
	f.lastFrom, f.lastTo = from, to
	return f.broadcasts[channelID], nil
}

func (f *preparedTimelineFake) AudioTrackFor(_ context.Context, channelID, _, _ string) int {
	f.audioCalls++
	if track, ok := f.audioTrackByChannel[channelID]; ok {
		return track
	}
	return 2
}

func (f *preparedTimelineFake) AudioTrackFromInventory(
	_ context.Context, channelID, _ string,
) (int, bool) {
	f.inventoryAudioCalls++
	if !f.inventoryAudio {
		return 0, false
	}
	if track, ok := f.audioTrackByChannel[channelID]; ok {
		return track, true
	}
	return 2, true
}

type preparedInputsFake struct {
	sources          map[string]library.InputSource
	inventorySources map[string]library.InputSource
	revisions        map[string]string
	current          map[prepared.Source]bool
	calls            int
	inventoryCalls   int
	currentChecks    int
}

func (f *preparedInputsFake) ResolvePreparedSourceFromInventory(
	_ context.Context, itemID string, _ library.PathMap,
) (prepared.Source, string, bool) {
	f.inventoryCalls++
	input, ok := f.inventorySources[itemID]
	if !ok || input.URL == "" {
		return prepared.Source{}, "", false
	}
	return preparedSource(itemID, 0), input.URL, true
}

func (f *preparedInputsFake) ResolvePreparedSource(
	_ context.Context, itemID string, _ library.PathMap,
) (prepared.Source, string, bool) {
	f.calls++
	input, ok := f.sources[itemID]
	if !ok || input.URL == "" {
		return prepared.Source{}, "", false
	}
	source := preparedSource(itemID, 0)
	if revision := f.revisions[itemID]; revision != "" {
		source.Revision = revision
	}
	return source, input.URL, true
}

func (f *preparedInputsFake) PreparedSourceCurrent(_ context.Context, source prepared.Source) bool {
	f.currentChecks++
	if f.current == nil {
		return true
	}
	return f.current[source]
}

func preparedSource(itemID string, audioTrack int) prepared.Source {
	return prepared.Source{
		ItemID: "inventory-item-" + itemID, SourceID: "inventory-source-" + itemID,
		Revision: "revision-" + itemID, AudioTrack: audioTrack,
	}
}

type preparedLookupFake struct {
	hits map[prepared.Request]prepared.Specification
}

func (f preparedLookupFake) Lookup(request prepared.Request) (prepared.Specification, bool, error) {
	spec, ok := f.hits[request]
	return spec, ok, nil
}

func newPreparedRuntimeForTest(
	t *testing.T,
	channels preparedChannelReader,
	timeline preparedTimeline,
	inputs preparedSourceResolver,
	lookup preparedLookup,
	now func() time.Time,
	pathMap func() library.PathMap,
	policy func() string,
	globalBackend func() string,
	rendition func() prepared.RenditionContract,
) *preparedRuntimeResolver {
	t.Helper()
	preparedLibrary, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := prepared.OpenReadiness(preparedLibrary)
	if err != nil {
		t.Fatal(err)
	}
	return newPreparedRuntimeResolver(preparedRuntimeDependencies{
		Channels: channels, Timeline: timeline, Sources: inputs, Lookup: lookup, Now: now,
		PathMap: pathMap, Policy: policy, GlobalBackend: globalBackend,
		Rendition: rendition, Readiness: readiness,
	})
}

func TestPreparedRuntimeCandidatesUseOnlyInternalSchedule(t *testing.T) {
	t.Parallel()
	now := time.Unix(10_000, 0)
	timeline := &preparedTimelineFake{broadcasts: map[string][]playout.Broadcast{
		"internal": {{Kind: schedule.SlotProgram, LibraryItemID: "shared", Start: now.Add(-time.Minute), Stop: now.Add(time.Hour)}},
		"tunarr":   {{Kind: schedule.SlotProgram, LibraryItemID: "remote", Start: now, Stop: now.Add(time.Hour)}},
	}}
	inputs := &preparedInputsFake{sources: map[string]library.InputSource{
		"shared": {URL: "/media/shared.mkv", Kind: library.InputFile},
		"remote": {URL: "http://media/remote", Kind: library.InputHTTP},
	}}
	r := newPreparedRuntimeForTest(t,
		preparedChannels{channels: []store.Channel{
			{Channel: schedule.Channel{ID: "internal"}},
			{Channel: schedule.Channel{ID: "tunarr"}, Policy: schedule.ChannelPolicy{OperatorPolicy: schedule.OperatorPolicy{Playout: &schedule.PlayoutPolicy{Backend: "tunarr"}}}},
		}}, timeline, inputs, preparedLookupFake{}, func() time.Time { return now }, nil,
		func() string { return "policy" }, func() string { return "internal" },
		func() prepared.RenditionContract { return playout.CanonicalPreparedRendition(playout.TierBalanced) },
	)

	plan, err := r.Plan(t.Context(), now, now.Add(6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 1 || plan.Candidates[0].Request.Source.SourceID != "inventory-source-shared" ||
		plan.Candidates[0].Request.Source.AudioTrack != 2 || !plan.Candidates[0].NeededAt.Equal(now) {
		t.Fatalf("candidates = %+v", plan.Candidates)
	}
	if inputs.calls != 1 || timeline.audioCalls != 1 {
		t.Fatalf("source resolution calls = input %d audio %d, want one each", inputs.calls, timeline.audioCalls)
	}

	// The warmed source policy makes the next planner pass a pure index read.
	if _, err := r.Plan(t.Context(), now, now.Add(6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if inputs.calls != 1 || timeline.audioCalls != 1 {
		t.Fatal("unchanged source policy re-resolved or re-probed the library item")
	}
}

func TestPreparedRuntimeAcceptsInternalHTTPSourceForBackgroundFFmpeg(t *testing.T) {
	t.Parallel()
	now := time.Unix(12_000, 0)
	timeline := &preparedTimelineFake{broadcasts: map[string][]playout.Broadcast{
		"internal": {{Kind: schedule.SlotProgram, LibraryItemID: "remote", Start: now, Stop: now.Add(time.Hour)}},
	}}
	inputs := &preparedInputsFake{sources: map[string]library.InputSource{
		"remote": {URL: "http://media/Videos/remote/stream?static=true&api_key=secret", Kind: library.InputHTTP},
	}}
	r := newPreparedRuntimeForTest(t,
		preparedChannels{channels: []store.Channel{{Channel: schedule.Channel{ID: "internal"}}}},
		timeline, inputs, preparedLookupFake{}, func() time.Time { return now }, nil,
		func() string { return "policy" }, func() string { return "internal" },
		func() prepared.RenditionContract { return playout.CanonicalPreparedRendition(playout.TierBalanced) },
	)

	plan, err := r.Plan(t.Context(), now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 1 {
		t.Fatalf("HTTP-backed prepared candidates = %d, want one background FFmpeg package", len(plan.Candidates))
	}
}

func TestPreparedRuntimeCandidatesKeepChannelAudioOverridesDistinct(t *testing.T) {
	t.Parallel()
	now := time.Unix(15_000, 0)
	timeline := &preparedTimelineFake{
		broadcasts: map[string][]playout.Broadcast{
			"english":  {{Kind: schedule.SlotProgram, LibraryItemID: "shared", Start: now, Stop: now.Add(time.Hour)}},
			"japanese": {{Kind: schedule.SlotProgram, LibraryItemID: "shared", Start: now, Stop: now.Add(time.Hour)}},
		},
		audioTrackByChannel: map[string]int{"english": 1, "japanese": 2},
	}
	inputs := &preparedInputsFake{sources: map[string]library.InputSource{
		"shared": {URL: "/media/shared.mkv", Kind: library.InputFile},
	}}
	r := newPreparedRuntimeForTest(t,
		preparedChannels{channels: []store.Channel{
			{Channel: schedule.Channel{ID: "english"}, Policy: schedule.ChannelPolicy{OperatorPolicy: schedule.OperatorPolicy{Playout: &schedule.PlayoutPolicy{AudioLanguage: "eng"}}}},
			{Channel: schedule.Channel{ID: "japanese"}, Policy: schedule.ChannelPolicy{OperatorPolicy: schedule.OperatorPolicy{Playout: &schedule.PlayoutPolicy{AudioLanguage: "jpn"}}}},
		}}, timeline, inputs, preparedLookupFake{}, func() time.Time { return now }, nil,
		func() string { return "policy" }, func() string { return "internal" },
		func() prepared.RenditionContract { return playout.CanonicalPreparedRendition(playout.TierBalanced) },
	)

	plan, err := r.Plan(t.Context(), now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 2 {
		t.Fatalf("candidate count = %d, want one publication per channel audio track", len(plan.Candidates))
	}
	tracks := map[int]bool{}
	for _, candidate := range plan.Candidates {
		tracks[candidate.Request.Source.AudioTrack] = true
	}
	if !tracks[1] || !tracks[2] {
		t.Fatalf("candidate audio tracks = %v, want tracks 1 and 2", tracks)
	}
}

func TestPreparedRuntimePlanExposesOneHundredChannelHotFrontier(t *testing.T) {
	t.Parallel()
	now := time.Unix(17_000, 0)
	channels := make([]store.Channel, 100)
	broadcasts := make(map[string][]playout.Broadcast, len(channels))
	sources := make(map[string]library.InputSource, len(channels))
	for i := range channels {
		channelID := fmt.Sprintf("ch-%03d", i)
		itemID := fmt.Sprintf("item-%03d", i)
		channels[i] = store.Channel{Channel: schedule.Channel{ID: channelID}}
		broadcasts[channelID] = []playout.Broadcast{{
			Kind: schedule.SlotProgram, LibraryItemID: itemID,
			Start: now.Add(time.Duration(i) * time.Second), Stop: now.Add(time.Hour),
		}}
		sources[itemID] = library.InputSource{URL: "/media/" + itemID + ".mkv", Kind: library.InputFile}
	}
	r := newPreparedRuntimeForTest(
		t, preparedChannels{channels: channels}, &preparedTimelineFake{broadcasts: broadcasts},
		&preparedInputsFake{sources: sources}, preparedLookupFake{}, func() time.Time { return now }, nil,
		func() string { return "policy" }, func() string { return "internal" },
		func() prepared.RenditionContract { return playout.CanonicalPreparedRendition(playout.TierBalanced) },
	)
	lastRequest := prepared.Request{
		Source:    preparedSource("item-099", 2),
		Rendition: playout.CanonicalPreparedRendition(playout.TierBalanced),
	}
	if err := r.readiness.RememberBinding(
		prepared.BindingKey{ChannelID: "ch-099", LibraryItemID: "item-099"},
		prepared.Binding{Policy: "policy", Request: lastRequest},
	); err != nil {
		t.Fatal(err)
	}
	r.lookup = preparedLookupFake{hits: map[prepared.Request]prepared.Specification{
		lastRequest: {SourceFingerprint: "durable", Rendition: lastRequest.Rendition},
	}}

	plan, err := r.Plan(t.Context(), now, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	inputs := r.sources.(*preparedInputsFake)
	if inputs.calls != 99 || len(plan.Candidates) != 99 {
		t.Fatalf("cold work = %d resolutions, %d candidates; want all 99 missing hot bindings", inputs.calls, len(plan.Candidates))
	}
	if len(plan.Protected) != 1 || plan.Protected[0].SourceFingerprint != "durable" {
		t.Fatalf("protected = %+v, want durable publication beyond cold batch", plan.Protected)
	}
	wantSummary := prepared.ReadinessSummary{
		Channels: 100, ReadyChannels: 1,
		ScheduledBindings: 100, ReadyBindings: 1, MissingBindings: 99,
		QueuedPublications: 99,
	}
	if plan.Summary != wantSummary {
		t.Fatalf("summary = %+v, want %+v", plan.Summary, wantSummary)
	}
}

func TestPreparedRuntimeInventoryBackedHotFrontierAvoidsExternalRefresh(t *testing.T) {
	t.Parallel()
	now := time.Unix(17_250, 0)
	const channelCount = 150
	channels := make([]store.Channel, channelCount)
	broadcasts := make(map[string][]playout.Broadcast, channelCount)
	inventorySources := make(map[string]library.InputSource, channelCount)
	for i := range channels {
		channelID := fmt.Sprintf("ch-%03d", i)
		itemID := fmt.Sprintf("item-%03d", i)
		channels[i] = store.Channel{Channel: schedule.Channel{ID: channelID}}
		broadcasts[channelID] = []playout.Broadcast{{
			Kind: schedule.SlotProgram, LibraryItemID: itemID,
			Start: now.Add(-time.Minute), Stop: now.Add(time.Hour),
		}}
		inventorySources[itemID] = library.InputSource{
			URL: "http://media/" + itemID, Kind: library.InputHTTP,
		}
	}
	timeline := &preparedTimelineFake{broadcasts: broadcasts, inventoryAudio: true}
	inputs := &preparedInputsFake{inventorySources: inventorySources}
	r := newPreparedRuntimeForTest(
		t, preparedChannels{channels: channels}, timeline, inputs, preparedLookupFake{},
		func() time.Time { return now }, nil, func() string { return "policy" },
		func() string { return "internal" },
		func() prepared.RenditionContract { return playout.CanonicalPreparedRendition(playout.TierBalanced) },
	)

	plan, err := r.Plan(t.Context(), now, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != preparedHotResolutionBatch {
		t.Fatalf("inventory-backed candidates = %d, want bounded frontier %d", len(plan.Candidates), preparedHotResolutionBatch)
	}
	if inputs.inventoryCalls != preparedHotResolutionBatch || inputs.calls != 0 {
		t.Fatalf("source resolution = %d inventory, %d external; want %d, 0", inputs.inventoryCalls, inputs.calls, preparedHotResolutionBatch)
	}
	if timeline.inventoryAudioCalls != preparedHotResolutionBatch || timeline.audioCalls != 0 {
		t.Fatalf("audio resolution = %d inventory, %d external; want %d, 0", timeline.inventoryAudioCalls, timeline.audioCalls, preparedHotResolutionBatch)
	}
}

func TestPreparedRuntimeScalesCurrentAndNextProtectionAtFiftyAndOneHundredChannels(t *testing.T) {
	t.Parallel()
	for _, channelCount := range []int{50, 100} {
		channelCount := channelCount
		t.Run(fmt.Sprintf("%d_channels", channelCount), func(t *testing.T) {
			t.Parallel()
			now := time.Unix(17_300, 0)
			channels := make([]store.Channel, channelCount)
			broadcasts := make(map[string][]playout.Broadcast, channelCount)
			inventorySources := make(map[string]library.InputSource, channelCount*2)
			hits := make(map[prepared.Request]prepared.Specification, channelCount*2)
			rendition := playout.CanonicalPreparedRendition(playout.TierBalanced)
			for i := range channels {
				channelID := fmt.Sprintf("ch-%03d", i)
				channels[i] = store.Channel{Channel: schedule.Channel{ID: channelID}}
				for offset, class := range []string{"current", "next"} {
					itemID := fmt.Sprintf("%s-%03d", class, i)
					start := now.Add(time.Duration(offset)*time.Hour - 30*time.Minute)
					broadcasts[channelID] = append(broadcasts[channelID], playout.Broadcast{
						Kind: schedule.SlotProgram, LibraryItemID: itemID,
						Start: start, Stop: start.Add(time.Hour),
					})
					inventorySources[itemID] = library.InputSource{
						URL: "http://media/" + itemID, Kind: library.InputHTTP,
					}
					request := prepared.Request{Source: preparedSource(itemID, 2), Rendition: rendition}
					hits[request] = prepared.Specification{SourceFingerprint: itemID, Rendition: rendition}
				}
			}
			timeline := &preparedTimelineFake{broadcasts: broadcasts, inventoryAudio: true}
			inputs := &preparedInputsFake{inventorySources: inventorySources}
			r := newPreparedRuntimeForTest(
				t, preparedChannels{channels: channels}, timeline, inputs, preparedLookupFake{hits: hits},
				func() time.Time { return now }, nil, func() string { return "policy" },
				func() string { return "internal" }, func() prepared.RenditionContract { return rendition },
			)

			var plan prepared.ReadinessPlan
			passes := (channelCount*2 + preparedHotResolutionBatch - 1) / preparedHotResolutionBatch
			for range passes {
				var err error
				plan, err = r.Plan(t.Context(), now, now.Add(6*time.Hour))
				if err != nil {
					t.Fatal(err)
				}
			}
			if len(plan.Protected) != channelCount*2 {
				t.Fatalf("protected publications = %d, want current+next %d", len(plan.Protected), channelCount*2)
			}
			if plan.Summary.ReadyChannels != channelCount || plan.Summary.ReadyBindings != channelCount*2 {
				t.Fatalf("readiness = %+v, want every current and next binding ready", plan.Summary)
			}
			if inputs.calls != 0 || timeline.audioCalls != 0 {
				t.Fatalf("durable scale plan performed external source/audio work: %d/%d", inputs.calls, timeline.audioCalls)
			}
		})
	}
}

func TestPreparedRuntimeObservationDoesNotResolveAbsentBindings(t *testing.T) {
	t.Parallel()
	now := time.Unix(17_375, 0)
	timeline := &preparedTimelineFake{broadcasts: map[string][]playout.Broadcast{"ch": {{
		Kind: schedule.SlotProgram, LibraryItemID: "item", Start: now, Stop: now.Add(time.Hour),
	}}}}
	inputs := &preparedInputsFake{}
	r := newPreparedRuntimeForTest(
		t, preparedChannels{channels: []store.Channel{{Channel: schedule.Channel{ID: "ch"}}}},
		timeline, inputs, preparedLookupFake{}, func() time.Time { return now }, nil,
		func() string { return "policy" }, func() string { return "internal" },
		func() prepared.RenditionContract { return playout.CanonicalPreparedRendition(playout.TierBalanced) },
	)

	plan, err := r.Observe(t.Context(), now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary.MissingBindings != 1 {
		t.Fatalf("observation summary = %+v, want one missing binding", plan.Summary)
	}
	if inputs.inventoryCalls != 0 || inputs.calls != 0 ||
		timeline.inventoryAudioCalls != 0 || timeline.audioCalls != 0 {
		t.Fatalf("lookup-only observation did source/audio work: inputs %+v timeline %+v", inputs, timeline)
	}
}

func TestPreparedRuntimeProtectsCurrentAndNextButNotLaterLookahead(t *testing.T) {
	t.Parallel()
	now := time.Unix(17_500, 0)
	rendition := playout.CanonicalPreparedRendition(playout.TierBalanced)
	timeline := &preparedTimelineFake{broadcasts: map[string][]playout.Broadcast{"ch": {
		{Kind: schedule.SlotProgram, LibraryItemID: "current", Start: now.Add(-time.Minute), Stop: now.Add(time.Hour)},
		{Kind: schedule.SlotProgram, LibraryItemID: "next", Start: now.Add(time.Hour), Stop: now.Add(2 * time.Hour)},
		{Kind: schedule.SlotProgram, LibraryItemID: "later", Start: now.Add(2 * time.Hour), Stop: now.Add(3 * time.Hour)},
	}}}
	inputs := &preparedInputsFake{sources: map[string]library.InputSource{
		"current": {URL: "/media/current.mkv", Kind: library.InputFile},
		"next":    {URL: "/media/next.mkv", Kind: library.InputFile},
		"later":   {URL: "/media/later.mkv", Kind: library.InputFile},
	}}
	hits := make(map[prepared.Request]prepared.Specification)
	for _, item := range []string{"current", "next", "later"} {
		request := prepared.Request{Source: preparedSource(item, 2), Rendition: rendition}
		hits[request] = prepared.Specification{SourceFingerprint: item, Rendition: rendition}
	}
	r := newPreparedRuntimeForTest(t,
		preparedChannels{channels: []store.Channel{{Channel: schedule.Channel{ID: "ch"}}}},
		timeline, inputs, preparedLookupFake{hits: hits}, func() time.Time { return now }, nil,
		func() string { return "policy" }, func() string { return "internal" },
		func() prepared.RenditionContract { return rendition },
	)

	plan, err := r.Plan(t.Context(), now, now.Add(6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Protected) != 2 {
		t.Fatalf("protected publications = %+v, want only current and next", plan.Protected)
	}
	protected := map[string]bool{}
	for _, specification := range plan.Protected {
		protected[specification.SourceFingerprint] = true
	}
	if !protected["current"] || !protected["next"] || protected["later"] {
		t.Fatalf("protected publication identities = %+v", protected)
	}
}

func TestPreparedRuntimeRebindsAnObservedSourceRevision(t *testing.T) {
	t.Parallel()
	now := time.Unix(18_000, 0)
	rendition := playout.CanonicalPreparedRendition(playout.TierBalanced)
	oldRequest := prepared.Request{Source: preparedSource("item", 2), Rendition: rendition}
	newSource := preparedSource("item", 2)
	newSource.Revision = "revision-item-2"
	newRequest := prepared.Request{Source: newSource, Rendition: rendition}
	sources := &preparedInputsFake{
		sources:   map[string]library.InputSource{"item": {URL: "http://media/item", Kind: library.InputHTTP}},
		revisions: map[string]string{"item": newSource.Revision},
		current:   map[prepared.Source]bool{oldRequest.Source: false, newSource: true},
	}
	r := newPreparedRuntimeForTest(t,
		preparedChannels{channels: []store.Channel{{Channel: schedule.Channel{ID: "ch"}}}},
		&preparedTimelineFake{broadcasts: map[string][]playout.Broadcast{"ch": {{
			Kind: schedule.SlotProgram, LibraryItemID: "item", Start: now, Stop: now.Add(time.Hour),
		}}}},
		sources, preparedLookupFake{hits: map[prepared.Request]prepared.Specification{
			oldRequest: {SourceFingerprint: "old", Rendition: rendition},
		}},
		func() time.Time { return now }, nil, func() string { return "policy" },
		func() string { return "internal" }, func() prepared.RenditionContract { return rendition },
	)
	key := prepared.BindingKey{ChannelID: "ch", LibraryItemID: "item"}
	if err := r.readiness.RememberBinding(key, prepared.Binding{Policy: "policy", Request: oldRequest}); err != nil {
		t.Fatal(err)
	}
	plan, err := r.Plan(t.Context(), now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Protected) != 0 || len(plan.Candidates) != 1 || plan.Candidates[0].Request != newRequest {
		t.Fatalf("revision-change plan = protected %+v candidates %+v, want only revision-2 candidate", plan.Protected, plan.Candidates)
	}
	if bound, ok := r.readiness.Binding(key, "policy", ""); !ok || bound != newRequest {
		t.Fatalf("rebound source = (%+v, %v), want revision-2", bound, ok)
	}
}

func TestPreparedRuntimeScheduleChangeAdvancesTheUrgentFrontier(t *testing.T) {
	t.Parallel()
	now := time.Unix(18_500, 0)
	rendition := playout.CanonicalPreparedRendition(playout.TierBalanced)
	timeline := &preparedTimelineFake{broadcasts: map[string][]playout.Broadcast{"ch": {{
		Kind: schedule.SlotProgram, LibraryItemID: "old", Start: now, Stop: now.Add(time.Hour),
	}}}}
	inputs := &preparedInputsFake{sources: map[string]library.InputSource{
		"old": {URL: "/media/old.mkv", Kind: library.InputFile},
		"new": {URL: "/media/new.mkv", Kind: library.InputFile},
	}}
	oldRequest := prepared.Request{Source: preparedSource("old", 2), Rendition: rendition}
	r := newPreparedRuntimeForTest(t,
		preparedChannels{channels: []store.Channel{{Channel: schedule.Channel{ID: "ch"}}}},
		timeline, inputs, preparedLookupFake{hits: map[prepared.Request]prepared.Specification{
			oldRequest: {SourceFingerprint: "old", Rendition: rendition},
		}}, func() time.Time { return now }, nil, func() string { return "policy" },
		func() string { return "internal" }, func() prepared.RenditionContract { return rendition },
	)
	if plan, err := r.Plan(t.Context(), now, now.Add(time.Hour)); err != nil || len(plan.Protected) != 1 {
		t.Fatalf("initial ready plan = (%+v, %v)", plan, err)
	}

	timeline.broadcasts["ch"] = []playout.Broadcast{{
		Kind: schedule.SlotProgram, LibraryItemID: "new", Start: now, Stop: now.Add(time.Hour),
	}}
	plan, err := r.Plan(t.Context(), now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	newRequest := prepared.Request{Source: preparedSource("new", 2), Rendition: rendition}
	if len(plan.Protected) != 0 || len(plan.Candidates) != 1 ||
		plan.Candidates[0].Class != prepared.CandidateCurrent || plan.Candidates[0].Request != newRequest {
		t.Fatalf("changed schedule plan = protected %+v candidates %+v, want new current first", plan.Protected, plan.Candidates)
	}
}

func TestPreparedRuntimeInvalidatesStaleBindingWhenSourceReresolutionFails(t *testing.T) {
	t.Parallel()
	now := time.Unix(19_000, 0)
	rendition := playout.CanonicalPreparedRendition(playout.TierBalanced)
	oldRequest := prepared.Request{Source: preparedSource("item", 2), Rendition: rendition}
	sources := &preparedInputsFake{current: map[prepared.Source]bool{oldRequest.Source: false}}
	timeline := &preparedTimelineFake{broadcasts: map[string][]playout.Broadcast{"ch": {{
		Kind: schedule.SlotProgram, LibraryItemID: "item", Start: now, Stop: now.Add(time.Hour),
	}}}}
	r := newPreparedRuntimeForTest(t,
		preparedChannels{channels: []store.Channel{{Channel: schedule.Channel{ID: "ch"}}}},
		timeline, sources, preparedLookupFake{hits: map[prepared.Request]prepared.Specification{
			oldRequest: {SourceFingerprint: "old", Rendition: rendition},
		}},
		func() time.Time { return now }, nil, func() string { return "policy" },
		func() string { return "internal" }, func() prepared.RenditionContract { return rendition },
	)
	key := prepared.BindingKey{ChannelID: "ch", LibraryItemID: "item"}
	if err := r.readiness.RememberBinding(key, prepared.Binding{Policy: "policy", Request: oldRequest}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plan(t.Context(), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := r.ResolvePrepared(t.Context(), playout.TuneRequest{ChannelID: "ch"}); err != nil || ok {
		t.Fatalf("ResolvePrepared after failed stale rebind = ok %v err %v, want immediate miss", ok, err)
	}
	if _, ok := r.readiness.Binding(key, "policy", ""); ok {
		t.Fatal("failed source re-resolution retained stale durable binding")
	}
}

func TestPreparedRuntimeTuneIsLookupOnlyAndCarriesPreviousAiring(t *testing.T) {
	t.Parallel()
	now := time.Unix(20_000, 0)
	previous := playout.Broadcast{Kind: schedule.SlotProgram, LibraryItemID: "previous", Start: now.Add(-time.Minute), Stop: now.Add(-time.Second)}
	current := playout.Broadcast{Kind: schedule.SlotProgram, LibraryItemID: "current", Start: now.Add(-time.Second), Stop: now.Add(time.Hour)}
	timeline := &preparedTimelineFake{broadcasts: map[string][]playout.Broadcast{"ch": {previous, current}}}
	inputs := &preparedInputsFake{sources: map[string]library.InputSource{
		"previous": {URL: "/media/previous.mkv", Kind: library.InputFile},
		"current":  {URL: "/media/current.mkv", Kind: library.InputFile},
	}}
	rendition := playout.CanonicalPreparedRendition(playout.TierBalanced)
	previousRequest := prepared.Request{Source: preparedSource("previous", 2), Rendition: rendition}
	currentRequest := prepared.Request{Source: preparedSource("current", 2), Rendition: rendition}
	lookup := preparedLookupFake{hits: map[prepared.Request]prepared.Specification{
		previousRequest: {SourceFingerprint: "previous", Rendition: rendition},
		currentRequest:  {SourceFingerprint: "current", Rendition: rendition},
	}}
	r := newPreparedRuntimeForTest(t,
		preparedChannels{channels: []store.Channel{{Channel: schedule.Channel{ID: "ch"}}}},
		timeline, inputs, lookup, func() time.Time { return now }, nil,
		func() string { return "policy" }, func() string { return "internal" },
		func() prepared.RenditionContract { return rendition },
	)

	// No control-plane pass yet: tune misses without touching the media server or audio prober.
	if _, ok, err := r.ResolvePrepared(t.Context(), playout.TuneRequest{ChannelID: "ch"}); err != nil || ok {
		t.Fatalf("cold ResolvePrepared = ok %v err %v, want clean miss", ok, err)
	}
	if inputs.calls != 0 || timeline.audioCalls != 0 {
		t.Fatal("cold tune performed control-plane source work")
	}
	if _, err := r.Plan(t.Context(), now.Add(-time.Minute), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	inputCalls, audioCalls := inputs.calls, timeline.audioCalls

	window, ok, err := r.ResolvePrepared(t.Context(), playout.TuneRequest{ChannelID: "ch"})
	if err != nil || !ok {
		t.Fatalf("warmed ResolvePrepared = ok %v err %v", ok, err)
	}
	if window.Current.Specification.SourceFingerprint != "current" || window.Current.Offset != time.Second ||
		len(window.Previous) != 1 || window.Previous[0].Specification.SourceFingerprint != "previous" {
		t.Fatalf("window = %+v", window)
	}
	if inputs.calls != inputCalls || timeline.audioCalls != audioCalls {
		t.Fatal("warmed tune re-resolved a source or ran an audio probe")
	}
}

func TestPreparedRuntimeTuneRequestsTheSharedDVRLookbehind(t *testing.T) {
	t.Parallel()
	now := time.Unix(25_000, 0)
	timeline := &preparedTimelineFake{broadcasts: map[string][]playout.Broadcast{"ch": {{
		Kind: schedule.SlotProgram, LibraryItemID: "current",
		Start: now.Add(-time.Minute), Stop: now.Add(time.Hour),
	}}}}
	r := newPreparedRuntimeForTest(t,
		preparedChannels{channels: []store.Channel{{Channel: schedule.Channel{ID: "ch"}}}},
		timeline, &preparedInputsFake{}, preparedLookupFake{}, func() time.Time { return now }, nil,
		func() string { return "policy" }, func() string { return "internal" },
		func() prepared.RenditionContract { return playout.CanonicalPreparedRendition(playout.TierBalanced) },
	)

	_, _, _ = r.ResolvePrepared(t.Context(), playout.TuneRequest{ChannelID: "ch"})
	if want := now.Add(-playout.DVRHorizon); !timeline.lastFrom.Equal(want) {
		t.Fatalf("prepared schedule starts at %v, want DVR cutoff %v", timeline.lastFrom, want)
	}
	if !timeline.lastTo.Equal(now.Add(time.Nanosecond)) {
		t.Fatalf("prepared schedule stops at %v, want live edge %v", timeline.lastTo, now.Add(time.Nanosecond))
	}
}

func TestPreparedRuntimePolicyChangeMakesTuneMissUntilPlannerRewarms(t *testing.T) {
	t.Parallel()
	now := time.Unix(30_000, 0)
	policy := "balanced"
	timeline := &preparedTimelineFake{broadcasts: map[string][]playout.Broadcast{"ch": {{
		Kind: schedule.SlotProgram, LibraryItemID: "item", Start: now.Add(-time.Minute), Stop: now.Add(time.Hour),
	}}}}
	inputs := &preparedInputsFake{sources: map[string]library.InputSource{
		"item": {URL: "/media/item.mkv", Kind: library.InputFile},
	}}
	rendition := playout.CanonicalPreparedRendition(playout.TierBalanced)
	request := prepared.Request{Source: preparedSource("item", 2), Rendition: rendition}
	r := newPreparedRuntimeForTest(t,
		preparedChannels{channels: []store.Channel{{Channel: schedule.Channel{ID: "ch"}}}}, timeline, inputs,
		preparedLookupFake{hits: map[prepared.Request]prepared.Specification{request: {SourceFingerprint: "hit", Rendition: rendition}}},
		func() time.Time { return now }, nil, func() string { return policy },
		func() string { return "internal" }, func() prepared.RenditionContract { return rendition },
	)
	if _, err := r.Plan(t.Context(), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	policy = "quality"
	if _, ok, err := r.ResolvePrepared(t.Context(), playout.TuneRequest{ChannelID: "ch"}); err != nil || ok {
		t.Fatalf("ResolvePrepared after policy change = ok %v err %v, want lookup-only miss", ok, err)
	}
	if inputs.calls != 1 {
		t.Fatal("tune tried to rewarm the changed policy")
	}
}

func TestPreparedRuntimeTuneUsesDurableReadinessBeforeTheFirstPlannerPass(t *testing.T) {
	t.Parallel()
	now := time.Unix(40_000, 0)
	preparedLibrary, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := prepared.OpenReadiness(preparedLibrary)
	if err != nil {
		t.Fatal(err)
	}
	rendition := playout.CanonicalPreparedRendition(playout.TierBalanced)
	request := prepared.Request{
		Source: preparedSource("item", 2), Rendition: rendition,
	}
	key := prepared.BindingKey{ChannelID: "ch", LibraryItemID: "item"}
	if err := readiness.RememberBinding(key, prepared.Binding{Policy: "policy", Request: request}); err != nil {
		t.Fatal(err)
	}
	reopened, err := prepared.OpenReadiness(preparedLibrary)
	if err != nil {
		t.Fatal(err)
	}
	timeline := &preparedTimelineFake{broadcasts: map[string][]playout.Broadcast{"ch": {{
		Kind: schedule.SlotProgram, LibraryItemID: "item", Start: now.Add(-time.Minute), Stop: now.Add(time.Hour),
	}}}}
	inputs := &preparedInputsFake{}
	r := newPreparedRuntimeResolver(preparedRuntimeDependencies{
		Channels: preparedChannels{channels: []store.Channel{{Channel: schedule.Channel{ID: "ch"}}}},
		Timeline: timeline, Sources: inputs,
		Lookup: preparedLookupFake{hits: map[prepared.Request]prepared.Specification{
			request: {SourceFingerprint: "ready", Rendition: rendition},
		}},
		Now: func() time.Time { return now }, Policy: func() string { return "policy" },
		GlobalBackend: func() string { return "internal" }, Rendition: func() prepared.RenditionContract { return rendition },
		Readiness: reopened,
	})

	window, ok, err := r.ResolvePrepared(t.Context(), playout.TuneRequest{ChannelID: "ch"})
	if err != nil || !ok || window.Current.Specification.SourceFingerprint != "ready" {
		t.Fatalf("ResolvePrepared after restart = (%+v, %v, %v), want durable hit", window, ok, err)
	}
	if inputs.calls != 0 || timeline.audioCalls != 0 {
		t.Fatal("cold-start tune performed media-server or audio-probe work")
	}
}

func TestPreparedRuntimePublishedInternalCanServeWarmedMediaBeforeCutover(t *testing.T) {
	t.Parallel()
	now := time.Unix(45_000, 0)
	preparedLibrary, err := prepared.NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	readiness, err := prepared.OpenReadiness(preparedLibrary)
	if err != nil {
		t.Fatal(err)
	}
	render := playout.CanonicalPreparedRendition(playout.TierBalanced)
	request := prepared.Request{Source: preparedSource("item", 0), Rendition: render}
	if err := readiness.RememberBinding(
		prepared.BindingKey{ChannelID: "ch", LibraryItemID: "item"},
		prepared.Binding{Policy: "policy", Request: request},
	); err != nil {
		t.Fatal(err)
	}
	checkpointReads := 0
	r := newPreparedRuntimeResolver(preparedRuntimeDependencies{
		Channels: preparedChannels{channels: []store.Channel{{Channel: schedule.Channel{ID: "ch"}}}},
		Timeline: &preparedTimelineFake{broadcasts: map[string][]playout.Broadcast{"ch": {{
			Kind: schedule.SlotProgram, LibraryItemID: "item", Start: now.Add(-time.Minute), Stop: now.Add(time.Hour),
		}}}},
		Lookup: preparedLookupFake{hits: map[prepared.Request]prepared.Specification{
			request: {SourceFingerprint: "prepared-internal", Rendition: render},
		}},
		Now: func() time.Time { return now }, Policy: func() string { return "policy" },
		GlobalBackendContext: func(context.Context) (string, error) {
			return schedule.PlayoutBackendTunarr, nil
		},
		TransportBackendContext: func(context.Context) (string, error) {
			checkpointReads++
			return schedule.PlayoutBackendInternal, nil
		},
		Rendition: func() prepared.RenditionContract { return render }, Readiness: readiness,
	})

	window, ok, err := r.ResolvePrepared(t.Context(), playout.TuneRequest{ChannelID: "ch"})
	if err != nil || !ok || window.Current.Specification.SourceFingerprint != "prepared-internal" {
		t.Fatalf("prepared transport before cutover = (%+v, %v, %v)", window, ok, err)
	}
	if checkpointReads != 1 {
		t.Fatalf("prepared tune checkpoint reads = %d, want one", checkpointReads)
	}
}

func TestPreparedRuntimeChannelAudioPolicyChangeMakesTuneMissImmediately(t *testing.T) {
	t.Parallel()
	now := time.Unix(50_000, 0)
	channels := preparedChannels{channels: []store.Channel{{
		Channel: schedule.Channel{ID: "ch"},
		Policy:  schedule.ChannelPolicy{OperatorPolicy: schedule.OperatorPolicy{Playout: &schedule.PlayoutPolicy{AudioLanguage: "eng"}}},
	}}}
	timeline := &preparedTimelineFake{broadcasts: map[string][]playout.Broadcast{"ch": {{
		Kind: schedule.SlotProgram, LibraryItemID: "item", Start: now.Add(-time.Minute), Stop: now.Add(time.Hour),
	}}}}
	inputs := &preparedInputsFake{sources: map[string]library.InputSource{
		"item": {URL: "/media/item.mkv", Kind: library.InputFile},
	}}
	rendition := playout.CanonicalPreparedRendition(playout.TierBalanced)
	request := prepared.Request{Source: preparedSource("item", 2), Rendition: rendition}
	r := newPreparedRuntimeForTest(
		t, channels, timeline, inputs,
		preparedLookupFake{hits: map[prepared.Request]prepared.Specification{
			request: {SourceFingerprint: "ready", Rendition: rendition},
		}},
		func() time.Time { return now }, nil, func() string { return "policy" },
		func() string { return "internal" }, func() prepared.RenditionContract { return rendition },
	)
	if _, err := r.Plan(t.Context(), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	channels.channels[0].Policy.Playout.AudioLanguage = "jpn"
	if _, ok, err := r.ResolvePrepared(t.Context(), playout.TuneRequest{ChannelID: "ch"}); err != nil || ok {
		t.Fatalf("ResolvePrepared after channel audio change = ok %v err %v, want immediate miss", ok, err)
	}
	if inputs.calls != 1 || timeline.audioCalls != 1 {
		t.Fatal("tune tried to re-resolve the changed channel policy")
	}
}

func TestPreparedBudgetBytesUsesGiBAndSaturates(t *testing.T) {
	t.Parallel()
	if got := preparedBudgetBytes(512); got != int64(512)<<30 {
		t.Fatalf("preparedBudgetBytes(512) = %d", got)
	}
	if got := preparedBudgetBytes(0); got != 0 {
		t.Fatalf("preparedBudgetBytes(0) = %d, want disabled", got)
	}
	if got := preparedBudgetBytes(int(^uint(0) >> 1)); got <= 0 {
		t.Fatalf("preparedBudgetBytes(max int) overflowed to %d", got)
	}
}
