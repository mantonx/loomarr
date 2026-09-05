package catalog

// Property tests for the identity-stability invariants that protect grounding
// (§8): dedupeKey / mergeCandidate / dedupeAndOrder must treat only
// MediaType+(TMDBID|TVDBID|Name) as identity, must fold candidates idempotently
// and order-independently, and must preserve source relevance inside the
// in-library-first blend. These
// functions are unexported, so this file is in-package (package catalog).
//
// Randomness is a seeded plain-Go loop (math/rand with a fixed source) — not a
// framework — so any failure reproduces from the printed seed. There is no
// network and no fixture: these exercise pure functions over synthetic candidates.

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/loomarr/loomarr/internal/provision"
)

// propSeed pins the corpus generation. Change it only to widen coverage; a
// failing case prints enough to reproduce deterministically at this seed.
const propSeed = 0x10054AC

var propMediaTypes = []provision.MediaType{provision.Movie, provision.Series}

// randCandidate builds a synthetic candidate. Identity fields (MediaType, ids,
// Name) are drawn from a SMALL space so collisions actually happen and the
// dedupe/merge machinery is exercised; non-identity fields are noise.
func randCandidate(rng *rand.Rand) Candidate {
	c := Candidate{
		MediaType: propMediaTypes[rng.Intn(len(propMediaTypes))],
	}
	// Pick an identity mode so dedupeKey exercises all three branches:
	// 0 = tmdb id, 1 = tvdb id (no tmdb), 2 = name-only (no ids).
	switch rng.Intn(3) {
	case 0:
		c.TMDBID = 1 + rng.Intn(6)
		if rng.Intn(2) == 0 { // sometimes also carry a tvdb id (tmdb still wins)
			c.TVDBID = 1 + rng.Intn(6)
		}
	case 1:
		c.TVDBID = 1 + rng.Intn(6)
	case 2:
		c.Name = randName(rng) // no ids: name IS identity, so it must vary
	}
	// Name is derived from the identity KEY, modelling the real-world guarantee
	// that one external id maps to one canonical title. This deliberately avoids
	// same-id/different-name aliases: when two records share a tmdb/tvdb id but
	// carry DIFFERENT names, the code legitimately dedupes them and the surviving
	// Name depends on add-order (merge never rewrites Name) — that is correct
	// behavior, but it is NOT an ordering invariant, so it must not be injected
	// here or the in-library-first/name-asc ORDER-stability claim would falsely
	// fail. (For name-identity candidates the name is already set above and the
	// key embeds it, so this call is a stable no-op for them.)
	c.Name = nameForKey(dedupeKey(c), c.Name)
	// Non-identity noise. These MUST NOT affect dedupeKey and MUST NOT reorder.
	c.Year = randNonIdentityYear(rng)
	c.InLibrary = rng.Intn(2) == 0
	if c.InLibrary {
		c.LibraryItemID = randLibraryItemID(rng)
	}
	if rng.Intn(2) == 0 {
		c.Genres = randGenres(rng)
	}
	if rng.Intn(2) == 0 {
		c.Overview = randOverview(rng)
	}
	if rng.Intn(2) == 0 {
		c.OfficialRating = randRating(rng)
	}
	if rng.Intn(2) == 0 {
		c.Networks = []string{"Network " + randName(rng)}
		c.Cast = []string{"Actor " + randName(rng)}
		c.Creators = []string{"Creator " + randName(rng)}
	}
	c.Source = randScope(rng)
	return c
}

func randName(rng *rand.Rand) string {
	names := []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo"}
	return names[rng.Intn(len(names))]
}

// propNames is the fixed name alphabet; nameForKey hashes an identity key into
// it so that a given id-based identity ALWAYS resolves to the same canonical
// name, no matter its add-order. For a name-based identity the name is already
// part of the key, so we return it unchanged.
var propNames = []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo"}

