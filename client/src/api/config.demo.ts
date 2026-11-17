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

// Fork addition: the HTTP settings check, as a demo server would answer it
function httpCheck(values) {
  return Promise.resolve({
    "status": "OK",
    "data": {
      "you": { "address": "192.168.1.5", "local": true, "throughProxy": false },
      "server": { "addresses": [{ "ip": "192.168.1.10", "network": "192.168.1.0/24" }], "inDocker": false },
      "ports": [{ "port": Number(values.httpPort) || 0, "state": "cosmos" }, { "port": Number(values.httpsPort) || 0, "state": "cosmos" }],
      "whitelist": { "passes": true, "entries": [] },
    }
  });
}

// Fork addition: what the server would propose for a mail domain
function detectEmail(domain) {
  return new Promise((resolve, reject) => {
    resolve({
      "status": "OK",
      "data": {
        "domain": domain,
        "mx": "found",
        "complete": true,
        "candidates": [
          { "host": "smtp." + domain, "port": 465, "security": "tls", "source": "guess", "check": "ok", "cert": "ok", "auth": ["PLAIN", "LOGIN"] },
          { "host": "smtp." + domain, "port": 587, "security": "starttls", "source": "guess", "check": "ok", "cert": "ok", "auth": ["PLAIN", "LOGIN"] },
        ],
      }
    })
  });
}

function upnpStatus() {
  return new Promise((resolve, reject) => {
    resolve({
      "status": "OK",
      "data": { "enabled": false, "mappings": [], "apps": [], "foreign": [], "foreignListed": false, "reserved": [25, 80, 110, 143, 443, 465, 587, 993, 995] }
    })
  });
}

const upnpRetry = upnpStatus;
const upnpAppPort = upnpStatus; // Fork addition: the demo has no router
const upnpClose = upnpStatus;

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

// Fork addition: the demo publishes no .local names
const localNames = () => Promise.resolve({ status: 'OK', data: { enabled: false, avahi: 'off', names: [], interfaces: [], problems: [], avahiConf: { read: false, allowInterfaces: [], denyInterfaces: [] } } });
const localNamesCheck = localNames;

// Fork addition: the demo has no Cloudflare to talk to
const cloudflareNone = () => Promise.resolve({ ok: false, status: 0, data: null, reason: 'no_answer', detail: '', message: '' });
const cloudflareStatus = () => Promise.resolve({
  ok: true, status: 200, reason: '', detail: '', message: '',
  data: { zones: [], hosts: [], publicPort: 443, portOK: true, allowedPorts: [443], ranges: { trusted: false, builtin: true, date: '', count: 0 } },
});
const cloudflareCheck = cloudflareNone;
// Fork addition: no names for use at home in the demo
const homeNames = () => Promise.resolve({ ok: true, status: 200, reason: '', detail: '', message: '', data: { zones: [], names: [], skipped: [], addressKnown: false, addressFresh: false } });
const homeNamesSet = cloudflareNone;
const homeNamesCheck = cloudflareNone;
// Fork addition: no Turnstile in the demo
const turnstileStatus = cloudflareNone;
const turnstileChange = cloudflareNone;
// Fork addition: no blocks at Cloudflare in the demo
const edgeBlocks = cloudflareNone;
const edgeBlocksChange = cloudflareNone;
const cloudflarePreview = cloudflareNone;
const cloudflareTakeOver = cloudflareNone;
const cloudflareShield = cloudflareNone;
const cloudflarePanel = cloudflareNone;
const cloudflareRanges = cloudflareNone;
const cloudflareOriginPulls = cloudflareNone;
const expose = cloudflareNone; // Fork addition

// Fork addition: the demo orders no certificate
const certificatesStatus = () => Promise.resolve({
  ok: true, status: 200, reason: '', detail: '', message: '',
  data: { ca: { kind: 'production', name: "Let's Encrypt" }, challenges: { http: 'possible', reason: 'ports', setting: '', httpsPort: 443, httpPort: 80 }, certificates: [], hosts: [], canAnswer: [] },
});
const certificatesProviders = cloudflareNone;
const certificatesRenew = cloudflareNone;
const certificatesDomain = cloudflareNone;
const certificatesDelegation = cloudflareNone;
const certificatesSettings = cloudflareNone;

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

// Fork addition: the demo has no Telegram.
function telegramStatus() {
  return new Promise((resolve, reject) => {
    resolve({
      "status": "OK",
      "data": { "enabled": false, "ready": false, "lastSent": null, "lastError": "", "waiting": 0, "held": 0, "pausedUntil": null, "sent": 0 }
    })
  });
}

function telegramTest(telegramConfig) {
  return new Promise((resolve, reject) => {
    resolve({
      "status": "OK",
      "data": { "success": false, "reason": "demo", "message": "The demo sends nothing." }
    })
  });
}

function telegramChats(token) {
  return new Promise((resolve, reject) => {
    resolve({
      "status": "OK",
      "data": { "success": false, "chats": [], "reason": "demo", "message": "The demo asks Telegram nothing." }
    })
  });
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
  detectEmail,
  httpCheck, // Fork addition
  upnpStatus,
  upnpAppPort,
  upnpClose,
  upnpRetry,
  reachability,
  reachabilityCheck,
  localNames,
  localNamesCheck,
  cloudflareStatus,
  homeNames,
  homeNamesSet,
  homeNamesCheck,
  turnstileStatus,
  turnstileChange,
  edgeBlocks,
  edgeBlocksChange,
  cloudflareCheck,
  cloudflarePreview,
  cloudflareTakeOver,
  cloudflareShield,
  cloudflarePanel,
  cloudflareRanges,
  certificatesStatus,
  certificatesProviders,
  certificatesRenew,
  certificatesDomain,
  certificatesDelegation,
  certificatesSettings,
  cloudflareOriginPulls,
  expose,
  serverUpdateStatus,
  checkServerUpdate,
  snapshotsStatus,
  snapshotsRun,
  snapshotsRestore,
  snapshotsCleanup,
  telegramStatus,
  telegramTest,
  telegramChats,
  getDashboard,
  getBackup,
};