// Fork addition (plan 45): "Who signs in with what" in the one review of every
// installation: per app, the username or e-mail, whether Cosmos generated the
// password here (with Copy), for an app that makes its first account in its
// own page what to type there, and where the values are kept. The lines come
// from utils/signIn.js (proposeSignIn); generated values live only in the
// review's state and in the definition sent with Create.
import * as React from 'react';
import { Alert, Button, Stack, Typography } from '@mui/material';
import { CopyOutlined, KeyOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { classesOf } from '../utils/signIn';

const wrap = { overflowWrap: 'anywhere', minWidth: 0, '& .MuiAlert-message': { minWidth: 0 } };
const mono = { fontFamily: 'monospace', overflowWrap: 'anywhere' };

const CopyButton = ({ value, label }) => {
  const { t } = useTranslation();
  const [copied, setCopied] = React.useState(false);
  return <Button size="small" variant="outlined" startIcon={<CopyOutlined />} sx={{ flexShrink: 0, '&&&': { minHeight: { xs: 44, sm: 0 } } }} data-signin-copy
    aria-label={label}
    onClick={() => {
      try {
        navigator.clipboard.writeText(value).then(() => setCopied(true), () => setCopied(false));
      } catch (e) { /* no clipboard: the value is on screen */ }
    }}>
    {copied ? t('mgmt.servapps.signIn.review.copied', 'Copied') : t('mgmt.servapps.signIn.review.copy', 'Copy')}
  </Button>;
};

const REASONS = {
  lower: ['mgmt.servapps.signIn.review.reason.lower', 'it takes lower-case letters only'],
  chars: ['mgmt.servapps.signIn.review.reason.chars', 'it takes letters, digits and _ only'],
  long: ['mgmt.servapps.signIn.review.reason.long', 'it takes shorter names'],
  reserved: ['mgmt.servapps.signIn.review.reason.reserved', 'that name is its own administrator\'s'],
};

const WHERE = {
  database: ['mgmt.servapps.signIn.review.where.database', 'with a database client or a web manager'],
  web: ['mgmt.servapps.signIn.review.where.web', 'in its web page'],
  s3: ['mgmt.servapps.signIn.review.where.s3', 'with an S3 client, and in its console'],
  amqp: ['mgmt.servapps.signIn.review.where.amqp', 'from apps, and in its management page'],
  http: ['mgmt.servapps.signIn.review.where.http', 'through its API'],
};

const kindsText = (t, rule) => {
  const symbols = classesOf(rule).find(([name]) => name === 'symbol');
  return symbols
    ? t('mgmt.servapps.signIn.review.kindsSymbols', 'letters, digits and {{symbols}}', { symbols: symbols[1].split('').join(' ') })
    : t('mgmt.servapps.signIn.review.kindsPlain', 'letters and digits');
};

const AccountLine = ({ line, rule }) => {
  const { t } = useTranslation();
  const who = line.email
    ? t('mgmt.servapps.signIn.review.asEmail', 'the e-mail {{email}}', { email: line.email, interpolation: { escapeValue: false } })
    : line.user ? t('mgmt.servapps.signIn.review.asUser', 'the username {{user}}', { user: line.user, interpolation: { escapeValue: false } }) : '';
  const where = WHERE[line.where] ? t(WHERE[line.where][0], WHERE[line.where][1]) : '';
  return <Stack spacing={0.5} sx={{ minWidth: 0 }} data-signin-line={line.service}>
    <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>
      <KeyOutlined style={{ marginRight: 6 }} />
      {line.role === 'root' || line.role === 'admin'
        ? t('mgmt.servapps.signIn.review.adminAccount', '{{service}}, its {{role}} account: {{who}}', { service: line.service, role: line.role, who, interpolation: { escapeValue: false } })
        : t('mgmt.servapps.signIn.review.account', '{{service}}: sign in {{where}} with {{who}}', { service: line.service, where, who: who || t('mgmt.servapps.signIn.review.itsPassword', 'its password'), interpolation: { escapeValue: false } })}
      {line.database ? t('mgmt.servapps.signIn.review.database', ', database {{database}}', { database: line.database, interpolation: { escapeValue: false } }) : ''}.
      {line.userChanged && REASONS[line.userChanged] ? ' ' + t('mgmt.servapps.signIn.review.variant', 'Your username {{from}} is {{user}} here: {{reason}}.', { from: line.userFrom, user: line.user, reason: t(REASONS[line.userChanged][0], REASONS[line.userChanged][1]), interpolation: { escapeValue: false } }) : ''}
      {line.source === 'guess' ? ' ' + t('mgmt.servapps.signIn.review.guess', 'Cosmos guessed this from the names of its settings ({{name}}): check it in the app\'s documentation.', { name: line.passwordVar, interpolation: { escapeValue: false } }) : ''}
    </Typography>
    {line.passwordVar && <Stack direction="row" spacing={1} sx={{ alignItems: 'center', minWidth: 0, flexWrap: 'wrap' }} useFlexGap>
      <Typography variant="caption" sx={{ color: 'text.secondary', overflowWrap: 'anywhere' }}>
        {line.generated
          ? t('mgmt.servapps.signIn.review.generated', 'Password ({{name}}): generated now, {{length}} characters: {{kinds}}.', { name: line.passwordVar, length: String(line.value || '').length, kinds: kindsText(t, rule), interpolation: { escapeValue: false } })
          : t('mgmt.servapps.signIn.review.setAbove', 'Password ({{name}}): as set above.', { name: line.passwordVar, interpolation: { escapeValue: false } })}
      </Typography>
      {line.generated && line.value && <CopyButton value={line.value} label={t('mgmt.servapps.signIn.review.copyPassword', 'Copy the password of {{service}}', { service: line.service })} />}
    </Stack>}
  </Stack>;
};

const FirstRunLine = ({ line }) => {
  const { t } = useTranslation();
  if (line.note === 'logPassword') {
    return <Alert severity="info" sx={wrap} data-signin-firstrun={line.service}>
      {t('mgmt.servapps.signIn.review.logPassword', '{{app}} ({{service}}) prints a temporary password in its log the first time it starts, for the username {{user}}: open its page, then Logs, sign in with it and choose your own. Cosmos cannot set it.', { app: line.app || line.service, service: line.service, user: line.user || 'admin', interpolation: { escapeValue: false } })}
    </Alert>;
  }
  return <Alert severity="info" sx={wrap} data-signin-firstrun={line.service}>
    <Stack spacing={1}>
      <div>{t('mgmt.servapps.signIn.review.firstRun', '{{app}} ({{service}}) makes its first account in its own page, the first time you open it: Cosmos cannot set it. Type these there, or your own:', { app: line.app || line.service, service: line.service, interpolation: { escapeValue: false } })}</div>
      {line.user && <Stack direction="row" spacing={1} sx={{ alignItems: 'center', flexWrap: 'wrap' }} useFlexGap>
        <Typography variant="body2">{t('mgmt.servapps.signIn.review.username', 'Username')}:</Typography>
        <Typography variant="body2" sx={mono} data-signin-first-user>{line.user}</Typography>
        <CopyButton value={line.user} label={t('mgmt.servapps.signIn.review.copyUser', 'Copy the username')} />
      </Stack>}
      {line.value && <Stack direction="row" spacing={1} sx={{ alignItems: 'center', flexWrap: 'wrap', minWidth: 0 }} useFlexGap>
        <Typography variant="body2">{t('mgmt.servapps.signIn.review.password', 'Password')}:</Typography>
        <Typography variant="body2" sx={mono} data-signin-first-password>{line.value}</Typography>
        <CopyButton value={line.value} label={t('mgmt.servapps.signIn.review.copyPasswordFirst', 'Copy the proposed password')} />
      </Stack>}
      <div>{t('mgmt.servapps.signIn.review.notKept', 'Cosmos does not keep this password and cannot show it again: keep it in your password manager. The username stays on the app\'s page.')}</div>
    </Stack>
  </Alert>;
};

const MAIL = {
  off: ['mgmt.servapps.signIn.review.mail.off', 'No e-mail is sent about it: that is switched off in Configuration > General.'],
  noMail: ['mgmt.servapps.signIn.review.mail.noMail', 'No e-mail is sent about it: Cosmos has no e-mail settings.'],
  noAddress: ['mgmt.servapps.signIn.review.mail.noAddress', 'No e-mail is sent about it: your account has no e-mail address.'],
  token: ['mgmt.servapps.signIn.review.mail.token', 'No e-mail is sent about it: an API token has no e-mail address.'],
};

// `lines`: proposeSignIn's (each with its password rule); `mail`: the defaults' mail state.
const SignInReview = ({ lines, mail }) => {
  const { t } = useTranslation();
  if (!(lines || []).length) return null;
  return <Stack spacing={1} sx={{ minWidth: 0 }} data-review-signin>
    <Typography variant="subtitle1">{t('mgmt.servapps.signIn.review.title', 'Who signs in with what')}</Typography>
    {lines.map((line, i) => (line.kind === 'firstRun'
      ? <FirstRunLine key={i} line={line} />
      : <AccountLine key={i} line={line} rule={line.rule} />))}
    {lines.some((l) => l.kind !== 'firstRun') && <Typography variant="caption" sx={{ color: 'text.secondary', overflowWrap: 'anywhere' }}>
      {t('mgmt.servapps.signIn.review.kept', 'The passwords are kept only in the app\'s own settings (and so in Cosmos\' backup of container definitions). After the creation they are on its page, under Sign-in details, for those who may read credentials, after the password confirmation.')}
    </Typography>}
    {mail && <Typography variant="caption" sx={{ color: 'text.secondary', overflowWrap: 'anywhere' }} data-review-signin-mail>
      {mail.ready
        ? t('mgmt.servapps.signIn.review.mail.ready', 'An e-mail with the app\'s name, address and username (never a password) goes to your address once it is created.')
        : MAIL[mail.why] ? t(MAIL[mail.why][0], MAIL[mail.why][1]) : ''}
    </Typography>}
  </Stack>;
};

export default SignInReview;