func nameForKey(key, nameIdentity string) string {
	if nameIdentity != "" {
		return nameIdentity // name-based identity: name already fixed and in-key
	}
	h := 0
	for _, r := range key {
		h = h*31 + int(r)
	}
	if h < 0 {
		h = -h
	}
	return propNames[h%len(propNames)]
}
func randNonIdentityYear(rng *rand.Rand) int { return 1980 + rng.Intn(45) }
func randLibraryItemID(rng *rand.Rand) string {
	ids := []string{"lib-a", "lib-b", "lib-c"}
	return ids[rng.Intn(len(ids))]
}
func randGenres(rng *rand.Rand) []string {
	all := []string{"Action", "Comedy", "Drama", "SciFi"}
	n := 1 + rng.Intn(2)
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, all[rng.Intn(len(all))])
	}
	return out
}
func randOverview(rng *rand.Rand) string {
	ov := []string{"a story", "another tale", "the epic", "quiet drama"}
	return ov[rng.Intn(len(ov))]
}
func randRating(rng *rand.Rand) string {
	r := []string{"G", "PG", "PG-13", "R", "TV-Y7"}
	return r[rng.Intn(len(r))]
}
func randScope(rng *rand.Rand) Scope {
	s := []Scope{ScopeLibrary, ScopeTMDB}
	return s[rng.Intn(len(s))]
}

// mutateNonIdentity flips exactly the field named, returning a copy. It touches
// ONLY fields the design doc marks as display/enforcement/provenance metadata
// (never identity). Used by the invariant-1 test.
func mutateNonIdentity(c Candidate, field string) Candidate {
	switch field {
	case "Genres":
		c.Genres = append([]string{"MUTATED"}, c.Genres...)
	case "Overview":
		c.Overview = c.Overview + "!!mutated"
	case "OfficialRating":
		if c.OfficialRating == "NC-17" {
			c.OfficialRating = "X"
		} else {
			c.OfficialRating = "NC-17"
		}
	case "InLibrary":
		c.InLibrary = !c.InLibrary
	case "Source":
		if c.Source == ScopeTMDB {
			c.Source = ScopeLibrary
		} else {
			c.Source = ScopeTMDB
		}
	case "Year":
		c.Year = c.Year + 1000
	case "LibraryItemID":
		c.LibraryItemID = c.LibraryItemID + "-mutated"
	case "Networks":
		c.Networks = append([]string{"MUTATED"}, c.Networks...)
	case "Cast":
		c.Cast = append([]string{"MUTATED"}, c.Cast...)
	case "Creators":
		c.Creators = append([]string{"MUTATED"}, c.Creators...)
	default:
		panic("unknown non-identity field: " + field)
	}
	return c
}

// Invariant (1): dedupeKey is INVARIANT under mutation of every non-identity
// field. Only MediaType + (TMDBID|TVDBID|Name) may participate in identity, or
// the grounding gate and in-library-first ordering would shift.
func TestProp_DedupeKey_InvariantUnderNonIdentityMutation(t *testing.T) {
	rng := rand.New(rand.NewSource(propSeed))
	nonIdentityFields := []string{
		"Genres", "Overview", "OfficialRating",
		"InLibrary", "Source", "Year", "LibraryItemID", "Networks", "Cast", "Creators",
	}
	const iters = 4000
	for i := 0; i < iters; i++ {
		c := randCandidate(rng)
		base := dedupeKey(c)
		for _, f := range nonIdentityFields {
			mutated := mutateNonIdentity(c, f)
			if got := dedupeKey(mutated); got != base {
				t.Fatalf("seed=%#x iter=%d: mutating non-identity field %q changed dedupeKey: %q -> %q\ncand=%+v",
					propSeed, i, f, base, got, c)
			}
		}
	}
}

