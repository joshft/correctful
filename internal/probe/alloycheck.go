package probe

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/joshft/correctful/schema"
)

// AlloyCheckRunner executes Alloy model commands as probes. A passing `check`
// (no counterexample within the declared scope) confers T3: bounded model
// checking corroborates a property across every trace the scope admits —
// stronger than a single assertion, short of proof. A `run` witness passes with
// INVERTED semantics: a solution must EXIST, or the model is vacuous.
//
// One model file's entire command set executes in a single Alloy CLI invocation
// (`exec --command '*' --type json`), whose JSON receipt reports every command.
// The result is cached per file and shared by all of that file's probes:
// measured on a real 16-check model, one invocation takes ~2 minutes and
// seventeen would take ~35. A counterexample refutes its claim CONCRETELY —
// the receipt names the trace.
//
// In the Alloy receipt a command's "solution" key is ABSENT when no instance
// was found (the pass case for a check) — absence must read as zero, not as
// missing data.
type AlloyCheckRunner struct{}

func (AlloyCheckRunner) CanRun(probeID string) bool {
	return strings.HasPrefix(probeID, schema.AlloyCheckProbePrefix)
}

func (AlloyCheckRunner) MaxTier() schema.Tier { return schema.T3Property }

// alloyJarPath resolves the Alloy dist jar once: $ALLOY_JAR, then the
// user-local cache. Never downloaded by the runner — a probe must not reach
// for the network mid-receipt.
var alloyJarPath = sync.OnceValue(func() string {
	if p := os.Getenv("ALLOY_JAR"); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".cache", "correctful", "alloy.jar")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
})

var javaPath = sync.OnceValue(func() string {
	p, err := exec.LookPath("java")
	if err != nil {
		return ""
	}
	return p
})

type alloyCmd struct {
	Type      string
	Solutions int
}

type alloyFileRun struct {
	once sync.Once
	cmds map[string]alloyCmd
	err  string // non-empty: the file-level execution failed
}

var alloyRuns sync.Map // abs model path -> *alloyFileRun

func (AlloyCheckRunner) Run(ctx context.Context, repoDir string, claim schema.Claim, probeID string) schema.Evidence {
	ev := schema.Evidence{ClaimID: claim.ID, ProbeID: probeID, Tier: schema.T3Property,
		Mechanism: schema.MechanismAlloyCheck}

	file, command, ok := schema.ParseAlloyCheckProbeID(probeID)
	if !ok {
		ev.Detail = "malformed alloy-check probe id"
		return ev
	}
	jar, java := alloyJarPath(), javaPath()
	if jar == "" {
		ev.Detail = "Alloy jar not found (set ALLOY_JAR or place ~/.cache/correctful/alloy.jar)"
		return ev
	}
	if java == "" {
		ev.Detail = "java not found on PATH"
		return ev
	}

	abs := filepath.Join(repoDir, file)
	frAny, _ := alloyRuns.LoadOrStore(abs, &alloyFileRun{})
	fr := frAny.(*alloyFileRun)

	start := time.Now()
	fr.once.Do(func() { fr.cmds, fr.err = execAlloy(ctx, java, jar, repoDir, abs) })
	ev.Duration = time.Since(start).Round(time.Millisecond).String()

	if fr.err != "" {
		ev.Detail = fr.err
		return ev
	}
	c, found := fr.cmds[command]
	if !found {
		ev.Detail = "command absent from Alloy receipt"
		return ev
	}
	ev.Ran, ev.Passed, ev.Detail = alloyVerdict(c)
	return ev
}

// alloyVerdict maps one command's receipt entry to evidence semantics.
func alloyVerdict(c alloyCmd) (ran, passed bool, detail string) {
	switch c.Type {
	case "check":
		if c.Solutions == 0 {
			return true, true, "no counterexample within declared scope"
		}
		return true, false, "COUNTEREXAMPLE found within scope — the assert does not hold"
	case "run":
		if c.Solutions > 0 {
			return true, true, "witness instance exists (model is not vacuous here)"
		}
		return true, false, "no witness instance — the model does not admit this scenario"
	default:
		return false, false, "unknown Alloy command type " + c.Type
	}
}

// execAlloy runs the whole model's command set once and parses the JSON receipt.
func execAlloy(ctx context.Context, java, jar, repoDir, absModel string) (map[string]alloyCmd, string) {
	outDir, err := os.MkdirTemp("", "correctful-alloy-*")
	if err != nil {
		return nil, "temp dir: " + err.Error()
	}
	defer os.RemoveAll(outDir)

	cmd := exec.CommandContext(ctx, java, "-jar", jar,
		"exec", "--command", "*", "--type", "json",
		"--output", filepath.Join(outDir, "base"), "--force", absModel)
	cmd.Dir = repoDir
	out, runErr := cmd.CombinedOutput()

	raw, readErr := os.ReadFile(filepath.Join(outDir, "base", "receipt.json"))
	if readErr != nil {
		// No receipt at all: the execution never completed (bad model, OOM,
		// context timeout). Nothing ran; nothing is refuted.
		return nil, "alloy execution produced no receipt: " + firstMeaningfulLine(string(out))
	}
	var receipt struct {
		Commands map[string]struct {
			Type     string            `json:"type"`
			Solution []json.RawMessage `json:"solution"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, "unparseable Alloy receipt: " + err.Error()
	}
	_ = runErr // verdicts come from the receipt, not the exit status
	cmds := make(map[string]alloyCmd, len(receipt.Commands))
	for name, c := range receipt.Commands {
		cmds[name] = alloyCmd{Type: c.Type, Solutions: len(c.Solution)}
	}
	return cmds, ""
}
