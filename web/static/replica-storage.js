// Each session replica owns a pool: SQLite's SAH handles lock the whole pool,
// even when workers open different database files. Keep legacy files as a
// recovery copy when moving a replica out of the former shared pool.
self.zziraReplicaStorage = async function (sqlite3, replica, boot = () => {}) {
  const filename = `/zzira-${replica}.db`;
  async function install(options) {
    for (let attempt = 1; ; attempt++) {
      try {
        return await sqlite3.installOpfsSAHPoolVfs({
          ...options, forceReinitIfPreviouslyFailed: attempt > 1,
        });
      } catch (error) {
        if (attempt >= 12) throw error;
        boot(`boot: pool install retry ${attempt} (${error?.message ?? error})`);
        await new Promise(resolve => setTimeout(resolve, 500));
      }
    }
  }
  const pool = await install({ name: `zzira-replica-${replica}`, directory: `/.zzira-replicas/${replica}` });
  await pool.reserveMinimumCapacity(pool.getFileCount() + 2);
  if (pool.getFileNames().includes(filename)) return pool;

  const root = await navigator.storage.getDirectory();
  try {
    await root.getDirectoryHandle('.opfs-sahpool');
  } catch (error) {
    if (error.name === 'NotFoundError') return pool;
    throw error;
  }
  async function migrate() {
    const legacy = await install({ name: 'opfs-sahpool' });
    try {
      if (!legacy.getFileNames().includes(filename)) return;
      await legacy.reserveMinimumCapacity(legacy.getFileCount() + 2);
      // Opening before export lets SQLite recover any hot rollback journal
      // left by a terminated worker. The copied database includes its outbox.
      const db = new legacy.OpfsSAHPoolDb(filename);
      try { db.exec('SELECT count(*) FROM sqlite_master'); } finally { db.close(); }
      pool.importDb(filename, legacy.exportFile(filename));
      boot('boot: legacy replica copied; original retained');
    } finally {
      legacy.pauseVfs();
    }
  }
  if (navigator.locks) {
    await navigator.locks.request('zzira-opfs-legacy-migration', { mode: 'exclusive' }, migrate);
  } else {
    await migrate();
  }
  return pool;
};
