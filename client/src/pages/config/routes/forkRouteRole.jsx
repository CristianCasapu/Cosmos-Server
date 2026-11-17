// Fork addition (plan 49): "This address is the app's …" on the URL form.
//
// What a URL is to its app decides which address the app's tile opens
// (utils/appInterfaces.js): the administration first for administrators,
// the page for users for everyone else, never an API while the app has a
// page. The server proposes the role (GET /api/dashboard, ForkRoleProposed,
// from the recipes, the known apps, the path and the URL's words); the form
// shows the proposal, lets the administrator choose another, type a label of
// their own, and say the address is kept at home on purpose. Stored as the
// route's ForkInterface (src/utils/fork_route_interface.go), checked on the
// server on every save.
import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { CosmosCheckbox, CosmosInputText, CosmosSelect } from '../users/formShortcuts';
import { useDashboardRoutes } from '../../../components/appOpen';
import { ROLES, dashboardEntryFor } from '../../../utils/appInterfaces';
import { Alert, Button, Stack } from '@mui/material';
import { API_ROUTE_SETTINGS, proposalFor } from '../../../utils/guides'; // plan 50
import * as API from '../../../api'; // plan 50

const ROLE_TEXT = {
  admin: ['mgmt.appLinks.role.admin', 'Administration'],
  user: ['mgmt.appLinks.role.user', 'Page for users'],
  api: ['mgmt.appLinks.role.api', 'API'],
  other: ['mgmt.appLinks.role.other', 'Other page'],
};

const SOURCE_TEXT = {
  recipe: ['mgmt.appLinks.source.recipe', 'from what Cosmos knows about this image'],
  app: ['mgmt.appLinks.source.app', 'from what Cosmos knows about this app'],
  path: ['mgmt.appLinks.source.path', 'from the path'],
  words: ['mgmt.appLinks.source.words', 'from the URL\'s name or description'],
  none: ['mgmt.appLinks.source.none', 'nothing more is known'],
};

// The form's values of the route's setting.
export const forkRoleValues = (route) => {
  const setting = (route && route.ForkInterface) || {};
  return {
    _ForkRole: ROLES.includes(setting.Role) ? setting.Role : '',
    _ForkLabel: String(setting.Label || ''),
    _ForkHomeOnly: setting.HomeOnly === true,
    _ForkGuide: GUIDE_SETTINGS.includes(setting.Guide) ? setting.Guide : '', // plan 50
  };
};

// The route as it is saved: ForkInterface from the form's values, left out
// when everything is as proposed.
export const withForkRole = (route) => {
  const out = { ...route };
  const role = ROLES.includes(out._ForkRole) ? out._ForkRole : '';
  const label = String(out._ForkLabel || '').trim().slice(0, 40);
  const homeOnly = out._ForkHomeOnly === true;
  const guide = GUIDE_SETTINGS.includes(out._ForkGuide) ? out._ForkGuide : ''; // plan 50
  delete out._ForkGuide;
  delete out._ForkRole;
  delete out._ForkLabel;
  delete out._ForkHomeOnly;
  if (role || label || homeOnly || guide) {
    out.ForkInterface = { ...(role ? { Role: role } : {}), ...(label ? { Label: label } : {}), ...(homeOnly ? { HomeOnly: true } : {}), ...(guide ? { Guide: guide } : {}) };
  } else {
    delete out.ForkInterface;
  }
  return out;
};

// plan 50: the app's how-to guide: as proposed, a kind of src/guides, or none
const GUIDE_SETTINGS = ['s3', 'none'];
const GUIDE_TEXT = {
  s3: ['mgmt.guide.kind.s3', 'S3-compatible storage (backups)'],
  none: ['mgmt.guide.kind.none', 'No guide'],
};

// plan 50: a NEW URL that is the API of a storage app (an app with the S3
// guide, on its service port) is proposed without Cosmos' limits: no shield,
// no throttle, no bot or referer blocking, a timeout of 24 hours. "No limits"
// is recommended only when the proxy itself keeps the URL to the home network
// (the server's answer, its own rule); for everything else keeping Cosmos'
// protection is. Nothing changes until a button is pressed and the form
// saved; existing URLs are never changed by themselves (utils/guides.js
// proposalFor decides).
const StorageApiProposalInner = ({ formik, dashboard, routeKey }) => {
  const { t } = useTranslation();
  const [answered, setAnswered] = React.useState('');
  const [homeOnly, setHomeOnly] = React.useState(null);
  const v = formik.values;
  const host = v.UseHost ? String(v.Host || '').trim() : '';
  React.useEffect(() => {
    setHomeOnly(null);
    if (!host) return undefined;
    let live = true;
    const timer = setTimeout(() => {
      API.config.hostHomeOnly(host).then((res) => { if (live) setHomeOnly(!!(res && res.data && res.data.homeOnly)); }).catch(() => { if (live) setHomeOnly(false); });
    }, 400);
    return () => { live = false; clearTimeout(timer); };
  }, [host]);
  const proposal = proposalFor({ routeKey, values: v, dashboard, homeOnly });
  if (!proposal || answered === 'keep') return null;
  const p = API_ROUTE_SETTINGS;
  const recommended = proposal.recommended;
  const none = () => {
    formik.setValues({ ...v, _SmartShield_Enabled: p.SmartShieldEnabled, ThrottlePerMinute: p.ThrottlePerMinute, BlockCommonBots: p.BlockCommonBots,
      BlockAPIAbuse: p.BlockAPIAbuse, MaxBandwith: p.MaxBandwith, Timeout: p.Timeout });
    setAnswered('none');
  };
  return <Alert severity="info" data-api-proposal={recommended} sx={{ '& .MuiAlert-message': { minWidth: 0 } }}>
    {t('mgmt.guide.form.apiProposal2', 'This address is the API of a storage app: backup tools send many requests and very large ones, which Cosmos\' shield, a limit per minute, bot or Referer blocking or a short timeout would slow, refuse (429) or cut.')}
    {' '}{recommended === 'keep'
      ? t('mgmt.guide.form.apiOpen', 'Cosmos does not keep this address to the home network (no host, or a name or address reachable from the internet): without Cosmos\' limits anyone may send it unlimited requests, and only the storage\'s keys decide what they may do. Keeping Cosmos\' protection is proposed; remove it only if backups are slowed or refused.')
      : t('mgmt.guide.form.apiHome', 'For an address used at home, no Cosmos limit is proposed: no shield, no limit per minute, no blocking of bots or of uploads without a Referer, and a timeout of 24 hours.')}
    <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ mt: 1 }}>
      <Button variant={recommended === 'none' ? 'contained' : 'outlined'} onClick={none} sx={{ minHeight: 44 }} data-api-none>
        {t('mgmt.guide.form.useApi2', 'No Cosmos limits on this address')}</Button>
      <Button variant={recommended === 'keep' ? 'contained' : 'outlined'} onClick={() => setAnswered('keep')} sx={{ minHeight: 44 }} data-api-keep>
        {t('mgmt.guide.form.keepApi', 'Keep Cosmos\' protection')}</Button>
    </Stack>
  </Alert>;
};

