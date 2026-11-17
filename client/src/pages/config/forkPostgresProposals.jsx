// Fork addition (plan 47): the monitoring database form (configExternal.jsx)
// proposes the PostgreSQL containers that run on this server and tests the
// connection before saving. A proposal fills the server, the database and the
// user, only where the form holds nothing of the administrator's own (logic:
// utils/postgresProposal.js); the password is never part of it: "Use its
// password" asks the server for it on a click (the recorded reveal of the
// sign-in details, which needs a recent password confirmation: the prompt
// opens by itself when it is old).
//
// Plan 51: the proposals say whether they answer from Cosmos; the server
// field offers them as choices with "Type it myself"; the password field
// says before the save when a masked password would be refused; a save the
// server refused because the database does not answer is said here with
// "Save anyway"; a save that moves the store says what happens to the
// history first (StoreMoveDialog); the store in use is said when it is the
// local file meanwhile (MonitoringStoreLine, also on the monitoring page).
import * as React from 'react';
import { Alert, Button, Chip, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle, FormHelperText, Grid, InputLabel, Stack, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import * as API from '../../api';
import ProposalBox from '../../components/proposalBox';
import { takeProposal, replaceWithProposal, holdsProposal, testPayload, postgresHostProblem } from '../../utils/postgresProposal';
import { choiceValues, containerAddressSentence, containerAddressWarning, hostChoices, maskedPasswordStale, moveSentence, pickedChoice, requestSaveAnyway, storeStateLine } from '../../utils/monitoringStoreForm';

const touch = { minHeight: 44 };

// { proposals: [...], error } once the server answered, null before.
export const usePostgresProposals = () => {
  const [state, setState] = React.useState(null);
  React.useEffect(() => {
    let live = true;
    API.config.postgresProposals()
      .then((res) => { if (live) setState({ proposals: (res && res.data && res.data.proposals) || [] }); })
      .catch((e) => { if (live) setState({ proposals: [], error: (e && e.message) || String(e) }); });
    return () => { live = false; };
  }, []);
  return state;
};

// One line above the folded section: there is something to propose in it.
export const PostgresFoundNote = ({ proposals }) => {
  const { t } = useTranslation();
  const found = (proposals && proposals.proposals) || [];
  if (!found.length) return null;
  return <FormHelperText data-fork-postgres-found>
    {t('mgmt.config.general.monitoringDb.fork.found', 'A PostgreSQL container runs on this server ({{names}}): open "Postgres" below to use it.', { names: found.map((p) => p.container).join(', ') })}
  </FormHelperText>;
};

const HOST_SOURCE = {
  published: ['mgmt.config.general.monitoringDb.fork.hostPublished', 'The port it publishes on this server.'],
  'container-network': ['mgmt.config.general.monitoringDb.fork.hostNetwork', 'Its name on the Docker network: Cosmos runs in a container and reaches it by name when both are on the same network.'],
  address: ['mgmt.config.general.monitoringDb.fork.hostAddress3', 'The container\'s own address on Docker\'s network: it works now, but it changes when the container is made again (an update, an edit) or the containers start in another order. Better: publish its port 5432 on 127.0.0.1 (in the container\'s Network tab), then use 127.0.0.1:5432.'],
  none: ['mgmt.config.general.monitoringDb.fork.hostNone', 'Cosmos runs in a container and shares no Docker network with it: open the container\'s page and use "Let these apps reach it" to put Cosmos on its network, or publish its port 5432.'],
};
// Plan 51: a port published on one address of the server only
const HOST_LAN = ['mgmt.config.general.monitoringDb.fork.hostLan', 'Its port is published on this address of the server only (not on 127.0.0.1).'];

const reachText = (t, reach) => (reach === 'answers'
  ? t('mgmt.config.general.monitoringDb.fork.reachAnswers', 'Answers from Cosmos.')
  : t('mgmt.config.general.monitoringDb.fork.reachSilent', 'Does not answer from Cosmos: check that it runs and how its port is published.'));

const OneProposal = ({ proposal, formik, marks, setMarks }) => {
  const { t } = useTranslation();
  const [held, setHeld] = React.useState(false);
  const [password, setPassword] = React.useState(''); // '', 'asking', 'set', 'failed', 'confirm'
  const values = formik.values;
  const inUse = holdsProposal(values, proposal);

  const use = () => {
    const out = takeProposal(values, proposal, marks);
    setMarks(out.marks);
    if (Object.keys(out.values).length) formik.setValues({ ...values, ...out.values }); // one setValues: Formik loses the first of two calls
    setHeld(out.held.length > 0);
  };
  const replace = () => {
    const out = replaceWithProposal(values, proposal);
    setMarks(out.marks);
    formik.setValues({ ...values, ...out.values });
    setHeld(false);
  };
  const usePassword = () => {
    setPassword('asking');
    API.signIn.reveal(proposal.container, proposal.passwordSecret, 'copy')
      .then((res) => {
        const value = String((res && res.data && res.data.value) || '');
        formik.setFieldValue('PostgresPassword', value);
        setPassword(value ? 'set' : 'failed');
      })
      // the password prompt opens by itself for a POST refused for want of the confirmation (utils/sudoPrompt.js)
      .catch((e) => setPassword(e && e.code === 'HTTP008' ? 'confirm' : 'failed'));
  };

  const lan = proposal.hostSource === 'published' && proposal.hostNote && !/^(127\.|\[::1\])/.test(proposal.host || '');
  const [sourceKey, sourceText] = lan ? HOST_LAN : (HOST_SOURCE[proposal.hostSource] || HOST_SOURCE.address);
  const noHost = !proposal.host;
  return <ProposalBox
    severity={noHost || proposal.reach === 'silent' ? 'warning' : 'info'}
    title={t('mgmt.config.general.monitoringDb.fork.title', 'PostgreSQL in the container {{name}}', { name: proposal.container })}
    source={t('mgmt.config.general.monitoringDb.fork.source', 'From the container\'s settings ({{image}}). The password is not part of this proposal.', { image: proposal.image })}
    inUse={inUse}
    inUseText={t('mgmt.config.general.monitoringDb.fork.inUse', 'The fields below hold these values. Save the settings to use this database.')}
    heldText={held ? t('mgmt.config.general.monitoringDb.fork.held', 'The fields below hold values of your own, so they were left as they are.') : ''}
    onUse={noHost ? undefined : use}
    useLabel={t('mgmt.config.general.monitoringDb.fork.use', 'Use these')}
  >
    <Typography variant="body2" component="div" data-fork-postgres-proposal={proposal.container}>
      {noHost ? t('mgmt.config.general.monitoringDb.fork.lineNoHost', 'Server: none that Cosmos can reach') : t('mgmt.config.general.monitoringDb.fork.lineHost', 'Server: {{host}}', { host: proposal.host })}
      {proposal.database ? ' · ' + t('mgmt.config.general.monitoringDb.fork.lineDatabase', 'database: {{database}}', { database: proposal.database }) : ''}
      {proposal.user ? ' · ' + t('mgmt.config.general.monitoringDb.fork.lineUser', 'user: {{user}}', { user: proposal.user }) : ''}
    </Typography>
    <Typography variant="body2" component="div">{t(sourceKey, sourceText)}</Typography>
    {proposal.reach && <Typography variant="body2" component="div" data-fork-postgres-reach={proposal.reach}>{reachText(t, proposal.reach)}</Typography>}
    {held && !inUse && <Button size="small" variant="outlined" color="inherit" sx={touch} onClick={replace}>
      {t('mgmt.config.general.monitoringDb.fork.replace', 'Replace what is typed with these')}
    </Button>}
    {proposal.passwordSecret && inUse && <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'wrap', alignItems: 'center' }}>
      <Button size="small" variant="outlined" color="inherit" sx={touch} loading={password === 'asking'} onClick={usePassword}>
        {t('mgmt.config.general.monitoringDb.fork.usePassword', 'Use its password')}
      </Button>
      <Typography variant="caption" component="div">
        {password === 'set' && t('mgmt.config.general.monitoringDb.fork.passwordSet', 'The password of {{name}} is in the password field; it is saved with the settings.', { name: proposal.container })}
        {password === 'failed' && t('mgmt.config.general.monitoringDb.fork.passwordFailed', 'The password could not be read: type it into the password field.')}
        {password === 'confirm' && t('mgmt.config.general.monitoringDb.fork.passwordConfirm', 'Confirm your password, then press "Use its password" again.')}
        {password === '' && t('mgmt.config.general.monitoringDb.fork.passwordHelp', 'Asks the server for the database\'s password; the request is recorded in the security record.')}
      </Typography>
    </Stack>}
  </ProposalBox>;
};

