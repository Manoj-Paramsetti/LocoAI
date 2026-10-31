package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func suiteFile(t *testing.T, threshold, response string) string {
	t.Helper()
	dir := t.TempDir()
	body := "name: t\ndataset: cases.yaml\nprovider: {name: mock, default: " + response + "}\nmodel: m\n" +
		"graders:\n  - type: exact\ngate:\n  threshold: " + threshold + "\n"
	if err := os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := "cases:\n  - {id: a, input: hi, expected: yes}\n"
	cases = strings.ReplaceAll(cases, "yes", `"yes"`)
	if err := os.WriteFile(filepath.Join(dir, "cases.yaml"), []byte(cases), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "suite.yaml")
}

func TestGateExitPaths(t *testing.T) {
	out := t.TempDir()
	if err := dispatch([]string{"run", suiteFile(t, "1", `"yes"`), "--out", out}, io.Discard); err != nil {
		t.Fatalf("passing run: %v", err)
	}
	err := dispatch([]string{"run", suiteFile(t, "1", `"no"`), "--out", out}, io.Discard)
	if !errors.Is(err, errGate) {
		t.Fatalf("failing run: %v", err)
	}
}

func TestRegressionAgainstBaselineFailsRun(t *testing.T) {
	out := t.TempDir()
	if err := dispatch([]string{"run", suiteFile(t, "0", `"yes"`), "--out", out}, io.Discard); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(out, "t", "*.json"))
	baseline := filepath.Join(out, "baseline.json")
	if err := dispatch([]string{"promote", matches[0], baseline}, io.Discard); err != nil {
		t.Fatal(err)
	}
	err := dispatch([]string{"run", suiteFile(t, "0", `"no"`), "--out", out, "--baseline", baseline}, io.Discard)
	if !errors.Is(err, errGate) {
		t.Fatalf("regression passed gate: %v", err)
	}
}

func TestUnusableInputIsNotAGateFailure(t *testing.T) {
	for _, args := range [][]string{
		{"run", "missing.yaml"},
		{"run", suiteFile(t, "1", `"yes"`), "--baseline", "missing.json"},
		{"nonsense"},
	} {
		if err := dispatch(args, io.Discard); err == nil || errors.Is(err, errGate) {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}
