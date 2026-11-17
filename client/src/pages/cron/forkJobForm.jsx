// Fork addition: the guided parts of the scheduled-job dialog (newJob.jsx):
//   JobWhere      where the job runs, as a choice: in a container of this
//                 server (picked from the list, the command then runs with
//                 docker exec) or on the server itself, said plainly: as root
//   JobSchedule   the schedule as a choice in words, the expression shown,
//                 "Type it myself" for any other, and the next three runs
//   JobProposals  the common jobs Cosmos knows for the chosen container (a
//                 database's dump: src/cron/fork_proposals.go)
// The logic is utils/jobForm.js; the command field stays the dialog's own and
// is always editable.
import * as React from 'react';
import { Alert, Box, Button, FormHelperText, MenuItem, Stack, TextField, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import * as API from '../../api';
import ChoiceChips from '../../components/choiceChips';
import ProposalBox from '../../components/proposalBox';
import CronHelp from '../../components/cronInput';
import { clockReading } from '../../utils/cronExpression';
import { applyProposal, acceptProposal } from '../../utils/proposals';
import {
  scheduleExpression, readSchedule, nextRuns, containerChoices, proposalFields, DEFAULT_SCHEDULE,
} from '../../utils/jobForm';

// The server's time zone, as cronInput.jsx asks it: only known when Cosmos
// runs directly on the server.
let serverZone = null;
const getServerZone = () => {
  if (!serverZone) {
    serverZone = API.getStatus()
      .then((res) => ((res && res.data && res.data.containerized === false && res.data.hostTimezone) || ''))
      .catch(() => '');
  }
  return serverZone;
};

const runText = (run, language) => {
  const date = new Date(Date.UTC(run.year, run.month - 1, run.day, run.hour, run.minute, run.second));
  const options = { weekday: 'long', day: 'numeric', month: 'long', hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone: 'UTC' };
  try {
    return new Intl.DateTimeFormat(language || 'en', options).format(date);
  } catch (e) {
    return new Intl.DateTimeFormat('en', options).format(date);
  }
};

// Monday first, as a week is read here; the values are cron's (0 = Sunday).
const WEEKDAYS = [1, 2, 3, 4, 5, 6, 0];
const weekdayName = (day, language) => new Date(Date.UTC(2026, 10, 15 + day)).toLocaleDateString(language || 'en', { weekday: 'long', timeZone: 'UTC' });

// A choice of the dialog, with the mark of a mandatory one (as formHelpers.jsx's ChoiceRow, outside a grid).
const Choice = ({ name, label, value, onChange, options }) => <ChoiceChips
  id={name}
  label={<>{label}<span aria-hidden="true" className="MuiInputLabel-asterisk">&thinsp;*</span></>}
  value={value}
  onChange={onChange}
  options={options.map((option) => ({ value: option.value, label: option.label, description: option.hint }))}
/>;

// The field rows of the dialog are full width, and tall enough to touch.
const rowSx = { width: '100%', '& .MuiInputBase-root': { minHeight: 44 } };

export const JobWhere = ({ formik, place, onPlace, containers, loading }) => {
  const { t } = useTranslation();
  const chosen = containers.find((c) => c.name === formik.values.Container);
  const error = formik.submitCount > 0 && formik.errors.Container;
  return <Stack spacing={1.5}>
    <Choice
      name="job-where"
      label={t('mgmt.cron.fork.where.label', 'Where the job runs')}
      value={place}
      onChange={onPlace}
      options={[
        { value: 'container', label: t('mgmt.cron.fork.where.container', 'In a container of this server'),
          hint: t('mgmt.cron.fork.where.containerHint', 'The command runs inside the container you pick (docker exec), with what that container can reach. The container has to be running at that time.') },
        { value: 'server', label: t('mgmt.cron.fork.where.server', 'On the server itself'),
          hint: t('mgmt.cron.fork.where.serverHint', 'The command runs on the server as root: it can read, change and delete everything on this server. Use this only for a command you know.') },
      ]}
    />
    {place === 'container' && <>
      <TextField
        select
        id="Container"
        name="Container"
        label={t('mgmt.cron.fork.container.label', 'Container')}
        value={chosen ? chosen.name : (formik.values.Container || '')}
        onChange={(event) => formik.setFieldValue('Container', event.target.value)}
        error={Boolean(error)}
        helperText={error || (loading ? t('mgmt.cron.fork.container.loading', 'Reading the containers of this server…') : '')}
        sx={rowSx}
      >
        {/* a saved container that is gone stays shown, so the edit does not change it silently */}
        {formik.values.Container && !chosen && <MenuItem value={formik.values.Container}>
          {t('mgmt.cron.fork.container.gone', '{{name}} (not on this server any more)', { name: formik.values.Container })}
        </MenuItem>}
        {containers.map((c) => <MenuItem key={c.name} value={c.name} sx={{ minHeight: 44, whiteSpace: 'normal', overflowWrap: 'anywhere' }}>
          {c.running ? c.name : t('mgmt.cron.fork.container.stopped', '{{name}} (stopped)', { name: c.name })}
        </MenuItem>)}
      </TextField>
      {chosen && !chosen.running && <Alert severity="warning">
        {t('mgmt.cron.fork.container.stoppedHint', 'This container is not running now. The job fails at every run while it is stopped.')}
      </Alert>}
      {!loading && containers.length === 0 && <FormHelperText>
        {t('mgmt.cron.fork.container.none', 'Cosmos sees no container on this server.')}
      </FormHelperText>}
    </>}
  </Stack>;
};

export const JobSchedule = ({ formik }) => {
  const { t, i18n } = useTranslation();
  const crontab = formik.values.Crontab || '';
  const [choice, setChoice] = React.useState(() => readSchedule(crontab));
  const [zone, setZone] = React.useState('');

  React.useEffect(() => {
    let live = true;
    getServerZone().then((found) => { if (live) setZone(found); });
    return () => { live = false; };
  }, []);

  // the expression changed from outside (a saved job loaded, a proposal used): the choice follows it
  React.useEffect(() => {
    if (choice.kind !== 'custom' && scheduleExpression(choice) !== crontab.trim()) {
      setChoice(readSchedule(crontab));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [crontab]);

  const pick = (next) => {
    setChoice(next);
    const expression = scheduleExpression(next);
    if (expression) formik.setFieldValue('Crontab', expression);
  };

  const kinds = [
    { value: 'hourly', label: t('mgmt.cron.fork.when.hourly', 'Every hour'), hint: t('mgmt.cron.fork.when.hourlyHint', 'At the start of every hour.') },
    { value: 'nightly', label: t('mgmt.cron.fork.when.nightly', 'Every night'), hint: t('mgmt.cron.fork.when.nightlyHint', 'Once a day at the time below; at night the server is usually quiet.') },
    { value: 'weekly', label: t('mgmt.cron.fork.when.weekly', 'Every week'), hint: t('mgmt.cron.fork.when.weeklyHint', 'Once a week, on the day and at the time below.') },
    { value: 'monthly', label: t('mgmt.cron.fork.when.monthly', 'Every month'), hint: t('mgmt.cron.fork.when.monthlyHint', 'On the 1st of every month at the time below.') },
    { value: 'custom', label: t('mgmt.cron.fork.when.custom', 'Type it myself'), hint: t('mgmt.cron.fork.when.customHint', 'Any schedule the server takes: six parts, seconds first.') },
  ];

  const runs = nextRuns(crontab, clockReading(new Date(), zone));
  const error = formik.submitCount > 0 && formik.errors.Crontab;

  return <Stack spacing={1.5}>
    <Choice
      name="job-schedule"
      label={t('mgmt.cron.fork.when.label', 'When it runs')}
      value={choice.kind}
      onChange={(kind) => pick({ ...choice, kind })}
      options={kinds}
    />
    {['nightly', 'weekly', 'monthly'].includes(choice.kind) && <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1.5}>
      {choice.kind === 'weekly' && <TextField
        select
        id="job-weekday"
        label={t('mgmt.cron.fork.when.day', 'Day')}
        value={String(choice.weekday)}
        onChange={(event) => pick({ ...choice, weekday: Number(event.target.value) })}
        sx={rowSx}
      >
        {WEEKDAYS.map((day) => <MenuItem key={day} value={String(day)} sx={{ minHeight: 44 }}>{weekdayName(day, i18n.resolvedLanguage)}</MenuItem>)}
      </TextField>}
      <TextField
        id="job-time"
        type="time"
        label={zone ? t('mgmt.cron.fork.when.timeServer', 'Time (server time)') : t('mgmt.cron.fork.when.time', 'Time')}
        value={choice.time}
        onChange={(event) => pick({ ...choice, time: event.target.value || DEFAULT_SCHEDULE.time })}
        slotProps={{ inputLabel: { shrink: true } }}
        sx={rowSx}
      />
    </Stack>}
    {choice.kind === 'custom' ? <>
      <TextField
        fullWidth
        id="Crontab"
        name="Crontab"
        label={t('mgmt.cron.newCron.crontabInput.crontabLabel')}
        value={crontab}
        onChange={formik.handleChange}
        error={formik.touched.Crontab && Boolean(formik.errors.Crontab)}
        sx={rowSx}
      />
      <CronHelp formik={formik} name="Crontab" />
    </> : <>
      <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }} data-testid="job-expression">
        {t('mgmt.cron.fork.when.expression', 'Saved as the schedule {{expression}}', { expression: crontab })}
      </Typography>
      {error && <FormHelperText error>{t('mgmt.cron.fork.when.invalid', 'Pick a time as HH:MM.')}</FormHelperText>}
    </>}
    {runs.length > 0 && <Box data-testid="job-next-runs">
      <Typography variant="caption" color="textSecondary">
        {zone
          ? t('mgmt.cron.fork.next.server', 'Next three runs (server time):')
          : t('mgmt.cron.fork.next.device', 'Next three runs (if the server\'s clock shows the same time as this device):')}
      </Typography>
      <Typography component="ul" variant="body2" sx={{ m: 0, pl: 2.5 }}>
        {runs.map((run) => <li key={`${run.year}-${run.month}-${run.day}-${run.hour}-${run.minute}-${run.second}`}>{runText(run, i18n.resolvedLanguage)}</li>)}
      </Typography>
    </Box>}
  </Stack>;
};