// dedupeKey sanity: two candidates with the SAME identity triple but different
// noise collide; changing an identity component splits them. (Complements
// invariant 1 from the other direction so "invariant" can't be trivially true.)
func TestProp_DedupeKey_IdentityComponentsMatter(t *testing.T) {
	base := Candidate{MediaType: provision.Movie, TMDBID: 5, Name: "Alpha"}
	k := dedupeKey(base)

	// Same identity, different noise -> same key.
	noisy := base
	noisy.Year = 2001
	noisy.InLibrary = true
	noisy.Genres = []string{"Action"}
	noisy.Source = ScopeLibrary
	if dedupeKey(noisy) != k {
		t.Fatalf("noise changed key: %q vs %q", dedupeKey(noisy), k)
	}

	// Changing MediaType splits identity.
	if mt := base; func() bool { mt.MediaType = provision.Series; return dedupeKey(mt) == k }() {
		t.Error("MediaType is part of identity but didn't change key")
	}
	// Changing the TMDB id splits identity.
	if id := base; func() bool { id.TMDBID = 6; return dedupeKey(id) == k }() {
		t.Error("TMDBID is part of identity but didn't change key")
	}
	// When identity is name-based (no ids), Name is part of identity.
	nameOnly := Candidate{MediaType: provision.Movie, Name: "Alpha"}
	renamed := nameOnly
	renamed.Name = "Bravo"
	if dedupeKey(nameOnly) == dedupeKey(renamed) {
		t.Error("Name is part of name-based identity but didn't change key")
	}
	// TMDB id wins over TVDB id: two candidates sharing tmdb but differing tvdb collide.
	a := Candidate{MediaType: provision.Movie, TMDBID: 5, TVDBID: 1, Name: "Alpha"}
	b := Candidate{MediaType: provision.Movie, TMDBID: 5, TVDBID: 2, Name: "Zeta"}
	if dedupeKey(a) != dedupeKey(b) {
		t.Error("with tmdb set, tvdb/name must not affect identity")
	}
}

// foldCorpus replays the exact Search/Discover machinery (byKey map + insertion
// order + add-or-merge) over a candidate slice and returns the deduped, ordered
// output. This is the real production fold path; the tests below assert identity,
// relevance-order, and bounded-blend invariants against it.
func foldCorpus(cands []Candidate, limit int) []Candidate {
	byKey := map[string]*Candidate{}
	order := []string{}
	for _, cand := range cands {
		k := dedupeKey(cand)
		if existing, ok := byKey[k]; ok {
			mergeCandidate(existing, cand)
			continue
		}
		cp := cand
		byKey[k] = &cp
		order = append(order, k)
	}
	return dedupeAndOrder(byKey, order, limit)
}

// identityView reduces a candidate to the fields whose merged value is
// ORDER-INDEPENDENT: the identity key and sticky-true InLibrary. It deliberately
// EXCLUDES the non-key id values (e.g. TVDBID when TMDBID is the key): those fill
// from the first non-zero contributor, so their value legitimately depends on
// add-order when contributors disagree. The permutation-stable claim is over the
// set of {identity key -> InLibrary}. "Every identity that had SOME id keeps an
// id" is asserted separately (TestProp_Fold_StickyInLibraryAndIDFill), which is
// order-independent because it's an any-non-zero property, not a which-value one.
type identityView struct {
	Key       string
	InLibrary bool
}

func viewOf(c Candidate) identityView {
	return identityView{Key: dedupeKey(c), InLibrary: c.InLibrary}
}

// identitySet is the set (keyed by identity) of views — order-independent.
func identitySet(cands []Candidate) map[string]identityView {
	m := make(map[string]identityView, len(cands))
	for _, c := range cands {
		m[dedupeKey(c)] = viewOf(c)
	}
	return m
}

// Invariant (2): the fold is COMMUTATIVE on identity — permuting the add-order of
// a corpus yields the same deduped identity set. InLibrary is sticky-true (any
// contributor in-library => merged in-library) and ids only fill from 0, never
// overwrite — both order-independent properties. A large limit avoids truncation
// interfering with set equality.
func TestProp_Fold_CommutativeOnIdentity(t *testing.T) {
	rng := rand.New(rand.NewSource(propSeed + 1))
	const iters = 1500
	for i := 0; i < iters; i++ {
		n := 1 + rng.Intn(12)
		corpus := make([]Candidate, n)
		for j := range corpus {
			corpus[j] = randCandidate(rng)
		}
		limit := 1000 // no truncation: compare full identity sets

		want := identitySet(foldCorpus(corpus, limit))

		// Try several independent shuffles of the SAME corpus.
		for s := 0; s < 4; s++ {
			shuffled := append([]Candidate(nil), corpus...)
			rng.Shuffle(len(shuffled), func(a, b int) {
				shuffled[a], shuffled[b] = shuffled[b], shuffled[a]
			})
			got := identitySet(foldCorpus(shuffled, limit))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seed=%#x iter=%d shuffle=%d: identity set depends on add-order\nwant=%v\ngot =%v\ncorpus=%+v",
					propSeed, i, s, want, got, corpus)
			}
		}
	}
}