export const PostgresProposals = ({ formik, proposals }) => {
  const { t } = useTranslation();
  const [marks, setMarks] = React.useState({});
  if (!proposals) return null;
  const found = proposals.proposals || [];
  return <Grid size={{ xs: 12 }}>
    <Stack spacing={1.5}>
      {found.map((proposal) => <OneProposal key={proposal.container} proposal={proposal} formik={formik} marks={marks} setMarks={setMarks} />)}
      {!found.length && <FormHelperText data-fork-postgres-none>
        {proposals.error
          ? t('mgmt.config.general.monitoringDb.fork.notListed', 'The PostgreSQL containers of this server could not be listed: {{error}}', { error: proposals.error })
          : t('mgmt.config.general.monitoringDb.fork.none', 'No PostgreSQL container runs on this server: enter the database server below.')}
      </FormHelperText>}
    </Stack>
  </Grid>;
};

export const PostgresTestButton = ({ formik }) => {
  const { t } = useTranslation();
  const [testing, setTesting] = React.useState(false);
  const [result, setResult] = React.useState(null);
  const payload = testPayload(formik.values);
  const usable = payload.host !== '' && !postgresHostProblem(payload.host);
  const run = () => {
    setTesting(true);
    setResult(null);
    API.config.postgresTest(payload)
      .then((res) => setResult((res && res.data) || { success: false, message: '' }))
      .catch((e) => setResult({ success: false, message: (e && e.message) || String(e) }))
      .finally(() => setTesting(false));
  };
  return <Grid size={{ xs: 12 }}>
    <Stack spacing={1.5}>
      <Stack direction="row" spacing={1.5} useFlexGap sx={{ alignItems: 'center', flexWrap: 'wrap' }}>
        <Button variant="outlined" sx={touch} loading={testing} disabled={!usable} onClick={run} data-fork-postgres-test>
          {t('mgmt.config.general.monitoringDb.fork.test', 'Test the connection')}
        </Button>
        <Typography variant="caption" sx={{ color: 'text.secondary', flexBasis: 0, flexGrow: 1, minWidth: '14em' }}>
          {t('mgmt.config.general.monitoringDb.fork.testHelp', 'Connects with the settings above as they are now, saved or not. Nothing is changed. When the database server does not offer an encrypted connection (TLS), the password travels to it unencrypted, as it does for the monitoring connection itself.')}
        </Typography>
      </Stack>
      {result && <Alert severity={result.success ? 'success' : 'error'} onClose={() => setResult(null)} sx={{ overflowWrap: 'anywhere' }} data-fork-postgres-result>
        {result.success
          ? t('mgmt.config.general.monitoringDb.fork.testOk', 'The database answered: these settings work.')
          : t('mgmt.config.general.monitoringDb.fork.testFailed', 'The connection did not work.') + (result.message ? ' ' + result.message : '')}
      </Alert>}
    </Stack>
  </Grid>;
};

