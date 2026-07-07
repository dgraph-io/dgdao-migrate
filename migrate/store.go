package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	mg "github.com/dgraph-io/dgdao"
)

// store persists migration state. It is the only layer that knows the Dgraph
// record/lock types.
type store interface {
	bootstrap(ctx context.Context) error
	// loadAppliedMigrations returns fully-applied migration records (all steps
	// done), sorted by ID ascending.
	loadAppliedMigrations(ctx context.Context) ([]migrationRec, error)
	// loadStepRecords returns the completed steps for one migration, any state.
	loadStepRecords(ctx context.Context, migrationID int64) ([]stepRec, error)
	// saveStep records a single step as completed.
	saveStep(ctx context.Context, migrationID int64, name string, index int, checksum string, durationMs int64) error
	// saveMigration records a migration as fully applied.
	saveMigration(ctx context.Context, id int64, name, checksum string, durationMs int64) error
	// removeMigration deletes a migration record and all its step records (Down).
	removeMigration(ctx context.Context, id int64) error
	acquireLock(ctx context.Context) error
	releaseLock(ctx context.Context) error
}

// migrationRec is the in-memory view of a MigrationRecord node.
type migrationRec struct {
	ID       int64  `json:"migrationId,omitempty"`
	Name     string `json:"migrationName,omitempty"`
	Checksum string `json:"migrationChecksum,omitempty"`
}

// stepRec is the in-memory view of a MigrationStepRecord node.
type stepRec struct {
	MigrationID int64  `json:"stepMigrationId,omitempty"`
	Name        string `json:"stepName,omitempty"`
	Index       int    `json:"stepIndex"`
	Checksum    string `json:"stepChecksum,omitempty"`
}

// --- Dgraph node types (migration_* / migration_step_* predicate prefixes) ---

type migrationRecord struct {
	UID        string    `json:"uid,omitempty"`
	DType      []string  `json:"dgraph.type,omitempty" dgraph:"MigrationRecord"`
	ID         int64     `json:"migrationId,omitempty" dgraph:"predicate=migration_id index=int"`
	Name       string    `json:"migrationName,omitempty" dgraph:"predicate=migration_name"`
	AppliedAt  time.Time `json:"migrationAppliedAt,omitempty" dgraph:"predicate=migration_applied_at index=hour"`
	Checksum   string    `json:"migrationChecksum,omitempty" dgraph:"predicate=migration_checksum"`
	DurationMs int64     `json:"migrationDurationMs,omitempty" dgraph:"predicate=migration_duration_ms"`
}

type migrationStepRecord struct {
	UID         string    `json:"uid,omitempty"`
	DType       []string  `json:"dgraph.type,omitempty" dgraph:"MigrationStepRecord"`
	MigrationID int64     `json:"stepMigrationId,omitempty" dgraph:"predicate=migration_step_migration_id index=int"`
	Name        string    `json:"stepName,omitempty" dgraph:"predicate=migration_step_name"`
	Index       int       `json:"stepIndex" dgraph:"predicate=migration_step_index"`
	Checksum    string    `json:"stepChecksum,omitempty" dgraph:"predicate=migration_step_checksum"`
	AppliedAt   time.Time `json:"stepAppliedAt,omitempty" dgraph:"predicate=migration_step_applied_at"`
	DurationMs  int64     `json:"stepDurationMs,omitempty" dgraph:"predicate=migration_step_duration_ms"`
}

type migrationLock struct {
	UID      string    `json:"uid,omitempty"`
	DType    []string  `json:"dgraph.type,omitempty" dgraph:"MigrationLock"`
	LockKey  string    `json:"migrationLockKey,omitempty" dgraph:"predicate=migration_lock_key index=exact upsert"`
	LockedAt time.Time `json:"migrationLockedAt,omitempty" dgraph:"predicate=migration_locked_at"`
}

const lockSingleton = "singleton"
const defaultLockTTL = 5 * time.Minute

type dgraphStore struct {
	conn    mg.Client
	lockTTL time.Duration
}

func newDgraphStore(conn mg.Client) *dgraphStore {
	return &dgraphStore{conn: conn, lockTTL: defaultLockTTL}
}

func (s *dgraphStore) bootstrap(ctx context.Context) error {
	return s.conn.UpdateSchema(ctx, &migrationRecord{}, &migrationStepRecord{}, &migrationLock{})
}

