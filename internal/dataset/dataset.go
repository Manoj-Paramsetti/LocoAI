// Package dataset loads and validates test cases from YAML or JSONL files.
package dataset

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Manoj-Paramsetti/LocoAI/internal/model"
)

// Load reads a dataset, reporting every problem at once so a bad file only
// needs one fix-and-retry round.
func Load(path string) ([]model.Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []model.Case
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		cases, err = parseYAML(data)
	case ".jsonl":
		cases, err = parseJSONL(data)
	default:
		return nil, fmt.Errorf("%s: unsupported dataset format (want .yaml, .yml or .jsonl)", path)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := Validate(cases); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cases, nil
}

func parseYAML(data []byte) ([]model.Case, error) {
	var doc struct {
		Cases []model.Case `yaml:"cases"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, err
	}
	return doc.Cases, nil
}

func parseJSONL(data []byte) ([]model.Case, error) {
	var cases []model.Case
	var errs []error
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for line := 1; sc.Scan(); line++ {
		text := bytes.TrimSpace(sc.Bytes())
		if len(text) == 0 {
			continue
		}
		var c model.Case
		dec := json.NewDecoder(bytes.NewReader(text))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			errs = append(errs, fmt.Errorf("line %d: %w", line, err))
			continue
		}
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		errs = append(errs, err)
	}
	return cases, errors.Join(errs...)
}

// Validate enforces the case schema: unique non-empty ids, an input, and at
// least one of expected or rubric.
func Validate(cases []model.Case) error {
	if len(cases) == 0 {
		return errors.New("dataset has no cases")
	}
	var errs []error
	seen := make(map[string]bool, len(cases))
	for i, c := range cases {
		label := fmt.Sprintf("case %d", i+1)
		if c.ID == "" {
			errs = append(errs, fmt.Errorf("%s: missing id", label))
		} else {
			label = fmt.Sprintf("case %q", c.ID)
			if seen[c.ID] {
				errs = append(errs, fmt.Errorf("%s: duplicate id", label))
			}
			seen[c.ID] = true
		}
		if strings.TrimSpace(c.Input) == "" {
			errs = append(errs, fmt.Errorf("%s: missing input", label))
		}
		if c.Expected == nil && c.Rubric == "" {
			errs = append(errs, fmt.Errorf("%s: needs expected or rubric", label))
		}
	}
	return errors.Join(errs...)
}
