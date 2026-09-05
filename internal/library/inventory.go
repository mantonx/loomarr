package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/loomarr/loomarr/internal/inventory"
)

const inventoryFields = "ProviderIds,OriginalTitle,SortName,Overview,Genres,Tags,Studios,People," +
	"ProductionYear,PremiereDate,OfficialRating,CommunityRating,RunTimeTicks,ImageTags," +
	"ParentId,SeriesId,SeasonId,IndexNumber,IndexNumberEnd,ParentIndexNumber,DateLastSaved,MediaSources,MediaStreams"

type inventoryItemResponse struct {
	Items []json.RawMessage `json:"Items"`
}

type inventoryWireItem struct {
	ID              string            `json:"Id"`
	Type            string            `json:"Type"`
	Name            string            `json:"Name"`
	OriginalTitle   string            `json:"OriginalTitle"`
	SortName        string            `json:"SortName"`
	Overview        string            `json:"Overview"`
	Genres          []string          `json:"Genres"`
	Tags            []string          `json:"Tags"`
	ProductionYear  int               `json:"ProductionYear"`
	PremiereDate    string            `json:"PremiereDate"`
	OfficialRating  string            `json:"OfficialRating"`
	CommunityRating float64           `json:"CommunityRating"`
	RunTimeTicks    int64             `json:"RunTimeTicks"`
	DateLastSaved   string            `json:"DateLastSaved"`
	ProviderIDs     map[string]string `json:"ProviderIds"`
	ImageTags       map[string]string `json:"ImageTags"`
	Studios         []struct {
		Name string `json:"Name"`
	} `json:"Studios"`
	People []struct {
		Name string `json:"Name"`
		Type string `json:"Type"`
		Role string `json:"Role"`
	} `json:"People"`
	ParentID          string                `json:"ParentId"`
	SeriesID          string                `json:"SeriesId"`
	SeasonID          string                `json:"SeasonId"`
	IndexNumber       int                   `json:"IndexNumber"`
	IndexNumberEnd    int                   `json:"IndexNumberEnd"`
	ParentIndexNumber int                   `json:"ParentIndexNumber"`
	MediaSources      []inventoryWireSource `json:"MediaSources"`
	MediaStreams      []inventoryWireStream `json:"MediaStreams"`
}

type inventoryWireSource struct {
	ID            string                `json:"Id"`
	Protocol      string                `json:"Protocol"`
	Container     string                `json:"Container"`
	Size          int64                 `json:"Size"`
	Bitrate       int64                 `json:"Bitrate"`
	RunTimeTicks  int64                 `json:"RunTimeTicks"`
	ETag          string                `json:"ETag"`
	DateLastSaved string                `json:"DateLastSaved"`
	MediaStreams  []inventoryWireStream `json:"MediaStreams"`
}

type inventoryWireStream struct {
	Index          *int            `json:"Index"`
	Type           string          `json:"Type"`
	Codec          string          `json:"Codec"`
	Profile        string          `json:"Profile"`
	Level          json.RawMessage `json:"Level"`
	Language       string          `json:"Language"`
	Title          string          `json:"Title"`
	IsDefault      bool            `json:"IsDefault"`
	IsForced       bool            `json:"IsForced"`
	Channels       int             `json:"Channels"`
	ChannelLayout  string          `json:"ChannelLayout"`
	SampleRate     int             `json:"SampleRate"`
	Width          int             `json:"Width"`
	Height         int             `json:"Height"`
	RealFrameRate  float64         `json:"RealFrameRate"`
	PixelFormat    string          `json:"PixelFormat"`
	ColorSpace     string          `json:"ColorSpace"`
	ColorTransfer  string          `json:"ColorTransfer"`
	ColorPrimaries string          `json:"ColorPrimaries"`
	VideoRangeType string          `json:"VideoRangeType"`
	IsInterlaced   bool            `json:"IsInterlaced"`
	SubtitleCodec  string          `json:"SubtitleCodec"`
}

// InventoryOrigin resolves the safe durable Origin for an item without making a network request.
// The normalized configured endpoint and flavor identify one importer authority; the API token is
// deliberately absent, so rotation does not fork identity and a warm inventory read performs no I/O.
func (c *Client) InventoryOrigin(itemID string) (inventory.OriginKey, error) {
	op, err := c.operation()
	if err != nil {
		return inventory.OriginKey{}, err
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return inventory.OriginKey{}, inventory.ErrInvalid
	}
	connection := op.Connection()
	digest := sha256.Sum256([]byte(string(connection.Flavor) + "\x00" + connection.BaseURL))
	return inventory.OriginKey{
		Authority:      inventory.AuthorityID("library:" + string(connection.Flavor) + ":" + hex.EncodeToString(digest[:12])),
		ExternalItemID: itemID,
	}, nil
}

