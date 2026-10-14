package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	input := "\xef\xbb\xbf# comment\n" +
		"\n" +
		"PLAIN=hello\n" +
		"SPACED = padded value   \n" +
		"export EXPORTED=yes\n" +
		"INLINE=value # trailing comment\n" +
		"HASH_IN_VALUE=a#b\n" +
		"EMPTY=\n" +
		`DOUBLE="has # hash and \"quotes\"\nnewline" # comment` + "\n" +
		`SINGLE='literal \n $HOME'` + "\n" +
		"URL=postgres://u:p@host:5432/db?sslmode=disable\n" +
		"EQUALS=a=b=c\n"

	got, err := parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PLAIN":         "hello",
		"SPACED":        "padded value",
		"EXPORTED":      "yes",
		"INLINE":        "value",
		"HASH_IN_VALUE": "a#b",
		"EMPTY":         "",
		"DOUBLE":        "has # hash and \"quotes\"\nnewline",
		"SINGLE":        `literal \n $HOME`,
		"URL":           "postgres://u:p@host:5432/db?sslmode=disable",
		"EQUALS":        "a=b=c",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d vars, want %d: %+v", len(got), len(want), got)
	}
	for _, kv := range got {
		if want[kv.key] != kv.value {
			t.Errorf("%s = %q, want %q", kv.key, kv.value, want[kv.key])
		}
	}
}

func TestParseErrorsNameTheLine(t *testing.T) {
	cases := map[string]string{
		"OK=1\nno equals sign\n":     "line 2",
		"OK=1\n1BAD=x\n":             "line 2",
		"A=\"unterminated\n":         "missing closing double quote",
		"A='unterminated\n":          "missing closing single quote",
		"A=\"quoted\" trailing junk": "unexpected text",
	}
	for input, wantMsg := range cases {
		_, err := parse(strings.NewReader(input))
		if err == nil || !strings.Contains(err.Error(), wantMsg) {
			t.Errorf("parse(%q) error = %v, want it to mention %q", input, err, wantMsg)
		}
	}
}

func TestLoadRealEnvironmentWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("PP_TEST_FROM_FILE=file\nPP_TEST_OVERRIDE=file\n"), 0o600)
	t.Setenv("PP_TEST_OVERRIDE", "real")
	t.Setenv("PP_TEST_FROM_FILE", "") // registers cleanup...
	os.Unsetenv("PP_TEST_FROM_FILE")  // ...then make it truly unset

	res, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PP_TEST_FROM_FILE") != "file" || os.Getenv("PP_TEST_OVERRIDE") != "real" {
		t.Fatalf("got FROM_FILE=%q OVERRIDE=%q", os.Getenv("PP_TEST_FROM_FILE"), os.Getenv("PP_TEST_OVERRIDE"))
	}
	if res.Loaded != 1 || res.Skipped != 1 || res.Exposed {
		t.Fatalf("result = %+v", res)
	}
}

func TestLoadMissingAndExposed(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.env"))
	if !IsNotExist(err) {
		t.Fatalf("missing file: got %v", err)
	}

	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("PP_TEST_X=1\n"), 0o644)
	t.Setenv("PP_TEST_X", "")
	os.Unsetenv("PP_TEST_X")
	res, err := Load(path)
	if err != nil || !res.Exposed {
		t.Fatalf("world-readable file should be flagged: %+v %v", res, err)
	}
}
