import { SettingOutlined } from "@ant-design/icons";
import { Chip, Tooltip } from "@mui/material";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { getOrigin, getFullOrigin } from "../utils/routes";
import { useTheme } from '@mui/material/styles';
import StatusDot from "./statusDot";
import { routeLink } from "../utils/appLinks"; // Fork addition: links carry the port
import { UnreachableDialog, useRouteReach } from "./appOpen"; // Fork addition (plan 49): an address that does not open from here says so
import { appTile, containerApp } from "../utils/appInterfaces"; // Fork addition (plan 49)

const isDemo = import.meta.env.MODE === 'demo';

const probeRoute = async (route) => {
  // demo routes are fake hostnames: a real probe would mark them all offline
  if (isDemo) return 'online';

  const origin = routeLink(route, window.location); // Fork addition: with the port Cosmos is on (upstream: getFullOrigin, without it)
  try {
    const res = await fetch(origin + (origin.includes('?') ? '&' : '?') + '__cosmos_probe=1', {
      method: 'HEAD',
      mode: 'cors',
      credentials: 'include',
      redirect: 'manual',
      cache: 'no-store',
    });
    return res.headers.get('X-Cosmos-Container') === 'sleeping' ? 'sleeping' : 'online';
  } catch (e) {
    try {
      await fetch(origin, { method: 'HEAD', mode: 'no-cors' });
      return 'online';
    } catch (e2) {
      return 'offline';
    }
  }
};

const HostChip = ({route, settings, style, ellipsis}) => {
  const { t } = useTranslation();
  const theme = useTheme();
  const isDark = theme.palette.mode === 'dark';
  const [status, setStatus] = useState(null);
  const url = getOrigin(route)
  // Fork addition (plan 49): from where the visitor is, does this address open? If not, the chip explains instead of opening it
  const { item: reach, ctx } = useRouteReach(route);
  const [explain, setExplain] = useState(false);
  const unreachable = !!reach && (reach.reach === 'home-only' || reach.reach === 'vpn-only');

  useEffect(() => {
    let cancelled = false;
    probeRoute(route).then((s) => { if (!cancelled) setStatus(s); });
    return () => { cancelled = true; };
  }, [url]);

  const dot = status === 'sleeping'
    ? <Tooltip title={t('global.containerSleeping')}><StatusDot status="unknown" hollow size={8} style={{ marginRight: 6 }} /></Tooltip>
    : <StatusDot status={status == null ? "unknown" : status === 'online' ? "success" : "error"} size={8} style={{ marginRight: 6 }} />;

  return <><Chip
    label={<>{dot}{url}</>}
    color="primary"
    variant="outlined"
    style={{
      paddingRight: '4px',
      // textDecoration: isOnline ? 'none' : 'underline wavy red',
      ...style,
      ...(ellipsis ? { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '250px' } : {})
    }}
    onClick={() => {
      if (unreachable) { setExplain(true); return; } // Fork addition (plan 49)
      // Fork addition: with the port Cosmos is on (upstream left it out for a URL with a host of its own)
      window.open(routeLink(route, window.location), '_blank', 'noopener,noreferrer'); // Fork addition: the app gets no handle on the panel's tab
    }}
    data-unreachable={unreachable ? 'true' : undefined}
    onDelete={settings ? () => {
      window.open('/cosmos-ui/config-url/'+route.Name, '_blank', 'noopener,noreferrer');
    } : null}
    deleteIcon={settings ? <SettingOutlined /> : null}
  />
  {/* Fork addition (plan 49): what the address is for and where it opens, with Copy */}
  {explain && <UnreachableDialog tile={{ ...appTile(containerApp('', [route]), ctx), container: '' }} ctx={ctx} onClose={() => setExplain(false)} />}
  </>
}

export default HostChip;