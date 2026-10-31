package dataset

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFormats(t *testing.T) {
	for name, body := range map[string]string{
		"a.yaml":  "cases:\n  - {id: one, input: hi, expected: yo}\n  - {id: two, input: hi, rubric: nice}\n",
		"a.jsonl": "{\"id\":\"one\",\"input\":\"hi\",\"expected\":\"yo\"}\n\n{\"id\":\"two\",\"input\":\"hi\",\"rubric\":\"nice\"}\n",
	} {
		cases, err := Load(write(t, name, body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(cases) != 2 || cases[1].Rubric != "nice" {
			t.Fatalf("%s: got %+v", name, cases)
		}
	}
}

func TestLoadReportsEveryProblem(t *testing.T) {
	_, err := Load(write(t, "a.yaml", `cases:
  - {id: one, input: hi}
  - {id: two, expected: yo}
  - {id: two, input: hi, expected: yo}
`))
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"needs expected or rubric", "missing input", "duplicate id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestLoadRejects(t *testing.T) {
	for name, body := range map[string]string{
		"unknown.yaml": "cases:\n  - {id: a, input: b, expected: c, typo: d}\n",
		"bad.jsonl":    "{\"id\":\"a\",\"input\":\"b\",\"expected\":\"c\",\"typo\":1}\n",
		"empty.yaml":   "",
		"data.csv":     "id,input\n",
	} {
		if _, err := Load(write(t, name, body)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
