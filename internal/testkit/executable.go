package testkit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/loomarr/loomarr/internal/testkit/execfixture"
)

const (
	// ProcessTreeModeEnv selects the behavior of DescendantProcessExecutable.
	ProcessTreeModeEnv = "LOOMARR_TEST_PROCESS_TREE_MODE"
	// ProcessTreeParentPIDFileEnv receives the blocking parent's process ID.
	ProcessTreeParentPIDFileEnv = "LOOMARR_TEST_PROCESS_TREE_PARENT_PID_FILE"
	// ProcessTreeChildPIDFileEnv receives the blocking descendant's process ID.
	ProcessTreeChildPIDFileEnv = "LOOMARR_TEST_PROCESS_TREE_CHILD_PID_FILE"
)

// Executable writes a shared system-boundary test double for code that invokes a required local
// tool. Keep these doubles in testkit rather than growing one private shell mock per caller.
func Executable(t *testing.T, name, script string) string {
	t.Helper()
	return execfixture.Executable(t, name, script)
}

// CopyingMediaExecutable builds a portable local-tool double that copies the argument following
// -i to the final argument, appends suffix, and emits ffmpeg's terminal progress marker.
func CopyingMediaExecutable(t *testing.T, name, suffix string, requiredArguments ...string) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	quoted := make([]string, 0, len(requiredArguments))
	for _, required := range requiredArguments {
		quoted = append(quoted, strconv.Quote(required))
	}
	program := fmt.Sprintf(`package main
import (
	"fmt"
	"os"
	"strings"
)
func main() {
	args := os.Args[1:]
	joined := strings.Join(args, " ")
	for _, required := range []string{%s} {
		if !strings.Contains(joined, required) { os.Exit(5) }
	}
	input := ""
	for i := 1; i < len(args); i++ {
		if args[i-1] == "-i" { input = args[i] }
	}
	if input == "" || len(args) == 0 { os.Exit(2) }
	data, err := os.ReadFile(input)
	if err != nil { os.Exit(3) }
	data = append(data, []byte(%q)...)
	if err := os.WriteFile(args[len(args)-1], data, 0600); err != nil { os.Exit(4) }
	fmt.Print("progress=end\n")
	}
	`, strings.Join(quoted, ","), suffix)
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatalf("write portable executable source: %v", err)
	}
	executable := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", executable, source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build portable executable %s: %v: %s", name, err, output)
	}
	return executable
}

// DescendantProcessExecutable builds a portable local-tool double for process-tree clients. The
// caller selects "block", "success", or "fail-large" with ProcessTreeModeEnv. Blocking mode
// records parent and child process IDs in the files named by the corresponding environment values.
func DescendantProcessExecutable(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	program := fmt.Sprintf(`package main
import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)
const (
	modeEnv = %q
	parentPIDFileEnv = %q
	childPIDFileEnv = %q
)
func writePID(path string) {
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil { os.Exit(3) }
}
func main() {
	switch os.Getenv(modeEnv) {
	case "block":
		writePID(os.Getenv(parentPIDFileEnv))
		self, err := os.Executable()
		if err != nil { os.Exit(2) }
		cmd := exec.Command(self)
		cmd.Env = append(os.Environ(), modeEnv+"=child")
		if err := cmd.Start(); err != nil { os.Exit(2) }
		_ = cmd.Process.Release()
		time.Sleep(time.Hour)
	case "child":
		writePID(os.Getenv(childPIDFileEnv))
		time.Sleep(time.Hour)
	case "fail-large":
		_, _ = fmt.Fprint(os.Stderr, "HEAD-MARKER\n", strings.Repeat("x", 128<<10), "\nTAIL-MARKER\n")
		os.Exit(23)
	case "success":
		return
	default:
		os.Exit(4)
	}
}
`, ProcessTreeModeEnv, ProcessTreeParentPIDFileEnv, ProcessTreeChildPIDFileEnv)
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatalf("write portable executable source: %v", err)
	}
	executable := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", executable, source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build portable executable %s: %v: %s", name, err, output)
	}
	return executable
}
