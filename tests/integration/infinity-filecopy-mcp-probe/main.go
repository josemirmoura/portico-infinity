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
func sha(p string)(string,error){
 f,e:=os.Open(p);if e!=nil{return "",e};defer f.Close()
 h:=sha256.New();if _,e=io.Copy(h,f);e!=nil{return "",e}
 return hex.EncodeToString(h.Sum(nil)),nil
}
func main(){
 run:=os.Getenv("GITHUB_RUN_ID")
 dir:="/opt/infinity-e2e-"+run
 endpoint:="http://127.0.0.1:18081/mcp"
 token:=os.Getenv("INFINITY_E2E_BEARER_TOKEN")
 if os.Getenv("CI")!="true"||os.Getenv("RUNNER_ENVIRONMENT")!="github-hosted"||
   os.Getenv("GITHUB_EVENT_NAME")!="pull_request"||run==""||token==""{panic("disposable CI only")}
 src,dst:=filepath.Join(dir,"real-filecopy-source.gguf"),filepath.Join(dir,"real-filecopy-destination.gguf")
 stat,e:=os.Stat(src);if e!=nil||!stat.Mode().IsRegular()||stat.Size()!=128<<20 {panic("bad synthetic source")}
 if _,e=os.Lstat(dst);!os.IsNotExist(e){panic("destination already exists")}
 want,e:=sha(src);if e!=nil{panic(e)}
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
 got,e:=sha(dst);if e!=nil{panic(e)}
 target,e:=os.Stat(dst)
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
}