export const JobProposals = ({ formik, container, marks, setMarks }) => {
  const { t } = useTranslation();
  const [answer, setAnswer] = React.useState(null);
  const [held, setHeld] = React.useState([]); // fields the last "Use" left alone, being the administrator's

  React.useEffect(() => {
    setAnswer(null);
    if (!container || !API.cron.proposals) return undefined;
    let live = true;
    API.cron.proposals(container)
      .then((res) => { if (live) setAnswer((res && res.data) || null); })
      .catch(() => { if (live) setAnswer(null); }); // proposals are an extra: the form works without them
    return () => { live = false; };
  }, [container]);

  const fieldWords = {
    Name: t('mgmt.cron.fork.proposal.fieldName', 'the name'),
    Command: t('mgmt.cron.fork.proposal.fieldCommand', 'the command'),
    Crontab: t('mgmt.cron.fork.proposal.fieldSchedule', 'the schedule'),
  };
  if (!answer || answer.container !== container) return null;
  if (answer.note === 'noDataFolder') {
    return <Alert severity="info">
      {t('mgmt.cron.fork.proposal.noDataFolder', 'Cosmos knows how to dump this database ({{recipe}}), but its data folder is not a volume or a folder of the server: a dump would be lost with the container. Give the container a volume for its data first.', { recipe: answer.recipe || answer.image })}
    </Alert>;
  }
  if (!answer.proposals || answer.proposals.length === 0) return null;

  return <Stack spacing={1}>
    {answer.proposals.map((proposal) => {
      const fields = proposalFields(proposal);
      const inUse = formik.values.Command === proposal.command;
      const use = () => {
        const { values, marks: next, held } = applyProposal(formik.values, fields, marks);
        formik.setValues({ ...formik.values, ...values });
        setMarks(next);
        setHeld(held);
      };
      const replace = () => {
        const { values, marks: next } = acceptProposal(formik.values, fields);
        formik.setValues({ ...formik.values, ...values });
        setMarks({ ...marks, ...next });
        setHeld([]);
      };
      const kept = held.filter((name) => formik.values[name] !== fields[name]);
      return <ProposalBox
        key={proposal.id}
        title={t('mgmt.cron.fork.proposal.dumpTitle', 'A nightly dump of this database ({{recipe}})', { recipe: answer.recipe })}
        source={t('mgmt.cron.fork.proposal.dumpSource', 'From what Cosmos knows of the image {{image}}: the dump signs in as {{user}}, read from the container\'s own settings when the job runs, so the command holds no password.', { image: answer.image, user: proposal.user })}
        status={proposal.folderOnServer
          ? t('mgmt.cron.fork.proposal.dumpWhere', 'The dumps go to {{server}} on the server ({{inside}} in the container); those older than {{days}} days are deleted.', { server: proposal.folderOnServer, inside: proposal.folderInContainer, days: proposal.keepDays })
          : t('mgmt.cron.fork.proposal.dumpWhereInside', 'The dumps go to {{inside}} in the container, on its data volume; those older than {{days}} days are deleted.', { inside: proposal.folderInContainer, days: proposal.keepDays })}
        inUse={inUse}
        inUseText={t('mgmt.cron.fork.proposal.inUse', 'The form holds this job: check the name and the schedule, then save.')}
        onUse={use}
        useLabel={t('mgmt.cron.fork.proposal.use', 'Use this job')}
      >
        <Box component="code" sx={{ display: 'block', fontSize: '0.8rem', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{proposal.command}</Box>
        {!inUse && kept.length > 0 && <Stack spacing={0.5} sx={{ alignItems: 'flex-start' }}>
          <Typography variant="body2">
            {t('mgmt.cron.fork.proposal.held', 'What you typed stays: {{fields}}.', { fields: kept.map((name) => fieldWords[name] || name).join(', ') })}
          </Typography>
          <Button size="small" variant="outlined" color="inherit" onClick={replace}>
            {t('mgmt.cron.fork.proposal.replace', 'Replace them with the proposed job')}
          </Button>
        </Stack>}
      </ProposalBox>;
    })}
  </Stack>;
};

// The containers of this server, for JobWhere.
export const useContainerChoices = () => {
  const [state, setState] = React.useState({ loading: true, containers: [] });
  React.useEffect(() => {
    let live = true;
    API.docker.list()
      .then((res) => { if (live) setState({ loading: false, containers: containerChoices(res && res.data) }); })
      .catch(() => { if (live) setState({ loading: false, containers: [] }); });
    return () => { live = false; };
  }, []);
  return state;
};
