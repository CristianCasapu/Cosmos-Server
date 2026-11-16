// Fork addition (plan 45): Configuration > General > "Sign-in details for new
// apps". The username and e-mail every installation proposes to an app's own
// account (empty: the installing administrator's own Cosmos name and e-mail,
// shown as the placeholder), the e-mail sent when an app is installed, and
// the length of generated passwords. Never a password: those are generated
// in the browser at each installation, following each app's rules. Part of
// the main settings form (Config.AppSignIn, src/utils/fork_app_signin.go).
import * as React from 'react';
import { Alert, Grid, Typography } from '@mui/material';
import { useTranslation } from 'react-i18next';
import * as Yup from 'yup';
import MainCard from '../../components/MainCard';
import * as API from '../../api';
import { CosmosCheckbox, CosmosInputText } from './users/formShortcuts';

// The form's values from the configuration, and back.
export const appSignInValues = (config) => {
  const s = (config && config.AppSignIn) || {};
  return { AppSignInUser: s.User || '', AppSignInEmail: s.Email || '', AppSignInMail: !s.MailOff, AppSignInLength: s.Length ? String(s.Length) : '' };
};

export const appSignInConfig = (values) => ({
  User: String(values.AppSignInUser || '').trim(),
  Email: String(values.AppSignInEmail || '').trim(),
  MailOff: !values.AppSignInMail,
  Length: Number(values.AppSignInLength) || 0,
});

// The form's checks (the server checks the same again on save).
export const appSignInValidation = (t) => ({
  AppSignInUser: Yup.string().trim().max(64, t('mgmt.config.appSignIn.userLong', 'At most 64 characters.')),
  AppSignInEmail: Yup.string().trim().matches(/^(\*\*\*|[^\s@<>()",;:]+@[^\s@<>()",;:]+\.[^\s@<>()",;:]+)$/, {
    message: t('mgmt.config.appSignIn.emailBad', 'This is not an e-mail address.'), excludeEmptyString: true,
  }),
  AppSignInLength: Yup.string().trim().matches(/^(1[6-9]|[2-5]\d|6[0-4])$/, {
    message: t('mgmt.config.appSignIn.lengthBad', 'Between 16 and 64, or empty for 24.'), excludeEmptyString: true,
  }),
});

const AppSignIn = ({ formik }) => {
  const { t } = useTranslation();
  const [mine, setMine] = React.useState(null);
  React.useEffect(() => {
    if (!API.signIn) return;
    API.signIn.defaults().then((res) => setMine((res && res.data) || null)).catch(() => {});
  }, []);
  const why = mine && mine.mail && !mine.mail.ready && mine.mail.why;
  return <MainCard title={t('mgmt.config.appSignIn.title', 'Sign-in details for new apps')}>
    <Grid container spacing={3} data-config-app-signin>
      <Grid size={{ xs: 12 }}>
        <Typography variant="body2" sx={{ color: 'text.secondary', overflowWrap: 'anywhere' }}>
          {t('mgmt.config.appSignIn.intro', 'Every installation proposes these to an app that asks for a username or an e-mail address, in the form the app takes (the review says when it changed one), and a password generated for it that follows the app\'s rules. Passwords are never kept here: each app keeps its own, shown on its page under Sign-in details.')}
        </Typography>
      </Grid>
      <CosmosInputText name="AppSignInUser" formik={formik} label={t('mgmt.config.appSignIn.user', 'Username for new apps')}
        placeholder={mine && mine.userFrom === 'account' ? mine.user : t('mgmt.config.appSignIn.userEmpty', 'Your Cosmos name')}
        helperText={t('mgmt.config.appSignIn.userHelp', 'Empty: the Cosmos name of the administrator who installs the app.')} autoComplete="off" />
      <CosmosInputText name="AppSignInEmail" formik={formik} label={t('mgmt.config.appSignIn.email', 'E-mail address for new apps')}
        placeholder={mine && mine.emailFrom === 'account' ? mine.email : t('mgmt.config.appSignIn.emailEmpty', 'Your Cosmos e-mail address')}
        helperText={t('mgmt.config.appSignIn.emailHelp', 'Empty: the e-mail address of the administrator who installs the app. Used by apps that sign in with an e-mail (pgAdmin, for one).')} autoComplete="off" />
      <CosmosInputText name="AppSignInLength" formik={formik} label={t('mgmt.config.appSignIn.length', 'Length of generated passwords')} placeholder="24"
        helperText={t('mgmt.config.appSignIn.lengthHelp', 'Between 16 and 64 characters; empty for 24. An app with a shorter limit gets its limit.')} />
      <CosmosCheckbox name="AppSignInMail" formik={formik}
        label={t('mgmt.config.appSignIn.mail', 'Send me an e-mail when an app is installed (its name, address and username, and a link to its sign-in details; never a password)')} />
      {formik.values.AppSignInMail && why && why !== 'off' && <Grid size={{ xs: 12 }}>
        <Alert severity="info" sx={{ overflowWrap: 'anywhere' }}>
          {why === 'noMail'
            ? t('mgmt.config.appSignIn.noMail', 'Cosmos has no e-mail settings yet (below): until then no e-mail is sent; the notification is.')
            : why === 'noAddress'
              ? t('mgmt.config.appSignIn.noAddress', 'Your account has no e-mail address: add one under Users to receive these e-mails.')
              : ''}
        </Alert>
      </Grid>}
    </Grid>
  </MainCard>;
};

export default AppSignIn;