// Plan 51: the server field's choices: the proposals' addresses, each with
// whether it answers from Cosmos, and "Type it myself" (the field stays editable).
const chipTouch = { maxWidth: '100%', height: 'auto', minHeight: { xs: 44, sm: 32 }, '& .MuiChip-label': { whiteSpace: 'normal', py: 0.5 } };

export const PostgresHostChoices = ({ formik, proposals }) => {
  const { t } = useTranslation();
  const list = (proposals && proposals.proposals) || [];
  const choices = hostChoices(list);
  if (!choices.length) return null;
  const picked = pickedChoice(formik.values.PostgresHost, choices);
  const pick = (choice) => {
    const proposal = list.find((p) => p.host === choice.host);
    formik.setValues({ ...formik.values, ...choiceValues(formik.values, proposal) }); // one setValues: Formik loses the first of two calls
  };
  const typeIt = () => {
    const input = document.getElementById('PostgresHost');
    if (input) input.focus();
  };
  return <Grid size={{ xs: 12 }}>
    <Stack spacing={1}>
      <InputLabel id="fork-postgres-host-choices-label">{t('mgmt.config.general.monitoringDb.fork.choicesLabel', 'Where Cosmos reaches the database')}</InputLabel>
      <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'wrap' }} role="radiogroup" aria-labelledby="fork-postgres-host-choices-label" data-fork-postgres-choices>
        {choices.map((choice) => {
          const on = picked === choice.value;
          return <Chip key={choice.value} role="radio" aria-checked={on} clickable sx={chipTouch}
            color={on ? 'primary' : 'default'} variant={on ? 'filled' : 'outlined'}
            data-fork-postgres-choice={choice.value}
            label={t('mgmt.config.general.monitoringDb.fork.choice', '{{host}} ({{container}}, {{reach}})', {
              host: choice.host, container: choice.container,
              reach: choice.reach === 'answers' ? t('mgmt.config.general.monitoringDb.fork.choiceAnswers', 'answers') : t('mgmt.config.general.monitoringDb.fork.choiceSilent', 'does not answer from Cosmos'),
            })}
            onClick={() => pick(choice)} />;
        })}
        <Chip role="radio" aria-checked={picked === 'custom'} clickable sx={chipTouch}
          color={picked === 'custom' ? 'primary' : 'default'} variant={picked === 'custom' ? 'filled' : 'outlined'}
          data-fork-postgres-choice="custom"
          label={t('mgmt.config.general.monitoringDb.fork.choiceCustom', 'Type it myself')} onClick={typeIt} />
      </Stack>
    </Stack>
  </Grid>;
};

