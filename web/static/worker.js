// ZZIRA worker bootstrap: loads sqlite3-wasm (OPFS SAH pool) and the Go worker
// (sync loop + local renderer), then hands control to Go main().

// Loud failure: forward every worker-level error to the page before anything else.
self.onerror = (msg, src, line) => {
  try { self.postMessage({ type: 'error', message: 'worker: ' + msg + ' (' + src + ':' + line + ')' }); } catch (_) {}
};
self.addEventListener('unhandledrejection', (e) => {
  try { self.postMessage({ type: 'error', message: 'worker rejection: ' + String(e.reason) }); } catch (_) {}
});

const boot = (msg) => { try { self.postMessage({ type: 'info', message: msg }); } catch (_) {} };

importScripts('/static/sqlite/sqlite3.js', '/static/wasm/wasm_exec.js');

(async () => {
  try {
    boot('boot: sqlite3.js + wasm_exec.js imported');
    const sqlite3 = await sqlite3InitModule({
      instantiateWasm(info, receive) {
        (async () => {
          try {
            const resp = await fetch('/static/sqlite/sqlite3.wasm');
            if (!resp.ok) throw new Error('sqlite3.wasm fetch HTTP ' + resp.status);
            const bytes = await resp.arrayBuffer();
            const results = await WebAssembly.instantiate(bytes, info);
            receive(results.instance, results.module);
          } catch (err) {
            boot('sqlite3 wasm FAILED: ' + (err?.message ?? String(err)));
            throw err;
          }
        })();
        return undefined; // emscripten falls back to its own flow if we return nothing
      },
    });
    boot('boot: sqlite3 module ready');
    const pool = await sqlite3.installOpfsSAHPoolVfs();
    boot('install result keys: ' + Object.keys(pool).join(',') + ' | oo1 keys: ' + Object.keys(sqlite3.oo1).join(','));
    sqlite3.oo1.OpfsSAHDb = pool.OpfsSAHPoolDb;
    boot('boot: OPFS SAH pool installed');
    self.sqlite3 = sqlite3;
    boot('boot: instantiating go worker');
    const go = new Go();
    const resp = await fetch('/static/zzira-worker.wasm');
    if (!resp.ok) {
      throw new Error('wasm fetch HTTP ' + resp.status + ' for /static/zzira-worker.wasm');
    }
    const bytes = await resp.arrayBuffer();
    const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
    go.run(instance);
  } catch (err) {
    self.postMessage({ type: 'error', message: String(err) });
  }
})();
