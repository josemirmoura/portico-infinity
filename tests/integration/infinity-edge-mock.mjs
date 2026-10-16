// Disposable-only mock services. No real SRV-IA, identity provider or credentials.
import http from 'node:http';
import http2 from 'node:http2';

const bind = '10.88.88.2';
function respond(backend, req, res) {
  const body = JSON.stringify({
    backend,
    path: req.url,
    host: req.headers.host ?? '',
    xForwardedProto: req.headers['x-forwarded-proto'] ?? '',
    xForwardedPort: req.headers['x-forwarded-port'] ?? '',
  }) + '\n';
  res.writeHead(200, {'content-type': 'application/json'});
  res.end(body);
}
http.createServer((req, res) => respond('gateway', req, res)).listen(18081, bind);
http.createServer((req, res) => respond('login', req, res)).listen(18083, bind);
http2.createServer((req, res) => respond('zitadel-h2c', req, res)).listen(18082, bind);
console.log('INFINITY_EDGE_MOCKS_LISTENING');
