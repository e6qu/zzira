import http from 'node:http';

const host = '127.0.0.1';
const port = 8100;
const appOrigin = new URL(process.env.ZZIRA_URL || 'http://localhost:8080').origin;

const server = http.createServer((request, response) => {
  const requestURL = new URL(request.url || '/', `http://${host}:${port}`);

  if (requestURL.pathname === '/healthz') {
    response.writeHead(204).end();
    return;
  }

  if (requestURL.pathname !== '/simulated-logout') {
    response.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' }).end('Not found');
    return;
  }

  const target = requestURL.searchParams.get('to');
  let targetURL;
  try {
    targetURL = new URL(target || '');
  } catch {
    response.writeHead(400, { 'Content-Type': 'text/plain; charset=utf-8' }).end('Invalid redirect target');
    return;
  }

  if (targetURL.origin !== appOrigin || targetURL.pathname !== '/auth/shauth/logout/complete') {
    response.writeHead(400, { 'Content-Type': 'text/plain; charset=utf-8' }).end('Invalid redirect target');
    return;
  }

  response.writeHead(303, {
    'Cache-Control': 'no-store',
    Location: targetURL.toString(),
  }).end();
});

server.listen(port, host);

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => server.close(() => process.exit(0)));
}
