import wrap, { type ApiResponse, type ApiFetch } from './wrap';
import { confirmQuery, sendWithConfirmation } from '../utils/sensitiveEdit'; // Fork addition (plan 40)

export interface ContainerState {
  Status: string;
  Running: boolean;
  Paused: boolean;
  Restarting: boolean;
  Dead: boolean;
  Pid: number;
  ExitCode: number;
  StartedAt: string;
  FinishedAt: string;
  [key: string]: any;
}

export interface Container {
  ID: string;
  Name: string;
  Image: string;
  Created: string;
  State: ContainerState;
  Config: {
    Hostname: string;
    Image: string;
    Env: string[];
    Cmd: string[];
    Labels: Record<string, string>;
    ExposedPorts: Record<string, object>;
    [key: string]: any;
  };
  HostConfig: any;
  NetworkSettings: any;
  Mounts: any[];
  [key: string]: any;
}

export interface DockerNetwork {
  ID: string;
  Name: string;
  Driver: string;
  Scope: string;
  Internal: boolean;
  Attachable: boolean;
  Labels: Record<string, string>;
  [key: string]: any;
}

export interface DockerVolume {
  Name: string;
  Driver: string;
  Mountpoint: string;
  Labels: Record<string, string>;
  Scope: string;
  Options: Record<string, string>;
  [key: string]: any;
}

