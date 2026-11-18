// Fork addition (plan 47): the wizard's first screen asks for the one-time
// setup code of this server (src/fork_setup_code.go). It is checked with the
// server through the wizard's harmless step 2; once accepted it is kept for
// the rest of the wizard (utils/setupCode.js) and sent with every request.
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Grid, Stack, Typography } from '@mui/material';
import { LockOutlined } from '@ant-design/icons';

import { CosmosInputText } from '../config/users/formShortcuts';
import AnimateButton from '../../components/@extended/AnimateButton';
import { LanguagesSelect } from '../../layout/MainLayout/Drawer/languages';
import { SETUP_CODE_HEADER, keepSetupCode, normalizeSetupCode, setupCodeAnswer, setupCodeProblem } from '../../utils/setupCode';

const SetupCodeGate = ({ configFolder, onAccepted, reason }) => {
  const { t } = useTranslation();
  const [typed, setTyped] = useState('');
  const [problem, setProblem] = useState(reason || null); // reason: why the wizard asks again
  const [checking, setChecking] = useState(false);
  const file = (configFolder ? String(configFolder).replace(/\/+$/, '') : '/var/lib/cosmos') + '/setup-code';

  const check = async (event) => {
    event.preventDefault();
    const local = setupCodeProblem(typed);
    if (local) { setProblem([local[0], local[1], {}]); return; }
    const code = normalizeSetupCode(typed);
    setChecking(true);
    setProblem(null);
    let status = 0, answer = {}, retryAfter = null;
    try {
      const response = await fetch('/cosmos/api/newInstall', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', [SETUP_CODE_HEADER]: code },
        body: JSON.stringify({ step: '2' }),
      });
      status = response.status;
      retryAfter = response.headers.get('Retry-After');
      try { answer = await response.json(); } catch { answer = {}; }
    } catch { status = 0; }
    setChecking(false);
    if (status === 200 && answer.status === 'OK') {
      keepSetupCode(code);
      onAccepted();
      return;
    }
    setProblem(setupCodeAnswer(status, answer.code, retryAfter, answer.message));
  };

  return <Grid container spacing={3}>
    <Grid size={{ xs: 12 }}>
      <Typography variant="h3"><LockOutlined /> {t('newInstall.setupCode.title', 'Setup code')}</Typography>
    </Grid>
    <Grid size={{ xs: 12 }}>
      <form noValidate onSubmit={check} data-fork-setup-code>
        <Stack spacing={2}>
          <span><LanguagesSelect /></span>
          <div>{t('newInstall.setupCode.text', 'To make sure that you, and nobody else on the network, set this server up, enter its setup code. install.sh printed it when it finished. On the server, it is shown by:')}</div>
          <Typography component="div" variant="body2" sx={{ fontFamily: 'monospace', wordBreak: 'break-all' }}>
            sudo cat {file}<br />
            sudo journalctl -u CosmosCloud | grep -A1 'SETUP CODE'
          </Typography>
          <CosmosInputText
            name="setupCode"
            label={t('newInstall.setupCode.label', 'Setup code')}
            placeholder="XXXX-XXXX-XXXX-XXXX-XXXX"
            value={typed}
            autoComplete="off"
            inputProps={{ spellCheck: false, autoCapitalize: 'characters', maxLength: 64, 'aria-describedby': problem ? 'setup-code-problem' : undefined }}
            onChange={(e) => { setTyped(e.target.value); setProblem(null); }}
          />
          {problem && <Alert severity="error" id="setup-code-problem">{t(problem[0], problem[1], problem[2])}</Alert>}
          <AnimateButton>
            <Button type="submit" variant="contained" color="primary" fullWidth disabled={checking}>
              {checking ? t('newInstall.setupCode.checking', 'Checking...') : t('newInstall.setupCode.action', 'Continue')}
            </Button>
          </AnimateButton>
        </Stack>
      </form>
    </Grid>
  </Grid>;
};

export default SetupCodeGate;
