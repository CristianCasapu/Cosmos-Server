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
  };
};

// The route as it is saved: ForkInterface from the form's values, left out
// when everything is as proposed.
export const withForkRole = (route) => {
  const out = { ...route };
  const role = ROLES.includes(out._ForkRole) ? out._ForkRole : '';
  const label = String(out._ForkLabel || '').trim().slice(0, 40);
  const homeOnly = out._ForkHomeOnly === true;
  delete out._ForkRole;
  delete out._ForkLabel;
  delete out._ForkHomeOnly;
  if (role || label || homeOnly) {
    out.ForkInterface = { ...(role ? { Role: role } : {}), ...(label ? { Label: label } : {}), ...(homeOnly ? { HomeOnly: true } : {}) };
  } else {
    delete out.ForkInterface;
  }
  return out;
};

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
  return <>
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
  </>;
};
