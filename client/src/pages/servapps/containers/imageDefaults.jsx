// Fork addition: pre-fills the new container form from what its image
// declares. When the image typed in the form is already on the server, its
// exposed ports, declared volumes and default environment variables are
// offered with one button. Only what is not in the form yet is added; nothing
// the admin entered is changed. The image is only inspected locally
// (GET /api/images): nothing is ever pulled for this.
import * as React from 'react';
import { Alert, Button, Grid, Stack } from '@mui/material';
import { useTranslation } from 'react-i18next';
import * as API from '../../../api';

// Variables an image carries for its own use. Copying them into the container
// would pin them, and a later version of the image could no longer change them.
const internalEnv = (key) => ['PATH', 'HOME', 'HOSTNAME', 'TERM', 'LANG', 'LANGUAGE', 'LC_ALL'].includes(key)
  || /(^|_)(VERSION|RELEASE|SHA\d*|GPG_KEYS?|DOWNLOAD_URL)$/.test(key);

const envKey = (line) => line.split(/=(.*)/s)[0];

// What an image (the answer of `docker image inspect`) declares.
export const imageDeclares = (image) => {
  const config = (image && image.Config) || {};
  return {
    // "8096/tcp": only TCP ports can be the target of a URL
    ports: Object.keys(config.ExposedPorts || {}).filter((port) => /^\d+(\/tcp)?$/.test(port)).map((port) => parseInt(port, 10)),
    volumes: Object.keys(config.Volumes || {}),
    env: (config.Env || []).filter((line) => line.includes('=') && !internalEnv(envKey(line))),
  };
};

// The part of it that the form does not have yet.
export const missingDefaults = (declared, containerInfo) => {
  const ports = (containerInfo.Ports || []).map((port) => port.PrivatePort);
  const targets = (containerInfo.HostConfig.Mounts || []).map((mount) => (mount.Target || '').replace(/\/+$/, ''));
  const keys = (containerInfo.Config.Env || []).map(envKey);
  return {
    ports: declared.ports.filter((port) => !ports.includes(port)),
    volumes: declared.volumes.filter((target) => !targets.includes(target.replace(/\/+$/, ''))),
    env: declared.env.filter((line) => !keys.includes(envKey(line))),
  };
};

// The name of the volume that keeps one of the image's folders: <app>-<folder>.
export const volumeName = (containerInfo, target) => {
  const image = (containerInfo.Config.Image || '').split('@')[0].split('/').pop().split(':')[0];
  const clean = (text) => text.replace(/[^a-zA-Z0-9_.-]+/g, '-').replace(/^[^a-zA-Z0-9]+|[^a-zA-Z0-9]+$/g, '');
  return (clean(containerInfo.Name || image) || 'app') + '-' + (clean(target) || 'data');
};

// The form's state with the missing defaults added.
export const applyImageDefaults = (containerInfo, missing) => ({
  ...containerInfo,
  // the choices of the URL step's port picker (it reads Image and Ports, as the container list gives them)
  Image: containerInfo.Config.Image,
  Ports: [
    ...(containerInfo.Ports || []),
    ...missing.ports.map((port) => ({ PrivatePort: port, Type: 'tcp' })),
  ],
  Config: {
    ...containerInfo.Config,
    Env: [...(containerInfo.Config.Env || []), ...missing.env],
  },
  HostConfig: {
    ...containerInfo.HostConfig,
    Mounts: [
      ...(containerInfo.HostConfig.Mounts || []),
      ...missing.volumes.map((target) => ({ Type: 'volume', Source: volumeName(containerInfo, target), Target: target })),
    ],
  },
});

// `image` is the image name once the admin has left the field.
const ImageDefaults = ({ image, containerInfo, onApply }) => {
  const { t } = useTranslation();
  const [declared, setDeclared] = React.useState(null);
  const [applied, setApplied] = React.useState(false);

  React.useEffect(() => {
    setDeclared(null);
    setApplied(false);
    if (!image || !API.docker.inspectImage) {
      return undefined;
    }
    let current = true;
    API.docker.inspectImage(image).then((res) => {
      if (current) {
        setDeclared(imageDeclares(res));
      }
    }).catch(() => {
      // not on the server (or not readable): nothing to offer, and nothing is pulled to find out
    });
    return () => { current = false; };
  }, [image]);

  if (!declared) {
    return null;
  }

  if (applied) {
    return <Grid item xs={12}>
      <Alert severity="success" onClose={() => setDeclared(null)} closeText={t('global.close')}>
        {t('mgmt.servapps.newContainer.imageDefaults.applied',
          'Added. The variables are below, the volumes under Storage, and the ports are the choices offered under URL. Nothing you had entered was changed.')}
      </Alert>
    </Grid>;
  }

  const missing = missingDefaults(declared, containerInfo);
  if (missing.ports.length + missing.volumes.length + missing.env.length === 0) {
    return null;
  }

  return <Grid item xs={12}>
    <Alert severity="info">
      <Stack spacing={1} alignItems="flex-start" sx={{ overflowWrap: 'anywhere' }}>
        <div>
          {t('mgmt.servapps.newContainer.imageDefaults.intro',
            'This image is already on the server. It declares settings that are not in the form yet:')}
        </div>
        {missing.ports.length > 0 && <div>
          {t('mgmt.servapps.newContainer.imageDefaults.ports',
            'Ports: {{list}}. They become the choices for the target of the app\'s URL; none is published on the host.',
            { list: missing.ports.join(', ') })}
        </div>}
        {missing.volumes.length > 0 && <div>
          {t('mgmt.servapps.newContainer.imageDefaults.volumes',
            'Folders it keeps data in: {{list}}. Each gets a named volume, so the data survives an update.',
            { list: missing.volumes.join(', ') })}
        </div>}
        {missing.env.length > 0 && <div>
          {t('mgmt.servapps.newContainer.imageDefaults.env',
            'Variables: {{list}}.',
            { list: missing.env.map(envKey).join(', ') })}
        </div>}
        <Button
          size="small"
          variant="outlined"
          onClick={() => {
            onApply(applyImageDefaults(containerInfo, missing));
            setApplied(true);
          }}
        >
          {t('mgmt.servapps.newContainer.imageDefaults.apply', 'Use the image\'s defaults')}
        </Button>
      </Stack>
    </Alert>
  </Grid>;
};

export default ImageDefaults;