// Plan 51: the masked password would be refused: said before the save.
export const MaskedPasswordHint = ({ formik }) => {
  const { t } = useTranslation();
  if (!maskedPasswordStale(formik.values, formik.initialValues)) return null;
  return <FormHelperText sx={{ color: 'warning.main' }} data-fork-postgres-masked>
    {t('mgmt.config.general.monitoringDb.fork.maskedHint', 'The server, port, database or user changed: type the password again. A saved password is never sent to another server.')}
  </FormHelperText>;
};

// Plan 51: the save was refused because the database does not answer (UC047).
export const DatabaseRefusal = ({ formik, isAdmin }) => {
  const { t } = useTranslation();
  const refusal = formik.status && formik.status.databaseRefusal;
  if (!refusal) return null;
  const anyway = () => {
    requestSaveAnyway();
    formik.submitForm();
  };
  return <Alert severity="error" sx={{ overflowWrap: 'anywhere' }} data-fork-postgres-refused
    onClose={() => formik.setStatus({ ...(formik.status || {}), databaseRefusal: null })}>
    <Stack spacing={1} sx={{ alignItems: 'flex-start' }}>
      <Typography variant="body2" component="div">
        {t('mgmt.config.general.monitoringDb.fork.refused', 'Not saved: {{message}}', { message: refusal.message })}
      </Typography>
      {isAdmin && <>
        <Typography variant="body2" component="div">
          {t('mgmt.config.general.monitoringDb.fork.anywayHelp', 'If the database will come up later, you can save these settings anyway: monitoring then records into the local file until it answers, and switches by itself. This is recorded in the security record.')}
        </Typography>
        <Button size="small" variant="outlined" color="inherit" sx={touch} onClick={anyway} data-fork-postgres-anyway>
          {t('mgmt.config.general.monitoringDb.fork.anyway', 'Save anyway')}
        </Button>
      </>}
    </Stack>
  </Alert>;
};

// Plan 51: the monitoring store in use, every 30 s; null before the first answer.
export const useMonitoringStore = (every = 30000) => {
  const [state, setState] = React.useState(null);
  React.useEffect(() => {
    let live = true;
    const ask = () => API.config.monitoringStore()
      .then((res) => { if (live) setState((res && res.data) || null); })
      .catch(() => {});
    ask();
    const timer = setInterval(ask, every);
    return () => { live = false; clearInterval(timer); };
  }, [every]);
  return state;
};

// Plan 51: "The monitoring database at … does not answer since …", where monitoring is shown.
export const MonitoringStoreLine = ({ state: given }) => {
  const { t } = useTranslation();
  const polled = useMonitoringStore();
  const state = given || polled;
  const line = storeStateLine(state, (d) => new Date(d).toLocaleString());
  if (!line) return null;
  const [key, text, params] = line;
  return <Alert severity="warning" sx={{ overflowWrap: 'anywhere' }} data-fork-monitoring-fallback>
    {t(key, text, params)}
  </Alert>;
};

// Plan 51: what a save that moves the store does to the history, asked once
// before it. askStoreMove(move) resolves true (save) or false (do not).
let openStoreMove = null;
export const askStoreMove = (move) => new Promise((resolve) => {
  if (!openStoreMove || !moveSentence(move)) { resolve(true); return; }
  openStoreMove({ move, resolve });
});

export const StoreMoveDialog = () => {
  const { t } = useTranslation();
  const [asking, setAsking] = React.useState(null);
  React.useEffect(() => {
    openStoreMove = setAsking;
    return () => { if (openStoreMove === setAsking) openStoreMove = null; };
  }, []);
  if (!asking) return null;
  const [key, text, params] = moveSentence(asking.move);
  const answer = (yes) => { asking.resolve(yes); setAsking(null); };
  return <Dialog open onClose={() => answer(false)} data-fork-store-move>
    <DialogTitle>{t('mgmt.config.general.monitoringDb.fork.moveTitle', 'Monitoring moves to another store')}</DialogTitle>
    <DialogContent>
      <DialogContentText>{t(key, text, params)}</DialogContentText>
    </DialogContent>
    <DialogActions>
      <Button sx={touch} onClick={() => answer(false)}>{t('global.cancelAction')}</Button>
      <Button sx={touch} variant="contained" onClick={() => answer(true)} data-fork-store-move-save>{t('mgmt.config.general.monitoringDb.fork.moveSave', 'Save and switch')}</Button>
    </DialogActions>
  </Dialog>;
};

// Plan 51: the server field holds a container's own address on Docker's network.
export const ContainerAddressWarning = ({ formik, proposals }) => {
  const { t } = useTranslation();
  const line = containerAddressSentence(containerAddressWarning(formik.values.PostgresHost, proposals && proposals.proposals));
  if (!line) return null;
  const [key, text, params] = line;
  return <FormHelperText sx={{ color: 'warning.main' }} data-fork-postgres-bridge>{t(key, text, params)}</FormHelperText>;
};
