// Fork addition: "Telegram notifications" card of the general settings. It
// sets up the notifier of src/telegram on the server: the bot, the chat,
// what is sent and when. The settings live in the page's form (see the three
// helpers below, used by index.jsx; their logic is in
// utils/telegramSettings.js); the test button and the delivery status talk
// to /api/telegram.
//
// The bot token is a secret: the server never sends it, so the token field
// always starts empty and only ever holds what is typed into it.
import * as React from 'react';
import * as Yup from 'yup';
import * as API from '../../api';
import MainCard from '../../components/MainCard';
import {
  Alert,
  Button,
  FormHelperText,
  Grid,
  Stack,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { SendOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { CosmosCheckbox, CosmosInputPassword, CosmosInputText, CosmosSelect } from './users/formShortcuts';
import PermissionGuard from '../../components/permissionGuard';
import { PERM_ADMIN } from '../../utils/permissions';
import {
  MAX_PER_MINUTE,
  TELEGRAM_CATEGORIES,
  canSendTest,
  chatProblem,
  maxPerMinuteProblem,
  quietProblem,
  serverNameProblem,
  telegramInitialValues as initialValues,
  telegramToConfig as toConfig,
  telegramUnsaved,
  tokenIsSaved,
  tokenProblem,
  topicProblem,
} from '../../utils/telegramSettings';

// What the form starts from, and what is saved as config.Telegram.
export const telegramInitialValues = initialValues;
export const telegramToConfig = toConfig;

// The form's checks; the server checks again when the settings are saved.
// config tells whether a token is stored (the form never holds it).
export const telegramValidation = (t, config) => {
  const check = (name, problem, messages) => Yup.mixed().test(name, '', function validate() {
    const found = problem(this.parent, config);
    return found ? this.createError({ message: messages[found] || messages.shape }) : true;
  });
  return {
    Telegram_BotToken: check('telegram-token', tokenProblem, {
      required: t('mgmt.config.telegram.token.required', 'Enter the bot token, or switch the notifications off'),
      shape: t('mgmt.config.telegram.token.validation', 'This is not a bot token. Copy the whole token BotFather gave you: digits, a colon, then letters and digits'),
    }),
    Telegram_ChatID: check('telegram-chat', chatProblem, {
      required: t('mgmt.config.telegram.chat.required', 'Enter the chat ID, or switch the notifications off'),
      shape: t('mgmt.config.telegram.chat.validation', 'A chat ID is a number such as 123456789 or -1001234567890, or the public name of a channel such as @mychannel'),
    }),
    Telegram_TopicID: check('telegram-topic', topicProblem, {
      shape: t('mgmt.config.telegram.topic.validation', 'A topic ID is a whole number above zero, or empty'),
    }),
    Telegram_ServerName: check('telegram-name', serverNameProblem, {
      long: t('mgmt.config.telegram.serverName.long', 'At most 64 characters'),
      shape: t('mgmt.config.telegram.serverName.validation', 'The name must be on one line, without invisible characters'),
    }),
    Telegram_QuietTo: check('telegram-quiet', quietProblem, {
      shape: t('mgmt.config.update.timeValidation', 'Enter a time as HH:MM, for example 02:00'),
      same: t('mgmt.config.telegram.quiet.same', 'The quiet hours must start and end at different times'),
    }),
    Telegram_MaxPerMinute: check('telegram-cap', maxPerMinuteProblem, {
      range: t('mgmt.config.telegram.cap.validation', 'Enter a whole number between 1 and 15'),
    }),
  };
};

const shownDate = (value) => {
  const date = value ? new Date(value) : null;
  return date && !isNaN(date.getTime()) ? date.toLocaleString() : '';
};

// One line each on what a kind of message is: [key, English text].
const CATEGORY_TEXT = {
  security: [
    ['mgmt.config.telegram.cat.security', 'Security events'],
    ['mgmt.config.telegram.cat.securityHelp', 'From the security record: a locked two-factor check, bans, a refused attempt to grant a permission, role changes, a terminal opened on the server itself, a snapshot restore. How much more is decided by the minimum severity below.'],
  ],
  logins: [
    ['mgmt.config.telegram.cat.logins', 'Logins from a new address'],
    ['mgmt.config.telegram.cat.loginsHelp', 'A password accepted from an address that was not seen before for that account. The security record starts with this version, so at first every address is new.'],
  ],
  system: [
    ['mgmt.config.telegram.cat.system', 'System'],
    ['mgmt.config.telegram.cat.systemHelp', 'A server update is available, was installed or failed; Cosmos started again after a restart or an unexpected stop; certificate problems; the configuration backup or the incremental backup failed; router port forwarding failed; a scheduled job failed; monitoring alerts.'],
  ],
  storage: [
    ['mgmt.config.telegram.cat.storage', 'Storage'],
    ['mgmt.config.telegram.cat.storageHelp', 'A disk is nearly full (the "disk full" alerts under Monitoring), SnapRAID sync or scrub failed.'],
  ],
  apps: [
    ['mgmt.config.telegram.cat.apps', 'Apps'],
    ['mgmt.config.telegram.cat.appsHelp', 'A container was updated automatically or could not be updated; a container keeps stopping with an error; monitoring alerts about an app.'],
  ],
  other: [
    ['mgmt.config.telegram.cat.other', 'Everything else'],
    ['mgmt.config.telegram.cat.otherHelp', 'Any other notification Cosmos shows to administrators under the bell, such as server errors.'],
  ],
};

const Telegram = ({ formik, config }) => {
  const { t } = useTranslation();
  const [status, setStatus] = React.useState(null);
  const [sending, setSending] = React.useState(false);
  const [result, setResult] = React.useState(null);
  const alive = React.useRef(true);

  const values = formik.values;
  const saved = tokenIsSaved(config);
  const forgetting = Boolean(values.Telegram_ForgetToken);
  const typed = String(values.Telegram_BotToken || '').trim() !== '';
  const unsaved = telegramUnsaved(values, config);

  const loadStatus = () => API.config.telegramStatus()
    .then((response) => { if (alive.current) setStatus(response.data); })
    .catch(() => {}); // the status is an extra: the card works without it

  // when the page opens, and again when the settings were saved or refreshed
  React.useEffect(() => { loadStatus(); }, [config]);
  React.useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);

  const sendTest = () => {
    setSending(true);
    setResult(null);
    API.config.telegramTest(telegramToConfig(values, config))
      .then((response) => { if (alive.current) setResult(response.data); })
      .catch((error) => {
        if (alive.current) setResult({ success: false, message: (error && error.message) || String(error) });
      })
      .finally(() => {
        if (alive.current) setSending(false);
        loadStatus();
      });
  };

  let tokenHelp = t('mgmt.config.telegram.token.helpNone', 'The token BotFather gave you, such as 123456789:AAE…. It is stored on the server and never shown again.');
  if (saved && forgetting) {
    tokenHelp = t('mgmt.config.telegram.token.helpForget', 'The saved token is removed when you save the settings.');
  } else if (saved && typed) {
    tokenHelp = t('mgmt.config.telegram.token.helpReplace', 'This token replaces the saved one when you save the settings.');
  } else if (saved) {
    tokenHelp = t('mgmt.config.telegram.token.helpSaved', 'A token is saved. It is never shown again: leave this empty to keep it, or type a new one to replace it.');
  }

  return (
    <MainCard title={t('mgmt.config.telegram.title', 'Telegram notifications')}>
      <Grid container spacing={3}>
        <Grid item xs={12}>
          <Alert severity="info">
            {t('mgmt.config.telegram.info',
              "Cosmos can tell you in a Telegram chat what happens on this server. The messages pass through Telegram's servers (api.telegram.org) and are kept there; they are not end-to-end encrypted. Nothing is sent while this is switched off.")}
          </Alert>
        </Grid>

        <CosmosCheckbox
          label={t('mgmt.config.telegram.enableLabel', 'Send notifications to a Telegram chat')}
          name="Telegram_Enabled"
          formik={formik}
        />

        {/* how to get the two things the form asks for */}
        <Grid item xs={12}>
          <Stack spacing={0.5}>
            <Typography variant="subtitle1">{t('mgmt.config.telegram.howto.title', 'Setting it up')}</Typography>
            <Typography variant="body2" component="ol" sx={{ pl: 2.5, m: 0, overflowWrap: 'anywhere', '& li': { mb: 0.5 } }}>
              <li>{t('mgmt.config.telegram.howto.bot', 'In Telegram, open a chat with @BotFather, send /newbot and answer its two questions. It replies with a token: paste it below.')}</li>
              <li>{t('mgmt.config.telegram.howto.start', 'Open the chat with your new bot and press Start. A bot cannot write to someone who has not done that.')}</li>
              <li>{t('mgmt.config.telegram.howto.chat', 'Send your bot any message, then open https://api.telegram.org/bot<your token>/getUpdates in a browser: the number after "chat":{"id": is your chat ID. Paste it below.')}</li>
              <li>{t('mgmt.config.telegram.howto.group', 'For a group, add the bot to the group, write a message there and look at the same page again: the ID of a group is negative. For a channel, make the bot an administrator of it.')}</li>
              <li>{t('mgmt.config.telegram.howto.test', 'Press "Send a test message". It works before the settings are saved.')}</li>
            </Typography>
          </Stack>
        </Grid>

        <CosmosInputPassword
          label={t('mgmt.config.telegram.token.label', 'Bot token')}
          name="Telegram_BotToken"
          autoComplete="new-password"
          formik={formik}
          helperText={tokenHelp}
          noStrength
        />
        {saved && !typed && (
          <Grid item xs={12} sx={{ pt: '0 !important' }}>
            <Button
              size="small"
              color={forgetting ? 'primary' : 'warning'}
              onClick={() => formik.setFieldValue('Telegram_ForgetToken', !forgetting)}
            >
              {forgetting
                ? t('mgmt.config.telegram.token.keep', 'Keep the saved token')
                : t('mgmt.config.telegram.token.forget', 'Remove the saved token')}
            </Button>
          </Grid>
        )}

        <CosmosInputText
          label={t('mgmt.config.telegram.chat.label', 'Chat ID')}
          name="Telegram_ChatID"
          placeholder="123456789"
          autoComplete="off"
          formik={formik}
          helperText={t('mgmt.config.telegram.chat.help', 'The number of your own chat with the bot, of a group (negative) or of a channel (starts with -100), or the public name of a channel such as @mychannel.')}
        />
        <CosmosInputText
          label={t('mgmt.config.telegram.topic.label', 'Topic ID (optional)')}
          name="Telegram_TopicID"
          autoComplete="off"
          inputProps={{ inputMode: 'numeric' }}
          formik={formik}
          helperText={t('mgmt.config.telegram.topic.help', 'Only for a group with topics: the number of the topic the messages go to. It is the middle number in the link of any message of that topic.')}
        />
        <CosmosInputText
          label={t('mgmt.config.telegram.serverName.label', 'Name of this server in the messages')}
          name="Telegram_ServerName"
          placeholder={(config && config.HTTPConfig && config.HTTPConfig.Hostname) || ''}
          autoComplete="off"
          formik={formik}
          helperText={t('mgmt.config.telegram.serverName.help', 'Every message starts with it. Empty: the hostname of the HTTP settings.')}
        />

        {/* what is sent */}
        <Grid item xs={12} sx={{ pb: 0 }}>
          <Typography variant="subtitle1">{t('mgmt.config.telegram.cat.title', 'What is sent')}</Typography>
        </Grid>
        {TELEGRAM_CATEGORIES.map(({ name }) => {
          const [[labelKey, label], [helpKey, help]] = CATEGORY_TEXT[name];
          return (
            <CosmosCheckbox
              key={name}
              label={t(labelKey, label)}
              name={'Telegram_Cat_' + name}
              formik={formik}
              helperText={t(helpKey, help)}
            />
          );
        })}

        <CosmosSelect
          name="Telegram_MinSeverity"
          label={t('mgmt.config.telegram.severity.label', 'Security events: minimum severity')}
          formik={formik}
          disabled={!values.Telegram_Cat_security}
          options={[
            ['high', t('mgmt.config.telegram.severity.high', 'High (recommended)')],
            ['medium', t('mgmt.config.telegram.severity.medium', 'Medium')],
            ['low', t('mgmt.config.telegram.severity.low', 'Low')],
          ]}
          helperText={t('mgmt.config.telegram.severity.help', 'High: only what a stranger cannot cause; these events are critical, are never held back by the limit below and still ring during the quiet hours. Medium: also failed logins, probes, strikes and changes of settings and accounts. Low: nearly everything in the record. With medium or low, expect messages for the background noise of the internet; many of one kind are summed up in one message.')}
        />

        <CosmosCheckbox
          label={t('mgmt.config.telegram.details.label', 'Include addresses and names in the messages')}
          name="Telegram_IncludeDetails"
          formik={formik}
          helperText={t('mgmt.config.telegram.details.help', 'User names, IP addresses, container names, paths and the text of errors. Switched off, a message only says what kind of thing happened and that the details are in the panel: nothing that identifies your users or your network leaves the server.')}
        />

        {/* when it makes a sound, and how much */}
        <CosmosCheckbox
          label={t('mgmt.config.telegram.quiet.label', 'Quiet hours: deliver without sound')}
          name="Telegram_QuietEnabled"
          formik={formik}
          helperText={t('mgmt.config.telegram.quiet.help', 'During these hours messages still arrive, but silently: nothing is held back or lost.')}
        />
        {values.Telegram_QuietEnabled && (<>
          <CosmosInputText
            label={t('mgmt.config.telegram.quiet.from', 'Quiet from (server time)')}
            name="Telegram_QuietFrom"
            type="time"
            formik={formik}
          />
          <CosmosInputText
            label={t('mgmt.config.telegram.quiet.to', 'Quiet until (server time)')}
            name="Telegram_QuietTo"
            type="time"
            formik={formik}
          />
          <CosmosCheckbox
            label={t('mgmt.config.telegram.quiet.critical', 'Critical messages still ring during the quiet hours')}
            name="Telegram_QuietCriticalRings"
            formik={formik}
          />
        </>)}

        <CosmosInputText
          label={t('mgmt.config.telegram.cap.label', 'Messages per minute at most')}
          name="Telegram_MaxPerMinute"
          type="number"
          inputProps={{ min: 1, max: MAX_PER_MINUTE, step: 1, inputMode: 'numeric' }}
          formik={formik}
          helperText={t('mgmt.config.telegram.cap.help', 'What goes over it, and more than three of one kind in five minutes, is counted and sent as one summary. Critical messages do not count against this limit.')}
        />

        {/* try it, and how deliveries go */}
        <Grid item xs={12}>
          <Stack spacing={1.5}>
            <Stack direction="row" spacing={1.5} alignItems="center" useFlexGap flexWrap="wrap">
              <PermissionGuard permission={PERM_ADMIN}>
                <LoadingButton
                  variant="outlined"
                  loading={sending}
                  disabled={!canSendTest(values, config)}
                  startIcon={<SendOutlined />}
                  onClick={sendTest}
                >
                  {t('mgmt.config.telegram.test.button', 'Send a test message')}
                </LoadingButton>
              </PermissionGuard>
              <Typography variant="caption" color="text.secondary" sx={{ flexBasis: 0, flexGrow: 1, minWidth: '14em' }}>
                {t('mgmt.config.telegram.test.help', 'Uses the bot, the chat and the name above as they are now, saved or not.')}
              </Typography>
            </Stack>

            {result && (
              <Alert severity={result.success ? 'success' : 'error'} onClose={() => setResult(null)} sx={{ overflowWrap: 'anywhere' }}>
                <strong>
                  {result.success
                    ? t('mgmt.config.telegram.test.success', 'Telegram accepted the test message: look at the chat.')
                    : t('mgmt.config.telegram.test.failure', 'The test message was not sent.')}
                </strong>
                {!result.success && result.message ? ' ' + result.message : ''}
              </Alert>
            )}

            <Typography variant="subtitle1">{t('mgmt.config.telegram.status.title', 'Deliveries')}</Typography>
            {unsaved && (
              <Typography variant="body2" color="text.secondary">
                {t('mgmt.config.telegram.status.unsaved', 'The settings above were changed: they apply once saved. What follows is about the saved settings.')}
              </Typography>
            )}
            {status && !status.enabled && (
              <Typography variant="body2" color="text.secondary">
                {t('mgmt.config.telegram.status.off', 'The notifications are off: nothing is sent.')}
              </Typography>
            )}
            {status && status.enabled && !status.ready && (
              <Alert severity="warning">
                {t('mgmt.config.telegram.status.notReady', 'The notifications are on, but the saved bot token or chat ID cannot be used: nothing is sent.')}
              </Alert>
            )}
            {status && status.enabled && status.ready && (<>
              <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
                {status.lastSent
                  ? t('mgmt.config.telegram.status.lastSent', 'Last message sent: {{when}}.', { when: shownDate(status.lastSent) })
                  : t('mgmt.config.telegram.status.noneSent', 'No message was sent since Cosmos started.')}
                {(status.waiting > 0 || status.held > 0) && ' '}
                {(status.waiting > 0 || status.held > 0) && t('mgmt.config.telegram.status.pending', 'Waiting to be sent: {{waiting}}; counted for the next summary: {{held}}.', {
                  waiting: Number(status.waiting || 0).toLocaleString(),
                  held: Number(status.held || 0).toLocaleString(),
                })}
              </Typography>
              {status.lastError && (
                <Alert severity="error" sx={{ overflowWrap: 'anywhere' }}>
                  {t('mgmt.config.telegram.status.lastError', 'Last problem ({{when}}): {{error}}', { when: shownDate(status.lastErrorAt), error: status.lastError })}
                  {status.pausedUntil
                    ? ' ' + t('mgmt.config.telegram.status.paused', 'Nothing is sent until {{when}}; a changed token or chat, or a test message that works, ends the wait.', { when: shownDate(status.pausedUntil) })
                    : ''}
                </Alert>
              )}
            </>)}
            <FormHelperText>
              {t('mgmt.config.telegram.status.help', 'When messages cannot be delivered, the bell at the top of the panel says so once.')}
            </FormHelperText>
          </Stack>
        </Grid>
      </Grid>
    </MainCard>
  );
};

export default Telegram;
