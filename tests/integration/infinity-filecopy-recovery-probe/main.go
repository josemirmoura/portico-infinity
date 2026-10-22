// Disposable MCP → Broker restart → systemd worker reconciliation probe.
// This program refuses to run outside pull_request CI on a GitHub-hosted runner.
package main

import (
    "context"
    "encoding/json"
    "errors"
    "flag"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "regexp"
    "strings"
    "time"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
    sourceLeaf = "real-filecopy-cancel-source.gguf"
    destLeaf = "real-filecopy-recovery-destination.gguf"
    recordLeaf = "real-filecopy-recovery-job.id"
    chunk = 64 << 20
)

var validID = regexp.MustCompile("^vps-agent-filecopy-[0-9a-f]{32}$")

type bearer struct { token string }
func (b bearer) RoundTrip(r *http.Request) (*http.Response,error) {
    request:=r.Clone(r.Context())
    request.Header.Set("Authorization","Bearer "+b.token)
    return http.DefaultTransport.RoundTrip(request)
}

func require(ok bool, msg string) {
    if !ok { panic("FILECOPY RECOVERY E2E: "+msg) }
}

func readID(root *os.Root) string {
    f,err:=root.Open(recordLeaf)
    if err!=nil {panic(err)}
    defer f.Close()
    data,err:=io.ReadAll(io.LimitReader(f,100))
    if err!=nil {panic(err)}
    id:=strings.TrimSpace(string(data))
    require(validID.MatchString(id),"invalid saved job ID")
    return id
}

func decoded(r *mcp.CallToolResult) (map[string]any,error) {
    if r==nil||r.IsError {return nil,errors.New("tool returned an MCP error")}
    data,err:=json.Marshal(r.StructuredContent)
    if err!=nil{return nil,err}
    var result map[string]any
    if err:=json.Unmarshal(data,&result);err!=nil{return nil,err}
    return result,nil
}

func main() {
    phase:=flag.String("phase","","start, check or cancel")
    flag.Parse()
    require(*phase=="start"||*phase=="check"||*phase=="cancel","unrecognized phase")
    runID:=os.Getenv("GITHUB_RUN_ID")
    require(os.Getenv("CI")=="true" &&
        os.Getenv("GITHUB_EVENT_NAME")=="pull_request" &&
        os.Getenv("RUNNER_ENVIRONMENT")=="github-hosted",
        "restricted to GitHub-hosted pull_request CI")
    require(len(runID)>0&&len(runID)<25,"missing or oversized run ID")
    for _,c:=range runID {
        require(c>='0'&&c<='9',"non-numeric run ID")
    }
    token:=os.Getenv("INFINITY_E2E_BEARER_TOKEN")
    require(len(token)>39,"disposable bearer is missing")
    rootPath:="/opt/infinity-e2e-"+runID
    root,err:=os.OpenRoot(rootPath)
    if err!=nil {panic(err)}
    defer root.Close()
    in,err:=root.Stat(sourceLeaf)
    require(err==nil&&in.Mode().IsRegular()&&in.Size()==8<<30,
        "missing synthetic 8 GiB source")
    _,err=root.Stat(destLeaf)
    require(os.IsNotExist(err),"recovery destination must be absent")

    ctx,cancel:=context.WithTimeout(context.Background(),90*time.Second)
    defer cancel()
    client:=mcp.NewClient(&mcp.Implementation{Name:"infinity-filecopy-recovery-e2e",Version:"1"},nil)
    session,err:=client.Connect(ctx,&mcp.StreamableClientTransport{
        Endpoint:"http://127.0.0.1:18081/mcp",
        HTTPClient:&http.Client{Transport:bearer{token},Timeout:15*time.Second},
    },nil)
    if err!=nil{panic(err)}
    defer session.Close()
    call:=func(tool string,args map[string]any)(map[string]any,error){
        r,err:=session.CallTool(ctx,&mcp.CallToolParams{Name:tool,Arguments:args})
        if err!=nil{return nil,err}
        return decoded(r)
    }
    jobArgs:=map[string]any{
        "path":filepath.Join(rootPath,sourceLeaf),
        "destination":filepath.Join(rootPath,destLeaf),
        "max_bytes":int64(9<<30),
        "operation_id":"filecopy-recovery-"+runID,
    }
    if *phase=="start" {
        r,err:=call("file.copy.start",jobArgs)
        if err!=nil{panic(fmt.Errorf("start rejected: %w",err))}
        id,ok:=r["job_id"].(string)
        require(ok&&validID.MatchString(id),"unrecognized MCP job identifier")
        // Persist only after worker has produced a real durable checkpoint.
        observed:=false
        for until:=time.Now().Add(75*time.Second);time.Now().Before(until);{
            p,err:=call("job.status",map[string]any{"job_id":id})
            if err==nil {
                state,_:=p["state"].(string)
                done,_:=p["bytes_done"].(float64)
                if done>=float64(chunk)&&state=="running" {observed=true;break}
                require(state!="done"&&state!="failed"&&state!="cancelled",
                    fmt.Sprintf("job terminal before recovery fixture: %v",p))
            }
            time.Sleep(70*time.Millisecond)
        }
        require(observed,"no running worker with >=64MiB checkpoint")
        f,err:=root.OpenFile(recordLeaf,os.O_WRONLY|os.O_CREATE|os.O_EXCL,0600)
        if err!=nil{panic(err)}
        _,writeErr:=f.WriteString(id+"\n")
        syncErr:=f.Sync()
        closeErr:=f.Close()
        if writeErr!=nil||syncErr!=nil||closeErr!=nil {
            panic("could not persist recovery job ID")
        }
        fmt.Println("BROKER_RESTART_LIVE_COPY_CHECKPOINT: PASS")
        return
    }
    id:=readID(root)
    current,err:=call("job.status",map[string]any{"job_id":id})
    if err!=nil{panic(err)}
    if *phase=="check" {
        require(current["state"]=="running",
            fmt.Sprintf("job did not survive broker restart: %v",current))
        bytes,_:=current["bytes_done"].(float64)
        require(bytes>=float64(chunk),"restarted broker did not read durable worker progress")
        cached,err:=call("file.copy.start",jobArgs)
        if err!=nil{panic(err)}
        require(cached["job_id"]==id,"broker redispatched idempotent filecopy")
        fmt.Println("BROKER_RESTART_RECONNECTED_JOB_STATUS_AND_REPLAY: PASS")
        return
    }
    // The workflow resumes the frozen systemd unit immediately before this
    // phase. MCP job.cancel must stop precisely the already-running job.
    require(current["state"]=="running",
        fmt.Sprintf("reconnected job unexpectedly terminal: %v",current))
    result,err:=call("job.cancel",map[string]any{"job_id":id})
    if err!=nil{panic(err)}
    require(result["cancelled"]==true,"MCP job.cancel did not confirm stop")
    status,err:=call("job.status",map[string]any{"job_id":id})
    if err!=nil{panic(err)}
    require(status["state"]=="cancelled",
        fmt.Sprintf("reconciled job not cancelled: %v",status))
    _,err=root.Stat(destLeaf)
    require(os.IsNotExist(err),"interrupted job published a destination")
    fmt.Println("BROKER_RESTART_WORKER_CANCEL_NO_PUBLICATION: PASS")
}
