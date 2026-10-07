package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/environment"
)

// Environment is one job environment.
type Environment struct {
	ID            string
	ScaleSet      string
	State         string
	RuntimeRef    string
	RunnerName    string
	RunnerID      int64
	IP            string
	TokenHash     string
	FailureStage  string
	FailureReason string
	JobID         string
	ExitCode      *int
	MemoryMB      int
	// Kind is KindJob, KindBuild or KindVerify (empty means KindJob).
	Kind string
	// TemplateVMID is the template the environment was cloned from (retention).
	TemplateVMID   int
	CreatedAt      time.Time
	UpdatedAt      time.Time
	StateChangedAt time.Time
}

// EnvironmentFilter narrows ListEnvironments. Zero values mean "any".
type EnvironmentFilter struct {
	States   []string
	ScaleSet string
	Kinds    []string
	Limit    int
}

const envColumns = `id, scale_set, state, runtime_ref, runner_name, runner_id, ip, token_hash,
	failure_stage, failure_reason, job_id, exit_code, memory_mb, kind, template_vmid, created_at, updated_at, state_changed_at`

type scanner interface{ Scan(dest ...any) error }

func scanEnvironment(row scanner) (Environment, error) {
	var e Environment
	var exit sql.NullInt64
	var created, updated, changed int64
	err := row.Scan(&e.ID, &e.ScaleSet, &e.State, &e.RuntimeRef, &e.RunnerName, &e.RunnerID, &e.IP, &e.TokenHash,
		&e.FailureStage, &e.FailureReason, &e.JobID, &exit, &e.MemoryMB, &e.Kind, &e.TemplateVMID, &created, &updated, &changed)
	if errors.Is(err, sql.ErrNoRows) {
		return Environment{}, ErrNotFound
	}
	if err != nil {
		return Environment{}, err
	}
	if exit.Valid {
		v := int(exit.Int64)
		e.ExitCode = &v
	}
	e.CreatedAt, e.UpdatedAt, e.StateChangedAt = fromMs(created), fromMs(updated), fromMs(changed)
	return e, nil
}

func exitArg(e Environment) any {
	if e.ExitCode == nil {
		return nil
	}
	return *e.ExitCode
}