// InventorySnapshot imports one rich item into the provider-neutral Media Inventory shape. The
// caller owns persistence; a missing item is (zero, false, nil), preserving explicit scan semantics.
func (c *Client) InventorySnapshot(ctx context.Context, itemID string) (inventory.Snapshot, bool, error) {
	op, err := c.operation()
	if err != nil {
		return inventory.Snapshot{}, false, err
	}
	origin, err := op.InventoryOrigin(itemID)
	if err != nil {
		return inventory.Snapshot{}, false, err
	}
	q := url.Values{}
	q.Set("Ids", itemID)
	q.Set("Fields", inventoryFields)
	q.Set("EnableUserData", "false")
	q.Set("Limit", "1")
	req, err := op.newRequest(ctx, http.MethodGet, "/Items?"+q.Encode(), nil)
	if err != nil {
		return inventory.Snapshot{}, false, err
	}
	op.flavor().applyTokenAuth(req, op.token(), op.deviceID)
	var response inventoryItemResponse
	if err := op.do(req, &response); err != nil {
		return inventory.Snapshot{}, false, err
	}
	if len(response.Items) != 1 {
		return inventory.Snapshot{}, false, nil
	}
	var item inventoryWireItem
	if err := json.Unmarshal(response.Items[0], &item); err != nil {
		return inventory.Snapshot{}, false, fmt.Errorf("decode inventory item: %w", err)
	}
	if item.ID != itemID {
		return inventory.Snapshot{}, false, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(response.Items[0], &fields); err != nil {
		return inventory.Snapshot{}, false, fmt.Errorf("decode inventory item coverage: %w", err)
	}
	at := time.Now().UTC()
	snapshot := inventory.Snapshot{
		Origin: origin,
		Kind:   inventory.ItemKind(strings.ToLower(strings.TrimSpace(item.Type))),
		Observation: inventory.Observation[inventory.ItemFacts]{
			SchemaVersion: 1,
			ObservedAt:    at,
			Coverage:      itemCoverage(fields),
			Facts:         itemFacts(item),
			Extension:     append(json.RawMessage(nil), response.Items[0]...),
		},
		ExternalIDs: externalIDs(item.ProviderIDs),
	}
	for i, source := range item.MediaSources {
		if strings.TrimSpace(source.ID) == "" {
			source.ID = "default-" + strconv.Itoa(i)
		}
		snapshot.Sources = append(snapshot.Sources, sourceSnapshot(origin, item, source, at))
	}
	if len(snapshot.Sources) == 0 && len(item.MediaStreams) > 0 {
		snapshot.Sources = append(snapshot.Sources, sourceSnapshot(origin, item, inventoryWireSource{
			ID: "default", RunTimeTicks: item.RunTimeTicks, DateLastSaved: item.DateLastSaved,
			MediaStreams: item.MediaStreams,
		}, at))
	}
	if len(snapshot.Sources) == 0 {
		snapshot.Observation.Coverage["sources"] = inventory.CoverageEmpty
	} else {
		snapshot.Observation.Coverage["sources"] = inventory.CoveragePresent
	}
	clean, err := inventory.ValidateSnapshot(snapshot)
	if err != nil {
		return inventory.Snapshot{}, false, fmt.Errorf("validate imported inventory item: %w", err)
	}
	return clean, true, nil
}

func itemFacts(item inventoryWireItem) inventory.ItemFacts {
	facts := inventory.ItemFacts{
		Name: item.Name, OriginalTitle: item.OriginalTitle, SortTitle: item.SortName,
		Overview: item.Overview, Genres: item.Genres, Tags: item.Tags,
		ProductionYear: item.ProductionYear, SeasonNumber: item.ParentIndexNumber,
		EpisodeNumber: item.IndexNumber, EpisodeEnd: item.IndexNumberEnd, PremiereDate: item.PremiereDate,
		OfficialRating: item.OfficialRating, CommunityScore: item.CommunityRating,
		RuntimeMillis: item.RunTimeTicks / 10_000,
	}
	for _, studio := range item.Studios {
		facts.Studios = append(facts.Studios, studio.Name)
	}
	for _, person := range item.People {
		role := person.Role
		if role == "" {
			role = person.Type
		}
		facts.People = append(facts.People, inventory.Person{Name: person.Name, Role: role})
	}
	for kind, tag := range item.ImageTags {
		facts.Artwork = append(facts.Artwork, inventory.ArtworkRef{Kind: strings.ToLower(kind), Ref: tag})
	}
	sort.Slice(facts.Artwork, func(i, j int) bool { return facts.Artwork[i].Kind < facts.Artwork[j].Kind })
	for kind, id := range map[string]string{
		"parent": item.ParentID, "series": item.SeriesID, "season": item.SeasonID,
	} {
		if id != "" {
			facts.Relationships = append(facts.Relationships, inventory.Relationship{Kind: kind, ExternalID: id})
		}
	}
	sort.Slice(facts.Relationships, func(i, j int) bool { return facts.Relationships[i].Kind < facts.Relationships[j].Kind })
	return facts
}

func itemCoverage(fields map[string]json.RawMessage) map[string]inventory.Coverage {
	coverage := make(map[string]inventory.Coverage)
	for key, name := range map[string]string{
		"Name": "name", "OriginalTitle": "originalTitle", "SortName": "sortTitle",
		"Overview": "overview", "Genres": "genres", "Tags": "tags", "Studios": "studios",
		"People": "people", "ProductionYear": "productionYear", "PremiereDate": "premiereDate",
		"ParentIndexNumber": "seasonNumber", "IndexNumber": "episodeNumber",
		"IndexNumberEnd": "episodeEnd",
		"OfficialRating": "officialRating", "CommunityRating": "communityScore",
		"RunTimeTicks": "runtime", "ImageTags": "artwork", "ProviderIds": "externalIds",
	} {
		if raw, ok := fields[key]; ok {
			coverage[name] = rawCoverage(raw)
		}
	}
	return coverage
}

func rawCoverage(raw json.RawMessage) inventory.Coverage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == `""` || trimmed == "[]" || trimmed == "{}" {
		return inventory.CoverageEmpty
	}
	return inventory.CoveragePresent
}

