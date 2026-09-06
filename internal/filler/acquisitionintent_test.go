package filler_test

import (
	"testing"

	"github.com/loomarr/loomarr/internal/filler"
)

func TestRemoteIdentityKey_NormalizesOnlyProvider(t *testing.T) {
	upper := filler.RemoteIdentity{Provider: " YouTube ", SourceID: "ChannelA", RemoteID: "AbCd123"}
	lowerProvider := filler.RemoteIdentity{Provider: "youtube", SourceID: "ChannelA", RemoteID: "AbCd123"}
	differentItemCase := filler.RemoteIdentity{Provider: "youtube", SourceID: "ChannelA", RemoteID: "abcd123"}
	if upper.Key() != lowerProvider.Key() {
		t.Fatalf("provider normalization keys = %q, %q; want equal", upper.Key(), lowerProvider.Key())
	}
	if upper.Key() == differentItemCase.Key() {
		t.Fatalf("case-sensitive remote ids share key %q", upper.Key())
	}
}
