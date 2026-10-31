package grader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Manoj-Paramsetti/LocoAI/internal/config"
	"github.com/Manoj-Paramsetti/LocoAI/internal/model"
)

// jsonSchema checks that the output is JSON valid against the suite's schema,
// or against the case's expected value when the suite sets none.
type jsonSchema struct{ fixed *jsonschema.Schema }

func newJSONSchema(spec config.GraderSpec, deps Deps) (Grader, error) {
	g := &jsonSchema{}
	switch {
	case spec.Schema != nil && spec.SchemaFile != "":
		return nil, errors.New("set schema or schema_file, not both")
	case spec.SchemaFile != "":
		data, err := os.ReadFile(deps.Suite.Path(spec.SchemaFile))
		if err != nil {
			return nil, err
		}
		if g.fixed, err = compileSchema(data); err != nil {
			return nil, err
		}
	case spec.Schema != nil:
		data, err := json.Marshal(spec.Schema)
		if err != nil {
			return nil, err
		}
		if g.fixed, err = compileSchema(data); err != nil {
			return nil, err
		}
	}
	return g, nil
}

func compileSchema(data []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("schema.json")
}

func (g *jsonSchema) schema(c model.Case) (*jsonschema.Schema, error) {
	if g.fixed != nil {
		return g.fixed, nil
	}
	if _, ok := c.Expected.(map[string]any); !ok {
		return nil, errors.New("json_schema grader needs a schema or an object expected value")
	}
	data, err := json.Marshal(c.Expected)
	if err != nil {
		return nil, err
	}
	return compileSchema(data)
}

func (g *jsonSchema) Name() string { return "json_schema" }

func (g *jsonSchema) Validate(c model.Case) error {
	_, err := g.schema(c)
	return err
}

func (g *jsonSchema) Grade(_ context.Context, c model.Case, output string) (Result, error) {
	sch, err := g.schema(c)
	if err != nil {
		return Result{}, err
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(output))
	if err != nil {
		return Result{0, fmt.Sprintf("output is not valid JSON: %v", err)}, nil
	}
	if err := sch.Validate(inst); err != nil {
		return Result{0, clip(fmt.Sprintf("schema violation: %v", err))}, nil
	}
	return Result{1, "output satisfies schema"}, nil
}
