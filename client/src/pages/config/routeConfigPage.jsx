import { useParams } from "react-router";
import Back from "../../components/back";
import { Alert, CircularProgress, Stack } from "@mui/material";
import PrettyTabbedView from "../../components/tabbedView/tabbedView";
import RouteManagement from "./routes/routeman";
import { useEffect, useState } from "react";
import * as API  from "../../api";
import RouteSecurity from "./routes/routeSecurity";
import RouteOverview from "./routes/routeoverview";
import RouteMetrics from "../dashboard/routeMonitoring";
import EventExplorerStandalone from "../dashboard/eventsExplorerStandalone";
import { useTranslation } from 'react-i18next';
import { managedRouteOwner } from "../../utils/routes";
import { ManagedByChip } from "../../components/routeComponents";

const RouteConfigPage = () => {
  const { t } = useTranslation();
  const { routeName } = useParams();
  const [config, setConfig] = useState(null);
  const [tunnelRoute, setTunnelRoute] = useState(null);
  
  let currentRoute = null;
  if (config) {
    currentRoute = config.HTTPConfig.ProxyConfig.Routes.find((r) => r.Name === routeName) || tunnelRoute;
  }
  const owner = managedRouteOwner(currentRoute);

  const refreshConfig = () => {
    API.config.get().then((res) => {
      setConfig(res.data);
      // Not in this node's config: it may be a tunnel advertised by other nodes.
      if (!(res.data.HTTPConfig.ProxyConfig.Routes || []).find((r) => r.Name === routeName)) {
        API.constellation.tunnels().then((tres) => {
          const found = (tres.data || []).find((tn) => tn.Route && tn.Route.Name === routeName);
          setTunnelRoute(found ? { ...found.Route, _IsTunnel: true, _from: found.Advertisers || (found.Targets || []).map((x) => x.deviceName) } : null);
        }).catch(() => setTunnelRoute(null));
      } else {
        setTunnelRoute(null);
      }
    });
  };

  useEffect(() => {
    refreshConfig();
  }, []);

  return <div>
    <h2>
      <Stack spacing={1}>
      <Stack direction="row" spacing={1} alignItems="center">
        <Back />
        <div>{routeName}</div>
      </Stack>

      {config && !currentRoute && <div>
        <Alert severity="error">{t('mgmt.servapps.routeConfig.routeNotFound')}</Alert>  
      </div>}

      {config && currentRoute && owner && <Alert severity="info" icon={false}>
        <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
          <span>{t('mgmt.urls.managedNote')}</span>
          <ManagedByChip route={currentRoute} />
        </Stack>
      </Alert>}

      {config && currentRoute && currentRoute._IsTunnel && !owner && <Alert severity="info">
        {t('mgmt.urls.tunnelEditNote', { nodes: (currentRoute._from || []).join(', ') })}
      </Alert>}

      {config && currentRoute && <PrettyTabbedView tabs={[
        {
          title: t('mgmt.servapps.overview'),
          children: <RouteOverview routeConfig={currentRoute} refreshConfig={refreshConfig} readOnly={!!owner} />
        },
        {
          title: t('mgmt.servapps.routeConfig.setup'),
          // Managed routes are read-only: the owner rebuilds them on every apply.
          children: <RouteManagement
            title={t('mgmt.servapps.routeConfig.setup')}
            submitButton={!owner}
            readOnly={!!owner}
            routeConfig={currentRoute}
            routeNames={config.HTTPConfig.ProxyConfig.Routes.map((r) => r.Name)}
            config={config}
          />
        },
        {
          title: t('global.securityTitle'),
          children:  <RouteSecurity
            routeConfig={currentRoute}
            config={config}
            readOnly={!!owner}
          />
        },
        {
          title: t('menu-items.navigation.monitoringTitle'),
          children:  <RouteMetrics routeName={routeName} />
        },
        {
          title: t('navigation.monitoring.eventsTitle'),
          children: <EventExplorerStandalone initLevel='info' initSearch={`{"object":"route@${routeName}"}`}/>
        },
      ]}/>}

      {!config && <div style={{textAlign: 'center'}}>
        <CircularProgress />
      </div>}
      </Stack>
    </h2>
  </div>
}

export default RouteConfigPage;