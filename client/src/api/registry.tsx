import wrap, { type ApiResponse, type ApiFetch } from './wrap';

// Native package registry (Pro). One registry = typed storage + the endpoint publishing it (host, visibility, tokens).

export type RegistryType = 'docker' | 'npm' | 'static' | 'generic' | 'pypi';
export type RegistryBackend = 'seaweedfs' | 's3' | 'local';

// secretKey is write-only: every read endpoint blanks it.
export interface RegistryStorage {
  backend: RegistryBackend;
  seaweedfs?: string;
  bucket?: string;
  endpoint?: string;
  accessKey?: string;
  secretKey?: string;
  region?: string;
  path?: string;
}

// Best-effort rollup maintained by CAS increments and a GC sweep: fine for quota checks, not accounting.
export interface RegistryStats {
  sizeBytes: number;
  packageCount: number;
  versionCount: number;
  pullCount: number;
  pushCount: number;
  updatedAt?: string;
}

// Only the suffix is kept for display — the raw token exists once, in the mint response.
export interface RegistryToken {
  name: string;
  tokenSuffix?: string;
  scopes?: string[];
  expiresAt?: string;
  lastUsedAt?: string;
  createdAt?: string;
}

export interface RegistryStatus {
  name: string;
  type: RegistryType;
  storage: RegistryStorage;
  quotaBytes: number;
  // Set on a registry a feature created for itself (kind "ci": the CI artifact store).
  managedByKind?: string;
  managedByName?: string;
  // The endpoint. Empty host for a static registry: its sites carry the URLs.
  host: string;
  internal: boolean;
  allowAnonymousPull: boolean;
  tags: string[];
  tokens?: RegistryToken[];
  // The user-facing half of the serving route (URL tab).
  route?: any;
  status: string;
  createdAt?: string;
  // Derived from node heartbeats by the read endpoints, never stored.
  servingNodes: string[];
  stats: RegistryStats;
}

export interface RegistryDeployment {
  version: string;
  digest: string;
  size: number;
  active: boolean;
  createdAt?: string;
}

export interface RegistryStaticSite {
  registry: string;
  name: string;
  // Empty means the site exists but is not published anywhere yet.
  host: string;
  internal: boolean;
  spa: boolean;
  tags: string[];
  active: string;
  versions: RegistryDeployment[];
  createdAt?: string;
  updatedAt?: string;
  totalBytes?: number;
}

export interface RegistryGenericFile {
  name: string;
  digest: string;
  size: number;
  contentType?: string;
  sha1?: string;
  sha512?: string;
  md5?: string;
}

// One version of any package type: a docker manifest (version = its digest,
// files = manifest + config + layers), an npm version (one tarball), a pypi
// release or a generic version (the files uploaded into it).
export interface RegistryGenericVersion {
  version: string;
  latest: boolean;
  // The pointers resolving to this version: OCI tags, npm dist-tags. Absent for generic and pypi.
  tags?: string[];
  files: RegistryGenericFile[];
  size: number;
  // Protocol extras: kind/mediaType (docker), description/deprecated (npm), summary/requires_python (pypi).
  props?: Record<string, string>;
  createdAt?: string;
}

export interface RegistryGenericPackage {
  registry: string;
  type: RegistryType;
  name: string;
  // The version "latest" resolves to; empty once every version is deleted. A manifest digest for docker.
  latest: string;
  // The whole pointer map, docker (tag -> digest) and npm (dist-tag -> version) only.
  tags?: Record<string, string>;
  versions: RegistryGenericVersion[];
  createdAt?: string;
  updatedAt?: string;
  totalBytes?: number;
}

export interface RegistryGenericUploadResult {
  package: RegistryGenericPackage;
  version: string;
  files: string[];
}

export interface RegistryDeleteResult {
  deleted: string;
  dataPurged: boolean;
  metadataDeleted: number;
  teardownNotes?: string[];
}

export interface RegistryTokenMintResult {
  token: string;
  registry: RegistryStatus;
}

export interface RegistryStaticUploadResult {
  site: RegistryStaticSite;
  version: string;
  activated: boolean;
}

