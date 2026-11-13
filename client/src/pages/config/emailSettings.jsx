// Fork addition: the email (SMTP) settings, rebuilt. The form asks for the
// address Cosmos sends from and its password, and proposes the rest: the
// server, the port with its encryption and the user name, each with where
// the proposal came from (a table of providers, the domain's DNS records,
// the usual host names checked by a real connection: src/maildetect on the
// server). Proposals fill empty fields and never replace what was entered;
// "Custom settings" opens every field at any time. "Check and send a test
// email" tries the values as they are shown, saved or not, and says what to
// do when a step fails. Saving stays the page's Save.
//
// The logic is in utils/smtpSetup.js and utils/proposals.js (tested with
// node --test); the pieces other forms can reuse are in components/:
// formField, secretInput, choiceChips, proposalBox, stepList.
import * as React from 'react';
import * as API from '../../api';
import { Alert, Button, CircularProgress, Collapse, Divider, Stack, Typography } from '@mui/material';
import { DownOutlined, SearchOutlined, UpOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { CosmosCheckbox } from './users/formShortcuts';
import { FormField, FormSelect } from '../../components/formField';
import SecretInput from '../../components/secretInput';
import ChoiceChips from '../../components/choiceChips';
import ProposalBox from '../../components/proposalBox';
import PermissionGuard from '../../components/permissionGuard';
import { PERM_ADMIN } from '../../utils/permissions';
import { useClientInfos } from '../../utils/hooks';
import { applyProposal, acceptProposal } from '../../utils/proposals';
import providers from '../../utils/smtpProviders';
import {
  MASK, splitSender, joinSender, isEmailAddress, emailDomain,
  CONNECTION_CHOICES, ENCRYPTION_CHOICES, connectionChoice, applyConnectionChoice, connectionInWords,
  fieldProblems, emailSettingsUnsaved, passwordNeededAgain,
  providerById, providerForHost, providerNotes, providerUnusable, serviceChoices, serviceToValues,
  candidateToValues, proposedCandidate, sameCandidate, candidateInUse, sourceText, checkText, nothingFoundText,
} from '../../utils/smtpSetup';
import EmailTest from './emailTest';

// the server and its port are proposed together or not at all
const UNITS = [['Email_Host', 'Email_Port', 'Email_UseTLS'], ['Email_Username']];

const EmailSettings = ({ formik, config }) => {
  const { t } = useTranslation();
  const tr = (text) => (text ? t(text[0], text[1], text[2]) : '');
  const { hasPermission } = useClientInfos();
  const mayDetect = hasPermission(PERM_ADMIN);

  const values = formik.values;
  const saved = (config && config.EmailConfig) || {};
  const sender = splitSender(values.Email_From);
  const problems = fieldProblems(values);
  // without the confirmed password the server hides the settings ("***")
  const hidden = saved.Host === MASK || values.Email_Host === MASK;

  const [custom, setCustom] = React.useState(false);
  const [otherOpen, setOtherOpen] = React.useState(false);
  const [marks, setMarks] = React.useState({}); // what proposals put into the form, see utils/proposals.js
  const [found, setFound] = React.useState(null); // { state: looking | done | failed, domain, result, error }
  const [showOthers, setShowOthers] = React.useState(false);

  // what an answer that arrives later has to work with
  const live = React.useRef({});
  live.current = { values, marks };
  const asked = React.useRef('');
  const mounted = React.useRef(true);
  React.useEffect(() => () => { mounted.current = false; }, []);

  const set = (changes) => {
    if (Object.keys(changes).length > 0) {
      formik.setValues({ ...live.current.values, ...changes });
    }
  };

  // propose fills what is free; with `take` the administrator asked for it
  const propose = (proposal, take) => {
    const now = live.current;
    if (take) {
      const out = acceptProposal(now.values, proposal);
      set(out.values);
      setMarks({ ...now.marks, ...out.marks });
      return;
    }
    const out = applyProposal(now.values, proposal, now.marks, UNITS);
    set(out.values);
    setMarks(out.marks);
  };

  const detect = (address) => {
    const domain = emailDomain(address);
    if (!domain) return;
    asked.current = domain;
    setFound({ state: 'looking', domain });
    API.config.detectEmail(domain).then((response) => {
      if (!mounted.current || asked.current !== domain) return;
      const result = response.data || { domain, candidates: [] };
      setFound({ state: 'done', domain, result });
      const candidate = proposedCandidate(result);
      if (candidate && !providerUnusable(providerById(providers, result.provider))) {
        propose(candidateToValues(candidate, splitSender(live.current.values.Email_From).address, providers));
      } else {
        // nothing to propose: an earlier proposal for another address must not stay as if it applied
        propose({ Email_Host: '', Email_Port: '', Email_UseTLS: false });
      }
    }).catch((error) => {
      if (!mounted.current || asked.current !== domain) return;
      setFound({ state: 'failed', domain, error: (error && error.message) || String(error) });
    });
  };

  const hostProvider = providerForHost(providers, values.Email_Host);

  const addressLeft = () => {
    const address = sender.address.trim();
    if (!isEmailAddress(address)) return;
    // the user name is usually the address, except at services with credentials of their own
    if (!hostProvider || hostProvider.user === 'address') {
      propose({ Email_Username: address });
    }
    const domain = emailDomain(address);
    const savedDomain = emailDomain(splitSender(saved.From).address);
    if (mayDetect && domain && domain !== asked.current && (domain !== savedDomain || !values.Email_Host)) {
      detect(address);
    }
  };

  const intro = <Alert severity="info" sx={{ '& .MuiAlert-message': { minWidth: 0 } }}>
    <Stack spacing={0.5}>
      <div>{t('mgmt.config.email.intro.title', 'Cosmos sends email for:')}</div>
      <Typography variant="body2" component="ul" sx={{ m: 0, pl: 2.5 }}>
        <li>{t('mgmt.config.email.intro.invite', 'the invitation link of a new user, and a password-reset link you send from Users')}</li>
        <li>{t('mgmt.config.email.intro.forgot', '"Forgot your password?" on the sign-in page (without email it is switched off)')}</li>
        <li>{t('mgmt.config.email.intro.alerts', 'alerts whose action is "email" (Monitoring > Alerts), to every administrator with an address')}</li>
        <li>{t('mgmt.config.email.intro.login', 'a notice at each sign-in, if switched on below')}</li>
      </Typography>
    </Stack>
  </Alert>;

  const enable = <CosmosCheckbox
    label={t('mgmt.config.email.enable', 'Send email from this server')}
    name="Email_Enabled"
    formik={formik}
  />;

  if (!values.Email_Enabled) {
    return <>{intro}{enable}</>;
  }

  if (hidden) {
    return <>
      {intro}
      {enable}
      <Alert severity="info">
        {t('mgmt.config.email.hidden', 'The mail settings are shown and changed after you confirm your password: use the padlock at the top of the page.')}
      </Alert>
    </>;
  }

  const result = found && found.state === 'done' ? found.result : null;
  const candidate = proposedCandidate(result);
  const foundProvider = result ? providerById(providers, result.provider) : null;
  const unusable = providerUnusable(foundProvider);
  const inUse = candidateInUse(candidate, values);
  const others = result ? (result.candidates || []).filter((c) => !sameCandidate(c, candidate)) : [];
  // hints: of the provider whose server the form holds, else of the one found for the address
  const hintProvider = hostProvider || (candidate && inUse ? providerById(providers, candidate.provider) : null);
  const hints = providerNotes(hintProvider);

  const unsaved = emailSettingsUnsaved(values, saved);
  const needsPassword = passwordNeededAgain(values, saved);
  const customOpen = custom || Boolean(problems.host || problems.port || problems.encryption);
  const choice = connectionChoice(values.Email_Port, values.Email_UseTLS, otherOpen);
  const domain = emailDomain(sender.address);

  const choiceDescriptions = {
    '465-tls': t('mgmt.config.email.conn.465.desc', 'Encrypted from the first moment. Offered by nearly every provider; try it first.'),
    '587-starttls': t('mgmt.config.email.conn.587.desc', 'Starts plain and is encrypted before anything is sent. The choice when 465 is not offered (Microsoft 365, iCloud).'),
    '25-starttls': t('mgmt.config.email.conn.25.desc', 'The port mail servers use among themselves. Most internet providers block it; a mail server on your own network may only listen here.'),
    other: t('mgmt.config.email.conn.other.desc', 'Another port, such as 2525, or a server without encryption.'),
  };

  const candidateLine = (c) => c.host + ', ' + tr(connectionInWords(c.port, c.security !== 'none'));

  return <>
    {intro}
    {enable}

    {/* 1. what is asked for: the address, its password, a name */}
    <FormField
      id="Email_Address"
      label={t('mgmt.config.email.address.label', 'Email address')}
      required
      type="email"
      autoComplete="off"
      inputMode="email"
      placeholder="cosmos@example.com"
      value={sender.address}
      error={problems.address && tr(problems.address)}
      helperText={t('mgmt.config.email.address.help', 'The mailbox Cosmos sends from, for example cosmos@example.com. The server settings are found from it.')}
      onChange={(text) => formik.setFieldValue('Email_From', joinSender(sender.name, text))}
      onBlur={addressLeft}
      onEnter={addressLeft}
    />

    <SecretInput
      id="Email_Password"
      label={t('mgmt.config.email.password.label', 'Password')}
      value={values.Email_Password}
      stored={saved.Password === MASK}
      error={needsPassword && t('mgmt.config.email.password.again',
        'Type the password again: the server, port, user name or encryption is no longer the saved one, and the saved password is only used for the connection it was saved for.')}
      helperText={hintProvider && (hintProvider.notes || []).includes('appPassword')
        ? t('mgmt.config.email.password.helpApp', 'An app password created for Cosmos, not the normal password of the account: see the note below.')
        : t('mgmt.config.email.password.help', 'The password of this mailbox. Leave empty for a mail server that asks for none.')}
      onChange={(text) => formik.setFieldValue('Email_Password', text)}
    />

    {hints.length > 0 && <Alert severity="info" sx={{ '& .MuiAlert-message': { minWidth: 0 } }}>
      <Stack spacing={0.75} sx={{ overflowWrap: 'anywhere' }}>
        <strong>{t('mgmt.config.email.hints.title', '{{name}}: what to know', { name: hintProvider.name })}</strong>
        {hints.map((note) => <div key={note[0]}>{tr(note)}</div>)}
      </Stack>
    </Alert>}

    <FormField
      id="Email_FromName"
      label={t('mgmt.config.email.name.label', 'Sender name (optional)')}
      value={sender.name}
      placeholder="Cosmos"
      helperText={t('mgmt.config.email.name.help', 'Shown as the sender in the mail program, for example "Cosmos at home". Empty: the address is shown.')}
      onChange={(text) => formik.setFieldValue('Email_From', joinSender(text, sender.address))}
    />

    <Divider />

    {/* 2. what was found for the address */}
    <Stack spacing={1.5}>
      <Stack direction="row" spacing={1.5} alignItems="center" useFlexGap flexWrap="wrap">
        <Typography variant="subtitle1">{t('mgmt.config.email.server.title', 'Mail server')}</Typography>
        <PermissionGuard permission={PERM_ADMIN}>
          <Button
            size="small"
            variant="outlined"
            startIcon={<SearchOutlined />}
            disabled={!domain || (found && found.state === 'looking')}
            onClick={() => detect(sender.address)}
          >
            {found ? t('mgmt.config.email.detect.again', 'Look again') : t('mgmt.config.email.detect.button', 'Find the settings')}
          </Button>
        </PermissionGuard>
      </Stack>

      {found && found.state === 'looking' && <Stack direction="row" spacing={1.5} alignItems="center" role="status">
        <CircularProgress size={16} />
        <Typography variant="body2">
          {t('mgmt.config.email.detect.looking', 'Looking for the mail servers of {{domain}}: a few seconds…', { domain: found.domain })}
        </Typography>
      </Stack>}

      {found && found.state === 'failed' && <Alert severity="warning">
        {t('mgmt.config.email.detect.failed', 'The settings could not be looked for: {{error}}. You can enter them under custom settings.', { error: found.error })}
      </Alert>}

      {result && unusable && <Alert severity="warning" sx={{ '& .MuiAlert-message': { minWidth: 0 } }}>
        <Stack spacing={0.75} sx={{ overflowWrap: 'anywhere' }}>
          <strong>{t('mgmt.config.email.detect.unusable', '{{name}} cannot be used for sending from Cosmos', { name: foundProvider.name })}</strong>
          {providerNotes(foundProvider).map((note) => <div key={note[0]}>{tr(note)}</div>)}
        </Stack>
      </Alert>}

      {result && !unusable && candidate && <ProposalBox
        severity={checkText(candidate).severity}
        title={candidateLine(candidate)}
        source={tr(sourceText(candidate, result, providers))}
        status={tr(checkText(candidate).text)}
        inUse={inUse}
        inUseText={emailSettingsUnsaved(values, saved)
          ? t('mgmt.config.email.proposal.inUse', 'These settings are in the form. Nothing is saved yet.')
          : t('mgmt.config.email.proposal.inUseSaved', 'These are the settings in the form, and they are saved.')}
        heldText={t('mgmt.config.email.proposal.held', 'The form holds other settings (saved earlier, or entered by you). They were left as they are.')}
        useLabel={t('mgmt.config.email.proposal.use', 'Use these settings')}
        onUse={() => { setOtherOpen(false); propose(candidateToValues(candidate, sender.address, providers), true); }}
      >
        {result.complete === false && <Typography variant="body2" component="div">
          {t('mgmt.config.email.proposal.incomplete', 'Time ran out before everything was checked. "Look again" tries once more.')}
        </Typography>}
      </ProposalBox>}

      {result && !unusable && !candidate && <Alert severity="warning">{tr(nothingFoundText(result))}</Alert>}

      {/* the other candidates, out of the way until asked for */}
      {result && !unusable && others.length > 0 && <div>
        <Button
          size="small"
          aria-expanded={showOthers}
          aria-controls="Email_Others"
          endIcon={showOthers ? <UpOutlined /> : <DownOutlined />}
          onClick={() => setShowOthers(!showOthers)}
        >
          {t('mgmt.config.email.proposal.others', 'Other possibilities ({{number}})', { number: others.length })}
        </Button>
        <Collapse in={showOthers} id="Email_Others" unmountOnExit>
          <Stack spacing={1} sx={{ pt: 1 }}>
            {others.map((other) => <Stack key={other.host + ':' + other.port} direction="row" spacing={1.5} alignItems="center" justifyContent="space-between">
              <Stack sx={{ minWidth: 0, overflowWrap: 'anywhere' }}>
                <Typography variant="body2">{candidateLine(other)}</Typography>
                <Typography variant="caption" color="text.secondary">
                  {tr(sourceText(other, result, providers))}{' · '}{tr(checkText(other).short)}
                </Typography>
              </Stack>
              {other.check !== 'local' && !candidateInUse(other, values) && <Button
                size="small"
                variant="outlined"
                sx={{ flexShrink: 0 }}
                onClick={() => { setOtherOpen(false); propose(candidateToValues(other, sender.address, providers), true); }}
              >
                {t('mgmt.config.email.proposal.useOther', 'Use')}
              </Button>}
            </Stack>)}
          </Stack>
        </Collapse>
      </div>}

      {/* 3. what the form holds now, and the door to every field */}
      <Stack spacing={0.5}>
        {values.Email_Host
          ? <>
            <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
              <Typography variant="body2" component="span" color="text.secondary">{t('mgmt.config.email.summary.server', 'Sends through:')}</Typography>
              {' '}{String(values.Email_Host).trim()}, {tr(connectionInWords(values.Email_Port, values.Email_UseTLS))}
            </Typography>
            <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
              <Typography variant="body2" component="span" color="text.secondary">{t('mgmt.config.email.summary.user', 'Signs in as:')}</Typography>
              {' '}{values.Email_Username || t('mgmt.config.email.summary.noUser', 'nobody (the server is asked for no sign-in)')}
            </Typography>
          </>
          : <Typography variant="body2" color="text.secondary">
            {t('mgmt.config.email.summary.none', 'No mail server yet. Enter the email address above and its settings are looked for, or open the custom settings.')}
          </Typography>}
      </Stack>

      <div>
        <Button
          size="small"
          aria-expanded={customOpen}
          aria-controls="Email_Custom"
          endIcon={customOpen ? <UpOutlined /> : <DownOutlined />}
          onClick={() => setCustom(!customOpen)}
        >
          {t('mgmt.config.email.custom.button', 'Custom settings')}
        </Button>
      </div>

      <Collapse in={customOpen} id="Email_Custom" unmountOnExit>
        <Stack spacing={2}>
          <FormSelect
            id="Email_Service"
            label={t('mgmt.config.email.service.label', 'Fill in for a known service')}
            value={hostProvider && serviceChoices(providers).some((p) => p.id === hostProvider.id) ? hostProvider.id : ''}
            emptyLabel={t('mgmt.config.email.service.none', 'None: I enter the server myself')}
            options={serviceChoices(providers).map((provider) => [provider.id, provider.name])}
            helperText={t('mgmt.config.email.service.help', 'Sets the server and the port of that service. Mail relay services (SendGrid, Brevo, Amazon SES…) are here too: they are not found from an address.')}
            onChange={(id) => {
              const provider = providerById(providers, id);
              if (provider) {
                setOtherOpen(false);
                propose(serviceToValues(provider, sender.address), true);
              }
            }}
          />

          <FormField
            id="Email_Host"
            label={t('mgmt.config.email.host.label', 'Server')}
            required
            autoComplete="off"
            placeholder="smtp.example.com"
            value={values.Email_Host}
            error={problems.host && tr(problems.host)}
            helperText={t('mgmt.config.email.host.help', 'The name providers list as "SMTP server" or "outgoing mail server", for example smtp.example.com.')}
            onChange={(text) => formik.setFieldValue('Email_Host', text)}
            onBlur={() => formik.setFieldValue('Email_Host', String(values.Email_Host || '').trim())}
          />

          <ChoiceChips
            id="Email_Connection"
            label={t('mgmt.config.email.conn.label', 'Port and encryption')}
            value={choice}
            options={CONNECTION_CHOICES.map((item) => ({ value: item.value, label: tr(item.label), description: choiceDescriptions[item.value] }))}
            onChange={(next) => {
              setOtherOpen(next === 'other');
              const pair = applyConnectionChoice(next, values.Email_Port, values.Email_UseTLS);
              set({ Email_Port: pair.Port, Email_UseTLS: pair.UseTLS });
            }}
          />

          {choice === 'other' && <>
            <FormField
              id="Email_Port"
              label={t('mgmt.config.email.port.label', 'Port')}
              required
              inputMode="numeric"
              autoComplete="off"
              placeholder="2525"
              value={values.Email_Port}
              error={problems.port && tr(problems.port)}
              helperText={t('mgmt.config.email.port.help', 'A number between 1 and 65535, for example 2525.')}
              onChange={(text) => formik.setFieldValue('Email_Port', text.trim())}
            />
            <ChoiceChips
              id="Email_Encryption"
              label={t('mgmt.config.email.enc.label', 'Encryption')}
              value={values.Email_UseTLS ? 'on' : 'off'}
              error={problems.encryption && tr(problems.encryption)}
              options={ENCRYPTION_CHOICES.map((item) => ({
                value: item.value,
                label: tr(item.label),
                description: item.value === 'on'
                  ? (String(values.Email_Port).trim() === '465'
                    ? t('mgmt.config.email.enc.on465', 'Port 465 is always encrypted from the start (TLS).')
                    : t('mgmt.config.email.enc.onDesc', 'The connection is encrypted before the password or a mail is sent. The server must offer STARTTLS.'))
                  : t('mgmt.config.email.enc.offDesc', 'The password and the mails can be read on the way. Only for a mail server on this machine or on your own network.'),
              }))}
              onChange={(next) => formik.setFieldValue('Email_UseTLS', next === 'on')}
            />
          </>}

          <FormField
            id="Email_Username"
            label={t('mgmt.config.email.user.label', 'User name')}
            autoComplete="off"
            placeholder={sender.address || 'cosmos@example.com'}
            value={values.Email_Username}
            helperText={t('mgmt.config.email.user.help', 'Usually the whole email address. Leave empty for a mail server that asks for no sign-in.')}
            onChange={(text) => formik.setFieldValue('Email_Username', text)}
          />

          {values.Email_UseTLS && <CosmosCheckbox
            label={t('mgmt.config.email.insecure.label', 'Accept a certificate that cannot be verified')}
            name="Email_AllowInsecureTLS"
            formik={formik}
            helperText={t('mgmt.config.email.insecure.help', 'Only for a mail server of your own with a self-signed certificate. With it on, Cosmos cannot tell that server from one that pretends to be it, and the password goes to whichever answers.')}
          />}
        </Stack>
      </Collapse>
    </Stack>

    <Divider />

    {/* 4. try it, then save */}
    <EmailTest formik={formik} provider={hintProvider} unsaved={unsaved} needsPassword={needsPassword} hasProblems={Object.keys(problems).length > 0} />

    {unsaved && <Typography variant="body2" color="text.secondary" role="status">
      {t('mgmt.config.email.unsaved', 'The email settings shown here are not saved yet. Press Save below to keep them.')}
    </Typography>}

    <Divider />

    <CosmosCheckbox
      label={t('mgmt.config.email.notifyLogin.label', 'Send users a notice at each sign-in')}
      name="Email_NotifyLogin"
      formik={formik}
      helperText={t('mgmt.config.email.notifyLogin.help', 'Each time someone signs in to an account, its owner gets a mail with the time and the address it came from. Users without an email address get none.')}
    />
  </>;
};

export default EmailSettings;
