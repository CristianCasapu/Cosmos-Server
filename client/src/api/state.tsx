import wrap, { type ApiResponse, type ApiFetch } from './wrap';

// Pro cluster state: the on-disk mirror of every Pro record (deployments,
// databases, S3, registries, CI projects) and the import that puts records
// back without touching anything running.

export interface StateSummary {
  counts: Record<string, number>;
  mirroredAt: string;
  mirrorError?: string;
  reseedPending: boolean;
  importing: boolean;
}

export interface StateImportResult {
  created: Record<string, number>;
  present: Record<string, number>;
}

export default function createStateAPI(apiFetch: ApiFetch) {
  function summary(): Promise<ApiResponse<StateSummary>> {
    return wrap(apiFetch('/cosmos/api/constellation/state', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  function importFile(contents: string): Promise<ApiResponse<StateImportResult>> {
    return wrap(apiFetch('/cosmos/api/constellation/state/import', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: contents,
    }))
  }

  function startEmpty(): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/constellation/state/start-empty', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  return {
    summary,
    importFile,
    startEmpty,
  };
}