func externalIDs(ids map[string]string) []inventory.ExternalID {
	out := make([]inventory.ExternalID, 0, len(ids))
	for namespace, value := range ids {
		if strings.TrimSpace(value) != "" {
			out = append(out, inventory.ExternalID{Namespace: strings.ToLower(namespace), Value: value})
		}
	}
	return out
}

func sourceSnapshot(
	origin inventory.OriginKey,
	item inventoryWireItem,
	source inventoryWireSource,
	at time.Time,
) inventory.SourceSnapshot {
	streams, reliable := inventoryStreams(source.MediaStreams)
	coverage := make(map[string]inventory.Coverage)
	if reliable {
		coverage["audioStreams"] = inventory.CoverageEmpty
		if len(streams) == 0 {
			coverage["streams"] = inventory.CoverageEmpty
		} else {
			coverage["streams"] = inventory.CoveragePresent
		}
		for _, stream := range streams {
			if stream.Kind == inventory.StreamAudio {
				coverage["audioStreams"] = inventory.CoveragePresent
				break
			}
		}
	}
	facts := inventory.SourceFacts{
		Protocol: source.Protocol, Container: source.Container, SizeBytes: source.Size,
		Bitrate: source.Bitrate, DurationMillis: source.RunTimeTicks / 10_000, Streams: streams,
	}
	extension, _ := json.Marshal(source)
	return inventory.SourceSnapshot{
		ExternalSourceID: source.ID,
		Kind:             inventory.SourceLibraryOriginal,
		Revision:         inventorySourceRevision(item, source),
		Locator: inventory.Locator{
			Authority: origin.Authority, ExternalItemID: origin.ExternalItemID, ExternalSourceID: source.ID,
		},
		Observation: inventory.Observation[inventory.SourceFacts]{
			SchemaVersion: 1, ObservedAt: at, Coverage: coverage, Facts: facts, Extension: extension,
		},
	}
}

func inventoryStreams(streams []inventoryWireStream) ([]inventory.Stream, bool) {
	seen := make(map[int]bool, len(streams))
	out := make([]inventory.Stream, 0, len(streams))
	for _, stream := range streams {
		kind := inventory.StreamKind(strings.ToLower(strings.TrimSpace(stream.Type)))
		if stream.Index == nil || *stream.Index < 0 || seen[*stream.Index] ||
			(kind != inventory.StreamVideo && kind != inventory.StreamAudio && kind != inventory.StreamSubtitle) {
			return nil, false
		}
		seen[*stream.Index] = true
		out = append(out, inventory.Stream{
			Index: *stream.Index, Kind: kind, Codec: stream.Codec, Profile: stream.Profile,
			Level: jsonScalarString(stream.Level), Language: stream.Language, Title: stream.Title,
			Disposition: inventory.Disposition{Default: stream.IsDefault, Forced: stream.IsForced},
			Channels:    stream.Channels, ChannelLayout: stream.ChannelLayout, SampleRate: stream.SampleRate,
			Width: stream.Width, Height: stream.Height, FrameRate: frameRateString(stream.RealFrameRate),
			PixelFormat: stream.PixelFormat, ColorSpace: stream.ColorSpace,
			ColorTransfer: stream.ColorTransfer, ColorPrimaries: stream.ColorPrimaries,
			HDR:        stream.VideoRangeType != "" && !strings.EqualFold(stream.VideoRangeType, "SDR"),
			Interlaced: stream.IsInterlaced, SubtitleFormat: stream.SubtitleCodec,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, true
}

func inventorySourceRevision(item inventoryWireItem, source inventoryWireSource) string {
	if etag := strings.TrimSpace(source.ETag); etag != "" {
		return "etag:" + etag
	}
	payload := strings.Join([]string{
		item.DateLastSaved, source.DateLastSaved, source.ID, source.Container,
		strconv.FormatInt(source.Size, 10), strconv.FormatInt(source.RunTimeTicks, 10),
	}, "\x00")
	digest := sha256.Sum256([]byte(payload))
	return "observed:" + hex.EncodeToString(digest[:16])
}

func jsonScalarString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return string(number)
	}
	return ""
}

func frameRateString(rate float64) string {
	if rate <= 0 {
		return ""
	}
	return strconv.FormatFloat(rate, 'f', -1, 64)
}
