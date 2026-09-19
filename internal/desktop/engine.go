package desktop

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// AppEngine is the engine bundled with the ChatGPT desktop app. It is preferred
// over a `codex` on PATH because the databases must be written by the same
// engine version the app runs.
const AppEngine = "/Applications/ChatGPT.app/Contents/Resources/codex"

// EngineEnv names the environment variable that overrides the engine binary.
const EngineEnv = "CODEX_BIN"

var providerSection = regexp.MustCompile(`^\s*\[model_providers\.("?)([^"\]]+)("?)\]`)

// Providers lists the model providers defined in config.toml plus "openai", sorted.
// thread/list filters by provider, so listing with every provider is what makes
// the engine index threads created under a custom one.
func Providers(baseDir string) []string {
	found := map[string]bool{"openai": true}
	if f, err := os.Open(filepath.Join(baseDir, "config.toml")); err == nil {
		defer f.Close()
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

// Backup copies the engine database, the desktop catalog and the session index
// into a new timestamped directory under root and returns its path. Files that do
// not exist are skipped.
func Backup(baseDir, root string) (string, error) {
	dest := filepath.Join(root, "db-backup-"+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(filepath.Join(dest, "sqlite"), 0o700); err != nil {
		return "", err
	}
	var files []string
	for _, pattern := range []string{StateDB + "*", "sqlite/codex-dev.db*"} {
		matches, err := filepath.Glob(filepath.Join(baseDir, filepath.FromSlash(pattern)))
		if err != nil {
			return "", err
		}
		files = append(files, matches...)
	}
	files = append(files, filepath.Join(baseDir, "session_index.jsonl"))
	for _, src := range files {
		rel, err := filepath.Rel(baseDir, src)
		if err != nil {
			return "", err
		}
		if err := copyFile(src, filepath.Join(dest, rel)); err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return dest, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
