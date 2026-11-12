// Fork addition: "send a test email" for the SMTP settings form. It sends the
// values currently in the form, so settings can be tried before saving them.
import * as React from 'react';
import * as API from '../../api';
import { Alert, InputLabel, OutlinedInput, Stack, Typography } from '@mui/material';
import { CheckCircleOutlined, CloseCircleOutlined, SendOutlined } from '@ant-design/icons';
import { LoadingButton } from '@mui/lab';
import PermissionGuard from '../../components/permissionGuard';
import { PERM_ADMIN } from '../../utils/permissions';
import { useTranslation } from 'react-i18next';

const STEP_LABELS = {
  connect: ['mgmt.config.email.test.step.connect', 'Connect to server'],
  tls: ['mgmt.config.email.test.step.tls', 'Encryption'],
  auth: ['mgmt.config.email.test.step.auth', 'Sign in'],
  sender: ['mgmt.config.email.test.step.sender', 'Sender accepted'],
  recipient: ['mgmt.config.email.test.step.recipient', 'Recipient accepted'],
  send: ['mgmt.config.email.test.step.send', 'Message sent'],
};

const EmailTest = ({ formik }) => {
  const { t } = useTranslation();
  const [recipient, setRecipient] = React.useState('');
  const [loading, setLoading] = React.useState(false);
  const [result, setResult] = React.useState(null);

  const values = formik.values;
  const canTest = Boolean(values.Email_Host && values.Email_Port && values.Email_From);

  const sendTest = () => {
    setLoading(true);
    setResult(null);
    API.config.testEmail({
      Enabled: true,
      Host: values.Email_Host,
      Port: String(values.Email_Port),
      Username: values.Email_Username,
      Password: values.Email_Password,
      From: values.Email_From,
      UseTLS: Boolean(values.Email_UseTLS),
      AllowInsecureTLS: Boolean(values.Email_AllowInsecureTLS),
    }, recipient).then((response) => {
      setResult(response.data);
    }).catch((error) => {
      setResult({ success: false, steps: [], error: error.message || String(error) });
    }).finally(() => {
      setLoading(false);
    });
  };

  return (
    <Stack spacing={1.5}>
      <Stack spacing={1}>
        <InputLabel htmlFor="Email_TestRecipient">
          {t('mgmt.config.email.test.recipientLabel', 'Send a test email to')}
        </InputLabel>
        <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1.5}>
          <OutlinedInput
            id="Email_TestRecipient"
            type="email"
            fullWidth
            value={recipient}
            placeholder={values.Email_From || 'you@example.com'}
            onChange={(event) => setRecipient(event.target.value)}
            onKeyDown={(event) => {
              // This field lives inside the settings form: without this,
              // Enter would save the whole form instead of sending the test.
              if (event.key === 'Enter') {
                event.preventDefault();
                if (canTest && !loading) sendTest();
              }
            }}
          />
          <PermissionGuard permission={PERM_ADMIN}>
            <LoadingButton
              variant="outlined"
              loading={loading}
              disabled={!canTest}
              startIcon={<SendOutlined />}
              onClick={sendTest}
              sx={{ whiteSpace: 'nowrap', flexShrink: 0 }}
            >
              {t('mgmt.config.email.test.button', 'Send test email')}
            </LoadingButton>
          </PermissionGuard>
        </Stack>
        <Typography variant="caption" color="text.secondary">
          {t('mgmt.config.email.test.helperText',
            'Uses the values above as they are now, saved or not. Leave the address empty to send to the sender address.')}
        </Typography>
      </Stack>

      {result && (
        <Alert severity={result.success ? 'success' : 'error'}>
          <Stack spacing={0.5}>
            <strong>
              {result.success
                ? t('mgmt.config.email.test.success', 'Test email sent to {{recipient}}.', { recipient: result.recipient })
                : t('mgmt.config.email.test.failure', 'The test email could not be sent.')}
            </strong>
            {(result.steps || []).map((step) => {
              const [key, fallback] = STEP_LABELS[step.name] || [step.name, step.name];
              return (
                <Stack key={step.name} direction="row" spacing={1} alignItems="baseline">
                  {step.ok ? <CheckCircleOutlined /> : <CloseCircleOutlined />}
                  <span>
                    {t(key, fallback)}
                    {step.detail ? `: ${step.detail}` : ''}
                  </span>
                </Stack>
              );
            })}
            {!result.success && !(result.steps || []).length && result.error && (
              <span>{result.error}</span>
            )}
          </Stack>
        </Alert>
      )}
    </Stack>
  );
};

export default EmailTest;
