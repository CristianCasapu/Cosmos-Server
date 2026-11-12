// Fork addition: shortcuts at the top of the file picker to the folders that
// matter on this install, instead of always walking down from "/": the top of
// the file system, the folder of the field's current value, the default data
// path (Configuration > Docker) and the mount points Cosmos lists under
// Storage. Clicking one shows that folder as the top of the tree.
import * as React from 'react';
import { Chip, Stack, Typography } from '@mui/material';
import { AimOutlined, DatabaseOutlined, FolderOutlined, HddOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../api';

const clean = (path) => {
  path = (typeof path === 'string' ? path : '').trim();
  if (!path.startsWith('/')) {
    return '';
  }
  return path.replace(/\/+$/, '') || '/';
};

// Where to open the picker for a field that already holds `value`: the path
// itself when it is a folder, else the folder it is in. Empty when the value
// is not a path of the local disk.
export const startFolders = (value) => {
  const path = clean(value);
  if (!path || path === '/') {
    return [];
  }
  const parent = path.slice(0, path.lastIndexOf('/'));
  return parent ? [path, parent] : [path];
};

const FilePickerShortcuts = ({ current, startFolder, onGo }) => {
  const { t } = useTranslation();
  const [dataPath, setDataPath] = React.useState('');
  const [mounts, setMounts] = React.useState([]);

  React.useEffect(() => {
    let live = true;
    API.config.get().then((res) => {
      if (live) {
        setDataPath(clean(res && res.data && res.data.DockerConfig && res.data.DockerConfig.DefaultDataPath));
      }
    }).catch(() => {});
    API.storage.mounts.list().then((res) => {
      if (live) {
        setMounts(((res && res.data) || []).map((mount) => clean(mount.path)).filter(Boolean));
      }
    }).catch(() => {});
    return () => { live = false; };
  }, []);

  const shortcuts = [];
  const add = (path, icon, title) => {
    if (path && !shortcuts.find((shortcut) => shortcut.path === path)) {
      shortcuts.push({ path, icon, title });
    }
  };
  add('/', <FolderOutlined />, t('mgmt.storage.picker.shortcutRoot', 'The top of the server\'s file system'));
  add(clean(startFolder), <AimOutlined />, t('mgmt.storage.picker.shortcutCurrent', 'Where the path in the field is'));
  add(dataPath, <DatabaseOutlined />, t('mgmt.storage.picker.shortcutData', 'The default data path for installs (Configuration > Docker)'));
  mounts.forEach((path) => add(path, <HddOutlined />, t('mgmt.storage.picker.shortcutMount', 'A mounted disk (Storage)')));

  return <Stack direction="row" useFlexGap flexWrap="wrap" spacing={1} alignItems="center" sx={{ mb: 1 }}>
    <Typography variant="caption" color="textSecondary">
      {t('mgmt.storage.picker.shortcuts', 'Go to:')}
    </Typography>
    {shortcuts.map((shortcut) => (
      <Chip
        key={shortcut.path}
        size="small"
        clickable
        color={current === shortcut.path ? 'primary' : 'default'}
        variant={current === shortcut.path ? 'filled' : 'outlined'}
        icon={shortcut.icon}
        label={shortcut.path}
        title={shortcut.title}
        onClick={() => onGo(shortcut.path)}
        sx={{ maxWidth: '100%' }}
      />
    ))}
  </Stack>;
};

export default FilePickerShortcuts;