export default function createRegistryAPI(apiFetch: ApiFetch) {
  const base = '/cosmos/api/constellation/registries';

  // --- registries -----------------------------------------------------------

  function list(): Promise<ApiResponse<RegistryStatus[]>> {
    return wrap(apiFetch(base, {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }));
  }

  function get(name: string): Promise<ApiResponse<RegistryStatus>> {
    return wrap(apiFetch(base + '/' + name, {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }));
  }

  // type is immutable afterwards: the metadata namespace and protocol semantics hang off it.
  // host is required for every type but static (refused there); empty tags = every node serves it.
  function create(values: {
    name: string;
    type: RegistryType;
    quotaBytes?: number;
    storage: RegistryStorage;
    host?: string;
    internal?: boolean;
    allowAnonymousPull?: boolean;
    tags?: string[];
    // The whole user-facing route (URL tab); its host / restriction win.
    route?: any;
  }): Promise<ApiResponse<RegistryStatus>> {
    return wrap(apiFetch(base, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(values),
    }));
  }

  // Serving nodes withdraw the endpoint; blobs survive unless purgeData is set.
  function remove(name: string, options?: { purgeData?: boolean }): Promise<ApiResponse<RegistryDeleteResult>> {
    const query = options && options.purgeData ? '?purgeData=true' : '';
    return wrap(apiFetch(base + '/' + name + query, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
    }));
  }

  // Absent fields keep their stored value — a partial body must not silently clear the tag list.
  function setSettings(name: string, values: {
    quotaBytes?: number;
    host?: string;
    internal?: boolean;
    allowAnonymousPull?: boolean;
    tags?: string[];
    // The whole user-facing route (URL tab); its host / restriction win.
    route?: any;
  }): Promise<ApiResponse<RegistryStatus>> {
    return wrap(apiFetch(base + '/' + name + '/settings', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(values),
    }));
  }

  // Answers 202 once the sweep is scheduled, and wrap() only treats a literal 200 as success,
  // so this is the one call in the feature that cannot use it. The error shape matches wrap exactly.
  function gc(name: string): Promise<ApiResponse<{ registry: string; started: boolean }>> {
    return apiFetch(base + '/' + name + '/gc', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
    }).then(async (response) => {
      const body = await response.text();
      let rep: any;
      try {
        rep = JSON.parse(body);
      } catch (err) {
        rep = { message: body, status: response.status, code: response.status };
      }
      if (response.status >= 200 && response.status < 300) return rep;
      const e: any = new Error(rep.message || 'Server error');
      e.status = rep.status;
      e.code = rep.code;
      throw e;
    });
  }

  // --- deploy tokens -----------------------------------------------------------

  // The response is the ONLY time the raw token exists outside the client.
  function mintToken(name: string, values: {
    name: string;
    scopes?: string[];
    expiryDays?: number;
  }): Promise<ApiResponse<RegistryTokenMintResult>> {
    return wrap(apiFetch(base + '/' + name + '/tokens', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(values),
    }));
  }

  function revokeToken(name: string, tokenName: string): Promise<ApiResponse<RegistryStatus>> {
    return wrap(apiFetch(base + '/' + name + '/tokens/' + tokenName, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
    }));
  }

  // --- static sites ---------------------------------------------------------

  function listSites(registry: string): Promise<ApiResponse<RegistryStaticSite[]>> {
    return wrap(apiFetch(base + '/' + registry + '/sites', {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }));
  }

  function getSite(registry: string, site: string): Promise<ApiResponse<RegistryStaticSite>> {
    return wrap(apiFetch(base + '/' + registry + '/sites/' + site, {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }));
  }

  // Route configuration; admin-only server-side — a deploy token may publish, never move a site.
  function updateSite(registry: string, site: string, values: {
    host?: string;
    internal?: boolean;
    spa?: boolean;
    tags?: string[];
    route?: any;
  }): Promise<ApiResponse<RegistryStaticSite>> {
    return wrap(apiFetch(base + '/' + registry + '/sites/' + site, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(values),
    }));
  }

  function removeSite(registry: string, site: string): Promise<ApiResponse<{ deleted: string; deployments: number }>> {
    return wrap(apiFetch(base + '/' + registry + '/sites/' + site, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
    }));
  }

  // Multipart form (field "file"); everything else goes in the query string.
  // No Content-Type header: the browser has to set its own multipart boundary.
  function uploadSiteVersion(registry: string, site: string, file: File, options?: {
    version?: string;
    activate?: boolean;
    host?: string;
    internal?: boolean;
    spa?: boolean;
    tags?: string[];
  }): Promise<ApiResponse<RegistryStaticUploadResult>> {
    const query: string[] = [];
    const opts = options || {};
    if (opts.version) query.push('version=' + encodeURIComponent(opts.version));
    if (typeof opts.activate === 'boolean') query.push('activate=' + (opts.activate ? 'true' : 'false'));
    if (typeof opts.host === 'string') query.push('host=' + encodeURIComponent(opts.host));
    if (typeof opts.internal === 'boolean') query.push('internal=' + (opts.internal ? 'true' : 'false'));
    if (typeof opts.spa === 'boolean') query.push('spa=' + (opts.spa ? 'true' : 'false'));
    if (opts.tags) query.push('tags=' + encodeURIComponent(opts.tags.join(',')));

    const formData = new FormData();
    formData.append('file', file);

    return wrap(apiFetch(base + '/' + registry + '/sites/' + site + '/versions' +
      (query.length ? '?' + query.join('&') : ''), {
      method: 'POST',
      body: formData,
    }));
  }

  // Deploy and rollback are the same pointer move: deployments are immutable.
  function activateSiteVersion(registry: string, site: string, version: string): Promise<ApiResponse<RegistryStaticSite>> {
    return wrap(apiFetch(base + '/' + registry + '/sites/' + site + '/activate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ version }),
    }));
  }

  // Refused for the deployment that is currently active.
  function removeSiteVersion(registry: string, site: string, version: string): Promise<ApiResponse<RegistryStaticSite>> {
    return wrap(apiFetch(base + '/' + registry + '/sites/' + site + '/versions/' + version, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
    }));
  }

  // A plain URL the browser streams to disk itself, with the session cookie.
  function siteVersionDownloadURL(registry: string, site: string, version: string): string {
    return base + '/' + registry + '/sites/' + site + '/versions/' + encodeURIComponent(version) + '/download';
  }

  // --- packages (every type but static) --------------------------------------

  function listPackages(registry: string): Promise<ApiResponse<RegistryGenericPackage[]>> {
    return wrap(apiFetch(base + '/' + registry + '/packages', {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }));
  }

  function getPackage(registry: string, pkg: string): Promise<ApiResponse<RegistryGenericPackage>> {
    return wrap(apiFetch(base + '/' + registry + '/packages/' + encodeURIComponent(pkg), {
      method: 'GET',
      headers: { 'Content-Type': 'application/json' },
    }));
  }

  function removePackage(registry: string, pkg: string): Promise<ApiResponse<{ deleted: string; versions: number }>> {
    return wrap(apiFetch(base + '/' + registry + '/packages/' + encodeURIComponent(pkg), {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
    }));
  }

  // A file already present in the version is refused (409): content never changes
  // under a (version, filename) address.
  function uploadPackageFiles(registry: string, pkg: string, files: File[], options?: {
    version?: string;
  }): Promise<ApiResponse<RegistryGenericUploadResult>> {
    const query: string[] = [];
    if (options && options.version) query.push('version=' + encodeURIComponent(options.version));

    const formData = new FormData();
    files.forEach((file) => formData.append('file', file, file.name));

    return wrap(apiFetch(base + '/' + registry + '/packages/' + encodeURIComponent(pkg) + '/versions' +
      (query.length ? '?' + query.join('&') : ''), {
      method: 'POST',
      body: formData,
    }));
  }

  // "latest" is re-pointed at the newest remaining version.
  function removePackageVersion(registry: string, pkg: string, version: string): Promise<ApiResponse<RegistryGenericPackage>> {
    return wrap(apiFetch(base + '/' + registry + '/packages/' + encodeURIComponent(pkg) +
      '/versions/' + encodeURIComponent(version), {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
    }));
  }

  // Removing the last file of a version removes the version with it.
  function removePackageFile(registry: string, pkg: string, version: string, file: string): Promise<ApiResponse<{ package: RegistryGenericPackage; versionRemoved: boolean }>> {
    return wrap(apiFetch(base + '/' + registry + '/packages/' + encodeURIComponent(pkg) +
      '/versions/' + encodeURIComponent(version) + '/files/' + encodeURIComponent(file), {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
    }));
  }

  // Same rationale as siteVersionDownloadURL.
  function packageFileURL(registry: string, pkg: string, version: string, file: string): string {
    return base + '/' + registry + '/packages/' + encodeURIComponent(pkg) +
      '/versions/' + encodeURIComponent(version) + '/files/' + encodeURIComponent(file);
  }

  return {
    list, get, create, remove, setSettings, gc,
    mintToken, revokeToken,
    listSites, getSite, updateSite, removeSite,
    uploadSiteVersion, activateSiteVersion, removeSiteVersion, siteVersionDownloadURL,
    listPackages, getPackage, removePackage, uploadPackageFiles,
    removePackageVersion, removePackageFile, packageFileURL,
  };
}
