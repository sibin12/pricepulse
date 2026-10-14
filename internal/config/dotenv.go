// Package config loads settings from a .env file into the environment, so
// secrets like GOOGLE_CLIENT_SECRET don't have to be typed on every start.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// Result describes what Load did, for logging.
type Result struct {
	Loaded  int  // variables set from the file
	Skipped int  // variables ignored because the real environment already sets them
	Exposed bool // the file is readable by other users on this machine
}

// Load reads KEY=VALUE lines from path and sets each variable that is not
// already set in the process environment. Real environment variables win, so
// a one-off override like `PORT=9000 go run ./cmd/api` still works.
//
// A missing file is not an error (returns an error matching fs.ErrNotExist,
// which callers can ignore). A malformed line is an error that names the line,
// because silently skipping it could mean running with the wrong settings.
func Load(path string) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()

	var res Result
	if info, err := f.Stat(); err == nil {
		// Any permission bit for group/others means someone else can read the
		// secrets in this file. (Meaningless on Windows, where it's never set.)
		res.Exposed = info.Mode().Perm()&0o077 != 0
	}

	vars, err := parse(f)
	if err != nil {
		return res, fmt.Errorf("%s: %w", path, err)
	}
	for _, kv := range vars {
		if _, set := os.LookupEnv(kv.key); set {
			res.Skipped++
			continue
		}
		if err := os.Setenv(kv.key, kv.value); err != nil {
			return res, fmt.Errorf("%s: set %s: %w", path, kv.key, err)
		}
		res.Loaded++
	}
	return res, nil
}

// IsNotExist reports whether Load failed only because the file is missing.
func IsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }

type keyValue struct{ key, value string }

// parse understands the common .env dialect:
//
//	# comment lines and blank lines are ignored
//	KEY=value                  unquoted; a " #" starts a trailing comment
//	export KEY=value           "export " prefix is allowed (shell-compatible)
//	KEY="a value # not a comment\n"   double quotes: \n \" \\ escapes
//	KEY='literal $value \n'    single quotes: taken exactly as written
//	KEY=                       empty value (treated as unset by the app)
func parse(r io.Reader) ([]keyValue, error) {
	var out []keyValue
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if n == 1 {
			line = strings.TrimPrefix(line, "\ufeff") // editors on Windows may add a BOM
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, raw, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !validKey(key) {
			return nil, fmt.Errorf("line %d: expected KEY=value", n)
		}
		value, err := parseValue(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %w", n, key, err)
		}
		out = append(out, keyValue{key, value})
	}
	return out, sc.Err()
}

func parseValue(raw string) (string, error) {
	switch {
	case strings.HasPrefix(raw, `"`):
		var b strings.Builder
		for i := 1; i < len(raw); i++ {
			c := raw[i]
			if c == '\\' && i+1 < len(raw) {
				i++
				switch raw[i] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default: // \" \\ and anything else: the character itself
					b.WriteByte(raw[i])
				}
				continue
			}
			if c == '"' {
				return b.String(), checkTrailing(raw[i+1:])
			}
			b.WriteByte(c)
		}
		return "", errors.New("missing closing double quote")

	case strings.HasPrefix(raw, "'"):
		end := strings.IndexByte(raw[1:], '\'')
		if end < 0 {
			return "", errors.New("missing closing single quote")
		}
		return raw[1 : end+1], checkTrailing(raw[end+2:])

	default:
		// Unquoted: " #" begins a comment, so "a#b" keeps its "#" but
		// "value # note" becomes "value".
		if i := strings.Index(raw, " #"); i >= 0 {
			raw = raw[:i]
		}
		return strings.TrimSpace(raw), nil
	}
}

// checkTrailing allows only whitespace or a comment after a closing quote.
func checkTrailing(rest string) error {
	rest = strings.TrimSpace(rest)
	if rest == "" || strings.HasPrefix(rest, "#") {
		return nil
	}
	return fmt.Errorf("unexpected text after closing quote: %q", rest)
}

// validKey accepts shell-style names: letters, digits, underscore; no leading digit.
func validKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
