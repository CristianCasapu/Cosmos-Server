import React, { useEffect } from "react";
import { BorderOuterOutlined, ClockCircleOutlined, ConsoleSqlOutlined, MonitorOutlined, WindowsOutlined } from "@ant-design/icons";
import { Badge, Button, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle, IconButton, Tooltip } from "@mui/material";
import TerminalComponent from "../../../../components/terminal";

import * as API from '../../../../api';
import { useTranslation } from "react-i18next";
import { useClientInfos } from "../../../../utils/hooks";
import { PERM_ADMIN } from "../../../../utils/permissions";
import PermissionGuard from "../../../../components/permissionGuard";

// Fork addition: `open`/`onClose` let the phone header's "More" menu open the dialog (components/headerMore.jsx); `sx` hides the button there
const TerminalHeader = ({ open: openFromMenu, onClose, sx }) => {
	const [openHere, setOpenHere] = React.useState(false);
	const open = openHere || Boolean(openFromMenu);
	const setOpen = (value) => { setOpenHere(typeof value === 'function' ? value(openHere) : value); if (!value && onClose) onClose(); };
	const [status, setStatus] = React.useState(false);
  const { t, Trans } = useTranslation();
	const { hasPermission } = useClientInfos();

	useEffect(() => {
    API.getStatus().then((res) => {
      setStatus(res.data);
    });
	}, []);

	const onopen = () => {
			setOpen((prevOpen) => !prevOpen);
	};

	return <PermissionGuard permission={PERM_ADMIN}>
		<>
		<Tooltip title={t('mgmt.servapps.containers.terminal.enabled')}>
			<IconButton
					sx={{ color: 'text.primary', ...(sx || {}) }} /* Fork addition: sx */
					disableRipple
					color="secondary"
					aria-label={t('mgmt.servapps.containers.terminal.enabled')} /* Fork addition: was "open profile" */
					aria-haspopup="true"
					onClick={onopen}
			>
					<BorderOuterOutlined />
			</IconButton>
		</Tooltip>

		<Dialog open={open} onClose={() => setOpen(false)} maxWidth="md" fullWidth={true}>
			<DialogTitle>{t('mgmt.servapps.containers.terminal.enabled')}</DialogTitle>
          {(!status || !status.containerized) ? <DialogContentText>
						<div style={{display: 'flex', alignItems: 'center', justifyContent: 'center', flexDirection: 'column'}}>
							<TerminalComponent refresh={() => {}} connectButtons={
								[
									{
										label: t('global.connect'),
										onClick: (connect) => connect(() => API.terminal(), 'Server Terminal')
									},
								]} />
						</div>
					</DialogContentText> : <DialogContentText>
						<div style={{display: 'flex', alignItems: 'center', justifyContent: 'center', flexDirection: 'column'}}>
							<MonitorOutlined style={{fontSize: 100, color: 'red'}} />
							<h3>{t('mgmt.servapps.containers.terminal.disabled')}</h3>
						</div>
					</DialogContentText>}
        <DialogActions>
            <Button onClick={() => {
              setOpen(false)
            }}>Close</Button>
        </DialogActions>
		</Dialog>
		</>
	</PermissionGuard>
}

export default TerminalHeader;