// Fork addition: the dialogs of the Users page — create, edit, the link to
// pass on, and a question before the two-factor reset. Upstream's own read
// their fields from the page by id, checked nothing (a nickname with a dot
// came back as a validator's message), offered a "Guest" role the server
// silently turns into "User", and lost what was typed on a click beside
// them. These check with the server's rules while typing, say what each role
// may do and what happens after "Create", and keep their content. The seat
// counter and its limit stay where they are, in usermanagement.jsx.
import * as React from 'react';
import {
  Alert,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Grid,
  IconButton,
  InputAdornment,
  OutlinedInput,
  Stack,
  Tooltip,
  Typography,
} from '@mui/material';
import { LoadingButton } from '@mui/lab';
import { CheckOutlined, CopyOutlined } from '@ant-design/icons';
import { useFormik } from 'formik';
import { useTranslation } from 'react-i18next';
import * as API from '../../../api';
import { CosmosInputText } from './formShortcuts';
import { ChoiceChips, keepOnBackdrop } from '../../../components/formChoices';
import { useClientInfos } from '../../../utils/hooks';
import { emailProblem, inviteWay, nicknameProblem, roleChoices } from '../../../utils/userForm';

// The roles to pick from, each with what it may do. "Guest" is left out
// while it is only the page's own placeholder: the server knows no such role
// then and would silently make the account a "User".
const useRoleOptions = (roles) => {
  const { t } = useTranslation();
  const placeholderGuest = Object.keys(roles || {}).length === 3 && roles[0] === 'Guest';
  const hints = {
    user: t('mgmt.usermgmt.fork.role.user', 'Can sign in and open the apps whose address asks for a Cosmos sign-in. Cannot change anything in Cosmos.'),
    admin: t('mgmt.usermgmt.fork.role.admin', 'Can do everything you can: apps, settings, users, the terminal. Give it to people you would hand the server to.'),
    guest: t('mgmt.usermgmt.fork.role.guest', 'A role of this server with the rights set for it.'),
    other: t('mgmt.usermgmt.fork.role.other', 'A role of this server with the rights set for it.'),
  };
  return roleChoices(roles)
    .filter((role) => !(role.kind === 'guest' && placeholderGuest))
    .map((role) => ({ value: role.value, label: role.name, hint: hints[role.kind] }));
};

const nicknameMessage = (t, problem) => ({
  required: t('mgmt.usermgmt.fork.nickname.required', 'Enter a nickname: the name the person signs in with'),
  short: t('mgmt.usermgmt.fork.nickname.short', 'At least 3 characters'),
  long: t('mgmt.usermgmt.fork.nickname.long', 'At most 32 characters'),
  chars: t('mgmt.usermgmt.fork.nickname.chars', 'Letters and digits only, without spaces, dots or dashes. For example maria or maria2'),
  taken: t('mgmt.usermgmt.fork.nickname.taken', 'This nickname is taken: pick another'),
}[problem]);

const emailMessage = (t) => t('mgmt.usermgmt.fork.email.invalid', 'This is not an email address. For example maria@example.org; or leave it empty');

