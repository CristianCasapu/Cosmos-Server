import wrap, { type ApiResponse, type ApiFetch } from './wrap';
export interface Route {
  Name: string;
}

export type Operation = 'replace' | 'move_up' | 'move_down' | 'delete' | 'add';

export default function createConfigAPI(apiFetch: ApiFetch) {
  function get() {
    return wrap(apiFetch('/cosmos/api/config', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  function set(values) {
    return wrap(apiFetch('/cosmos/api/config', {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(values),
    }))
  }

  function restart() {
    return apiFetch('/cosmos/api/restart', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    })
  }

  function canSendEmail() {
    return apiFetch('/cosmos/api/can-send-email', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }).then((response) => {
      return response.json();
    });
  }

  // Fork addition: try SMTP settings (saved or not) by sending a test email.
  function testEmail(emailConfig, recipient = '') {
    return wrap(apiFetch('/cosmos/api/config/test-email', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ EmailConfig: emailConfig, Recipient: recipient }),
    }))
  }

  // Fork addition: UPnP port forwarding status; retry re-applies the saved settings now.
  function upnpStatus() {
    return wrap(apiFetch('/cosmos/api/upnp', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  // Fork addition: can the apps be reached from the internet (src/reach). GET
  // answers from the server's cache and stays quiet on errors (a reader
  // without the permission simply gets no answer); POST checks again.
  function reachability() {
    return wrap(apiFetch('/cosmos/api/reachability', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }), true)
  }

  function reachabilityCheck(askPublicIP = false) {
    return wrap(apiFetch('/cosmos/api/reachability', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ askPublicIP: !!askPublicIP }),
    }))
  }

  // Fork addition: the user audit panel (src/security/useraudit*.go). All GET,
  // read only; the query is built by utils/auditFilters.js.
  function securityAudit(query: string, cursor = 0, limit = 50, quiet = false) {
    const params = new URLSearchParams(query || '');
    if (cursor) params.set('cursor', String(cursor));
    params.set('limit', String(limit));
    return wrap(apiFetch('/cosmos/api/security/audit?' + params.toString(), {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }), quiet)
  }

  function securityUserSummary(user: string) {
    return wrap(apiFetch('/cosmos/api/security/user-summary?' + new URLSearchParams({ user }).toString(), {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  function securityMyActivity(cursor = 0, limit = 20) {
    const params = new URLSearchParams({ limit: String(limit) });
    if (cursor) params.set('cursor', String(cursor));
    return wrap(apiFetch('/cosmos/api/security/my-activity?' + params.toString(), {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }), true)
  }

  function upnpRetry() {
    return wrap(apiFetch('/cosmos/api/upnp', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  // Fork addition: is there a newer server release? GET = last known answer,
  // POST = ask again. Neither installs anything.
  function serverUpdateStatus() {
    return wrap(apiFetch('/cosmos/api/server-update', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  function checkServerUpdate() {
    return wrap(apiFetch('/cosmos/api/server-update', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  // Fork addition: incremental snapshots (src/snapshots). GET = settings in
  // use, snapshots and what is running; run and restore start in the
  // background and are followed through the status; cleanup deletes one
  // folder that a restore kept and answers when it is gone.
  function snapshotsStatus() {
    return wrap(apiFetch('/cosmos/api/snapshots', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  function snapshotsRun() {
    return wrap(apiFetch('/cosmos/api/snapshots/run', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  function snapshotsRestore(source: string, snapshot: string) {
    return wrap(apiFetch('/cosmos/api/snapshots/restore', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ Source: source, Snapshot: snapshot }),
    }))
  }

  // Fork addition: notifications through Telegram (src/telegram). GET = how
  // deliveries go (no settings in it); the test sends one message with the
  // settings given, saved or not, and answers with Telegram's verdict in words.
  function telegramStatus() {
    return wrap(apiFetch('/cosmos/api/telegram', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  function telegramTest(telegramConfig) {
    return wrap(apiFetch('/cosmos/api/telegram/test', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ Telegram: telegramConfig }),
    }))
  }

  function snapshotsCleanup(source: string, name: string) {
    return wrap(apiFetch('/cosmos/api/snapshots/cleanup', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ Source: source, Name: name }),
    }))
  }

  async function rawUpdateRoute(routeName: string, operation: Operation, newRoute?: Route): Promise<ApiResponse> {
    const payload = {
      routeName,
      operation,
      newRoute,
    };

    if (operation === 'replace') {
      if (!newRoute) throw new Error('newRoute must be provided for replace operation');
    }

    return wrap(apiFetch('/cosmos/api/config', {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(payload),
    }));
  }

  async function replaceRoute(routeName: string, newRoute: Route): Promise<ApiResponse> {
    return rawUpdateRoute(routeName, 'replace', newRoute);
  }

  async function moveRouteUp(routeName: string): Promise<ApiResponse> {
    return rawUpdateRoute(routeName, 'move_up');
  }

  async function moveRouteDown(routeName: string): Promise<ApiResponse> {
    return rawUpdateRoute(routeName, 'move_down');
  }

  async function deleteRoute(routeName: string): Promise<ApiResponse> {
    return rawUpdateRoute(routeName, 'delete');
  }
  async function addRoute(newRoute: Route): Promise<ApiResponse> {
    return rawUpdateRoute("", 'add', newRoute);
  }

  function getBackup() {
    return wrap(apiFetch('/cosmos/api/get-backup', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  function getDashboard() {
    return wrap(apiFetch('/cosmos/api/dashboard', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  // --- Individual Route CRUD ---
  function listRoutes(): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/routes', {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }))
  }

  function getRoute(name: string): Promise<ApiResponse> {
    return wrap(apiFetch(`/cosmos/api/routes/${encodeURIComponent(name)}`, {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }))
  }

  function createNewRoute(route: Route): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/routes', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(route),
    }))
  }

  function updateRouteByName(name: string, route: Route): Promise<ApiResponse> {
    return wrap(apiFetch(`/cosmos/api/routes/${encodeURIComponent(name)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(route),
    }))
  }

  function deleteRouteByName(name: string): Promise<ApiResponse> {
    return wrap(apiFetch(`/cosmos/api/routes/${encodeURIComponent(name)}`, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
    }))
  }

  function updateDNS(dnsConfig: {
    dnsPort?: string;
    dnsFallback?: string;
    dnsBlockBlacklist?: boolean;
    dnsAdditionalBlocklists?: string[];
    customDNSEntries?: { Type: string; Key: string; Value: string }[];
  }) {
    return wrap(apiFetch('/cosmos/api/config/dns', {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(dnsConfig),
    }))
  }

  return {
    get,
    set,
    restart,
    rawUpdateRoute,
    replaceRoute,
    moveRouteUp,
    moveRouteDown,
    deleteRoute,
    addRoute,
    canSendEmail,
    testEmail,
    serverUpdateStatus,
    checkServerUpdate,
    upnpStatus,
    upnpRetry,
    reachability,
    reachabilityCheck,
    securityAudit, // Fork addition: user audit panel
    securityUserSummary,
    securityMyActivity,
    snapshotsStatus,
    snapshotsRun,
    snapshotsRestore,
    snapshotsCleanup,
    telegramStatus,
    telegramTest,
    getBackup,
    getDashboard,
    updateDNS,
    listRoutes,
    getRoute,
    createNewRoute,
    updateRouteByName,
    deleteRouteByName,
  };
}
