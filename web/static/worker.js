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

async function requiredAsset(path, label) {
  let lastError;
  for (let attempt = 1; attempt <= 5; attempt++) {
    const controller = new AbortController();
    const deadline = setTimeout(() => controller.abort(), 2_000);
    try {
      const response = await fetch(path, { signal: controller.signal });
      if (!response.ok) throw new Error('HTTP ' + response.status);
      return await response.arrayBuffer();
    } catch (error) {
      lastError = error;
      if (attempt === 5) break;
      const delay = 250 * (2 ** (attempt - 1));
      boot(`${label} retry ${attempt} in ${delay}ms (${error?.message ?? error})`);
      await new Promise(resolve => setTimeout(resolve, delay));
    } finally {
      clearTimeout(deadline);
    }
  }
  throw new Error(`${label} unavailable: ${lastError?.message ?? lastError}`);
}

importScripts('/static/sqlite/sqlite3.js', '/static/wasm/wasm_exec.js');

(async () => {
  try {
    boot('boot: sqlite3.js + wasm_exec.js imported');
    // The Go command runtime is a required part of the local-first client.
    // Start loading it alongside SQLite so an immediate offline transition
    // cannot interrupt a later, serial fetch after the page is usable.
    const goWasm = requiredAsset('/static/zzira-worker.wasm', 'go worker wasm');
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
    let pool;
    for (let attempt = 1; ; attempt++) {
      try {
        pool = await sqlite3.installOpfsSAHPoolVfs({
          forceReinitIfPreviouslyFailed: attempt > 1,
          clearOnInit: attempt > 1, // stale pool files from a killed session
        });
        break;
      } catch (err) {
        if (attempt >= 3) throw err;
        boot('boot: pool install retry ' + attempt + ' (' + (err?.message ?? err) + ')');
        await new Promise(r => setTimeout(r, 500 * attempt));
      }
    }
    sqlite3.oo1.OpfsSAHDb = pool.OpfsSAHPoolDb;
    boot('boot: OPFS SAH pool installed');
    self.sqlite3 = sqlite3;
    boot('boot: instantiating go worker');
    const go = new Go();
    const bytes = await goWasm;
    const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
    go.run(instance);
  } catch (err) {
    self.postMessage({ type: 'error', message: String(err) });
  }
})();
