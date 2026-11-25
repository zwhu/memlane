package setupcli

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/controlplane"
)

const DefaultVersion = "0.1.0-beta.3"

func Dispatch(args []string, runtimeAssets, bootstrapAssets fs.FS, version string) (bool, int) {
	return DispatchIO(context.Background(), args, runtimeAssets, bootstrapAssets, version, os.Stdout, os.Stderr)
}

func DispatchIO(ctx context.Context, args []string, runtimeAssets, bootstrapAssets fs.FS, version string, stdout, stderr io.Writer) (bool, int) {
	return (Runner{}).DispatchIO(ctx, args, runtimeAssets, bootstrapAssets, version, stdout, stderr)
}

type commandSpec struct {
	switches []string
	values   []string
}

var specs = map[string]commandSpec{
	"doctor":           {[]string{"check-network", "include-logs"}, []string{"mirror", "agent", "operation-id"}},
	"install":          {[]string{"plan", "apply"}, []string{"plan-digest", "component", "mirror", "wait", "operation-id"}},
	"connect":          {[]string{"dry-run", "apply"}, []string{"agent", "phase", "instance-id", "profile", "reviewed-sha"}},
	"repair":           {[]string{"dry-run", "apply"}, []string{"owner-fingerprint", "operation-id", "instance-id", "reviewed-sha"}},
	"verify":           {[]string{"expect-zero-downloads", "expect-runtime-current"}, []string{"stage", "agent"}},
	"stop":             {[]string{"dry-run", "apply"}, []string{"owner-fingerprint"}},
	"__install-worker": {[]string{"start-runtime"}, []string{"mirror", "operation-id", "plan-digest", "component"}},
	"version":          {},
	"help":             {},
}

type manifest struct {
	SchemaVersion int                 `json:"schema_version"`
	Commands      map[string][]string `json:"commands"`
	RepairTargets []string            `json:"repair_targets"`
	ExitCodes     map[int]string      `json:"exit_codes"`
}

func commandManifest() *manifest {
	m := &manifest{1, map[string][]string{}, slices.Clone(repairTargets), statuses}
	for name := range specs {
		if name == "__install-worker" {
			continue
		}
		spec := flagsFor(name)
		m.Commands[name] = []string{}
		for _, flag := range append(spec.switches, spec.values...) {
			m.Commands[name] = append(m.Commands[name], "--"+flag)
		}
	}
	m.Commands["--cli"] = []string{}
	m.Commands["--help"] = m.Commands["help"]
	m.Commands["-h"] = m.Commands["help"]
	m.Commands["--version"] = m.Commands["version"]
	return m
}

var repairTargets = []string{"stale-daemon", "pending-ops", "trae-hooks", "stale-owner"}
var componentIDs = []string{"uv", "python", "runtime", "cognee-source", "embedding", "agent-plugin"}

func flagsFor(command string) commandSpec {
	spec := specs[command]
	spec.switches = append(slices.Clone(spec.switches), "json")
	if command != "help" && command != "version" {
		spec.values = append(slices.Clone(spec.values), "config", "component-root")
	}
	return spec
}

type options struct {
	values     map[string]string
	components []string
	target     string
	wait       int
}

func (o options) get(name string) string { return o.values[name] }
func (o options) has(name string) bool   { return o.values[name] != "" }

func parse(command string, args []string) (options, bool) {
	o := options{values: map[string]string{}, wait: 45}
	spec := flagsFor(command)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			if command != "repair" || o.target != "" || !slices.Contains(repairTargets, arg) {
				return o, false
			}
			o.target = arg
			continue
		}
		name, value, inline := strings.Cut(arg[2:], "=")
		switch {
		case slices.Contains(spec.switches, name):
			if inline && value != "true" {
				return o, false
			}
			o.values[name] = "true"
		case slices.Contains(spec.values, name):
			if !inline {
				i++
				if i == len(args) || strings.HasPrefix(args[i], "-") {
					return o, false
				}
				value = args[i]
			}
			if value == "" {
				return o, false
			}
			if name == "component" {
				if !slices.Contains(componentIDs, value) {
					return o, false
				}
				if !slices.Contains(o.components, value) {
					o.components = append(o.components, value)
				}
				continue
			}
			if old := o.get(name); old != "" && old != value {
				return o, false
			}
			o.values[name] = value
		default:
			return o, false
		}
	}
	return o, o.valid(command)
}

