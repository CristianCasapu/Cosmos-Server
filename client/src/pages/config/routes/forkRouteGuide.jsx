// Fork addition: the simple view of the URL form. Upstream's form asks a
// dozen things before the first one that matters; this view asks for the
// address, proposes the rest and says where each proposal comes from: which
// port of the app (the app's usual one, or the only one it has), how the app
// speaks (http, or https on its own HTTPS ports), who may open the address
// (one choice in place of switches on two tabs), the name it is saved under.
// Upstream's fields are its `children`: they stay as they are and are shown
// as "all settings", the door for every value this view does not ask. Both
// views write the same Formik values, so switching loses nothing. What the
// view knows is in utils/routeGuide.js.
import * as React from 'react';
import {
  Alert,
  Autocomplete,
  Button,
  FormHelperText,
  Grid,
  InputLabel,
  Stack,
  TextField,
  Typography,
} from '@mui/material';
import { ControlOutlined, EditOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import * as API from '../../../api';
import { CosmosCheckbox, CosmosFormDivider, CosmosInputText } from '../users/formShortcuts';
import HostnameSuggestions, { localAddress } from '../../../components/hostnameSuggestions';
import { ChoiceChips, ProposalNote } from '../../../components/formChoices';
import defaultport from '../../servapps/defaultport.json';
import {
  accessFields, accessOf, buildTarget, containerPorts, describeAddress, freeName, guideFits,
  proposePort, schemeForPort, targetParts, tidyAddress,
} from '../../../utils/routeGuide';

const nameOf = (container) => String((container && ((container.Names && container.Names[0]) || container.Name)) || '').replace(/^\//, '');
const imageOf = (container) => String((container && (container.Image || (container.Config && container.Config.Image))) || '');
// "lscr.io/linuxserver/radarr:latest" → "radarr"
const shortImage = (image) => String(image || '').split('@')[0].split('/').pop().split(':')[0];

// Where a proposed port comes from (the `source` of utils/routeGuide.js'
// proposePort), as a sentence. Shared with the "address for every app" dialog.
export const portSourceText = (t, proposal, image) => {
  const port = proposal.port;
  const app = shortImage(image);
  return {
    usual: t('mgmt.urls.guide.port.usual', 'Proposed: {{port}} is the usual web port of {{app}}.', { port, app }),
    only: t('mgmt.urls.guide.port.only', 'Proposed: {{port}} is the only port this app has.', { port }),
    common: t('mgmt.urls.guide.port.common', 'Proposed: {{port}} is the most likely web port among the app\'s ports.', { port }),
    first: t('mgmt.urls.guide.port.first', 'None of the app\'s ports is a common web port; {{port}} is the first. The app\'s documentation says which one is its web page.', { port }),
    usualOnly: t('mgmt.urls.guide.port.usualOnly', 'The app declares no port. Proposed: {{port}}, the usual web port of {{app}}.', { port, app }),
    none: t('mgmt.urls.guide.port.none', 'The app declares no port: type the one its web page is on (the app\'s documentation says which).'),
  }[proposal.source] || '';
};

// One line on the name the app sees. Cosmos passes the address the visitor
// typed on to the app unchanged (src/proxy/routeTo.go); apps that only answer
// names they know refuse it until the name is allowed in their own settings.
export const HostSeenHint = ({ values }) => {
  const { t } = useTranslation();
  const overwrite = String((values && values.OverwriteHostHeader) || '').trim();
  const host = overwrite || String((values && values.UseHost && values.Host) || '').trim();
  const mode = (values && values.Mode) || 'SERVAPP';
  if (!host || (mode !== 'SERVAPP' && mode !== 'PROXY')) {
    return null;
  }
  return <Grid item xs={12}>
    <Typography variant="caption" color="textSecondary" component="div" sx={{ overflowWrap: 'anywhere' }}>
      {overwrite
        ? t('mgmt.urls.guide.hostSeenOverwrite',
          'The app receives its requests under the name {{host}} (set in "Overwrite Host Header"), not under the address above.', { host })
        : t('mgmt.urls.guide.hostSeen',
          'The app receives its requests under the name {{host}}. Some apps only answer to names they know (Radarr and Sonarr say "Invalid Hostname", others speak of allowed or trusted hosts): if the app refuses, add {{host}} in the app\'s own settings.', { host })}
    </Typography>
  </Grid>;
};

// Upstream's messages for the address, in words that say what to type.
const plainErrors = (errors, t) => {
  const known = {
    'Host is required': t('mgmt.urls.guide.error.hostRequired', 'Enter the address, for example radarr.local'),
    'Host must be full domain ([sub.]domain.com) or an IP (IPs won\'t work with Let\'s Encrypt!)':
      t('mgmt.urls.guide.error.hostDot', 'Write the whole name with a dot, such as radarr.local or films.example.org'),
    'Do not add the protocol here!': t('mgmt.urls.guide.error.hostProtocol', 'Leave out http:// and https://: only the name'),
    'Name is required': t('mgmt.urls.guide.error.nameRequired', 'Give the URL a name, for example the app\'s'),
    'Target is required': t('mgmt.urls.guide.error.targetRequired', 'Say where the address leads'),
  };
  const plain = { ...errors };
  Object.keys(plain).forEach((field) => {
    if (typeof plain[field] === 'string' && known[plain[field]]) {
      plain[field] = known[plain[field]];
    }
  });
  return plain;
};

const ForkRouteGuide = ({ formik, config, routeNames, TargetContainer, lockTarget, isNew, readOnly, hostError, onHostChange, addressExtra, children }) => {
  const { t } = useTranslation();
  const [view, setView] = React.useState('simple');
  const [containers, setContainers] = React.useState(null);
  const [otherPort, setOtherPort] = React.useState(false);
  const [showName, setShowName] = React.useState(false);
  // what is still as proposed, and so may be proposed again when the address or the app changes
  const proposed = React.useRef({ name: !!isNew, description: !!isNew, host: !!isNew && !lockTarget, scheme: true });

  const values = formik.values;
  const mode = values.Mode || 'SERVAPP';
  const parts = targetParts(values.Target);
  // Several changes made in one go (an effect after another, a pick that sets
  // three fields) are gathered here and written as one set of values: Formik
  // works each change out from the values of the last render, so the later
  // one would otherwise undo the earlier, and the dialogs take the URL from
  // the last set they are shown.
  const latest = React.useRef(values);
  latest.current = values;
  const change = (changes) => {
    latest.current = { ...latest.current, ...changes };
    formik.setValues(latest.current);
  };
  // for what follows a change the field itself writes (typing in it): once that change is in
  const changeAfter = (changes) => setTimeout(() => change(changes()), 0);

  // a new URL has an address of its own: that is what this form is for
  const fit = guideFits(isNew && !values.UsePathPrefix ? { ...values, UseHost: true } : values);
  const simple = !readOnly && view === 'simple' && fit.fits;

  // ---- the app the address leads to
  const needList = !readOnly && mode === 'SERVAPP' && !TargetContainer;
  React.useEffect(() => {
    if (!needList || containers) {
      return undefined;
    }
    let live = true;
    API.docker.list().then((res) => live && setContainers(res.data || [])).catch(() => live && setContainers([]));
    return () => { live = false; };
  }, [needList]);

  const container = TargetContainer || (containers || []).find((one) => nameOf(one) === parts.host) || null;
  const appName = TargetContainer ? nameOf(TargetContainer) : parts.host;
  const ports = containerPorts(container);
  const proposal = React.useMemo(() => proposePort(imageOf(container), ports, defaultport), [imageOf(container), ports.join(',')]);

  // ---- what a new URL starts with
  React.useEffect(() => {
    if (!isNew) {
      return;
    }
    const start = {};
    if (!values.UseHost && !values.UsePathPrefix) {
      start.UseHost = true;
    }
    if (lockTarget && (routeNames || []).includes(values.Name)) {
      start.Name = freeName(values.Name, routeNames);
    }
    if (lockTarget && values.Host) {
      start.Description = describeAddress(appName || values.Name, values.Host);
    }
    if (Object.keys(start).length > 0) {
      change(start);
    }
  }, []);

  // the port: proposed while none is set, never in place of one that is
  React.useEffect(() => {
    // (not while a port is being typed: an emptied field is about to be filled)
    if (readOnly || otherPort || mode !== 'SERVAPP' || !container || !appName || parts.port !== '' || !proposal.port) {
      return;
    }
    const scheme = schemeForPort(proposal.port);
    change({
      Target: buildTarget({ scheme, host: appName, port: proposal.port }),
      AcceptInsecureHTTPSTarget: scheme === 'https',
    });
  }, [mode, appName, proposal.port, parts.port, !!container, otherPort]);

  // What follows the address while it is still as proposed: the description
  // (local or internet), and for a URL to another device its name ("router"
  // for router.local), which has no app to be named after.
  const withAddress = (host) => {
    const changes = {};
    let name = appName || values.Name;
    if (isNew && proposed.current.name && mode === 'PROXY') {
      const label = String(host || '').split(/[.:]/)[0].replace(/[^a-zA-Z0-9-]/g, '');
      if (label) {
        name = freeName(label, routeNames);
        changes.Name = name;
      }
    }
    if (isNew && proposed.current.description) {
      changes.Description = describeAddress(name, host);
    }
    return changes;
  };

  const setHost = (host) => {
    proposed.current.host = false;
    change({ Host: host, UseHost: true, ...withAddress(host) });
    onHostChange && onHostChange(host);
  };

  const setPort = (port) => {
    const scheme = proposed.current.scheme || !parts.scheme ? schemeForPort(port) : parts.scheme;
    change({
      Target: buildTarget({ scheme, host: appName, port }),
      ...(proposed.current.scheme ? { AcceptInsecureHTTPSTarget: scheme === 'https' } : {}),
    });
  };

  const setScheme = (scheme) => {
    proposed.current.scheme = false;
    change({
      Target: buildTarget({ scheme, host: appName, port: parts.port }),
      // an app's own HTTPS certificate is its own: there is nothing to check it against
      AcceptInsecureHTTPSTarget: scheme === 'https',
    });
  };

  const pickApp = (picked) => {
    const name = nameOf(picked);
    if (!name) {
      return;
    }
    const next = proposePort(imageOf(picked), containerPorts(picked), defaultport);
    const scheme = schemeForPort(next.port);
    proposed.current.scheme = true;
    setOtherPort(false);
    const changes = {
      Target: buildTarget({ scheme, host: name, port: next.port || '' }),
      AcceptInsecureHTTPSTarget: scheme === 'https',
    };
    if (isNew && proposed.current.name) {
      changes.Name = freeName(name, routeNames);
    }
    let host = values.Host;
    if (isNew && (proposed.current.host || !values.Host)) {
      try {
        host = localAddress(name, config);
        proposed.current.host = true;
        changes.Host = host;
        changes.UseHost = true;
        onHostChange && onHostChange(host);
      } catch (e) {
        // the configuration is not loaded: the address is left to be typed
      }
    }
    if (isNew && proposed.current.description) {
      changes.Description = describeAddress(name, host);
    }
    change(changes);
  };

  const setMode = (next) => {
    if (next === 'OTHER') {
      setView('all');
      return;
    }
    if (next === mode) {
      return;
    }
    proposed.current.scheme = true;
    change({ Mode: next, Target: next === 'PROXY' ? 'http://' : '', AcceptInsecureHTTPSTarget: false });
  };

  // ---- all settings: upstream's form as it is
  if (!simple) {
    const reasons = {
      mode: t('mgmt.urls.guide.why.mode', 'it serves a folder or redirects'),
      socket: t('mgmt.urls.guide.why.socket', 'it forwards a port that is not a web address'),
      path: t('mgmt.urls.guide.why.path', 'it answers on a path'),
      noHost: t('mgmt.urls.guide.why.noHost', 'it has no address of its own'),
      tunnel: t('mgmt.urls.guide.why.tunnel', 'it goes through a tunnel'),
      access: t('mgmt.urls.guide.why.access', 'its access settings do not match one of the three choices'),
    };
    return <>
      {!readOnly && <Grid item xs={12}>
        {fit.fits
          ? <Stack direction="row" justifyContent="flex-end">
            <Button size="small" startIcon={<ThunderboltOutlined />} onClick={() => setView('simple')}>
              {t('mgmt.urls.guide.toSimple', 'Back to the simple view')}
            </Button>
          </Stack>
          : <Alert severity="info">
            {t('mgmt.urls.guide.notSimple', 'All settings are shown, because the simple view cannot show this URL: {{reasons}}.',
              { reasons: fit.why.map((why) => reasons[why] || why).join('; ') })}
          </Alert>}
      </Grid>}
      {children}
      {addressExtra && <Grid item xs={12}>{addressExtra}</Grid>}
    </>;
  }

  // ---- the simple view
  const plain = { ...formik, errors: plainErrors(formik.errors, t) };
  const access = accessOf(values);
  const portNumber = Number(parts.port);
  const knownPort = ports.includes(portNumber);
  // typed when asked for, when it is none of the app's ports, and when the app has none to pick from
  const typedPort = otherPort || (parts.port !== '' && !knownPort) || (!!container && ports.length === 0);
  let portNote = '';
  if (container) {
    if (parts.port === '' || portNumber === proposal.port) {
      portNote = portSourceText(t, proposal, imageOf(container));
    } else if (!knownPort) {
      portNote = t('mgmt.urls.guide.port.unknown',
        'The app does not declare port {{port}}. If nothing answers there, the address shows "502 Bad Gateway".', { port: parts.port });
    }
  } else if (mode === 'SERVAPP' && appName && containers) {
    portNote = t('mgmt.urls.guide.port.noApp', 'No app named {{app}} is on this server right now, so its ports are not known.', { app: appName });
  }
  const targetError = formik.errors.Target && (formik.touched.Target || formik.touched.Host || formik.submitCount > 0)
    ? (parts.port === '' ? t('mgmt.urls.guide.error.port', 'Pick the port of the app') : plain.errors.Target)
    : null;
  const nameError = formik.errors.Name;

  return <>
    <Grid item xs={12}>
      <Stack direction="row" justifyContent="space-between" alignItems="center" spacing={1} useFlexGap flexWrap="wrap">
        <Typography variant="body2" color="textSecondary">
          {t('mgmt.urls.guide.intro', 'Pick the address. The rest is proposed; change what does not fit.')}
        </Typography>
        <Button size="small" startIcon={<ControlOutlined />} onClick={() => setView('all')}>
          {t('mgmt.urls.guide.toAll', 'All settings')}
        </Button>
      </Stack>
    </Grid>

    {!lockTarget && <ChoiceChips
      name="guide-mode"
      label={t('mgmt.urls.guide.mode.label', 'What should the address open?')}
      value={mode}
      onChange={setMode}
      options={[
        { value: 'SERVAPP', label: t('mgmt.urls.guide.mode.app', 'An app on this server'), hint: t('mgmt.urls.guide.mode.appHint', 'A container that runs here, such as Jellyfin or Radarr.') },
        { value: 'PROXY', label: t('mgmt.urls.guide.mode.proxy', 'Another device or address'), hint: t('mgmt.urls.guide.mode.proxyHint', 'A web page elsewhere on your network, such as the router or a NAS.') },
        { value: 'OTHER', label: t('mgmt.urls.guide.mode.other', 'Something else…'), hint: '' },
      ]}
    />}

    {mode === 'SERVAPP' && !lockTarget && <Grid item xs={12}>
      <Stack spacing={1}>
        <InputLabel htmlFor="guide-app" required>{t('mgmt.urls.guide.app.label', 'App')}</InputLabel>
        <Autocomplete
          id="guide-app"
          options={containers || []}
          loading={containers === null}
          value={container}
          isOptionEqualToValue={(option, value) => nameOf(option) === nameOf(value)}
          getOptionLabel={(option) => nameOf(option)}
          renderOption={(props, option) => <li {...props} key={nameOf(option)}>
            <Stack sx={{ minWidth: 0 }}>
              <span>{nameOf(option)}</span>
              <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>{imageOf(option)}</Typography>
            </Stack>
          </li>}
          onChange={(event, picked) => picked && pickApp(picked)}
          renderInput={(params) => <TextField {...params} placeholder={t('mgmt.urls.guide.app.placeholder', 'Pick the app the address opens')} />}
        />
        {!container && appName && containers && <FormHelperText>{portNote}</FormHelperText>}
      </Stack>
    </Grid>}

    <CosmosInputText
      name="Host"
      label={t('mgmt.urls.guide.address.label', 'Address')}
      placeholder="radarr.local"
      formik={plain}
      required
      helperText={t('mgmt.urls.guide.address.help', 'What people type to open it, without http://. For example radarr.local, or films.example.org with a domain of yours.')}
      onChange={(event) => {
        proposed.current.host = false;
        const host = event.target.value;
        if (Object.keys(withAddress(host)).length > 0) {
          changeAfter(() => withAddress(host));
        }
        onHostChange && onHostChange(event.target.value);
      }}
      inputProps={{
        autoCapitalize: 'none',
        autoCorrect: 'off',
        spellCheck: false,
        onBlur: (event) => {
          // a pasted link becomes the name it contains
          const tidy = tidyAddress(event.target.value);
          if (tidy !== event.target.value) {
            setHost(tidy);
          }
        },
      }}
    />
    <Grid item xs={12} sx={{ pt: '8px !important' }}>
      <HostnameSuggestions inline name={appName || values.Name} value={values.Host} config={config} onPick={setHost} />
    </Grid>
    {hostError && <Grid item xs={12}>
      <Alert color="warning">{hostError}</Alert>
    </Grid>}
    {addressExtra && <Grid item xs={12}>{addressExtra}</Grid>}

    {mode === 'SERVAPP' && (lockTarget || appName) && <>
      <ChoiceChips
        name="Target"
        required
        label={lockTarget
          ? t('mgmt.urls.guide.port.labelOf', 'Port of {{app}} the address leads to', { app: appName })
          : t('mgmt.urls.guide.port.label', 'Port of the app')}
        value={typedPort ? 'other' : portNumber}
        onChange={(picked) => {
          if (picked === 'other') {
            setOtherPort(true);
          } else {
            setOtherPort(false);
            setPort(picked);
          }
        }}
        options={[
          ...ports.map((port) => ({ value: port, label: String(port) })),
          { value: 'other', label: ports.length > 0 ? t('mgmt.urls.guide.port.other', 'Another port…') : t('mgmt.urls.guide.port.type', 'Type the port') },
        ]}
        error={targetError}
      >
        {typedPort && <TextField
          size="small"
          sx={{ maxWidth: 160 }}
          inputProps={{ inputMode: 'numeric', 'aria-label': t('mgmt.urls.guide.port.label', 'Port of the app') }}
          placeholder="8080"
          value={parts.port}
          onChange={(event) => setPort(event.target.value.replace(/\D/g, '').slice(0, 5))}
        />}
        <ProposalNote>{portNote}</ProposalNote>
      </ChoiceChips>

      <ChoiceChips
        name="guide-scheme"
        label={t('mgmt.urls.guide.scheme.label', 'The app speaks')}
        value={parts.scheme === 'https' ? 'https' : 'http'}
        onChange={setScheme}
        options={[
          { value: 'http', label: 'http', hint: t('mgmt.urls.guide.scheme.http', 'What nearly every app speaks. It is only how Cosmos talks to the app inside the server.') },
          { value: 'https', label: 'https', hint: t('mgmt.urls.guide.scheme.https', 'For a port that is the app\'s own HTTPS port (443, 8443…). The app\'s self-made certificate is accepted.') },
        ]}
      />
    </>}

    {mode === 'PROXY' && <>
      <CosmosInputText
        name="Target"
        label={t('mgmt.urls.guide.proxy.label', 'Where it leads')}
        placeholder="http://192.168.1.1:8080"
        formik={plain}
        required
        helperText={t('mgmt.urls.guide.proxy.help', 'The address of the other page, with http:// or https:// and its port. For example http://192.168.1.1:8080.')}
      />
      {String(values.Target || '').startsWith('https://') && <CosmosCheckbox
        name="AcceptInsecureHTTPSTarget"
        label={t('mgmt.urls.guide.proxy.insecure', 'Accept its certificate without checking it')}
        helperText={t('mgmt.urls.guide.proxy.insecureHelp', 'Needed for devices with a self-made certificate: most routers, NAS and printers.')}
        formik={formik}
      />}
    </>}

    <ChoiceChips
      name="guide-access"
      label={t('mgmt.urls.guide.access.label', 'Who may open it')}
      value={access}
      onChange={(choice) => change(accessFields(choice))}
      options={[
        { value: 'anyone', label: t('mgmt.urls.guide.access.anyone', 'Anyone with the address'), hint: t('mgmt.urls.guide.access.anyoneHint', 'Cosmos asks nothing: the app\'s own sign-in is what protects it. Fine for apps with their own accounts.') },
        { value: 'users', label: t('mgmt.urls.guide.access.users', 'People signed in to Cosmos'), hint: t('mgmt.urls.guide.access.usersHint', 'Cosmos asks for its sign-in first. For apps without a sign-in of their own.') },
        { value: 'admins', label: t('mgmt.urls.guide.access.admins', 'Cosmos administrators only'), hint: t('mgmt.urls.guide.access.adminsHint', 'Cosmos asks for its sign-in and lets administrators through only.') },
      ]}
    />

    <HostSeenHint values={values} />

    <Grid item xs={12}>
      {showName || nameError
        ? <Grid container spacing={2}>
          <CosmosInputText
            name="Name"
            label={t('mgmt.urls.guide.name.label', 'Name in the list of URLs')}
            formik={plain}
            required
            onChange={() => { proposed.current.name = false; }}
          />
          <CosmosInputText
            name="Description"
            label={t('global.description')}
            formik={formik}
            onChange={() => { proposed.current.description = false; }}
          />
        </Grid>
        : <Stack direction="row" alignItems="center" spacing={1} useFlexGap flexWrap="wrap">
          <Typography variant="caption" color="textSecondary" sx={{ overflowWrap: 'anywhere' }}>
            {t('mgmt.urls.guide.summary',
              'Saved as "{{name}}", with the protection against abuse (Smart Shield) switched on.', { name: values.Name })}
          </Typography>
          <Button size="small" startIcon={<EditOutlined />} onClick={() => setShowName(true)}>
            {t('mgmt.urls.guide.name.change', 'Change the name')}
          </Button>
        </Stack>}
    </Grid>
  </>;
};

export default ForkRouteGuide;
