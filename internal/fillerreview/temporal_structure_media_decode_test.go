package fillerreview

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRunTemporalStructureMediaCommandRejectsSuccessfulProcessWithErrorOutput(t *testing.T) {
	if os.Getenv("LOOMARR_TEMPORAL_MEDIA_ERROR_HELPER") == "1" {
		_, _ = fmt.Fprintln(os.Stderr, "damaged input packet")
		os.Exit(0)
	}
	command := exec.Command(os.Args[0], "-test.run=TestRunTemporalStructureMediaCommandRejectsSuccessfulProcessWithErrorOutput")
	command.Env = append(os.Environ(), "LOOMARR_TEMPORAL_MEDIA_ERROR_HELPER=1")
	output := &boundedTemporalStructureMediaOutput{}
	command.Stdout, command.Stderr = output, output
	err := runTemporalStructureMediaCommand(context.Background(), command, output)
	if err == nil || !strings.Contains(err.Error(), "errors despite a successful exit") || !strings.Contains(err.Error(), "damaged input packet") {
		t.Fatalf("error = %v", err)
	}
}
