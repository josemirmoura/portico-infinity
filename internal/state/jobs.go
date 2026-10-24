package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type JobRecord struct {
	ID        string
	Subject   string
	Tool      string
	Resource  string
	UnitName  string
	GrantID   string
	State     string
	Deadline  time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
	ExitCode  *int
	Detail    string
}

func (s *Store) CreateJob(ctx context.Context, j JobRecord) error {
	if j.ID == "" || j.Subject == "" || j.Tool == "" || j.State == "" || j.Deadline.IsZero() {
		return errors.New("job id, subject, tool, state and deadline are required")
	}
	now := time.Now()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = now
	}
	j.UpdatedAt = now
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs(job_id,subject,tool,resource,unit_name,grant_id,state,deadline,created_at,updated_at,detail)
		  VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.Subject, j.Tool, j.Resource, j.UnitName, j.GrantID, j.State,
		j.Deadline.UnixNano(), j.CreatedAt.UnixNano(), j.UpdatedAt.UnixNano(), j.Detail)
	return err
}

func (s *Store) UpdateJobState(ctx context.Context, id, stateName, detail string, exitCode *int) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state=?, detail=?, exit_code=?, updated_at=? WHERE job_id=?`,
		stateName, detail, exitCode, time.Now().UnixNano(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("job not found")
	}
	return nil
}


// UpdateActiveJobState transitions only nonterminal in-flight records. A
// concurrent status poll, janitor or cancellation must never overwrite a
// previously persisted terminal or reconciliation-required checkpoint.
func (s *Store) UpdateActiveJobState(ctx context.Context, id, stateName, detail string, exitCode *int) (bool, error) {
    result, err := s.db.ExecContext(ctx,
        `UPDATE jobs SET state=?, detail=?, exit_code=?, updated_at=? WHERE job_id=?
          AND state IN ('starting','running','active','activating','reloading','deactivating','cancelling')`,
        stateName, detail, exitCode, time.Now().UnixNano(), id)
    if err != nil { return false, err }
    affected, err := result.RowsAffected()
    if err != nil { return false, err }
    return affected == 1, nil
}

func (s *Store) GetJob(ctx context.Context, id string) (JobRecord, error) {
	var j JobRecord
	var deadline, created, updated int64
	var exit sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT job_id,subject,tool,resource,unit_name,grant_id,state,deadline,created_at,updated_at,exit_code,detail
		  FROM jobs WHERE job_id=?`, id).
		Scan(&j.ID, &j.Subject, &j.Tool, &j.Resource, &j.UnitName, &j.GrantID, &j.State,
			&deadline, &created, &updated, &exit, &j.Detail)
	if err != nil {
		return JobRecord{}, err
	}
	j.Deadline = time.Unix(0, deadline)
	j.CreatedAt = time.Unix(0, created)
	j.UpdatedAt = time.Unix(0, updated)
	if exit.Valid {
		v := int(exit.Int64)
		j.ExitCode = &v
	}
	return j, nil
}


// ListActiveShellJobsPage performs a bounded keyset scan in SQLite. It does
// not materialize the entire active-job ledger before applying the limit.
// Only shell tools are eligible; Compose/filecopy and foreign jobs are
// deliberately excluded. A zero cursor starts at the oldest eligible row.
func (s *Store) ListActiveShellJobsPage(
    ctx context.Context, afterCreated time.Time, afterID string,
    createdBefore time.Time, limit int,
) ([]JobRecord, error) {
    if limit < 1 || limit > 64 {
        return nil, errors.New("shell reconciliation page limit must be between 1 and 64")
    }
    if createdBefore.IsZero() {
        return nil, errors.New("shell reconciliation cutoff is required")
    }
    var cursorNanos int64
    if !afterCreated.IsZero() {
        cursorNanos = afterCreated.UnixNano()
    }
    rows, err := s.db.QueryContext(ctx,
        `SELECT job_id FROM jobs
          WHERE tool IN ('shell.exec','job.start','shell.exec_admin')
            AND state IN ('starting','running','active','activating','reloading','deactivating','cancelling')
            AND created_at <= ?
            AND (created_at > ? OR (created_at = ? AND job_id > ?))
          ORDER BY created_at, job_id LIMIT ?`,
        createdBefore.UnixNano(), cursorNanos, cursorNanos, afterID, limit)
    if err != nil { return nil, err }
    // Close the rows before calling GetJob, since SQLite is configured with
    // MaxOpenConns(1) and a nested query would otherwise deadlock.
    ids := make([]string, 0, limit)
    for rows.Next() {
        var id string
        if err := rows.Scan(&id); err != nil {
            _ = rows.Close()
            return nil, err
        }
        ids = append(ids, id)
    }
    if err := rows.Err(); err != nil {
        _ = rows.Close()
        return nil, err
    }
    if err := rows.Close(); err != nil { return nil, err }
    jobs := make([]JobRecord, 0, len(ids))
    for _, id := range ids {
        job, err := s.GetJob(ctx, id)
        if errors.Is(err, sql.ErrNoRows) {
            // An operator may remove a record after the bounded SELECT.
            continue
        }
        if err != nil { return nil, err }
        jobs = append(jobs, job)
    }
    return jobs, nil
}


// FirstBlockingComposeJob performs a bounded lookup for a single project.
// An unresolved earlier dispatch is a resource fence even when it no longer
// appears in ListActiveJobs. No job from another project or a filecopy/shell
// operation can be returned.
func (s *Store) FirstBlockingComposeJob(ctx context.Context, project string)(JobRecord,bool,error){
    var id string
    err:=s.db.QueryRowContext(ctx,`
        SELECT job_id FROM jobs
        WHERE resource=? AND tool IN ('compose.pull','compose.up','compose.down')
          AND state IN ('starting','running','active','activating','reloading',
                        'deactivating','cancelling','reconcile_required')
        ORDER BY created_at, job_id LIMIT 1`,project).Scan(&id)
    if errors.Is(err,sql.ErrNoRows){return JobRecord{},false,nil}
    if err!=nil{return JobRecord{},false,err}
    record,err:=s.GetJob(ctx,id)
    if errors.Is(err,sql.ErrNoRows){
        return JobRecord{},false,errors.New("compose job disappeared during resource fence; operator reconciliation required")
    }
    if err!=nil{return JobRecord{},false,err}
    return record,true,nil
}

func (s *Store) ListActiveJobs(ctx context.Context) ([]JobRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT job_id FROM jobs WHERE state IN ('starting','running','active','activating','reloading','deactivating','cancelling') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]JobRecord, 0, len(ids))
	for _, id := range ids {
		j, err := s.GetJob(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}
