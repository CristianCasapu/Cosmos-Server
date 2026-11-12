import { createApiClient } from '../../client/src/api/client';
import createAuthAPI from '../../client/src/api/authentication';
import createUsersAPI from '../../client/src/api/users';
import createConfigAPI from '../../client/src/api/config';
import createDockerAPI from '../../client/src/api/docker';
import createMarketAPI from '../../client/src/api/market';
import createConstellationAPI from '../../client/src/api/constellation';
import createMetricsAPI from '../../client/src/api/metrics';
import createStorageAPI from '../../client/src/api/storage';
import createCronAPI from '../../client/src/api/cron';
import createRcloneAPI from '../../client/src/api/rclone';
import createBackupsAPI from '../../client/src/api/backup';
import createApiTokensAPI from '../../client/src/api/apiTokens';
import createOpenIDAPI from '../../client/src/api/openid';
import createGroupsAPI from '../../client/src/api/groups';
import createDeploymentsAPI from '../../client/src/api/deployments';
import createDatabasesAPI from '../../client/src/api/databases';
import createSeaweedFSAPI from '../../client/src/api/seaweedfs';
import createRegistryAPI from '../../client/src/api/registry';
import createFunctionsAPI from '../../client/src/api/functions';
import createCIAPI from '../../client/src/api/ci';
import createShieldAPI from '../../client/src/api/shield';
import createZonesAPI from '../../client/src/api/zones';
import createStateAPI from '../../client/src/api/state';
import wrap from '../../client/src/api/wrap';

export interface SetupRequest {
  mongodbMode: string;
  mongodb?: string;
  hostname: string;
  httpsCertificateMode: string;
  sslEmail?: string;
  useWildcardCertificate?: boolean;
  dnsChallengeProvider?: string;
  DNSChallengeConfig?: Record<string, string>;
  tlsCert?: string;
  tlsKey?: string;
  allowHTTPLocalIPAccess?: boolean;
  nickname: string;
  password: string;
  email?: string;
  clearConfig?: boolean;
  constellationConfig?: string;
  createAdminToken?: boolean;
}

export function createClient({ baseUrl, token }: { baseUrl: string; token: string }) {
  const { apiFetch, createWs } = createApiClient(baseUrl, token);

  return {
    auth: createAuthAPI(apiFetch),
    users: createUsersAPI(apiFetch),
    config: createConfigAPI(apiFetch),
    docker: createDockerAPI(apiFetch, createWs),
    market: createMarketAPI(apiFetch),
    constellation: createConstellationAPI(apiFetch),
    metrics: createMetricsAPI(apiFetch),
    storage: createStorageAPI(apiFetch),
    cron: createCronAPI(apiFetch, createWs),
    rclone: createRcloneAPI(apiFetch),
    backups: createBackupsAPI(apiFetch),
    apiTokens: createApiTokensAPI(apiFetch),
    openid: createOpenIDAPI(apiFetch),
    groups: createGroupsAPI(apiFetch),
    deployments: createDeploymentsAPI(apiFetch),
    databases: createDatabasesAPI(apiFetch),
    seaweedfs: createSeaweedFSAPI(apiFetch),
    registry: createRegistryAPI(apiFetch),
    functions: createFunctionsAPI(apiFetch),
    ci: createCIAPI(apiFetch),
    shield: createShieldAPI(apiFetch),
    zones: createZonesAPI(apiFetch),
    state: createStateAPI(apiFetch),

    setup: (request: SetupRequest) => {
      return wrap(apiFetch('/cosmos/api/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(request),
      }));
    },

    getStatus: () => {
      return wrap(apiFetch('/cosmos/api/status', {
        method: 'GET',
        headers: { 'Content-Type': 'application/json' }
      }));
    },

    isOnline: () => {
      return apiFetch('/cosmos/api/status', {
        method: 'GET',
        headers: { 'Content-Type': 'application/json' }
      }).then(async (response) => {
        const rep = await response.json();
        if (response.status === 200) return rep;
        const e: any = new Error(rep.message);
        e.status = response.status;
        throw e;
      });
    },

    uploadImage: (file: any, name: string) => {
      const formData = new FormData();
      formData.append('image', file);
      return wrap(apiFetch('/cosmos/api/upload/' + name, {
        method: 'POST',
        body: formData
      }));
    },

    restartServer: () => {
      return wrap(apiFetch('/cosmos/api/restart-server', {
        method: 'GET',
        headers: { 'Content-Type': 'application/json' },
      }));
    },

    terminal: (startCmd: string = "bash") => {
      return createWs('/cosmos/api/terminal/' + startCmd);
    },

    forceAutoUpdate: () => {
      return wrap(apiFetch('/cosmos/api/force-server-update', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
      }));
    },

    checkHost: (host: string) => {
      return apiFetch('/cosmos/api/dns-check?url=' + host, {
        method: 'GET',
        headers: { 'Content-Type': 'application/json' }
      }).then(async (response) => {
        const rep = await response.json();
        if (response.status === 200) return rep;
        const e: any = new Error(rep.message);
        e.status = response.status;
        throw e;
      });
    },

    getDNS: (host: string) => {
      return apiFetch('/cosmos/api/dns?url=' + host, {
        method: 'GET',
        headers: { 'Content-Type': 'application/json' }
      }).then(async (response) => {
        const rep = await response.json();
        if (response.status === 200) return rep;
        const e: any = new Error(rep.message);
        e.status = response.status;
        throw e;
      });
    },
  };
}

export { createApiClient };

// Re-export types for SDK consumers
export type { ApiResponse, ApiFetch } from '../../client/src/api/wrap';
export type { LoginRequest, UserMe } from '../../client/src/api/authentication';
export type { User } from '../../client/src/api/users';
export type { Route, Operation } from '../../client/src/api/config';
export type { Container, ContainerState, DockerNetwork, DockerVolume } from '../../client/src/api/docker';
export type { MarketApp, MarketResult } from '../../client/src/api/market';
export type { CreateAPITokenRequest, CreateAPITokenResponse, UpdateAPITokenRequest, APITokenConfig } from '../../client/src/api/apiTokens';
export type { OpenIDClient } from '../../client/src/api/openid';
export type { ConstellationDevice } from '../../client/src/api/constellation';
export type { CronJob } from '../../client/src/api/cron';
export type { DiskInfo, MountRequest, UnmountRequest, MergeRequest, SnapRAIDConfig, RaidCreateRequest } from '../../client/src/api/storage';
export type { BackupConfig, RestoreConfig } from '../../client/src/api/backup';
export type * from '../../client/src/api/groups';
export type * from '../../client/src/api/deployments';
export type * from '../../client/src/api/databases';
export type * from '../../client/src/api/seaweedfs';
export type * from '../../client/src/api/registry';
export type * from '../../client/src/api/functions';
export type * from '../../client/src/api/ci';
