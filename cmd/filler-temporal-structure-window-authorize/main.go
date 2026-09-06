// Command filler-temporal-structure-window-authorize issues a separately reviewed, narrow
// long-reel authority from one complete passing short-versus-long shadow lineage.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/loomarr/loomarr/internal/fillerreview"
	"github.com/loomarr/loomarr/internal/fillerstructurewindow"
)

type capabilities struct {
	publish func(fillerreview.TemporalStructureWindowAuthorityConfig) (fillerstructurewindow.MaterializationAuthority, string, error)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, capabilities{publish: fillerreview.PublishTemporalStructureWindowAuthority}))
}

func run(args []string, stdout, stderr io.Writer, capability capabilities) int {
	flags := flag.NewFlagSet("filler-temporal-structure-window-authorize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifest := flags.String("window-set", "", "public prepared-window manifest JSON")
	certificate := flags.String("window-certificate", "", "passing immutable private window certification JSON")
	complete := flags.String("complete-decisions", "", "immutable complete-video decision-set JSON")
	windows := flags.String("window-decisions", "", "immutable overlapping-window decision-set JSON")
	shadow := flags.String("short-long-shadow", "", "complete passing short-versus-long shadow JSON")
	reviewer := flags.String("reviewer", "", "bounded identity of the person reviewing this release")
	reviewedRaw := flags.String("reviewed-at", "", "fixed RFC3339 review time")
	allow := flags.Bool("allow-materialization", false, "explicitly allow creation of held child work inside the certified envelope")
	output := flags.String("out", "", "new immutable private long-reel materialization authority JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *manifest == "" || *certificate == "" || *complete == "" || *windows == "" || *shadow == "" ||
		*reviewer == "" || *reviewedRaw == "" || !*allow || *output == "" {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-authorize: manifest, certificate, both decision sets, passing shadow, reviewer, review time, explicit --allow-materialization, and output are required")
		return 2
	}
	reviewedAt, err := time.Parse(time.RFC3339Nano, *reviewedRaw)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-authorize: --reviewed-at must be RFC3339")
		return 2
	}
	authority, digest, err := capability.publish(fillerreview.TemporalStructureWindowAuthorityConfig{
		WindowSetManifestPath: *manifest, WindowCertificationPath: *certificate,
		CompleteDecisionSetPath: *complete, WindowDecisionSetPath: *windows, ShortLongShadowPath: *shadow,
		ReviewerID: *reviewer, ReviewedAt: reviewedAt, AutomaticMaterializationAllowed: *allow, OutputPath: *output,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "filler-temporal-structure-window-authorize:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "filler-temporal-structure-window-authorize: authorized held-child materialization for %d units, %d roles, %d assessors, %d..%d ms, at most %d windows and %d bytes/window; training=false admission=false; sha256 %s; %s\n",
		len(authority.AllowedUnits), len(authority.AllowedRoles), len(authority.Assessors),
		authority.MinimumSourceDurationMS, authority.MaximumSourceDurationMS, authority.MaximumWindows,
		authority.MaximumWindowBytes, digest, *output)
	return 0
}
