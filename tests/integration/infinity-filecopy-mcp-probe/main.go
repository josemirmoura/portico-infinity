// Genuine localhost MCP to Broker to host systemd and filecopy worker.
// Run only in an isolated, GitHub-hosted CI pull_request runner.
package main
import(
 "context"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "os"
 "path/filepath"
 "strings"
 "time"
 "github.com/modelcontextprotocol/go-sdk/mcp"
)
type bearer struct{ token string }
func (b bearer) RoundTrip(r *http.Request)(*http.Response,error) {
 copy:=r.Clone(r.Context()); copy.Header.Set("Authorization","Bearer "+b.token)
 return http.DefaultTransport.RoundTrip(copy)
}
func decoded(r *mcp.CallToolResult)(map[string]any,error) {
 if r==nil||r.IsError{return nil,fmt.Errorf("missing or denied MCP tool result")}
 raw,e:=json.Marshal(r.StructuredContent);if e!=nil{return nil,e}
 var obj map[string]any
 if e=json.Unmarshal(raw,&obj);e!=nil{return nil,e}
 return obj,nil
}
func sha(root *os.Root, leaf string)(string,error){
 f,e:=root.Open(leaf);if e!=nil{return "",e};defer f.Close()
 h:=sha256.New();if _,e=io.Copy(h,f);e!=nil{return "",e}
 return hex.EncodeToString(h.Sum(nil)),nil
}
func main(){
 run:=os.Getenv("GITHUB_RUN_ID")
 dir:="/opt/infinity-e2e-"+run
 endpoint:="http://127.0.0.1:18081/mcp"
 token:=os.Getenv("INFINITY_E2E_BEARER_TOKEN")
 if os.Getenv("CI")!="true"||os.Getenv("RUNNER_ENVIRONMENT")!="github-hosted"||
   os.Getenv("GITHUB_EVENT_NAME")!="pull_request"||run==""||len(run)>24||token==""{panic("disposable CI only")}
 for _, c:=range run{if c<'0'||c>'9'{panic("non-numeric GitHub run ID")}}
 root,e:=os.OpenRoot(dir);if e!=nil{panic(e)};defer root.Close()
 src,dst:=filepath.Join(dir,"real-filecopy-source.gguf"),filepath.Join(dir,"real-filecopy-destination.gguf")
 sourceHandle,e:=root.Open("real-filecopy-source.gguf");if e!=nil{panic(e)}
 stat,e:=sourceHandle.Stat();_ = sourceHandle.Close()
 if e!=nil||!stat.Mode().IsRegular()||stat.Size()!=128<<20 {panic("bad synthetic source")}
 if _,e=root.Stat("real-filecopy-destination.gguf");!os.IsNotExist(e){panic("destination already exists")}
 want,e:=sha(root,"real-filecopy-source.gguf");if e!=nil{panic(e)}
 ctx,cancel:=context.WithTimeout(context.Background(),150*time.Second);defer cancel()
 client:=mcp.NewClient(&mcp.Implementation{Name:"infinity-filecopy-disposable",Version:"1"},nil)
 session,e:=client.Connect(ctx,&mcp.StreamableClientTransport{
  Endpoint:endpoint,HTTPClient:&http.Client{Timeout:20*time.Second,Transport:bearer{token}},
 },nil);if e!=nil{panic(e)};defer session.Close()
 call:=func(name string,args map[string]any)(*mcp.CallToolResult,error){
  return session.CallTool(ctx,&mcp.CallToolParams{Name:name,Arguments:args})
 }
 params:=map[string]any{"path":src,"destination":dst,"max_bytes":int64(256<<20),
  "expected_sha256":want,"operation_id":"infinity-mcp-filecopy-"+run}
 response,e:=call("file.copy.start",params);if e!=nil{panic(e)}
 started,e:=decoded(response);if e!=nil{panic(fmt.Errorf("file.copy.start: %w",e))}
 id,ok:=started["job_id"].(string)
 if !ok||!strings.HasPrefix(id,"vps-agent-filecopy-")||len(id)!=51{panic("bad worker id")}
 fmt.Println("MCP_FILECOPY_STARTED",id)
 var last map[string]any
 for until:=time.Now().Add(125*time.Second);time.Now().Before(until);{
  r,err:=call("job.status",map[string]any{"job_id":id})
  if err==nil {
   o,e:=decoded(r);if e==nil{
    last=o
    state,_:=o["state"].(string)
    if state=="done"{break}
    if state=="failed"||state=="cancelled"||state=="reconcile_required"{panic(fmt.Errorf("filecopy state: %v",o))}
   }
  }
  time.Sleep(200*time.Millisecond)
 }
 if last==nil||last["state"]!="done"{panic(fmt.Errorf("timed out or uncertain: %v",last))}
 got,e:=sha(root,"real-filecopy-destination.gguf");if e!=nil{panic(e)}
 target,e:=root.Stat("real-filecopy-destination.gguf")
 if e!=nil||target.Size()!=128<<20||got!=want||last["sha256"]!=want{panic("size/hash mismatch")}
 fmt.Println("MCP_BROKER_SYSTEMD_WORKER_SHA: PASS")
 retry,e:=call("file.copy.start",params);if e!=nil{panic(e)}
 again,e:=decoded(retry);if e!=nil||again["job_id"]!=id{panic("idempotent replay failed")}
 fmt.Println("MCP_FILECOPY_IDEMPOTENT_REPLAY: PASS")
 malicious:=map[string]any{"path":src,"destination":"/etc/portico-infinity-should-not-exist",
  "max_bytes":int64(256<<20),"operation_id":"infinity-mcp-outside-"+run}
 denied,e:=call("file.copy.start",malicious)
 if e!=nil||denied==nil||!denied.IsError{panic("outside-scope request was not denied")}
 if _,e=os.Lstat("/etc/portico-infinity-should-not-exist");!os.IsNotExist(e){panic("wrote outside scope")}
 fmt.Println("MCP_FILECOPY_OUTSIDE_ROOT_DENIED: PASS")

 // Independent, long, sparse synthetic file to exercise cancellation through
 // the *MCP job.cancel tool*, not through an operator shell on the host.
 sourceLeaf:="real-filecopy-cancel-source.gguf"
 cancelledLeaf:="real-filecopy-cancel-destination.gguf"
 sparse,e:=root.OpenFile(sourceLeaf,os.O_WRONLY|os.O_CREATE|os.O_EXCL,0600)
 if e!=nil{panic(e)}
 if e=sparse.Truncate(8<<30);e!=nil{_ = sparse.Close();panic(e)}
 if e=sparse.Close();e!=nil{panic(e)}
 if _,e=root.Stat(cancelledLeaf);!os.IsNotExist(e){panic("cancel destination exists")}
 cancelArgs:=map[string]any{"path":filepath.Join(dir,sourceLeaf),
  "destination":filepath.Join(dir,cancelledLeaf),"max_bytes":int64(9<<30),
  "operation_id":"infinity-mcp-filecopy-cancel-"+run}
 cr,e:=call("file.copy.start",cancelArgs);if e!=nil{panic(e)}
 cjob,e:=decoded(cr);if e!=nil{panic(e)}
 cancelID,ok:=cjob["job_id"].(string)
 if !ok||!strings.HasPrefix(cancelID,"vps-agent-filecopy-"){panic("bad cancel job id")}
 var before map[string]any
 for until:=time.Now().Add(80*time.Second);time.Now().Before(until);{
  r,err:=call("job.status",map[string]any{"job_id":cancelID})
  if err==nil{
   p,de:=decoded(r);if de==nil{
    before=p
    phase,_:=p["state"].(string)
    bytes,_:=p["bytes_done"].(float64)
    if bytes>=float64(64<<20) && phase=="running"{break}
    if phase=="done"||phase=="failed"||phase=="cancelled"{
     panic(fmt.Errorf("long copy terminated before cancel: %v",p))
    }
   }
  }
  time.Sleep(100*time.Millisecond)
 }
 if before==nil{panic("no long copy progress")}
 doneBytes,_:=before["bytes_done"].(float64)
 if doneBytes<float64(64<<20){panic(fmt.Errorf("progress checkpoint missing: %v",before))}
 stopped,e:=call("job.cancel",map[string]any{"job_id":cancelID})
 if e!=nil{panic(e)}
 cancelResult,e:=decoded(stopped);if e!=nil{panic(fmt.Errorf("cancel MCP denied: %w",e))}
 if cancelResult["cancelled"]!=true{panic(fmt.Errorf("cancel not confirmed: %v",cancelResult))}
 var cancelled map[string]any
 for until:=time.Now().Add(30*time.Second);time.Now().Before(until);{
  r,err:=call("job.status",map[string]any{"job_id":cancelID})
  if err==nil{
   p,de:=decoded(r);if de==nil{
    cancelled=p
    if p["state"]=="cancelled"{break}
   }
  }
  time.Sleep(100*time.Millisecond)
 }
 if cancelled==nil||cancelled["state"]!="cancelled"{
  panic(fmt.Errorf("durable cancel phase not confirmed: %v",cancelled))
 }
 if _,e=root.Stat(cancelledLeaf);!os.IsNotExist(e){panic("cancelled MCP job published destination")}
 re,e:=call("file.copy.start",cancelArgs);if e!=nil{panic(e)}
 cached,e:=decoded(re);if e!=nil||cached["job_id"]!=cancelID{panic("cancelled operation retried")}
 if _,e=root.Stat(cancelledLeaf);!os.IsNotExist(e){panic("cancel replay published destination")}
 fmt.Println("MCP_FILECOPY_INFLIGHT_CANCEL_AND_SAFE_REPLAY: PASS")
}

