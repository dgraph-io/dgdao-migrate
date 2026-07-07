package migrate

import (
	"context"
	"encoding/json"
	"fmt"

	mg "github.com/dgraph-io/dgdao"
	"github.com/dgraph-io/dgo/v250/protos/api"
)

// ScalarType is the target Dgraph scalar for a RetypePredicate.
type ScalarType string

const (
	Int      ScalarType = "int"
	Float    ScalarType = "float"
	String   ScalarType = "string"
	Bool     ScalarType = "bool"
	DateTime ScalarType = "datetime"
)

// RetypeSpec describes an in-place predicate type change with a value transform.
type RetypeSpec struct {
	// Predicate is the existing predicate to retype; it keeps its name. The
	// source predicate must currently be of Dgraph type string: the stage step
	// reads each value into a Go string, so a non-string source would silently
	// decode to "".
	Predicate string
	// To is the target scalar type.
	To ScalarType
	// Index is an optional tokenizer for the retyped predicate (e.g. "int"); "" = none.
	Index string
	// Convert maps each existing value, rendered as a string, to its new typed
	// value. It is required; RetypePredicate panics if it is nil.
	Convert func(old string) (any, error)
}

func (s RetypeSpec) staging() string { return s.Predicate + "__retype_staging" }

func (s RetypeSpec) schemaLine(pred string) string {
	if s.Index != "" {
		return fmt.Sprintf("%s: %s @index(%s) .", pred, s.To, s.Index)
	}
	return fmt.Sprintf("%s: %s .", pred, s.To)
}

// RetypePredicate expands spec into five staged, checkpointed, idempotent steps:
//
//  1. stage   — declare the staging predicate; read source values, Convert, write staging
//  2. verify  — assert staging count == source count, else ErrVerifyFailed (before any drop)
//  3. swap    — drop the old predicate, redeclare it as To (staging holds the data)
//  4. copy    — copy staging values onto the retyped predicate
//  5. cleanup — drop the staging predicate
//
// The source predicate must be of Dgraph type string; Convert receives each
// value as its string rendering. spec.Convert is required: RetypePredicate
// panics if it is nil.
//
// The op is irreversible (lossy): every step's Down is nil. To reverse, author a
// new forward RetypePredicate(To→original) with an inverse Convert.
func RetypePredicate(spec RetypeSpec) []Step {
	if spec.Convert == nil {
		panic("migrate: RetypeSpec.Convert must not be nil")
	}
	staging := spec.staging()
	return []Step{
		{
			Name:   spec.Predicate + "_retype_stage",
			Schema: SchemaChange{Alter: spec.schemaLine(staging)},
			Up:     func(ctx context.Context, c mg.Client) error { return retypeStage(ctx, c, spec, staging) },
		},
		{
			Name: spec.Predicate + "_retype_verify",
			Up:   func(ctx context.Context, c mg.Client) error { return retypeVerify(ctx, c, spec.Predicate, staging) },
		},
		{
			Name: spec.Predicate + "_retype_swap",
			Up:   func(ctx context.Context, c mg.Client) error { return retypeSwap(ctx, c, spec) },
		},
		{
			Name: spec.Predicate + "_retype_copy",
			Up:   func(ctx context.Context, c mg.Client) error { return retypeCopy(ctx, c, spec, staging) },
		},
		{
			Name: spec.Predicate + "_retype_cleanup",
			Up:   func(ctx context.Context, c mg.Client) error { return dropAttr(ctx, c, staging) },
		},
	}
}

func retypeStage(ctx context.Context, c mg.Client, spec RetypeSpec, staging string) error {
	raw, err := c.QueryRaw(ctx, fmt.Sprintf(`{ q(func: has(%s)) { uid v: %s } }`, spec.Predicate, spec.Predicate), nil)
	if err != nil {
		return err
	}
	var res struct {
		Q []struct {
			UID string `json:"uid"`
			V   string `json:"v"`
		} `json:"q"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	rows := make([]map[string]any, 0, len(res.Q))
	for _, r := range res.Q {
		conv, err := spec.Convert(r.V)
		if err != nil {
			return fmt.Errorf("migrate: convert predicate %s uid %s: %w", spec.Predicate, r.UID, err)
		}
		rows = append(rows, map[string]any{"uid": r.UID, staging: conv})
	}
	return mutateRows(ctx, c, rows)
}

func retypeVerify(ctx context.Context, c mg.Client, pred, staging string) error {
	sc, err := countHas(ctx, c, pred)
	if err != nil {
		return err
	}
	stc, err := countHas(ctx, c, staging)
	if err != nil {
		return err
	}
	if sc != stc {
		return &ErrVerifyFailed{Predicate: pred, SourceCount: sc, StagingCount: stc}
	}
	return nil
}

func retypeSwap(ctx context.Context, c mg.Client, spec RetypeSpec) error {
	if err := dropAttr(ctx, c, spec.Predicate); err != nil {
		return err
	}
	return c.AlterSchema(ctx, spec.schemaLine(spec.Predicate))
}

func retypeCopy(ctx context.Context, c mg.Client, spec RetypeSpec, staging string) error {
	raw, err := c.QueryRaw(ctx, fmt.Sprintf(`{ q(func: has(%s)) { uid v: %s } }`, staging, staging), nil)
	if err != nil {
		return err
	}
	var res struct {
		Q []struct {
			UID string          `json:"uid"`
			V   json.RawMessage `json:"v"`
		} `json:"q"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	rows := make([]map[string]any, 0, len(res.Q))
	for _, r := range res.Q {
		rows = append(rows, map[string]any{"uid": r.UID, spec.Predicate: json.RawMessage(r.V)})
	}
	return mutateRows(ctx, c, rows)
}

func countHas(ctx context.Context, c mg.Client, pred string) (int, error) {
	raw, err := c.QueryRaw(ctx, fmt.Sprintf(`{ q(func: has(%s)) { c: count(uid) } }`, pred), nil)
	if err != nil {
		return 0, err
	}
	var res struct {
		Q []struct {
			C int `json:"c"`
		} `json:"q"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, err
	}
	if len(res.Q) == 0 {
		return 0, nil
	}
	return res.Q[0].C, nil
}

func mutateRows(ctx context.Context, c mg.Client, rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}
	dg, cleanup, err := c.DgraphClient()
	if err != nil {
		return err
	}
	defer cleanup()
	payload, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	_, err = dg.NewTxn().Mutate(ctx, &api.Mutation{SetJson: payload, CommitNow: true})
	return err
}

func dropAttr(ctx context.Context, c mg.Client, pred string) error {
	dg, cleanup, err := c.DgraphClient()
	if err != nil {
		return err
	}
	defer cleanup()
	return dg.Alter(ctx, &api.Operation{DropAttr: pred})
}
