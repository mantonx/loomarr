package inventory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"unicode"
)

const (
	maxStringBytes    = 64 << 10
	maxExtensionBytes = 1 << 20
	maxCollection     = 4096
	maxSources        = 128
	maxStreams        = 512
)

func ValidateItemRef(ref ItemRef) error {
	hasID := strings.TrimSpace(string(ref.ID)) != ""
	hasOrigin := ref.Origin != nil && strings.TrimSpace(string(ref.Origin.Authority)) != "" &&
		strings.TrimSpace(ref.Origin.ExternalItemID) != ""
	if hasID == hasOrigin {
		return fmt.Errorf("%w: item reference needs exactly one identity", ErrInvalid)
	}
	return nil
}

func ValidateSnapshot(snapshot Snapshot) (Snapshot, error) {
	snapshot.Origin.Authority = AuthorityID(strings.TrimSpace(string(snapshot.Origin.Authority)))
	snapshot.Origin.ExternalItemID = strings.TrimSpace(snapshot.Origin.ExternalItemID)
	snapshot.Kind = ItemKind(strings.TrimSpace(string(snapshot.Kind)))
	if snapshot.Origin.Authority == "" || snapshot.Origin.ExternalItemID == "" || snapshot.Kind == "" ||
		snapshot.Observation.SchemaVersion <= 0 || snapshot.Observation.ObservedAt.IsZero() ||
		len(snapshot.Sources) > maxSources || len(snapshot.ExternalIDs) > maxCollection {
		return Snapshot{}, ErrInvalid
	}
	var err error
	snapshot.Observation, err = validateObservation(snapshot.Observation, validateItemFacts)
	if err != nil {
		return Snapshot{}, err
	}
	seenIDs := make(map[string]bool, len(snapshot.ExternalIDs))
	for i := range snapshot.ExternalIDs {
		snapshot.ExternalIDs[i].Namespace = strings.ToLower(strings.TrimSpace(snapshot.ExternalIDs[i].Namespace))
		snapshot.ExternalIDs[i].Value = strings.TrimSpace(snapshot.ExternalIDs[i].Value)
		key := snapshot.ExternalIDs[i].Namespace + "\x00" + snapshot.ExternalIDs[i].Value
		if snapshot.ExternalIDs[i].Namespace == "" || snapshot.ExternalIDs[i].Value == "" || seenIDs[key] ||
			!bounded(snapshot.ExternalIDs[i].Namespace) || !bounded(snapshot.ExternalIDs[i].Value) {
			return Snapshot{}, ErrInvalid
		}
		seenIDs[key] = true
	}
	sort.Slice(snapshot.ExternalIDs, func(i, j int) bool {
		if snapshot.ExternalIDs[i].Namespace == snapshot.ExternalIDs[j].Namespace {
			return snapshot.ExternalIDs[i].Value < snapshot.ExternalIDs[j].Value
		}
		return snapshot.ExternalIDs[i].Namespace < snapshot.ExternalIDs[j].Namespace
	})
	seenSources := make(map[string]bool, len(snapshot.Sources))
	for i := range snapshot.Sources {
		source := &snapshot.Sources[i]
		source.ExternalSourceID = strings.TrimSpace(source.ExternalSourceID)
		source.Kind = SourceKind(strings.TrimSpace(string(source.Kind)))
		source.Revision = strings.TrimSpace(source.Revision)
		if source.ExternalSourceID == "" || source.Kind == "" || source.Revision == "" ||
			seenSources[source.ExternalSourceID] || !bounded(source.ExternalSourceID) || !bounded(source.Revision) {
			return Snapshot{}, ErrInvalid
		}
		seenSources[source.ExternalSourceID] = true
		if err := validateLocator(snapshot.Origin, source); err != nil {
			return Snapshot{}, err
		}
		source.Observation, err = validateObservation(source.Observation, validateSourceFacts)
		if err != nil {
			return Snapshot{}, err
		}
	}
	return snapshot, nil
}

func ValidateMeasurement(measurement Measurement) (Measurement, error) {
	measurement.SourceID = SourceID(strings.TrimSpace(string(measurement.SourceID)))
	measurement.Revision = strings.TrimSpace(measurement.Revision)
	if measurement.SourceID == "" || measurement.Revision == "" ||
		measurement.Observation.SchemaVersion <= 0 || measurement.Observation.ObservedAt.IsZero() {
		return Measurement{}, ErrInvalid
	}
	observation, err := validateObservation(measurement.Observation, validateSourceFacts)
	if err != nil {
		return Measurement{}, err
	}
	measurement.Observation = observation
	return measurement, nil
}