func (o *options) valid(command string) bool {
	for _, name := range []string{"config", "component-root", "profile"} {
		if o.has(name) && (!filepath.IsAbs(o.get(name)) || strings.ContainsRune(o.get(name), 0)) {
			return false
		}
	}
	if o.has("agent") && !isAgent(o.get("agent")) ||
		o.has("mirror") && !oneOf(o.get("mirror"), "china", "official") ||
		o.has("apply") && (o.has("plan") || o.has("dry-run")) {
		return false
	}
	if o.has("wait") {
		wait, err := strconv.Atoi(o.get("wait"))
		if err != nil || wait < 0 || wait > 55 {
			return false
		}
		o.wait = wait
	}
	if o.has("instance-id") && !instanceID.MatchString(o.get("instance-id")) ||
		o.has("operation-id") && !operationIdentifier.MatchString(o.get("operation-id")) {
		return false
	}
	// Digests supplied by users are compared to fresh values. A stale value
	// is a preflight conflict even if a former version used a different format.
	switch command {
	case "install":
		return !o.has("apply") || o.has("plan-digest")
	case "__install-worker":
		return o.has("config") && o.has("component-root") && o.has("mirror") && o.has("operation-id") && o.has("plan-digest")
	case "connect":
		phase := o.get("phase")
		return o.has("agent") && oneOf(phase, "", "prepare", "activate", "allow-capture", "defer-capture", "select-env", "review") &&
			(!o.has("apply") || oneOf(phase, "prepare", "review") || phase == "select-env" && o.has("profile") || phase != "select-env" && o.has("reviewed-sha"))
	case "repair":
		if o.target == "" || o.has("operation-id") && !controlplane.ValidOperationID(o.get("operation-id")) {
			return false
		}
		switch o.target {
		case "stale-daemon":
			return !o.has("apply") || o.has("owner-fingerprint")
		case "pending-ops":
			return !o.has("apply") || o.has("operation-id")
		case "trae-hooks":
			return !o.has("apply") || o.has("instance-id") && o.has("reviewed-sha")
		}
	case "verify":
		return oneOf(o.get("stage"), "", "components", "setup", "connection")
	case "stop":
		return !o.has("apply") || o.has("owner-fingerprint")
	}
	return true
}

func oneOf(s string, options ...string) bool { return slices.Contains(options, s) }
func isAgent(s string) bool                  { return slices.Contains(bootstrap.AllowedAgentKinds(), s) }

func commandMode(command string, o options) string {
	if o.has("apply") || command == "__install-worker" {
		return "apply"
	}
	switch command {
	case "install":
		return "plan"
	case "connect", "repair", "stop":
		return "dry_run"
	default:
		return "read_only"
	}
}

func (r Runner) DispatchIO(ctx context.Context, args []string, runtimeAssets, bootstrapAssets fs.FS, version string, stdout, _ io.Writer) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	explicit := args[0] == "--cli"
	if explicit {
		args = args[1:]
	}
	command := "cli"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
		switch command {
		case "--help", "-h":
			command = "help"
		case "--version":
			command = "version"
		}
	}
	if _, known := specs[command]; !known {
		if !explicit {
			return false, 0
		}
		// Include the unknown token in the secret scan, without echoing it.
		args = append([]string{command}, args...)
		command = "cli"
	}
	r.defaults()
	o, valid := parse(command, args)
	e := newEnvelope(command, commandMode(command, o))
	e.App = appSummary{Version: DefaultVersion, Platform: runtime.GOOS + "/" + runtime.GOARCH}
	forbidden := secretInput(args, r.Environ)
	if forbidden {
		e.block(ExitFatal, "secret_input_forbidden")
	} else if !valid || command == "cli" {
		e.block(ExitInvalidArguments, "invalid_arguments")
	} else {
		e.App = r.app(version, runtimeAssets)
		s := session{Runner: r, ctx: ctx, runtimeAssets: runtimeAssets, bootstrapAssets: bootstrapAssets, options: o, envelope: &e}
		s.run()
	}
	e.OK, e.Status = e.ExitCode == ExitOK, statuses[e.ExitCode]
	if command != "__install-worker" || o.has("json") || !valid || forbidden {
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(e); err != nil {
			return true, ExitFatal
		}
	}
	return true, e.ExitCode
}

func (r Runner) app(version string, assets fs.FS) appSummary {
	if version == "" {
		macos := filepath.Dir(r.Executable)
		contents := filepath.Dir(macos)
		bundle := filepath.Dir(contents)
		if filepath.Base(macos) == "MacOS" && filepath.Base(contents) == "Contents" && strings.HasSuffix(bundle, ".app") {
			if bundleVersion := bootstrap.ReadBundleVersion(bundle); versionPattern.MatchString(bundleVersion) {
				version = bundleVersion
			}
		}
		if version == "" {
			version = DefaultVersion
		}
	}
	hash, _ := bootstrap.BundledRuntimeHash(assets)
	return appSummary{SanitizeString(version, ""), SanitizeString(r.Executable, r.HomeDir), hash, runtime.GOOS + "/" + runtime.GOARCH}
}

func (s *session) run() {
	if oneOf(s.Command, "help", "version") {
		s.Capabilities = commandManifest()
		return
	}
	if !s.loadPaths() {
		return
	}
	switch s.Command {
	case "doctor":
		s.doctor()
	case "install":
		s.install()
	case "__install-worker":
		s.worker()
	case "connect":
		s.connect()
	case "repair":
		s.repair()
	case "verify":
		s.verify()
	case "stop":
		s.stop()
	}
}

func executable() string {
	path, _ := os.Executable()
	return path
}
