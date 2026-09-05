// Package inventory owns Loomarr's durable, provider-neutral understanding of media (design §5,
// V66). Importers describe what they observed; consumers resolve stable items and sources without
// receiving Emby/Jellyfin response types or operational credentials.
package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type (
	ItemID      string
	SourceID    string
	AuthorityID string
	ItemKind    string
	SourceKind  string
	StreamKind  string
	Coverage    string
)

const (
	CoveragePresent Coverage = "present"
	CoverageEmpty   Coverage = "empty"

	SourceLibraryOriginal SourceKind = "library_original"
	SourceLocalFile       SourceKind = "local_file"

	StreamVideo    StreamKind = "video"
	StreamAudio    StreamKind = "audio"
	StreamSubtitle StreamKind = "subtitle"
)

var (
	ErrInvalid            = errors.New("inventory: invalid document")
	ErrIdentityConflict   = errors.New("inventory: identity conflict")
	ErrSourceRevisionGone = errors.New("inventory: source revision changed")
)

// OriginKey is the exact importer identity of an item. Authority identifies one configured server
// or scanner root; ExternalItemID is stable only within that authority.
type OriginKey struct {
	Authority      AuthorityID `json:"authority"`
	ExternalItemID string      `json:"externalItemId"`
}

// ExternalID is a grounded cross-provider identity asserted by one Origin.
type ExternalID struct {
	Namespace string `json:"namespace"`
	Value     string `json:"value"`
}

type Person struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

type Rating struct {
	System string  `json:"system"`
	Value  float64 `json:"value"`
}

type ArtworkRef struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

type Relationship struct {
	Kind       string `json:"kind"`
	ExternalID string `json:"externalId"`
}

// ItemFacts intentionally retains more than today's consumers need. Typed fields are the stable
// projection Loomarr understands; Observation.Extension preserves bounded safe importer fields.
type ItemFacts struct {
	Name           string         `json:"name,omitempty"`
	OriginalTitle  string         `json:"originalTitle,omitempty"`
	SortTitle      string         `json:"sortTitle,omitempty"`
	Overview       string         `json:"overview,omitempty"`
	Genres         []string       `json:"genres,omitempty"`
	Tags           []string       `json:"tags,omitempty"`
	Studios        []string       `json:"studios,omitempty"`
	People         []Person       `json:"people,omitempty"`
	ProductionYear int            `json:"productionYear,omitempty"`
	SeasonNumber   int            `json:"seasonNumber,omitempty"`
	EpisodeNumber  int            `json:"episodeNumber,omitempty"`
	EpisodeEnd     int            `json:"episodeEnd,omitempty"`
	PremiereDate   string         `json:"premiereDate,omitempty"`
	OfficialRating string         `json:"officialRating,omitempty"`
	CommunityScore float64        `json:"communityScore,omitempty"`
	RuntimeMillis  int64          `json:"runtimeMillis,omitempty"`
	Artwork        []ArtworkRef   `json:"artwork,omitempty"`
	Relationships  []Relationship `json:"relationships,omitempty"`
}

type Disposition struct {
	Default bool `json:"default,omitempty"`
	Forced  bool `json:"forced,omitempty"`
}

// Stream is ordered by Index, the source-global stream index. Audio consumers derive ffmpeg's
// 0:a:N ordinal from the ordered audio subset rather than treating the global index as an ordinal.
type Stream struct {
	Index          int         `json:"index"`
	Kind           StreamKind  `json:"kind"`
	Codec          string      `json:"codec,omitempty"`
	Profile        string      `json:"profile,omitempty"`
	Level          string      `json:"level,omitempty"`
	Language       string      `json:"language,omitempty"`
	Title          string      `json:"title,omitempty"`
	Disposition    Disposition `json:"disposition,omitempty"`
	Channels       int         `json:"channels,omitempty"`
	ChannelLayout  string      `json:"channelLayout,omitempty"`
	SampleRate     int         `json:"sampleRate,omitempty"`
	Width          int         `json:"width,omitempty"`
	Height         int         `json:"height,omitempty"`
	FrameRate      string      `json:"frameRate,omitempty"`
	PixelFormat    string      `json:"pixelFormat,omitempty"`
	ColorSpace     string      `json:"colorSpace,omitempty"`
	ColorTransfer  string      `json:"colorTransfer,omitempty"`
	ColorPrimaries string      `json:"colorPrimaries,omitempty"`
	HDR            bool        `json:"hdr,omitempty"`
	Interlaced     bool        `json:"interlaced,omitempty"`
	SubtitleFormat string      `json:"subtitleFormat,omitempty"`
}

type SourceFacts struct {
	Protocol         string   `json:"protocol,omitempty"`
	Container        string   `json:"container,omitempty"`
	SizeBytes        int64    `json:"sizeBytes,omitempty"`
	Bitrate          int64    `json:"bitrate,omitempty"`
	DurationMillis   int64    `json:"durationMillis,omitempty"`
	ModifiedUnixNano int64    `json:"modifiedUnixNano,omitempty"`
	UnsafePreroll    bool     `json:"unsafePreroll,omitempty"`
	Streams          []Stream `json:"streams,omitempty"`
}