// Invariant (2, continued): the sticky/fill rules are order-independent in
// detail. For each identity key, merged InLibrary == OR of contributors'
// InLibrary, and merged TMDBID/TVDBID == the first non-zero contributor id for
// that identity (ids fill from 0, never overwrite). Both are permutation-stable
// because they don't depend on which contributor came first (OR / any-non-zero).
func TestProp_Fold_StickyInLibraryAndIDFill(t *testing.T) {
	rng := rand.New(rand.NewSource(propSeed + 2))
	const iters = 1500
	for i := 0; i < iters; i++ {
		n := 1 + rng.Intn(12)
		corpus := make([]Candidate, n)
		for j := range corpus {
			corpus[j] = randCandidate(rng)
		}

		// Expected per-identity: OR of InLibrary; any-non-zero for each id.
		type expect struct {
			inLib          bool
			anyTMDB, anyTV bool
		}
		exp := map[string]*expect{}
		for _, c := range corpus {
			k := dedupeKey(c)
			e := exp[k]
			if e == nil {
				e = &expect{}
				exp[k] = e
			}
			if c.InLibrary {
				e.inLib = true
			}
			if c.TMDBID != 0 {
				e.anyTMDB = true
			}
			if c.TVDBID != 0 {
				e.anyTV = true
			}
		}

		got := foldCorpus(corpus, 1000)
		for _, c := range got {
			k := dedupeKey(c)
			e := exp[k]
			if e == nil {
				t.Fatalf("seed=%#x iter=%d: folded candidate with unknown identity %q", propSeed, i, k)
			}
			if c.InLibrary != e.inLib {
				t.Fatalf("seed=%#x iter=%d key=%q: InLibrary=%v want sticky-OR %v\ncorpus=%+v",
					propSeed, i, k, c.InLibrary, e.inLib, corpus)
			}
			// If any contributor had a tmdb/tvdb id, the merged view must carry one
			// (ids fill from 0). dedupeKey with a tmdb identity guarantees TMDBID>0
			// already; this checks the fill path for the non-key id too.
			if e.anyTMDB && c.TMDBID == 0 {
				t.Fatalf("seed=%#x iter=%d key=%q: a contributor had a TMDB id but merged view has none", propSeed, i, k)
			}
			if e.anyTV && c.TVDBID == 0 {
				t.Fatalf("seed=%#x iter=%d key=%q: a contributor had a TVDB id but merged view has none", propSeed, i, k)
			}
		}
	}
}

// Invariant (2, idempotence): folding a corpus, then folding its OUTPUT again,
// yields the same identity set (dedupe is a fixed point). Merging a candidate
// into itself must not flip InLibrary or clear ids.
func TestProp_Fold_IdempotentOnIdentity(t *testing.T) {
	rng := rand.New(rand.NewSource(propSeed + 3))
	const iters = 1500
	for i := 0; i < iters; i++ {
		n := 1 + rng.Intn(12)
		corpus := make([]Candidate, n)
		for j := range corpus {
			corpus[j] = randCandidate(rng)
		}
		once := foldCorpus(corpus, 1000)
		twice := foldCorpus(once, 1000)
		if !reflect.DeepEqual(identitySet(once), identitySet(twice)) {
			t.Fatalf("seed=%#x iter=%d: fold is not idempotent on identity\nonce =%v\ntwice=%v\ncorpus=%+v",
				propSeed, i, identitySet(once), identitySet(twice), corpus)
		}
		// The full output slices must also be identity-stable element-for-element:
		// a second fold changes nothing about the ordered, deduped list.
		if len(once) != len(twice) {
			t.Fatalf("seed=%#x iter=%d: second fold changed length %d->%d", propSeed, i, len(once), len(twice))
		}
		for j := range once {
			if dedupeKey(once[j]) != dedupeKey(twice[j]) || once[j].InLibrary != twice[j].InLibrary {
				t.Fatalf("seed=%#x iter=%d idx=%d: second fold perturbed output\nonce =%+v\ntwice=%+v",
					propSeed, i, j, once[j], twice[j])
			}
		}
	}
}