// ValidateItem protects direct Store reads and test doubles from malformed persisted documents.
func ValidateItem(item Item) (Item, error) {
	item.ID = ItemID(strings.TrimSpace(string(item.ID)))
	item.Kind = ItemKind(strings.TrimSpace(string(item.Kind)))
	if item.ID == "" || item.Kind == "" || len(item.Origins) == 0 || len(item.Origins) > maxCollection ||
		len(item.ExternalIDs) > maxCollection || len(item.Sources) > maxSources {
		return Item{}, ErrInvalid
	}
	for i := range item.Origins {
		origin := &item.Origins[i]
		origin.Key.Authority = AuthorityID(strings.TrimSpace(string(origin.Key.Authority)))
		origin.Key.ExternalItemID = strings.TrimSpace(origin.Key.ExternalItemID)
		if origin.Key.Authority == "" || origin.Key.ExternalItemID == "" || origin.LastSeenAt.IsZero() {
			return Item{}, ErrInvalid
		}
		observation, err := validateObservation(origin.Observation, validateItemFacts)
		if err != nil {
			return Item{}, err
		}
		origin.Observation = observation
	}
	for _, externalID := range item.ExternalIDs {
		if strings.TrimSpace(externalID.Namespace) == "" || strings.TrimSpace(externalID.Value) == "" ||
			!bounded(externalID.Namespace) || !bounded(externalID.Value) {
			return Item{}, ErrInvalid
		}
	}
	for i := range item.Sources {
		source := &item.Sources[i]
		if source.ID == "" || source.ItemID != item.ID || source.Kind == "" || source.Revision == "" ||
			len(source.Origins) == 0 || len(source.Origins) > maxCollection {
			return Item{}, ErrInvalid
		}
		for j := range source.Origins {
			origin := &source.Origins[j]
			candidate := SourceSnapshot{
				ExternalSourceID: origin.ExternalSourceID, Kind: source.Kind, Revision: source.Revision,
				Locator: origin.Locator, Observation: origin.Observation,
			}
			if err := validateLocator(origin.Key, &candidate); err != nil || origin.LastSeenAt.IsZero() {
				return Item{}, ErrInvalid
			}
			observation, err := validateObservation(origin.Observation, validateSourceFacts)
			if err != nil {
				return Item{}, err
			}
			origin.Locator = candidate.Locator
			origin.Observation = observation
		}
		if source.Measurement != nil {
			measurement, err := ValidateMeasurement(*source.Measurement)
			if err != nil || measurement.SourceID != source.ID {
				return Item{}, ErrInvalid
			}
			source.Measurement = &measurement
		}
	}
	return item, nil
}

func validateObservation[T any](observation Observation[T], facts func(*T) error) (Observation[T], error) {
	observation.ObservedAt = observation.ObservedAt.UTC()
	if len(observation.Coverage) > maxCollection {
		return Observation[T]{}, ErrInvalid
	}
	cleanCoverage := make(map[string]Coverage, len(observation.Coverage))
	for key, state := range observation.Coverage {
		key = strings.TrimSpace(key)
		if key == "" || !bounded(key) || (state != CoveragePresent && state != CoverageEmpty) {
			return Observation[T]{}, ErrInvalid
		}
		cleanCoverage[key] = state
	}
	observation.Coverage = cleanCoverage
	if err := facts(&observation.Facts); err != nil {
		return Observation[T]{}, err
	}
	extension, err := sanitizeExtension(observation.Extension)
	if err != nil {
		return Observation[T]{}, err
	}
	observation.Extension = extension
	return observation, nil
}

func validateItemFacts(facts *ItemFacts) error {
	stringsToCheck := []*string{&facts.Name, &facts.OriginalTitle, &facts.SortTitle, &facts.Overview,
		&facts.PremiereDate, &facts.OfficialRating}
	for _, value := range stringsToCheck {
		*value = strings.TrimSpace(*value)
		if !bounded(*value) {
			return ErrInvalid
		}
	}
	if facts.ProductionYear < 0 || facts.SeasonNumber < 0 || facts.EpisodeNumber < 0 || facts.EpisodeEnd < 0 ||
		facts.RuntimeMillis < 0 || facts.CommunityScore < 0 ||
		facts.CommunityScore > 10 || len(facts.Genres) > maxCollection || len(facts.Tags) > maxCollection ||
		len(facts.Studios) > maxCollection || len(facts.People) > maxCollection ||
		len(facts.Artwork) > maxCollection || len(facts.Relationships) > maxCollection {
		return ErrInvalid
	}
	for _, values := range [][]string{facts.Genres, facts.Tags, facts.Studios} {
		if err := validateStrings(values); err != nil {
			return err
		}
	}
	for _, person := range facts.People {
		if !bounded(person.Name) || !bounded(person.Role) {
			return ErrInvalid
		}
	}
	for _, artwork := range facts.Artwork {
		if !bounded(artwork.Kind) || !bounded(artwork.Ref) {
			return ErrInvalid
		}
	}
	for _, relationship := range facts.Relationships {
		if !bounded(relationship.Kind) || !bounded(relationship.ExternalID) {
			return ErrInvalid
		}
	}
	return nil
}