// A failure of the proposal never takes the URL form down: it shows nothing and logs.
class ProposalBoundary extends React.Component {
  constructor(props) { super(props); this.state = { failed: false }; }
  static getDerivedStateFromError() { return { failed: true }; }
  componentDidCatch(error) { console.error('The storage proposal of the URL form failed', error); }
  render() { return this.state.failed ? null : this.props.children; }
}

export const StorageApiProposal = (props) => <ProposalBoundary><StorageApiProposalInner {...props} /></ProposalBoundary>;

export const ForkRouteRole = ({ formik, routeKey }) => {
  const { t } = useTranslation();
  const dashboard = useDashboardRoutes();
  // by the route's key, as GET /api/config and GET /api/dashboard both give it; a disabled URL is in the
  // dashboard for those who may edit URLs (marked Disabled), so its proposal is there too
  const known = dashboardEntryFor(dashboard, routeKey);
  const proposed = known && ROLES.includes(known.ForkRoleProposed) ? known.ForkRoleProposed : '';
  const source = known && known.ForkRoleSource !== 'set' ? known.ForkRoleSource : '';
  const proposal = proposed
    ? t('mgmt.appLinks.form.proposed', 'As proposed: {{role}}', { role: t(...ROLE_TEXT[proposed]) }) + (source && SOURCE_TEXT[source] ? ' (' + t(...SOURCE_TEXT[source]) + ')' : '')
    : t('mgmt.appLinks.form.proposedNew', 'As proposed when it is saved');
  const options = [['', proposal], ...ROLES.map((role) => [role, t(...ROLE_TEXT[role])])];
  const shownRole = formik.values._ForkRole || proposed || 'user';
  const guideProposed = known && GUIDE_TEXT[known.ForkGuideProposed] ? known.ForkGuideProposed : '';
  const guideProposal = !known ? t('mgmt.appLinks.form.proposedNew', 'As proposed when it is saved') : guideProposed
    ? t('mgmt.guide.form.proposed', 'As proposed: {{guide}} (from what Cosmos knows about this image)', { guide: t(...GUIDE_TEXT[guideProposed]) })
    : t('mgmt.guide.form.proposedNone', 'As proposed: none (Cosmos does not know this image)');
  return <>
    {!routeKey && <StorageApiProposal formik={formik} dashboard={dashboard} routeKey={routeKey} />}
    <CosmosSelect
      name="_ForkRole"
      label={t('mgmt.appLinks.form.role', 'This address is the app\'s')}
      helperText={t('mgmt.appLinks.form.roleHelp', 'The app\'s tile opens its administration for administrators and its page for users for everyone else, on the address that works from where they are. An API is listed, never opened by the tile.')}
      formik={formik}
      options={options}
    />
    <CosmosInputText
      name="_ForkLabel"
      label={t('mgmt.appLinks.form.label', 'Its name in the list of addresses (optional)')}
      placeholder={t(...ROLE_TEXT[shownRole])}
      formik={formik}
    />
    <CosmosCheckbox
      name="_ForkHomeOnly"
      label={t('mgmt.appLinks.form.homeOnly', 'Kept at home on purpose')}
      helperText={t('mgmt.appLinks.form.homeOnlyHelp', 'Seen from the internet, the app\'s tile says so and does not propose an internet address for it.')}
      formik={formik}
    />
    <CosmosSelect
      name="_ForkGuide"
      label={t('mgmt.guide.form.label', 'How-to guide of the app')}
      helperText={t('mgmt.guide.form.help', '"How to use it" on the app\'s page and in its tile\'s menu: examples with the app\'s own addresses. Proposed from what Cosmos knows about the image; choose one if Cosmos did not recognise the app.')}
      formik={formik}
      options={[['', guideProposal], ...GUIDE_SETTINGS.map((g) => [g, t(...GUIDE_TEXT[g])])]}
    />
  </>;
};