// Invariant (3): dedupeAndOrder keeps the first upstream occurrence as the
// relevance rank, even when a later duplicate enriches or changes ownership.
// The owned/outside blend may partition that stream, but it must not alphabetize
// either partition.
func TestProp_DedupeAndOrder_PreservesUpstreamOrderWithinPartitions(t *testing.T) {
	rng := rand.New(rand.NewSource(propSeed + 4))
	const iters = 1500
	for i := 0; i < iters; i++ {
		n := 1 + rng.Intn(12)
		corpus := make([]Candidate, n)
		for j := range corpus {
			corpus[j] = randCandidate(rng)
		}
		limit := 1000

		got := foldCorpus(corpus, limit)
		want := sourceOrderByFinalPartition(corpus)
		if !reflect.DeepEqual(orderKeys(got), want) {
			t.Fatalf("seed=%#x iter=%d: source relevance order was not preserved\nwant=%v\ngot =%v\ncorpus=%+v",
				propSeed, i, want, orderKeys(got), corpus)
		}
		assertInLibraryFirst(t, got)
	}
}

// orderKeys projects output to the canonical identity sequence: relevance order
// is meaningful only among grounded identities, never display names.
func orderKeys(cands []Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = dedupeKey(c)
	}
	return out
}

func sourceOrderByFinalPartition(corpus []Candidate) []string {
	byKey := make(map[string]*Candidate, len(corpus))
	order := make([]string, 0, len(corpus))
	for _, candidate := range corpus {
		key := dedupeKey(candidate)
		if existing, ok := byKey[key]; ok {
			mergeCandidate(existing, candidate)
			continue
		}
		copy := candidate
		byKey[key] = &copy
		order = append(order, key)
	}
	out := make([]string, 0, len(order))
	for _, inLibrary := range []bool{true, false} {
		for _, key := range order {
			if byKey[key].InLibrary == inLibrary {
				out = append(out, key)
			}
		}
	}
	return out
}

func assertInLibraryFirst(t *testing.T, cands []Candidate) {
	t.Helper()
	seenNotInLib := false
	for _, c := range cands {
		if !c.InLibrary {
			seenNotInLib = true
		} else if seenNotInLib {
			t.Fatalf("in-library candidate after a not-in-library one: %+v", cands)
		}
	}
}

// Truncation takes the head of each relevance-preserving partition. It never
// reaches deeper alphabetic entries merely because their names sort earlier.
func TestProp_DedupeAndOrder_TruncationUsesPartitionHeads(t *testing.T) {
	rng := rand.New(rand.NewSource(propSeed + 5))
	const iters = 1000
	for i := 0; i < iters; i++ {
		n := 2 + rng.Intn(12)
		corpus := make([]Candidate, n)
		for j := range corpus {
			corpus[j] = randCandidate(rng)
		}
		full := foldCorpus(corpus, 1000)
		limit := 1 + rng.Intn(len(full)+2)

		got := foldCorpus(corpus, limit)
		wantCandidates := blendHeads(full, limit)
		want := orderKeys(wantCandidates)
		if !reflect.DeepEqual(orderKeys(got), want) {
			t.Fatalf("seed=%#x iter=%d limit=%d: truncation did not keep partition heads\nwant=%v\ngot =%v",
				propSeed, i, limit, want, orderKeys(got))
		}
		if len(want) != min(limit, len(full)) {
			t.Fatalf("seed=%#x iter=%d limit=%d: blended length=%d want %d", propSeed, i, limit, len(want), min(limit, len(full)))
		}
	}
}

func blendHeads(full []Candidate, limit int) []Candidate {
	if len(full) <= limit {
		return full
	}
	ownedEnd := 0
	for ownedEnd < len(full) && full[ownedEnd].InLibrary {
		ownedEnd++
	}
	owned, outside := full[:ownedEnd], full[ownedEnd:]
	if len(owned) == 0 {
		return outside[:min(limit, len(outside))]
	}
	if len(outside) == 0 {
		return owned[:min(limit, len(owned))]
	}
	outsideSlots := min(max(1, limit/4), len(outside))
	ownedSlots := min(limit-outsideSlots, len(owned))
	outsideSlots = min(limit-ownedSlots, len(outside))
	out := append([]Candidate(nil), owned[:ownedSlots]...)
	return append(out, outside[:outsideSlots]...)
}
