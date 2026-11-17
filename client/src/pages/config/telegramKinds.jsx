// Fork addition (plan 48): "Telegram messages", a card under the Telegram
// notifications: every kind of message grouped by severity, each with the
// first line a phone shows before the message is opened, the whole message
// as it arrives (rendered by the server from the same texts as the real
// ones, with sample values), a switch, and "Send me this sample". Per
// severity: with sound, silently or not at all. The switches and the
// deliveries live in the page's form (Telegram_Kinds,
// Telegram_Delivery_<severity>) and are saved with the other settings; the
// logic is in utils/telegramMessage.js.
import * as React from 'react';
import * as API from '../../api';
import MainCard from '../../components/MainCard';
import {
  Alert,
  Box,
  Button,
  Collapse,
  FormControlLabel,
  Grid,
  Stack,
  Switch,
  Typography,
} from '@mui/material';
import { Button as LoadingButton } from '@mui/material'; // MUI 9: Button has the loading state
import { DownOutlined, SendOutlined, UpOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { CosmosCheckbox, CosmosInputText, CosmosSelect } from './users/formShortcuts';
import PermissionGuard from '../../components/permissionGuard';
import { PERM_ADMIN } from '../../utils/permissions';
import {
  SEVERITIES,
  deliveryOf,
  flipKind,
  kindOn,
  kindsOf,
  messageParts,
  previewLine,
  whyNotSent,
} from '../../utils/telegramMessage';
import { telegramUnsaved } from '../../utils/telegramSettings';
import { MAX_FILL_DAYS, watchProblem } from '../../utils/watchSettings';

const SEVERITY_TEXT = {
  critical: ['🔴', ['mgmt.config.telegram.kinds.critical', 'Critical'], ['mgmt.config.telegram.kinds.criticalHelp', 'Something is wrong now and needs you: rings, also in the quiet hours, and stays pinned in the chat while it lasts.']],
  error: ['🟠', ['mgmt.config.telegram.kinds.error', 'Error'], ['mgmt.config.telegram.kinds.errorHelp', 'Something failed and stays failed until it is looked at.']],
  warn: ['🟡', ['mgmt.config.telegram.kinds.warn', 'Warning'], ['mgmt.config.telegram.kinds.warnHelp', 'Worth knowing soon; often nothing to do.']],
  info: ['🔵', ['mgmt.config.telegram.kinds.info', 'Info'], ['mgmt.config.telegram.kinds.infoHelp', 'For the record, and the messages that say something is over (🟢 OK): they answer the message that told it.']],
};

// The message as Telegram shows it: values in a fixed-width font, the error
// text as a block, the button under it. Text only: nothing is read as HTML.
const MessageView = ({ kind }) => {
  const { t } = useTranslation();
  return (
    <Box sx={{ border: 1, borderColor: 'divider', borderRadius: 1, p: 1.5, bgcolor: 'background.default' }}>
      <Typography variant="body2" component="div" sx={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>
        {messageParts(kind.text, kind.entities).map((part, i) => {
          if (part.type === 'code') {
            return <Box key={i} component="code" sx={{ fontFamily: 'monospace', bgcolor: 'action.hover', borderRadius: 0.5, px: 0.25 }}>{part.text}</Box>;
          }
          if (part.type === 'pre') {
            return <Box key={i} component="pre" sx={{ fontFamily: 'monospace', bgcolor: 'action.hover', borderRadius: 0.5, p: 0.75, m: 0, display: 'block', whiteSpace: 'pre-wrap' }}>{part.text}</Box>;
          }
          return <React.Fragment key={i}>{part.text}</React.Fragment>;
        })}
      </Typography>
      {kind.button && (
        <Box
          className="telegram-sample-button"
          title={kind.button.url}
          sx={{ mt: 1, border: 1, borderColor: 'divider', borderRadius: 1, py: 0.75, textAlign: 'center', color: 'primary.main', typography: 'body2' }}
        >
          {kind.button.text}
        </Box>
      )}
      {!kind.button && (
        <Typography variant="caption" sx={{ color: 'text.secondary', display: 'block', mt: 1 }}>
          {t('mgmt.config.telegram.kinds.noButton', 'No button: the panel has no address a phone can open (the hostname of the HTTP settings).')}
        </Typography>
      )}
    </Box>
  );
};

const KindRow = ({ kind, formik, canSend }) => {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const [sending, setSending] = React.useState(false);
  const [result, setResult] = React.useState(null);
  const alive = React.useRef(true);
  React.useEffect(() => () => { alive.current = false; }, []);

  const values = formik.values;
  const on = kindOn(kind, values);
  const why = whyNotSent(kind, values);
  const id = 'telegram-kind-' + kind.kind.replace(/[^a-z0-9]+/gi, '-');

  const send = () => {
    setSending(true);
    setResult(null);
    API.telegramMessages.sample(kind.kind)
      .then((response) => { if (alive.current) setResult((response && response.data) || {}); })
      .catch((error) => { if (alive.current) setResult({ success: false, message: (error && error.message) || String(error) }); })
      .finally(() => { if (alive.current) setSending(false); });
  };

  return (
    <Box id={id} data-kind={kind.kind} className="telegram-kind" sx={{ borderTop: 1, borderColor: 'divider', py: 1.5 }}>
      <Stack direction="row" spacing={1} sx={{ alignItems: 'center', flexWrap: 'wrap' }} useFlexGap>
        {kind.own ? (
          <Typography variant="subtitle2" sx={{ flexGrow: 1, minWidth: '12em', minHeight: 44, display: 'flex', alignItems: 'center' }}>{kind.label}</Typography>
        ) : (
          <FormControlLabel
            sx={{ flexGrow: 1, minWidth: '12em', minHeight: 44, mr: 0 }}
            control={<Switch checked={on} onChange={() => formik.setFieldValue('Telegram_Kinds', flipKind(kind, values))} inputProps={{ 'aria-label': kind.label }} />}
            label={<Typography variant="subtitle2" component="span">{kind.label}</Typography>}
          />
        )}
        <Button
          size="small"
          variant="text"
          onClick={() => setOpen(!open)}
          endIcon={open ? <UpOutlined /> : <DownOutlined />}
          aria-expanded={open}
          sx={{ minHeight: 44 }}
        >
          {open ? t('mgmt.config.telegram.kinds.hide', 'Hide the message') : t('mgmt.config.telegram.kinds.show', 'Show the message')}
        </Button>
        <PermissionGuard permission={PERM_ADMIN}>
          <LoadingButton
            size="small"
            variant="outlined"
            loading={sending}
            disabled={!canSend}
            startIcon={<SendOutlined />}
            onClick={send}
            sx={{ minHeight: 44 }}
          >
            {t('mgmt.config.telegram.kinds.send', 'Send me this sample')}
          </LoadingButton>
        </PermissionGuard>
      </Stack>
      {/* what the phone shows before the message is opened: one line, cut where the screen ends */}
      <Box className="telegram-preview" title={kind.preview} sx={{ mt: 0.5, px: 1.5, py: 1, borderRadius: 1, bgcolor: 'action.hover', minWidth: 0 }}>
        <Typography variant="body2" noWrap>{previewLine(kind.preview, 200)}</Typography>
      </Box>
      {why && (
        <Typography variant="caption" sx={{ color: 'text.secondary', display: 'block', mt: 0.5 }}>{t(why[0], why[1])}</Typography>
      )}
      <Collapse in={open} unmountOnExit>
        <Box sx={{ mt: 1 }}><MessageView kind={kind} /></Box>
      </Collapse>
      {result && (
        <Alert severity={result.success ? 'success' : 'error'} onClose={() => setResult(null)} sx={{ mt: 1, overflowWrap: 'anywhere' }}>
          {result.success
            ? t('mgmt.config.telegram.kinds.sent', 'Telegram accepted the sample: look at the chat.')
            : t('mgmt.config.telegram.kinds.notSent', 'The sample was not sent.') + (result.message ? ' ' + result.message : '')}
        </Alert>
      )}
    </Box>
  );
};

const TelegramKinds = ({ formik, config }) => {
  const { t } = useTranslation();
  const [kinds, setKinds] = React.useState(null);
  const [failed, setFailed] = React.useState(false);

  React.useEffect(() => {
    let alive = true;
    API.telegramMessages.kinds()
      .then((response) => {
        if (!alive) return;
        const data = (response && response.data) || {};
        setKinds(Array.isArray(data.kinds) ? data.kinds : []);
        setFailed(false);
      })
      .catch(() => { if (alive) setFailed(true); });
    return () => { alive = false; };
  }, [config]);

  const saved = (config && config.Telegram) || {};
  const canSend = Boolean(saved.Enabled && saved.BotToken && saved.ChatID);
  const unsaved = telegramUnsaved(formik.values, config);

  return (
    <MainCard title={t('mgmt.config.telegram.kinds.title', 'Telegram messages')}>
      <Grid container spacing={2}>
        <Grid size={{ xs: 12 }}>
          <Typography variant="body2">
            {t('mgmt.config.telegram.kinds.intro', 'Every kind of message, by severity, as it arrives. The grey line is what your phone shows before you open the message; "Show the message" shows all of it. The samples use made-up values and the settings as saved.')}
          </Typography>
          {unsaved && (
            <Typography variant="caption" sx={{ color: 'text.secondary', display: 'block', mt: 0.5 }}>
              {t('mgmt.config.telegram.kinds.unsaved', 'The settings were changed: the switches below apply once saved, and a sample is sent with the saved settings.')}
            </Typography>
          )}
          {!canSend && (
            <Typography variant="caption" sx={{ color: 'text.secondary', display: 'block', mt: 0.5 }}>
              {t('mgmt.config.telegram.kinds.needSaved', 'Samples can be sent once the notifications are switched on with a bot token and a chat, and saved.')}
            </Typography>
          )}
        </Grid>
        {failed && (
          <Grid size={{ xs: 12 }}>
            <Alert severity="warning">{t('mgmt.config.telegram.kinds.failed', 'The samples could not be loaded. The switches still work.')}</Alert>
          </Grid>
        )}
        {/* plan 48: what Cosmos watches by itself, and when it speaks up */}
        <Grid size={{ xs: 12 }} id="telegram-watches">
          <Stack spacing={1}>
            <Typography variant="h5" component="h3">{t('mgmt.config.watch.title', 'What Cosmos watches by itself')}</Typography>
            <Typography variant="body2" sx={{ color: 'text.secondary' }}>
              {t('mgmt.config.watch.help', 'Each watch writes a notification in the panel, and a message here, when something starts and when it is over. Nothing is asked of other servers.')}
            </Typography>
          </Stack>
        </Grid>
        <CosmosCheckbox name="Watch_SMART" formik={formik}
          label={t('mgmt.config.watch.smart', 'Disks that report they are failing (S.M.A.R.T.)')}
          helperText={t('mgmt.config.watch.smartHelp', 'Every hour. A disk that sleeps is not woken: it is read when it is awake.')} />
        <CosmosCheckbox name="Watch_Fill" formik={formik}
          label={t('mgmt.config.watch.fill', 'Disks filling fast')}
          helperText={t('mgmt.config.watch.fillHelp', 'The space used is measured every half hour; the trend of the last day says when the disk is full.')} />
        {formik.values.Watch_Fill && (
          <CosmosInputText name="Watch_FillDays" formik={formik} type="number"
            inputProps={{ min: 1, max: MAX_FILL_DAYS, step: 1, inputMode: 'numeric' }}
            label={t('mgmt.config.watch.fillDays', 'Warn when a disk is full within (days)')}
            helperText={watchProblem(formik.values) === 'days'
              ? t('mgmt.config.watch.fillDaysBad', 'Enter a whole number of days between 1 and 30')
              : t('mgmt.config.watch.fillDaysHelp', 'At the rate of the last day. Proposed: 3.')} />
        )}
        <CosmosCheckbox name="Watch_Docker" formik={formik}
          label={t('mgmt.config.watch.docker', 'Docker not answering')}
          helperText={t('mgmt.config.watch.dockerHelp', 'Asked every 30 seconds; said after two minutes without an answer, not in the first minutes after Cosmos starts.')} />
        <CosmosCheckbox name="Watch_Snapshots" formik={formik}
          label={t('mgmt.config.watch.snapshots', 'Incremental backup folders without a recent snapshot')}
          helperText={t('mgmt.config.watch.snapshotsHelp', 'Late when the last snapshot is older than the schedule allows plus half a period (at least 6 hours).')} />
        {formik.values.Watch_Snapshots && (
          <CosmosInputText name="Watch_SnapshotLateHours" formik={formik} type="number"
            inputProps={{ min: 1, step: 1, inputMode: 'numeric' }}
            label={t('mgmt.config.watch.lateHours', 'Late after (hours, optional)')}
            helperText={watchProblem(formik.values) === 'hours'
              ? t('mgmt.config.watch.lateHoursBad', 'Enter a whole number of hours between 1 and 2160, or leave it empty')
              : t('mgmt.config.watch.lateHoursHelp', 'Empty: from the schedule (36 hours for a daily backup).')} />
        )}
        <CosmosCheckbox name="Watch_Country" formik={formik}
          label={t('mgmt.config.watch.country', 'Sign-ins from a country an account never signed in from')}
          helperText={t('mgmt.config.watch.countryHelp', 'Looked up in the country file Cosmos ships, never on another server. Home and VPN addresses are not a country. Only the countries are kept, a few per account; the first sign-in of an account only records. Its Telegram message is off until switched on below.')} />
        {SEVERITIES.map((severity) => {
          const [mark, [labelKey, label], [helpKey, help]] = SEVERITY_TEXT[severity];
          const list = kindsOf(kinds, severity);
          const mode = deliveryOf(severity, formik.values);
          return (
            <Grid size={{ xs: 12 }} key={severity} id={'telegram-severity-' + severity} className="telegram-severity" data-severity={severity}>
              <Stack spacing={1}>
                <Typography variant="h5" component="h3">{mark} {t(labelKey, label)}</Typography>
                <Typography variant="body2" sx={{ color: 'text.secondary' }}>{t(helpKey, help)}</Typography>
                <Grid container>
                  <CosmosSelect
                    name={'Telegram_Delivery_' + severity}
                    label={t('mgmt.config.telegram.kinds.delivery', 'How these messages arrive')}
                    formik={formik}
                    options={[
                      ['sound', t('mgmt.config.telegram.kinds.sound', 'With sound')],
                      ['silent', t('mgmt.config.telegram.kinds.silent', 'Silently (no sound, no vibration)')],
                      ['off', t('mgmt.config.telegram.kinds.off', 'Not sent')],
                    ]}
                  />
                </Grid>
                {mode === 'off' && (
                  <Alert severity="warning">{t('mgmt.config.telegram.kinds.severityOff', 'Nothing of this severity is sent.')}</Alert>
                )}
                <Box>
                  {kinds === null && !failed && (
                    <Typography variant="body2" sx={{ color: 'text.secondary' }}>{t('mgmt.config.telegram.kinds.loading', 'Loading the samples…')}</Typography>
                  )}
                  {list.map((kind) => <KindRow key={kind.kind} kind={kind} formik={formik} canSend={canSend} />)}
                </Box>
              </Stack>
            </Grid>
          );
        })}
      </Grid>
    </MainCard>
  );
};

export default TelegramKinds;
