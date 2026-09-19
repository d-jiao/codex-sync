package desktop

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// AppEngine is the engine bundled with the ChatGPT desktop app. It is preferred
// over a `codex` on PATH because the databases must be written by the same
// engine version the app runs.
const AppEngine = "/Applications/ChatGPT.app/Contents/Resources/codex"

// EngineEnv names the environment variable that overrides the engine binary.
const EngineEnv = "CODEX_BIN"

// providerSection matches `[model_providers.<id>]` headers; sub-tables such as
// `[model_providers.<id>.http_headers]` are not providers.
var providerSection = regexp.MustCompile(`^\s*\[model_providers\.("?)([^"\].]+)("?)\]`)

// Providers lists the model providers defined in config.toml plus "openai", sorted.
// thread/list filters by provider, so listing with every provider is what makes
// the engine index threads created under a custom one.
func Providers(baseDir string) []string {
	found := map[string]bool{"openai": true}
	if f, err := os.Open(filepath.Join(baseDir, "config.toml")); err == nil {
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if m := providerSection.FindStringSubmatch(sc.Text()); m != nil {
				found[strings.TrimSpace(m[2])] = true
			}
		}
	}
	provs := make([]string, 0, len(found))
	for p := range found {
		provs = append(provs, p)
	}
	sort.Strings(provs)
	return provs
}

// ResolveEngine picks the engine binary: the explicit flag value, then $CODEX_BIN,
// then the ChatGPT app's bundled engine when installed, else `codex` on PATH.
func ResolveEngine(flag string) string {
	return resolveEngine(flag, os.Getenv(EngineEnv), AppEngine)
}

func resolveEngine(flag, env, app string) string {
	switch {
	case flag != "":
		return flag
	case env != "":
		return env
	}
	if _, err := os.Stat(app); err == nil {
		return app
	}
	return "codex"
}
