// Fork addition (plan 47): the monitoring database form (configExternal.jsx)
// proposes the PostgreSQL containers that run on this server and tests the
// connection before saving. A proposal fills the server, the database and the
// user, only where the form holds nothing of the administrator's own (logic:
// utils/postgresProposal.js); the password is never part of it: "Use its
// password" asks the server for it on a click (the recorded reveal of the
// sign-in details, which needs a recent password confirmation: the prompt
// opens by itself when it is old).
import * as React from 'react';
import { Alert, Button, FormHelperText, Grid, Stack, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import * as API from '../../api';
import ProposalBox from '../../components/proposalBox';
import { takeProposal, replaceWithProposal, holdsProposal, testPayload, postgresHostProblem } from '../../utils/postgresProposal';

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
  address: ['mgmt.config.general.monitoringDb.fork.hostAddress', 'The container\'s own address: it can change when the container is made again (an update, an edit). Publishing port 5432 on 127.0.0.1 gives an address that stays.'],
};

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

  const [sourceKey, sourceText] = HOST_SOURCE[proposal.hostSource] || HOST_SOURCE.address;
  return <ProposalBox
    title={t('mgmt.config.general.monitoringDb.fork.title', 'PostgreSQL in the container {{name}}', { name: proposal.container })}
    source={t('mgmt.config.general.monitoringDb.fork.source', 'From the container\'s settings ({{image}}). The password is not part of this proposal.', { image: proposal.image })}
    inUse={inUse}
    inUseText={t('mgmt.config.general.monitoringDb.fork.inUse', 'The fields below hold these values. Save the settings to use this database.')}
    heldText={held ? t('mgmt.config.general.monitoringDb.fork.held', 'The fields below hold values of your own, so they were left as they are.') : ''}
    onUse={use}
    useLabel={t('mgmt.config.general.monitoringDb.fork.use', 'Use these')}
  >
    <Typography variant="body2" component="div" data-fork-postgres-proposal={proposal.container}>
      {t('mgmt.config.general.monitoringDb.fork.lineHost', 'Server: {{host}}', { host: proposal.host })}
      {proposal.database ? ' · ' + t('mgmt.config.general.monitoringDb.fork.lineDatabase', 'database: {{database}}', { database: proposal.database }) : ''}
      {proposal.user ? ' · ' + t('mgmt.config.general.monitoringDb.fork.lineUser', 'user: {{user}}', { user: proposal.user }) : ''}
    </Typography>
    <Typography variant="body2" component="div">{t(sourceKey, sourceText)}</Typography>
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
