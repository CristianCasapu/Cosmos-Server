import configDemo from './demo.config.json';

interface Route {
  Name: string;
}

type Operation = 'replace' | 'move_up' | 'move_down' | 'delete' | 'add';

function get() {
  return new Promise((resolve, reject) => {
    resolve(configDemo)
  });
}

function set(values) {
  return new Promise((resolve, reject) => {
    resolve({
      "status": "ok",
    })
  });
}

function restart() {
  return new Promise((resolve, reject) => {
    resolve({
      "status": "ok",
    })
  });
}

function testEmail(emailConfig, recipient = '') {
  return new Promise((resolve, reject) => {
    resolve({
      "status": "OK",
      "data": {
        "success": true,
        "recipient": recipient || emailConfig.From,
        "steps": [],
      }
    })
  });
}

function upnpStatus() {
  return new Promise((resolve, reject) => {
    resolve({
      "status": "OK",
      "data": { "enabled": false, "mappings": [] }
    })
  });
}

const upnpRetry = upnpStatus;

// Fork addition: the demo has no server to check from
function reachability() {
  return new Promise((resolve) => {
    resolve({
      "status": "OK",
      "data": { "routes": [], "publicIPSource": "", "checkedAt": new Date().toISOString() }
    })
  });
}

const reachabilityCheck = reachability;

// Fork addition: the demo has no Cloudflare to talk to
const cloudflareNone = () => Promise.resolve({ ok: false, status: 0, data: null, reason: 'no_answer', detail: '', message: '' });
const cloudflareStatus = () => Promise.resolve({
  ok: true, status: 200, reason: '', detail: '', message: '',
  data: { zones: [], hosts: [], publicPort: 443, portOK: true, allowedPorts: [443], ranges: { trusted: false, builtin: true, date: '', count: 0 } },
});
const cloudflareCheck = cloudflareNone;
const cloudflarePreview = cloudflareNone;
const cloudflareTakeOver = cloudflareNone;
const cloudflareShield = cloudflareNone;
const cloudflarePanel = cloudflareNone;
const cloudflareRanges = cloudflareNone;

function serverUpdateStatus() {
  return new Promise((resolve, reject) => {
    resolve({
      "status": "OK",
      "data": { "currentVersion": "demo", "latestVersion": "demo", "updateAvailable": false, "autoUpdate": true, "maintenanceTime": "02:00", "checkedAt": new Date().toISOString() }
    })
  });
}

const checkServerUpdate = serverUpdateStatus;

function snapshotsStatus() {
  return new Promise((resolve, reject) => {
    resolve({
      "status": "OK",
      "data": { "enabled": false, "destination": "", "running": false, "current": "", "filesDone": 0, "startedAt": null, "sources": [], "suggested": [] }
    })
  });
}

const snapshotsRun = snapshotsStatus;

function snapshotsRestore(source, snapshot) {
  return snapshotsStatus();
}

function snapshotsCleanup(source, name) {
  return snapshotsStatus();
}

function canSendEmail() {
  return new Promise((resolve, reject) => {
    resolve({
      "status": "ok",
      "data": {
        "canSendEmail": true,
      }
    })
  });
}

// the route forms read res.status
async function rawUpdateRoute(routeName: string, operation: Operation, newRoute?: Route): Promise<{ status: string }> {
  return new Promise((resolve, reject) => {
    resolve({ status: "OK" })
  });
}

async function replaceRoute(routeName: string, newRoute: Route): Promise<{ status: string }> {
  return rawUpdateRoute(routeName, 'replace', newRoute);
}

async function moveRouteUp(routeName: string): Promise<{ status: string }> {
  return rawUpdateRoute(routeName, 'move_up');
}

async function moveRouteDown(routeName: string): Promise<{ status: string }> {
  return rawUpdateRoute(routeName, 'move_down');
}

async function deleteRoute(routeName: string): Promise<{ status: string }> {
  return rawUpdateRoute(routeName, 'delete');
}
async function addRoute(newRoute: Route): Promise<{ status: string }> {
  return rawUpdateRoute("", 'add', newRoute);
}

async function newRoute(routeName: string, route: Route): Promise<{ status: string }> {
  return rawUpdateRoute(routeName, 'add', route);
}

async function updateRouteByName(routeName: string, route: Route): Promise<{ status: string }> {
  return rawUpdateRoute(routeName, 'replace', route);
}

async function deleteRouteByName(routeName: string): Promise<{ status: string }> {
  return rawUpdateRoute(routeName, 'delete');
}

function updateDNS(values) {
  return Promise.resolve({ status: "OK" });
}

function getDashboard() {
  return new Promise((resolve, reject) => {
    const routes = configDemo.data.HTTPConfig.ProxyConfig.Routes;
    const dashboardRoutes = routes.map((route: any) => ({
      Name: route.Name,
      Description: route.Description,
      Target: route.Target,
      Mode: route.Mode,
      Host: route.Host,
      UseHost: route.UseHost,
      UsePathPrefix: route.UsePathPrefix,
      PathPrefix: route.PathPrefix,
      Icon: route.Icon || "",
      HideFromDashboard: route.HideFromDashboard || false,
      ContainerRunning: route.Mode === "SERVAPP" ? true : false,
      ContainerIcon: "",
    }));
    resolve({
      status: "OK",
      data: dashboardRoutes,
    });
  });
}

function getBackup() {
  return new Promise((resolve, reject) => {
    resolve({
      status: "OK",
      data: {},
    });
  });
}

export {
  get,
  set,
  restart,
  rawUpdateRoute,
  replaceRoute,
  moveRouteUp,
  moveRouteDown,
  deleteRoute,
  addRoute,
  newRoute,
  updateRouteByName,
  deleteRouteByName,
  updateDNS,
  canSendEmail,
  testEmail,
  upnpStatus,
  upnpRetry,
  reachability,
  reachabilityCheck,
  cloudflareStatus,
  cloudflareCheck,
  cloudflarePreview,
  cloudflareTakeOver,
  cloudflareShield,
  cloudflarePanel,
  cloudflareRanges,
  serverUpdateStatus,
  checkServerUpdate,
  snapshotsStatus,
  snapshotsRun,
  snapshotsRestore,
  snapshotsCleanup,
  getDashboard,
  getBackup,
};