// Locator is protected operational data, never an authenticated URL. Library sources retain only
// stable external identity; Source Access reconstructs credentials at use time. Local paths are
// protected at export/log boundaries in the same way as other operational paths.
type Locator struct {
	Authority        AuthorityID `json:"authority,omitempty"`
	ExternalItemID   string      `json:"externalItemId,omitempty"`
	ExternalSourceID string      `json:"externalSourceId,omitempty"`
	Path             string      `json:"path,omitempty"`
}

// Observation records current imported facts. Coverage keys distinguish unknown (key absent) from
// explicitly observed empty (CoverageEmpty).
type Observation[T any] struct {
	SchemaVersion int                 `json:"schemaVersion"`
	ObservedAt    time.Time           `json:"observedAt"`
	Coverage      map[string]Coverage `json:"coverage,omitempty"`
	Facts         T                   `json:"facts"`
	Extension     json.RawMessage     `json:"extension,omitempty"`
}

type SourceSnapshot struct {
	ExternalSourceID string                   `json:"externalSourceId"`
	Kind             SourceKind               `json:"kind"`
	Revision         string                   `json:"revision"`
	Locator          Locator                  `json:"locator"`
	Observation      Observation[SourceFacts] `json:"observation"`
}

// Snapshot is one importer's atomic current observation of an item and its observed sources.
type Snapshot struct {
	Origin      OriginKey              `json:"origin"`
	Kind        ItemKind               `json:"kind"`
	Observation Observation[ItemFacts] `json:"observation"`
	ExternalIDs []ExternalID           `json:"externalIds,omitempty"`
	Sources     []SourceSnapshot       `json:"sources,omitempty"`
}

type ItemOrigin struct {
	Key         OriginKey              `json:"key"`
	Observation Observation[ItemFacts] `json:"observation"`
	LastSeenAt  time.Time              `json:"lastSeenAt"`
	MissingAt   time.Time              `json:"missingAt,omitempty"`
}

type SourceOrigin struct {
	Key              OriginKey                `json:"key"`
	ExternalSourceID string                   `json:"externalSourceId"`
	Locator          Locator                  `json:"locator"`
	Observation      Observation[SourceFacts] `json:"observation"`
	LastSeenAt       time.Time                `json:"lastSeenAt"`
	MissingAt        time.Time                `json:"missingAt,omitempty"`
}

type Measurement struct {
	SourceID    SourceID                 `json:"sourceId"`
	Revision    string                   `json:"revision"`
	Observation Observation[SourceFacts] `json:"observation"`
}

type Source struct {
	ID          SourceID       `json:"id"`
	ItemID      ItemID         `json:"itemId"`
	Kind        SourceKind     `json:"kind"`
	Revision    string         `json:"revision"`
	Origins     []SourceOrigin `json:"origins"`
	Measurement *Measurement   `json:"measurement,omitempty"`
}

type Item struct {
	ID          ItemID       `json:"id"`
	Kind        ItemKind     `json:"kind"`
	Origins     []ItemOrigin `json:"origins"`
	ExternalIDs []ExternalID `json:"externalIds,omitempty"`
	Sources     []Source     `json:"sources,omitempty"`
}

type ItemRef struct {
	ID     ItemID     `json:"id,omitempty"`
	Origin *OriginKey `json:"origin,omitempty"`
}

type SourceRequest struct {
	Item             ItemRef       `json:"item"`
	Kinds            []SourceKind  `json:"kinds,omitempty"`
	Now              time.Time     `json:"now"`
	MaxAge           time.Duration `json:"maxAge,omitempty"`
	RequiredCoverage []string      `json:"requiredCoverage,omitempty"`
}

type ResolvedSource struct {
	ID          SourceID                 `json:"id"`
	ItemID      ItemID                   `json:"itemId"`
	Kind        SourceKind               `json:"kind"`
	Revision    string                   `json:"revision"`
	Locator     Locator                  `json:"-"`
	Observation Observation[SourceFacts] `json:"observation"`
	Measurement *Measurement             `json:"measurement,omitempty"`
}

// Service is the consumer-facing Media Inventory port from design §5 V66.
type Service interface {
	ApplySnapshot(context.Context, Snapshot) (ItemID, error)
	Item(context.Context, ItemRef) (Item, bool, error)
	ResolveSource(context.Context, SourceRequest) (ResolvedSource, bool, error)
	RecordMeasurement(context.Context, Measurement) error
	MarkUnseen(context.Context, AuthorityID, time.Time, []OriginKey) error
}

// Repository is the durable command/read seam implemented by store. It remains aggregate-shaped so
// callers cannot partially update the six-table representation.
type Repository interface {
	ApplyInventorySnapshot(context.Context, Snapshot) (ItemID, error)
	InventoryItem(context.Context, ItemRef) (Item, bool, error)
	RecordInventoryMeasurement(context.Context, Measurement) error
	MarkInventoryUnseen(context.Context, AuthorityID, time.Time, []OriginKey) error
}
