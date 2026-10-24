package state

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "time"
)

// ShellUnitCleanup holds durable evidence that a terminal job still has a
// systemd unit retirement to verify. Commands are never replayed by this API.
type ShellUnitCleanup struct {
    JobID string
    Unit string
    Attempts int
    LastError string
}

// CompleteShellJobAndQueueCleanup changes the job to a proven terminal state
// and records its exact unit in the SAME SQLite transaction. It is safe if
// status polls and the janitor race: at most one terminal outcome wins.
func (s *Store) CompleteShellJobAndQueueCleanup(
    ctx context.Context, id, terminal, detail string, exitCode *int,
) (bool,error) {
    if terminal != "done" && terminal != "failed" {
        return false, errors.New("only proven shell terminal states may queue cleanup")
    }
    if exitCode == nil {
        return false, errors.New("terminal shell cleanup requires verified exit code")
    }
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil {return false,err}
    defer tx.Rollback()
    var unit,tool string
    if err:=tx.QueryRowContext(ctx,`SELECT unit_name, tool FROM jobs WHERE job_id=?`,id).Scan(&unit,&tool);err!=nil {return false,err}
    switch tool {
    case "shell.exec","job.start","shell.exec_admin":
    default:return false,errors.New("non-shell jobs cannot queue shell unit cleanup")
    }
    if unit=="" {return false,errors.New("shell job missing systemd unit")}
    now:=time.Now().UnixNano()
    result,err:=tx.ExecContext(ctx,
        `UPDATE jobs SET state=?,detail=?,exit_code=?,updated_at=? WHERE job_id=?
         AND state IN ('starting','running','active','activating','reloading','deactivating','cancelling')`,
         terminal,detail,*exitCode,now,id)
    if err!=nil {return false,err}
    changed,err:=result.RowsAffected()
    if err!=nil {return false,err}
    if changed==1 {
        if _,err:=tx.ExecContext(ctx,
           `INSERT INTO shell_unit_cleanup(job_id,unit_name,status,created_at,updated_at)
            VALUES(?,?,'pending',?,?)`,id,unit,now,now);err!=nil {
            return false,fmt.Errorf("cannot durably queue terminal cleanup: %w",err)
        }
    }
    if err:=tx.Commit();err!=nil {return false,err}
    return changed==1,nil
}

// PendingShellUnitCleanup selects at most limit oldest outstanding receipts.
// Each failed attempt moves to the end of the retry queue.
func (s *Store) PendingShellUnitCleanup(ctx context.Context,limit int)([]ShellUnitCleanup,error){
    if limit<1||limit>64 {return nil,errors.New("cleanup page limit must be 1..64")}
    rows,err:=s.db.QueryContext(ctx,`
        SELECT c.job_id,c.unit_name,c.attempts,c.last_error
        FROM shell_unit_cleanup c JOIN jobs j ON c.job_id=j.job_id
        WHERE c.status='pending' AND c.unit_name=j.unit_name
        AND j.tool IN ('shell.exec','job.start','shell.exec_admin')
        AND j.state IN ('done','failed') AND j.exit_code IS NOT NULL
        ORDER BY c.updated_at,c.job_id LIMIT ?`,limit)
    if err!=nil {return nil,err}
    defer rows.Close()
    result:=make([]ShellUnitCleanup,0,limit)
    for rows.Next(){
        var item ShellUnitCleanup
        if err:=rows.Scan(&item.JobID,&item.Unit,&item.Attempts,&item.LastError);err!=nil{return nil,err}
        result=append(result,item)
    }
    return result,rows.Err()
}

// FinishShellUnitCleanup acknowledges a retired unit without changing the
// original terminal job status, exit code, or audit history.
func (s *Store) FinishShellUnitCleanup(ctx context.Context,id,unit string)error{
    res,err:=s.db.ExecContext(ctx,`
        UPDATE shell_unit_cleanup SET status='done',last_error='',updated_at=?
        WHERE job_id=? AND unit_name=? AND status='pending'`,
        time.Now().UnixNano(),id,unit)
    if err!=nil{return err}
    rows,err:=res.RowsAffected()
    if err!=nil{return err}
    if rows!=1{return sql.ErrNoRows}
    return nil
}

func (s *Store) FailShellUnitCleanup(ctx context.Context,id,unit string,reason string)error{
    if len(reason)>300 {reason=reason[:300]}
    res,err:=s.db.ExecContext(ctx,`
        UPDATE shell_unit_cleanup SET attempts=attempts+1,last_error=?,updated_at=?
        WHERE job_id=? AND unit_name=? AND status='pending'`,
        reason,time.Now().UnixNano(),id,unit)
    if err!=nil{return err}
    rows,err:=res.RowsAffected()
    if err!=nil{return err}
    if rows!=1{return sql.ErrNoRows}
    return nil
}
