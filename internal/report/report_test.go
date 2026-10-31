package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Manoj-Paramsetti/LocoAI/internal/model"
)

func mkRun(threshold float64, cases ...model.CaseResult) *model.Run {
	return &model.Run{SchemaVersion: model.SchemaVersion, ID: "r1", Suite: "s", Model: "m",
		Threshold: threshold, Summary: model.Summarize(cases), Cases: cases}
}

func cr(id string, passed bool) model.CaseResult {
	return model.CaseResult{ID: id, Hash: "h-" + id, Passed: passed}
}

func TestCompareOnlyCountsUnchangedCases(t *testing.T) {
	base := mkRun(0, cr("reg", true), cr("fix", false), cr("same", true), cr("gone", true),
		model.CaseResult{ID: "edited", Hash: "old", Passed: true})
	cur := mkRun(0, cr("reg", false), cr("fix", true), cr("same", true), cr("new", false),
		model.CaseResult{ID: "edited", Hash: "new", Passed: false})
	c := Compare(base, cur)
	if c.Comparable != 3 || c.Delta != 0 {
		t.Fatalf("%+v", c)
	}
	for name, ids := range map[string][]string{"reg": c.Regressions, "fix": c.Fixes, "new": c.Added, "gone": c.Removed, "edited": c.Changed} {
		if len(ids) != 1 || ids[0] != name {
			t.Errorf("%s: %v", name, ids)
		}
	}
}

func TestGate(t *testing.T) {
	good, bad := cr("a", true), cr("a", false)
	base := mkRun(0, good, cr("b", true))
	cur := mkRun(0.4, bad, cr("b", true))
	if f := Gate(cur, nil, 0); len(f) != 0 {
		t.Fatalf("threshold met: %v", f)
	}
	if f := Gate(cur, Compare(base, cur), 0); len(f) != 1 {
		t.Fatalf("regression not caught: %v", f)
	}
	if f := Gate(cur, Compare(base, cur), 0.5); len(f) != 0 {
		t.Fatalf("inside tolerance: %v", f)
	}
	if f := Gate(mkRun(0.9, bad), nil, 0); len(f) != 1 {
		t.Fatalf("threshold miss not caught: %v", f)
	}
	unrelated := mkRun(0, cr("z", true))
	c := Compare(unrelated, cur)
	if c.Comparable != 0 || len(c.Warnings) == 0 {
		t.Fatalf("no-overlap comparison should warn: %+v", c)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	run := mkRun(0.5, cr("a", true))
	path, err := Save(run, dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.Summary.Passed != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("want schema version error")
	}
}

func TestSiteEscapesModelOutput(t *testing.T) {
	c := cr("a", false)
	c.Output = `<script>alert(1)</script>`
	dir := t.TempDir()
	if err := WriteSite([]*model.Run{mkRun(0.5, c)}, dir); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(dir, "runs", "r1.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "<script>") {
		t.Fatal("model output not escaped")
	}
	if !strings.Contains(string(page), "&lt;script&gt;") {
		t.Fatal("output missing")
	}
	badge, err := os.ReadFile(filepath.Join(dir, "badge.json"))
	if err != nil || !strings.Contains(string(badge), `"message":"0%"`) {
		t.Fatalf("badge %s %v", badge, err)
	}
}
