import { Link, useSearchParams } from 'react-router';

// material-ui
import { Checkbox, Grid, Stack, Typography } from '@mui/material';

// project import
import AuthLogin from './auth-forms/AuthLogin';
import AuthWrapper from './AuthWrapper';
import { getOpenIDClientFaviconURL } from '../../utils/routes';
import { Button as LoadingButton } from '@mui/material'; // Fork addition: MUI 9 has no LoadingButton (Button has the loading state since 6.4)
import { Field, useFormik } from 'formik';
import { useEffect, useRef, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';

// ================================|| LOGIN ||================================ //

const OpenID = () => {
  const { t } = useTranslation();
  const [searchParams, setSearchParams] = useSearchParams();
  const client_id = searchParams.get("client_id")
  const redirect_uri = searchParams.get("redirect_uri")
  const scope = searchParams.get("scope")
  const entireSearch = searchParams.toString()
  const [checkedScopes, setCheckedScopes] = useState(() => [...new Set([...(scope ? scope.split(' ') : []), "openid"])])
  const formRef = useRef(null)

  // if only 'account' as scope, auto redirect
  useEffect(() => {
    if (scope == "openid" && formRef && formRef.current) {
      formRef.current.submit();
    }
  }, [formRef]);
  

  const icon = getOpenIDClientFaviconURL(client_id);

  const selfport = new URL(window.location.href).port
  const selfprotocol = new URL(window.location.href).protocol + "//"
  const selfHostname = selfprotocol + (new URL(window.location.href).hostname) + (selfport ? ":" + selfport : "")

  const onchange = (e, scope) => {
    if (e.target.checked) {
      setCheckedScopes([...new Set([...checkedScopes, scope])])
    } else {
      setCheckedScopes(checkedScopes.filter((s) => s != scope))
    }
  }

  return (<AuthWrapper>
    <Grid container spacing={3}>
      <Grid size={{ xs: 12 }}>
        <Stack spacing={2}>
          <Typography variant="h3">{t('oidc.title', {client_id: client_id})}</Typography>
          <Stack direction="row" sx={{ justifyContent: 'space-between', alignItems: 'baseline' }} spacing={2} style={{
            alignItems: 'center',
          }}>
            <img src={icon} alt={'icon'} width="64px" />
            <div>
              {/* Fork addition: the client's name from the address is shown as text, never parsed as markup */}
              {t('oidc.signInTo', 'You are about to sign in to')} <strong>{client_id}</strong>.<br />
              {t('oidc.checkPermissions', 'Check which permissions you are giving to this application.')}
            </div>
          </Stack>
        </Stack>
      </Grid>
      <Grid size={{ xs: 12 }}>
			  <link rel="openid2.provider openid.server" href={selfHostname + "/oauth2/auth"} />
        <form action={"/oauth2/auth?" + entireSearch} method="post" ref={formRef}>
          <input type="hidden" name="client_id" value={client_id} />
          {[...new Set(scope ? scope.split(' ') : [])].map((scope) => {
            // Fork addition: each permission's name labels its checkbox
            return scope == "openid" ? <div>
              <input type="checkbox" name="scopes" value={scope} checked hidden />
              <label style={{ display: 'inline-flex', alignItems: 'center' }}><Checkbox checked disabled />
              {t('oidc.account')}</label>
            </div>
              : <div>
                <input type="checkbox" name="scopes" hidden value={scope} checked={checkedScopes.includes(scope)} readOnly />{/* Fork addition: readOnly (React warns of a checked field without onChange) */}
                <label style={{ display: 'inline-flex', alignItems: 'center', cursor: 'pointer' }}><Checkbox checked={checkedScopes.includes(scope)} onChange={(e) => onchange(e, scope)} />
                {scope}</label>
              </div>
          })}
          <div style={{
            fontSize: '0.8125rem', /* Fork addition: the skin's small size, its secondary colour instead of a faded italic */
            marginTop: '15px',
            marginBottom: '20px',
            color: 'var(--ct-muted)',
          }}>
            {/* Fork addition: was <Trans i18nkey=…> (lower-case k: nothing was shown); the address is text, never markup */}
            {t('oidc.sentBackTo', 'You will be sent back to')} <strong style={{ overflowWrap: 'anywhere' }}>{redirect_uri}</strong> {t('oidc.afterSignIn', 'after signing in.')}
          </div>

          <LoadingButton
            disableElevation
            fullWidth
            size="large"
            type="submit"
            variant="contained"
            color="primary"
          >
            {t('oidc.loginTitle')}
          </LoadingButton>
        </form>
      </Grid>
    </Grid>
  </AuthWrapper>)
};

export default OpenID;
