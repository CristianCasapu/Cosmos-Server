// Fork addition: for the Volumes page, which only listed names: where the
// data of a volume is on the server and which apps use it, or that none does.
// Built from the volume list and the container list the UI already loads
// (utils/volumeUsage.js); Docker gives sizes only through a slow call, so
// none is shown.
import * as React from 'react';
import { Box, Chip, Stack, Tooltip, Typography } from '@mui/material';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import * as API from '../api';
import { volumeUsers, volumeLocation } from '../utils/volumeUsage';

// The container list, loaded by `reload`: the page calls it wherever it loads
// its volumes (when it opens, and on Refresh). null until it is known.
export const useVolumeContainers = () => {
  const [containers, setContainers] = React.useState(null);
  const live = React.useRef(true);

  const reload = React.useCallback(() => {
    API.docker.list()
      .then((res) => { if (live.current) setContainers((res && res.data) || []); })
      .catch(() => {});
  }, []);

  React.useEffect(() => {
    live.current = true;
    return () => { live.current = false; };
  }, []);

  return [containers, reload];
};

// One line under the volume's name: where its data is.
export const VolumeLocation = ({ volume }) => {
  const { t } = useTranslation();
  const where = volumeLocation(volume);

  let text;
  if (where.kind === 'memory') {
    text = t('mgmt.servapps.volumes.where.memory', 'Kept in memory only: emptied when the app stops');
  } else if (where.kind === 'share') {
    text = t('mgmt.servapps.volumes.where.share', 'Network share: {{path}} ({{type}})', where);
  } else if (where.kind === 'device') {
    text = t('mgmt.servapps.volumes.where.device', 'Disk: {{path}} ({{type}})', where);
  } else if (where.kind === 'other') {
    text = where.path
      ? t('mgmt.servapps.volumes.where.driverAt', 'Kept by the volume driver "{{driver}}": {{path}}', where)
      : t('mgmt.servapps.volumes.where.driver', 'Kept by the volume driver "{{driver}}"', where);
  } else if (where.path) {
    text = t('mgmt.servapps.volumes.where.folder', 'Folder on the server: {{path}}', where);
  } else {
    return null;
  }
  return <span style={{ overflowWrap: 'anywhere' }}>{text}</span>;
};

// The apps that use the volume, each a link to the app, or "not used".
// `narrow` is the copy shown under the name on screens too small for the
// "Used by" column; `wide` is the column's own.
export const VolumeUsers = ({ volume, containers, narrow }) => {
  const { t } = useTranslation();
  const users = volumeUsers(volume, containers);
  if (users === null) return null;

  const content = users.length === 0
    ? <Typography variant="body2" color="textSecondary">
        {t('mgmt.servapps.volumes.unused', 'Not used by any app')}
      </Typography>
    : <Stack direction="row" useFlexGap flexWrap="wrap" spacing={0.75} alignItems="center">
        {narrow && <Typography variant="caption" color="textSecondary">{t('mgmt.servapps.volumes.usedBy', 'Used by')}:</Typography>}
        {users.map((user) => {
          const vars = { path: user.target };
          let tip = user.readOnly
            ? t('mgmt.servapps.volumes.mountedReadOnly', 'Seen by the app as {{path}} (read only)', vars)
            : t('mgmt.servapps.volumes.mounted', 'Seen by the app as {{path}}', vars);
          if (!user.running) tip += ' ' + t('mgmt.servapps.volumes.appStopped', 'The app is stopped.');
          return <Tooltip key={user.name + ':' + user.target} title={tip}>
            <Chip
              size="small"
              clickable
              variant="outlined"
              color={user.running ? 'primary' : 'default'}
              component={Link}
              to={'/cosmos-ui/servapps/containers/' + encodeURIComponent(user.name)}
              label={user.name}
              sx={{ maxWidth: '100%' }}
            />
          </Tooltip>;
        })}
      </Stack>;

  if (!narrow) return content;
  return <Box sx={{ display: { xs: 'block', md: 'none' }, mt: 0.75 }}>{content}</Box>;
};
