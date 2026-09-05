package playout

import "testing"

// A realistic single ffprobe payload feeding ALL three consumers: h264 video + two audio tracks
// (different languages, one 5.1) + two subtitle tracks + container format. One fixture, one decode,
// exercised by tracksOf / audioTracksOf / formatOf — the whole point of consolidating the probe.
const probeFixture = `{
  "streams": [
    {"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"avg_frame_rate":"24000/1001","pix_fmt":"yuv420p"},
    {"codec_type":"audio","codec_name":"eac3","channels":6,"sample_rate":"48000","tags":{"language":"ENG","title":"Surround"}},
    {"codec_type":"audio","codec_name":"aac","channels":2,"tags":{"language":"rus"}},
    {"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng","title":"Forced"}},
    {"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"fra"}}
  ],
  "format": {"format_name":"matroska,webm","duration":"1327.5","bit_rate":"5000000"}
}`

func probeFrom(t *testing.T, raw string) probed {
	t.Helper()
	p, err := parseProbeJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parseProbeJSON: %v", err)
	}
	return p
}

func TestTracksOf_BucketsAndIndexesPerType(t *testing.T) {
	tracks := tracksOf(probeFrom(t, probeFixture).Streams)
	if len(tracks.Audio) != 2 || len(tracks.Subtitles) != 2 {
		t.Fatalf("got %d audio / %d subs, want 2/2", len(tracks.Audio), len(tracks.Subtitles))
	}
	// Per-TYPE indexing (audio 0,1 / subs 0,1), not absolute stream indices — a later -map 0:a:N
	// counts within its own type.
	if tracks.Audio[0].Index != 0 || tracks.Audio[1].Index != 1 {
		t.Fatalf("audio indices %d,%d; want 0,1", tracks.Audio[0].Index, tracks.Audio[1].Index)
	}
	if tracks.Subtitles[0].Index != 0 || tracks.Subtitles[1].Index != 1 {
		t.Fatalf("sub indices %d,%d; want 0,1", tracks.Subtitles[0].Index, tracks.Subtitles[1].Index)
	}
	// Language lowercased+trimmed (ENG→eng), title preserved, missing language empty.
	if tracks.Audio[0].Language != "eng" || tracks.Audio[0].Title != "Surround" {
		t.Fatalf("audio[0] = %+v", tracks.Audio[0])
	}
	if tracks.Audio[1].Language != "rus" || tracks.Audio[1].Title != "" {
		t.Fatalf("audio[1] = %+v", tracks.Audio[1])
	}
}

func TestAudioTracksOf_InOrder(t *testing.T) {
	// audioTracksOf feeds PickAudioTrack — the audio streams in ffmpeg's order, languages verbatim.
	got := audioTracksOf(probeFrom(t, probeFixture).Streams)
	if len(got) != 2 || got[0].Language != "ENG" || got[1].Language != "rus" {
		t.Fatalf("got %+v, want [ENG rus] in order", got)
	}
}

func TestSourceObservationOf_PreservesSharedProbeSuperset(t *testing.T) {
	observed := sourceObservationOf(probeFrom(t, `{
		"streams":[
			{"index":0,"codec_type":"video","codec_name":"hevc","profile":"Main 10","level":153,
			 "width":3840,"height":2160,"avg_frame_rate":"24000/1001","pix_fmt":"yuv420p10le",
			 "color_space":"bt2020nc","color_transfer":"smpte2084","color_primaries":"bt2020","field_order":"tt"},
			{"index":2,"codec_type":"audio","codec_name":"eac3","channels":6,"channel_layout":"5.1",
			 "sample_rate":"48000","disposition":{"default":1},"tags":{"language":"eng","title":"Surround"}}
		],
		"packets":[{"stream_index":0,"pts_time":"-0.5","flags":"KD_"}],
		"format":{"format_name":"matroska,webm","duration":"90.25","bit_rate":"4000000"}
	}`))
	if observed.Container != "matroska,webm" || observed.DurationMillis != 90_250 || observed.Bitrate != 4_000_000 ||
		!observed.UnsafePreroll || len(observed.Streams) != 2 {
		t.Fatalf("source observation = %+v", observed)
	}
	video, audio := observed.Streams[0], observed.Streams[1]
	if video.Profile != "Main 10" || video.Level != "153" || !video.HDR || !video.Interlaced ||
		video.ColorSpace != "bt2020nc" || video.ColorPrimaries != "bt2020" {
		t.Fatalf("video observation lost technical facts: %+v", video)
	}
	if audio.Language != "eng" || audio.Title != "Surround" || !audio.Default || audio.Channels != 6 ||
		audio.ChannelLayout != "5.1" || audio.SampleRate != 48_000 {
		t.Fatalf("audio observation lost technical facts: %+v", audio)
	}
}

