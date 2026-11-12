// Fork addition: "Incremental backup" card of the general settings. It sets up
// the dated, hard-linked snapshots made by src/snapshots on the server, lists
// the snapshots there are and can put one back in place of its folder. The
// settings live in the page's form (see the three helpers below, used by
// index.jsx); the list and the two buttons talk to /api/snapshots.
import * as React from 'react';
import * as Yup from 'yup';
import * as API from '../../api';
import MainCard from '../../components/MainCard';
import {
  Alert,
  Button,
  Chip,
  CircularProgress,
  FormHelperText,
  Grid,
  InputLabel,
  Stack,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { FolderOutlined, PlayCircleOutlined, PlusOutlined, RollbackOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { CosmosCheckbox, CosmosInputText, CosmosSelect } from './users/formShortcuts';
import { FilePickerButton } from '../../components/filePicker';
import { ConfirmModalDirect } from '../../components/confirmModal';
import PermissionGuard from '../../components/permissionGuard';
import { PERM_ADMIN } from '../../utils/permissions';
import { notify } from '../../utils/activity';
import { simplifyNumber } from '../dashboard/components/utils';

const DEFAULT_KEEP = { Recent: 8, Daily: 7, Weekly: 4, Monthly: 6 };
const SHOWN_AT_FIRST = 5;
const POLL_EVERY = 3000;

// What the form starts from. A keep count of 0 in the configuration means
// "the default", which the form shows as the number it stands for.
export const snapshotsInitialValues = (config) => {
  const saved = (config && config.Snapshots) || {};
  return {
    Snapshots_Enabled: Boolean(saved.Enabled),
    Snapshots_Destination: saved.Destination || '',
    Snapshots_Sources: Array.isArray(saved.Sources) ? saved.Sources : [],
    Snapshots_Frequency: saved.Frequency || 'daily',
    Snapshots_Time: saved.Time || '03:00',
    Snapshots_Weekday: String(saved.Weekday || 0),
    Snapshots_KeepRecent: saved.KeepRecent || DEFAULT_KEEP.Recent,
    Snapshots_KeepDaily: saved.KeepDaily || DEFAULT_KEEP.Daily,
    Snapshots_KeepWeekly: saved.KeepWeekly || DEFAULT_KEEP.Weekly,
    Snapshots_KeepMonthly: saved.KeepMonthly || DEFAULT_KEEP.Monthly,
  };
};

// The plain form of an absolute path, as the server stores it: no doubled or
// trailing slashes. Anything else is left for the checks to refuse.
const plainPath = (path) => {
  const text = String(path || '');
  if (!text.startsWith('/') || text.trim() !== text) {
    return text;
  }
  return '/' + text.split('/').filter((part) => part && part !== '.').join('/');
};

// What is saved as config.Snapshots: numbers as numbers, the folders as a list.
export const snapshotsToConfig = (values) => ({
  Enabled: Boolean(values.Snapshots_Enabled),
  Destination: plainPath(values.Snapshots_Destination),
  Sources: Array.isArray(values.Snapshots_Sources) ? values.Snapshots_Sources.filter(Boolean).map(plainPath) : [],
  Frequency: values.Snapshots_Frequency || 'daily',
  Time: values.Snapshots_Time || '03:00',
  Weekday: Number(values.Snapshots_Weekday) || 0,
  KeepRecent: Number(values.Snapshots_KeepRecent) || 0,
  KeepDaily: Number(values.Snapshots_KeepDaily) || 0,
  KeepWeekly: Number(values.Snapshots_KeepWeekly) || 0,
  KeepMonthly: Number(values.Snapshots_KeepMonthly) || 0,
});

const isHostDirectory = (value) =>
  !value || (/^\/[^\0\r\n]*$/.test(value) && !value.split('/').includes('..') && value.trim() === value);

// The form's checks; the server checks again, and more, when the settings are saved.
export const snapshotsValidation = (t) => {
  const keep = Yup.mixed().test(
    'keep',
    t('mgmt.config.snapshots.keepValidation', 'Enter a whole number between 1 and 365'),
    (value) => /^\d{1,3}$/.test(String(value)) && Number(value) >= 1 && Number(value) <= 365,
  );
  return {
    Snapshots_Destination: Yup.string()
      .test('host-directory',
        t('mgmt.config.pathValidation', 'Enter an absolute path such as /storage/backups, without ".." and without spaces at either end'),
        isHostDirectory)
      .when('Snapshots_Enabled', {
        is: true,
        then: (schema) => schema.required(t('mgmt.config.snapshots.destinationRequired', 'Choose the folder the snapshots are stored in')),
      }),
    Snapshots_Sources: Yup.array().when('Snapshots_Enabled', {
      is: true,
      then: (schema) => schema.min(1, t('mgmt.config.snapshots.sourcesRequired', 'Add at least one folder to back up')),
    }),
    Snapshots_Time: Yup.string().matches(/^([01]\d|2[0-3]):[0-5]\d$/,
      t('mgmt.config.update.timeValidation', 'Enter a time as HH:MM, for example 02:00')),
    Snapshots_KeepRecent: keep,
    Snapshots_KeepDaily: keep,
    Snapshots_KeepWeekly: keep,
    Snapshots_KeepMonthly: keep,
  };
};

// A path can be long and has no spaces to break at: let it wrap anywhere.
const pathChipSx = {
  height: 'auto',
  maxWidth: '100%',
  '& .MuiChip-label': { whiteSpace: 'normal', overflowWrap: 'anywhere', py: 0.5 },
};

const shownDate = (value) => {
  const date = new Date(value);
  return isNaN(date.getTime()) ? '' : date.toLocaleString();
};

const baseName = (path) => String(path || '').split('/').filter(Boolean).pop() || '';

const Snapshots = ({ formik, config }) => {
  const { t } = useTranslation();
  const [status, setStatus] = React.useState(null);
  const [loadError, setLoadError] = React.useState('');
  const [starting, setStarting] = React.useState(false);
  const [confirm, setConfirm] = React.useState(null); // { source, snapshot } waiting for a yes
  const [expanded, setExpanded] = React.useState({}); // folders whose whole list is shown
  const [outcome, setOutcome] = React.useState(null); // how the restore started from here ended
  const followed = React.useRef(''); // what this page saw running: 'snapshot' or 'restore'
  const alive = React.useRef(true);

  const values = formik.values;
  const sources = Array.isArray(values.Snapshots_Sources) ? values.Snapshots_Sources : [];
  const destination = values.Snapshots_Destination || '';

  // tells how a run or a restore that this page followed has ended
  const report = (data) => {
    const what = followed.current;
    followed.current = '';
    if (what === 'restore' && data.lastRestore) {
      setOutcome(data.lastRestore);
    } else if (what === 'snapshot') {
      const failed = (data.sources || []).filter((source) => source.lastError);
      if (failed.length) {
        notify(t('mgmt.config.snapshots.run.problems', 'The incremental backup finished with problems, see the list of snapshots.'), 'error');
      } else {
        notify(t('mgmt.config.snapshots.run.done', 'The incremental backup is finished.'), 'success');
      }
    }
  };

  const accept = (data) => {
    if (!alive.current || !data) return;
    setStatus(data);
    setLoadError('');
    if (data.running) {
      followed.current = data.action === 'restore' ? 'restore' : 'snapshot';
    } else if (followed.current) {
      report(data);
    }
  };

  const load = () => API.config.snapshotsStatus()
    .then((response) => accept(response.data))
    .catch((error) => {
      if (alive.current) setLoadError(error.message || String(error));
    });

  // when the page opens, and again when the settings were saved or refreshed
  React.useEffect(() => { load(); }, [config]);
  React.useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);

  // while something runs, and only then, ask again every few seconds; a
  // failed request stops it (the page's Refresh button starts it again)
  const polling = Boolean(status && status.running && !loadError);
  React.useEffect(() => {
    if (!polling) return undefined;
    const timer = setInterval(load, POLL_EVERY);
    return () => clearInterval(timer);
  }, [polling]);

  const addSource = (path) => {
    const clean = plainPath(path);
    if (clean && clean !== '/' && !sources.includes(clean)) {
      formik.setFieldValue('Snapshots_Sources', [...sources, clean]);
    }
  };
  const removeSource = (path) => formik.setFieldValue('Snapshots_Sources', sources.filter((source) => source !== path));

  const suggestions = ((status && status.suggested) || [])
    .filter((path) => !sources.includes(path) && path !== destination && !path.startsWith(destination ? destination + '/' : '\0'));

  // "Back up now" runs what is saved, not what is in the form
  const unsaved = JSON.stringify(snapshotsToConfig(values)) !== JSON.stringify(snapshotsToConfig(snapshotsInitialValues(config)));
  const running = Boolean(status && status.running);
  const canRun = Boolean(status && status.enabled && !running && !unsaved);

  const runNow = () => {
    setStarting(true);
    setOutcome(null);
    API.config.snapshotsRun()
      .then((response) => accept(response.data))
      .catch(() => { load(); }) // the reason is shown by the command's own message
      .finally(() => { if (alive.current) setStarting(false); });
  };

  const restore = () => {
    const { source, snapshot } = confirm;
    setOutcome(null);
    API.config.snapshotsRestore(source.path, snapshot.id)
      .then((response) => accept(response.data))
      .catch(() => { load(); });
  };

  const sourcesError = (formik.touched.Snapshots_Sources || formik.submitCount > 0) && typeof formik.errors.Snapshots_Sources === 'string'
    ? formik.errors.Snapshots_Sources : '';

  return (
    <MainCard title={t('mgmt.config.snapshots.title', 'Incremental backup')}>
      <Grid container spacing={3}>
        <Grid item xs={12}>
          <Alert severity="info">
            {t('mgmt.config.snapshots.info',
              'Cosmos keeps dated copies (snapshots) of the folders below in the destination: one folder per date, which any file manager can open. A file that did not change since the previous snapshot is stored as a link to it, so only changes take up space; the copies are not encrypted.')}
          </Alert>
        </Grid>

        <CosmosCheckbox
          label={t('mgmt.config.snapshots.enableLabel', 'Make snapshots of the folders below on a schedule')}
          name="Snapshots_Enabled"
          formik={formik}
        />

        {/* where the snapshots go */}
        <Grid item xs={12}>
          {/* the label above the row and the row aligned at its top: the folder button stays level
              with the input when an error is shown under it, and the field fits a phone */}
          <Stack spacing={1}>
            <InputLabel htmlFor="Snapshots_Destination">
              {t('mgmt.config.snapshots.destinationLabel', 'Destination: the folder the snapshots are stored in')}
            </InputLabel>
            <Stack direction={"row"} spacing={2} alignItems="flex-start">
              <FilePickerButton canCreate onPick={(path) => {
                if (path)
                  formik.setFieldValue('Snapshots_Destination', path);
              }} size="150%" select="folder" />
              <CosmosInputText
                name="Snapshots_Destination"
                placeholder="/storage/snapshots"
                formik={formik}
              />
            </Stack>
          </Stack>
          <FormHelperText>
            {t('mgmt.config.snapshots.destinationHelp',
              'On a disk formatted for Linux (ext4, XFS, Btrfs…), in a place the apps cannot reach, and preferably not on the disk the folders themselves are on. It is created if it does not exist.')}
          </FormHelperText>
        </Grid>

        {/* what is backed up */}
        <Grid item xs={12}>
          <Stack spacing={1}>
            <InputLabel>{t('mgmt.config.snapshots.sourcesLabel', 'Folders to back up')}</InputLabel>
            <Stack direction="row" spacing={1} alignItems="center" useFlexGap flexWrap="wrap">
              <FilePickerButton
                onPick={(path) => addSource(path)}
                select="folder"
                tooltip={t('mgmt.config.snapshots.addFolder', 'Add folder')}
              />
              {sources.map((path) => (
                <Chip
                  key={path}
                  icon={<FolderOutlined />}
                  label={path}
                  onDelete={() => removeSource(path)}
                  sx={pathChipSx}
                />
              ))}
              {sources.length === 0 && (
                <Typography variant="body2" color="text.secondary">
                  {t('mgmt.config.snapshots.noSource', 'No folder yet: add one with the button, or pick a suggestion.')}
                </Typography>
              )}
            </Stack>
            {sourcesError && <FormHelperText error>{sourcesError}</FormHelperText>}

            {suggestions.length > 0 && (<>
              <Typography variant="caption" color="textSecondary">
                {t('mgmt.config.snapshots.suggested', 'Suggested (click to add):')}
              </Typography>
              <Stack direction="row" spacing={1} useFlexGap flexWrap="wrap">
                {suggestions.map((path) => (
                  <Chip
                    key={path}
                    size="small"
                    clickable
                    variant="outlined"
                    icon={<PlusOutlined />}
                    label={path}
                    onClick={() => addSource(path)}
                    sx={pathChipSx}
                  />
                ))}
              </Stack>
            </>)}
          </Stack>
        </Grid>

        {/* when */}
        <CosmosSelect
          name="Snapshots_Frequency"
          label={t('mgmt.config.snapshots.frequency', 'How often a snapshot is made')}
          formik={formik}
          options={[
            ['6h', t('mgmt.config.backupSchedule.6h', 'Every six hours')],
            ['12h', t('mgmt.config.backupSchedule.12h', 'Every twelve hours')],
            ['daily', t('mgmt.config.backupSchedule.daily', 'Once a day')],
            ['weekly', t('mgmt.config.backupSchedule.weekly', 'Once a week')],
          ]}
        />
        {values.Snapshots_Frequency === 'weekly' && <CosmosSelect
          name="Snapshots_Weekday"
          label={t('mgmt.config.snapshots.weekday', 'Day of the weekly snapshot')}
          formik={formik}
          options={[1, 2, 3, 4, 5, 6, 0].map((day) => [String(day),
            new Date(2026, 10, 15 + day).toLocaleDateString(undefined, { weekday: 'long' })])}
        />}
        <CosmosInputText
          label={['daily', 'weekly'].includes(values.Snapshots_Frequency)
            ? t('mgmt.config.snapshots.timeAt', 'Time of the snapshot (server time)')
            : t('mgmt.config.backupSchedule.timeFrom', 'Counted from this time of day (server time)')}
          name="Snapshots_Time"
          type="time"
          formik={formik}
        />

        {/* how long */}
        <CosmosInputText
          label={t('mgmt.config.snapshots.keepRecent', 'Most recent snapshots that always stay, however close together they were made')}
          name="Snapshots_KeepRecent"
          type="number"
          inputProps={{ min: 1, max: 365, step: 1, inputMode: 'numeric' }}
          formik={formik}
        />
        <CosmosInputText
          label={t('mgmt.config.snapshots.keepDaily', 'Days to go back: the newest snapshot of each of the last … days is kept')}
          name="Snapshots_KeepDaily"
          type="number"
          inputProps={{ min: 1, max: 365, step: 1, inputMode: 'numeric' }}
          formik={formik}
        />
        <CosmosInputText
          label={t('mgmt.config.snapshots.keepWeekly', 'Weeks to go back: the newest snapshot of each of the last … weeks is kept')}
          name="Snapshots_KeepWeekly"
          type="number"
          inputProps={{ min: 1, max: 365, step: 1, inputMode: 'numeric' }}
          formik={formik}
        />
        <CosmosInputText
          label={t('mgmt.config.snapshots.keepMonthly', 'Months to go back: the newest snapshot of each of the last … months is kept')}
          name="Snapshots_KeepMonthly"
          type="number"
          inputProps={{ min: 1, max: 365, step: 1, inputMode: 'numeric' }}
          formik={formik}
        />
        <Grid item xs={12}>
          <Typography variant="caption" color="text.secondary">
            {t('mgmt.config.snapshots.keepNote',
              'After each run the snapshots that are not kept by one of these rules are removed. Several snapshots made on the same day all stay while they are among the most recent ones; after that only the newest of that day stays.')}
          </Typography>
        </Grid>

        {/* what there is, and what is running (the saved settings, not the form) */}
        <Grid item xs={12}>
          <Stack spacing={1.5}>
            <Stack direction="row" spacing={1.5} alignItems="center" useFlexGap flexWrap="wrap">
              <Typography variant="subtitle1">
                {t('mgmt.config.snapshots.status.title', 'Snapshots')}
              </Typography>
              <PermissionGuard permission={PERM_ADMIN}>
                <LoadingButton
                  size="small"
                  variant="outlined"
                  loading={starting}
                  disabled={!canRun}
                  startIcon={<PlayCircleOutlined />}
                  onClick={runNow}
                >
                  {t('mgmt.config.snapshots.run.button', 'Back up now')}
                </LoadingButton>
              </PermissionGuard>
            </Stack>

            {loadError && <Alert severity="error">{loadError}</Alert>}
            {status && status.error && <Alert severity="error" sx={{ overflowWrap: 'anywhere' }}>{status.error}</Alert>}

            {running && (
              <Alert severity="info" icon={<CircularProgress size={18} color="inherit" />} sx={{ overflowWrap: 'anywhere' }}>
                {status.action === 'restore'
                  ? t('mgmt.config.snapshots.status.restoring', 'Restoring {{folder}}…', { folder: status.current || '' })
                  : t('mgmt.config.snapshots.status.running', 'Backing up {{folder}}…', { folder: status.current || '' })}
                {' '}
                {t('mgmt.config.snapshots.status.filesDone', '{{files}} files so far.', { files: Number(status.filesDone || 0).toLocaleString() })}
              </Alert>
            )}

            {outcome && (outcome.error ? (
              <Alert severity="error" onClose={() => setOutcome(null)} sx={{ overflowWrap: 'anywhere' }}>
                {t('mgmt.config.snapshots.restore.failed', '{{folder}} was not restored: {{error}}', { folder: outcome.source, error: outcome.error })}
              </Alert>
            ) : (
              <Alert severity="success" onClose={() => setOutcome(null)} sx={{ overflowWrap: 'anywhere' }}>
                {t('mgmt.config.snapshots.restore.done', '{{folder}} is back to how it was in the snapshot.', { folder: outcome.source })}
                {outcome.keptAs
                  ? ' ' + t('mgmt.config.snapshots.restore.keptAs', 'The folder as it was before the restore is kept as {{path}}; delete it when you no longer need it.', { path: outcome.keptAs })
                  : ''}
              </Alert>
            ))}

            {status && !status.enabled && (
              <Typography variant="body2" color="text.secondary">
                {t('mgmt.config.snapshots.status.off', 'The incremental backup is off: no snapshot is made, on a schedule or with the button.')}
              </Typography>
            )}
            {unsaved && (
              <Typography variant="body2" color="text.secondary">
                {t('mgmt.config.snapshots.status.unsaved', 'The settings above were changed: save them before backing up.')}
              </Typography>
            )}

            {status && (status.sources || []).map((source) => {
              const list = source.snapshots || [];
              const shown = expanded[source.path] ? list : list.slice(0, SHOWN_AT_FIRST);
              return (
                <Stack key={source.path} spacing={0.75}>
                  <Typography variant="subtitle2" sx={{ overflowWrap: 'anywhere' }}>{source.path}</Typography>
                  {source.lastError && <Alert severity="warning" sx={{ overflowWrap: 'anywhere' }}>{source.lastError}</Alert>}
                  {list.length === 0 && (
                    <Typography variant="body2" color="text.secondary">
                      {t('mgmt.config.snapshots.status.none', 'No snapshot yet.')}
                    </Typography>
                  )}
                  {shown.map((snapshot) => (
                    <Stack key={snapshot.id} direction="row" spacing={1} alignItems="center" useFlexGap flexWrap="wrap">
                      <Typography variant="body2" sx={{ overflowWrap: 'anywhere', flexGrow: 1, flexBasis: 0, minWidth: '12em' }}>
                        {shownDate(snapshot.finished) || snapshot.id}
                        {' · '}
                        {t('mgmt.config.snapshots.status.detail', 'files: {{files}} · new or changed: {{copied}} · unchanged: {{linked}}', {
                          files: Number(snapshot.files || 0).toLocaleString(),
                          copied: simplifyNumber(snapshot.bytesCopied || 0, 'B'),
                          linked: simplifyNumber(snapshot.bytesLinked || 0, 'B'),
                        })}
                        {snapshot.errors > 0 && (
                          <Typography component="span" variant="body2" color="warning.main">
                            {' · '}
                            {t('mgmt.config.snapshots.status.errors', 'not backed up: {{errors}}', { errors: Number(snapshot.errors).toLocaleString() })}
                          </Typography>
                        )}
                      </Typography>
                      <PermissionGuard permission={PERM_ADMIN}>
                        <Button
                          size="small"
                          variant="outlined"
                          disabled={running}
                          startIcon={<RollbackOutlined />}
                          onClick={() => setConfirm({ source, snapshot })}
                        >
                          {t('mgmt.config.snapshots.restore.button', 'Restore')}
                        </Button>
                      </PermissionGuard>
                    </Stack>
                  ))}
                  {list.length > SHOWN_AT_FIRST && (
                    <Stack direction="row">
                      <Button size="small" onClick={() => setExpanded({ ...expanded, [source.path]: !expanded[source.path] })}>
                        {expanded[source.path]
                          ? t('mgmt.config.snapshots.status.showFewer', 'Show the newest only')
                          : t('mgmt.config.snapshots.status.showAll', 'Show all {{number}}', { number: list.length })}
                      </Button>
                    </Stack>
                  )}
                </Stack>
              );
            })}

            <Typography variant="caption" color="text.secondary" sx={{ overflowWrap: 'anywhere' }}>
              {status && status.destination
                ? t('mgmt.config.snapshots.status.noteWhere',
                  'The list reflects the saved settings. The snapshots are in {{destination}}, one folder per folder backed up.', { destination: status.destination })
                : t('mgmt.config.snapshots.status.note', 'The list reflects the saved settings.')}
            </Typography>
          </Stack>

          {confirm && <ConfirmModalDirect
            callback={restore}
            onClose={() => setConfirm(null)}
            content={t('mgmt.config.snapshots.restore.confirm',
              'Put {{folder}} back to how it was on {{date}}? The folder as it is now is not deleted: it is kept next to it as {{name}}.before-restore-…, which takes as much room again. Stop the apps that use this folder first, and start them again afterwards.',
              { folder: confirm.source.path, date: shownDate(confirm.snapshot.finished) || confirm.snapshot.id, name: baseName(confirm.source.path) })}
          />}
        </Grid>
      </Grid>
    </MainCard>
  );
};

export default Snapshots;
