import wrap, { type ApiResponse, type ApiFetch } from './wrap';

export interface DNSZoneConfig {
  Zone: string;
  HTTPSCertificateMode: string;
  TLSCert?: string;
  TLSKey?: string;
  UseWildcardCertificate: boolean;
  DNSChallengeProvider: string;
  DNSChallengeConfig?: Record<string, string>;
  DNSChallengeResolvers: string;
  DNSChallengePropagationWait: number;
  DisablePropagationChecks: boolean;
  ManageRecords: boolean;
  WildcardRecord: boolean;
}

export interface HostCertificate {
  source: 'zone' | 'node' | 'selfsigned' | 'provided' | 'none';
  zone?: string;
  hosts: string[];
  validUntil: string;
  covered: boolean;
}

export interface ZoneStatus extends DNSZoneConfig {
  derived: boolean;
  issuesCertificate: boolean;
  hosts: string[];
  certificate?: {
    hosts: string[];
    validUntil: string;
    issuedBy?: string;
  };
  hostCertificates: Record<string, HostCertificate>;
  recordsSupported: boolean;
  records?: {
    supported: boolean;
    lastSync: string;
    lastError?: string;
    records: Record<string, string[]>;
  };
}

export interface AdvertisedAddressOption {
  value: string;
  address: string;
}

export default function createZonesAPI(apiFetch: ApiFetch) {
  function list(): Promise<ApiResponse<ZoneStatus[]>> {
    return wrap(apiFetch('/cosmos/api/zones', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  function set(zone: string, config: Partial<DNSZoneConfig>): Promise<ApiResponse> {
    return wrap(apiFetch(`/cosmos/api/zones/${encodeURIComponent(zone)}`, {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(config),
    }))
  }

  function remove(zone: string): Promise<ApiResponse> {
    return wrap(apiFetch(`/cosmos/api/zones/${encodeURIComponent(zone)}`, {
      method: 'DELETE',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  function addresses(): Promise<ApiResponse<AdvertisedAddressOption[]>> {
    return wrap(apiFetch('/cosmos/api/zones-addresses', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
    }))
  }

  return {
    addresses,
    list,
    set,
    remove,
  };
}
