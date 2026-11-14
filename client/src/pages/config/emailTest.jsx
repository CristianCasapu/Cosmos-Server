// Fork addition: "Check and send a test email" of the email settings
// (emailSettings.jsx). It sends the values currently in the form, so
// settings are tried before they are saved, shows the steps the server went
// through in plain words and, for the step that failed, what to do next.
import * as React from 'react';
import * as API from '../../api';
import { Alert, Stack, Typography } from '@mui/material';
import { SendOutlined } from '@ant-design/icons';
import { LoadingButton } from '@mui/lab';
import PermissionGuard from '../../components/permissionGuard';
import { FormField } from '../../components/formField';
import StepList from '../../components/stepList';
import { PERM_ADMIN } from '../../utils/permissions';
import { useTranslation } from 'react-i18next';
import { STEP_LABELS, adviceFor, isEmailAddress, splitSender } from '../../utils/smtpSetup';

// provider: the mail provider of the server in the form, if known (its
// hints sharpen the advice). unsaved: the form differs from the saved
// settings. needsPassword: the stored password cannot be used for what the
// form holds now. hasProblems: the form marks a value as wrong.
const EmailTest = ({ formik, provider, unsaved, needsPassword, hasProblems }) => {
  const { t } = useTranslation();
  const [recipient, setRecipient] = React.useState('');
  const [loading, setLoading] = React.useState(false);
  const [result, setResult] = React.useState(null);

  const values = formik.values;
  const sender = splitSender(values.Email_From).address;
  const recipientWrong = recipient.trim() !== '' && !isEmailAddress(recipient);
  const missing = !values.Email_Host || !String(values.Email_Port || '').trim() || !sender;
  const canTest = !missing && !needsPassword && !hasProblems && !recipientWrong;

  // a result is about the values it was made with
  const signature = [values.Email_Host, values.Email_Port, values.Email_Username, values.Email_Password,
    values.Email_From, values.Email_UseTLS, values.Email_AllowInsecureTLS].join('\n');
  React.useEffect(() => { setResult(null); }, [signature]);

  const sendTest = () => {
    setLoading(true);
    setResult(null);
    API.config.testEmail({
      Enabled: true,
      Host: String(values.Email_Host || '').trim(),
      Port: String(values.Email_Port || '').trim(),
      Username: values.Email_Username,
      Password: values.Email_Password,
      From: values.Email_From,
      UseTLS: Boolean(values.Email_UseTLS),
      AllowInsecureTLS: Boolean(values.Email_AllowInsecureTLS),
    }, recipient.trim()).then((response) => {
      setResult(response.data);
    }).catch((error) => {
      setResult({ success: false, steps: [], error: error.message || String(error) });
    }).finally(() => {
      setLoading(false);
    });
  };

  const failed = result && !result.success ? (result.steps || []).find((step) => !step.ok) : null;
  const stepLabel = (step) => {
    const label = STEP_LABELS[step.name];
    return label ? t(label[0], label[1]) : step.name;
  };

  let why = '';
  if (missing) {
    why = t('mgmt.config.email.test.missing', 'Needs the email address and a mail server first.');
  } else if (needsPassword) {
    why = t('mgmt.config.email.test.needsPassword', 'Type the password first (see above).');
  } else if (hasProblems) {
    why = t('mgmt.config.email.test.problems', 'Correct the values marked above first.');
  }

  return (
    <Stack spacing={1.5}>
      <Typography variant="subtitle1">{t('mgmt.config.email.test.title', 'Check before saving')}</Typography>
      <FormField
        id="Email_TestRecipient"
        label={t('mgmt.config.email.test.recipientLabel', 'Send a test email to')}
        type="email"
        inputMode="email"
        autoComplete="off"
        value={recipient}
        placeholder={sender || 'you@example.com'}
        error={recipientWrong && t('mgmt.config.email.test.recipientWrong', 'Enter one email address, or leave empty')}
        helperText={t('mgmt.config.email.test.recipientHelp', 'An address you can read right now. Empty: the test goes to the sender address itself.')}
        onChange={setRecipient}
        onEnter={() => { if (canTest && !loading) sendTest(); }}
      />
      <Stack direction="row" spacing={1.5} alignItems="center" useFlexGap flexWrap="wrap">
        <PermissionGuard permission={PERM_ADMIN}>
          <LoadingButton
            variant="outlined"
            loading={loading}
            disabled={!canTest}
            startIcon={<SendOutlined />}
            onClick={sendTest}
          >
            {t('mgmt.config.email.test.check', 'Check and send a test email')}
          </LoadingButton>
        </PermissionGuard>
        <Typography variant="caption" color="text.secondary" sx={{ minWidth: 0 }}>
          {why || t('mgmt.config.email.test.uses', 'Uses the values as they are shown now, saved or not. Nothing is saved by the check.')}
        </Typography>
      </Stack>

      {result && result.success && <Alert severity="success" sx={{ '& .MuiAlert-message': { minWidth: 0 } }}>
        <Stack spacing={0.75} sx={{ overflowWrap: 'anywhere' }}>
          <strong>{t('mgmt.config.email.test.worked', 'It works: the mail server took the test email for {{recipient}}.', { recipient: result.recipient })}</strong>
          <span>{t('mgmt.config.email.test.lookInbox', 'Look for it in that mailbox, also in its spam folder.')}</span>
          {unsaved && <span>{t('mgmt.config.email.test.notSavedYet', 'These settings are not saved yet: press Save below.')}</span>}
        </Stack>
      </Alert>}

      {result && !result.success && <Alert severity="error" sx={{ '& .MuiAlert-message': { width: '100%', minWidth: 0 } }}>
        <Stack spacing={1}>
          <strong>
            {failed
              ? t('mgmt.config.email.test.stoppedAt', 'The check stopped at: {{step}}', { step: stepLabel(failed) })
              : t('mgmt.config.email.test.failure', 'The test email could not be sent.')}
          </strong>
          <StepList
            steps={result.steps || []}
            label={stepLabel}
            advice={(step) => {
              const advice = adviceFor(step, { port: values.Email_Port, provider });
              return advice ? t(advice[0], advice[1], advice[2]) : '';
            }}
            detailLabel={t('mgmt.config.email.test.detail', 'The mail server said:')}
          />
          {!(result.steps || []).length && result.error && <span>{result.error}</span>}
        </Stack>
      </Alert>}
    </Stack>
  );
};

export default EmailTest;