func validateSourceFacts(facts *SourceFacts) error {
	if facts.SizeBytes < 0 || facts.Bitrate < 0 || facts.DurationMillis < 0 || len(facts.Streams) > maxStreams {
		return ErrInvalid
	}
	if !bounded(facts.Protocol) || !bounded(facts.Container) {
		return ErrInvalid
	}
	seen := make(map[int]bool, len(facts.Streams))
	for i := range facts.Streams {
		stream := &facts.Streams[i]
		stream.Kind = StreamKind(strings.ToLower(strings.TrimSpace(string(stream.Kind))))
		if stream.Index < 0 || seen[stream.Index] ||
			(stream.Kind != StreamVideo && stream.Kind != StreamAudio && stream.Kind != StreamSubtitle) ||
			stream.Channels < 0 || stream.SampleRate < 0 || stream.Width < 0 || stream.Height < 0 {
			return ErrInvalid
		}
		seen[stream.Index] = true
		for _, value := range []string{stream.Codec, stream.Profile, stream.Level, stream.Language,
			stream.Title, stream.ChannelLayout, stream.FrameRate, stream.PixelFormat, stream.ColorSpace,
			stream.ColorTransfer, stream.ColorPrimaries, stream.SubtitleFormat} {
			if !bounded(value) {
				return ErrInvalid
			}
		}
	}
	sort.Slice(facts.Streams, func(i, j int) bool { return facts.Streams[i].Index < facts.Streams[j].Index })
	return nil
}

func validateLocator(origin OriginKey, source *SourceSnapshot) error {
	locator := &source.Locator
	locator.Authority = AuthorityID(strings.TrimSpace(string(locator.Authority)))
	locator.ExternalItemID = strings.TrimSpace(locator.ExternalItemID)
	locator.ExternalSourceID = strings.TrimSpace(locator.ExternalSourceID)
	locator.Path = strings.TrimSpace(locator.Path)
	if !bounded(string(locator.Authority)) || !bounded(locator.ExternalItemID) ||
		!bounded(locator.ExternalSourceID) || !bounded(locator.Path) {
		return ErrInvalid
	}
	switch source.Kind {
	case SourceLibraryOriginal:
		if locator.Authority != origin.Authority || locator.ExternalItemID != origin.ExternalItemID ||
			locator.ExternalSourceID != source.ExternalSourceID || locator.Path != "" {
			return ErrInvalid
		}
	case SourceLocalFile:
		if locator.Path == "" || strings.Contains(locator.Path, "://") || locator.Authority != "" ||
			locator.ExternalItemID != "" || locator.ExternalSourceID != "" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func validateStrings(values []string) error {
	for _, value := range values {
		if !bounded(strings.TrimSpace(value)) {
			return ErrInvalid
		}
	}
	return nil
}

func bounded(value string) bool { return len(value) <= maxStringBytes }

func sanitizeExtension(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	if len(raw) > maxExtensionBytes {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: extension: %v", ErrInvalid, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("%w: extension has trailing data", ErrInvalid)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: extension must be an object", ErrInvalid)
	}
	clean, _ := sanitizeValue(object)
	encoded, err := json.Marshal(clean)
	if err != nil || len(encoded) > maxExtensionBytes {
		return nil, ErrInvalid
	}
	return encoded, nil
}

func sanitizeValue(value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(typed))
		for key, child := range typed {
			if unsafeExtensionKey(key) {
				continue
			}
			if next, ok := sanitizeValue(child); ok {
				clean[key] = next
			}
		}
		return clean, true
	case []any:
		if len(typed) > maxCollection {
			return nil, false
		}
		clean := make([]any, 0, len(typed))
		for _, child := range typed {
			if next, ok := sanitizeValue(child); ok {
				clean = append(clean, next)
			}
		}
		return clean, true
	case string:
		if !bounded(typed) || secretURL(typed) {
			return nil, false
		}
		return typed, true
	default:
		return typed, true
	}
}

func unsafeExtensionKey(key string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
	for _, fragment := range []string{"apikey", "accesstoken", "authtoken", "password", "credential",
		"playbacksession", "transcodesession", "sessionid", "userstate", "userdata", "imagebytes",
		"path", "mediasources", "mediastreams", "liveprogram", "opentoken", "livestreamid",
		"requiredhttpheaders", "transcodingurl", "directstreamurl", "streamurl", "playbackinfo"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func secretURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	if parsed.User != nil {
		return true
	}
	for key := range parsed.Query() {
		if unsafeExtensionKey(key) {
			return true
		}
	}
	return false
}
