package schema

import "strings"

// Probe-id formats are part of the payload contract: harvesters mint them,
// runners parse them, and a receipt's reader can interpret them. Keeping the
// codec here — rather than duplicated across packages — makes drift between
// minting and parsing structurally impossible.

const (
	// GoTestProbePrefix identifies a single-test probe:
	//   go-test:<file>:<TestName>
	GoTestProbePrefix = "go-test:"
	// GoTestPairProbePrefix identifies a compound accept/reject pair probe:
	//   go-test-pair:<pkgdir>:<AcceptTestName>|<RejectTestName>
	// Both tests live in the same package and run in one invocation; a pass
	// means the positive case held AND the negative case was rejected — the
	// T2-adversarial shape.
	GoTestPairProbePrefix = "go-test-pair:"
	// DotnetTestProbePrefix identifies a single .NET test-method probe:
	//   dotnet-test:<csproj-path>:<Class.Method>
	// Run as `dotnet test <csproj> --filter FullyQualifiedName~<Class.Method>`.
	DotnetTestProbePrefix = "dotnet-test:"
	// AlloyCheckProbePrefix identifies one Alloy command (a `check` of a safety
	// assert, or a `run` witness) in a model file:
	//   alloy-check:<file.als>:<CommandName>
	// The runner executes the whole file's command set once (the Alloy CLI's
	// JSON receipt reports every command) and confers T3 on a pass: bounded
	// model checking is stronger than an assertion but short of proof.
	AlloyCheckProbePrefix = "alloy-check:"
)

// GoTestProbeID mints the probe id for a single Go test.
func GoTestProbeID(file, name string) string {
	return GoTestProbePrefix + file + ":" + name
}

// ParseGoTestProbeID splits "go-test:<file>:<TestName>" into its parts. The
// test name is the final colon-separated field; the file is everything between.
func ParseGoTestProbeID(probeID string) (file, name string, ok bool) {
	if !strings.HasPrefix(probeID, GoTestProbePrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(probeID, GoTestProbePrefix)
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return "", "", false
	}
	file, name = rest[:i], rest[i+1:]
	if file == "" || name == "" {
		return "", "", false
	}
	return file, name, true
}

// DotnetTestProbeID mints the probe id for a single .NET test method.
func DotnetTestProbeID(csproj, classDotMethod string) string {
	return DotnetTestProbePrefix + csproj + ":" + classDotMethod
}

// ParseDotnetTestProbeID splits "dotnet-test:<csproj>:<Class.Method>". The
// class-dot-method is the final colon-separated field; the csproj path is
// everything between.
func ParseDotnetTestProbeID(probeID string) (csproj, classDotMethod string, ok bool) {
	if !strings.HasPrefix(probeID, DotnetTestProbePrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(probeID, DotnetTestProbePrefix)
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return "", "", false
	}
	csproj, classDotMethod = rest[:i], rest[i+1:]
	if csproj == "" || classDotMethod == "" {
		return "", "", false
	}
	return csproj, classDotMethod, true
}

// AlloyCheckProbeID mints the probe id for one Alloy command in a model file.
func AlloyCheckProbeID(file, command string) string {
	return AlloyCheckProbePrefix + file + ":" + command
}

// ParseAlloyCheckProbeID splits "alloy-check:<file.als>:<CommandName>".
func ParseAlloyCheckProbeID(probeID string) (file, command string, ok bool) {
	if !strings.HasPrefix(probeID, AlloyCheckProbePrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(probeID, AlloyCheckProbePrefix)
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return "", "", false
	}
	file, command = rest[:i], rest[i+1:]
	if file == "" || command == "" {
		return "", "", false
	}
	return file, command, true
}

// GoTestPairProbeID mints the probe id for an accept/reject pair in pkgDir.
func GoTestPairProbeID(pkgDir, acceptName, rejectName string) string {
	return GoTestPairProbePrefix + pkgDir + ":" + acceptName + "|" + rejectName
}

// ParseGoTestPairProbeID splits "go-test-pair:<pkgdir>:<Accept>|<Reject>".
func ParseGoTestPairProbeID(probeID string) (pkgDir, acceptName, rejectName string, ok bool) {
	if !strings.HasPrefix(probeID, GoTestPairProbePrefix) {
		return "", "", "", false
	}
	rest := strings.TrimPrefix(probeID, GoTestPairProbePrefix)
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return "", "", "", false
	}
	pkgDir = rest[:i]
	names := strings.SplitN(rest[i+1:], "|", 2)
	if pkgDir == "" || len(names) != 2 || names[0] == "" || names[1] == "" {
		return "", "", "", false
	}
	return pkgDir, names[0], names[1], true
}
