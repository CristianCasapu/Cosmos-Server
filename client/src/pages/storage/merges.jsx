import React from "react";
import { useEffect, useState } from "react";
import * as API  from "../../api";
import PrettyTableView from "../../components/tableView/prettyTableView";
import { DeleteButton, DeleteIconButton } from "../../components/delete";
import { CloudOutlined, CloudServerOutlined, CompassOutlined, DeleteOutlined, DesktopOutlined, EditOutlined, FolderOutlined, LaptopOutlined, MobileOutlined, ReloadOutlined, TabletOutlined } from "@ant-design/icons";
import { Alert, Button, CircularProgress, InputLabel, ListItemIcon, ListItemText, MenuItem, Stack } from "@mui/material";
import { CosmosCheckbox, CosmosFormDivider, CosmosInputText } from "../config/users/formShortcuts";
import MainCard from "../../components/MainCard";
import { Formik } from "formik";
import { LoadingButton } from "@mui/lab";
import ApiModal from "../../components/apiModal";
import ConfirmModal from "../../components/confirmModal";
import { isDomain } from "../../utils/indexs";
import UploadButtons from "../../components/fileUpload";
import MergerDialog, { MergerDialogInternal } from "./mergerDialog";
import ResponsiveButton from "../../components/responseiveButton";
import MenuButton from "../../components/MenuButton";
import { useTranslation } from 'react-i18next';
import VMWarning from "./vmWarning";
import { useClientInfos } from "../../utils/hooks";
import { PERM_RESOURCES } from "../../utils/permissions";
import PermissionGuard from "../../components/permissionGuard";

import { mountedHere } from "./mounts";
import { ForkUnmountDialog } from "./forkStorageForms"; // Fork addition

export const StorageMerges = () => {
  const { t } = useTranslation();
  const { hasPermission } = useClientInfos();
  const isAdmin = hasPermission(PERM_RESOURCES);
  const [config, setConfig] = useState(null);
  const [mounts, setMounts] = useState([]);
  const [mergeDialog, setMergeDialog] = useState(null);
  const [unmerge, setUnmerge] = useState(null); // Fork addition: removing a merge asks first, see forkStorageForms.jsx
  const [loading, setLoading] = useState(false);
  const [containerized, setContainerized] = useState(false);

  const refresh = async () => {
    setLoading(true);
    let mountsData = await API.storage.mounts.list();
    let configAsync = await API.config.get();
    let status = await API.getStatus();

    setConfig(configAsync.data);
    setMounts(mountsData.data);
    setContainerized(status.data.containerized);
    setLoading(false);
  };

  useEffect(() => {
    refresh();
  }, []);

  return <div style={{ maxWidth: "1200px", margin: "auto" }}>
    {(config) ? <>
      {unmerge && <ForkUnmountDialog merged mount={unmerge} onClose={() => setUnmerge(null)} onDone={() => { setUnmerge(null); refresh(); }} />}
      {mergeDialog && <MergerDialogInternal data={mergeDialog.data} refresh={refresh} unmount={mergeDialog.unmount} open={mergeDialog} setOpen={setMergeDialog} />}
      <Stack spacing={2}>
      {containerized && <VMWarning />}
      <div>
        <PrettyTableView
            empty={{ title: t('emptyState.merges.title', 'No merged disks'), text: t('emptyState.merges.text', 'A merge shows several disks as one folder (MergerFS). Create one with the button above.') }} /* Fork addition: what this list is for and how it fills */
          data={mounts.filter((mount) => mount.type === 'fuse.mergerfs')}
          getKey={(r) => `${r.device} - ${refresh.path}`}
          buttons={[
            <MergerDialog disk={{name: '/dev/sda'}} refresh={refresh} disabled={containerized}/>,
            <ResponsiveButton variant="outlined" startIcon={<ReloadOutlined />} onClick={() => {
              refresh();
            }}>{t('global.refresh')}</ResponsiveButton>
          ]}
          columns={[
            {
              title: t('mgmt.storage.deviceTitle'),
              field: (r) => r.device,
            },
            { 
              title: t('mgmt.storage.pathTitle'),
              field: (r) => r.path,
            },
            { 
              title: t('mgmt.storage.typeTitle'),
              field: (r) => r.type,
            },
            { 
              title: t('mgmt.storage.optionsTitle'),
              screenMin: 'md',
              field: (r) => (Array.isArray(r.opts) ? r.opts.join(', ') : JSON.stringify(r.opts)), // Fork addition: a list that wraps (was a JSON array)

            },
            {
              title: '',
              clickable:true, 
              field: (r) => <>
                <PermissionGuard permission={PERM_RESOURCES}>
                  <DeleteIconButton disabled={containerized || !mountedHere(r.path) /* Fork addition: see mounts.jsx */} onDelete={() => {
                    setUnmerge(r); // Fork addition: what uses it and a typed confirmation first (upstream unmounted at once)
                  }} />
                </PermissionGuard>
              </>
            },
          ]}
        />

      </div>  
      </Stack>
    </> : <center>
      <CircularProgress color="inherit" size={20} />
    </center>}
  </div>
};