export const CreateUserDialog = ({ open, onClose, roles, users, onCreated }) => {
  const { t } = useTranslation();
  const roleOptions = useRoleOptions(roles);
  const [canSendEmail, setCanSendEmail] = React.useState(false);

  React.useEffect(() => {
    if (!open) {
      return undefined;
    }
    let live = true;
    API.config.canSendEmail().then((res) => live && setCanSendEmail(!!(res && res.data && res.data.canSendEmail))).catch(() => {});
    return () => { live = false; };
  }, [open]);

  const formik = useFormik({
    initialValues: { nickname: '', email: '', role: 1 },
    validate: (values) => {
      const errors = {};
      const nickname = nicknameProblem(values.nickname, (users || []).map((user) => user.nickname));
      if (nickname) {
        errors.nickname = nicknameMessage(t, nickname);
      }
      if (emailProblem(values.email)) {
        errors.email = emailMessage(t);
      }
      return errors;
    },
    onSubmit: (values, { setErrors, setSubmitting, resetForm }) => {
      const nickname = values.nickname.trim();
      return API.users.create({ nickname, email: values.email.trim(), role: values.role }).then(() => {
        setSubmitting(false);
        resetForm();
        onCreated(nickname);
      }).catch((error) => {
        // the server's reason, kept with everything that was typed
        setErrors({ submit: (error && error.message) || String(error) });
        setSubmitting(false);
      });
    },
  });

  const way = inviteWay({ email: formik.values.email, canSendEmail });

  return <Dialog open={!!open} onClose={keepOnBackdrop(onClose)} fullWidth maxWidth="xs">
    <form noValidate onSubmit={formik.handleSubmit}>
      <DialogTitle>{t('mgmt.usermgmt.createUserTitle')}</DialogTitle>
      <DialogContent>
        <Grid container spacing={2} sx={{ pt: 0.5 }}>
          <CosmosInputText
            name="nickname"
            label={t('global.nicknameLabel')}
            placeholder="maria"
            formik={formik}
            required
            autoComplete="off"
            inputProps={{ autoCapitalize: 'none', autoCorrect: 'off', spellCheck: false, maxLength: 40, autoFocus: true }}
            helperText={t('mgmt.usermgmt.fork.nickname.help', 'The name the person signs in with: 3 to 32 letters and digits.')}
          />
          <CosmosInputText
            name="email"
            type="email"
            label={t('mgmt.usermgmt.fork.email.label', 'Email address (optional)')}
            placeholder="maria@example.org"
            formik={formik}
            autoComplete="off"
            helperText={t('mgmt.usermgmt.fork.email.help', 'Used to send the invitation and password resets.')}
          />
          <ChoiceChips
            name="role"
            label={t('global.role')}
            options={roleOptions}
            value={formik.values.role}
            onChange={(role) => formik.setFieldValue('role', role)}
          />
          <Grid item xs={12}>
            <Typography variant="caption" color="textSecondary" component="div">
              {{
                mail: t('mgmt.usermgmt.fork.next.mail', 'Next: the account is created and Cosmos emails {{email}} a link to choose a password. You also get the link, in case the mail does not arrive.', { email: formik.values.email.trim() }),
                linkNoAddress: t('mgmt.usermgmt.fork.next.linkNoAddress', 'Next: the account is created and you get a link to pass on to the person; they choose their password with it.'),
                linkNoMail: t('mgmt.usermgmt.fork.next.linkNoMail', 'Next: the account is created and you get a link to pass on to the person. No email is sent: sending mail is not set up (Configuration > General > Email).'),
              }[way]}
            </Typography>
          </Grid>
          {formik.errors.submit && <Grid item xs={12}>
            <Alert severity="error" sx={{ overflowWrap: 'anywhere' }}>{formik.errors.submit}</Alert>
          </Grid>}
        </Grid>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} disabled={formik.isSubmitting}>{t('global.cancelAction')}</Button>
        <LoadingButton type="submit" loading={formik.isSubmitting}>{t('global.createAction')}</LoadingButton>
      </DialogActions>
    </form>
  </Dialog>;
};

export const EditUserDialog = ({ user, onClose, roles, onSaved }) => {
  const { t } = useTranslation();
  const roleOptions = useRoleOptions(roles);
  const { nickname: me } = useClientInfos();
  const own = !!user && String(user.nickname).toLowerCase() === String(me || '').toLowerCase();

  const formik = useFormik({
    enableReinitialize: true,
    initialValues: { email: (user && user.email) || '', role: user ? user.role : 1 },
    validate: (values) => (emailProblem(values.email) ? { email: emailMessage(t) } : {}),
    onSubmit: (values, { setErrors, setSubmitting }) => {
      const changes = {};
      if (values.role !== user.role) {
        changes.role = values.role;
      }
      // as upstream: an empty field leaves the address as it is
      if (values.email.trim() && values.email.trim() !== (user.email || '')) {
        changes.email = values.email.trim();
      }
      if (Object.keys(changes).length === 0) {
        setSubmitting(false);
        onClose();
        return undefined;
      }
      return API.users.edit(user.nickname, changes).then(() => {
        setSubmitting(false);
        onSaved();
      }).catch((error) => {
        setErrors({ submit: (error && error.message) || String(error) });
        setSubmitting(false);
      });
    },
  });

  // the role the account has stays among the choices, whatever it is
  const options = user && !roleOptions.some((option) => option.value === user.role)
    ? [...roleOptions, { value: user.role, label: (roles && roles[user.role]) || String(user.role), hint: '' }]
    : roleOptions;

  return <Dialog open={!!user} onClose={keepOnBackdrop(onClose)} fullWidth maxWidth="xs">
    {user && <form noValidate onSubmit={formik.handleSubmit}>
      <DialogTitle sx={{ overflowWrap: 'anywhere' }}>{t('mgmt.usermgmt.fork.editTitle', 'Edit {{user}}', { user: user.nickname })}</DialogTitle>
      <DialogContent>
        <Grid container spacing={2} sx={{ pt: 0.5 }}>
          <CosmosInputText
            name="email"
            type="email"
            label={t('mgmt.usermgmt.editEmail.emailInput.emailLabel')}
            placeholder="maria@example.org"
            formik={formik}
            autoComplete="off"
            helperText={t('mgmt.usermgmt.fork.email.help', 'Used to send the invitation and password resets.')}
          />
          <ChoiceChips
            name="role"
            label={t('global.role')}
            options={options}
            value={formik.values.role}
            disabled={own}
            onChange={(role) => formik.setFieldValue('role', role)}
            helperText={own ? t('mgmt.usermgmt.fork.role.own', 'This is your own account: another administrator has to change its role.') : undefined}
          />
          {formik.errors.submit && <Grid item xs={12}>
            <Alert severity="error" sx={{ overflowWrap: 'anywhere' }}>{formik.errors.submit}</Alert>
          </Grid>}
        </Grid>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} disabled={formik.isSubmitting}>{t('global.cancelAction')}</Button>
        <LoadingButton type="submit" loading={formik.isSubmitting}>{t('global.saveAction')}</LoadingButton>
      </DialogActions>
    </form>}
  </Dialog>;
};

