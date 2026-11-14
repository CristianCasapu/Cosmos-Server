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

  // Fork addition: proposals for the SMTP settings of a mail domain (src/maildetect).
  // Only the domain is sent: the mailbox name stays in the browser. The form
  // shows the outcome itself, so the error is not announced a second time.
  function detectEmail(domain) {
    return wrap(apiFetch('/cosmos/api/config/email-detect', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ address: domain }),
    }), true)
  }

  // Fork addition: UPnP port forwarding status; retry re-applies the saved settings now.
  function upnpStatus(quiet = false) {
    return wrap(apiFetch('/cosmos/api/upnp', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }), quiet)
  }

  // Fork addition: open or close a published port of a container on the
  // router (src/upnp/apps.go). Opening is confirmed with the name of the app
  // and the port; the answer is the new status.
  function upnpAppPort(body: { container: string, port: number, protocol: string, enabled: boolean, externalPort?: number, scheme?: string, confirm?: string }) {
    return wrap(apiFetch('/cosmos/api/upnp/apps', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(body),
    }))
  }

  // Fork addition: close one of Cosmos' own ports on the router ("HTTPS" or "HTTP").
  function upnpClose(name: string) {
    return wrap(apiFetch('/cosmos/api/upnp/close', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ name }),
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

  // Fork addition: are the .local names published and answered
  // (src/proxy/fork_mdns_status.go). Admins only; GET stays quiet on errors,
  // POST asks the network again.
  function localNames() {
    return wrap(apiFetch('/cosmos/api/local-names', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }), true)
  }

  function localNamesCheck() {
    return wrap(apiFetch('/cosmos/api/local-names', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ check: true }),
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

  // Fork addition: the guided Cloudflare integration (src/cloudflare). These
  // calls never throw: the screens show Cloudflare's refusals themselves, in
  // plain words, so the answer comes back as { ok, data, reason, detail,
  // message } whatever the status was.
  function cloudflareCall(path: string, method: string, body?: any) {
    return forkCall('/cosmos/api/cloudflare' + path, method, body);
  }

  // Fork addition: the same for the certificates (src/certs).
  function certificatesCall(path: string, method: string, body?: any) {
    return forkCall('/cosmos/api/certificates' + path, method, body);
  }

  function forkCall(url: string, method: string, body?: any) {
    return apiFetch(url, {
      method,
      headers: {
        'Content-Type': 'application/json'
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    }).then(async (response) => {
      let answer: any = {};
      try {
        answer = JSON.parse(await response.text());
      } catch (e) {
        answer = {};
      }
      return {
        ok: response.status === 200 && answer.status === 'OK',
        status: response.status,
        data: answer.data || null,
        reason: answer.reason || '',
        detail: answer.detail || '',
        message: answer.message || '',
      };
    }).catch(() => ({ ok: false, status: 0, data: null, reason: 'no_answer', detail: '', message: '' }));
  }

  const cloudflareStatus = () => cloudflareCall('', 'GET');

  // Fork addition: certificates (src/certs)
  const certificatesStatus = () => certificatesCall('', 'GET');
  const certificatesProviders = () => certificatesCall('/providers', 'GET');
  const certificatesRenew = (domain: string) => certificatesCall('/renew', 'POST', { domain });
  const certificatesDomain = (domain: string) => certificatesCall('/domain', 'POST', { domain });
  const certificatesDelegation = (domain: string, via: string) => certificatesCall('/delegation', 'POST', { domain, via });
  const certificatesSettings = (httpChallenge: string) => certificatesCall('/settings', 'POST', { httpChallenge });
  // Fork addition: expose an app to the internet, or withdraw that (src/cloudflare/expose.go); answers like the calls above
  const expose = (body: any) => forkCall('/cosmos/api/expose', 'POST', body);
  const cloudflareCheck = (token: string, zone = '') => cloudflareCall('/check', 'POST', { token, zone });
  const cloudflarePreview = (token: string, zone: string, panel = '', takeOver: string[] = []) =>
    cloudflareCall('/preview', 'POST', { token, zone, panel, takeOver });
  const cloudflareTakeOver = (names: string[], zone = '') => cloudflareCall('/takeover', 'POST', { names, zone });
  const cloudflareShield = (hostname: string, shielded: boolean) => cloudflareCall('/shield', 'POST', { hostname, shielded });
  const cloudflarePanel = (hostname: string) => cloudflareCall('/panel', 'POST', { hostname });
  const cloudflareRanges = (action: string) => cloudflareCall('/ranges', 'POST', { action });
  // Authenticated Origin Pulls: one route, the step is the body's "action"
  const cloudflareOriginPulls = (body: any) => cloudflareCall('/origin-pulls', 'POST', body);

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

  // Fork addition: Fail2ban on the server (src/fail2ban). GET = whether it is
  // installed and running, the fork's jails, the banned addresses and the
  // settings; unban lifts one ban (jail "" = every jail of the fork);
  // settings writes the thresholds and the never-ban list.
  function fail2banStatus() {
    return wrap(apiFetch('/cosmos/api/fail2ban', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  function fail2banUnban(ip: string, jail: string) {
    return wrap(apiFetch('/cosmos/api/fail2ban/unban', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ ip, jail }),
    }))
  }

  function fail2banSettings(settings: { jails: Record<string, { maxRetry: number, findTime: number, banTime: number }>, ignoreIP: string[] }) {
    return wrap(apiFetch('/cosmos/api/fail2ban/settings', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(settings),
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
    detectEmail,
    serverUpdateStatus,
    checkServerUpdate,
    upnpStatus,
    upnpAppPort,
    upnpClose,
    upnpRetry,
    reachability,
    reachabilityCheck,
    localNames, // Fork addition: the .local names
    localNamesCheck,
    securityAudit, // Fork addition: user audit panel
    securityUserSummary,
    securityMyActivity,
    cloudflareStatus,
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
    snapshotsStatus,
    snapshotsRun,
    snapshotsRestore,
    snapshotsCleanup,
    telegramStatus,
    telegramTest,
    fail2banStatus,
    fail2banUnban,
    fail2banSettings,
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
