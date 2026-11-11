import { Miniflare } from '/app/node_modules/miniflare/dist/src/index.js';
// The upstream Worker supports API_USER_NAME/API_SECRET for initial login.
// Server creation, Agent ingestion and reads all use its original APIs.
const mf = new Miniflare({
  host: '0.0.0.0', port: 8787,
  modules: true, scriptPath: '/app/worker.mjs',
  compatibilityDate: '2024-12-01', compatibilityFlags: ['nodejs_compat'],
  name: 'cfsm-lab', resourcePersistencePath: '/data',
  d1Databases: { DB: '8a0b6ddf-303c-4090-a048-650ce31f180b' },
  durableObjects: { METRICS_BROADCASTER: { className: 'MetricsBroadcaster', useSQLite: true } },
  bindings: { API_SECRET: process.env.LAB_SECRET, API_USER_NAME: 'lab', DEBUG: 'false' },
});
await mf.ready;
console.log('Disposable Worker ready');
for (const signal of ['SIGTERM', 'SIGINT']) {
  process.on(signal, async () => { await mf.dispose(); process.exit(0); });
}
