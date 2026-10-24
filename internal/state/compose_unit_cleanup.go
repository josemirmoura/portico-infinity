package state

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "time"
)

type ComposeUnitCleanup struct{
    JobID string
    Unit string
    Attempts int
    LastError string
}

func (s *Store) CompleteComposeJobAndQueueCleanup(
    ctx context.Context,id,terminal,detail string,exitCode *int,
)(bool,error){
    if terminal!="done"&&terminal!="failed" {
        return false,errors.New("Compose cleanup requires proven terminal state")
    }
    if exitCode==nil {
        return false,errors.New("Compose cleanup requires proven exit code")
    }
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return false,err}
    defer tx.Rollback()
    var tool,unit string
    if err:=tx.QueryRowContext(ctx,`SELECT tool,unit_name FROM jobs WHERE job_id=?`,id).
        Scan(&tool,&unit);err!=nil{return false,err}
    switch tool {
    case "compose.pull","compose.up","compose.down":
    default:return false,errors.New("non-Compose job cannot queue Compose cleanup")
    }
    if unit==""{return false,errors.New("Compose job missing unit")}
    now:=time.Now().UnixNano()
    res,err:=tx.ExecContext(ctx,`
        UPDATE jobs SET state=?,detail=?,exit_code=?,updated_at=? WHERE job_id=?
        AND state IN ('starting','running','active','activating','reloading','deactivating','cancelling')
    `,terminal,detail,*exitCode,now,id)
    if err!=nil{return false,err}
    affected,err:=res.RowsAffected()
    if err!=nil{return false,err}
    if affected==1 {
        if _,err:=tx.ExecContext(ctx,`
            INSERT INTO compose_unit_cleanup(job_id,unit_name,status,created_at,updated_at)
            VALUES(?,?,'pending',?,?)`,id,unit,now,now);err!=nil {
            return false,fmt.Errorf("cannot queue Compose cleanup atomically: %w",err)
        }
    }
    if err:=tx.Commit();err!=nil{return false,err}
    return affected==1,nil
}

func (s *Store) PendingComposeUnitCleanup(ctx context.Context,limit int)([]ComposeUnitCleanup,error){
    if limit<1||limit>64{return nil,errors.New("Compose cleanup page must be 1..64")}
    rows,err:=s.db.QueryContext(ctx,`
        SELECT c.job_id,c.unit_name,c.attempts,c.last_error
        FROM compose_unit_cleanup c JOIN jobs j ON c.job_id=j.job_id
        WHERE c.status='pending' AND c.unit_name=j.unit_name
          AND j.tool IN ('compose.pull','compose.up','compose.down')
          AND j.state IN ('done','failed') AND j.exit_code IS NOT NULL
        ORDER BY c.updated_at,c.job_id LIMIT ?`,limit)
    if err!=nil{return nil,err}
    defer rows.Close()
    out:=make([]ComposeUnitCleanup,0,limit)
    for rows.Next(){
        var c ComposeUnitCleanup
        if err:=rows.Scan(&c.JobID,&c.Unit,&c.Attempts,&c.LastError);err!=nil{return nil,err}
        out=append(out,c)
    }
    return out,rows.Err()
}
func (s *Store) FinishComposeUnitCleanup(ctx context.Context,id,unit string)error{
    res,err:=s.db.ExecContext(ctx,`
        UPDATE compose_unit_cleanup SET status='done',last_error='',updated_at=?
        WHERE job_id=? AND unit_name=? AND status='pending'
    `,time.Now().UnixNano(),id,unit)
    if err!=nil{return err}
    affected,err:=res.RowsAffected()
    if err!=nil{return err}
    if affected!=1{return sql.ErrNoRows}
    return nil
}
func (s *Store) FailComposeUnitCleanup(ctx context.Context,id,unit string)error{
    res,err:=s.db.ExecContext(ctx,`
        UPDATE compose_unit_cleanup
        SET attempts=attempts+1,last_error='unit not yet confirmed retired',updated_at=?
        WHERE job_id=? AND unit_name=? AND status='pending'
    `,time.Now().UnixNano(),id,unit)
    if err!=nil{return err}
    affected,err:=res.RowsAffected()
    if err!=nil{return err}
    if affected!=1{return sql.ErrNoRows}
    return nil
}

// ListActiveComposeJobsPage limits the SELECT itself; no global materialized
// job list, and no nested query before rows are closed (SQLite pool size=1).
func (s *Store) ListActiveComposeJobsPage(
    ctx context.Context,afterCreated time.Time,afterID string,createdBefore time.Time,limit int,
)([]JobRecord,error){
    if limit<1||limit>64{return nil,errors.New("Compose page must be 1..64")}
    if createdBefore.IsZero(){return nil,errors.New("cutoff is required")}
    var cursor int64
    if !afterCreated.IsZero(){cursor=afterCreated.UnixNano()}
    rows,err:=s.db.QueryContext(ctx,`
        SELECT job_id FROM jobs
        WHERE tool IN ('compose.pull','compose.up','compose.down')
          AND state IN ('starting','running','active','activating','reloading','deactivating','cancelling')
          AND created_at <= ?
          AND (created_at>? OR (created_at=? AND job_id>?))
        ORDER BY created_at,job_id LIMIT ?`,
        createdBefore.UnixNano(),cursor,cursor,afterID,limit)
    if err!=nil{return nil,err}
    ids:=make([]string,0,limit)
    for rows.Next(){
        var id string
        if err:=rows.Scan(&id);err!=nil{_ = rows.Close();return nil,err}
        ids=append(ids,id)
    }
    if err:=rows.Err();err!=nil{_ = rows.Close();return nil,err}
    if err:=rows.Close();err!=nil{return nil,err}
    out:=make([]JobRecord,0,len(ids))
    for _,id:=range ids{
        rec,err:=s.GetJob(ctx,id)
        if errors.Is(err,sql.ErrNoRows){continue}
        if err!=nil{return nil,err}
        out=append(out,rec)
    }
    return out,nil
}
