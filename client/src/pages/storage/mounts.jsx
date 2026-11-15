import React from "react";
import { useEffect, useState } from "react";
import * as API  from "../../api";
import PrettyTableView from "../../components/tableView/prettyTableView";
import { DeleteButton } from "../../components/delete";
import { CloudOutlined, CloudServerOutlined, CompassOutlined, DeleteOutlined, DesktopOutlined, EditOutlined, FolderOutlined, LaptopOutlined, MobileOutlined, PlusCircleOutlined, ReloadOutlined, TabletOutlined } from "@ant-design/icons";
import { Alert, Button, CircularProgress, InputLabel, ListItemIcon, ListItemText, MenuItem, Stack } from "@mui/material";
import { CosmosCheckbox, CosmosFormDivider, CosmosInputText } from "../config/users/formShortcuts";
import MainCard from "../../components/MainCard";
import { Formik } from "formik";
import { LoadingButton } from "@mui/lab";
import ApiModal from "../../components/apiModal";
import ConfirmModal, { ConfirmModalDirect } from "../../components/confirmModal";
import { isDomain } from "../../utils/indexs";
import UploadButtons from "../../components/fileUpload";
import SnapRAIDDialog from "./snapRaidDialog";
import MenuButton from "../../components/MenuButton";
import MountDialog, { MountDialogInternal } from "./mountDialog";
import ResponsiveButton from "../../components/responseiveButton";
import { useTranslation } from 'react-i18next';
import VMWarning from "./vmWarning";
import { useClientInfos } from "../../utils/hooks";
import { PERM_RESOURCES } from "../../utils/permissions";
import PermissionGuard from "../../components/permissionGuard";
import { ForkMountDialog, ForkUnmountDialog } from "./forkStorageForms"; // Fork addition: the guided dialogs

// Fork addition: data disks mounted outside /mnt are listed too. They were not
// mounted through this page (which only mounts under /mnt), so they are shown
// without the actions that would take them out of /etc/fstab.
export const mountedHere = (path) => /^\/(mnt|var\/mnt)\//.test(path || '');

export const StorageMounts = () => {
  const { t } = useTranslation();
  const { hasPermission } = useClientInfos();
  const isAdmin = hasPermission(PERM_RESOURCES);
  const [config, setConfig] = useState(null);
  const [mounts, setMounts] = useState([]);
  const [mountDialog, setMountDialog] = useState(null);
  const [loading, setLoading] = useState(false);
  const [containerized, setContainerized] = useState(false);

  const refresh = async () => {
    setLoading(true);
    let mountsData = await API.storage.mounts.list();
    let configAsync = await API.config.get();
    let status = await API.getStatus();

    setConfig(configAsync.data);
    setMounts(mountsData.data);
    setLoading(false);
    setContainerized(status.data.containerized);
  };

  useEffect(() => {
    refresh();
  }, []);

  return <div style={{ maxWidth: "1200px", margin: "auto" }}>
    {/* Fork addition: the guided dialogs, see forkStorageForms.jsx (upstream: MountDialogInternal) */}
    {mountDialog && !mountDialog.unmount && <ForkMountDialog existing={mountDialog.data} onClose={() => setMountDialog(null)} onDone={() => { setMountDialog(null); refresh(); }} />}
    {mountDialog && mountDialog.unmount && <ForkUnmountDialog mount={mountDialog.data} onClose={() => setMountDialog(null)} onDone={() => { setMountDialog(null); refresh(); }} />}
    {(config) ? <Stack spacing={2}>
      {containerized && <VMWarning />}
      <PrettyTableView
            empty={{ title: t('emptyState.mounts.title', 'No mounts'), text: t('emptyState.mounts.text', 'Disks and partitions mounted on the server are listed here. Mount one with “New Mount” above.') }} /* Fork addition: what this list is for and how it fills */
        data={mounts}
        getKey={(r) => `${r.device} - ${refresh.path}`}
        buttons={[
          <PermissionGuard permission={PERM_RESOURCES}><ResponsiveButton startIcon={<PlusCircleOutlined />}  disabled={containerized} variant="contained" onClick={() => setMountDialog({data: null, unmount: false})}>{t('mgmt.storage.newMount.newMountButton')}</ResponsiveButton></PermissionGuard>,
          <ResponsiveButton variant="outlined" startIcon={<ReloadOutlined />} onClick={() => {
            refresh();
          }}>{t('global.refresh')}</ResponsiveButton>
        ]}
        columns={[
          {
            title: t('mgmt.storage.deviceTitle'),
            field: (r) => <span style={{ overflowWrap: 'break-word' }}><FolderOutlined/>  {r.device}</span>, // Fork addition: a merge's list of folders breaks
          },
          { 
            title: t('mgmt.storage.pathTitle'),
            field: (r) => <span style={{ overflowWrap: 'anywhere' }}>{r.path}</span>, // Fork addition: a long path breaks (it widened the table past the page)
          },
          { 
            title: t('mgmt.storage.typeTitle'),
            field: (r) => r.type,
          },
          { 
            title: t('mgmt.storage.optionsTitle'),
            field: (r) => <span style={{ overflowWrap: 'anywhere' }}>{Array.isArray(r.opts) ? r.opts.join(', ') : JSON.stringify(r.opts)}</span>, // Fork addition: a list that wraps (was a JSON array)
          },
          {
            title: '',
            field: (r) => <>
              <div style={{position: 'relative'}}>
                <PermissionGuard permission={PERM_RESOURCES}>
                  <MenuButton>
                    <MenuItem disabled={!r.device.startsWith('/dev/') || loading || containerized || !mountedHere(r.path)} onClick={() => setMountDialog({data: r, unmount: false})}>
                      <ListItemIcon>
                        <EditOutlined fontSize="small" />
                      </ListItemIcon>
                      <ListItemText >{t('global.edit')}</ListItemText>
                    </MenuItem>
                    <MenuItem disabled={loading || containerized || !mountedHere(r.path)} onClick={() => setMountDialog({data: r, unmount: true})}>
                      <ListItemIcon>
                        <DeleteOutlined fontSize="small" />
                      </ListItemIcon>
                      <ListItemText >{t('global.unmount')}</ListItemText>
                    </MenuItem>
                  </MenuButton>
                </PermissionGuard>
              </div>
            </>
          },
        ]}
      />
    </Stack> : <center>
      <CircularProgress color="inherit" size={20} />
    </center>}
  </div>
};