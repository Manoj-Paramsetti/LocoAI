// Package report stores runs, compares them, and renders HTML.
package report

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Manoj-Paramsetti/LocoAI/internal/model"
)

var unsafeName = regexp.MustCompile(`[^a-z0-9._-]+`)

// Slug turns a suite name into something safe in file names and URLs.
func Slug(name string) string {
	s := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(name), "-"), "-.")
	if s == "" {
		return "suite"
	}
	return s
}

// Save writes the run to dir/<suite>/<id>.json and returns the path. The write
// goes through a rename so an interrupted run never leaves half a file.
func Save(run *model.Run, dir string) (string, error) {
	path := filepath.Join(dir, Slug(run.Suite), run.ID+".json")
	return path, WriteRun(run, path)
}

// PagePath is where WriteSite puts a run's page under dir.
func PagePath(run *model.Run, dir string) string {
	return filepath.Join(dir, Slug(run.Suite), run.ID+".html")
}

// LoadAll reads every stored run under dir. Files that cannot be read are
// returned as problems rather than failing, so one bad file does not cost the
// index the rest of the history.
func LoadAll(dir string) ([]*model.Run, []error) {
	var runs []*model.Run
	var problems []error
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".json" || d.Name() == "badge.json" {
			return nil
		}
		run, err := Load(path)
		if err != nil {
			problems = append(problems, err)
			return nil
		}
		runs = append(runs, run)
		return nil
	})
	if err != nil {
		problems = append(problems, err)
	}
	return runs, problems
}

// WriteRun writes the run to an exact path.
func WriteRun(run *model.Run, path string) error {
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".run-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Load reads a stored run.
func Load(path string) (*model.Run, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var run model.Run
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if run.SchemaVersion != model.SchemaVersion {
		return nil, fmt.Errorf("%s: schema version %d, want %d", path, run.SchemaVersion, model.SchemaVersion)
	}
	return &run, nil
}