func (s *dgraphStore) loadAppliedMigrations(ctx context.Context) ([]migrationRec, error) {
	raw, err := s.conn.QueryRaw(ctx,
		`{ records(func: type(MigrationRecord), orderasc: migration_id) {
			migrationId: migration_id
			migrationName: migration_name
			migrationChecksum: migration_checksum
		} }`, nil)
	if err != nil {
		return nil, fmt.Errorf("loading applied migrations: %w", err)
	}
	var result struct {
		Records []migrationRec `json:"records"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parsing migration records: %w", err)
	}
	return result.Records, nil
}

func (s *dgraphStore) loadStepRecords(ctx context.Context, migrationID int64) ([]stepRec, error) {
	raw, err := s.conn.QueryRaw(ctx,
		fmt.Sprintf(`{ steps(func: type(MigrationStepRecord)) @filter(eq(migration_step_migration_id, %d)) {
			stepMigrationId: migration_step_migration_id
			stepName: migration_step_name
			stepIndex: migration_step_index
			stepChecksum: migration_step_checksum
		} }`, migrationID), nil)
	if err != nil {
		return nil, fmt.Errorf("loading step records for %d: %w", migrationID, err)
	}
	var result struct {
		Steps []stepRec `json:"steps"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parsing step records: %w", err)
	}
	return result.Steps, nil
}

// Insert (not Upsert) is safe: the Runner only calls this for a step/migration
// that has no record yet — already-recorded steps and migrations are skipped on
// (re-)run — so each is saved exactly once.
func (s *dgraphStore) saveStep(ctx context.Context, migrationID int64, name string, index int, checksum string, durationMs int64) error {
	return s.conn.Insert(ctx, &migrationStepRecord{
		DType:       []string{"MigrationStepRecord"},
		MigrationID: migrationID,
		Name:        name,
		Index:       index,
		Checksum:    checksum,
		AppliedAt:   time.Now().UTC(),
		DurationMs:  durationMs,
	})
}

// Insert (not Upsert) is safe: the Runner only calls this for a step/migration
// that has no record yet — already-recorded steps and migrations are skipped on
// (re-)run — so each is saved exactly once.
func (s *dgraphStore) saveMigration(ctx context.Context, id int64, name, checksum string, durationMs int64) error {
	return s.conn.Insert(ctx, &migrationRecord{
		DType:      []string{"MigrationRecord"},
		ID:         id,
		Name:       name,
		AppliedAt:  time.Now().UTC(),
		Checksum:   checksum,
		DurationMs: durationMs,
	})
}

func (s *dgraphStore) removeMigration(ctx context.Context, id int64) error {
	raw, err := s.conn.QueryRaw(ctx,
		fmt.Sprintf(`{
			m(func: type(MigrationRecord)) @filter(eq(migration_id, %d)) { uid }
			st(func: type(MigrationStepRecord)) @filter(eq(migration_step_migration_id, %d)) { uid }
		}`, id, id), nil)
	if err != nil {
		return fmt.Errorf("finding records for migration %d: %w", id, err)
	}
	var result struct {
		M []struct {
			UID string `json:"uid"`
		} `json:"m"`
		St []struct {
			UID string `json:"uid"`
		} `json:"st"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("parsing remove query: %w", err)
	}
	var uids []string
	for _, r := range result.M {
		uids = append(uids, r.UID)
	}
	for _, r := range result.St {
		uids = append(uids, r.UID)
	}
	if len(uids) == 0 {
		return nil
	}
	return s.conn.Delete(ctx, uids)
}

// NOTE: acquireLock is not atomic — it reads then writes in separate
// transactions, so two concurrent runners can both observe no/stale lock and
// proceed. A conditional-upsert/CAS helper on the Client is the planned fix
// (see spec "dgdao improvement candidates"). For now rely on single-runner
// deploys (app scaled to zero).
func (s *dgraphStore) acquireLock(ctx context.Context) error {
	now := time.Now().UTC()
	threshold := now.Add(-s.lockTTL)
	raw, err := s.conn.QueryRaw(ctx,
		`{ lock(func: type(MigrationLock)) @filter(eq(migration_lock_key, "singleton")) {
			uid migration_locked_at
		} }`, nil)
	if err != nil {
		return fmt.Errorf("checking migration lock: %w", err)
	}
	var result struct {
		Lock []struct {
			UID      string    `json:"uid"`
			LockedAt time.Time `json:"migration_locked_at"`
		} `json:"lock"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("parsing lock query: %w", err)
	}
	if len(result.Lock) > 0 && result.Lock[0].LockedAt.After(threshold) {
		return ErrLocked
	}
	if len(result.Lock) > 0 {
		return s.conn.Update(ctx, &migrationLock{
			UID: result.Lock[0].UID, DType: []string{"MigrationLock"},
			LockKey: lockSingleton, LockedAt: now,
		})
	}
	return s.conn.Insert(ctx, &migrationLock{
		DType: []string{"MigrationLock"}, LockKey: lockSingleton, LockedAt: now,
	})
}

func (s *dgraphStore) releaseLock(ctx context.Context) error {
	raw, err := s.conn.QueryRaw(ctx,
		`{ lock(func: type(MigrationLock)) @filter(eq(migration_lock_key, "singleton")) { uid } }`, nil)
	if err != nil {
		return fmt.Errorf("querying lock for release: %w", err)
	}
	var result struct {
		Lock []struct {
			UID string `json:"uid"`
		} `json:"lock"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("parsing lock release: %w", err)
	}
	if len(result.Lock) == 0 {
		return nil
	}
	return s.conn.Delete(ctx, []string{result.Lock[0].UID})
}
