import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import { expect, it } from 'vitest';

const run = promisify(execFile);
const proof = String.raw`const assert = require('node:assert/strict');
const http = require('node:http');
const vm = require('node:vm');
const redirects = require('follow-redirects');
const version = require('follow-redirects/package.json').version;
const listen = server => new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const close = server => new Promise(resolve => { server.closeAllConnections(); server.close(resolve); });
const describe = req => ({ secret: Boolean(req.headers['x-api-key']), harmless: req.headers['x-harmless'] === 'kept', authorization: Boolean(req.headers.authorization), cookie: Boolean(req.headers.cookie) });
(async () => {
  const destination = http.createServer((req, res) => res.end(JSON.stringify(describe(req))));
  const source = http.createServer((req, res) => {
    if (req.url === '/done') return res.end(JSON.stringify(describe(req)));
    res.writeHead(302, { Location: req.url === '/same' ? '/done' : 'http://127.0.0.1:' + destination.address().port + '/done' });
    res.end();
  });
  await listen(destination); await listen(source);
  const results = [];
  try {
    for (const mode of ['same-realm', 'cross-realm', 'cross-realm-uppercase', 'same-origin']) {
      const names = mode === 'cross-realm-uppercase' ? ['X-API-KEY'] : ['x-api-key'];
      const sensitiveHeaders = mode === 'same-realm' ? names : vm.runInNewContext(JSON.stringify(names));
      const result = await new Promise((resolve, reject) => {
        const request = redirects.http.get('http://127.0.0.1:' + source.address().port + (mode === 'same-origin' ? '/same' : '/cross'), {
          sensitiveHeaders, headers: { 'X-API-Key': 'test-only-secret', 'X-Harmless': 'kept', Authorization: 'Bearer test-only', Cookie: 'test=only' }
        }, response => { let data = ''; response.setEncoding('utf8'); response.on('data', chunk => data += chunk); response.on('end', () => resolve(JSON.parse(data))); });
        request.on('error', reject); request.setTimeout(5000, () => request.destroy(new Error('local redirect timeout')));
      });
      results.push({ mode, ...result });
      assert.equal(result.harmless, true);
      if (mode !== 'same-origin') { assert.equal(result.authorization, false); assert.equal(result.cookie, false); }
      else assert.equal(result.secret, true);
    }
    console.log(JSON.stringify({ version, results }));
  } finally { await close(source); await close(destination); }
})().catch(error => { console.error(error); process.exitCode = 1; });`;

it('strips cross-origin credentials for cross-realm sensitive-header arrays', async () => {
  const { stdout } = await run(process.execPath, ['-e', proof], {
    cwd: fileURLToPath(new URL('../../', import.meta.url)),
    timeout: 15000,
    maxBuffer: 16384,
  });
  const result = JSON.parse(stdout) as {
    results: {
      mode: string;
      secret: boolean;
      harmless: boolean;
      authorization: boolean;
      cookie: boolean;
    }[];
  };
  expect(result.results).toHaveLength(4);
  for (const row of result.results) {
    expect(row.harmless, row.mode).toBe(true);
    expect(row.secret, row.mode).toBe(row.mode === 'same-origin');
    expect(row.authorization, row.mode).toBe(row.mode === 'same-origin');
    expect(row.cookie, row.mode).toBe(row.mode === 'same-origin');
  }
}, 20000);