// CreateEnvironment inserts a new environment.
func (s *Store) CreateEnvironment(ctx context.Context, e Environment) error {
	now := time.Now()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = e.CreatedAt
	}
	if e.StateChangedAt.IsZero() {
		e.StateChangedAt = e.CreatedAt
	}
	if e.Kind == "" {
		e.Kind = KindJob
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO environments (`+envColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.ScaleSet, e.State, e.RuntimeRef, e.RunnerName, e.RunnerID, e.IP, e.TokenHash,
		e.FailureStage, e.FailureReason, e.JobID, exitArg(e), e.MemoryMB, e.Kind, e.TemplateVMID, ms(e.CreatedAt), ms(e.UpdatedAt), ms(e.StateChangedAt))
	if err != nil {
		return fmt.Errorf("store: create environment %s: %w", e.ID, err)
	}
	return nil
}

// GetEnvironment returns one environment or ErrNotFound.
func (s *Store) GetEnvironment(ctx context.Context, id string) (Environment, error) {
	return scanEnvironment(s.db.QueryRowContext(ctx, `SELECT `+envColumns+` FROM environments WHERE id = ?`, id))
}

// FindEnvironmentByToken returns the environment whose token hash matches.
func (s *Store) FindEnvironmentByToken(ctx context.Context, tokenHash string) (Environment, error) {
	if tokenHash == "" {
		return Environment{}, ErrNotFound
	}
	return scanEnvironment(s.db.QueryRowContext(ctx, `SELECT `+envColumns+` FROM environments WHERE token_hash = ?`, tokenHash))
}

// FindEnvironmentByRunner returns the newest environment with this runner name.
func (s *Store) FindEnvironmentByRunner(ctx context.Context, runnerName string) (Environment, error) {
	if runnerName == "" {
		return Environment{}, ErrNotFound
	}
	return scanEnvironment(s.db.QueryRowContext(ctx,
		`SELECT `+envColumns+` FROM environments WHERE runner_name = ? ORDER BY created_at DESC LIMIT 1`, runnerName))
}

// FindEnvironmentByVMID returns the environment on a Proxmox VMID: a live one first,
// else the newest (runtime references are "<vmid>/<environment id>").
func (s *Store) FindEnvironmentByVMID(ctx context.Context, vmid int) (Environment, error) {
	return scanEnvironment(s.db.QueryRowContext(ctx, `SELECT `+envColumns+` FROM environments WHERE runtime_ref LIKE ?
		ORDER BY state = 'destroyed', created_at DESC LIMIT 1`, fmt.Sprintf("%d/%%", vmid)))
}

// ListEnvironments returns environments, newest first.
func (s *Store) ListEnvironments(ctx context.Context, f EnvironmentFilter) ([]Environment, error) {
	var where []string
	var args []any
	if len(f.States) > 0 {
		where = append(where, "state IN (?"+strings.Repeat(",?", len(f.States)-1)+")")
		for _, st := range f.States {
			args = append(args, st)
		}
	}
	if f.ScaleSet != "" {
		where = append(where, "scale_set = ?")
		args = append(args, f.ScaleSet)
	}
	if len(f.Kinds) > 0 {
		where = append(where, "kind IN (?"+strings.Repeat(",?", len(f.Kinds)-1)+")")
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	q := `SELECT ` + envColumns + ` FROM environments`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at DESC, id DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Environment
	for rows.Next() {
		e, err := scanEnvironment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// TransitionEnvironment moves an environment to state `to` if its current state is
// in `from` (any state when from is empty) and the state machine allows it. mutate,
// when non-nil, may change other fields in the same transaction.
func (s *Store) TransitionEnvironment(ctx context.Context, id string, from []string, to string, mutate func(*Environment)) (Environment, error) {
	return s.updateEnvironment(ctx, id, func(e *Environment) error {
		if len(from) > 0 && !slices.Contains(from, e.State) {
			return fmt.Errorf("%w: environment %s is %s, not %v", ErrConflict, id, e.State, from)
		}
		if !environment.CanTransition(environment.State(e.State), environment.State(to)) {
			return fmt.Errorf("store: environment %s cannot go from %s to %s", id, e.State, to)
		}
		if mutate != nil {
			mutate(e)
		}
		e.State = to
		e.StateChangedAt = time.Now()
		return nil
	})
}

// UpdateEnvironment changes non-state fields. Changes to State are ignored.
func (s *Store) UpdateEnvironment(ctx context.Context, id string, mutate func(*Environment)) (Environment, error) {
	return s.updateEnvironment(ctx, id, func(e *Environment) error {
		state, changed := e.State, e.StateChangedAt
		mutate(e)
		e.State, e.StateChangedAt = state, changed
		return nil
	})
}

func (s *Store) updateEnvironment(ctx context.Context, id string, apply func(*Environment) error) (Environment, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Environment{}, err
	}
	defer func() { _ = tx.Rollback() }()
	e, err := scanEnvironment(tx.QueryRowContext(ctx, `SELECT `+envColumns+` FROM environments WHERE id = ?`, id))
	if err != nil {
		return Environment{}, err
	}
	if err := apply(&e); err != nil {
		return Environment{}, err
	}
	e.UpdatedAt = time.Now()
	_, err = tx.ExecContext(ctx, `UPDATE environments SET scale_set=?, state=?, runtime_ref=?, runner_name=?, runner_id=?, ip=?, token_hash=?,
		failure_stage=?, failure_reason=?, job_id=?, exit_code=?, memory_mb=?, kind=?, template_vmid=?, updated_at=?, state_changed_at=? WHERE id=?`,
		e.ScaleSet, e.State, e.RuntimeRef, e.RunnerName, e.RunnerID, e.IP, e.TokenHash,
		e.FailureStage, e.FailureReason, e.JobID, exitArg(e), e.MemoryMB, e.Kind, e.TemplateVMID, ms(e.UpdatedAt), ms(e.StateChangedAt), id)
	if err != nil {
		return Environment{}, err
	}
	return e, tx.Commit()
}
