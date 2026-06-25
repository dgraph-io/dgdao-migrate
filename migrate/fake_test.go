package migrate

import (
	"context"
	"sort"
	"sync"
	"testing"

	"github.com/dgraph-io/dgo/v250"
	dg "github.com/dolan-in/dgman/v2"
	mg "github.com/matthewmcneely/modusgraph"
)

// mustMarshalSchema renders the models' schema, failing the test on a conflict.
func mustMarshalSchema(t *testing.T, models ...any) string {
	t.Helper()
	s, err := MarshalSchema(models...)
	if err != nil {
		t.Fatalf("MarshalSchema: %v", err)
	}
	return s
}

// fakeStore is an in-memory store for unit-testing the Runner.
type fakeStore struct {
	mu      sync.Mutex
	migs    []migrationRec
	steps   []stepRec
	locked  bool
	bootErr error
	lockErr error
	saveErr error // injected error for saveStep
}

func newFakeStore() *fakeStore { return &fakeStore{} }

func (f *fakeStore) bootstrap(context.Context) error { return f.bootErr }

func (f *fakeStore) loadAppliedMigrations(context.Context) ([]migrationRec, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]migrationRec(nil), f.migs...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeStore) loadStepRecords(_ context.Context, migrationID int64) ([]stepRec, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []stepRec
	for _, s := range f.steps {
		if s.MigrationID == migrationID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeStore) saveStep(_ context.Context, migrationID int64, name string, index int, checksum string, _ int64) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, stepRec{MigrationID: migrationID, Name: name, Index: index, Checksum: checksum})
	return nil
}

func (f *fakeStore) saveMigration(_ context.Context, id int64, name, checksum string, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.migs = append(f.migs, migrationRec{ID: id, Name: name, Checksum: checksum})
	return nil
}

func (f *fakeStore) removeMigration(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var migs []migrationRec
	for _, m := range f.migs {
		if m.ID != id {
			migs = append(migs, m)
		}
	}
	f.migs = migs
	var steps []stepRec
	for _, s := range f.steps {
		if s.MigrationID != id {
			steps = append(steps, s)
		}
	}
	f.steps = steps
	return nil
}

func (f *fakeStore) acquireLock(context.Context) error {
	if f.lockErr != nil {
		return f.lockErr
	}
	if f.locked {
		return ErrLocked
	}
	f.locked = true
	return nil
}

func (f *fakeStore) releaseLock(context.Context) error { f.locked = false; return nil }

// seedStep records a step as already applied (test setup for resume/skip).
func (f *fakeStore) seedStep(migrationID int64, name string, index int, checksum string) {
	f.steps = append(f.steps, stepRec{MigrationID: migrationID, Name: name, Index: index, Checksum: checksum})
}

// seedMigration records a migration as fully applied (test setup).
func (f *fakeStore) seedMigration(id int64, name, checksum string) {
	f.migs = append(f.migs, migrationRec{ID: id, Name: name, Checksum: checksum})
}

// stubClient implements mg.Client. Schema calls are no-ops; the rest are inert.
type stubClient struct {
	schemaErr      error
	alterSchemaErr error
}

func (c *stubClient) Insert(context.Context, any) error                         { return nil }
func (c *stubClient) InsertRaw(context.Context, any) error                      { return nil }
func (c *stubClient) Upsert(context.Context, any, ...string) error              { return nil }
func (c *stubClient) Update(context.Context, any) error                         { return nil }
func (c *stubClient) LoadOrStore(context.Context, any, ...string) (bool, error) { return false, nil }
func (c *stubClient) LoadAndDelete(context.Context, any, any, ...string) (bool, error) {
	return false, nil
}
func (c *stubClient) Get(context.Context, any, string) error     { return nil }
func (c *stubClient) Query(context.Context, any) *dg.Query       { return nil }
func (c *stubClient) Delete(context.Context, []string) error     { return nil }
func (c *stubClient) Close()                                     {}
func (c *stubClient) UpdateSchema(context.Context, ...any) error { return c.schemaErr }
func (c *stubClient) AlterSchema(context.Context, string) error  { return c.alterSchemaErr }
func (c *stubClient) GetSchema(context.Context) (string, error)  { return "", nil }
func (c *stubClient) DropAll(context.Context) error              { return nil }
func (c *stubClient) DropData(context.Context) error             { return nil }
func (c *stubClient) QueryRaw(context.Context, string, map[string]string) ([]byte, error) {
	return []byte(`{}`), nil
}
func (c *stubClient) DgraphClient() (*dgo.Dgraph, func(), error) { return nil, func() {}, nil }
func (c *stubClient) WithRetry(_ context.Context, _ mg.RetryPolicy, fn func() error) error {
	return fn()
}