// The link the admin passes on (an invitation or a password reset), in a
// field that fits a phone, with a copy button that says it copied.
export const UserLinkDialog = ({ action, onClose }) => {
  const { t } = useTranslation();
  const [copied, setCopied] = React.useState(false);
  const field = React.useRef(null);

  if (!action) {
    return null;
  }
  const invite = action.formType === 2;

  const copy = () => {
    const done = () => { setCopied(true); setTimeout(() => setCopied(false), 2500); };
    // the clipboard is there on HTTPS pages only: otherwise the link is selected, ready for Ctrl+C
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(action.sendLink).then(done).catch(() => field.current && field.current.select());
    } else if (field.current) {
      field.current.select();
    }
  };

  return <Dialog open onClose={keepOnBackdrop(onClose)} fullWidth maxWidth="xs">
    <DialogTitle sx={{ overflowWrap: 'anywhere' }}>{invite
      ? t('mgmt.usermgmt.fork.link.inviteTitle', 'Invitation for {{user}}', { user: action.nickname })
      : t('mgmt.usermgmt.fork.link.resetTitle', 'Password reset for {{user}}', { user: action.nickname })}</DialogTitle>
    <DialogContent>
      <Stack spacing={1.5}>
        {action.emailError && <Alert severity="warning" sx={{ overflowWrap: 'anywhere' }}>
          {t('mgmt.usermgmt.inviteUser.emailFailed', 'The email could not be sent: {{reason}}', { reason: action.emailError })}
        </Alert>}
        <Typography variant="body2">
          {action.emailWasSent
            ? t('mgmt.usermgmt.fork.link.sent', 'An email with this link has been sent. You can also pass the link on yourself:')
            : (invite
              ? t('mgmt.usermgmt.fork.link.passInvite', 'No email was sent. Pass this link on to {{user}}: with it they choose their password and can sign in.', { user: action.nickname })
              : t('mgmt.usermgmt.fork.link.passReset', 'No email was sent. Pass this link on to {{user}}: with it they choose a new password.', { user: action.nickname }))}
        </Typography>
        <OutlinedInput
          fullWidth
          readOnly
          size="small"
          value={action.sendLink}
          inputRef={field}
          inputProps={{ 'aria-label': t('mgmt.usermgmt.fork.link.label', 'Link'), onFocus: (event) => event.target.select() }}
          endAdornment={<InputAdornment position="end">
            <Tooltip title={copied ? t('global.copied', 'Copied') : t('global.copy', 'Copy')}>
              <IconButton edge="end" onClick={copy} aria-label={t('global.copy', 'Copy')}>
                {copied ? <CheckOutlined /> : <CopyOutlined />}
              </IconButton>
            </Tooltip>
          </InputAdornment>}
        />
        <Typography variant="caption" color="textSecondary" role="status">
          {copied
            ? t('mgmt.usermgmt.fork.link.copied', 'Copied. Whoever has the link can set the password of this account: send it to that person only.')
            : t('mgmt.usermgmt.fork.link.care', 'Whoever has the link can set the password of this account: send it to that person only.')}
        </Typography>
      </Stack>
    </DialogContent>
    <DialogActions>
      <Button onClick={onClose}>{t('global.close')}</Button>
    </DialogActions>
  </Dialog>;
};

// Asked before a two-factor reset: upstream reset it on the click, one
// button away from "Delete".
export const Reset2FADialog = ({ nickname, onClose, onDone }) => {
  const { t } = useTranslation();
  const [busy, setBusy] = React.useState(false);
  if (!nickname) {
    return null;
  }
  const reset = () => {
    setBusy(true);
    API.users.reset2FA(nickname).then(() => { setBusy(false); onDone(); }).catch(() => { setBusy(false); onClose(); });
  };
  return <Dialog open onClose={busy ? undefined : onClose} fullWidth maxWidth="xs">
    <DialogTitle sx={{ overflowWrap: 'anywhere' }}>{t('mgmt.usermgmt.fork.reset2fa.title', 'Reset the two-factor sign-in of {{user}}?', { user: nickname })}</DialogTitle>
    <DialogContent>
      <Typography variant="body2">
        {t('mgmt.usermgmt.fork.reset2fa.text',
          'Their authenticator app stops working for this account, and they set up two-factor sign-in again the next time they sign in. Do this when the person lost their phone, not otherwise: until they sign in again, the password alone protects the account.')}
      </Typography>
    </DialogContent>
    <DialogActions>
      <Button onClick={onClose} disabled={busy}>{t('global.cancelAction')}</Button>
      <LoadingButton color="error" loading={busy} onClick={reset}>{t('mgmt.usermgmt.reset2faButton')}</LoadingButton>
    </DialogActions>
  </Dialog>;
};
