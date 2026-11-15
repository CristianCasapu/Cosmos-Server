// Fork addition: why the settings on this page cannot be changed right now.
//
// Upstream showed "As you are not an admin, you can't edit the
// configuration. <br/>This page is only here for visibility." to everyone
// who could not save: the tag was shown as text (the sentence went through
// t(), which does not read tags), and an administrator whose password
// confirmation had run out was told they were not an administrator. The two
// cases are told apart here (utils/readOnly.js), and the administrator gets a
// button that asks for the password on the spot.
import * as React from 'react';
import { Alert, Button } from '@mui/material';
import { useTranslation } from 'react-i18next';
import { useClientInfos } from '../utils/hooks';
import { askForPassword } from '../utils/sudoPrompt';
import { readOnlyReason } from '../utils/readOnly';

const ReadOnlyNotice = ({ permission }) => {
  const { t } = useTranslation();
  const { hasPermission, hasRolePermission } = useClientInfos();
  const reason = readOnlyReason(hasPermission(permission), hasRolePermission(permission));
  if (reason === 'none') return null;
  if (reason === 'password') {
    return <Alert severity="info" data-read-only="password" sx={{ alignItems: 'center', '& .MuiAlert-message': { minWidth: 0 } }}
      action={<Button color="inherit" size="small" variant="outlined" onClick={() => askForPassword('button')} sx={{ minHeight: 44, whiteSpace: 'nowrap' }}>
        {t('readOnly.confirm', 'Confirm your password')}
      </Button>}>
      {t('readOnly.password', 'You can look at these settings. To change them, confirm your password first: the confirmation lasts two hours.')}
    </Alert>;
  }
  return <Alert severity="warning" data-read-only="role">
    {t('readOnly.role', 'Your account may look at these settings but not change them: that takes an administrator.')}
  </Alert>;
};

export default ReadOnlyNotice;