func TestFormatOf_FullFormat(t *testing.T) {
	f := formatOf(probeFrom(t, probeFixture))
	if f.VideoCodec != "h264" || f.Width != 1920 || f.Height != 1080 {
		t.Fatalf("video: %q %dx%d", f.VideoCodec, f.Width, f.Height)
	}
	// 24000/1001 ≈ 23.976 fps.
	if f.FrameRate < 23.9 || f.FrameRate > 24.0 {
		t.Fatalf("framerate %.3f, want ~23.976", f.FrameRate)
	}
	// FIRST audio stream is the primary (eac3 5.1) — track selection is a separate concern.
	if f.AudioCodec != "eac3" || f.AudioChannels != 6 {
		t.Fatalf("audio: %q %dch, want eac3 6", f.AudioCodec, f.AudioChannels)
	}
	if f.AudioSampleRate != 48000 {
		t.Fatalf("audio sample rate = %d, want 48000", f.AudioSampleRate)
	}
	if f.Container != "matroska,webm" || f.Duration < 1327 || f.Bitrate != 5000000 {
		t.Fatalf("container=%q dur=%.0f br=%d", f.Container, f.Duration, f.Bitrate)
	}
	if f.TenBit() || f.HDR() {
		t.Fatal("yuv420p SDR must not report 10-bit or HDR")
	}
}

func TestFormatOf_HDRAndTenBit(t *testing.T) {
	f := formatOf(probeFrom(t, `{"streams":[
		{"codec_type":"video","codec_name":"hevc","width":3840,"height":2160,"pix_fmt":"yuv420p10le","color_transfer":"smpte2084"}
	]}`))
	if !f.TenBit() {
		t.Fatal("yuv420p10le must report 10-bit")
	}
	if !f.HDR() {
		t.Fatal("smpte2084 transfer must report HDR")
	}
}

func TestFormatOf_DetectsDiscardPrerollOnTheFirstVideoPacket(t *testing.T) {
	f := formatOf(probeFrom(t, `{
		"streams":[{"index":0,"codec_type":"video","codec_name":"h264"},{"index":1,"codec_type":"audio","codec_name":"aac"}],
		"packets":[
			{"stream_index":1,"pts_time":"-0.021333","flags":"KD_"},
			{"stream_index":0,"pts_time":"-1.000000","flags":"KD_"}
		]
	}`))
	if !f.VideoPreroll {
		t.Fatal("negative discarded video packet must mark the source unsafe to remux by copy")
	}

	ordinary := formatOf(probeFrom(t, `{
		"streams":[{"index":0,"codec_type":"video","codec_name":"h264"}],
		"packets":[{"stream_index":0,"pts_time":"-0.080000","flags":"K__"}]
	}`))
	if ordinary.VideoPreroll {
		t.Fatal("ordinary negative encoder timestamps without a discard edit are not preroll")
	}
}

func TestFormatOf_VideoOnlyAndMalformed(t *testing.T) {
	f := formatOf(probeFrom(t, `{"streams":[{"codec_type":"video","codec_name":"h264"}]}`))
	if f.AudioCodec != "" {
		t.Fatalf("video-only source has no audio, got %q", f.AudioCodec)
	}
	if _, err := parseProbeJSON([]byte("not json")); err == nil {
		t.Fatal("malformed probe output must error (caller degrades)")
	}
}