export default function createDockerAPI(apiFetch: ApiFetch, createWs: (path: string) => WebSocket) {
  function list(): Promise<ApiResponse<Container[]>> {
    return wrap(apiFetch('/cosmos/api/servapps', {
      method: 'GET',
      headers: {
          'Content-Type': 'application/json'
      },
    }))
  }

  function get(containerName: string): Promise<ApiResponse<Container>> {
    return wrap(apiFetch('/cosmos/api/servapps/' + containerName, {
      method: 'GET',
      headers: {
          'Content-Type': 'application/json'
      },
    }))
  }

  function getContainerLogs(containerId: string, searchQuery?: string, limit?: number, lastReceivedLogs?: string, errorOnly?: string): Promise<ApiResponse> {

    if(limit && limit < 50) limit = 50;

    // remove starting / from containerId
    if(containerId.startsWith('/')) {
      containerId = containerId.substring(1);
    }

    const queryParams = new URLSearchParams({
      search: searchQuery || "",
      limit: String(limit || ""),
      lastReceivedLogs: lastReceivedLogs || "",
      errorOnly: errorOnly || "",
    });

    return wrap(apiFetch(`/cosmos/api/servapps/${containerId}/logs?${queryParams}`, {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      }
    }));
  }

  function volumeList(): Promise<ApiResponse<DockerVolume[]>> {
    return wrap(apiFetch('/cosmos/api/volumes', {
      method: 'GET',
      headers: {
          'Content-Type': 'application/json'
      },
    }))
  }

  function volumeDelete(name: string): Promise<ApiResponse> {
    return wrap(apiFetch(`/cosmos/api/volume/${name}`, {
      method: 'DELETE',
      headers: {
          'Content-Type': 'application/json'
      },
    }))
  }

  function networkList(): Promise<ApiResponse<DockerNetwork[]>> {
    return wrap(apiFetch('/cosmos/api/networks', {
      method: 'GET',
      headers: {
          'Content-Type': 'application/json'
      },
    }))
  }

  function networkDelete(name: string): Promise<ApiResponse> {
    return wrap(apiFetch(`/cosmos/api/network/${name}`, {
      method: 'DELETE',
      headers: {
          'Content-Type': 'application/json'
      },
    }))
  }

  function secure(id: string, res: string): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/servapps/' + id + '/secure/'+res, {
      method: 'POST', // Fork addition: a command, sent as POST (src/fork_post_only.go)
      headers: {
          'Content-Type': 'application/json'
      },
    }))
  }

  const newDB = (): Promise<ApiResponse> => {
    return wrap(apiFetch('/cosmos/api/newDB', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      }
    }))
  }

  const manageContainer = (containerId: string, action: string): Promise<ApiResponse> => {
    return wrap(apiFetch('/cosmos/api/servapps/' + containerId + '/manage/' + action, {
      method: 'POST', // Fork addition: a command, sent as POST (src/fork_post_only.go)
      headers: {
        'Content-Type': 'application/json'
      }
    }))
  }

  function migrateHost(values: any): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/migrate-host', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(values),
    }))
  }

  function updateContainer(containerId: string, values: any): Promise<ApiResponse> {
    // Fork addition: access to the server the container did not have is asked for, tick by tick (utils/sensitiveEdit.js)
    return wrap(sendWithConfirmation((confirm: string[]) => apiFetch('/cosmos/api/servapps/' + containerId + '/update' + confirmQuery(confirm), {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(values),
    })))
  }

  function exportContainer(containerId: string, values: any): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/servapps/' + containerId + '/export', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(values),
    }))
  }

  function listContainerNetworks(containerId: string): Promise<ApiResponse<DockerNetwork[]>> {
    return wrap(apiFetch('/cosmos/api/servapps/' + containerId + '/networks', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      }
    }))
  }

  function createNetwork(values: { Name: string; Driver?: string; [key: string]: any }): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/networks', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(values),
    }))
  }

  function attachNetwork(containerId: string, networkId: string): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/servapps/' + containerId + '/network/' + networkId, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      }
    }))
  }

  // Fork addition (plan 43): why a container does not start (state, the end of its log, what it says it needs)
  function startReport(containerId: string): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/servapps/' + encodeURIComponent(containerId) + '/start-report', {
      method: 'GET',
    }))
  }

  // Fork addition (plan 43): does a port of the container speak web (asked once by the server)
  function probePort(containerId: string, port: number): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/servapps/' + encodeURIComponent(containerId) + '/probe', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ port }),
    }))
  }

  function detachNetwork(containerId: string, networkId: string): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/servapps/' + containerId + '/network/' + networkId, {
      method: 'DELETE',
      headers: {
        'Content-Type': 'application/json'
      }
    }))
  }

  function createVolume(values: { Name: string; Driver?: string; [key: string]: any }): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/volumes', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(values),
    }))
  }

  function attachTerminal(containerId: string, readonly?: boolean): WebSocket {
    return createWs('/cosmos/api/servapps/' + containerId + '/terminal/attach' + (readonly ? '?readonly=true' : ''));
  }

  function createTerminal(containerId: string): WebSocket {
    return createWs('/cosmos/api/servapps/' + containerId + '/terminal/new');
  }

  // Fork addition: replace — an edit of existing containers; without it the server refuses a name that exists
  // Fork addition: `confirm` names each access to the server the admin said yes to, `anyArch` installs an image
  // made for another processor anyway (src/docker/fork_sensitive_access.go, fork_image_arch.go)
  function createService(serviceData: any, onProgress: (line: string) => void, replace = false, confirm: string[] = [], anyArch = false): Promise<ReadableStream> {
    const requestOptions: RequestInit = {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(serviceData)
    };

    const query = new URLSearchParams(); // Fork addition
    if (replace) query.append('replace', '1');
    (confirm || []).forEach((item) => query.append('confirm', item));
    if (anyArch) query.append('anyArch', '1');
    return apiFetch('/cosmos/api/docker-service' + (query.toString() ? '?' + query.toString() : ''), requestOptions)
      .then(response => {
        if (!response.ok) {
          throw new Error(response.statusText);
        }

        const reader = response.body!.getReader();

        return new ReadableStream({
          start(controller) {
            function read(): any {
              return reader.read().then(({ done, value }) => {
                if (done) {
                  controller.close();
                  return;
                }
                let text = new TextDecoder().decode(value);
                let lines = text.split('\n');
                for (let line of lines) {
                  if (line) {
                    onProgress(line);
                  }
                }
                controller.enqueue(value);
                return read();
              });
            }
            return read();
          }
        });
      });
  }

  function pullImage(imageName: string, onProgress: (line: string) => void, ifMissing?: boolean): Promise<ReadableStream> {
    const requestOptions: RequestInit = {
      method: 'POST', // Fork addition: a command, sent as POST (src/fork_post_only.go)
      headers: {
        'Content-Type': 'application/json'
      },
    };

    const imageNameEncoded = encodeURIComponent(imageName);

    return apiFetch(`/cosmos/api/images/${ifMissing ? 'pull-if-missing' : 'pull'}?imageName=${imageNameEncoded}`, requestOptions)
      .then(response => {
        if (!response.ok) {
          throw new Error(response.statusText);
        }

        const reader = response.body!.getReader();

        return new ReadableStream({
          start(controller) {
            function read(): any {
              return reader.read().then(({ done, value }) => {
                if (done) {
                  controller.close();
                  return;
                }
                let text = new TextDecoder().decode(value);
                let lines = text.split('\n');
                for (let line of lines) {
                  if (line) {
                    onProgress(line);
                  }
                }
                controller.enqueue(value);
                return read();
              });
            }
            return read();
          }
        });
      });
  }

  function updateContainerImage(containerName: string, onProgress: (line: string) => void): Promise<ReadableStream> {
    const requestOptions: RequestInit = {
      method: 'POST', // Fork addition: a command, sent as POST (src/fork_post_only.go)
      headers: {
        'Content-Type': 'application/json'
      },
    };

    const containerNameEncoded = encodeURIComponent(containerName);

    return apiFetch(`/cosmos/api/servapps/${containerNameEncoded}/manage/update`, requestOptions)
      .then(response => {
        if (!response.ok) {
          throw new Error(response.statusText);
        }

        const reader = response.body!.getReader();

        return new ReadableStream({
          start(controller) {
            function read(): any {
              return reader.read().then(({ done, value }) => {
                if (done) {
                  controller.close();
                  return;
                }
                let text = new TextDecoder().decode(value);
                let lines = text.split('\n');
                for (let line of lines) {
                  if (line) {
                    onProgress(line);
                  }
                }
                controller.enqueue(value);
                return read();
              });
            }
            return read();
          }
        });
      });
  }

  function autoUpdate(id: string, toggle: string): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/servapps/' + id + '/auto-update/'+toggle, {
      method: 'POST', // Fork addition: a command, sent as POST (src/fork_post_only.go)
      headers: {
          'Content-Type': 'application/json'
      },
    }))
  }

  function lazy(id: string, toggle: string, opts?: { idle?: string, startTimeout?: string }): Promise<ApiResponse> {
    const params = new URLSearchParams();
    if (opts && opts.idle) params.set('idle', opts.idle);
    if (opts && opts.startTimeout) params.set('startTimeout', opts.startTimeout);
    const query = params.toString() ? '?' + params.toString() : '';

    return wrap(apiFetch('/cosmos/api/servapps/' + id + '/lazy/' + toggle + query, {
      method: 'POST', // Fork addition: a command, sent as POST (src/fork_post_only.go)
      headers: {
          'Content-Type': 'application/json'
      },
    }))
  }

  // Fork addition: what an image already on the server declares (ports,
  // volumes, environment). Fails when the image is not there; never pulls.
  function inspectImage(imageName: string): Promise<any> {
    return wrap(apiFetch('/cosmos/api/images?imageName=' + encodeURIComponent(imageName), {
      method: 'GET',
      headers: {
          'Content-Type': 'application/json'
      },
    }), true)
  }

  // Fork addition: containers left behind by an interrupted change
  // (src/docker/fork_leftovers.go). GET lists them; POST puts one back under
  // its app's name ("restore") or deletes it ("remove").
  // Fork addition: what the review of an installation needs (src/fork_install_plan.go); nothing is pulled
  function installPlan(images: string[]): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/install/plan', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ images }),
    }));
  }

  // Fork addition (plan 40): Docker Hub search and what an image declares, read without a pull (src/anyimage)
  function imageSearch(query: string): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/images/search', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ query }),
    }));
  }

  function imageDescribe(reference: string): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/images/describe', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ reference }),
    }));
  }

  function leftovers(): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/servapps-leftovers', {
      method: 'GET',
      headers: {
          'Content-Type': 'application/json'
      },
    }), true)
  }

  function leftoverAction(action: 'restore' | 'remove', name: string): Promise<ApiResponse> {
    return wrap(apiFetch('/cosmos/api/servapps-leftovers', {
      method: 'POST',
      headers: {
          'Content-Type': 'application/json'
      },
      body: JSON.stringify({ action, name }),
    }))
  }

  return {
    inspectImage,
    list,
    get,
    newDB,
    secure,
    manageContainer,
    volumeList,
    volumeDelete,
    networkList,
    networkDelete,
    getContainerLogs,
    updateContainer,
    listContainerNetworks,
    createNetwork,
    attachNetwork,
    detachNetwork,
    startReport, // Fork addition (plan 43)
    probePort, // Fork addition (plan 43)
    createVolume,
    attachTerminal,
    createTerminal,
    createService,
    pullImage,
    autoUpdate,
    lazy,
    updateContainerImage,
    exportContainer,
    migrateHost,
    installPlan, // Fork addition: the server's platform, the images' platforms, the places for data
    imageSearch, // Fork addition (plan 40)
    imageDescribe, // Fork addition (plan 40)
    leftovers, // Fork addition
    leftoverAction, // Fork addition
  };
}
